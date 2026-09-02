package vpn

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/conn"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/dnsproxy"
	"github.com/user/amnezia-web-client/internal/firewall"
	"github.com/user/amnezia-web-client/internal/iproute"
	"github.com/user/amnezia-web-client/internal/routing"
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

// Manager manages VPN connections
type Manager struct {
	mu sync.RWMutex

	status        ConnectionStatus
	activeConfig  *config.AmneziaWGConfig
	routingConfig *config.RoutingConfig

	// Туннель работает внутри этого же процесса: ядро amneziawg-go
	// подключено библиотекой, а не запущено отдельным бинарником.
	dev    *device.Device
	cancel context.CancelFunc

	// done закрывается, когда горутина соединения полностью завершилась.
	// Без ожидания этого сигнала уборка прошлого туннеля успевала снести
	// маршруты уже нового — при переключении конфига на живом соединении.
	done chan struct{}

	// connectMu сериализует подключение и отключение между собой.
	connectMu sync.Mutex

	// interfaceName — имя интерфейса. Меняется один раз, при создании TUN
	// (ядро вправе выдать не то имя, которое просили), но читается из чужих
	// горутин, поэтому живёт под mu — см. ifname.
	interfaceName string

	// Callbacks
	statusCallbacks []StatusCallback
	callbackMu      sync.RWMutex

	// notify — очередь оповещений о смене состояния, см. notifyLoop.
	notify chan ConnectionStatus

	// routeMu защищает всё, что трогает таблицу маршрутизации и системный DNS:
	// и учёт installedRoutes, и саму последовательность «снять старое —
	// поставить новое». Без этого пересборка правил из интерфейса могла
	// вклиниться в первичную настройку маршрутов при подключении.
	routeMu sync.Mutex

	// installedRoutes — маршруты, которые поставили мы сами. Нужны, чтобы при
	// изменении правил снять ровно свои записи и не тронуть чужие.
	// Только под routeMu.
	installedRoutes [][]string

	// dns подменяет системные серверы имён на время соединения.
	dns dnsControl

	// ip — команда iproute2 за интерфейсом: адреса, маршруты и опрос таблицы
	// маршрутизации. Поле, а не прямые вызовы exec, — иначе решения о
	// маршрутах можно было бы проверить только от root на живой машине.
	ip iproute.Tool

	// ipToolErr — почему недоступна команда ip. Ею ставятся адреса и
	// маршруты, и без неё туннель поднимется, но трафик в него не пойдёт.
	ipToolErr error

	// Блокировка трафика мимо туннеля. driver — чем блокируем (nil, если в
	// системе нечем), driverErr — почему нечем, killSwitchOn — настройка
	// пользователя. Всё под mu; сами вызовы Apply и Clear идут под routeMu
	// вместе с маршрутами.
	firewallDriver firewall.Driver
	firewallErr    error
	killSwitchOn   bool

	// Паузы между попытками переподключения. Поля, а не константы: иначе
	// проверить политику повторов можно было бы только реальным ожиданием
	// в секундах.
	reconnectMin, reconnectMax time.Duration

	// Маршрутизация по доменам и зонам — см. nameroutes.go. Живёт только
	// пока поднят туннель, поэтому хранится здесь, а не создаётся в NewManager.
	dnsProxy      *dnsproxy.Proxy
	dynamicRoutes *routing.DynamicSet
	sweepStop     chan struct{}
}

// statusQueue — глубина очереди оповещений. За соединение их единицы плюс
// счётчики раз в несколько секунд, так что запас огромен: если очередь всё же
// переполнилась, значит подписчик не разбирает её вовсе.
const statusQueue = 64

// NewManager creates a new VPN manager
func NewManager() *Manager {
	m := &Manager{
		status: ConnectionStatus{
			State: StateDisconnected,
		},
		interfaceName:   "awg0",
		ip:              iproute.New(),
		statusCallbacks: []StatusCallback{},
		notify:          make(chan ConnectionStatus, statusQueue),
		reconnectMin:    reconnectMinDelay,
		reconnectMax:    reconnectMaxDelay,
	}

	// Адреса и маршруты ставятся командой ip. Без неё туннель поднимется, а
	// трафик в него не пойдёт — раньше это выражалось серией предупреждений
	// в журнале при состоянии «Подключено».
	if err := iproute.Available(); err != nil {
		m.ipToolErr = err
		log.Printf("ВНИМАНИЕ: %v. Подключение работать не будет", err)
	}

	// Прошлый запуск мог не пережить SIGKILL и оставить систему с чужим
	// resolv.conf, указывающим на исчезнувший адрес туннеля.
	restoreOrphanedDNS()

	// Блокировка тоже переживает падение процесса — в этом её смысл, но
	// после смерти клиента она оставила бы машину без сети навсегда.
	if driver, err := firewall.Detect(); err == nil {
		m.firewallDriver = driver
		if err := driver.Clear(); err != nil {
			log.Printf("Не удалось снять блокировку от прошлого запуска: %v", err)
		}
	} else {
		// Подробности — в журнал: в интерфейс уйдёт короткое объяснение.
		log.Printf("Блокировка трафика мимо туннеля недоступна: %v", err)
		m.firewallErr = err
	}

	go m.notifyLoop()

	return m
}

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

