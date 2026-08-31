package vpn

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/user/amnezia-web-client/internal/config"
)

// newTestManager возвращает менеджер с мгновенными паузами: политику повторов
// проверяем логикой, а не ожиданием реальных секунд.
func newTestManager(t *testing.T) *Manager {
	t.Helper()

	m := NewManager()
	m.reconnectMin = time.Millisecond
	m.reconnectMax = 2 * time.Millisecond
	return m
}

// recordStates подписывается на смену состояний и возвращает функцию, отдающую
// накопленную последовательность. Оповещения асинхронны, поэтому чтение идёт
// под тем же мьютексом, что и запись.
func recordStates(m *Manager) func() []ConnectionState {
	var (
		mu     sync.Mutex
		states []ConnectionState
	)

	m.OnStatusChange(func(s ConnectionStatus) {
		mu.Lock()
		states = append(states, s.State)
		mu.Unlock()
	})

	return func() []ConnectionState {
		mu.Lock()
		defer mu.Unlock()
		return append([]ConnectionState(nil), states...)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("не дождались: %s", what)
}

// Ядро отвергло конфигурацию — повторять нечего: ошибка в самих параметрах,
// а не в сети.
func TestRetryLoopStopsOnFatalError(t *testing.T) {
	m := newTestManager(t)

	attempts := 0
	m.retryLoop(context.Background(), func(context.Context) (bool, error) {
		attempts++
		return false, fatalError{errors.New("ядро отвергло конфигурацию")}
	})

	if attempts != 1 {
		t.Errorf("попыток %d, ожидалась одна: отказ неисправим", attempts)
	}
	if st := m.GetStatus(); st.State != StateError {
		t.Errorf("состояние %q, ожидалось %q", st.State, StateError)
	}
}

// Соединение, не состоявшееся ни разу, скорее всего сломано по-настоящему.
// Стучаться в него вечно значит прятать причину отказа от пользователя.
func TestRetryLoopGivesUpWhenNeverEstablished(t *testing.T) {
	m := newTestManager(t)

	attempts := 0
	m.retryLoop(context.Background(), func(context.Context) (bool, error) {
		attempts++
		return false, errors.New("сервер не отвечает")
	})

	if attempts != initialConnectAttempts {
		t.Errorf("попыток %d, ожидалось %d", attempts, initialConnectAttempts)
	}

	st := m.GetStatus()
	if st.State != StateError {
		t.Errorf("состояние %q, ожидалось %q", st.State, StateError)
	}
	if st.Error == "" {
		t.Error("причина отказа потеряна — на экране будет одно слово «Ошибка»")
	}
	if st.Attempt != 0 {
		t.Errorf("счётчик попыток %d, после отказа он не нужен", st.Attempt)
	}
}

// Туннель, который однажды поднялся, доказал, что ключи верны и сервер жив.
// Дальше молчание — это сеть, и ждать её можно сколько угодно.
func TestRetryLoopRetriesForeverAfterEstablished(t *testing.T) {
	m := newTestManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const want = initialConnectAttempts + 5
	attempts := 0

	m.retryLoop(ctx, func(context.Context) (bool, error) {
		attempts++
		if attempts >= want {
			cancel()
		}
		// Рукопожатие состоялось, но связь оборвалась.
		return true, errors.New("сервер молчит")
	})

	if attempts < want {
		t.Errorf("попыток %d, ожидалось не меньше %d: после успеха предел не применяется", attempts, want)
	}
}

// Разрыв после успеха начинает отсчёт заново: следующий обрыв должен
// восстанавливаться быстро, а не наследовать предел от прошлых неудач.
func TestRetryLoopResetsCounterAfterSuccess(t *testing.T) {
	m := newTestManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	attempts := 0
	var seenAttempts []int

	m.retryLoop(ctx, func(context.Context) (bool, error) {
		attempts++
		seenAttempts = append(seenAttempts, m.GetStatus().Attempt)

		switch attempts {
		case 1, 2:
			return false, errors.New("сервер не отвечает") // две неудачи подряд
		case 3:
			return true, errors.New("сервер молчит") // подключились и оборвались
		}
		cancel()
		return false, errors.New("хватит")
	})

	if attempts < 4 {
		t.Fatalf("цикл прервался на %d попытке: успех должен был обнулить счётчик", attempts)
	}
	// После успеха номер следующей попытки снова 2, а не 4.
	if got := seenAttempts[3]; got != 2 {
		t.Errorf("номер попытки после успеха %d, ожидался 2: счётчик не обнулился", got)
	}
}

// Отключение — выполненная просьба, а не отказ: состояние ошибки не ставится.
func TestRetryLoopStopsOnDisconnect(t *testing.T) {
	m := newTestManager(t)
	ctx, cancel := context.WithCancel(context.Background())

	attempts := 0
	m.retryLoop(ctx, func(ctx context.Context) (bool, error) {
		attempts++
		cancel()
		return true, ctx.Err()
	})

	if attempts != 1 {
		t.Errorf("попыток %d, ожидалась одна", attempts)
	}
	if st := m.GetStatus(); st.State == StateError {
		t.Error("отключение записано как ошибка")
	}
}

// Между попытками пользователь должен видеть «Переподключение» и причину, а
// не «Подключение», как будто он сам только что нажал кнопку.
func TestRetryLoopReportsReconnecting(t *testing.T) {
	m := newTestManager(t)
	states := recordStates(m)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	attempts := 0
	m.retryLoop(ctx, func(context.Context) (bool, error) {
		attempts++
		if attempts >= 3 {
			cancel()
		}
		return true, errors.New("сервер молчит")
	})

	waitFor(t, "состояние «переподключение»", func() bool {
		for _, s := range states() {
			if s == StateReconnecting {
				return true
			}
		}
		return false
	})
}

// finish сохраняет причину отказа: иначе она пропадает с экрана раньше, чем
// пользователь успеет её прочитать.
func TestFinishKeepsErrorButClearsConnection(t *testing.T) {
	m := newTestManager(t)

	m.setError(errors.New("ключ больше не действителен"))
	m.finish()

	st := m.GetStatus()
	if st.State != StateError {
		t.Errorf("состояние %q, ожидалось %q", st.State, StateError)
	}
	if st.Error != "ключ больше не действителен" {
		t.Errorf("причина отказа потеряна: %q", st.Error)
	}
	if st.ConfigID != "" || st.ConnectedAt != nil {
		t.Errorf("остались следы соединения: %+v", st)
	}
}

// А явное отключение причину снимает: итог у него один.
func TestTeardownClearsErrorState(t *testing.T) {
	m := newTestManager(t)

	m.setError(errors.New("сервер не отвечает"))
	m.teardown()

	if st := m.GetStatus(); st.State != StateDisconnected || st.Error != "" {
		t.Errorf("после отключения состояние %+v, ожидалось чистое «Отключено»", st)
	}
}

// Без iproute2 туннель создастся, но ни адреса, ни маршруты на него не лягут.
// Повторять нечего: команда в системе сама не появится.
func TestConnectionFailsFastWithoutIproute2(t *testing.T) {
	m := newTestManager(t)
	m.ipToolErr = errors.New("не найдена команда ip")

	cfg := config.AmneziaWGConfig{Peers: []config.PeerConfig{{AllowedIPs: []string{"0.0.0.0/0"}}}}

	established, err := m.runConnection(context.Background(), &cfg, nil)

	if established {
		t.Error("соединение объявлено состоявшимся")
	}
	if err == nil {
		t.Fatal("отсутствие iproute2 прошло незамеченным")
	}
	if !isFatal(err) {
		t.Error("отказ признан временным — клиент будет повторять его вечно")
	}
}

// Адреса обеих версий протокола попадают в таблицу маршрутизации в виде
// одноадресных префиксов.
func TestHostPrefix(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7": "203.0.113.7/32",
		"2001:db8::1": "2001:db8::1/128",
	}

	for input, want := range cases {
		if got := hostPrefix(net.ParseIP(input)); got != want {
			t.Errorf("%s: %s, ожидалось %s", input, got, want)
		}
	}
}
