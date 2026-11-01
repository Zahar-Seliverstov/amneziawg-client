package dnsproxy

import (
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Маршрут по ответу обязан встать раньше, чем программа получит адрес:
// иначе она успевает открыть соединение мимо туннеля, и правило по имени не
// действует на первое (а часто и единственное) соединение.
func TestObserverRunsBeforeClientGetsAnswer(t *testing.T) {
	upstream := upstreamTCP(t, func(q []byte) []byte { return answerWith(t, q, [4]byte{203, 0, 113, 7}) })

	var observed atomic.Bool
	proxy := New(func(Answer) {
		// Медленная установка маршрута: ответ клиенту обязан её дождаться.
		time.Sleep(50 * time.Millisecond)
		observed.Store(true)
	})
	if err := proxy.Start(netip.MustParseAddrPort("127.0.0.1:0"), []netip.AddrPort{upstream}); err != nil {
		t.Fatal(err)
	}
	defer proxy.Stop()

	askTCP(t, proxy.Addr(), "example.com.")
	if !observed.Load() {
		t.Fatal("клиент получил ответ раньше, чем наблюдатель поставил маршрут")
	}
}

// Вопрос AAAA об имени из фильтра получает пустой ответ, не доходя до
// вышестоящего сервера; остальные вопросы идут как обычно.
func TestDropAAAA(t *testing.T) {
	var asked atomic.Int32
	upstream := upstreamTCP(t, func(q []byte) []byte {
		asked.Add(1)
		return answerWith(t, q, [4]byte{203, 0, 113, 7})
	})

	proxy := New(nil)
	proxy.DropAAAA(func(name string) bool { return name == "blocked.example" })
	if err := proxy.Start(netip.MustParseAddrPort("127.0.0.1:0"), []netip.AddrPort{upstream}); err != nil {
		t.Fatal(err)
	}
	defer proxy.Stop()

	resp := askTCPType(t, proxy.Addr(), "blocked.example.", dnsmessage.TypeAAAA)
	var parser dnsmessage.Parser
	header, err := parser.Start(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !header.Response || header.RCode != dnsmessage.RCodeSuccess {
		t.Errorf("заголовок пустого ответа: %+v", header)
	}
	parser.SkipAllQuestions()
	if _, err := parser.AnswerHeader(); err != dnsmessage.ErrSectionDone {
		t.Errorf("в пустом ответе есть записи: %v", err)
	}
	if asked.Load() != 0 {
		t.Error("подавленный вопрос ушёл вышестоящему серверу")
	}

	askTCPType(t, proxy.Addr(), "blocked.example.", dnsmessage.TypeA)
	askTCPType(t, proxy.Addr(), "other.example.", dnsmessage.TypeAAAA)
	if asked.Load() != 2 {
		t.Errorf("вышестоящему ушло %d вопросов, ожидалось 2: A того же имени и AAAA чужого", asked.Load())
	}
}

func askTCPType(t *testing.T, addr netip.AddrPort, name string, qtype dnsmessage.Type) []byte {
	t.Helper()

	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 7, RecursionDesired: true})
	builder.StartQuestions()
	builder.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: qtype, Class: dnsmessage.ClassINET})
	query, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}

	conn := dialTCP(t, addr)
	defer conn.Close()
	if err := writeMessage(conn, query); err != nil {
		t.Fatal(err)
	}
	resp, err := readMessage(conn)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func dialTCP(t *testing.T, addr netip.AddrPort) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr.String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	return conn
}
