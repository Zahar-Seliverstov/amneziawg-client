package vpn

// Жизненный цикл соединения: поднять туннель, дождаться рукопожатия, отдать
// ему трафик и разобрать всё обратно.
//
// Частности лежат рядом и в этот порядок только вставляются: состояние и
// оповещения — status.go, политика повторов — retry.go, наблюдение за живым
// туннелем — watch.go, адреса интерфейса — link.go, маршруты и серверы имён —
// routes.go.
//
// Протоколов менеджер не знает: интерфейс поднимает ядро (tunnel.Engine), а
// всё, что нужно системному слою, приходит сводкой tunnel.Spec.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/dnsproxy"
	"github.com/user/amnezia-web-client/internal/iproute"
	"github.com/user/amnezia-web-client/internal/routing"
	"github.com/user/amnezia-web-client/internal/tunnel"
)

// DefaultInterface — имя интерфейса туннеля, которое просит менеджер.
const DefaultInterface = "awg0"

// Engines выбирает ядро для конфигурации; см. tunnel.Registry.
type Engines interface {
	Engine(tunnel.Config) (tunnel.Engine, error)
}

// Target — что подключить: сохранённая конфигурация, уже разобранная
// протоколом.
type Target struct {
	ID     string
	Name   string
	Config tunnel.Config
}

// Manager manages VPN connections
type Manager struct {
	mu sync.RWMutex

	status        ConnectionStatus
	routingConfig *config.RoutingConfig

	// active — сводка подключённой конфигурации; nil — соединения нет.
	active *tunnel.Spec

	// engines — ядра, которыми поднимаются туннели.
	engines Engines

	// dev — запущенный туннель текущей попытки.
	dev    tunnel.Device
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

	// nameMatcher — правила по именам, по которым посредник DNS прямо сейчас
	// принимает решения. Атомарный указатель, потому что читается он в
	// обработчике каждого ответа DNS, а меняется правкой правил из
	// интерфейса.
	//
	// Ради него всё и затевалось: раньше сопоставитель захватывался при
	// старте посредника, и поменять правила можно было только вместе с самим
	// посредником — с повторным bind на :53 и потерей всех выученных
	// маршрутов. Теперь правила подменяются под работающим посредником.
	nameMatcher atomic.Pointer[routing.Matcher]

	// ruleRoutes — маршруты, поставленные по правилам маршрутизации, и то,
	// какие правила их держат. Отдельно от installedRoutes: те описывают
	// устройство туннеля и меняются только вместе с соединением, а эти
	// правит пользователь посреди работы. См. ruleroutes.go. Только под
	// routeMu.
	ruleRoutes map[string]*ruleRoute

	// endpointAddrs — адреса сервера VPN, выясненные один раз за соединение.
	// Ключ — имя из Endpoint. Только под routeMu.
	//
	// Кэш здесь не ради скорости — вернее, не только ради неё. Адрес сервера
	// спрашивается у СИСТЕМНОГО резолвера, а пересборка правил на живом
	// туннеле начинается с остановки нашего же посредника DNS, на который
	// resolv.conf в этот момент и указывает. Запрос уходил в мёртвый сокет,
	// и в режиме «всё через VPN, кроме списка» настройка туннеля целиком
	// отказывала: правку правил пользователь оплачивал пропавшим интернетом.
	//
	// Адрес сервера за время соединения не меняется — а если бы поменялся,
	// то соединение по старому адресу всё равно продолжает работать, и
	// пришпиливать надо именно его.
	endpointAddrs map[string][]net.IP

	// dns подменяет системные серверы имён на время соединения.
	dns dnsControl

	// flushDNS сбрасывает кэш системного резолвера. Поле, а не прямой вызов:
	// тесты не должны трогать кэш машины, на которой идут. nil — не нужно.
	flushDNS func()

	// ip — команда iproute2 за интерфейсом: адреса, маршруты и опрос таблицы
	// маршрутизации. Поле, а не прямые вызовы exec, — иначе решения о
	// маршрутах можно было бы проверить только от root на живой машине.
	ip iproute.Tool

	// ipToolErr — почему недоступна команда ip. Ею ставятся адреса и
	// маршруты, и без неё туннель поднимется, но трафик в него не пойдёт.
	ipToolErr error

	// Паузы между попытками переподключения. Поля, а не константы: иначе
	// проверить политику повторов можно было бы только реальным ожиданием
	// в секундах.
	reconnectMin, reconnectMax time.Duration

	// Маршрутизация по доменам и зонам — см. nameroutes.go. Живёт только
	// пока поднят туннель, поэтому хранится здесь, а не создаётся в NewManager.
	dnsProxy      *dnsproxy.Proxy
	dynamicRoutes *routing.DynamicSet
	sweepStop     chan struct{}

	// geo — базы geoip/geosite для правил-наборов, см. georoutes.go. nil —
	// баз нет, и такие правила ничего не покрывают.
	geo GeoSource

	// policy — маршрутизация по политикам: свои маршруты в своей таблице и
	// правила для программ (policy.go). nil — её нет (Windows, тесты), и
	// маршруты ложатся в общую таблицу.
	policy policyRouter

	// apps — правила для программ (approuter.go). nil — система их не
	// умеет, и такие правила не действуют.
	apps appRouter
}

