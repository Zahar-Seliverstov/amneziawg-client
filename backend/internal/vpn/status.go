package vpn

// Состояние соединения и оповещение о его изменениях.
//
// Подписчики (API, а через него — окно) получают состояние из своей горутины,
// поэтому оповещение идёт очередью: вызывать обработчик прямо под замком
// менеджера значит дать чужому коду остановить соединение.

import (
	"log"
	"time"
)

// ConnectionState represents the VPN connection state
type ConnectionState string

const (
	StateDisconnected ConnectionState = "disconnected"
	StateConnecting   ConnectionState = "connecting"
	StateConnected    ConnectionState = "connected"
	// StateReconnecting — туннель был поднят и оборвался, идёт восстановление.
	// Отдельное состояние, а не «подключение»: пользователь должен видеть, что
	// связь потеряна, а не что он сам только что нажал кнопку.
	StateReconnecting  ConnectionState = "reconnecting"
	StateDisconnecting ConnectionState = "disconnecting"
	StateError         ConnectionState = "error"
)

// ConnectionStatus holds detailed connection status
type ConnectionStatus struct {
	State       ConnectionState `json:"state"`
	ConfigID    string          `json:"config_id,omitempty"`
	ConfigName  string          `json:"config_name,omitempty"`
	ConnectedAt *time.Time      `json:"connected_at,omitempty"`
	Error       string          `json:"error,omitempty"`
	Interface   string          `json:"interface,omitempty"`

	// Attempt — номер идущей попытки подключения. Ноль, когда соединение
	// установлено или его нет вовсе.
	Attempt int `json:"attempt,omitempty"`

	// KillSwitch — блокировка трафика мимо туннеля сейчас действует.
	// Важна именно во время разрыва: без неё «Переподключение» выглядит
	// одинаково и когда трафик закрыт, и когда он утекает открытым.
	KillSwitch bool `json:"kill_switch"`

	// Statistics
	BytesReceived uint64     `json:"bytes_received"`
	BytesSent     uint64     `json:"bytes_sent"`
	LastHandshake *time.Time `json:"last_handshake,omitempty"`
}

// StatusCallback is called when connection status changes
type StatusCallback func(status ConnectionStatus)

// statusQueue — глубина очереди оповещений. За соединение их единицы плюс
// счётчики раз в несколько секунд, так что запас огромен: если очередь всё же
// переполнилась, значит подписчик не разбирает её вовсе.
const statusQueue = 64

// OnStatusChange registers a callback for status changes
func (m *Manager) OnStatusChange(callback StatusCallback) {
	m.callbackMu.Lock()
	defer m.callbackMu.Unlock()
	m.statusCallbacks = append(m.statusCallbacks, callback)
}

// notifyStatusChange ставит текущее состояние в очередь на рассылку.
//
// Постановка в очередь, а не прямой вызов: подписчик пишет в открытый поток
// событий, и не читающий его клиент задерживал бы сам туннель.
func (m *Manager) notifyStatusChange() {
	select {
	case m.notify <- m.GetStatus():
	default:
		log.Printf("Очередь оповещений переполнена — состояние пропущено")
	}
}

// notifyLoop разбирает очередь оповещений.
//
// Одна горутина и строгий порядок принципиальны. Раньше на каждого подписчика
// запускалась своя, и порядок доставки ничем не удерживался: «подключено»
// могло прийти в интерфейс ПОСЛЕ «отключено», и кнопка залипала в неверном
// состоянии до следующего события.
func (m *Manager) notifyLoop() {
	for status := range m.notify {
		m.callbackMu.RLock()
		callbacks := m.statusCallbacks
		m.callbackMu.RUnlock()

		for _, cb := range callbacks {
			cb(status)
		}
	}
}

// GetStatus returns the current connection status
func (m *Manager) GetStatus() ConnectionStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

// setReconnecting отмечает потерю связи и начало новой попытки. Причина
// остаётся в статусе: без неё на экране «Переподключение» без объяснения,
// почему связь вообще пропала.
func (m *Manager) setReconnecting(cause error, attempt int) {
	m.mu.Lock()
	m.status.State = StateReconnecting
	m.status.Error = cause.Error()
	m.status.Attempt = attempt
	m.status.ConnectedAt = nil
	m.mu.Unlock()

	m.notifyStatusChange()
}

// setError sets an error state
func (m *Manager) setError(err error) {
	log.Printf("VPN error: %v", err)

	m.mu.Lock()
	m.status.State = StateError
	m.status.Error = err.Error()
	m.status.Attempt = 0
	m.mu.Unlock()

	m.notifyStatusChange()
}
