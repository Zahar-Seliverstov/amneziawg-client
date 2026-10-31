//go:build linux

package vpn

// Правила для программ: app:minecraft.
//
// Адрес назначения здесь ни при чём — решает то, какой процесс открыл
// соединение. Ядро умеет сопоставлять сокет с cgroup процесса (nftables,
// socket cgroupv2), поэтому подходящие процессы переносятся в нашу cgroup, а
// её соединения получают метку и идут своим путём (policy_linux.go).
//
// Процесс узнаётся по имени программы, по имени исполняемого файла или по
// слову из командной строки. Последнее нужно ради программ на Java и
// Python: у Minecraft имя процесса — java, а слово «minecraft» есть только
// в аргументах. Дочерние процессы попадают в cgroup родителя сами.
//
// Внутри нашей cgroup две: all — для app:имя, все соединения программы, и
// udp — для app:имя/udp, где своим путём идёт только UDP. Процесс живёт в
// одной cgroup, поэтому, если программа попала под оба правила, побеждает
// all: она шире.
//
// Новые процессы находятся опросом /proc раз в секунду. Соединение, открытое
// программой в первую секунду жизни, успевает уйти старым путём; игре или
// браузеру это не мешает — до первого подключения у них проходят секунды.

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/user/amnezia-web-client/internal/apps"
	"github.com/user/amnezia-web-client/internal/config"
)

// appsCgroup — наша cgroup, прямо под корнем иерархии.
const appsCgroup = "awg-apps"

const cgroupRoot = "/sys/fs/cgroup"

const appScanInterval = time.Second

type appWatcher struct {
	cgroup string // имя под корнем

	// all и udp — пути вложенных cgroup от корня иерархии: в них лежат
	// процессы, и по ним nftables узнаёт их сокеты.
	all, udp string

	mu    sync.Mutex
	names appSet
	stopc chan struct{}
	done  chan struct{}

	// origin — откуда взят процесс: туда он и вернётся, когда правило
	// уберут или туннель опустят.
	origin map[int]string
}

func newAppWatcher(cgroup string) *appWatcher {
	return &appWatcher{
		cgroup: cgroup,
		all:    cgroup + "/all",
		udp:    cgroup + "/udp",
		origin: make(map[int]string),
	}
}

// dirs — наша cgroup и вложенные, от внешней к внутренним.
func (w *appWatcher) dirs() []string {
	return []string{
		filepath.Join(cgroupRoot, w.cgroup),
		filepath.Join(cgroupRoot, w.all),
		filepath.Join(cgroupRoot, w.udp),
	}
}

func (w *appWatcher) ensureCgroup() error {
	for _, dir := range w.dirs() {
		if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("не создать cgroup %s: %w", dir, err)
		}
	}
	return nil
}

// removeCgroup убирает наши cgroup: сначала вложенные, иначе внешняя не
// удалится.
func (w *appWatcher) removeCgroup() {
	dirs := w.dirs()
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i])
	}
}

// target — в какую из наших cgroup положить процесс и по какому правилу.
// Пусто — ни одно правило его не берёт.
func (w *appWatcher) target(info apps.Proc, names appSet) (cgroup, rule string) {
	if name := apps.Match(info, names.all); name != "" {
		return w.all, name
	}
	if name := apps.Match(info, names.udp); name != "" {
		return w.udp, name + config.AppUDPSuffix
	}
	return "", ""
}

// watch запускает опрос или меняет набор имён под работающим.
func (w *appWatcher) watch(names appSet) {
	w.mu.Lock()
	w.names = appSet{all: append([]string(nil), names.all...), udp: append([]string(nil), names.udp...)}
	running := w.stopc != nil
	if !running {
		w.stopc = make(chan struct{})
		w.done = make(chan struct{})
	}
	stop, done := w.stopc, w.done
	w.mu.Unlock()

	// Процессы, переставшие подходить под новый набор, возвращаются сразу.
	w.releaseUnmatched()

	if running {
		return
	}

	go func() {
		defer close(done)
		ticker := time.NewTicker(appScanInterval)
		defer ticker.Stop()

		w.scan()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				w.scan()
			}
		}
	}()
}

