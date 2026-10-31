package geodata

import (
	"bytes"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Кодировщик ровно под тесты: собирает файлы той же схемы, что и v2ray.

func pbVarint(n uint64) []byte {
	var out []byte
	for n >= 0x80 {
		out = append(out, byte(n)|0x80)
		n >>= 7
	}
	return append(out, byte(n))
}

func pbBytes(num int, data []byte) []byte {
	out := pbVarint(uint64(num<<3 | wireBytes))
	out = append(out, pbVarint(uint64(len(data)))...)
	return append(out, data...)
}

func pbUint(num int, v uint64) []byte {
	return append(pbVarint(uint64(num<<3|wireVarint)), pbVarint(v)...)
}

func geoipFile(entries map[string][]string) []byte {
	var out []byte
	for code, cidrs := range entries {
		entry := pbBytes(1, []byte(code))
		for _, c := range cidrs {
			p := netip.MustParsePrefix(c)
			cidr := append(pbBytes(1, p.Addr().AsSlice()), pbUint(2, uint64(p.Bits()))...)
			entry = append(entry, pbBytes(2, cidr)...)
		}
		out = append(out, pbBytes(1, entry)...)
	}
	return out
}

type site struct {
	t     DomainType
	value string
	attrs []string
}

func geositeFile(entries map[string][]site) []byte {
	var out []byte
	for code, domains := range entries {
		entry := pbBytes(1, []byte(code))
		for _, d := range domains {
			msg := append(pbUint(1, uint64(d.t)), pbBytes(2, []byte(d.value))...)
			for _, a := range d.attrs {
				msg = append(msg, pbBytes(3, append(pbBytes(1, []byte(a)), pbUint(2, 1)...))...)
			}
			entry = append(entry, pbBytes(2, msg)...)
		}
		out = append(out, pbBytes(1, entry)...)
	}
	return out
}

func writeStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()

	ip := geoipFile(map[string][]string{
		"RU":      {"5.0.0.0/9", "5.128.0.0/9", "10.1.2.0/24", "10.1.0.0/16", "2a00::/12"},
		"PRIVATE": {"192.168.0.0/16"},
	})
	sites := geositeFile(map[string][]site{
		"YOUTUBE": {{DomainSuffix, "youtube.com", nil}, {DomainFull, "youtu.be", nil}},
		"GOOGLE":  {{DomainSuffix, "google.com", nil}, {DomainSuffix, "google.cn", []string{"cn"}}},
	})

	if err := os.WriteFile(filepath.Join(dir, "geoip.dat"), ip, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "geosite.dat"), sites, 0600); err != nil {
		t.Fatal(err)
	}
	return Open(dir)
}

func TestIndexAndLookup(t *testing.T) {
	s := writeStore(t)

	info := s.Info()
	if !info[IP].Present || len(info[IP].Tags) != 2 || info[IP].Tags[0].Name != "private" {
		t.Fatalf("оглавление geoip: %+v", info[IP])
	}
	if info[Site].Tags[1].Name != "youtube" || info[Site].Tags[1].Count != 2 {
		t.Fatalf("оглавление geosite: %+v", info[Site].Tags)
	}

	ips, err := s.IPs("ru")
	if err != nil {
		t.Fatal(err)
	}
	// Две половины 5.0.0.0/8 склеены, вложенная /24 исчезла.
	want := []string{"5.0.0.0/8", "10.1.0.0/16", "2a00::/12"}
	if len(ips) != len(want) {
		t.Fatalf("получили %v, ждали %v", ips, want)
	}
	for i, p := range ips {
		if p.String() != want[i] {
			t.Errorf("[%d] = %s, ждали %s", i, p, want[i])
		}
	}

	all, _ := s.Sites("google")
	cn, _ := s.Sites("google@cn")
	if len(all) != 2 || len(cn) != 1 || cn[0].Value != "google.cn" {
		t.Errorf("отбор по атрибуту: все %v, @cn %v", all, cn)
	}

	if err := s.Check(Site, "youtube"); err != nil {
		t.Errorf("категория есть, а проверка отказала: %v", err)
	}
	if err := s.Check(Site, "nope"); err == nil {
		t.Error("несуществующая категория прошла проверку")
	}
}

func TestReplaceRejectsGarbage(t *testing.T) {
	s := writeStore(t)

	err := s.Replace(IP, strings.NewReader("<html>Not found</html>"))
	if err == nil {
		t.Fatal("мусор принят за базу")
	}

	// Прежняя база осталась на месте.
	if _, err := s.IPs("ru"); err != nil {
		t.Errorf("рабочая база пропала после отказа: %v", err)
	}
}

func TestReplaceInvalidatesCache(t *testing.T) {
	s := writeStore(t)

	if _, err := s.IPs("ru"); err != nil {
		t.Fatal(err)
	}

	fresh := geoipFile(map[string][]string{"RU": {"1.0.0.0/8"}})
	if err := s.Replace(IP, bytes.NewReader(fresh)); err != nil {
		t.Fatal(err)
	}

	ips, _ := s.IPs("ru")
	if len(ips) != 1 || ips[0].String() != "1.0.0.0/8" {
		t.Errorf("после замены базы отдана старая категория: %v", ips)
	}
}

func TestMissingStore(t *testing.T) {
	s := Open(t.TempDir())
	if err := s.Check(IP, "ru"); err == nil || !strings.Contains(err.Error(), "не загружена") {
		t.Errorf("без базы ждали отказ «не загружена», получили %v", err)
	}
}

// Настоящие базы, если они есть на машине разработчика: формат собирают
// чужие инструменты, и синтетики мало.
func TestRealFiles(t *testing.T) {
	dir := os.Getenv("GEODATA_DIR")
	if dir == "" {
		t.Skip("GEODATA_DIR не задан")
	}
	s := Open(dir)
	for _, kind := range Kinds {
		if !s.Info()[kind].Present {
			t.Fatalf("%s не прочитан: %s", kind, s.Info()[kind].Error)
		}
	}
	ips, err := s.IPs("ru")
	if err != nil || len(ips) == 0 {
		t.Fatalf("geoip:ru: %d, %v", len(ips), err)
	}
	t.Logf("geoip:ru — %d подсетей после сведения", len(ips))
	sites, err := s.Sites("youtube")
	if err != nil || len(sites) == 0 {
		t.Fatalf("geosite:youtube: %d, %v", len(sites), err)
	}
	t.Logf("geosite:youtube — %d записей, категорий всего %d", len(sites), len(s.Info()[Site].Tags))
}
