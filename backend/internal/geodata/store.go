// Package geodata — базы geoip.dat и geosite.dat в формате v2ray/xray.
//
// Ради них правило может назвать не адрес и не имя, а целый набор:
// geoip:ru — все сети России, geosite:youtube — все имена YouTube. Сами
// базы собирают другие (v2fly, runetfreedom), клиент их только скачивает и
// читает.
//
// Файлы лежат в каталоге состояния службы. Откуда их брать, помнится там же
// (geo.json), и устаревшие базы обновляются сами — см. AutoUpdate.
package geodata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Kind — вид базы.
type Kind string

const (
	IP   Kind = "geoip"
	Site Kind = "geosite"
)

// Kinds перечисляет виды баз.
var Kinds = []Kind{IP, Site}

// DefaultURLs — откуда базы берутся, пока пользователь не указал своё.
// Сборка runetfreedom: категории v2fly плюс списки заблокированного в России
// (geosite:ru-blocked, geoip:ru-blocked) — ради них клиент обычно и ставят.
var DefaultURLs = map[Kind]string{
	IP:   "https://github.com/runetfreedom/russia-v2ray-rules-dat/releases/latest/download/geoip.dat",
	Site: "https://github.com/runetfreedom/russia-v2ray-rules-dat/releases/latest/download/geosite.dat",
}

const (
	// maxFileSize — предел на скачиваемый и загружаемый файл. Самые
	// полные сборки — десятки мегабайт; больше — уже не база, а ошибка.
	maxFileSize = 256 << 20

	// downloadTimeout — предел на одно скачивание целиком.
	downloadTimeout = 5 * time.Minute

	// staleAfter — с какого возраста база обновляется сама. Сборки выходят
	// раз в сутки-неделю; гонять десятки мегабайт чаще незачем.
	staleAfter = 7 * 24 * time.Hour

	// checkEvery — как часто проверяем возраст баз.
	checkEvery = 6 * time.Hour
)

// Info — состояние одной базы для интерфейса.
type Info struct {
	Present  bool      `json:"present"`
	Size     int64     `json:"size,omitempty"`
	Updated  time.Time `json:"updated,omitempty"`
	URL      string    `json:"url"`
	Updating bool      `json:"updating,omitempty"`
	Error    string    `json:"error,omitempty"`
	Tags     []Tag     `json:"tags"`
}

// Store — обе базы и всё, что из них уже прочитано.
// Безопасен для одновременного использования.
type Store struct {
	dir    string
	client *http.Client

	mu       sync.Mutex
	files    map[Kind]*file
	urls     map[Kind]string
	errs     map[Kind]string
	updating map[Kind]bool

	// Прочитанные категории. Правила перечитываются при каждой правке
	// списка, а разбор geosite:ru-blocked — это сотни тысяч записей.
	ipCache   map[string][]netip.Prefix
	siteCache map[string][]Domain
}

type meta struct {
	URLs map[Kind]string `json:"urls"`
}

// Open открывает базы в каталоге dir. Отсутствие файлов — не ошибка: базы
// нужны только правилам, которые на них ссылаются.
func Open(dir string) *Store {
	s := &Store{
		dir:       dir,
		client:    &http.Client{Timeout: downloadTimeout},
		files:     make(map[Kind]*file),
		urls:      make(map[Kind]string),
		errs:      make(map[Kind]string),
		updating:  make(map[Kind]bool),
		ipCache:   make(map[string][]netip.Prefix),
		siteCache: make(map[string][]Domain),
	}

	if data, err := os.ReadFile(s.metaPath()); err == nil {
		var m meta
		if err := json.Unmarshal(data, &m); err == nil {
			for k, u := range m.URLs {
				s.urls[k] = u
			}
		}
	}

	for _, kind := range Kinds {
		path := s.path(kind)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		f, err := indexFile(path, kind)
		if err != nil {
			log.Printf("База %s не прочитана: %v", kind, err)
			s.errs[kind] = err.Error()
			continue
		}
		s.files[kind] = f
		log.Printf("База %s: категорий %d", kind, len(f.tags))
	}

	return s
}

