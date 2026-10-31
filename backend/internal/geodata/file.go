package geodata

// Один файл базы: оглавление и чтение категорий по требованию.
//
// Схема (v2ray/xray, common/router/config.proto):
//
//	GeoIPList   { repeated GeoIP entry = 1 }
//	GeoIP       { string country_code = 1; repeated CIDR cidr = 2 }
//	CIDR        { bytes ip = 1; uint32 prefix = 2 }
//	GeoSiteList { repeated GeoSite entry = 1 }
//	GeoSite     { string country_code = 1; repeated Domain domain = 2 }
//	Domain      { Type type = 1; string value = 2; repeated Attribute attribute = 3 }
//	Attribute   { string key = 1; ... }
//
// В оглавлении — где в файле лежит каждая категория и сколько в ней записей.
// Сами записи читаются, только когда категорию упомянуло правило: из
// полутора тысяч категорий geosite нужны обычно две-три.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"sort"
	"strings"
	"time"
)

// DomainType — как запись geosite сопоставляется с именем.
type DomainType uint8

// Значения совпадают с перечислением в схеме v2ray.
const (
	// DomainKeyword — подстрока имени.
	DomainKeyword DomainType = 0
	// DomainRegexp — регулярное выражение.
	DomainRegexp DomainType = 1
	// DomainSuffix — имя и все его поддомены.
	DomainSuffix DomainType = 2
	// DomainFull — ровно это имя.
	DomainFull DomainType = 3
)

// Domain — одна запись категории geosite.
type Domain struct {
	Type  DomainType
	Value string
}

// Tag — категория из оглавления.
type Tag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type entry struct {
	off, size int64
	count     int
}

type file struct {
	path    string
	kind    Kind
	size    int64
	modTime time.Time
	entries map[string]entry
	tags    []Tag
}

// indexFile читает оглавление файла. Заодно это проверка файла: мусор вместо
// базы (страница ошибки, обрезанная загрузка) здесь и отсеивается.
func indexFile(path string, kind Kind) (*file, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	s := &stream{r: bufio.NewReaderSize(f, 1<<16)}
	out := &file{
		path:    path,
		kind:    kind,
		size:    info.Size(),
		modTime: info.ModTime(),
		entries: make(map[string]entry),
	}

	for {
		num, wire, err := s.field()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if num != 1 || wire != wireBytes {
			if err := s.skip(wire); err != nil {
				return nil, err
			}
			continue
		}

		size, err := s.varint()
		if err != nil {
			return nil, err
		}
		if int64(size) > out.size-s.pos {
			return nil, errMalformed
		}

		off := s.pos
		name, count, err := scanEntry(s, off+int64(size))
		if err != nil {
			return nil, err
		}

		name = strings.ToLower(name)
		if name == "" {
			continue
		}
		// Повтор категории бывает в самодельных базах — берём первую, как
		// и v2ray.
		if _, dup := out.entries[name]; dup {
			continue
		}
		out.entries[name] = entry{off: off, size: int64(size), count: count}
		out.tags = append(out.tags, Tag{Name: name, Count: count})
	}

	if len(out.entries) == 0 {
		return nil, fmt.Errorf("%w: в файле нет ни одной категории", errMalformed)
	}

	sort.Slice(out.tags, func(i, j int) bool { return out.tags[i].Name < out.tags[j].Name })
	return out, nil
}

// scanEntry проходит одну категорию до границы end: достаёт имя и считает
// записи, не разбирая их.
func scanEntry(s *stream, end int64) (string, int, error) {
	var (
		name  string
		count int
	)

	for s.pos < end {
		num, wire, err := s.field()
		if err != nil {
			return "", 0, errMalformed
		}

		switch {
		case num == 1 && wire == wireBytes:
			n, err := s.varint()
			if err != nil {
				return "", 0, err
			}
			raw, err := s.read(int64(n))
			if err != nil {
				return "", 0, err
			}
			name = string(raw)

		case num == 2 && wire == wireBytes:
			count++
			if err := s.skip(wire); err != nil {
				return "", 0, err
			}

		default:
			if err := s.skip(wire); err != nil {
				return "", 0, err
			}
		}
	}

	if s.pos != end {
		return "", 0, errMalformed
	}
	return name, count, nil
}

// raw читает байты категории.
func (f *file) raw(name string) ([]byte, bool, error) {
	e, ok := f.entries[name]
	if !ok {
		return nil, false, nil
	}

	fh, err := os.Open(f.path)
	if err != nil {
		return nil, true, err
	}
	defer fh.Close()

	data := make([]byte, e.size)
	if _, err := fh.ReadAt(data, e.off); err != nil {
		return nil, true, err
	}
	return data, true, nil
}

// decodeCIDRs разбирает категорию geoip.
func decodeCIDRs(data []byte) ([]netip.Prefix, error) {
	var out []netip.Prefix

	b := &buf{data: data}
	for !b.done() {
		num, wire, err := b.field()
		if err != nil {
			return nil, err
		}
		if num != 2 || wire != wireBytes {
			if err := b.skip(wire); err != nil {
				return nil, err
			}
			continue
		}

		msg, err := b.bytes()
		if err != nil {
			return nil, err
		}

		prefix, ok, err := decodeCIDR(msg)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, prefix)
		}
	}

	return out, nil
}

