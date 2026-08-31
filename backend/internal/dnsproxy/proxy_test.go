package dnsproxy

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// buildResponse собирает ответ DNS с заданными адресами — заменяет живой
// сервер имён в тестах.
func buildResponse(t *testing.T, name string, ttl uint32, addrs ...netip.Addr) []byte {
	t.Helper()

	dnsName, err := dnsmessage.NewName(name + ".")
	if err != nil {
		t.Fatalf("имя %q: %v", name, err)
	}
	question := dnsmessage.Question{Name: dnsName, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}

	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 1, Response: true})
	if err := builder.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := builder.Question(question); err != nil {
		t.Fatal(err)
	}
	if err := builder.StartAnswers(); err != nil {
		t.Fatal(err)
	}

	for _, addr := range addrs {
		header := dnsmessage.ResourceHeader{Name: dnsName, Class: dnsmessage.ClassINET, TTL: ttl}
		if addr.Is4() {
			if err := builder.AResource(header, dnsmessage.AResource{A: addr.As4()}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := builder.AAAAResource(header, dnsmessage.AAAAResource{AAAA: addr.As16()}); err != nil {
			t.Fatal(err)
		}
	}

	msg, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func TestParseAnswerExtractsAddresses(t *testing.T) {
	v4 := netip.MustParseAddr("93.184.216.34")
	v6 := netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946")

	answer, ok := parseAnswer(buildResponse(t, "example.com", 300, v4, v6))
	if !ok {
		t.Fatal("адреса должны были найтись")
	}
	if answer.Name != "example.com" {
		t.Errorf("имя: получили %q, ждали example.com", answer.Name)
	}
	if len(answer.Addrs) != 2 || answer.Addrs[0] != v4 || answer.Addrs[1] != v6 {
		t.Errorf("адреса: получили %v", answer.Addrs)
	}
	if answer.TTL != 300*time.Second {
		t.Errorf("TTL: получили %v, ждали 5m", answer.TTL)
	}
}

// Короткий TTL поднимается до минимума: маршрут на пару секунд бесполезен,
// а таблицу маршрутизации дёргает.
func TestParseAnswerClampsTTL(t *testing.T) {
	addr := netip.MustParseAddr("1.2.3.4")

	short, _ := parseAnswer(buildResponse(t, "a.test", 5, addr))
	if short.TTL != minTTL {
		t.Errorf("короткий TTL: получили %v, ждали %v", short.TTL, minTTL)
	}

	long, _ := parseAnswer(buildResponse(t, "b.test", 999999, addr))
	if long.TTL != maxTTL {
		t.Errorf("длинный TTL: получили %v, ждали %v", long.TTL, maxTTL)
	}
}

func TestParseAnswerIgnoresEmptyAndBroken(t *testing.T) {
	if _, ok := parseAnswer(buildResponse(t, "empty.test", 300)); ok {
		t.Error("ответ без адресов не должен считаться полезным")
	}
	if _, ok := parseAnswer([]byte{0x00, 0x01, 0x02}); ok {
		t.Error("обрывок сообщения не должен разбираться")
	}
	if _, ok := parseAnswer(nil); ok {
		t.Error("пустое сообщение не должно разбираться")
	}
}

// Сквозная проверка: поднимаем фальшивый вышестоящий сервер, пропускаем
// через посредника настоящий запрос и убеждаемся, что клиент получил ответ,
// а наблюдатель — адреса.
func TestProxyForwardsAndObserves(t *testing.T) {
	addr := netip.MustParseAddr("203.0.113.7")

	upstream, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()

	go func() {
		buf := make([]byte, maxMessage)
		n, client, err := upstream.ReadFromUDP(buf)
		if err != nil {
			return
		}
		// Отвечаем тем же идентификатором, что пришёл в запросе.
		response := buildResponse(t, "example.com", 120, addr)
		response[0], response[1] = buf[0], buf[1]
		_ = n
		upstream.WriteToUDP(response, client)
	}()

	observed := make(chan Answer, 1)
	proxy := New(func(a Answer) { observed <- a })

	if err := proxy.Start(netip.MustParseAddrPort("127.0.0.1:0"), []netip.AddrPort{upstream.LocalAddr().(*net.UDPAddr).AddrPort()}); err != nil {
		t.Fatal(err)
	}
	defer proxy.Stop()

	client, err := net.DialUDP("udp", nil, proxy.conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	query := buildResponse(t, "example.com", 0)
	query[2] = 0 // снимаем флаг ответа: это запрос
	if _, err := client.Write(query); err != nil {
		t.Fatal(err)
	}

	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, maxMessage)
	if _, err := client.Read(buf); err != nil {
		t.Fatalf("клиент не получил ответ: %v", err)
	}

	select {
	case answer := <-observed:
		if answer.Name != "example.com" || len(answer.Addrs) != 1 || answer.Addrs[0] != addr {
			t.Errorf("наблюдатель получил %+v", answer)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("наблюдатель не был вызван")
	}
}

func TestProxyRejectsEmptyUpstream(t *testing.T) {
	proxy := New(nil)
	if err := proxy.Start(netip.MustParseAddrPort("127.0.0.1:0"), nil); err == nil {
		t.Error("без вышестоящих серверов посредник обязан отказаться стартовать")
	}
}
