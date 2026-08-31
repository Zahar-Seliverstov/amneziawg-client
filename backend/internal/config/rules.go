package config

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// RoutingMode defines how traffic should be routed
type RoutingMode string

const (
	// RoutingModeVPNList - route only listed items through VPN, rest direct
	RoutingModeVPNList RoutingMode = "vpn_list"
	// RoutingModeDirectList - route all through VPN except listed items
	RoutingModeDirectList RoutingMode = "direct_list"
)

// ValidRoutingMode сообщает, известен ли режим маршрутизации. Единственный
// источник правды: раньше пара сравнений была продублирована в обработчиках
// HTTP, и добавление режима требовало не забыть про каждое место.
func ValidRoutingMode(m RoutingMode) bool {
	return m == RoutingModeVPNList || m == RoutingModeDirectList
}

// RuleType — вид правила маршрутизации.
type RuleType = string

const (
	// RuleTypeIP — одиночный адрес: 1.1.1.1
	RuleTypeIP RuleType = "ip"
	// RuleTypeCIDR — подсеть: 10.0.0.0/8
	RuleTypeCIDR RuleType = "cidr"
	// RuleTypeDomain — имя и его поддомены: google.com
	RuleTypeDomain RuleType = "domain"
	// RuleTypeZone — доменная зона: .ru
	RuleTypeZone RuleType = "zone"
)

// RuleTypes перечисляет все допустимые виды правил. Единственный источник
// правды: раньше этот список был продублирован в трёх местах и разъезжался.
var RuleTypes = []RuleType{RuleTypeIP, RuleTypeCIDR, RuleTypeDomain, RuleTypeZone}

// ValidRuleType сообщает, известен ли вид правила.
func ValidRuleType(t RuleType) bool {
	for _, known := range RuleTypes {
		if t == known {
			return true
		}
	}
	return false
}

// RoutingRule represents a single routing rule
type RoutingRule struct {
	ID      string   `json:"id"`
	Type    RuleType `json:"type"`
	Value   string   `json:"value"`
	Enabled bool     `json:"enabled"`
}

// Validate проверяет правило целиком и возвращает понятную человеку причину
// отказа. Проверка живёт здесь, а не в обработчике HTTP, потому что правила
// приходят тремя путями — из интерфейса, из загруженного файла и из
// config.json на диске — и все три обязаны отсеивать мусор одинаково.
func (r *RoutingRule) Validate() error {
	if !ValidRuleType(r.Type) {
		return fmt.Errorf("неизвестный тип правила: %q", r.Type)
	}

	value := strings.TrimSpace(r.Value)
	if value == "" {
		return errors.New("значение правила не может быть пустым")
	}

	switch r.Type {
	case RuleTypeIP:
		if _, err := netip.ParseAddr(value); err != nil {
			return fmt.Errorf("%q не похоже на IP-адрес", value)
		}

	case RuleTypeCIDR:
		if _, err := netip.ParsePrefix(value); err != nil {
			return fmt.Errorf("%q не похоже на подсеть вида 10.0.0.0/8", value)
		}

	case RuleTypeDomain:
		if err := validateHostname(strings.TrimSuffix(value, ".")); err != nil {
			return fmt.Errorf("%q не похоже на доменное имя: %w", value, err)
		}

	case RuleTypeZone:
		if err := validateHostname(strings.TrimPrefix(value, ".")); err != nil {
			return fmt.Errorf("%q не похоже на доменную зону: %w", value, err)
		}
	}

	r.Value = value
	return nil
}

// validateHostname проверяет доменное имя по мягким правилам: нас интересует
// не соответствие RFC, а отсев явного мусора вроде пробелов и схем URL.
func validateHostname(name string) error {
	if name == "" {
		return errors.New("пустое имя")
	}
	if len(name) > 253 {
		return errors.New("слишком длинное имя")
	}
	if strings.ContainsAny(name, " /\\:@") {
		return errors.New("имя содержит недопустимые символы")
	}

	for _, label := range strings.Split(name, ".") {
		if label == "" {
			return errors.New("пустая часть имени")
		}
		if len(label) > 63 {
			return errors.New("слишком длинная часть имени")
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return errors.New("часть имени не может начинаться или заканчиваться дефисом")
		}
		for _, c := range label {
			isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
			isDigit := c >= '0' && c <= '9'
			// Пропускаем и не-ASCII: интернационализированные домены
			// пользователь вправе вписать как есть.
			if !isLetter && !isDigit && c != '-' && c != '_' && c < 128 {
				return fmt.Errorf("недопустимый символ %q", c)
			}
		}
	}

	return nil
}

// RoutingConfig holds all routing configuration
type RoutingConfig struct {
	Mode  RoutingMode   `json:"mode"`
	Rules []RoutingRule `json:"rules"`
}

// Clone возвращает независимую копию правил.
//
// Структура возвращается по значению, но срез Rules в поверхностной копии
// делил бы массив с оригиналом: менеджер VPN держит правила всё время
// соединения и читал бы их ровно тогда, когда интерфейс их переписывает.
func (r RoutingConfig) Clone() RoutingConfig {
	clone := r
	if r.Rules != nil {
		clone.Rules = append([]RoutingRule(nil), r.Rules...)
	}
	return clone
}

// Validate проверяет набор правил целиком и приводит его к пригодному виду:
// чинит отсутствующие и повторяющиеся идентификаторы. Первая же непригодная
// запись возвращается ошибкой — молча выбрасывать правило нельзя, иначе
// трафик пойдёт не туда, куда рассчитывал пользователь.
//
// Вызывается и на входе HTTP, и при чтении файла с диска: config.json
// пользователь вправе править руками, и оттуда приходит тот же мусор.
func (r *RoutingConfig) Validate() error {
	if !ValidRoutingMode(r.Mode) {
		return fmt.Errorf("неизвестный режим маршрутизации: %q", r.Mode)
	}

	seen := make(map[string]bool, len(r.Rules))

	for i := range r.Rules {
		rule := &r.Rules[i]

		if err := rule.Validate(); err != nil {
			return err
		}

		// В чужом файле идентификаторов может не быть или они могут
		// повторяться — тогда удаление одного правила убирало бы соседнее.
		if rule.ID == "" || seen[rule.ID] {
			rule.ID = GenerateID()
		}
		seen[rule.ID] = true
	}

	return nil
}