// stop останавливает опрос и возвращает все процессы на место.
func (w *appWatcher) stop() {
	w.mu.Lock()
	stop, done := w.stopc, w.done
	w.stopc, w.done, w.names = nil, nil, appSet{}
	w.mu.Unlock()

	if stop != nil {
		close(stop)
		<-done
	}

	w.releaseAll()
	w.removeCgroup()
}

func (w *appWatcher) currentNames() appSet {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.names
}

// scan переносит в нашу cgroup подходящие процессы, которых там ещё нет, и
// перекладывает между вложенными те, чьё правило сменилось.
func (w *appWatcher) scan() {
	names := w.currentNames()
	if names.empty() {
		return
	}

	self := os.Getpid()
	ours := "/" + w.cgroup

	for _, pid := range apps.ListPIDs() {
		if pid == self {
			continue
		}

		info, ok := apps.ReadProc(pid)
		if !ok {
			continue
		}
		target, rule := w.target(info, names)
		if target == "" || info.Cgroup == "/"+target {
			continue
		}
		inside := info.Cgroup == ours || strings.HasPrefix(info.Cgroup, ours+"/")
		// Только то, что запустил пользователь. Системные службы живут в
		// своих юнитах, и вынимать их оттуда по совпавшему слову нельзя:
		// правило app:sh иначе забрало бы половину системы.
		if !inside && !info.UserProcess() {
			continue
		}

		if err := moveToCgroup(pid, filepath.Join(cgroupRoot, target)); err != nil {
			log.Printf("Программа %s (pid %d) не взята: %v", info.Comm, pid, err)
			continue
		}

		if !inside {
			w.mu.Lock()
			w.origin[pid] = info.Cgroup
			w.mu.Unlock()
		}

		log.Printf("Программа %s (pid %d) идёт по правилу app:%s", info.Comm, pid, rule)
	}
}

// releaseUnmatched возвращает процессы, которые больше ни под одно имя не
// подходят.
func (w *appWatcher) releaseUnmatched() {
	names := w.currentNames()
	w.release(func(info apps.Proc) bool {
		target, _ := w.target(info, names)
		return target == ""
	})
}

func (w *appWatcher) releaseAll() {
	w.release(func(apps.Proc) bool { return true })
}

// release возвращает процессы из нашей cgroup на прежнее место. Проходит по
// самой cgroup, а не по учёту: туда попадают и дочерние процессы, о которых
// учёт не знает, — их ведём туда же, куда родителя, или в корень.
func (w *appWatcher) release(should func(apps.Proc) bool) {
	var pids []string
	for _, dir := range w.dirs() {
		if data, err := os.ReadFile(filepath.Join(dir, "cgroup.procs")); err == nil {
			pids = append(pids, strings.Fields(string(data))...)
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	for _, field := range pids {
		pid, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		info, ok := apps.ReadProc(pid)
		if !ok || !should(info) {
			continue
		}

		target := w.origin[pid]
		if target == "" {
			target = w.origin[info.PPID]
		}
		if err := moveToCgroup(pid, filepath.Join(cgroupRoot, target)); err != nil {
			// Прежней cgroup уже нет (сессия закрылась) — в корень.
			_ = moveToCgroup(pid, cgroupRoot)
		}
		delete(w.origin, pid)
	}

	// Учёт умерших процессов незачем держать.
	for pid := range w.origin {
		if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); err != nil {
			delete(w.origin, pid)
		}
	}
}

// cleanupOrphans выпускает процессы, оставленные в нашей cgroup прошлым
// запуском службы, и убирает её саму.
func (w *appWatcher) cleanupOrphans() {
	w.releaseAll()
	w.removeCgroup()
}

func moveToCgroup(pid int, dir string) error {
	return os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0o644)
}
