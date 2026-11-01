// Реализация Tool для Windows — через IP Helper API (iphlpapi.dll).
//
// Команды ip здесь нет, а route.exe и netsh пришлось бы разбирать по
// локализованному выводу. API отвечает структурами и не зависит от языка
// системы. Интерфейсы называются так же, как их показывает сама Windows
// («Ethernet», «Wi-Fi», «awg0»): это поле Alias.

package iproute

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// Available на Windows всегда успешна: IP Helper API есть в любой системе.
func Available() error {
	return nil
}

// New возвращает Tool поверх IP Helper API.
func New() Tool {
	return winTool{}
}

type winTool struct{}

// luidByAlias находит интерфейс по имени, которое показывает Windows.
func luidByAlias(alias string) (winipcfg.LUID, error) {
	rows, err := winipcfg.GetIfTable2Ex(winipcfg.MibIfEntryNormalWithoutStatistics)
	if err != nil {
		return 0, fmt.Errorf("не удалось получить список интерфейсов: %w", err)
	}
	for i := range rows {
		if strings.EqualFold(rows[i].Alias(), alias) {
			return rows[i].InterfaceLUID, nil
		}
	}
	return 0, fmt.Errorf("интерфейс %q не найден", alias)
}

// aliasOf возвращает имя интерфейса по его LUID.
func aliasOf(luid winipcfg.LUID) string {
	row, err := luid.Interface()
	if err != nil {
		return ""
	}
	return row.Alias()
}

func (winTool) AddAddress(prefix, dev string) error {
	p, err := netip.ParsePrefix(strings.TrimSpace(prefix))
	if err != nil {
		return fmt.Errorf("адрес %q: %w", prefix, err)
	}
	luid, err := luidByAlias(dev)
	if err != nil {
		return err
	}
	return luid.AddIPAddress(p)
}

