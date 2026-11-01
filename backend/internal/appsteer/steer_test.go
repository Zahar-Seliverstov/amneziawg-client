package appsteer

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"
)

var (
	home   = netip.MustParseAddr("192.168.0.124") // адрес Wi-Fi
	tun    = netip.MustParseAddr("10.8.1.13")     // адрес туннеля
	remote = netip.MustParseAddr("203.0.113.7")
)

// tcpPacket собирает IPv4+TCP с опцией MSS 1460 и полезной нагрузкой.
func tcpPacket(src, dst netip.Addr, sport, dport uint16, flags uint8) []byte {
	opts := []byte{tcpOptMSS, 4, 0x05, 0xb4} // 1460
	payload := []byte("привет")
	tcpLen := 20 + len(opts)
	p := make([]byte, 20+tcpLen+len(payload))
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	p[8], p[9] = 64, protoTCP
	setSrc(p, src)
	setDst(p, dst)
	t := p[20:]
	binary.BigEndian.PutUint16(t[0:2], sport)
	binary.BigEndian.PutUint16(t[2:4], dport)
	t[12] = byte(tcpLen/4) << 4
	t[13] = flags
	copy(t[20:], opts)
	copy(t[tcpLen:], payload)
	h, _ := parse(p)
	fixChecksums(p, h)
	return p
}

func udpPacket(src, dst netip.Addr, sport, dport uint16) []byte {
	p := make([]byte, 20+8+4)
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	p[8], p[9] = 64, protoUDP
	setSrc(p, src)
	setDst(p, dst)
	binary.BigEndian.PutUint16(p[20:22], sport)
	binary.BigEndian.PutUint16(p[22:24], dport)
	binary.BigEndian.PutUint16(p[24:26], 12)
	copy(p[28:], "data")
	h, _ := parse(p)
	fixChecksums(p, h)
	return p
}

// checksumsValid проверяет суммы так, как это сделал бы получатель.
func checksumsValid(t *testing.T, p []byte) {
	t.Helper()
	h, ok := parse(p)
	if !ok {
		t.Fatal("пакет не разбирается")
	}
	if checksum(p[:h.ipLen], 0) != 0 {
		t.Error("неверная сумма IP")
	}
	seg := p[h.ipLen:]
	var sum uint32
	for i := 12; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(p[i : i+2]))
	}
	sum += uint32(h.proto) + uint32(len(seg))
	if checksum(seg, sum) != 0 {
		t.Error("неверная сумма TCP/UDP")
	}
}

func mss(p []byte) uint16 { return binary.BigEndian.Uint16(p[20+22 : 20+24]) }

// steerer уводит всё, что принадлежит pid 42.
func steerer(cfg Config) (*Steerer, *int) {
	asked := new(int)
	owner := func(proto uint8, local netip.AddrPort) (uint32, bool) {
		*asked++
		if local.Port() == 5000 {
			return 42, true
		}
		return 7, true
	}
	match := func(proto uint8, pid uint32) bool { return pid == 42 }
	return New(cfg, owner, match), asked
}

func TestSteersAppConnectionBothWays(t *testing.T) {
	s, _ := steerer(Config{From: home, To: tun, MSS: 1340})

	syn := tcpPacket(home, remote, 5000, 443, tcpSYN)
	if v := s.Outbound(syn); v != Rewritten {
		t.Fatalf("SYN программы: %v", v)
	}
	if h, _ := parse(syn); h.src != tun {
		t.Errorf("адрес отправителя %v, ожидался адрес туннеля", h.src)
	}
	if mss(syn) != 1340 {
		t.Errorf("MSS %d не снижен", mss(syn))
	}
	checksumsValid(t, syn)

	// Ответ приходит на адрес туннеля и возвращается программе на её адрес.
	synack := tcpPacket(remote, tun, 443, 5000, tcpSYN|tcpACK)
	if v := s.Inbound(synack); v != Rewritten {
		t.Fatalf("ответ: %v", v)
	}
	if h, _ := parse(synack); h.dst != home {
		t.Errorf("адрес получателя %v, ожидался исходный", h.dst)
	}
	if mss(synack) != 1340 {
		t.Errorf("MSS ответа %d не снижен", mss(synack))
	}
	checksumsValid(t, synack)

	data := tcpPacket(home, remote, 5000, 443, tcpACK)
	if v := s.Outbound(data); v != Rewritten {
		t.Errorf("данные того же соединения: %v", v)
	}
	if s.Flows() != 1 {
		t.Errorf("уводится соединений: %d", s.Flows())
	}
}

func TestLeavesOtherTraffic(t *testing.T) {
	s, _ := steerer(Config{From: home, To: tun})

	other := tcpPacket(home, remote, 6000, 443, tcpSYN)
	before := append([]byte(nil), other...)
	if v := s.Outbound(other); v != Pass || string(other) != string(before) {
		t.Errorf("чужое соединение тронуто: %v", v)
	}

	// Пакет с другим адресом отправителя (сам туннель, другой интерфейс).
	if v := s.Outbound(tcpPacket(tun, remote, 5000, 443, tcpSYN)); v != Pass {
		t.Errorf("пакет с чужого адреса: %v", v)
	}

	// Ответ на адрес туннеля по соединению, которого мы не уводили.
	if v := s.Inbound(tcpPacket(remote, tun, 443, 7000, tcpACK)); v != Pass {
		t.Errorf("чужой ответ: %v", v)
	}
}