// ifname возвращает имя интерфейса туннеля.
func (m *Manager) ifname() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.interfaceName
}

// Connect поднимает соединение с указанным конфигом.
//
// Если туннель уже поднят на другом конфиге, это переключение: текущее
// соединение разбирается и дожидается полной остановки, после чего
// поднимается новое. Отдельно нажимать «отключить» не нужно.
func (m *Manager) Connect(cfg *config.AmneziaWGConfig, routing *config.RoutingConfig) error {
	m.connectMu.Lock()
	defer m.connectMu.Unlock()

	m.mu.RLock()
	state, current := m.status.State, m.status.ConfigID
	m.mu.RUnlock()

	// Уже подключены, подключаемся или переподключаемся к нему же — делать
	// нечего. Переподключение считается тем же соединением: прерывать его
	// ради того же самого конфига значит начинать всё сначала без причины.
	if current == cfg.ID && (state == StateConnected || state == StateConnecting || state == StateReconnecting) {
		return nil
	}

	m.teardown()

	m.mu.Lock()
	m.status = ConnectionStatus{
		State:      StateConnecting,
		ConfigID:   cfg.ID,
		ConfigName: cfg.Name,
		Interface:  m.interfaceName,
		Attempt:    1,
	}
	m.activeConfig = cfg
	m.routingConfig = routing

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.cancel, m.done = cancel, done
	m.mu.Unlock()

	m.notifyStatusChange()

	go func() {
		defer close(done)
		m.supervise(ctx, cfg, routing)
	}()

	return nil
}

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

// runConnection поднимает туннель и держит его, пока он жив. Возвращает
// признак того, что рукопожатие состоялось, и причину, по которой всё
// закончилось.
//
// Ядро amneziawg-go работает внутри этого процесса: TUN создаётся напрямую,
// устройство настраивается вызовом IpcSet. Отдельного бинарника, unix-сокета
// и разбора чужого лога больше нет — вместе с ними ушли и их отказы
// (протухший сокет, «интерфейс занят», гонка при старте).
//
// Устройство создаётся заново на каждую попытку, а не переиспользуется:
// сокет ядра привязан к маршруту, который был при его создании, и после
// смены сети (Wi-Fi на кабель, новая точка доступа) продолжает слать пакеты
// в исчезнувший путь.
func (m *Manager) runConnection(ctx context.Context, cfg *config.AmneziaWGConfig, routing *config.RoutingConfig) (bool, error) {
	// Без iproute2 дальше идти незачем: интерфейс создастся, но ни адреса,
	// ни маршруты на него не лягут. Повторять нечего — команда в системе не
	// появится сама.
	m.mu.RLock()
	ipErr := m.ipToolErr
	m.mu.RUnlock()

	if ipErr != nil {
		return false, fatalError{ipErr}
	}

	// Уборка на любом пути выхода: маршруты, посредник имён, системный DNS и
	// само устройство. Держать их до следующей попытки нельзя — интерфейс
	// исчезает вместе с устройством, и маршруты на него ведут в пустоту.
	defer m.releaseSystem()

	mtu := cfg.Interface.MTU
	if mtu <= 0 {
		mtu = device.DefaultMTU
	}

	ifname := m.ifname()

	tunDev, err := tun.CreateTUN(ifname, mtu)
	if err != nil {
		return false, fmt.Errorf("не удалось создать интерфейс %s: %w", ifname, err)
	}

	// Ядро может выдать имя, отличное от запрошенного. Дальше по коду ходит
	// именно оно, иначе маршруты лягут не на тот интерфейс.
	if name, err := tunDev.Name(); err == nil && name != "" {
		m.mu.Lock()
		m.interfaceName = name
		m.status.Interface = name
		m.mu.Unlock()
	}

	dev := device.NewDevice(tunDev, conn.NewDefaultBind(), m.deviceLogger())

	m.mu.Lock()
	m.dev = dev
	m.mu.Unlock()

	// Тот же самый текст UAPI, что раньше уходил в сокет.
	if err := dev.IpcSet(m.buildUAPIConfig(cfg)); err != nil {
		// Повторять нечего: ядро отвергает не сеть, а сами параметры.
		return false, fatalError{fmt.Errorf("ядро отвергло конфигурацию: %w", err)}
	}

	if err := dev.Up(); err != nil {
		return false, fmt.Errorf("не удалось поднять туннель: %w", err)
	}

	if err := m.configureLink(cfg); err != nil {
		return false, fmt.Errorf("не удалось настроить интерфейс: %w", err)
	}

	// Состояние остаётся «подключение», пока не состоялось рукопожатие:
	// поднятый интерфейс сам по себе ещё ничего не значит. Маршруты тоже
	// ставятся только после него.
	//
	// Наблюдатель обязан завершиться ДО уборки. Он ставит маршруты и
	// подменяет системный DNS, и закрытие устройства не останавливает его
	// мгновенно: успей он сделать это после уборки — записи остались бы в
	// системе, уводя трафик в исчезнувший интерфейс.
	return m.watchDevice(ctx, dev, cfg, routing)
}

