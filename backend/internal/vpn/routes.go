package vpn

// Куда уходит трафик, пока поднят туннель: маршруты и системные серверы имён.
//
// Собрано в одном месте, потому что содержание здесь — это порядок действий:
// сначала снять поставленное нами прежде, потом поставить новое, и всё под
// одним замком. Те же самые шаги повторяются на живом туннеле, когда правила
// меняют из интерфейса.

import (
	"fmt"
	"log"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/tunnel"
)

// configureTraffic перенаправляет трафик в туннель и переключает DNS.
//
// Вызывается ПОСЛЕ рукопожатия. Раньше маршруты ставились сразу, и мёртвый
// конфиг забирал весь трафик в туннель, который никуда не ведёт: пользователь
// оставался без интернета всё время ожидания. Рукопожатию маршруты не нужны —
// оно идёт по обычному пути.
//
// Та же самая функция пересобирает маршрутизацию на живом туннеле, когда
// правила поменяли из интерфейса: последовательность действий там ровно та
// же, а два её отдельных списка успели разъехаться.
func (m *Manager) configureTraffic(spec *tunnel.Spec, routingCfg *config.RoutingConfig) error {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()

	// Посредник имён пересобирается целиком: правила по именам могли
	// появиться, исчезнуть или поменять направление вместе с режимом.
	// При первичной настройке оба вызова — пустая работа.
	m.stopNameRouting()
	m.flushRoutes()

	if err := m.configureRouting(spec, routingCfg); err != nil {
		return fmt.Errorf("не удалось настроить маршрутизацию: %w", err)
	}

	// Правила по доменам и зонам обслуживает посредник DNS: адреса для них
	// становятся известны только из ответов на запросы пользователя.
	// Не поднялся — работаем как раньше, с прямым DNS из конфига.
	servers := spec.DNS
	if proxyAddr, ok := m.startNameRouting(spec, routingCfg); ok {
		servers = []string{proxyAddr.String()}
	}

	if err := m.dns.Apply(m.ifname(), servers); err != nil {
		log.Printf("Warning: failed to configure DNS: %v", err)
	}
	m.flushDNSCache()

	return nil
}

// addRoute добавляет маршрут и запоминает его, если добавление удалось.
// Уже существующие чужие маршруты (RTNETLINK "File exists") не запоминаются,
// поэтому при пересборке мы их не удалим. Вызывать под routeMu.
func (m *Manager) addRoute(args ...string) error {
	if err := m.ip.AddRoute(args...); err != nil {
		return err
	}

	m.installedRoutes = append(m.installedRoutes, args)
	return nil
}

// flushRoutes снимает все маршруты, поставленные нами, в обратном порядке.
// Вызывать под routeMu.
//
// Обратный порядок сохранён и в пакете: маршрут мог опираться на другой,
// поставленный раньше, и снимать их вразнобой нельзя.
func (m *Manager) flushRoutes() {
	// Сначала маршруты правил, потом устройство туннеля: в режиме исключений
	// правила ложатся поверх /1-маршрутов, и снимать их надо раньше.
	routes := m.flushRuleRoutes()

	if len(routes) == 0 && len(m.installedRoutes) == 0 {
		if m.policy != nil {
			m.policy.Down()
		}
		return
	}

	for i := len(m.installedRoutes) - 1; i >= 0; i-- {
		routes = append(routes, m.installedRoutes[i])
	}

	// Исход каждого снятия здесь не важен: учёт всё равно обнуляется. Не
	// снявшийся маршрут — это либо чужая рука, либо уже исчезнувший
	// интерфейс, и в обоих случаях делать с ним нечего.
	m.ip.DelRoutes(routes)
	m.installedRoutes = nil

	// Правила выбора таблиц снимаются вместе с маршрутами: без маршрутов
	// наша таблица пуста, и держать на неё правила незачем.
	if m.policy != nil {
		m.policy.Down()
	}
}

