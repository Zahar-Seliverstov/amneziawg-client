package tunnel

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// Registry — набор протоколов и ядер, известных службе.
//
// Заполняется один раз при старте и дальше только читается, поэтому
// блокировок не требует.
type Registry struct {
	protocols []Protocol
	engines   []Engine
}

// NewRegistry собирает реестр. Порядок важен: при распознавании формата
// протоколы опрашиваются по очереди, ядро для профиля — тоже.
func NewRegistry(protocols []Protocol, engines []Engine) *Registry {
	return &Registry{protocols: protocols, engines: engines}
}

// ErrUnknownFormat — текст не похож ни на один известный протокол.
var ErrUnknownFormat = errors.New("формат конфигурации не распознан")

// Protocols — все протоколы в порядке регистрации.
func (r *Registry) Protocols() []Protocol {
	return append([]Protocol(nil), r.protocols...)
}

// Protocol находит протокол по идентификатору.
func (r *Registry) Protocol(id string) (Protocol, bool) {
	for _, p := range r.protocols {
		if p.ID() == id {
			return p, true
		}
	}
	return nil, false
}

// Detect выбирает протокол по тексту конфигурации.
func (r *Registry) Detect(raw string) (Protocol, error) {
	for _, p := range r.protocols {
		if p.Sniff(raw) {
			return p, nil
		}
	}
	return nil, ErrUnknownFormat
}

// Parse разбирает конфигурацию. Пустой id означает «распознать по тексту»:
// так разбирается всё, что вставил пользователь, и записи, сохранённые до
// появления поля протокола.
func (r *Registry) Parse(id, raw string) (Config, error) {
	var (
		p   Protocol
		err error
	)
	if id == "" {
		p, err = r.Detect(raw)
	} else if found, ok := r.Protocol(id); ok {
		p = found
	} else {
		err = fmt.Errorf("протокол %q не поддерживается этой версией", id)
	}
	if err != nil {
		return nil, err
	}
	return p.Parse(raw)
}

// Engine выбирает ядро для конфигурации.
func (r *Registry) Engine(cfg Config) (Engine, error) {
	for _, e := range r.engines {
		if e.Supports(cfg) {
			return e, nil
		}
	}
	return nil, fmt.Errorf("нет ядра для протокола %q", cfg.Protocol())
}

// SuggestName придумывает имя конфигурации, когда пользователь его не задал:
// адрес сервера, а без него — адрес интерфейса. Именно по ним конфигурацию и
// узнают.
func SuggestName(spec Spec) string {
	for _, endpoint := range spec.Endpoints {
		if host := Host(endpoint); host != "" {
			return host
		}
	}
	for _, addr := range spec.Addresses {
		if host, _, _ := strings.Cut(strings.TrimSpace(addr), "/"); host != "" {
			return host
		}
	}
	return "Конфигурация"
}

// Host вырезает хост из "host:port" и "[ipv6]:port"; адрес без порта
// возвращается как есть.
func Host(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if host, _, err := net.SplitHostPort(endpoint); err == nil {
		return host
	}
	return strings.Trim(endpoint, "[]")
}