// deviceLogger направляет журнал ядра в общий лог приложения. Штатный
// device.NewLogger пишет напрямую в stdout мимо log, из-за чего строки шли
// без нашего префикса и вперемешку по времени.
func (m *Manager) deviceLogger() *device.Logger {
	return &device.Logger{
		Verbosef: func(format string, args ...any) { log.Printf("[awg] "+format, args...) },
		Errorf:   func(format string, args ...any) { log.Printf("[awg] ERROR "+format, args...) },
	}
}

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

// deviceStats — сводка по всем пирам устройства.
type deviceStats struct {
	RxBytes       uint64
	TxBytes       uint64
	LastHandshake time.Time
}

// parseDeviceStats разбирает ответ IpcGet. Формат построчный, "ключ=значение";
// счётчики пиров суммируются, из времён рукопожатия берётся самое свежее.
func parseDeviceStats(state string) deviceStats {
	var stats deviceStats
	var sec, nsec int64

	flushHandshake := func() {
		if sec == 0 && nsec == 0 {
			return
		}
		t := time.Unix(sec, nsec)
		if t.After(stats.LastHandshake) {
			stats.LastHandshake = t
		}
		sec, nsec = 0, 0
	}

	for _, line := range strings.Split(state, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}

		switch key {
		case "public_key":
			// Началось описание следующего пира — закрываем предыдущего.
			flushHandshake()
		case "rx_bytes":
			if n, err := strconv.ParseUint(value, 10, 64); err == nil {
				stats.RxBytes += n
			}
		case "tx_bytes":
			if n, err := strconv.ParseUint(value, 10, 64); err == nil {
				stats.TxBytes += n
			}
		case "last_handshake_time_sec":
			sec, _ = strconv.ParseInt(value, 10, 64)
		case "last_handshake_time_nsec":
			nsec, _ = strconv.ParseInt(value, 10, 64)
		}
	}
	flushHandshake()

	return stats
}

// configureLink поднимает интерфейс и вешает на него адреса. Маршрутов здесь
// нет намеренно — см. configureTraffic.
func (m *Manager) configureLink(cfg *config.AmneziaWGConfig) error {
	ifname := m.ifname()

	for _, addr := range cfg.Interface.Address {
		if err := m.ip.AddAddress(addr, ifname); err != nil {
			// Отдельный адрес мог не лечь по безобидной причине — например,
			// он уже назначен. Итог проверяем по состоянию интерфейса ниже.
			log.Printf("Не удалось назначить адрес %s: %v", addr, err)
		}
	}

	// MTU задан при создании TUN, дублировать командой не нужно.
	if err := m.ip.LinkUp(ifname); err != nil {
		return fmt.Errorf("не удалось поднять интерфейс: %w", err)
	}

	// Интерфейс без единого адреса ничего не отправит. Раньше это проходило
	// одним предупреждением в журнал: туннель поднимался, состояние
	// становилось «Подключено», а трафик не шёл — и связи между этим и
	// строчкой в логе пользователь не видел.
	//
	// Спрашиваем систему, а не считаем удачные вызовы: адрес мог быть уже
	// назначен, и тогда ошибка команды означает успех.
	if !m.interfaceHasAddress(ifname) {
		return fmt.Errorf("интерфейсу %s не назначен ни один адрес — проверьте Address в конфигурации", ifname)
	}

	return nil
}

// interfaceHasAddress сообщает, есть ли у интерфейса адрес, пригодный для
// отправки. Локальные адреса канала (scope link) не в счёт: они появляются
// сами и ничего не значат.
func (m *Manager) interfaceHasAddress(ifname string) bool {
	has, err := m.ip.HasGlobalAddress(ifname)
	if err != nil {
		// Не смогли спросить — не наказываем: настоящую причину увидим
		// дальше, когда трафик не пойдёт.
		log.Printf("Не удалось проверить адреса интерфейса %s: %v", ifname, err)
		return true
	}
	return has
}