// ApplyRouting перестраивает маршруты на живом туннеле.
// Если туннель не поднят — ничего не делает: правила применятся при подключении.
//
// По возможности правит вместо пересборки. Пересборка гасит посредника DNS, а
// вместе с ним пропадают все выученные им маршруты: после правки одного
// правила браузер заново учил адреса всех остальных, и сайты на секунду
// отваливались. Что можно поправить на месте и что нельзя — решает
// planRoutingDelta (incremental.go).
func (m *Manager) ApplyRouting(rc *config.RoutingConfig) error {
	m.mu.RLock()
	state := m.status.State
	spec := m.active
	previous := m.routingConfig
	proxyUp := m.dnsProxy != nil
	m.mu.RUnlock()

	if spec == nil {
		return nil
	}

	// Туннель ещё поднимается: маршрутов пока нет, править нечего. Правку
	// запоминаем — после рукопожатия маршруты встанут уже по ней. Раньше она
	// здесь молча терялась, и правило, добавленное в первые секунды после
	// запуска службы, не действовало до переподключения.
	if state != StateConnected {
		if state == StateConnecting || state == StateReconnecting {
			m.mu.Lock()
			m.routingConfig = rc
			m.mu.Unlock()
		}
		return nil
	}

	delta, incremental := planRoutingDelta(previous, rc)

	// Посредник имён обязан работать, если правила по именам есть. Подняться
	// он мог и не суметь — :53 бывает занят, — и тогда правка ничего не
	// исправит: нужна пересборка, она же единственная попытка запустить его
	// снова.
	if incremental && needsNameRouting(rc) && !proxyUp {
		incremental = false
	}

	if incremental {
		log.Printf("Правка маршрутизации на живом туннеле: добавлено %d, удалено %d",
			len(delta.added), len(delta.removed))

		m.applyRoutingDelta(spec, rc, delta)

		m.mu.Lock()
		m.routingConfig = rc
		m.status.RoutingError = ""
		m.mu.Unlock()

		m.notifyStatusChange()
		return nil
	}

	log.Printf("Re-applying routing rules on live tunnel")

	err := m.configureTraffic(spec, rc)

	m.mu.Lock()
	if err == nil {
		m.routingConfig = rc
		m.status.RoutingError = ""
	} else {
		// Применённой маршрутизацией остаётся прежняя: новую поставить не
		// вышло, и притворяться, что она в силе, нельзя.
		m.status.RoutingError = err.Error()
	}
	m.mu.Unlock()

	// Оповещаем в обоих случаях: отказ применения виден пользователю только
	// отсюда — HTTP на правку правил ответил задолго до неё.
	m.notifyStatusChange()

	if err != nil {
		return fmt.Errorf("не удалось пересобрать маршруты: %w", err)
	}
	return nil
}

// configureRouting sets up routing based on configuration. Вызывать под routeMu.
func (m *Manager) configureRouting(spec *tunnel.Spec, routing *config.RoutingConfig) error {
	// Правила по доменам резолвим через серверы имён из конфига НАПРЯМУЮ,
	// минуя системный резолвер.
	//
	// Через системный нельзя: пересборка правил на живом туннеле начинается с
	// остановки нашего же посредника DNS, а resolv.conf в этот момент всё ещё
	// указывает на него. Запрос уходил бы в мёртвый сокет, и правила по
	// доменам молча оставались без маршрутов — со стороны это выглядело как
	// «удалил одно правило, перестали работать все остальные».
	resolver := resolverFor(parseDNSServers(spec.DNS))

	// Get the peer's allowed IPs

	// Правила выбора таблиц ставятся раньше маршрутов: маршрут в нашей
	// таблице без правила на неё ничего не значит.
	if m.policy != nil {
		if err := m.policy.Up(fullTunnel(spec, routing)); err != nil {
			return fmt.Errorf("не удалось включить маршрутизацию по политикам: %w", err)
		}
	}

	// Программы — не адреса: им не нужны ни резолв, ни маршруты, только
	// метка (apps_linux.go).
	defer m.syncApps(spec, routing)

	if routing == nil || len(routing.Rules) == 0 {
		// Правил нет — маршрутизируем по AllowedIPs из конфига.
		//
		// AllowedIPs вида 0.0.0.0/0 нельзя добавлять напрямую: тогда в туннель
		// уйдут и сами зашифрованные пакеты к серверу VPN, получится петля —
		// туннель встаёт, пинг внутри него идёт, а интернет пропадает.
		// Полный туннель поднимаем через setupDefaultVPNRoute: он исключает
		// адрес сервера и вместо 0.0.0.0/0 ставит пару /1-маршрутов.
		if hasDefaultRoute(spec.AllowedIPs) {
			return m.setupDefaultVPNRoute(spec)
		}

		for _, ip := range spec.AllowedIPs {
			if err := m.addRoute(m.tunnelRoute(ip)...); err != nil {
				log.Printf("Warning: failed to add route %s: %v", ip, err)
			}
		}
		return nil
	}

	// В режиме «только список» серверы имён из конфига без отдельного
	// маршрута шли бы мимо туннеля, а провайдер подменяет ответы о
	// заблокированных именах: youtube.com не разрешался вовсе, и правило
	// geosite:youtube не помогало — до маршрута дело не доходило. Ставим их
	// до резолва правил, чтобы и он шёл через туннель.
	if routing.Mode != config.RoutingModeDirectList {
		m.routeDNSServers(spec)
	}

	// Все имена спрашиваются разом, и только потом ставятся маршруты.
	// Последовательный резолв был здесь главным источником задержки: сотня
	// доменов, каждый со своим таймаутом, складывалась в минуты ожидания на
	// молчащем сервере имён — и всё это внутри обработчика HTTP.
	addrs := m.resolveRules(routing.Rules, resolver, hasIPv6Address(spec.Addresses))

	if routing.Mode == config.RoutingModeDirectList {
		// Всё в туннель, кроме перечисленного. Сначала сам туннель.
		if err := m.setupDefaultVPNRoute(spec); err != nil {
			log.Printf("Warning: failed to setup default VPN route: %v", err)
		}
	}

	m.installRuleRoutes(routing.Mode, addrs)
	return nil
}
