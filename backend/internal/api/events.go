package api

import (
	"encoding/json"
	"net/http"

	"github.com/user/amnezia-web-client/internal/vpn"
)

// Поток изменений статуса подключения.
//
// Раньше здесь был WebSocket — он был нужен, пока за статусом следила
// страница в браузере. Теперь единственный подписчик это оболочка рабочего
// стола, и она ходит по unix-сокету, где нет ни origin, ни рукопожатия, ни
// нужды удерживать соединение пингами. Осталось то, что и требовалось:
// открытый ответ, в который построчно пишется JSON (NDJSON).
//
// Направление одностороннее: команды приходят обычными запросами, а сюда
// только уходят обновления.
const (
	// eventBuffer — сколько обновлений держим для подписчика, который не
	// успевает читать. Восьми хватает на любую цепочку смен состояния при
	// подключении; переполнение вытесняет самое старое, потому что новое
	// состояние отменяет предыдущее, а не дополняет его.
	eventBuffer = 8

	// maxEventClients ограничивает число подписчиков. В норме он ровно один
	// (оболочка); всё сверх — либо утечка соединений, либо попытка занять
	// память backend'а, работающего от root.
	maxEventClients = 8
)

// eventClient — один открытый поток.
type eventClient struct {
	updates chan vpn.ConnectionStatus
}

// handleEvents держит ответ открытым и пишет в него по строке на изменение.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	// Без Flusher поток бессмыслен: ответ осел бы в буфере и ушёл только при
	// закрытии соединения, то есть никогда.
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, "Поток событий недоступен", http.StatusInternalServerError)
		return
	}

	client, ok := s.addEventClient()
	if !ok {
		jsonError(w, "Слишком много подписчиков", http.StatusServiceUnavailable)
		return
	}
	defer s.dropEventClient(client)

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	encoder := json.NewEncoder(w)

	// Первой строкой — текущее состояние. Иначе подписчик не знал бы о
	// происходящем до первого изменения, а его может не быть часами.
	if err := encoder.Encode(s.vpnManager.GetStatus()); err != nil {
		return
	}
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return

		case status := <-client.updates:
			if err := encoder.Encode(status); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// addEventClient заводит подписчика. ok=false — мест больше нет.
func (s *Server) addEventClient() (*eventClient, bool) {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()

	if len(s.eventClients) >= maxEventClients {
		return nil, false
	}

	client := &eventClient{updates: make(chan vpn.ConnectionStatus, eventBuffer)}
	s.eventClients[client] = true

	return client, true
}

func (s *Server) dropEventClient(client *eventClient) {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()

	delete(s.eventClients, client)
}

// broadcastStatus раздаёт обновление подписчикам.
//
// Отправка неблокирующая: обработчик статуса зовут из менеджера VPN, и
// застрять здесь значило бы задержать само подключение.
func (s *Server) broadcastStatus(status vpn.ConnectionStatus) {
	s.eventsMu.RLock()
	defer s.eventsMu.RUnlock()

	for client := range s.eventClients {
		pushStatus(client.updates, status)
	}
}

// pushStatus кладёт обновление, вытесняя самое старое, если очередь полна.
func pushStatus(updates chan vpn.ConnectionStatus, status vpn.ConnectionStatus) {
	for {
		select {
		case updates <- status:
			return
		default:
		}

		select {
		case <-updates:
		default:
			// Очередь опустошил сам подписчик — пробуем записать снова.
		}
	}
}