func (s *Store) path(kind Kind) string { return filepath.Join(s.dir, string(kind)+".dat") }
func (s *Store) metaPath() string      { return filepath.Join(s.dir, "geo.json") }

// URL — откуда берётся база.
func (s *Store) URL(kind Kind) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.urlLocked(kind)
}

func (s *Store) urlLocked(kind Kind) string {
	if u := s.urls[kind]; u != "" {
		return u
	}
	return DefaultURLs[kind]
}

// Info описывает обе базы.
func (s *Store) Info() map[Kind]Info {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[Kind]Info, len(Kinds))
	for _, kind := range Kinds {
		info := Info{
			URL:      s.urlLocked(kind),
			Updating: s.updating[kind],
			Error:    s.errs[kind],
			Tags:     []Tag{},
		}
		if f := s.files[kind]; f != nil {
			info.Present = true
			info.Size = f.size
			info.Updated = f.modTime
			info.Tags = f.tags
		}
		out[kind] = info
	}
	return out
}

// Check отвечает, годится ли ссылка на категорию: есть ли база и есть ли в
// ней такая категория. Причина отказа — для человека.
func (s *Store) Check(kind Kind, tag string) error {
	name, _ := splitTag(tag)

	s.mu.Lock()
	f := s.files[kind]
	s.mu.Unlock()

	if f == nil {
		return fmt.Errorf("база %s.dat не загружена — скачайте её внизу панели", kind)
	}
	if _, ok := f.entries[name]; !ok {
		return fmt.Errorf("в %s.dat нет категории %q", kind, name)
	}
	return nil
}

// IPs возвращает подсети категории geoip, уже сведённые.
func (s *Store) IPs(code string) ([]netip.Prefix, error) {
	code = strings.ToLower(strings.TrimSpace(code))

	s.mu.Lock()
	if cached, ok := s.ipCache[code]; ok {
		s.mu.Unlock()
		return cached, nil
	}
	f := s.files[IP]
	s.mu.Unlock()

	if f == nil {
		return nil, errors.New("база geoip.dat не загружена")
	}

	data, found, err := f.raw(code)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("в geoip.dat нет категории %q", code)
	}

	prefixes, err := decodeCIDRs(data)
	if err != nil {
		return nil, err
	}
	prefixes = Aggregate(prefixes)

	s.mu.Lock()
	if s.files[IP] == f {
		s.ipCache[code] = prefixes
	}
	s.mu.Unlock()

	return prefixes, nil
}

// Sites возвращает записи категории geosite. tag — имя категории и, через
// @, атрибуты, которые обязана нести запись: google@cn.
func (s *Store) Sites(tag string) ([]Domain, error) {
	tag = strings.ToLower(strings.TrimSpace(tag))

	s.mu.Lock()
	if cached, ok := s.siteCache[tag]; ok {
		s.mu.Unlock()
		return cached, nil
	}
	f := s.files[Site]
	s.mu.Unlock()

	if f == nil {
		return nil, errors.New("база geosite.dat не загружена")
	}

	name, attrs := splitTag(tag)

	data, found, err := f.raw(name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("в geosite.dat нет категории %q", name)
	}

	domains, err := decodeDomains(data, attrs)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.files[Site] == f {
		s.siteCache[tag] = domains
	}
	s.mu.Unlock()

	return domains, nil
}

func splitTag(tag string) (string, []string) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(tag)), "@")
	return parts[0], parts[1:]
}

