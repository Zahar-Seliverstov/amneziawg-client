// Package appsteer уводит соединения выбранных программ не тем путём, каким
// идёт остальной трафик: в режиме «только список» — в туннель, в режиме «всё,
// кроме списка» — мимо него.
//
// Это аналог nftables-правил с masquerade на Linux (vpn/policy_linux.go) для
// систем, где маршрут нельзя выбрать по процессу. Пакеты программы
// перехватываются (на Windows — драйвером WinDivert), адрес отправителя
// подменяется адресом нужного интерфейса, и пакет отправляется заново —
// система ведёт его уже по маршрутам этого интерфейса. Ответы, пришедшие на
// подменённый адрес, получают исходный адрес обратно и доставляются
// программе так, будто пришли обычным путём.
//
// Пакет разделён так, чтобы почти всё проверялось тестами на любой системе:
// разбор и правка пакетов (packet.go), учёт соединений и решения
// (steer.go) — чистый Go; перехват и вопросы к системе — в файлах
// *_windows.go.
package appsteer

import (
	"encoding/binary"
	"net/netip"
)

const (
	protoTCP = 6
	protoUDP = 17

	tcpFIN = 0x01
	tcpSYN = 0x02
	tcpRST = 0x04
	tcpACK = 0x10

	tcpOptMSS = 2
)

// header — то, что нужно знать о пакете IP с TCP или UDP внутри.
type header struct {
	v6       bool
	proto    uint8
	ipLen    int // длина заголовка IP: с неё начинается TCP/UDP
	src, dst netip.Addr
	sport    uint16
	dport    uint16
	flags    uint8 // флаги TCP; у UDP — 0
}

// parse разбирает пакет. false — не IP, не TCP/UDP, фрагмент или обрезок:
// такие пакеты пропускаются как есть.
func parse(p []byte) (header, bool) {
	if len(p) < 1 {
		return header{}, false
	}
	var h header

	switch p[0] >> 4 {
	case 4:
		if len(p) < 20 {
			return h, false
		}
		h.ipLen = int(p[0]&0x0f) * 4
		// Фрагменты (кроме первого без флага MF) несут не заголовок TCP/UDP.
		frag := binary.BigEndian.Uint16(p[6:8])
		if h.ipLen < 20 || len(p) < h.ipLen || frag&0x3fff != 0 {
			return h, false
		}
		h.proto = p[9]
		h.src = netip.AddrFrom4([4]byte(p[12:16]))
		h.dst = netip.AddrFrom4([4]byte(p[16:20]))
	case 6:
		if len(p) < 40 {
			return h, false
		}
		// Заголовки расширений не разбираем: программам они почти не нужны,
		// а пакет с ними просто идёт как обычно.
		h.v6, h.ipLen, h.proto = true, 40, p[6]
		h.src = netip.AddrFrom16([16]byte(p[8:24]))
		h.dst = netip.AddrFrom16([16]byte(p[24:40]))
	default:
		return h, false
	}

	t := p[h.ipLen:]
	switch h.proto {
	case protoTCP:
		if len(t) < 20 {
			return h, false
		}
		h.flags = t[13]
	case protoUDP:
		if len(t) < 8 {
			return h, false
		}
	default:
		return h, false
	}
	h.sport = binary.BigEndian.Uint16(t[0:2])
	h.dport = binary.BigEndian.Uint16(t[2:4])
	return h, true
}

// setSrc и setDst меняют адрес в пакете IPv4. Контрольные суммы после них
// пересчитывает fixChecksums.
func setSrc(p []byte, a netip.Addr) { b := a.As4(); copy(p[12:16], b[:]) }
func setDst(p []byte, a netip.Addr) { b := a.As4(); copy(p[16:20], b[:]) }

// clampMSS снижает MSS в SYN до max. Без этого соединение, начатое по
// интерфейсу с MTU 1500, договаривалось бы о сегментах, которые в туннель
// с MTU 1380 не пролезают: рукопожатие проходит, а крупные ответы теряются,
// и страница «висит» на загрузке.
func clampMSS(p []byte, h header, max uint16) {
	if h.proto != protoTCP || h.flags&tcpSYN == 0 || max == 0 {
		return
	}
	t := p[h.ipLen:]
	dataOff := int(t[12]>>4) * 4
	if dataOff < 20 || dataOff > len(t) {
		return
	}
	opts := t[20:dataOff]
	for i := 0; i < len(opts); {
		switch kind := opts[i]; kind {
		case 0: // конец списка
			return
		case 1: // NOP
			i++
			continue
		default:
			if i+1 >= len(opts) || opts[i+1] < 2 || i+int(opts[i+1]) > len(opts) {
				return
			}
			if kind == tcpOptMSS && opts[i+1] == 4 {
				if binary.BigEndian.Uint16(opts[i+2:i+4]) > max {
					binary.BigEndian.PutUint16(opts[i+2:i+4], max)
				}
				return
			}
			i += int(opts[i+1])
		}
	}
}

// fixChecksums пересчитывает контрольные суммы IPv4 и TCP/UDP целиком.
func fixChecksums(p []byte, h header) {
	binary.BigEndian.PutUint16(p[10:12], 0)
	binary.BigEndian.PutUint16(p[10:12], checksum(p[:h.ipLen], 0))

	t := p[h.ipLen:]
	total := int(binary.BigEndian.Uint16(p[2:4]))
	if total < h.ipLen || total > len(p) {
		total = len(p)
	}
	t = t[:total-h.ipLen]

	off := 16 // TCP
	if h.proto == protoUDP {
		off = 6
	}
	binary.BigEndian.PutUint16(t[off:off+2], 0)

	// Псевдозаголовок: адреса, протокол, длина сегмента.
	var sum uint32
	sum += uint32(binary.BigEndian.Uint16(p[12:14])) + uint32(binary.BigEndian.Uint16(p[14:16]))
	sum += uint32(binary.BigEndian.Uint16(p[16:18])) + uint32(binary.BigEndian.Uint16(p[18:20]))
	sum += uint32(h.proto) + uint32(len(t))

	c := checksum(t, sum)
	if h.proto == protoUDP && c == 0 {
		c = 0xffff // у UDP ноль значит «суммы нет»
	}
	binary.BigEndian.PutUint16(t[off:off+2], c)
}

// checksum — интернет-сумма (RFC 1071) с начальным значением.
func checksum(b []byte, sum uint32) uint16 {
	for len(b) >= 2 {
		sum += uint32(b[0])<<8 | uint32(b[1])
		b = b[2:]
	}
	if len(b) == 1 {
		sum += uint32(b[0]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}
