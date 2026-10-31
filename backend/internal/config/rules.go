package config

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
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
	// RuleTypeFull — ровно это имя, без поддоменов: full:www.google.com
	RuleTypeFull RuleType = "full"
	// RuleTypeKeyword — имя, содержащее подстроку: keyword:google
	RuleTypeKeyword RuleType = "keyword"
	// RuleTypeRegexp — имя по регулярному выражению: regexp:^ads\.
	RuleTypeRegexp RuleType = "regexp"
	// RuleTypeGeoIP — сети из базы geoip.dat: geoip:ru
	RuleTypeGeoIP RuleType = "geoip"
	// RuleTypeGeoSite — имена из базы geosite.dat: geosite:youtube
	RuleTypeGeoSite RuleType = "geosite"
	// RuleTypeApp — программа по имени: app:minecraft. Решается не адресом и
	// не именем, а тем, какой процесс открыл соединение (см. vpn/apps_linux.go).
	RuleTypeApp RuleType = "app"
)

// RuleTypes перечисляет все допустимые виды правил. Единственный источник
// правды: раньше этот список был продублирован в трёх местах и разъезжался.
var RuleTypes = []RuleType{
	RuleTypeIP, RuleTypeCIDR, RuleTypeDomain, RuleTypeZone,
	RuleTypeFull, RuleTypeKeyword, RuleTypeRegexp, RuleTypeGeoIP, RuleTypeGeoSite,
	RuleTypeApp,
}

// prefixedTypes — виды, которые пишутся с приставкой «вид:значение», как в
// xray. Остальные узнаются по самому значению. domain: — тоже приставка:
// ею имя без точки можно записать доменом явно.
var prefixedTypes = map[string]RuleType{
	"domain":  RuleTypeDomain,
	"full":    RuleTypeFull,
	"keyword": RuleTypeKeyword,
	"regexp":  RuleTypeRegexp,
	"geoip":   RuleTypeGeoIP,
	"geosite": RuleTypeGeoSite,
	"app":     RuleTypeApp,
}

// geoTag — имя категории базы и, у geosite, атрибуты через @: ru-blocked,
// geolocation-!cn, google@cn.
var geoTag = regexp.MustCompile(`^[a-z0-9][a-z0-9!_.-]*(@[a-z0-9!_-]+)*$`)

// appName — имя программы или слово из её командной строки: minecraft, java,
// firefox, steam. Без пробелов и путей: сравнивается с именем процесса, и
// путь в правиле означал бы, что пользователь ждёт совпадения, которого не
// будет.
var appName = regexp.MustCompile(`^[a-z0-9][a-z0-9._+-]{1,63}$`)

// AppUDPSuffix — хвост правила app:, берущий у программы только UDP:
// app:vesktop/udp. Нужен там, где протоколы программы обязаны идти разными
// путями: у Discord вход и текст идут по TCP через VPN (провайдер режет TCP к
// Cloudflare), а голос и стримы по UDP — напрямую, без лишней задержки.
const AppUDPSuffix = "/udp"

