package iproute

import (
	"fmt"
	"net/netip"
	"strings"
)

// routeSpec — маршрут, разобранный из хвоста команды "ip route add".
type routeSpec struct {
	Prefix  netip.Prefix
	Gateway netip.Addr // нулевой, если шлюза нет
	Device  string
}

// parseRouteArgs разбирает "префикс [via шлюз] [dev интерфейс]".
//
// Нужен там, где команды ip нет и маршрут ставится вызовом API: менеджер
// описывает маршруты одинаково на всех системах. Незнакомое слово — ошибка, а
// не повод пропустить его: молча поставить не тот маршрут хуже, чем никакого.
func parseRouteArgs(args []string) (routeSpec, error) {
	if len(args) == 0 {
		return routeSpec{}, fmt.Errorf("пустой маршрут")
	}

	var spec routeSpec

	prefix, err := parsePrefix(args[0])
	if err != nil {
		return routeSpec{}, err
	}
	spec.Prefix = prefix

	for i := 1; i < len(args); i += 2 {
		if i+1 >= len(args) {
			return routeSpec{}, fmt.Errorf("маршрут %q: у %q нет значения", strings.Join(args, " "), args[i])
		}
		value := args[i+1]

		switch args[i] {
		case "via":
			gw, err := netip.ParseAddr(value)
			if err != nil {
				return routeSpec{}, fmt.Errorf("маршрут %q: шлюз %q: %w", strings.Join(args, " "), value, err)
			}
			if gw.Is4() != prefix.Addr().Is4() {
				return routeSpec{}, fmt.Errorf("маршрут %q: шлюз другого семейства адресов", strings.Join(args, " "))
			}
			spec.Gateway = gw
		case "dev":
			spec.Device = value
		default:
			return routeSpec{}, fmt.Errorf("маршрут %q: незнакомое слово %q", strings.Join(args, " "), args[i])
		}
	}

	return spec, nil
}

// parsePrefix принимает и подсеть, и одиночный адрес — последний
// превращается в префикс на один адрес, как это делает ip.
func parsePrefix(value string) (netip.Prefix, error) {
	value = strings.TrimSpace(value)

	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix.Masked(), nil
	}

	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("не адрес и не подсеть: %q", value)
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// formatRoute печатает маршрут так же, как его печатает "ip route show":
// по этим строкам менеджер ищет, ведёт ли маршрут в туннель.
func formatRoute(spec routeSpec) string {
	parts := []string{spec.Prefix.String()}
	if spec.Gateway.IsValid() && !spec.Gateway.IsUnspecified() {
		parts = append(parts, "via", spec.Gateway.String())
	}
	if spec.Device != "" {
		parts = append(parts, "dev", spec.Device)
	}
	return strings.Join(parts, " ")
}