// Соединение, открытое до включения правила, не переводится: его бы просто
// оборвало.
func TestLeavesConnectionsOpenedBefore(t *testing.T) {
	s, asked := steerer(Config{From: home, To: tun})
	if v := s.Outbound(tcpPacket(home, remote, 5000, 443, tcpACK)); v != Pass {
		t.Errorf("старое соединение: %v", v)
	}
	if *asked != 0 {
		t.Error("для старого соединения спрашивали владельца")
	}
}

func TestDecisionIsCached(t *testing.T) {
	s, asked := steerer(Config{From: home, To: tun})
	s.Outbound(tcpPacket(home, remote, 5000, 443, tcpSYN))
	s.Outbound(tcpPacket(home, remote, 5000, 443, tcpACK))
	s.Outbound(tcpPacket(home, remote, 5000, 443, tcpACK))
	if *asked != 1 {
		t.Errorf("владельца спрашивали %d раз — на каждый пакет вместо одного раза на соединение", *asked)
	}
}

func TestResetForgetsConnection(t *testing.T) {
	s, _ := steerer(Config{From: home, To: tun})
	s.Outbound(tcpPacket(home, remote, 5000, 443, tcpSYN))
	s.Inbound(tcpPacket(remote, tun, 443, 5000, tcpRST|tcpACK))
	if s.Flows() != 0 {
		t.Error("оборванное соединение осталось в учёте")
	}
}

func TestUDP(t *testing.T) {
	s, _ := steerer(Config{From: home, To: tun})
	p := udpPacket(home, remote, 5000, 3478)
	if v := s.Outbound(p); v != Rewritten {
		t.Fatalf("UDP программы: %v", v)
	}
	checksumsValid(t, p)

	reply := udpPacket(remote, tun, 3478, 5000)
	if v := s.Inbound(reply); v != Rewritten {
		t.Fatalf("ответ UDP: %v", v)
	}
	checksumsValid(t, reply)
}

func TestMatchSeesProtocol(t *testing.T) {
	owner := func(uint8, netip.AddrPort) (uint32, bool) { return 42, true }
	udpOnly := func(proto uint8, _ uint32) bool { return proto == protoUDP }
	s := New(Config{From: home, To: tun}, owner, udpOnly)

	if v := s.Outbound(tcpPacket(home, remote, 5000, 443, tcpSYN)); v != Pass {
		t.Errorf("TCP программы с правилом /udp: %v", v)
	}
	if v := s.Outbound(udpPacket(home, remote, 5000, 443)); v != Rewritten {
		t.Errorf("UDP программы с правилом /udp: %v", v)
	}
}

func TestDropIPv6OfApp(t *testing.T) {
	v6 := func(sport uint16) []byte {
		p := make([]byte, 40+20)
		p[0], p[6] = 0x60, protoTCP
		copy(p[8:24], netip.MustParseAddr("2001:db8::1").AsSlice())
		copy(p[24:40], netip.MustParseAddr("2001:db8::2").AsSlice())
		binary.BigEndian.PutUint16(p[40:42], sport)
		p[40+12], p[40+13] = 5<<4, tcpSYN
		return p
	}

	s, _ := steerer(Config{From: home, To: tun, DropIPv6: true})
	if v := s.Outbound(v6(5000)); v != Drop {
		t.Errorf("IPv6 программы: %v", v)
	}
	if v := s.Outbound(v6(6000)); v != Pass {
		t.Errorf("IPv6 чужой программы: %v", v)
	}

	s, _ = steerer(Config{From: home, To: tun})
	if v := s.Outbound(v6(5000)); v != Pass {
		t.Errorf("IPv6 без DropIPv6: %v", v)
	}
}

func TestSweep(t *testing.T) {
	s, _ := steerer(Config{From: home, To: tun})
	clock := time.Now()
	s.now = func() time.Time { return clock }

	s.Outbound(udpPacket(home, remote, 5000, 1))
	s.Outbound(tcpPacket(home, remote, 5000, 443, tcpSYN))

	clock = clock.Add(udpIdle + time.Second)
	s.Sweep()
	if s.Flows() != 1 {
		t.Errorf("после срока UDP осталось %d", s.Flows())
	}
	clock = clock.Add(tcpIdle)
	s.Sweep()
	if s.Flows() != 0 {
		t.Errorf("после срока TCP осталось %d", s.Flows())
	}
}

func TestParseRejectsJunk(t *testing.T) {
	frag := tcpPacket(home, remote, 5000, 443, tcpSYN)
	binary.BigEndian.PutUint16(frag[6:8], 100) // смещение фрагмента
	for name, p := range map[string][]byte{
		"пусто":    nil,
		"обрезок":  {0x45, 0, 0},
		"фрагмент": frag,
		"ICMP":     append([]byte{0x45, 0, 0, 28, 0, 0, 0, 0, 64, 1}, make([]byte, 18)...),
	} {
		if _, ok := parse(p); ok {
			t.Errorf("%s разобран", name)
		}
	}
}
