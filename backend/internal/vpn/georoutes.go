package vpn

// Правила-наборы: geoip:ru и geosite:youtube.
//
// geosite — это те же правила по именам, только из базы: их обслуживает
// посредник DNS (nameroutes.go). geoip — подсети, известные заранее: они
// ставятся маршрутами, как обычные правила-подсети, но пачками по десятку
// тысяч, и поэтому иначе (installRuleRoutes, ветка bulk).

import (
	"log"
	"net/netip"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/routing"
)

// GeoSource — откуда берутся наборы. Его даёт geodata.Store.
type GeoSource interface {
	IPs(code string) ([]netip.Prefix, error)
	routing.Sites
}

// SetGeo подключает базы. Вызывать до первого подключения.
func (m *Manager) SetGeo(geo GeoSource) {
	m.mu.Lock()
	m.geo = geo
	m.mu.Unlock()
}

func (m *Manager) geoSource() GeoSource {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.geo
}

// sites — базы geosite для сопоставителя. Возвращает настоящий nil, а не
// интерфейс с пустым указателем внутри: сопоставитель проверяет именно nil.
func (m *Manager) sites() routing.Sites {
	if geo := m.geoSource(); geo != nil {
		return geo
	}
	return nil
}

// geoPrefixes — подсети правила geoip. IPv6 отбрасывается, если у туннеля
// нет IPv6-адреса: в режиме «только список» такие маршруты увели бы трафик в
// туннель, который его не довезёт, а в режиме «всё, кроме списка» IPv6 и так
// идёт мимо туннеля.
func (m *Manager) geoPrefixes(code string, v6 bool) ([]string, error) {
	geo := m.geoSource()
	if geo == nil {
		return nil, nil
	}

	prefixes, err := geo.IPs(code)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		if p.Addr().Is6() && !v6 {
			continue
		}
		out = append(out, p.String())
	}
	return out, nil
}

// RefreshGeo переставляет правила-наборы после обновления базы: подсети
// geoip снимаются и ставятся заново, сопоставитель geosite пересобирается под
// работающим посредником. Остальные маршруты не трогаются.
func (m *Manager) RefreshGeo() {
	m.mu.RLock()
	state := m.status.State
	spec := m.active
	rc := m.routingConfig
	proxyUp := m.dnsProxy != nil
	m.mu.RUnlock()

	if state != StateConnected || spec == nil || rc == nil {
		return
	}

	// Посредника не было — например, geosite стоял в списке, а базы ещё не
	// было. Поднять его можно только пересборкой.
	if needsNameRouting(rc) && !proxyUp {
		log.Printf("База обновлена — пересобираем маршрутизацию")
		if err := m.configureTraffic(spec, rc); err != nil {
			log.Printf("Пересборка после обновления базы: %v", err)
		}
		return
	}

	m.routeMu.Lock()
	defer m.routeMu.Unlock()

	if proxyUp {
		matcher := routing.NewMatcher(rc.Rules, m.sites())
		m.nameMatcher.Store(matcher)

		m.mu.RLock()
		set := m.dynamicRoutes
		m.mu.RUnlock()
		if set != nil {
			set.Retain(matcher.Match)
		}
	}

	var geoip []config.RoutingRule
	gone := map[string]bool{}
	for _, rule := range rc.Rules {
		if rule.Enabled && rule.Type == config.RuleTypeGeoIP {
			geoip = append(geoip, rule)
			gone[ruleKey(rule)] = true
		}
	}
	if len(geoip) == 0 {
		return
	}

	m.dropRuleRoutes(gone)
	resolver := resolverFor(parseDNSServers(spec.DNS))
	m.installRuleRoutes(rc.Mode, m.resolveRules(geoip, resolver, hasIPv6Address(spec.Addresses)))
}