// LinkUp на Windows не поднимает интерфейс — адаптер wintun работает с
// момента запуска устройства, — а настраивает его так же, как это делает
// официальный клиент WireGuard:
//
//   - метрика 0 и без автоматики: из всех интерфейсов Windows спрашивает
//     серверы имён того, у кого метрика меньше, и только так запросы имён
//     пойдут в туннель, а не провайдеру. Маршрутам это тоже помогает: при
//     равной длине префикса выигрывает меньшая сумма метрик;
//   - без обнаружения маршрутизаторов и проверки адреса на дубликат (DAD):
//     в туннеле их некому делать, а DAD задерживает появление адреса на
//     секунду-другую.
func (winTool) LinkUp(dev string) error {
	luid, err := luidByAlias(dev)
	if err != nil {
		return err
	}

	var errs []error
	for _, family := range []winipcfg.AddressFamily{windows.AF_INET, windows.AF_INET6} {
		ipif, err := luid.IPInterface(family)
		if err != nil {
			// IPv6 на машине бывает выключен — тогда у интерфейса нет и
			// этой половины, и настраивать её нечего.
			if family == windows.AF_INET6 {
				continue
			}
			errs = append(errs, err)
			continue
		}

		ipif.RouterDiscoveryBehavior = winipcfg.RouterDiscoveryDisabled
		ipif.DadTransmits = 0
		ipif.ManagedAddressConfigurationSupported = false
		ipif.OtherStatefulConfigurationSupported = false
		ipif.UseAutomaticMetric = false
		ipif.Metric = 0

		if err := ipif.Set(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (winTool) HasGlobalAddress(dev string) (bool, error) {
	luid, err := luidByAlias(dev)
	if err != nil {
		return false, err
	}

	rows, err := winipcfg.GetUnicastIPAddressTable(windows.AF_UNSPEC)
	if err != nil {
		return false, err
	}
	for i := range rows {
		if rows[i].InterfaceLUID != luid {
			continue
		}
		addr := rows[i].Address.Addr()
		if addr.IsValid() && !addr.IsLinkLocalUnicast() && !addr.IsLoopback() {
			return true, nil
		}
	}
	return false, nil
}

// nextHop — шлюз маршрута в том виде, в каком его ждёт API: маршрут по
// линку задаётся нулевым адресом своего семейства.
func nextHop(spec routeSpec) netip.Addr {
	if spec.Gateway.IsValid() {
		return spec.Gateway
	}
	if spec.Prefix.Addr().Is4() {
		return netip.IPv4Unspecified()
	}
	return netip.IPv6Unspecified()
}

// routeTarget разбирает маршрут и находит его интерфейс.
func routeTarget(args []string) (routeSpec, winipcfg.LUID, error) {
	spec, err := parseRouteArgs(args)
	if err != nil {
		return routeSpec{}, 0, err
	}
	if spec.Device == "" {
		return routeSpec{}, 0, fmt.Errorf("маршрут %q: не указан интерфейс", strings.Join(args, " "))
	}
	luid, err := luidByAlias(spec.Device)
	if err != nil {
		return routeSpec{}, 0, err
	}
	return spec, luid, nil
}

func (winTool) AddRoute(args ...string) error {
	spec, luid, err := routeTarget(args)
	if err != nil {
		return err
	}
	if err := luid.AddRoute(spec.Prefix, nextHop(spec), 0); err != nil {
		if errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
			err = errors.Join(ErrExists, err)
		}
		return fmt.Errorf("маршрут %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (winTool) DelRoute(args ...string) error {
	spec, luid, err := routeTarget(args)
	if err != nil {
		return err
	}
	if err := luid.DeleteRoute(spec.Prefix, nextHop(spec)); err != nil {
		return fmt.Errorf("маршрут %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// AddRoutes и DelRoutes здесь просто идут по списку: вызов API не порождает
// процесс, и складывать маршруты в пакет незачем.
func (t winTool) AddRoutes(routes [][]string) []error {
	errs := make([]error, len(routes))
	for i, args := range routes {
		errs[i] = t.AddRoute(args...)
	}
	return errs
}

func (t winTool) DelRoutes(routes [][]string) []error {
	errs := make([]error, len(routes))
	for i, args := range routes {
		errs[i] = t.DelRoute(args...)
	}
	return errs
}

var procGetBestRoute2 = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetBestRoute2")

// bestRoute спрашивает у системы, каким маршрутом она пойдёт к адресу
// сейчас. В winipcfg этой функции нет, поэтому вызов прямой.
func bestRoute(addr netip.Addr) (*winipcfg.MibIPforwardRow2, error) {
	row, _, err := bestRouteFrom(addr)
	return row, err
}

// SourceFor — адрес, с которого система сейчас пошла бы к addr, и
// интерфейс этого пути.
func SourceFor(addr netip.Addr) (netip.Addr, winipcfg.LUID, error) {
	row, source, err := bestRouteFrom(addr)
	if err != nil {
		return netip.Addr{}, 0, err
	}
	return source, row.InterfaceLUID, nil
}

func bestRouteFrom(addr netip.Addr) (*winipcfg.MibIPforwardRow2, netip.Addr, error) {
	var dest, source winipcfg.RawSockaddrInet
	if err := dest.SetAddr(addr); err != nil {
		return nil, netip.Addr{}, err
	}

	var row winipcfg.MibIPforwardRow2
	ret, _, _ := procGetBestRoute2.Call(
		0, // InterfaceLuid: любой
		0, // InterfaceIndex: любой
		0, // SourceAddress: любой
		uintptr(unsafe.Pointer(&dest)),
		0, // AddressSortOptions
		uintptr(unsafe.Pointer(&row)),
		uintptr(unsafe.Pointer(&source)),
	)
	if ret != 0 {
		err := windows.Errno(ret)
		if err == windows.ERROR_NETWORK_UNREACHABLE || err == windows.ERROR_HOST_UNREACHABLE || err == windows.ERROR_NOT_FOUND {
			return nil, netip.Addr{}, fmt.Errorf("%w: %s", ErrUnreachable, addr)
		}
		return nil, netip.Addr{}, fmt.Errorf("GetBestRoute2 %s: %w", addr, err)
	}
	return &row, source.Addr(), nil
}

// isLocalAddress сообщает, назначен ли адрес самой машине.
func isLocalAddress(addr netip.Addr) bool {
	rows, err := winipcfg.GetUnicastIPAddressTable(windows.AF_UNSPEC)
	if err != nil {
		return false
	}
	for i := range rows {
		if rows[i].Address.Addr() == addr {
			return true
		}
	}
	return false
}

func (winTool) PathTo(addr string) (Path, error) {
	ip, err := netip.ParseAddr(Host(addr))
	if err != nil {
		return Path{}, fmt.Errorf("адрес %q: %w", addr, err)
	}
	ip = ip.Unmap()

	if ip.IsLoopback() || isLocalAddress(ip) {
		return Path{Local: true, Device: "loopback"}, nil
	}

	row, err := bestRoute(ip)
	if err != nil {
		return Path{}, err
	}
	return pathOf(row), nil
}

// pathOf переводит строку таблицы маршрутизации в Path.
func pathOf(row *winipcfg.MibIPforwardRow2) Path {
	path := Path{Device: aliasOf(row.InterfaceLUID)}
	if gw := row.NextHop.Addr(); gw.IsValid() && !gw.IsUnspecified() {
		path.Gateway = gw.String()
	}
	return path
}

func family(v6 bool) winipcfg.AddressFamily {
	if v6 {
		return windows.AF_INET6
	}
	return windows.AF_INET
}

// DefaultPathFor выбирает маршрут по умолчанию так же, как Windows: по
// наименьшей сумме метрики маршрута и метрики интерфейса, только среди
// работающих интерфейсов.
func (winTool) DefaultPathFor(dest string) (Path, error) {
	fam := family(isIPv6(dest))

	rows, err := winipcfg.GetIPForwardTable2(fam)
	if err != nil {
		return Path{}, err
	}

	var best *winipcfg.MibIPforwardRow2
	bestMetric := ^uint64(0)

	for i := range rows {
		row := &rows[i]
		if row.DestinationPrefix.Prefix().Bits() != 0 {
			continue
		}
		if ifRow, err := row.InterfaceLUID.Interface(); err != nil || ifRow.OperStatus != winipcfg.IfOperStatusUp {
			continue
		}
		ipif, err := row.InterfaceLUID.IPInterface(fam)
		if err != nil {
			continue
		}

		metric := uint64(row.Metric) + uint64(ipif.Metric)
		if metric < bestMetric {
			best, bestMetric = row, metric
		}
	}

	if best == nil {
		name := "IPv4"
		if fam == windows.AF_INET6 {
			name = "IPv6"
		}
		return Path{}, fmt.Errorf("в системе нет маршрута по умолчанию (%s)", name)
	}
	return pathOf(best), nil
}

func (winTool) RoutesFor(prefix string) ([]string, error) {
	want, err := parsePrefix(prefix)
	if err != nil {
		return nil, err
	}

	rows, err := winipcfg.GetIPForwardTable2(family(want.Addr().Is6()))
	if err != nil {
		return nil, err
	}

	var routes []string
	for i := range rows {
		if rows[i].DestinationPrefix.Prefix() != want {
			continue
		}
		routes = append(routes, formatRoute(routeSpec{
			Prefix:  want,
			Gateway: rows[i].NextHop.Addr(),
			Device:  aliasOf(rows[i].InterfaceLUID),
		}))
	}
	return routes, nil
}