// configureTraffic перенаправляет трафик в туннель и переключает DNS.
//
// Вызывается ПОСЛЕ рукопожатия. Раньше маршруты ставились сразу, и мёртвый
// конфиг забирал весь трафик в туннель, который никуда не ведёт: пользователь
// оставался без интернета всё время ожидания. Рукопожатию маршруты не нужны —
// оно идёт по обычному пути.
//
// Та же самая функция пересобирает маршрутизацию на живом туннеле, когда
// правила поменяли из интерфейса: последовательность действий там ровно та
// же, а два её отдельных списка успели разъехаться.
func (m *Manager) configureTraffic(cfg *config.AmneziaWGConfig, routingCfg *config.RoutingConfig) error {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()

	// Посредник имён пересобирается целиком: правила по именам могли
	// появиться, исчезнуть или поменять направление вместе с режимом.
	// При первичной настройке оба вызова — пустая работа.
	m.stopNameRouting()
	m.flushRoutes()

	if err := m.configureRouting(cfg, routingCfg); err != nil {
		return fmt.Errorf("не удалось настроить маршрутизацию: %w", err)
	}

	// Правила по доменам и зонам обслуживает посредник DNS: адреса для них
	// становятся известны только из ответов на запросы пользователя.
	// Не поднялся — работаем как раньше, с прямым DNS из конфига.
	servers := cfg.Interface.DNS
	if proxyAddr, ok := m.startNameRouting(cfg, routingCfg); ok {
		servers = []string{proxyAddr.String()}
	}

	if err := m.dns.Apply(m.ifname(), servers); err != nil {
		log.Printf("Warning: failed to configure DNS: %v", err)
	}

	// Блокировка взводится здесь же, вместе с маршрутами: с этого момента
	// трафик рассчитывает уйти в туннель, и с этого же момента его нельзя
	// выпускать мимо. Снимается она не здесь — см. killswitch.go.
	m.syncKillSwitch(cfg, routingCfg)

	return nil
}

// buildUAPIConfig creates a UAPI configuration string
func (m *Manager) buildUAPIConfig(cfg *config.AmneziaWGConfig) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("private_key=%s\n", hexEncodeKey(cfg.Interface.PrivateKey)))

	// Метка на сокете ядра. По ней блокировка отличает наши же зашифрованные
	// пакеты к серверу VPN от всего остального трафика — см. internal/firewall.
	// Ставится всегда: на работу туннеля она не влияет, а привязывать её к
	// настройке значило бы получить блокировку, которая не пропускает сам
	// туннель, если включить её не в том порядке.
	sb.WriteString(fmt.Sprintf("fwmark=%d\n", firewall.Mark))

	// AmneziaWG specific parameters
	if cfg.Interface.Jc > 0 {
		sb.WriteString(fmt.Sprintf("jc=%d\n", cfg.Interface.Jc))
	}
	if cfg.Interface.Jmin > 0 {
		sb.WriteString(fmt.Sprintf("jmin=%d\n", cfg.Interface.Jmin))
	}
	if cfg.Interface.Jmax > 0 {
		sb.WriteString(fmt.Sprintf("jmax=%d\n", cfg.Interface.Jmax))
	}
	if cfg.Interface.S1 > 0 {
		sb.WriteString(fmt.Sprintf("s1=%d\n", cfg.Interface.S1))
	}
	if cfg.Interface.S2 > 0 {
		sb.WriteString(fmt.Sprintf("s2=%d\n", cfg.Interface.S2))
	}
	if cfg.Interface.S3 > 0 {
		sb.WriteString(fmt.Sprintf("s3=%d\n", cfg.Interface.S3))
	}
	if cfg.Interface.S4 > 0 {
		sb.WriteString(fmt.Sprintf("s4=%d\n", cfg.Interface.S4))
	}
	// AmneziaWG v2. Без этих параметров сервер v2 не примет наши пакеты:
	// заголовки шифруются header_protection_key, а размеры — padding'ом.
	if cfg.Interface.HeaderProtectionKey != "" {
		sb.WriteString(fmt.Sprintf("header_protection_key=%s\n", hexEncodeKey(cfg.Interface.HeaderProtectionKey)))
	}
	if cfg.Interface.ContentPaddingAddition != "" {
		sb.WriteString(fmt.Sprintf("content_padding_addition=%s\n", cfg.Interface.ContentPaddingAddition))
	}
	if cfg.Interface.RekeyAfterTime != "" {
		sb.WriteString(fmt.Sprintf("rekey_after_time=%s\n", cfg.Interface.RekeyAfterTime))
	}
	if cfg.Interface.RekeyTimeout != "" {
		sb.WriteString(fmt.Sprintf("rekey_timeout=%s\n", cfg.Interface.RekeyTimeout))
	}
	if cfg.Interface.RejectAfterTime != "" {
		sb.WriteString(fmt.Sprintf("reject_after_time=%s\n", cfg.Interface.RejectAfterTime))
	}
	if cfg.Interface.KeepaliveTimeout != "" {
		sb.WriteString(fmt.Sprintf("keepalive_timeout=%s\n", cfg.Interface.KeepaliveTimeout))
	}
	if cfg.Interface.MaxHandshakeAttempts != "" {
		sb.WriteString(fmt.Sprintf("max_handshake_attempts=%s\n", cfg.Interface.MaxHandshakeAttempts))
	}
	// Переключатели меняют формат пакетов: если сервер ждёт случайные хвосты,
	// а мы их не шлём, он просто выбрасывает наши пакеты и молчит в ответ.
	if v, ok := parseSwitch(cfg.Interface.RandomTrailers); ok {
		sb.WriteString(fmt.Sprintf("random_trailers=%s\n", v))
	}
	if v, ok := parseSwitch(cfg.Interface.DisableCookies); ok {
		sb.WriteString(fmt.Sprintf("disable_cookies=%s\n", v))
	}
	if cfg.Interface.H1 > 0 {
		sb.WriteString(fmt.Sprintf("h1=%d\n", cfg.Interface.H1))
	}
	if cfg.Interface.H2 > 0 {
		sb.WriteString(fmt.Sprintf("h2=%d\n", cfg.Interface.H2))
	}
	if cfg.Interface.H3 > 0 {
		sb.WriteString(fmt.Sprintf("h3=%d\n", cfg.Interface.H3))
	}
	if cfg.Interface.H4 > 0 {
		sb.WriteString(fmt.Sprintf("h4=%d\n", cfg.Interface.H4))
	}

	// Configure peers
	for _, peer := range cfg.Peers {
		sb.WriteString(fmt.Sprintf("public_key=%s\n", hexEncodeKey(peer.PublicKey)))

		if peer.PresharedKey != "" {
			sb.WriteString(fmt.Sprintf("preshared_key=%s\n", hexEncodeKey(peer.PresharedKey)))
		}

		if peer.Endpoint != "" {
			sb.WriteString(fmt.Sprintf("endpoint=%s\n", peer.Endpoint))
		}

		for _, ip := range peer.AllowedIPs {
			sb.WriteString(fmt.Sprintf("allowed_ip=%s\n", ip))
		}

		// В v2 интервал может быть диапазоном, UAPI принимает его строкой.
		if peer.PersistentKeepaliveRaw != "" {
			sb.WriteString(fmt.Sprintf("persistent_keepalive_interval=%s\n", peer.PersistentKeepaliveRaw))
		} else if peer.PersistentKeepalive > 0 {
			sb.WriteString(fmt.Sprintf("persistent_keepalive_interval=%d\n", peer.PersistentKeepalive))
		}
	}

	return sb.String()
}

