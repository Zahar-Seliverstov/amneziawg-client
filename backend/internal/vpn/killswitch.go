package vpn

// Блокировка трафика мимо туннеля — та часть, которая решает, КОГДА её
// включать. Как именно блокировать, знает пакет firewall.
//
// Главное здесь — момент снятия. Блокировка взводится вместе с маршрутами и
// держится до явного отключения, переживая разрывы и попытки восстановления.
// Снимать её при каждом разрыве значило бы открывать щель ровно тогда, когда
// она и нужна: маршруты на исчезнувший интерфейс уже пропали, и трафик,
// который шёл в туннель, пошёл бы наружу открытым.

import (
	"log"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/firewall"
)

// KillSwitchState описывает блокировку для интерфейса.
type KillSwitchState struct {
	// Enabled — настройка пользователя.
	Enabled bool `json:"enabled"`
	// Available — блокировку вообще можно включить на этой машине.
	Available bool `json:"available"`
	// Active — блокировка сейчас действительно стоит.
	Active bool `json:"active"`
	// Reason — почему недоступна либо почему не действует при включённой
	// настройке.
	Reason string `json:"reason,omitempty"`
}

// SetKillSwitchEnabled запоминает настройку и сразу приводит блокировку в
// соответствие с ней: включать её должно быть можно на живом туннеле, не
// переподключаясь.
func (m *Manager) SetKillSwitchEnabled(on bool) {
	m.mu.Lock()
	m.killSwitchOn = on
	cfg, rc := m.activeConfig, m.routingConfig
	m.mu.Unlock()

	m.routeMu.Lock()
	defer m.routeMu.Unlock()
	m.syncKillSwitch(cfg, rc)
}

// KillSwitchState собирает состояние блокировки для интерфейса.
func (m *Manager) KillSwitchState() KillSwitchState {
	m.mu.RLock()
	state := KillSwitchState{
		Enabled:   m.killSwitchOn,
		Available: m.firewallDriver != nil,
		Active:    m.status.KillSwitch,
	}
	driverErr := m.firewallErr
	cfg, rc := m.activeConfig, m.routingConfig
	m.mu.RUnlock()

	if !state.Available {
		state.Reason = "Блокировка недоступна: " + firewall.Reason(driverErr)
		return state
	}

	// Включённая настройка, которая ни на что не влияет, — худший вид
	// защиты: пользователь уверен, что закрыт, а он открыт. Объясняем прямо.
	if state.Enabled && !state.Active && cfg != nil {
		if _, reason := killSwitchApplicable(cfg, rc); reason != "" {
			state.Reason = reason
		}
	}

	return state
}

// syncKillSwitch приводит блокировку в соответствие с настройкой и текущей
// маршрутизацией. Вызывать под routeMu.
func (m *Manager) syncKillSwitch(cfg *config.AmneziaWGConfig, rc *config.RoutingConfig) {
	m.mu.RLock()
	on, driver := m.killSwitchOn, m.firewallDriver
	ifname := m.interfaceName
	m.mu.RUnlock()

	applicable := false
	if cfg != nil {
		applicable, _ = killSwitchApplicable(cfg, rc)
	}

	if !on || driver == nil || !applicable {
		m.clearKillSwitch()
		return
	}

	if err := driver.Apply(firewall.Rules{Interface: ifname}); err != nil {
		// Молчать нельзя: пользователь включил защиту, а её нет.
		log.Printf("Не удалось включить блокировку трафика мимо туннеля: %v", err)
		m.setKillSwitchActive(false)
		return
	}

	log.Printf("Блокировка трафика мимо туннеля включена (%s)", driver.Name())
	m.setKillSwitchActive(true)
}

// clearKillSwitch снимает блокировку. Вызывать под routeMu.
func (m *Manager) clearKillSwitch() {
	m.mu.RLock()
	driver, active := m.firewallDriver, m.status.KillSwitch
	m.mu.RUnlock()

	if driver == nil || !active {
		return
	}

	if err := driver.Clear(); err != nil {
		// Здесь молчать опаснее всего: не снятая блокировка оставляет машину
		// без сети, и починить это можно только руками.
		log.Printf("НЕ УДАЛОСЬ СНЯТЬ БЛОКИРОВКУ ТРАФИКА: %v. "+
			"Снять вручную: sudo nft delete table inet awg_killswitch", err)
		return
	}

	log.Printf("Блокировка трафика мимо туннеля снята")
	m.setKillSwitchActive(false)
}

func (m *Manager) setKillSwitchActive(active bool) {
	m.mu.Lock()
	changed := m.status.KillSwitch != active
	m.status.KillSwitch = active
	m.mu.Unlock()

	if changed {
		m.notifyStatusChange()
	}
}

// killSwitchApplicable сообщает, имеет ли блокировка смысл при текущей
// маршрутизации, и если нет — почему.
//
// Смысл она имеет только когда в туннель уходит ВЕСЬ трафик. Там, где часть
// маршрутов ведёт наружу намеренно — режим «только список через VPN» или
// исключения в режиме «всё через VPN, кроме списка», — блокировка обрезала бы
// ровно то, что пользователь сам и вывел мимо туннеля. Так же поступает
// wg-quick: его правило ставится только при AllowedIPs = 0.0.0.0/0.
//
// Условия намеренно повторяют configureRouting: разъехавшись, они дали бы
// худший из возможных исходов — блокировку, которая режет разрешённое.
func killSwitchApplicable(cfg *config.AmneziaWGConfig, rc *config.RoutingConfig) (bool, string) {
	var allowed []string
	for _, peer := range cfg.Peers {
		allowed = append(allowed, peer.AllowedIPs...)
	}

	// Правил нет — маршрутизация идёт по AllowedIPs.
	if rc == nil || len(rc.Rules) == 0 {
		if hasDefaultRoute(allowed) {
			return true, ""
		}
		return false, "Конфигурация уводит в туннель не весь трафик: в AllowedIPs нет 0.0.0.0/0. Блокировать нечего — остальное и должно идти напрямую"
	}

	switch rc.Mode {
	case config.RoutingModeVPNList:
		return false, "В режиме «только список через VPN» остальной трафик идёт напрямую по вашему выбору — блокировка обрезала бы именно его"

	case config.RoutingModeDirectList:
		for _, rule := range rc.Rules {
			if rule.Enabled {
				return false, "В списке исключений есть правила: они выведены мимо туннеля намеренно, и блокировка перекрыла бы их"
			}
		}
		return true, ""
	}

	return false, "Неизвестный режим маршрутизации"
}
