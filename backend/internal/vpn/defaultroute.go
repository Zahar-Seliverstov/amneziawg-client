package vpn

import (
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/user/amnezia-web-client/internal/tunnel"
)

// setupDefaultVPNRoute sets up routing all traffic through VPN
func (m *Manager) setupDefaultVPNRoute(spec *tunnel.Spec) error {
	ifname := m.ifname()

	// С маршрутизацией по политикам сервер ниоткуда выводить не надо: пакеты
	// к нему несут метку туннеля и идут системной таблицей — тем путём,
	// каким система к нему и шла. Маршрут по умолчанию — один, в нашей
	// таблице, без пары /1.
	if m.policy != nil {
		if err := m.addRoute(m.tunnelRoute("0.0.0.0/0")...); err != nil {
			return fmt.Errorf("не удалось направить трафик в туннель: %w", err)
		}
		if hasIPv6Address(spec.Addresses) {
			if err := m.addRoute(m.tunnelRoute("::/0")...); err != nil {
				log.Printf("Warning: failed to add IPv6 route ::/0: %v", err)
			}
		}
		return nil
	}

	// Адрес сервера VPN должен идти В ОБХОД туннеля, иначе получится петля.
	// Endpoint может быть именем хоста — резолвим его, пока DNS ещё исправен.
	for _, endpoint := range spec.Endpoints {
		host, _, err := net.SplitHostPort(endpoint)
		if err != nil || host == "" {
			return fmt.Errorf("не разобрать адрес сервера %q: %w", endpoint, err)
		}

		addrs, err := m.endpointAddresses(host)
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
	if hasIPv6Address(spec.Addresses) {
		for _, prefix := range []string{"::/1", "8000::/1"} {
			if err := m.addRoute(prefix, "dev", ifname); err != nil {
				log.Printf("Warning: failed to add IPv6 route %s: %v", prefix, err)
			}
		}
	}

	return nil
}

// endpointAddresses возвращает адреса сервера VPN, выясняя их один раз за
// соединение. Вызывать под routeMu.
//
// Кэш здесь не роскошь. Адрес спрашивается у системного резолвера, а
// пересборка правил на живом туннеле начинается с остановки нашего же
// посредника DNS, на который resolv.conf в этот момент и указывает: запрос
// уходил в мёртвый сокет. В режиме «всё через VPN, кроме списка» это валило
// настройку туннеля целиком — правку одного правила пользователь оплачивал
// пропавшим интернетом.
//
// Разбор строки адреса идёт до всякого резолва: Endpoint чаще всего записан
// адресом, и спрашивать о нём кого бы то ни было незачем.
func (m *Manager) endpointAddresses(host string) ([]net.IP, error) {
	if addrs, ok := m.endpointAddrs[host]; ok {
		return addrs, nil
	}

	var addrs []net.IP
	if ip := net.ParseIP(host); ip != nil {
		addrs = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(host)
		if err != nil {
			return nil, err
		}
		addrs = resolved
	}

	if m.endpointAddrs == nil {
		m.endpointAddrs = make(map[string][]net.IP)
	}
	m.endpointAddrs[host] = addrs

	return addrs, nil
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