// SplitApp разбирает значение правила app: на имя программы и признак
// «только UDP».
func SplitApp(value string) (name string, udpOnly bool) {
	return strings.CutSuffix(value, AppUDPSuffix)
}

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

	case RuleTypeFull:
		if err := validateHostname(strings.TrimSuffix(value, ".")); err != nil {
			return fmt.Errorf("%q не похоже на доменное имя: %w", value, err)
		}

	case RuleTypeKeyword:
		if strings.ContainsAny(value, " \t/\\:@") || len(value) > 253 {
			return fmt.Errorf("%q — не часть имени", value)
		}

	case RuleTypeRegexp:
		if len(value) > 1000 {
			return errors.New("слишком длинное выражение")
		}
		if _, err := regexp.Compile(value); err != nil {
			return fmt.Errorf("выражение %q не разобрано: %v", value, err)
		}

	case RuleTypeApp:
		value = strings.ToLower(value)
		if name, _ := SplitApp(value); !appName.MatchString(name) {
			return fmt.Errorf("%q не похоже на имя программы: нужно слово вроде minecraft или firefox, для одного UDP — vesktop/udp", value)
		}

	case RuleTypeGeoIP, RuleTypeGeoSite:
		value = strings.ToLower(value)
		if !geoTag.MatchString(value) || (r.Type == RuleTypeGeoIP && strings.Contains(value, "@")) {
			return fmt.Errorf("%q не похоже на категорию %s", value, r.Type)
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

	labels := strings.Split(name, ".")
	// Домена верхнего уровня из одних цифр не бывает (RFC 3696), а вот
	// опечатка в адресе выглядит именно так: 1.2.3.999, 10.0.0.1.5.
	if last := labels[len(labels)-1]; last != "" && strings.Trim(last, "0123456789") == "" {
		return errors.New("похоже на адрес с ошибкой: последняя часть имени — число")
	}

	for _, label := range labels {
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

// ParseRuleValue выводит вид правила из самого значения: 1.1.1.1 — адрес,
// 10.0.0.0/8 — подсеть, .ru — зона, git.example.org — домен. Наборы и особые
// виды имён пишутся с приставкой, как в xray: geoip:ru, geosite:youtube,
// full:, keyword:, regexp:.
//
// Нужен массовому вводу: там правила приходят списком по строке, и
// спрашивать вид у каждой строки отдельно не у кого. Разбор намеренно
// повторяет parseRule из окна (frontend/composables/useRuleDraft.ts) —
// пользователь набирает список там же, и решение о виде обязано совпасть.
//
// Само значение после разбора проверяется обычным Validate: здесь решается
// только вид, годность — не здесь.
func ParseRuleValue(raw string) (RoutingRule, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return RoutingRule{}, errors.New("значение правила не может быть пустым")
	}

	rule := RoutingRule{Value: value, Enabled: true}

	if kind, rest, ok := strings.Cut(value, ":"); ok {
		if t, known := prefixedTypes[strings.ToLower(kind)]; known {
			rule.Type = t
			rule.Value = strings.TrimSpace(rest)
			if t != RuleTypeRegexp {
				rule.Value = strings.ToLower(rule.Value)
			}
			if err := rule.Validate(); err != nil {
				return RoutingRule{}, err
			}
			return rule, nil
		}
	}

	switch {
	case strings.Contains(value, "/"):
		rule.Type = RuleTypeCIDR
	case strings.HasPrefix(value, "."):
		rule.Type = RuleTypeZone
		rule.Value = strings.ToLower(value)
	default:
		// Точка в конце — полная запись имени, «1.1.1.1.» всё равно адрес.
		if _, err := netip.ParseAddr(strings.TrimSuffix(value, ".")); err == nil {
			rule.Type = RuleTypeIP
			rule.Value = strings.TrimSuffix(value, ".")
		} else {
			// Слово без точки доменом быть не пытается: домен верхнего
			// уровня добавляют зоной, «.ru», а не «ru».
			if !strings.Contains(value, ".") {
				return RoutingRule{}, fmt.Errorf("%q — не правило: домен верхнего уровня пишется зоной, «.ru»", value)
			}
			rule.Type = RuleTypeDomain
			rule.Value = strings.ToLower(value)
		}
	}

	if err := rule.Validate(); err != nil {
		return RoutingRule{}, err
	}

	return rule, nil
}

// IsNameRule сообщает, решается ли правило по имени — то есть в момент ответа
// DNS, а не заранее известным адресом.
func IsNameRule(t RuleType) bool {
	switch t {
	case RuleTypeDomain, RuleTypeZone, RuleTypeFull, RuleTypeKeyword, RuleTypeRegexp, RuleTypeGeoSite:
		return true
	}
	return false
}

// SameRule сообщает, что два правила означают одно и то же. Сравнение по
// виду и значению, без учёта регистра: список правил — это множество, и
// «EXAMPLE.org» вторым правилом быть не должно.
func SameRule(a, b RoutingRule) bool {
	return a.Type == b.Type && strings.EqualFold(a.Value, b.Value)
}