// parseSwitch переводит значение вида on/off из конфига в "1"/"0" для UAPI.
// Второе значение — было ли задано вообще.
func parseSwitch(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "yes", "1":
		return "1", true
	case "off", "false", "no", "0":
		return "0", true
	default:
		return "", false
	}
}

// hexEncodeKey converts a base64 WireGuard key to hex
func hexEncodeKey(base64Key string) string {
	hexKey, err := KeyToHex(base64Key)
	if err != nil {
		log.Printf("Warning: failed to convert key to hex: %v, using original", err)
		return base64Key
	}
	return hexKey
}

// addRoute добавляет маршрут и запоминает его, если добавление удалось.
// Уже существующие чужие маршруты (RTNETLINK "File exists") не запоминаются,
// поэтому при пересборке мы их не удалим. Вызывать под routeMu.
func (m *Manager) addRoute(args ...string) error {
	if err := m.ip.AddRoute(args...); err != nil {
		return err
	}

	m.installedRoutes = append(m.installedRoutes, args)
	return nil
}

// flushRoutes снимает все маршруты, поставленные нами, в обратном порядке.
// Вызывать под routeMu.
func (m *Manager) flushRoutes() {
	for i := len(m.installedRoutes) - 1; i >= 0; i-- {
		m.ip.DelRoute(m.installedRoutes[i]...)
	}
	m.installedRoutes = nil
}

// ApplyRouting перестраивает маршруты на живом туннеле.
// Если туннель не поднят — ничего не делает: правила применятся при подключении.
func (m *Manager) ApplyRouting(routing *config.RoutingConfig) error {
	m.mu.RLock()
	state := m.status.State
	cfg := m.activeConfig
	m.mu.RUnlock()

	if state != StateConnected || cfg == nil {
		return nil
	}

	log.Printf("Re-applying routing rules on live tunnel")

	if err := m.configureTraffic(cfg, routing); err != nil {
		return fmt.Errorf("не удалось пересобрать маршруты: %w", err)
	}

	m.mu.Lock()
	m.routingConfig = routing
	m.mu.Unlock()

	m.notifyStatusChange()
	return nil
}

