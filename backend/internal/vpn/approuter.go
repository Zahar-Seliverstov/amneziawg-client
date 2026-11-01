package vpn

// Правила для программ (app:имя): соединения выбранных программ идут не так,
// как остальное — в режиме «только список» в туннель, в режиме «всё, кроме
// списка» мимо него.
//
// Как это делается, решает система: на Linux — cgroup, метка nftables и
// своя таблица маршрутов (policy_linux.go), на Windows — перехват пакетов
// драйвером WinDivert (approuter_windows.go). Менеджеру всё равно: он
// описывает, чего хочет, запросом appRequest.

import (
	"log"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/tunnel"
)

// appRouter применяет правила для программ.
type appRouter interface {
	// SetApps приводит систему в соответствие с запросом. Пустой набор
	// программ возвращает все взятые программы на место.
	SetApps(req appRequest) error
}

// appRequest — чего менеджер хочет от правил для программ.
type appRequest struct {
	apps appSet
	// viaTunnel — программы ведутся в туннель («только список»); иначе —
	// мимо него («всё, кроме списка»).
	viaTunnel bool
	// ifname — интерфейс туннеля.
	ifname string
	// spec — сводка туннеля: адреса, MTU.
	spec tunnel.Spec
	// endpoints — серверы VPN с разрешёнными адресами: их трафик не трогаем.
	endpoints []netip.AddrPort
}

// v6 сообщает, есть ли у туннеля IPv6.
func (r appRequest) v6() bool { return hasIPv6Address(r.spec.Addresses) }

// appSet — имена программ из правил app:: all — все соединения программы,
// udp — только UDP (app:vesktop/udp).
type appSet struct {
	all, udp []string
}

func (s appSet) empty() bool { return len(s.all) == 0 && len(s.udp) == 0 }

func (s appSet) String() string {
	names := append([]string(nil), s.all...)
	for _, name := range s.udp {
		names = append(names, name+config.AppUDPSuffix)
	}
	return strings.Join(names, ", ")
}

// appNames — имена программ из включённых правил app:.
func appNames(rc *config.RoutingConfig) appSet {
	var set appSet
	if rc == nil {
		return set
	}
	for _, rule := range rc.Rules {
		if !rule.Enabled || rule.Type != config.RuleTypeApp {
			continue
		}
		if name, udp := config.SplitApp(strings.ToLower(rule.Value)); udp {
			set.udp = append(set.udp, name)
		} else {
			set.all = append(set.all, name)
		}
	}
	return set
}

// syncApps приводит набор взятых программ в соответствие с правилами.
// Вызывать под routeMu.
func (m *Manager) syncApps(spec *tunnel.Spec, rc *config.RoutingConfig) {
	names := appNames(rc)

	if m.apps == nil {
		if !names.empty() {
			log.Printf("Правила для программ (%s) на этой системе не действуют", names)
		}
		return
	}

	req := appRequest{
		apps:      names,
		viaTunnel: rc != nil && rc.Mode == config.RoutingModeVPNList,
		ifname:    m.ifname(),
		spec:      *spec,
		endpoints: m.endpointAddrPorts(spec),
	}
	if err := m.apps.SetApps(req); err != nil {
		log.Printf("Правила для программ не применены: %v", err)
	}
}

// releaseApps возвращает все взятые программы на место. Вызывать под routeMu.
func (m *Manager) releaseApps() {
	if m.apps == nil {
		return
	}
	if err := m.apps.SetApps(appRequest{}); err != nil {
		log.Printf("Программы не возвращены на место: %v", err)
	}
}

// endpointAddrPorts — адреса серверов VPN с портами. Имена разрешаются тем
// же кэшем, что и для вывода сервера мимо туннеля (defaultroute.go).
// Вызывать под routeMu.
func (m *Manager) endpointAddrPorts(spec *tunnel.Spec) []netip.AddrPort {
	var out []netip.AddrPort
	for _, endpoint := range spec.Endpoints {
		host, portText, err := net.SplitHostPort(endpoint)
		if err != nil {
			continue
		}
		port, err := strconv.ParseUint(portText, 10, 16)
		if err != nil {
			continue
		}
		addrs, err := m.endpointAddresses(host)
		if err != nil {
			continue
		}
		for _, ip := range addrs {
			if addr, ok := netip.AddrFromSlice(ip); ok {
				out = append(out, netip.AddrPortFrom(addr.Unmap(), uint16(port)))
			}
		}
	}
	return out
}
