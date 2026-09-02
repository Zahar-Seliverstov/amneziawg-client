package api

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/user/amnezia-web-client/internal/vpn"
)

// Поток обязан сразу назвать текущее состояние: подписчик не должен ждать
// первого изменения, чтобы узнать, подключён ли VPN.
func TestEventsSendsCurrentStatusFirst(t *testing.T) {
	s := newTestServer(t)

	srv := httptest.NewServer(s)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/vpn/events")
	if err != nil {
		t.Fatalf("поток не открылся: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("код ответа %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)

	first := readStatus(t, reader)
	if first.State != vpn.StateDisconnected {
		t.Errorf("первое состояние %q, ожидалось %q", first.State, vpn.StateDisconnected)
	}

	// Изменение приходит в тот же поток, без повторного запроса.
	s.broadcastStatus(vpn.ConnectionStatus{State: vpn.StateConnected, ConfigName: "тест"})

	second := readStatus(t, reader)
	if second.State != vpn.StateConnected || second.ConfigName != "тест" {
		t.Errorf("второе сообщение: %+v", second)
	}
}

// Подписчик, который не читает, не должен задерживать сам VPN: рассылка
// обязана вернуться немедленно, даже когда очередь переполнена.
func TestBroadcastNeverBlocks(t *testing.T) {
	s := newTestServer(t)

	client, ok := s.addEventClient()
	if !ok {
		t.Fatal("подписчик не завёлся")
	}
	defer s.dropEventClient(client)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < eventBuffer*4; i++ {
			s.broadcastStatus(vpn.ConnectionStatus{State: vpn.StateConnecting})
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("рассылка застряла на медленном подписчике")
	}

	// Вытесняется самое старое, поэтому последнее состояние на месте.
	s.broadcastStatus(vpn.ConnectionStatus{State: vpn.StateConnected})

	var last vpn.ConnectionStatus
	for len(client.updates) > 0 {
		last = <-client.updates
	}
	if last.State != vpn.StateConnected {
		t.Errorf("последнее состояние в очереди %q", last.State)
	}
}

// Мест конечное число: иначе поток соединений от локального процесса съел бы
// память backend'а, работающего от root.
func TestEventClientsAreLimited(t *testing.T) {
	s := newTestServer(t)

	for i := 0; i < maxEventClients; i++ {
		if _, ok := s.addEventClient(); !ok {
			t.Fatalf("подписчик %d не завёлся", i)
		}
	}

	if _, ok := s.addEventClient(); ok {
		t.Error("подписчик сверх предела был принят")
	}
}

func readStatus(t *testing.T, reader *bufio.Reader) vpn.ConnectionStatus {
	t.Helper()

	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("строка потока не прочиталась: %v", err)
	}

	var status vpn.ConnectionStatus
	if err := json.Unmarshal(line, &status); err != nil {
		t.Fatalf("строка потока не разобралась: %v (%s)", err, line)
	}
	return status
}
