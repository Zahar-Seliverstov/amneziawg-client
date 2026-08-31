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
	expires map[netip.Prefix]time.Time
}

// NewDynamicSet создаёт учёт поверх заданного установщика.
func NewDynamicSet(installer Installer) *DynamicSet {
	return &DynamicSet{
		installer: installer,
		expires:   make(map[netip.Prefix]time.Time),
	}
}

// Observe регистрирует адреса из ответа DNS. Уже известный адрес только
// продлевает срок: повторно трогать таблицу маршрутизации не нужно, а ответы
// на популярные имена приходят непрерывно.
//
// Возвращает количество действительно добавленных маршрутов.
func (s *DynamicSet) Observe(addrs []netip.Addr, ttl time.Duration, now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	added := 0
	deadline := now.Add(ttl)

	for _, addr := range addrs {
		prefix := PrefixOf(addr.Unmap())

		if _, known := s.expires[prefix]; known {
			// Продлеваем, только если новый срок дальше текущего.
			if deadline.After(s.expires[prefix]) {
				s.expires[prefix] = deadline
			}
			continue
		}

		if err := s.installer.Add(prefix); err != nil {
			log.Printf("Не удалось добавить маршрут %s: %v", prefix, err)
			continue
		}

		s.expires[prefix] = deadline
		added++
	}

	return added
}

// Sweep снимает маршруты, срок которых истёк. Возвращает их количество.
func (s *DynamicSet) Sweep(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for prefix, deadline := range s.expires {
		if now.Before(deadline) {
			continue
		}

		if err := s.installer.Remove(prefix); err != nil {
			log.Printf("Не удалось снять маршрут %s: %v", prefix, err)
		}
		delete(s.expires, prefix)
		removed++
	}

	return removed
}

// Clear снимает все маршруты разом — при отключении и при смене правил.
func (s *DynamicSet) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for prefix := range s.expires {
		if err := s.installer.Remove(prefix); err != nil {
			log.Printf("Не удалось снять маршрут %s: %v", prefix, err)
		}
		delete(s.expires, prefix)
	}
}

// Len сообщает, сколько маршрутов сейчас на учёте.
func (s *DynamicSet) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.expires)
}
