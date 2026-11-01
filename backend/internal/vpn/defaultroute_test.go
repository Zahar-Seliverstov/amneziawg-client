package vpn

import (
	"net"
	"strings"
	"testing"

	"github.com/user/amnezia-web-client/internal/iproute"
	"github.com/user/amnezia-web-client/internal/tunnel"
)

// Регрессия. Адрес сервера спрашивается у системного резолвера, а пересборка
// правил на живом туннеле начинается с остановки нашего же посредника DNS, на
// который resolv.conf в этот момент и указывает. Запрос уходил в мёртвый
// сокет, и в режиме «всё через VPN, кроме списка» настройка туннеля
// отказывала целиком: правку одного правила пользователь оплачивал пропавшим
// интернетом. Выясненный при подключении адрес держится всё соединение.
func TestEndpointAddressIsRememberedForTheConnection(t *testing.T) {
	ip := &fakeIP{
		paths: map[string]iproute.Path{"203.0.113.10": homeGateway()},
		def:   homeGateway(),
	}
	m := managerWith(ip)

	// Имя заведомо неразрешимое: .invalid зарезервирован ровно для этого.
	cfg := &tunnel.Spec{
		Addresses: []string{"10.8.1.18/32"},
		Endpoints: []string{"сервера-нет.invalid:51820"},
	}

	// Ровно то, что случается при подключении: адрес выяснен и запомнен.
	// Спроси клиент имя заново — .invalid не разрешается ни у кого, и
	// настройка отказала бы.
	m.endpointAddrs = map[string][]net.IP{"сервера-нет.invalid": {net.ParseIP("203.0.113.10")}}

	if err := m.setupDefaultVPNRoute(cfg); err != nil {
		t.Fatalf("запомненный адрес сервера не пригодился: %v", err)
	}

	if !containsRoute(ip.joinedAdded(), "203.0.113.10/32") {
		t.Errorf("адрес сервера не выведен мимо туннеля: %v", ip.joinedAdded())
	}
}

// Endpoint чаще всего записан адресом. Спрашивать о нём кого бы то ни было
// незачем — и молчащий резолвер такому соединению не помеха.
func TestLiteralEndpointIsNotResolved(t *testing.T) {
	ip := &fakeIP{
		paths: map[string]iproute.Path{"203.0.113.10": homeGateway()},
		def:   homeGateway(),
	}
	m := managerWith(ip)

	cfg := &tunnel.Spec{
		Addresses: []string{"10.8.1.18/32"},
		Endpoints: []string{"203.0.113.10:51820"},
	}

	if err := m.setupDefaultVPNRoute(cfg); err != nil {
		t.Fatalf("настройка не удалась: %v", err)
	}

	if len(m.endpointAddrs["203.0.113.10"]) != 1 {
		t.Errorf("адрес сервера не запомнен: %v", m.endpointAddrs)
	}
}

func containsRoute(routes []string, prefix string) bool {
	for _, route := range routes {
		if strings.HasPrefix(route, prefix+" ") {
			return true
		}
	}
	return false
}