// Download скачивает базу. Пустой url — с того адреса, что уже записан;
// непустой после удачного скачивания запоминается как новый адрес базы.
func (s *Store) Download(ctx context.Context, kind Kind, url string) error {
	if !validKind(kind) {
		return fmt.Errorf("неизвестная база %q", kind)
	}

	url = strings.TrimSpace(url)
	if url != "" && !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return errors.New("адрес базы должен начинаться с https://")
	}
	if url == "" {
		url = s.URL(kind)
	}

	if !s.begin(kind) {
		return errors.New("база уже обновляется")
	}

	err := s.download(ctx, kind, url)
	s.finish(kind, err)
	if err != nil {
		return err
	}

	// Адрес запоминается, только когда с него действительно скачалось:
	// опечатка в адресе не должна ломать автообновление.
	s.mu.Lock()
	if url == DefaultURLs[kind] {
		delete(s.urls, kind)
	} else {
		s.urls[kind] = url
	}
	s.saveMetaLocked()
	s.mu.Unlock()
	return nil
}

func (s *Store) download(ctx context.Context, kind Kind, url string) error {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("не удалось скачать %s.dat: %w", kind, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("не удалось скачать %s.dat: сервер ответил %s", kind, resp.Status)
	}

	return s.install(kind, resp.Body)
}

// Replace ставит базу из присланного файла — для тех, у кого адрес базы
// недоступен.
func (s *Store) Replace(kind Kind, r io.Reader) error {
	if !validKind(kind) {
		return fmt.Errorf("неизвестная база %q", kind)
	}
	if !s.begin(kind) {
		return errors.New("база уже обновляется")
	}

	err := s.install(kind, r)
	s.finish(kind, err)
	return err
}

func (s *Store) begin(kind Kind) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updating[kind] {
		return false
	}
	s.updating[kind] = true
	return true
}

func (s *Store) finish(kind Kind, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updating[kind] = false
	if err != nil {
		s.errs[kind] = err.Error()
	} else {
		delete(s.errs, kind)
	}
}

// install пишет файл рядом, проверяет его оглавлением и только потом ставит
// на место: битая загрузка не должна заменить работающую базу.
func (s *Store) install(kind Kind, r io.Reader) error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(s.dir, string(kind)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	n, err := io.Copy(tmp, io.LimitReader(r, maxFileSize+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("не удалось записать %s.dat: %w", kind, err)
	}
	if n > maxFileSize {
		return fmt.Errorf("%s.dat больше %d МБ — это не похоже на базу", kind, maxFileSize>>20)
	}

	f, err := indexFile(tmpPath, kind)
	if err != nil {
		return fmt.Errorf("%s.dat не годится: %w", kind, err)
	}

	path := s.path(kind)
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	f.path = path
	if info, err := os.Stat(path); err == nil {
		f.modTime = info.ModTime()
	}

	s.mu.Lock()
	s.files[kind] = f
	if kind == IP {
		s.ipCache = make(map[string][]netip.Prefix)
	} else {
		s.siteCache = make(map[string][]Domain)
	}
	s.mu.Unlock()

	log.Printf("База %s обновлена: категорий %d, %d КБ", kind, len(f.tags), f.size>>10)
	return nil
}

func (s *Store) saveMetaLocked() {
	data, err := json.MarshalIndent(meta{URLs: s.urls}, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(s.metaPath(), data, 0600); err != nil {
		log.Printf("Не удалось записать %s: %v", s.metaPath(), err)
	}
}

// AutoUpdate обновляет устаревшие базы, пока не отменён ctx. Отсутствующие
// не скачивает: базу впервые качают по просьбе пользователя — это десятки
// мегабайт и обращение к чужому серверу. updated вызывается после каждого
// удавшегося обновления.
func (s *Store) AutoUpdate(ctx context.Context, updated func(Kind)) {
	// Первую проверку откладываем: при старте системы сети может ещё не
	// быть, а туннель как раз поднимается.
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		for _, kind := range Kinds {
			s.mu.Lock()
			f := s.files[kind]
			s.mu.Unlock()

			if f == nil || time.Since(f.modTime) < staleAfter {
				continue
			}
			if err := s.Download(ctx, kind, ""); err != nil {
				log.Printf("Автообновление %s: %v", kind, err)
				continue
			}
			if updated != nil {
				updated(kind)
			}
		}

		timer.Reset(checkEvery)
	}
}

func validKind(kind Kind) bool {
	return kind == IP || kind == Site
}
