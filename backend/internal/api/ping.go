package api

import (
	"errors"
	"math"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/vpn"
)

const (
	// Три замера, берётся минимальный: одиночный ловит случайную очередь
	// по пути и скачет на десятки миллисекунд.
	pingAttempts = 3
	// Полторы секунды: живой ответ приходит за десятки миллисекунд, а
	// дольше ждать смысла нет — порт просто отфильтрован, пора к следующему.
	pingTimeout = 1500 * time.Millisecond
)

type pingResponse struct {
	Success bool    `json:"success"`
	Latency float64 `json:"latency,omitempty"`
	Target  string  `json:"target,omitempty"`
	Error   string  `json:"error,omitempty"`
}

// handlePing меряет задержку до сервера VPN установкой TCP-соединения.
// Замер идёт В ОБХОД туннеля: адрес эндпоинта исключён из него отдельным
// bypass-маршрутом, который ставит vpn.Manager, — так что это чистая
// сетевая задержка до сервера, без шифрования и обработки на нём.
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	cfg := s.pingConfig(s.vpnManager.GetStatus())
	if cfg == nil {
		jsonResponse(w, pingResponse{Error: "Нет конфигурации"})
		return
	}

	host, port := endpointHostPort(cfg)
	if host == "" {
		jsonResponse(w, pingResponse{Error: "В конфигурации нет адреса сервера"})
		return
	}

	// Разрешение имени делается до замера: поход к DNS занимает свои
	// миллисекунды и внутри отсчёта завышал бы первый пинг.
	ip, err := resolveEndpoint(host)
	if err != nil {
		jsonResponse(w, pingResponse{Error: "Не удалось определить адрес сервера"})
		return
	}

	rtt, err := tcpPing(ip, pingPorts(port))
	if err != nil {
		jsonResponse(w, pingResponse{Error: "Сервер не отвечает"})
		return
	}

	jsonResponse(w, pingResponse{
		Success: true,
		Latency: millis(rtt),
		Target:  ip.String(),
	})
}

// tcpPing возвращает наименьшее время оборота до сервера.
//
// Засекается только сама попытка соединения, и засчитывается любой ответ
// сервера: открытый порт отвечает SYN-ACK, закрытый — RST, и то и другое
// приходит ровно через один оборот, то есть меряет одно и то же. Провал —
// только молчание: значит пакет отфильтрован, и надо пробовать другой порт.
func tcpPing(ip net.IP, ports []string) (time.Duration, error) {
	for _, port := range ports {
		addr := net.JoinHostPort(ip.String(), port)

		var best time.Duration
		for i := 0; i < pingAttempts; i++ {
			start := time.Now()
			conn, err := net.DialTimeout("tcp", addr, pingTimeout)
			rtt := time.Since(start)

			if err != nil && !errors.Is(err, syscall.ECONNREFUSED) {
				break // Молчит: ждать остальные попытки на этом порту незачем.
			}
			if conn != nil {
				conn.Close()
			}
			if best == 0 || rtt < best {
				best = rtt
			}
		}

		if best > 0 {
			return best, nil
		}
	}

	return 0, errors.New("нет ответа ни на одном порту")
}

// pingPorts перечисляет порты в порядке проверки. Первым идёт порт самого
// эндпоинта: для TCP он почти наверняка закрыт (у AmneziaWG там UDP), но
// закрытый порт отвечает RST — оборот тот же, зато стучимся ровно туда, где
// сервер точно есть. Веб-порты дальше — на случай firewall, который вместо
// отказа молча роняет пакеты.
func pingPorts(endpointPort string) []string {
	ports := make([]string, 0, 3)
	if endpointPort != "" {
		ports = append(ports, endpointPort)
	}
	return append(ports, "443", "80")
}

// pingConfig выбирает конфиг, до сервера которого мерить: активный, а если
// подключения нет — выбранный на главном экране.
func (s *Server) pingConfig(status vpn.ConnectionStatus) *config.AmneziaWGConfig {
	if status.ConfigID != "" {
		if cfg := s.config.GetConfig(status.ConfigID); cfg != nil {
			return cfg
		}
	}
	if id := s.config.GetSelectedConfigID(); id != "" {
		return s.config.GetConfig(id)
	}
	return nil
}

// endpointHostPort разбирает Endpoint первого пира на хост и порт.
func endpointHostPort(cfg *config.AmneziaWGConfig) (string, string) {
	for _, peer := range cfg.Peers {
		if peer.Endpoint == "" {
			continue
		}
		host, port, err := net.SplitHostPort(peer.Endpoint)
		if err != nil {
			return peer.Endpoint, "" // Порта нет — берём как есть.
		}
		return host, port
	}
	return "", ""
}

// resolveEndpoint предпочитает IPv4: в обход туннеля выведен только он —
// vpn.Manager ставит bypass-маршрут именно для IPv4-адресов эндпоинта.
// Замер по IPv6 ушёл бы внутрь туннеля и мерил бы совсем не то.
func resolveEndpoint(host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip, nil
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip, nil
		}
	}
	if len(ips) > 0 {
		return ips[0], nil
	}
	return nil, errors.New("имя не разрешается")
}

// millis округляет до десятых: большего разрешения в этом числе нет смысла
// показывать, а «42.316999» в JSON только мешает.
func millis(d time.Duration) float64 {
	return math.Round(float64(d)/float64(time.Millisecond)*10) / 10
}
