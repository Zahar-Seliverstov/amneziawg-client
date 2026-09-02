package vpn

// Политика повторов: что считается неисправимым, сколько ждать между
// попытками и когда прекращать.

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/user/amnezia-web-client/internal/config"
)

// Пределы переподключения.
const (
	// deadPeerTimeout — сколько молчания считаем разрывом. Ядро WireGuard
	// пересогласовывает ключи каждые две минуты, поэтому живой пир не молчит
	// дольше этого срока при любой нагрузке; три минуты — уверенный разрыв,
	// а не задержка в сети.
	deadPeerTimeout = 180 * time.Second

	reconnectMinDelay = 1 * time.Second
	reconnectMaxDelay = 60 * time.Second

	// initialConnectAttempts — сколько раз пробуем поднять соединение,
	// которое ещё ни разу не состоялось.
	//
	// Здесь предел нужен, а при разрыве уже работавшего туннеля — нет. Разница
	// принципиальная: туннель, который однажды поднялся, доказал, что и ключи
	// верны, и сервер жив, поэтому молчание — это сеть, и ждать её можно
	// сколько угодно. Соединение, не состоявшееся ни разу, скорее всего
	// сломано по-настоящему (истёк ключ, сменился адрес сервера), и вечно
	// стучаться в него значит прятать от пользователя причину отказа.
	initialConnectAttempts = 3
)

// fatalError — отказ, который повторной попыткой не лечится.
type fatalError struct{ err error }

func (e fatalError) Error() string { return e.err.Error() }

func (e fatalError) Unwrap() error { return e.err }

func isFatal(err error) bool {
	var f fatalError
	return errors.As(err, &f)
}

// supervise держит соединение живым: поднимает туннель, а когда тот падает —
// поднимает заново.
//
// Раньше переподключения не было вовсе. После рукопожатия наблюдатель только
// считал байты, поэтому пропавший сервер, отозванный ключ или смена сети
// оставляли состояние «Подключено» навсегда: маршруты стоят, трафик уходит в
// никуда, и ни ошибки, ни попытки восстановиться.
func (m *Manager) supervise(ctx context.Context, cfg *config.AmneziaWGConfig, routing *config.RoutingConfig) {
	defer m.finish()

	m.retryLoop(ctx, func(ctx context.Context) (bool, error) {
		return m.runConnection(ctx, cfg, routing)
	})
}

// connectAttempt — одна попытка поднять туннель. Возвращает признак
// состоявшегося рукопожатия и причину, по которой всё закончилось.
//
// Отдельный тип, а не прямой вызов runConnection: политика повторов —
// самая тонкая часть менеджера, а проверить её иначе можно было бы только
// создавая настоящий TUN, для которого нужен root.
type connectAttempt func(ctx context.Context) (established bool, err error)

// retryLoop повторяет попытки, пока это имеет смысл.
func (m *Manager) retryLoop(ctx context.Context, attempt connectAttempt) {
	everEstablished := false
	delay := m.reconnectMin

	for n := 1; ; n++ {
		established, err := attempt(ctx)
		everEstablished = everEstablished || established

		// Отключили — это не отказ, а выполненная просьба.
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = errors.New("соединение прекратилось без объяснения")
		}

		// Состоявшееся соединение обнуляет и счётчик, и паузу: следующий
		// разрыв должен восстанавливаться быстро, а не наследовать минуту
		// ожидания от прошлого раза.
		if established {
			n = 1
			delay = m.reconnectMin
		}

		if isFatal(err) || (!everEstablished && n >= initialConnectAttempts) {
			m.setError(err)
			return
		}

		m.setReconnecting(err, n+1)
		log.Printf("Соединение потеряно (%v). Следующая попытка через %s", err, delay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		delay = min(delay*2, m.reconnectMax)
	}
}
