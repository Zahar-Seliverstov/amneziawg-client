package vpn

// Сам интерфейс туннеля: адреса и состояние линка.
//
// Маршрутов здесь нет намеренно: они ставятся позже, уже после рукопожатия,
// и живут отдельно — см. routes.go.

import (
	"fmt"
	"log"

	"github.com/user/amnezia-web-client/internal/config"
)

// configureLink поднимает интерфейс и вешает на него адреса. Маршрутов здесь
// нет намеренно — см. configureTraffic.
func (m *Manager) configureLink(cfg *config.AmneziaWGConfig) error {
	ifname := m.ifname()

	for _, addr := range cfg.Interface.Address {
		if err := m.ip.AddAddress(addr, ifname); err != nil {
			// Отдельный адрес мог не лечь по безобидной причине — например,
			// он уже назначен. Итог проверяем по состоянию интерфейса ниже.
			log.Printf("Не удалось назначить адрес %s: %v", addr, err)
		}
	}

	// MTU задан при создании TUN, дублировать командой не нужно.
	if err := m.ip.LinkUp(ifname); err != nil {
		return fmt.Errorf("не удалось поднять интерфейс: %w", err)
	}

	// Интерфейс без единого адреса ничего не отправит. Раньше это проходило
	// одним предупреждением в журнал: туннель поднимался, состояние
	// становилось «Подключено», а трафик не шёл — и связи между этим и
	// строчкой в логе пользователь не видел.
	//
	// Спрашиваем систему, а не считаем удачные вызовы: адрес мог быть уже
	// назначен, и тогда ошибка команды означает успех.
	if !m.interfaceHasAddress(ifname) {
		return fmt.Errorf("интерфейсу %s не назначен ни один адрес — проверьте Address в конфигурации", ifname)
	}

	return nil
}

// interfaceHasAddress сообщает, есть ли у интерфейса адрес, пригодный для
// отправки. Локальные адреса канала (scope link) не в счёт: они появляются
// сами и ничего не значат.
func (m *Manager) interfaceHasAddress(ifname string) bool {
	has, err := m.ip.HasGlobalAddress(ifname)
	if err != nil {
		// Не смогли спросить — не наказываем: настоящую причину увидим
		// дальше, когда трафик не пойдёт.
		log.Printf("Не удалось проверить адреса интерфейса %s: %v", ifname, err)
		return true
	}
	return has
}