// configureRouting sets up routing based on configuration. Вызывать под routeMu.
func (m *Manager) configureRouting(cfg *config.AmneziaWGConfig, routing *config.RoutingConfig) error {
	ifname := m.ifname()

	// Правила по доменам резолвим через серверы имён из конфига НАПРЯМУЮ,
	// минуя системный резолвер.
	//
	// Через системный нельзя: пересборка правил на живом туннеле начинается с
	// остановки нашего же посредника DNS, а resolv.conf в этот момент всё ещё
	// указывает на него. Запрос уходил бы в мёртвый сокет, и правила по
	// доменам молча оставались без маршрутов — со стороны это выглядело как
	// «удалил одно правило, перестали работать все остальные».
	resolver := resolverFor(parseDNSServers(cfg.Interface.DNS))

	// Get the peer's allowed IPs
	var peerAllowedIPs []string
	for _, peer := range cfg.Peers {
		peerAllowedIPs = append(peerAllowedIPs, peer.AllowedIPs...)
	}

	if routing == nil || len(routing.Rules) == 0 {
		// Правил нет — маршрутизируем по AllowedIPs из конфига.
		//
		// AllowedIPs вида 0.0.0.0/0 нельзя добавлять напрямую: тогда в туннель
		// уйдут и сами зашифрованные пакеты к серверу VPN, получится петля —
		// туннель встаёт, пинг внутри него идёт, а интернет пропадает.
		// Полный туннель поднимаем через setupDefaultVPNRoute: он исключает
		// адрес сервера и вместо 0.0.0.0/0 ставит пару /1-маршрутов.
		if hasDefaultRoute(peerAllowedIPs) {
			return m.setupDefaultVPNRoute(cfg)
		}

		for _, ip := range peerAllowedIPs {
			if err := m.addRoute(ip, "dev", ifname); err != nil {
				log.Printf("Warning: failed to add route %s: %v", ip, err)
			}
		}
		return nil
	}

	// Apply routing rules based on mode
	switch routing.Mode {
	case config.RoutingModeVPNList:
		// Route only specified items through VPN
		for _, rule := range routing.Rules {
			if !rule.Enabled {
				continue
			}

			ips, err := m.resolveRoutingRule(rule, resolver)
			if err != nil {
				log.Printf("Warning: failed to resolve rule %s: %v", rule.Value, err)
				continue
			}

			for _, ip := range ips {
				if err := m.addRoute(ip, "dev", ifname); err != nil {
					log.Printf("Warning: failed to add route %s: %v", ip, err)
				}
			}
		}

	case config.RoutingModeDirectList:
		// Route all through VPN except specified items
		// First, set up default route through VPN
		if err := m.setupDefaultVPNRoute(cfg); err != nil {
			log.Printf("Warning: failed to setup default VPN route: %v", err)
		}

		// Then exclude specific routes
		for _, rule := range routing.Rules {
			if !rule.Enabled {
				continue
			}

			ips, err := m.resolveRoutingRule(rule, resolver)
			if err != nil {
				log.Printf("Warning: failed to resolve rule %s: %v", rule.Value, err)
				continue
			}

			// Эти адреса должны идти мимо туннеля — тем путём, каким система
			// шла к ним до подключения. Каким именно, решает bypassRoute:
			// шлюз по умолчанию тут не годится, см. bypass.go.
			for _, prefix := range ips {
				if err := m.addBypassRoute(prefix); err != nil {
					log.Printf("Исключение %s не выведено мимо туннеля: %v", prefix, err)
				}
			}
		}
	}

	return nil
}

// resolveRoutingRule resolves a routing rule to IP addresses/CIDRs
func (m *Manager) resolveRoutingRule(rule config.RoutingRule, resolver *net.Resolver) ([]string, error) {
	switch rule.Type {
	case config.RuleTypeIP:
		// Single IP - add /32 suffix if no mask
		if !strings.Contains(rule.Value, "/") {
			if strings.Contains(rule.Value, ":") {
				return []string{rule.Value + "/128"}, nil // IPv6
			}
			return []string{rule.Value + "/32"}, nil // IPv4
		}
		return []string{rule.Value}, nil

	case config.RuleTypeCIDR:
		return []string{rule.Value}, nil

	case config.RuleTypeDomain:
		// Разовый резолв нужен ради быстрого старта: маршрут появляется сразу
		// при подключении, не дожидаясь, пока пользователь откроет сайт.
		// Поддерживает же правило в актуальном состоянии посредник DNS
		// (nameroutes.go) — адреса за именем меняются, и одного резолва мало.
		ctx, cancel := context.WithTimeout(context.Background(), ruleResolveTimeout)
		defer cancel()

		ips, err := resolver.LookupIP(ctx, "ip", rule.Value)
		if err != nil {
			return nil, err
		}

		result := make([]string, 0, len(ips))
		for _, ip := range ips {
			if ip.To4() != nil {
				result = append(result, ip.String()+"/32")
			} else {
				result = append(result, ip.String()+"/128")
			}
		}
		return result, nil

	case config.RuleTypeZone:
		// У зоны нет заранее известных адресов: под .ru подпадают миллионы
		// имён, перечислить их невозможно. Такие правила целиком на
		// посреднике DNS — он ставит маршрут в тот момент, когда имя из зоны
		// действительно спросили. Здесь ставить нечего.
		return nil, nil

	default:
		return nil, fmt.Errorf("неизвестный тип правила: %s", rule.Type)
	}
}

