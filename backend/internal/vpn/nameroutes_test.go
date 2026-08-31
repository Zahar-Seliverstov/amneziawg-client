package vpn

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// fakeDNS поднимает сервер имён, который на любой запрос A отвечает заданным
// адресом. Возвращает его адрес и функцию остановки.
func fakeDNS(t *testing.T, answer netip.Addr) (netip.AddrPort, func()) {
	t.Helper()

	conn, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)

		for {
			n, client, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}

			var parser dnsmessage.Parser
			header, err := parser.Start(buf[:n])
			if err != nil {
				continue
			}
			question, err := parser.Question()
			if err != nil {
				continue
			}

			header.Response = true
			builder := dnsmessage.NewBuilder(nil, header)
			builder.StartQuestions()
			builder.Question(question)
			builder.StartAnswers()
			builder.AResource(
				dnsmessage.ResourceHeader{Name: question.Name, Class: dnsmessage.ClassINET, TTL: 300},
				dnsmessage.AResource{A: answer.As4()},
			)

			msg, err := builder.Finish()
			if err != nil {
				continue
			}
			conn.WriteToUDP(msg, client)
		}
	}()

	return conn.LocalAddr().(*net.UDPAddr).AddrPort(), func() {
		conn.Close()
		<-done
	}
}

// Регрессия. Правила по доменам должны резолвиться через серверы имён из
// конфига, а НЕ через системный резолвер.
//
// Почему это важно: пересборка правил на живом туннеле начинается с остановки
// нашего посредника DNS, а resolv.conf в этот момент всё ещё указывает на
// него. Пока резолв шёл через систему, запрос уходил в мёртвый сокет, и все
// правила по доменам оставались без маршрутов — со стороны выглядело как
// «удалил одно правило, отвалились все остальные».
func TestResolverForUsesConfiguredServers(t *testing.T) {
	want := netip.MustParseAddr("203.0.113.99")
	server, stop := fakeDNS(t, want)
	defer stop()

	resolver := resolverFor([]netip.AddrPort{server})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ips, err := resolver.LookupIP(ctx, "ip4", "example.test")
	if err != nil {
		t.Fatalf("резолв через заданный сервер не удался: %v", err)
	}
	if len(ips) != 1 || ips[0].String() != want.String() {
		t.Fatalf("получили %v, ждали [%v]", ips, want)
	}
}

// Без серверов в конфиге остаётся системный резолвер — иначе имена вообще
// перестали бы разрешаться.
func TestResolverForFallsBackToSystem(t *testing.T) {
	if got := resolverFor(nil); got != net.DefaultResolver {
		t.Error("при пустом списке серверов должен возвращаться системный резолвер")
	}
	if got := resolverFor(parseDNSServers([]string{"не адрес"})); got != net.DefaultResolver {
		t.Error("при нечитаемых адресах должен возвращаться системный резолвер")
	}
}

func TestParseDNSServersAddsPort53(t *testing.T) {
	got := parseDNSServers([]string{"1.1.1.1", " 8.8.8.8 ", "мусор", ""})
	want := []netip.AddrPort{
		netip.MustParseAddrPort("1.1.1.1:53"),
		netip.MustParseAddrPort("8.8.8.8:53"),
	}

	if len(got) != len(want) {
		t.Fatalf("получили %v, ждали %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("сервер %d: получили %v, ждали %v", i, got[i], want[i])
		}
	}
}

func TestTunnelAddrPicksFirstIPv4(t *testing.T) {
	addr, ok := tunnelAddr([]string{"fd00::1/64", "10.8.1.17/32"})
	if !ok || addr.String() != "10.8.1.17" {
		t.Errorf("получили %v (%v), ждали 10.8.1.17", addr, ok)
	}

	if _, ok := tunnelAddr([]string{"fd00::1/64"}); ok {
		t.Error("без адреса IPv4 функция должна сообщать о неудаче")
	}
}
