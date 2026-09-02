package vpn

// Жизненный цикл соединения: поднять туннель, дождаться рукопожатия, отдать
// ему трафик и разобрать всё обратно.
//
// Частности лежат рядом и в этот порядок только вставляются: состояние и
// оповещения — status.go, политика повторов — retry.go, наблюдение за живым
// туннелем — watch.go, адреса интерфейса — link.go, маршруты, серверы имён и
// блокировка — routes.go, конфигурация ядра — uapi.go.

import (
	"context"
	"fmt"
	"log"
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