// setupDefaultVPNRoute sets up routing all traffic through VPN
func (m *Manager) setupDefaultVPNRoute(cfg *config.AmneziaWGConfig) error {
	ifname := m.ifname()

	// Адрес сервера VPN должен идти В ОБХОД туннеля, иначе получится петля.
	// Endpoint может быть именем хоста — резолвим его, пока DNS ещё исправен.
	for _, peer := range cfg.Peers {
		if peer.Endpoint == "" {
			continue
		}

		host, _, err := net.SplitHostPort(peer.Endpoint)
		if err != nil || host == "" {
			return fmt.Errorf("не разобрать адрес сервера %q: %w", peer.Endpoint, err)
		}

		addrs, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("не удалось определить адрес сервера %q: %w", host, err)
		}

		// Исключаем адреса ОБЕИХ версий протокола. Раньше IPv6 пропускался
		// целиком, потому что шлюз спрашивался один раз и только для IPv4:
		// сервер, доступный лишь по IPv6, не исключался из туннеля, и
		// зашифрованные пакеты к нему уходили в сам туннель — петля, из-за
		// которой соединение не поднималось вовсе.
		excluded := 0
		for _, addr := range addrs {
			// Закрепляем ТОТ ЖЕ путь, которым система идёт к серверу сейчас:
			// через шлюз, по линку или вовсе через другой туннель. Шлюз по
			// умолчанию, который брался здесь раньше, — лишь частный случай,
			// и для сервера, доступного иначе, он уводил пакеты в никуда.
			path, err := m.ip.PathTo(addr.String())
			if err != nil {
				log.Printf("Адрес сервера %s не исключён: %v", addr, err)
				continue
			}

			// Адрес самой машины: локальная таблица просматривается раньше
			// всех, туннель до него не доберётся и без исключения.
			if path.Local {
				excluded++
				continue
			}

			// Путь уже ведёт в туннель — это петля, и закреплять её нельзя.
			if path.Device == ifname {
				log.Printf("Адрес сервера %s не исключён: путь к нему ведёт в сам туннель", addr)
				continue
			}

			if err := m.addRoute(path.RouteArgs(hostPrefix(addr))...); err != nil {
				// Маршрут мог уже существовать: его оставил аварийно
				// завершившийся прошлый запуск или другой клиент к тому же
				// серверу. Отказываться из-за этого нельзя — важно не кто
				// поставил маршрут, а что адрес сервера пришпилен мимо
				// туннеля. Раньше такой маршрут валил настройку целиком, и
				// клиент оставался подключённым, но без единого маршрута.
				if m.pinnedOutsideTunnel(hostPrefix(addr)) {
					log.Printf("Адрес сервера %s уже выведен мимо туннеля чужим маршрутом — оставляем как есть", addr)
					excluded++
					continue
				}

				log.Printf("Адрес сервера %s не исключён: %v", addr, err)
				continue
			}
			excluded++
		}

		// Ни одного исключения — дальше нельзя: маршруты ниже уведут в туннель
		// и сами пакеты к серверу. Получится петля, и вместо полного туннеля
		// пользователь останется без сети.
		if excluded == 0 {
			return fmt.Errorf("не удалось вывести сервер %s мимо туннеля: без этого весь трафик, включая пакеты к самому серверу, ушёл бы в петлю", host)
		}
	}

	// Две /1-подсети вместо 0.0.0.0/0: перекрывают весь адресный простор,
	// но не конфликтуют с существующим маршрутом по умолчанию.
	//
	// Отказ здесь — это отказ полного туннеля целиком, поэтому он ошибка, а
	// не предупреждение: молча оставить весь трафик вне VPN нельзя, ради него
	// пользователь и подключался.
	if err := m.addRoute("0.0.0.0/1", "dev", ifname); err != nil {
		return fmt.Errorf("не удалось направить трафик в туннель: %w", err)
	}
	if err := m.addRoute("128.0.0.0/1", "dev", ifname); err != nil {
		return fmt.Errorf("не удалось направить трафик в туннель: %w", err)
	}

	// IPv6 заворачиваем в туннель ТОЛЬКО если у интерфейса есть IPv6-адрес.
	// Иначе маршрут ведёт в никуда: браузер по AAAA-записи уходит в IPv6,
	// упирается в чёрную дыру и сайт не открывается, хотя IPv4 живой.
	if hasIPv6Address(cfg.Interface.Address) {
		for _, prefix := range []string{"::/1", "8000::/1"} {
			if err := m.addRoute(prefix, "dev", ifname); err != nil {
				log.Printf("Warning: failed to add IPv6 route %s: %v", prefix, err)
			}
		}
	}

	return nil
}

