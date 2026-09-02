package iproute

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// Path — путь до адреса: через какой узел и какой интерфейс.
type Path struct {
	// Gateway — следующий узел. Пусто, когда адрес доступен прямо на линке
	// или интерфейс точка-точка: такому маршруту шлюз не нужен и указывать
	// его нельзя.
	Gateway string
	// Device — интерфейс, через который уходит пакет.
	Device string
	// Local — адрес принадлежит самой машине. Маршрут для него не ставят.
	Local bool
}

// ErrUnreachable — до адреса нет пути вовсе.
var ErrUnreachable = errors.New("адрес недостижим")

// RouteArgs собирает хвост команды "ip route add", повторяющей этот путь для
// заданного префикса.
//
// Интерфейс указывается всегда, даже когда известен шлюз: до одного и того же
// шлюза машина может дотянуться разными интерфейсами, и выбор ядра не
// обязан совпасть с тем, который мы только что видели.
func (p Path) RouteArgs(prefix string) []string {
	args := []string{prefix}
	if p.Gateway != "" {
		args = append(args, "via", p.Gateway)
	}
	if p.Device != "" {
		args = append(args, "dev", p.Device)
	}
	return args
}

// parsePath разбирает вывод "ip route get".
//
// Первая строка выглядит одним из этих способов:
//
//	192.168.96.28 via 192.168.35.1 dev tun0 src 192.168.35.38 uid 1000
//	1.1.1.1 dev awg0 src 10.8.1.18 uid 1000
//	local 127.0.0.1 dev lo src 127.0.0.1 uid 1000
//
// Дальше идёт строка "cache", она ничего не добавляет.
func parsePath(output string) (Path, error) {
	fields := strings.Fields(firstLine(output))
	if len(fields) == 0 {
		return Path{}, fmt.Errorf("ip route get: пустой ответ")
	}

	var path Path

	// Первым словом может стоять тип маршрута. Отказные типы разбирать
	// дальше незачем: маршрут через них не поставить.
	switch fields[0] {
	case "unreachable", "prohibit", "blackhole":
		return Path{}, fmt.Errorf("%w: %s", ErrUnreachable, firstLine(output))
	case "local", "broadcast", "multicast":
		path.Local = fields[0] == "local"
	}

	for i, field := range fields {
		if i+1 >= len(fields) {
			break
		}
		switch field {
		case "via":
			path.Gateway = fields[i+1]
		case "dev":
			path.Device = fields[i+1]
		}
	}

	if path.Device == "" {
		return Path{}, fmt.Errorf("ip route get: в ответе нет интерфейса: %s", firstLine(output))
	}
	return path, nil
}

// parseDefaultPath разбирает вывод "ip route show default".
//
// Маршрутов по умолчанию может быть несколько (проводная сеть и Wi-Fi
// одновременно); ядро печатает их в порядке предпочтения, поэтому берём
// первый.
func parseDefaultPath(output string) (Path, error) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "default" {
			continue
		}

		var path Path
		for i, field := range fields {
			if i+1 >= len(fields) {
				break
			}
			switch field {
			case "via":
				path.Gateway = fields[i+1]
			case "dev":
				path.Device = fields[i+1]
			}
		}

		// Маршрут по умолчанию без шлюза — обычное дело для точка-точка
		// (мобильный модем, ppp). Без интерфейса же он бесполезен.
		if path.Device == "" {
			continue
		}
		return path, nil
	}

	return Path{}, errors.New("в системе нет маршрута по умолчанию")
}

func firstLine(output string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	return strings.TrimSpace(line)
}

// isIPv6 определяет версию адреса назначения. Принимает и адрес, и подсеть.
func isIPv6(dest string) bool {
	dest = strings.TrimSpace(dest)

	if ip, _, err := net.ParseCIDR(dest); err == nil {
		return ip.To4() == nil
	}
	if ip := net.ParseIP(dest); ip != nil {
		return ip.To4() == nil
	}
	return strings.Contains(dest, ":")
}

// Host отбрасывает маску: "ip route get" спрашивают об адресе, а правила
// маршрутизации задают подсетями. Для подсети спрашиваем про её начало —
// путь до остальных её адресов тот же, если у них нет своих маршрутов.
func Host(prefix string) string {
	host, _, found := strings.Cut(strings.TrimSpace(prefix), "/")
	if !found {
		return strings.TrimSpace(prefix)
	}
	return host
}