func decodeCIDR(data []byte) (netip.Prefix, bool, error) {
	var (
		ip   []byte
		bits uint64
	)

	b := &buf{data: data}
	for !b.done() {
		num, wire, err := b.field()
		if err != nil {
			return netip.Prefix{}, false, err
		}
		switch {
		case num == 1 && wire == wireBytes:
			if ip, err = b.bytes(); err != nil {
				return netip.Prefix{}, false, err
			}
		case num == 2 && wire == wireVarint:
			if bits, err = b.varint(); err != nil {
				return netip.Prefix{}, false, err
			}
		default:
			if err := b.skip(wire); err != nil {
				return netip.Prefix{}, false, err
			}
		}
	}

	addr, ok := netip.AddrFromSlice(ip)
	if !ok || int(bits) > addr.BitLen() {
		// Негодную запись пропускаем, а не бракуем весь файл: в базе их
		// сотни тысяч, и одна кривая не повод остаться без всех.
		return netip.Prefix{}, false, nil
	}

	return netip.PrefixFrom(addr.Unmap(), int(bits)-unmapShift(addr)).Masked(), true, nil
}

// unmapShift — на сколько укоротить маску адреса IPv4, записанного как IPv6
// (::ffff:1.2.3.0/120 — это 1.2.3.0/24).
func unmapShift(addr netip.Addr) int {
	if addr.Is4In6() {
		return 96
	}
	return 0
}

// decodeDomains разбирает категорию geosite. attrs — атрибуты, которые
// обязана нести запись (geosite:google@cn); пустой список — без отбора.
func decodeDomains(data []byte, attrs []string) ([]Domain, error) {
	var out []Domain

	b := &buf{data: data}
	for !b.done() {
		num, wire, err := b.field()
		if err != nil {
			return nil, err
		}
		if num != 2 || wire != wireBytes {
			if err := b.skip(wire); err != nil {
				return nil, err
			}
			continue
		}

		msg, err := b.bytes()
		if err != nil {
			return nil, err
		}

		d, have, err := decodeDomain(msg)
		if err != nil {
			return nil, err
		}
		if d.Value == "" || !hasAll(have, attrs) {
			continue
		}
		out = append(out, d)
	}

	return out, nil
}

func decodeDomain(data []byte) (Domain, []string, error) {
	var (
		d     Domain
		attrs []string
	)

	b := &buf{data: data}
	for !b.done() {
		num, wire, err := b.field()
		if err != nil {
			return d, nil, err
		}
		switch {
		case num == 1 && wire == wireVarint:
			t, err := b.varint()
			if err != nil {
				return d, nil, err
			}
			d.Type = DomainType(t)
		case num == 2 && wire == wireBytes:
			v, err := b.bytes()
			if err != nil {
				return d, nil, err
			}
			d.Value = string(v)
		case num == 3 && wire == wireBytes:
			msg, err := b.bytes()
			if err != nil {
				return d, nil, err
			}
			if key := attributeKey(msg); key != "" {
				attrs = append(attrs, strings.ToLower(key))
			}
		default:
			if err := b.skip(wire); err != nil {
				return d, nil, err
			}
		}
	}

	return d, attrs, nil
}

func attributeKey(data []byte) string {
	b := &buf{data: data}
	for !b.done() {
		num, wire, err := b.field()
		if err != nil {
			return ""
		}
		if num == 1 && wire == wireBytes {
			v, err := b.bytes()
			if err != nil {
				return ""
			}
			return string(v)
		}
		if err := b.skip(wire); err != nil {
			return ""
		}
	}
	return ""
}

func hasAll(have, want []string) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if h == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Aggregate сводит список подсетей к наименьшему: убирает вложенные и
// склеивает соседние половины в целое. Каждая подсеть — это маршрут в
// таблице, а geoip:ru без сведения — десятки тысяч записей.
func Aggregate(prefixes []netip.Prefix) []netip.Prefix {
	sorted := make([]netip.Prefix, 0, len(prefixes))
	for _, p := range prefixes {
		sorted = append(sorted, p.Masked())
	}
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if c := a.Addr().Compare(b.Addr()); c != 0 {
			return c < 0
		}
		return a.Bits() < b.Bits()
	})

	var out []netip.Prefix
	for _, p := range sorted {
		if n := len(out); n > 0 && out[n-1].Overlaps(p) && out[n-1].Bits() <= p.Bits() {
			continue // вложена в предыдущую
		}
		out = append(out, p)

		// Две половины одной подсети подряд — заменяем родителем, и так
		// вверх, пока склеивается.
		for len(out) >= 2 {
			a, b := out[len(out)-2], out[len(out)-1]
			if a.Bits() != b.Bits() || a.Bits() == 0 {
				break
			}
			parent, err := a.Addr().Prefix(a.Bits() - 1)
			if err != nil || parent.Addr() != a.Addr() || !parent.Contains(b.Addr()) {
				break
			}
			out = append(out[:len(out)-2], parent)
		}
	}

	return out
}