// hostPrefix превращает адрес в одноадресный префикс: /32 для IPv4 и /128
// для IPv6 — в таком виде адреса и попадают в таблицу маршрутизации.
func hostPrefix(ip net.IP) string {
	if ip4 := ip.To4(); ip4 != nil {
		return ip4.String() + "/32"
	}
	return ip.String() + "/128"
}

// hasDefaultRoute сообщает, покрывают ли AllowedIPs весь трафик.
func hasDefaultRoute(allowedIPs []string) bool {
	for _, ip := range allowedIPs {
		switch strings.TrimSpace(ip) {
		case "0.0.0.0/0", "::/0":
			return true
		}
	}
	return false
}

// hasIPv6Address сообщает, назначен ли интерфейсу IPv6-адрес.
func hasIPv6Address(addresses []string) bool {
	for _, addr := range addresses {
		host, _, err := net.ParseCIDR(strings.TrimSpace(addr))
		if err != nil {
			continue
		}
		if host.To4() == nil {
			return true
		}
	}
	return false
}

// Disconnect stops the VPN connection
func (m *Manager) Disconnect() error {
	m.connectMu.Lock()
	defer m.connectMu.Unlock()

	m.teardown()
	return nil
}

// teardown разбирает текущее соединение и возвращается только когда всё
// действительно закончилось: супервизор вышел, устройство закрыто, маршруты
// сняты и системный DNS возвращён. Вызывать под connectMu.
func (m *Manager) teardown() {
	m.mu.Lock()
	if m.status.State == StateDisconnected {
		m.mu.Unlock()
		return
	}
	cancel, done := m.cancel, m.done
	m.status.State = StateDisconnecting
	m.mu.Unlock()

	m.notifyStatusChange()

	// Отмена контекста доводит до конца всю цепочку: наблюдатель выходит из
	// цикла, попытка подключения освобождает систему, супервизор перестаёт
	// переподключаться.
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}

	// Состояние ошибки от прошлой попытки супервизор оставляет намеренно —
	// чтобы причина не пропала с экрана. Здесь же отключение запрошено явно,
	// и итог у него один: «Отключено».
	m.mu.Lock()
	m.status = ConnectionStatus{State: StateDisconnected}
	m.activeConfig = nil
	m.routingConfig = nil
	m.cancel, m.done = nil, nil
	m.mu.Unlock()

	m.notifyStatusChange()
}

// releaseSystem возвращает системе всё, что мы в ней меняли, и закрывает
// устройство. Вызывается в конце КАЖДОЙ попытки подключения, а не только при
// отключении: между попытками интерфейс пересоздаётся, и маршруты на прежний
// вели бы в пустоту.
func (m *Manager) releaseSystem() {
	// Всё, что трогает таблицу маршрутизации, — под одной блокировкой: иначе
	// пересборка правил из интерфейса могла бы вклиниться в уборку и
	// поставить маршруты на уже разбираемый туннель.
	m.routeMu.Lock()
	// Посредник DNS останавливается первым: он ставит маршруты сам, и его
	// последний ответ не должен обогнать уборку.
	m.stopNameRouting()
	// Записи "via <шлюз>" висят на физическом интерфейсе и удалением awg0 не
	// убираются — без этого они копились бы после каждой попытки.
	m.flushRoutes()
	m.routeMu.Unlock()

	m.dns.Restore()

	// Интерфейс удалять не нужно: TUN закрывается вместе с устройством и
	// исчезает из системы сам.
	m.mu.Lock()
	dev := m.dev
	m.dev = nil
	m.mu.Unlock()

	if dev != nil {
		dev.Close()
	}
}

// finish переводит менеджер в покой после выхода супервизора.
//
// Состояние ошибки сохраняется: раньше уборка затирала его на «Отключено», и
// в интерфейсе оставалось одно слово без причины. Сбрасывает его либо новое
// подключение, либо явное отключение.
func (m *Manager) finish() {
	// Блокировку снимаем до захвата mu: она берёт routeMu, а порядок
	// «сначала routeMu, потом mu» держится по всему менеджеру.
	m.routeMu.Lock()
	m.clearKillSwitch()
	m.routeMu.Unlock()

	m.mu.Lock()
	prevState, prevErr := m.status.State, m.status.Error

	m.status = ConnectionStatus{State: StateDisconnected}
	if prevState == StateError {
		m.status.State = StateError
		m.status.Error = prevErr
	}

	m.activeConfig = nil
	m.routingConfig = nil
	m.mu.Unlock()

	m.notifyStatusChange()
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
