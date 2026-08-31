package routing

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

// fakeInstaller записывает вызовы вместо обращения к таблице маршрутизации.
type fakeInstaller struct {
	added   []netip.Prefix
	removed []netip.Prefix
	failOn  netip.Prefix
}

func (f *fakeInstaller) Add(p netip.Prefix) error {
	if p == f.failOn {
		return errors.New("отказ по условию теста")
	}
	f.added = append(f.added, p)
	return nil
}

func (f *fakeInstaller) Remove(p netip.Prefix) error {
	f.removed = append(f.removed, p)
	return nil
}

func addrs(list ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(list))
	for _, s := range list {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

func TestDynamicSetAddsOncePerAddress(t *testing.T) {
	installer := &fakeInstaller{}
	set := NewDynamicSet(installer)
	now := time.Now()

	if n := set.Observe(addrs("1.2.3.4", "5.6.7.8"), time.Hour, now); n != 2 {
		t.Fatalf("первое наблюдение: добавлено %d, ждали 2", n)
	}

	// Повтор того же ответа не должен снова трогать таблицу.
	if n := set.Observe(addrs("1.2.3.4", "5.6.7.8"), time.Hour, now); n != 0 {
		t.Errorf("повтор: добавлено %d, ждали 0", n)
	}

	if len(installer.added) != 2 {
		t.Errorf("обращений к таблице: %d, ждали 2", len(installer.added))
	}
	if set.Len() != 2 {
		t.Errorf("на учёте %d, ждали 2", set.Len())
	}
}

func TestDynamicSetSweepRemovesExpiredOnly(t *testing.T) {
	installer := &fakeInstaller{}
	set := NewDynamicSet(installer)
	start := time.Now()

	set.Observe(addrs("1.2.3.4"), time.Minute, start)
	set.Observe(addrs("5.6.7.8"), time.Hour, start)

	set.Sweep(start.Add(2 * time.Minute))

	if len(installer.removed) != 1 || installer.removed[0] != netip.MustParsePrefix("1.2.3.4/32") {
		t.Errorf("снято %v, ждали только 1.2.3.4/32", installer.removed)
	}
	if set.Len() != 1 {
		t.Errorf("на учёте %d, ждали 1", set.Len())
	}
}

// Повторный ответ продлевает срок — иначе маршрут к активно используемому
// сайту снимался бы прямо во время работы.
func TestDynamicSetObserveExtendsDeadline(t *testing.T) {
	installer := &fakeInstaller{}
	set := NewDynamicSet(installer)
	start := time.Now()

	set.Observe(addrs("1.2.3.4"), time.Minute, start)
	set.Observe(addrs("1.2.3.4"), time.Hour, start.Add(30*time.Second))

	set.Sweep(start.Add(2 * time.Minute))

	if len(installer.removed) != 0 {
		t.Errorf("маршрут сняли досрочно: %v", installer.removed)
	}
}

func TestDynamicSetFailedAddIsNotTracked(t *testing.T) {
	installer := &fakeInstaller{failOn: netip.MustParsePrefix("1.2.3.4/32")}
	set := NewDynamicSet(installer)

	if n := set.Observe(addrs("1.2.3.4", "5.6.7.8"), time.Hour, time.Now()); n != 1 {
		t.Errorf("добавлено %d, ждали 1 (второй адрес)", n)
	}
	if set.Len() != 1 {
		t.Errorf("неудачное добавление не должно попадать на учёт, на учёте %d", set.Len())
	}
}

func TestDynamicSetClear(t *testing.T) {
	installer := &fakeInstaller{}
	set := NewDynamicSet(installer)

	set.Observe(addrs("1.2.3.4", "5.6.7.8"), time.Hour, time.Now())
	set.Clear()

	if len(installer.removed) != 2 {
		t.Errorf("снято %d, ждали 2", len(installer.removed))
	}
	if set.Len() != 0 {
		t.Errorf("после Clear на учёте %d, ждали 0", set.Len())
	}
}

// IPv4-адрес, пришедший в виде IPv4-in-IPv6, должен попасть в таблицу как
// обычный /32: иначе "ip route" получит ::ffff:1.2.3.4/128 и откажет.
func TestDynamicSetUnmapsV4(t *testing.T) {
	installer := &fakeInstaller{}
	set := NewDynamicSet(installer)

	set.Observe([]netip.Addr{netip.MustParseAddr("::ffff:1.2.3.4")}, time.Hour, time.Now())

	if len(installer.added) != 1 || installer.added[0] != netip.MustParsePrefix("1.2.3.4/32") {
		t.Errorf("получили %v, ждали 1.2.3.4/32", installer.added)
	}
}
