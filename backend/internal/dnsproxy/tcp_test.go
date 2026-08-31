package dnsproxy

import (
	"encoding/binary"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// upstreamTCP поднимает подставной вышестоящий сервер, отвечающий по TCP.
func upstreamTCP(t *testing.T, reply func(query []byte) []byte) netip.AddrPort {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("слушатель: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				for {
					conn.SetDeadline(time.Now().Add(2 * time.Second))
					query, err := readMessage(conn)
					if err != nil {
						return
					}
					if err := writeMessage(conn, reply(query)); err != nil {
						return
					}
				}
			}()
		}
	}()

	return netip.MustParseAddrPort(ln.Addr().String())
}

// askTCP задаёт вопрос посреднику по TCP и возвращает ответ.
func askTCP(t *testing.T, addr netip.AddrPort, name string) []byte {
	t.Helper()

	conn, err := net.DialTimeout("tcp", addr.String(), 2*time.Second)
	if err != nil {
		t.Fatalf("соединение с посредником: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))

	if err := writeMessage(conn, buildQuery(t, name)); err != nil {
		t.Fatalf("отправка вопроса: %v", err)
	}

	response, err := readMessage(conn)
	if err != nil {
		t.Fatalf("чтение ответа: %v", err)
	}
	return response
}

func buildQuery(t *testing.T, name string) []byte {
	t.Helper()

	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 1, RecursionDesired: true})
	if err := builder.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	err := builder.Question(dnsmessage.Question{
		Name:  dnsmessage.MustNewName(name),
		Type:  dnsmessage.TypeA,
		Class: dnsmessage.ClassINET,
	})
	if err != nil {
		t.Fatal(err)
	}

	msg, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func answerWith(t *testing.T, query []byte, addrs ...[4]byte) []byte {
	t.Helper()

	var parser dnsmessage.Parser
	header, err := parser.Start(query)
	if err != nil {
		t.Fatal(err)
	}
	question, err := parser.Question()
	if err != nil {
		t.Fatal(err)
	}

	header.Response = true
	builder := dnsmessage.NewBuilder(nil, header)
	if err := builder.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := builder.Question(question); err != nil {
		t.Fatal(err)
	}
	if err := builder.StartAnswers(); err != nil {
		t.Fatal(err)
	}

	for _, a := range addrs {
		res := dnsmessage.ResourceHeader{
			Name:  question.Name,
			Type:  dnsmessage.TypeA,
			Class: dnsmessage.ClassINET,
			TTL:   300,
		}
		if err := builder.AResource(res, dnsmessage.AResource{A: a}); err != nil {
			t.Fatal(err)
		}
	}

	msg, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

// Резолвер приходит по TCP, когда ответ не поместился в датаграмму. Раньше он
// упирался в закрытый порт, и имя не разрешалось вовсе.
func TestProxyServesTCP(t *testing.T) {
	var (
		mu      sync.Mutex
		seen    []Answer
		queries int
	)

	up := upstreamTCP(t, func(query []byte) []byte {
		mu.Lock()
		queries++
		mu.Unlock()
		return answerWith(t, query, [4]byte{203, 0, 113, 7})
	})

	proxy := New(func(a Answer) {
		mu.Lock()
		seen = append(seen, a)
		mu.Unlock()
	})
	if err := proxy.Start(netip.MustParseAddrPort("127.0.0.1:0"), []netip.AddrPort{up}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proxy.Stop()

	response := askTCP(t, proxy.Addr(), "example.com.")

	answer, ok := parseAnswer(response)
	if !ok {
		t.Fatal("ответ по TCP не разобрался")
	}
	if answer.Name != "example.com" || len(answer.Addrs) != 1 {
		t.Fatalf("разобрано неверно: %+v", answer)
	}

	// Наблюдатель обязан срабатывать одинаково на обоих транспортах: иначе
	// правило по имени работало бы через раз.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(seen)
		mu.Unlock()
		if got > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("наблюдатель не вызван для ответа по TCP")
}

// По одному соединению резолвер задаёт несколько вопросов подряд — этим
// пользуются и glibc, и systemd-resolved.
func TestProxyHandlesPipelinedTCPQueries(t *testing.T) {
	up := upstreamTCP(t, func(query []byte) []byte {
		return answerWith(t, query, [4]byte{203, 0, 113, 7})
	})

	proxy := New(nil)
	if err := proxy.Start(netip.MustParseAddrPort("127.0.0.1:0"), []netip.AddrPort{up}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proxy.Stop()

	conn, err := net.DialTimeout("tcp", proxy.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("соединение: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))

	for i := 0; i < 3; i++ {
		if err := writeMessage(conn, buildQuery(t, "example.com.")); err != nil {
			t.Fatalf("вопрос %d: %v", i+1, err)
		}
		if _, err := readMessage(conn); err != nil {
			t.Fatalf("ответ %d: %v", i+1, err)
		}
	}
}

// Молчащий вышестоящий сервер не должен оставлять резолвер ждать таймаута:
// отказ приходит сразу.
func TestProxyRefusesWhenUpstreamIsDeadTCP(t *testing.T) {
	// Порт, который заведомо никто не слушает.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := netip.MustParseAddrPort(ln.Addr().String())
	ln.Close()

	proxy := New(nil)
	if err := proxy.Start(netip.MustParseAddrPort("127.0.0.1:0"), []netip.AddrPort{dead}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proxy.Stop()

	response := askTCP(t, proxy.Addr(), "example.com.")

	var parser dnsmessage.Parser
	header, err := parser.Start(response)
	if err != nil {
		t.Fatalf("ответ не разобрался: %v", err)
	}
	if header.RCode != dnsmessage.RCodeServerFailure {
		t.Errorf("код ответа %v, ожидался SERVFAIL", header.RCode)
	}
}

// Формат TCP: два байта длины перед сообщением. Ошибка здесь ломает всё
// молча — разбор поедет по границам сообщений.
func TestMessageFraming(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	payload := []byte(strings.Repeat("x", 300))

	go func() {
		writeMessage(client, payload)
	}()

	got, err := readMessage(server)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("прочитано %d байт вместо %d", len(got), len(payload))
	}
}

func TestReadMessageRejectsEmpty(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		var header [2]byte
		binary.BigEndian.PutUint16(header[:], 0)
		client.Write(header[:])
	}()

	if _, err := readMessage(server); err == nil {
		t.Error("сообщение нулевой длины принято")
	}
}

// Посредник, который не смог занять TCP, не должен подниматься вовсе: он
// отвечал бы на обычные запросы и молча терял усечённые.
func TestStartFailsWhenTCPPortIsTaken(t *testing.T) {
	// Занимаем TCP-порт, оставляя UDP свободным.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	listen := netip.MustParseAddrPort(ln.Addr().String())
	proxy := New(nil)

	err = proxy.Start(listen, []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:53")})
	if err == nil {
		proxy.Stop()
		t.Fatal("посредник поднялся без TCP")
	}
	if !strings.Contains(err.Error(), "TCP") {
		t.Errorf("причина отказа не названа: %v", err)
	}
}
