package vpn

// Правила для программ на Windows — перехватом пакетов (internal/appsteer).
//
// Маршрут по процессу Windows выбрать не даёт, поэтому соединения программы
// переводятся на адрес другого интерфейса: в режиме «только список» — на
// адрес туннеля, в режиме «всё, кроме списка» — на адрес физической сети.
// Дальше их ведут маршруты этого интерфейса.
//
// Для режима «только список» у туннеля нет маршрута по умолчанию — весь
// трафик идёт мимо него. Поэтому на время перехвата туннелю добавляется
// маршрут по умолчанию с огромной метрикой: обычные соединения его не
// выбирают (у физической сети метрика меньше), а пакет с адресом туннеля
// по строгой модели хоста может уйти только через туннель — и находит в нём
// этот маршрут.

import (
	"fmt"
	"log"
	"net/netip"
	"slices"
	"sync"

	"github.com/user/amnezia-web-client/internal/appsteer"
	"github.com/user/amnezia-web-client/internal/iproute"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

const (
	// steerRouteMetric — метрика запасного маршрута по умолчанию в туннель.
	steerRouteMetric = 9000

	// defaultTunnelMTU — MTU туннеля, если конфигурация его не задаёт (как у
	// ядра amneziawg-go).
	defaultTunnelMTU = 1420
)

var defaultRoute4 = netip.MustParsePrefix("0.0.0.0/0")

type winApps struct {
	mu      sync.Mutex
	session *appsteer.Session
	// routeLUID — интерфейс, которому добавлен запасной маршрут; 0 — нет.
	routeLUID winipcfg.LUID
}

func newAppRouter(policyRouter) appRouter {
	if err := appsteer.Available(); err != nil {
		log.Printf("Правила для программ недоступны: %v", err)
		return nil
	}
	return &winApps{}
}

func (w *winApps) SetApps(req appRequest) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if req.apps.empty() {
		w.stop()
		return nil
	}

	opts, luid, err := steerOptions(req)
	if err != nil {
		w.stop()
		return err
	}
	programs := appsteer.Programs{All: req.apps.all, UDP: req.apps.udp}

	// Сменился только набор программ — перехват не перезапускаем: уже
	// уведённые соединения оборвались бы.
	if w.session != nil && sameOptions(w.session.Options(), opts) {
		w.session.SetPrograms(programs)
		return nil
	}

	w.stop()
	if req.viaTunnel {
		if err := luid.AddRoute(defaultRoute4, netip.IPv4Unspecified(), steerRouteMetric); err != nil {
			return fmt.Errorf("маршрут для программ в туннель: %w", err)
		}
		w.routeLUID = luid
	}

	session, err := appsteer.Start(opts, programs)
	if err != nil {
		w.stop()
		return err
	}
	w.session = session
	return nil
}

// stop выключает перехват и убирает запасной маршрут. Под mu.
func (w *winApps) stop() {
	if w.session != nil {
		w.session.Stop()
		w.session = nil
	}
	if w.routeLUID != 0 {
		if err := w.routeLUID.DeleteRoute(defaultRoute4, netip.IPv4Unspecified()); err != nil {
			log.Printf("Не удалось убрать маршрут для программ: %v", err)
		}
		w.routeLUID = 0
	}
}

// steerOptions выясняет адреса: туннеля — из конфигурации, физической сети —
// у системы, по пути к серверу VPN (он всегда идёт мимо туннеля).
func steerOptions(req appRequest) (appsteer.Options, winipcfg.LUID, error) {
	tunAddr, ok := firstIPv4(req.spec.Addresses)
	if !ok {
		return appsteer.Options{}, 0, fmt.Errorf("у туннеля нет адреса IPv4")
	}
	luid, err := tunnelLUID(req.ifname)
	if err != nil {
		return appsteer.Options{}, 0, err
	}

	var server netip.Addr
	for _, ep := range req.endpoints {
		if ep.Addr().Is4() {
			server = ep.Addr()
			break
		}
	}
	if !server.IsValid() {
		return appsteer.Options{}, 0, fmt.Errorf("не найден адрес IPv4 сервера VPN")
	}
	physAddr, _, err := iproute.SourceFor(server)
	if err != nil {
		return appsteer.Options{}, 0, fmt.Errorf("адрес физической сети: %w", err)
	}

	opts := appsteer.Options{Exclude: req.endpoints}
	if req.viaTunnel {
		mtu := req.spec.MTU
		if mtu <= 0 {
			mtu = defaultTunnelMTU
		}
		opts.From, opts.To = physAddr, tunAddr
		opts.MSS = uint16(mtu - 40) // заголовки IPv4 и TCP
		opts.DropIPv6 = !req.v6()
	} else {
		opts.From, opts.To = tunAddr, physAddr
	}
	return opts, luid, nil
}

func sameOptions(a, b appsteer.Options) bool {
	return a.Config == b.Config && slices.Equal(a.Exclude, b.Exclude)
}

func firstIPv4(addresses []string) (netip.Addr, bool) {
	for _, a := range addresses {
		if p, err := netip.ParsePrefix(a); err == nil && p.Addr().Is4() {
			return p.Addr(), true
		}
	}
	return netip.Addr{}, false
}
