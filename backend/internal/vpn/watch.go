package vpn

// Наблюдение за живым туннелем: счётчики, рукопожатия и признание туннеля
// мёртвым.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/user/amnezia-web-client/internal/config"
)

// Тайминги ожидания рукопожатия и опроса счётчиков.
const (
	statsInterval = 3 * time.Second

	// graceWithoutRoutes — сколько ждём рукопожатия, НЕ трогая маршрутизацию,
	// когда у пира нет PersistentKeepalive. Такой пир начинает рукопожатие
	// только когда через туннель пойдёт трафик, а трафик пойдёт лишь по
	// маршрутам — иначе соединение не поднимется никогда.
	//
	// Если keepalive задан, этот запас НЕ применяется: ядро шлёт первый
	// пакет само, живой сервер отвечает за секунду, и ставить маршруты до
	// рукопожатия незачем. Ровно это однажды и увело весь трафик в
	// неподнятый туннель — интернет пропадал до самого отключения.
	graceWithoutRoutes = 10 * time.Second

	// handshakeTimeout — общий предел ожидания.
	handshakeTimeout = 45 * time.Second
)

// watchDevice ведёт соединение от поднятого интерфейса до рабочего туннеля:
// ждёт рукопожатия, включает маршрутизацию, а дальше следит, что туннель жив.
//
// Возвращает признак состоявшегося рукопожатия и причину завершения.
func (m *Manager) watchDevice(ctx context.Context, dev *device.Device, cfg *config.AmneziaWGConfig, routing *config.RoutingConfig) (bool, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	started := time.Now()
	routesUp := false
	established := false
	lastNotify := time.Time{}

	// Пир с keepalive обязан рукопожаться сам. Молчит — значит соединения
	// нет, и трогать маршрутизацию нельзя ни при каких обстоятельствах.
	waitsForTraffic := !hasKeepalive(cfg)

	// Маршруты ставим один раз, дальше только следим.
	bringUpTraffic := func() error {
		if routesUp {
			return nil
		}
		if err := m.configureTraffic(cfg, routing); err != nil {
			return fmt.Errorf("не удалось настроить маршрутизацию: %w", err)
		}
		routesUp = true
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return established, nil
		case <-dev.Wait():
			return established, errors.New("ядро закрыло туннель")
		case <-ticker.C:
		}

		state, err := dev.IpcGet()
		if err != nil {
			continue
		}
		stats := parseDeviceStats(state)

		if !established {
			if stats.LastHandshake.IsZero() {
				waited := time.Since(started)

				// Маршруты до рукопожатия ставим только ради пиров, которым
				// без трафика не с чего начать.
				if waitsForTraffic && waited >= graceWithoutRoutes {
					if err := bringUpTraffic(); err != nil {
						return false, err
					}
				}
				if waited >= handshakeTimeout {
					return false, fmt.Errorf("сервер не отвечает: рукопожатие не состоялось за %s. Проверьте конфигурацию — возможно, ключ больше не действителен", handshakeTimeout)
				}
				continue
			}

			// Рукопожатие есть — вот теперь соединение действительно живо.
			if err := bringUpTraffic(); err != nil {
				return false, err
			}

			established = true
			now := time.Now()

			m.mu.Lock()
			m.status.State = StateConnected
			m.status.ConnectedAt = &now
			// Причина прошлого разрыва больше не актуальна.
			m.status.Error = ""
			m.status.Attempt = 0
			m.mu.Unlock()

			// Смену состояния шлём немедленно, не дожидаясь очередного среза
			// счётчиков: пользователь ждёт именно её.
			lastNotify = time.Now()
			m.notifyStatusChange()
		}

		// Живой пир не молчит дольше deadPeerTimeout: ядро пересогласовывает
		// ключи само. Молчание сверх этого срока — разрыв, и признать его
		// нужно самим. Раньше здесь не было ничего: пропавший сервер оставлял
		// состояние «Подключено» навсегда, а трафик уходил в мёртвый туннель.
		if silence := time.Since(stats.LastHandshake); silence > deadPeerTimeout {
			return true, fmt.Errorf("сервер молчит %s", silence.Round(time.Second))
		}

		m.mu.Lock()
		m.status.BytesReceived = stats.RxBytes
		m.status.BytesSent = stats.TxBytes
		if !stats.LastHandshake.IsZero() {
			handshake := stats.LastHandshake
			m.status.LastHandshake = &handshake
		}
		m.mu.Unlock()

		// Счётчики шлём не каждую секунду: тикер здесь частый ради быстрой
		// реакции на рукопожатие, а рассылка статуса такой частоты не стоит.
		if time.Since(lastNotify) >= statsInterval {
			lastNotify = time.Now()
			m.notifyStatusChange()
		}
	}
}

// hasKeepalive сообщает, задан ли хотя бы у одного пира PersistentKeepalive.
// Значение бывает диапазоном ("25-35"), поэтому смотрим и исходную строку.
func hasKeepalive(cfg *config.AmneziaWGConfig) bool {
	for _, peer := range cfg.Peers {
		if peer.PersistentKeepalive > 0 || strings.TrimSpace(peer.PersistentKeepaliveRaw) != "" {
			return true
		}
	}
	return false
}
