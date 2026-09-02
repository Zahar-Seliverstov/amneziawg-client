package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/user/amnezia-web-client/internal/autostart"
	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/desktopuser"
	"github.com/user/amnezia-web-client/internal/vpn"
)

func TestEndpointHostPort(t *testing.T) {
	cases := []struct{ endpoint, host, port string }{
		{"203.0.113.7:51820", "203.0.113.7", "51820"},
		{"vpn.example.com:443", "vpn.example.com", "443"},
		{"[2001:db8::1]:51820", "2001:db8::1", "51820"},
		{"203.0.113.7", "203.0.113.7", ""},
		{"", "", ""},
	}

	for _, c := range cases {
		cfg := &config.AmneziaWGConfig{Peers: []config.PeerConfig{{Endpoint: c.endpoint}}}
		host, port := endpointHostPort(cfg)
		if host != c.host || port != c.port {
			t.Errorf("%q: ожидалось (%q, %q), получено (%q, %q)", c.endpoint, c.host, c.port, host, port)
		}
	}
}

func TestPingPorts(t *testing.T) {
	// Порт эндпоинта идёт первым, дублей быть не должно.
	if got := pingPorts("51820"); len(got) != 3 || got[0] != "51820" {
		t.Fatalf("ожидался порт эндпоинта первым: %v", got)
	}
	if got := pingPorts(""); len(got) != 2 || got[0] != "443" {
		t.Fatalf("без порта эндпоинта ожидались только веб-порты: %v", got)
	}
}

func TestResolveEndpointPrefersIPv4(t *testing.T) {
	ip, err := resolveEndpoint("203.0.113.7")
	if err != nil || ip.String() != "203.0.113.7" {
		t.Fatalf("литерал не разобран: %v %v", ip, err)
	}
	if _, err := resolveEndpoint("несуществующий.invalid"); err == nil {
		t.Fatal("ожидалась ошибка на неразрешимом имени")
	}
}

// Закрытый порт отвечает RST — это такой же один оборот, что и SYN-ACK,
// и замер обязан его засчитать, а не признать сервер недоступным.
func TestTCPPingCountsRefused(t *testing.T) {
	// Слушаем и сразу закрываем: порт гарантированно свободен и закрыт.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	rtt, err := tcpPing(net.ParseIP("127.0.0.1"), []string{strconv.Itoa(port)})
	if err != nil {
		t.Fatalf("отказ в соединении должен считаться ответом: %v", err)
	}
	t.Logf("закрытый порт: rtt=%v", rtt)
}

// Открытый порт: обычное рукопожатие.
func TestTCPPingOpenPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	rtt, err := tcpPing(net.ParseIP("127.0.0.1"), []string{strconv.Itoa(l.Addr().(*net.TCPAddr).Port)})
	if err != nil {
		t.Fatalf("открытый порт не ответил: %v", err)
	}
	t.Logf("открытый порт: rtt=%v", rtt)
}

// Молчащий адрес: честная ошибка, а не выдуманное число.
func TestTCPPingFiltered(t *testing.T) {
	if testing.Short() {
		t.Skip("ждёт таймауты")
	}
	// TEST-NET-1 (RFC 5737): получателя нет, ответа не будет.
	if _, err := tcpPing(net.ParseIP("192.0.2.1"), []string{"51820"}); err == nil {
		t.Fatal("ожидалась ошибка на отфильтрованном адресе")
	}
}

func newTestServer(t *testing.T, cfgs ...config.AmneziaWGConfig) *Server {
	t.Helper()
	dir := t.TempDir()

	appCfg := config.NewAppConfig(filepath.Join(dir, "config.json"), desktopuser.User{})
	for _, c := range cfgs {
		appCfg.AddConfig(c)
	}
	if len(cfgs) > 0 {
		appCfg.SetSelectedConfig(cfgs[0].ID)
	}

	return NewServer(appCfg, vpn.NewManager(), autostart.NewManager(filepath.Join(dir, "a.desktop"), ""))
}

func doPing(t *testing.T, s *Server) pingResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handlePing(rec, httptest.NewRequest(http.MethodGet, "/api/ping", nil))

	var got pingResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("невалидный JSON: %v", err)
	}
	return got
}

func TestHandlePingNoConfig(t *testing.T) {
	if got := doPing(t, newTestServer(t)); got.Success || got.Error == "" {
		t.Fatalf("ожидалась ошибка, получено %+v", got)
	}
}

func TestHandlePingNoEndpoint(t *testing.T) {
	s := newTestServer(t, config.AmneziaWGConfig{ID: "c1"})
	if got := doPing(t, s); got.Success || got.Error == "" {
		t.Fatalf("ожидалась ошибка, получено %+v", got)
	}
}

func TestHandlePingReal(t *testing.T) {
	if testing.Short() {
		t.Skip("нужен интернет")
	}
	s := newTestServer(t, config.AmneziaWGConfig{
		ID:    "c1",
		Peers: []config.PeerConfig{{Endpoint: "1.1.1.1:443"}},
	})

	got := doPing(t, s)
	if !got.Success {
		t.Fatalf("замер не удался: %+v", got)
	}
	if got.Latency <= 0 || got.Latency > 2000 {
		t.Fatalf("неправдоподобная задержка: %v", got.Latency)
	}
	t.Logf("target=%s latency=%vms", got.Target, got.Latency)
}
