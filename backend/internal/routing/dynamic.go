package routing

import (
	"log"
	"net/netip"
	"sync"
	"time"
)

// Installer ставит и снимает один маршрут. Абстракция нужна, чтобы учёт
// динамических маршрутов можно было проверить тестами без прав root и без
// настоящей таблицы маршрутизации.
type Installer interface {
	Add(prefix netip.Prefix) error
	Remove(prefix netip.Prefix) error
}

// DynamicSet хранит маршруты, полученные из ответов DNS, и снимает их, когда
// истёк срок жизни записи.
//
// Зачем срок: имена вроде CDN отдают десятки адресов и меняют их постоянно.
// Без вычистки таблица маршрутизации за сутки работы распухла бы тысячами
// записей, каждая из которых давно ведёт в никуда.
type DynamicSet struct {
	installer Installer

	mu      sync.Mutex
	entries map[netip.Prefix]*dynEntry
}

// dynEntry — один взятый под учёт адрес: до каких пор он живёт и по каким
// именам попал сюда.
//
// Имён может быть несколько: за одним адресом CDN стоят десятки сайтов, и
// пока хоть одно из них подпадает под правила, маршрут нужен. Без этого
// списка снять маршруты удалённого правила было бы нечем — оставался только
// один способ, снести все разом и учить заново.
type dynEntry struct {
	deadline time.Time
	names    map[string]bool
}

// NewDynamicSet создаёт учёт поверх заданного установщика.
func NewDynamicSet(installer Installer) *DynamicSet {
	return &DynamicSet{
		installer: installer,
		entries:   make(map[netip.Prefix]*dynEntry),
	}
}

// Observe регистрирует адреса из ответа DNS на имя name. Уже известный адрес
// только продлевает срок: повторно трогать таблицу маршрутизации не нужно, а
// ответы на популярные имена приходят непрерывно.
//
// Имя запоминается всегда, даже для уже известного адреса: правило могли
// удалить, и тогда решать судьбу маршрута придётся по тому, кто ещё за него
// держится.
//
// Возвращает количество действительно добавленных маршрутов.
func (s *DynamicSet) Observe(name string, addrs []netip.Addr, ttl time.Duration, now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	added := 0
	deadline := now.Add(ttl)

	for _, addr := range addrs {
		prefix := PrefixOf(addr.Unmap())

		if entry, known := s.entries[prefix]; known {
			// Продлеваем, только если новый срок дальше текущего.
			if deadline.After(entry.deadline) {
				entry.deadline = deadline
			}
			entry.names[name] = true
			continue
		}

		if err := s.installer.Add(prefix); err != nil {
			log.Printf("Не удалось добавить маршрут %s: %v", prefix, err)
			continue
		}

		s.entries[prefix] = &dynEntry{deadline: deadline, names: map[string]bool{name: true}}
		added++
	}

	return added
}

// Sweep снимает маршруты, срок которых истёк. Возвращает их количество.
func (s *DynamicSet) Sweep(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for prefix, entry := range s.entries {
		if now.Before(entry.deadline) {
			continue
		}

		s.drop(prefix)
		removed++
	}

	return removed
}

// Retain снимает маршруты, ни одно имя которых больше не подпадает под
// правила. Возвращает их количество.
//
// Ради этого имена и запоминаются. Раньше смена правил означала полную
// пересборку: посредник DNS гасился, весь учёт сносился разом, и браузер
// заново учил адреса всех оставшихся правил — самая заметная пользователю
// боль после каждой правки. Здесь уходят ровно маршруты исчезнувших имён.
func (s *DynamicSet) Retain(covered func(name string) bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for prefix, entry := range s.entries {
		for name := range entry.names {
			if !covered(name) {
				delete(entry.names, name)
			}
		}

		if len(entry.names) == 0 {
			s.drop(prefix)
			removed++
		}
	}

	return removed
}

// Clear снимает все маршруты разом — при отключении и при смене режима.
func (s *DynamicSet) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for prefix := range s.entries {
		s.drop(prefix)
	}
}

// drop снимает один маршрут и забывает его. Вызывать под mu.
//
// Забываем даже когда снять не вышло: маршрут либо уже исчез вместе с
// интерфейсом, либо его убрала чужая рука, и держать о нём запись значит
// пытаться снять его снова и снова на каждой уборке.
func (s *DynamicSet) drop(prefix netip.Prefix) {
	if err := s.installer.Remove(prefix); err != nil {
		log.Printf("Не удалось снять маршрут %s: %v", prefix, err)
	}
	delete(s.entries, prefix)
}

// Len сообщает, сколько маршрутов сейчас на учёте.
func (s *DynamicSet) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