// NewManager создаёт менеджер соединения.
func NewManager(engines Engines) *Manager {
	m := &Manager{
		status: ConnectionStatus{
			State: StateDisconnected,
		},
		engines:         engines,
		flushDNS:        flushDNSCache,
		interfaceName:   DefaultInterface,
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
	} else {
		m.policy = newPolicyRouter()
		m.apps = newAppRouter(m.policy)
	}

	// Прошлый запуск мог не пережить SIGKILL и оставить систему с чужим
	// resolv.conf, указывающим на исчезнувший адрес туннеля.
	restoreOrphanedDNS()

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
func (m *Manager) Connect(target Target, routing *config.RoutingConfig) error {
	engine, err := m.engines.Engine(target.Config)
	if err != nil {
		return err
	}
	spec := target.Config.Spec()

	m.connectMu.Lock()
	defer m.connectMu.Unlock()

	m.mu.RLock()
	state, current := m.status.State, m.status.ConfigID
	m.mu.RUnlock()

	// Уже подключены, подключаемся или переподключаемся к нему же — делать
	// нечего. Переподключение считается тем же соединением: прерывать его
	// ради того же самого конфига значит начинать всё сначала без причины.
	if current == target.ID && (state == StateConnected || state == StateConnecting || state == StateReconnecting) {
		return nil
	}

	m.teardown()

	m.mu.Lock()
	m.status = ConnectionStatus{
		State:      StateConnecting,
		ConfigID:   target.ID,
		ConfigName: target.Name,
		Interface:  m.interfaceName,
		Attempt:    1,
	}
	m.active = &spec
	m.routingConfig = routing

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.cancel, m.done = cancel, done
	m.mu.Unlock()

	m.notifyStatusChange()

	go func() {
		defer close(done)
		m.supervise(ctx, engine, target.Config, routing)
	}()

	return nil
}

// runConnection поднимает туннель и держит его, пока он жив. Возвращает
// признак того, что рукопожатие состоялось, и причину, по которой всё
// закончилось.
func (m *Manager) runConnection(ctx context.Context, engine tunnel.Engine, cfg tunnel.Config, routing *config.RoutingConfig) (bool, error) {
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

	opts := tunnel.Options{Interface: m.ifname(), Logf: log.Printf}
	// Метка на зашифрованных пакетах самого туннеля: по ней они идут
	// системной таблицей, а не в наш маршрут по умолчанию (policy.go).
	if m.policy != nil {
		opts.FirewallMark = m.policy.TunnelMark()
	}

	dev, err := engine.Start(cfg, opts)
	if err != nil {
		if errors.Is(err, tunnel.ErrRejected) {
			return false, fatalError{err}
		}
		return false, err
	}

	// Ядро вправе выдать имя, отличное от запрошенного. Дальше по коду ходит
	// именно оно, иначе маршруты лягут не на тот интерфейс.
	m.mu.Lock()
	m.dev = dev
	m.interfaceName = dev.Name()
	m.status.Interface = dev.Name()
	m.mu.Unlock()

	spec := cfg.Spec()
	if err := m.configureLink(&spec); err != nil {
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
	return m.watchDevice(ctx, dev, &spec, routing)
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
	m.active = nil
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
	// Программы возвращаются на место: без туннеля их метке некуда вести.
	m.releaseApps()
	// Записи "via <шлюз>" висят на физическом интерфейсе и удалением awg0 не
	// убираются — без этого они копились бы после каждой попытки.
	m.flushRoutes()
	// Адреса сервера выясняются заново на каждое соединение: между попытками
	// сервер мог переехать, и держаться за прежний адрес значит не подняться
	// вовсе.
	m.endpointAddrs = nil
	m.routeMu.Unlock()

	m.dns.Restore()

	// Интерфейс удалять не нужно: TUN закрывается вместе с устройством и
	// исчезает из системы сам.
	m.mu.Lock()
	dev := m.dev
	m.dev = nil
	m.mu.Unlock()

	if dev != nil {
		if err := dev.Close(); err != nil {
			log.Printf("Туннель закрыт с ошибкой: %v", err)
		}
	}
}

// finish переводит менеджер в покой после выхода супервизора.
//
// Состояние ошибки сохраняется: раньше уборка затирала его на «Отключено», и
// в интерфейсе оставалось одно слово без причины. Сбрасывает его либо новое
// подключение, либо явное отключение.
func (m *Manager) finish() {
	m.mu.Lock()
	prevState, prevErr := m.status.State, m.status.Error

	m.status = ConnectionStatus{State: StateDisconnected}
	if prevState == StateError {
		m.status.State = StateError
		m.status.Error = prevErr
	}

	m.active = nil
	m.routingConfig = nil
	m.mu.Unlock()

	m.notifyStatusChange()
}

// CleanupSystem убирает из системы то, что могло остаться от аварийно
// завершённой службы: подменённые серверы имён. Вызывается при удалении
// службы — после него перезапуска, который убрал бы это сам, уже не будет.
func CleanupSystem() {
	restoreOrphanedDNS()
}
