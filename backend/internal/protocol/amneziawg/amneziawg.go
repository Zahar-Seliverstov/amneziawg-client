// Package amneziawg — протокол AmneziaWG (и обычный WireGuard как его
// частный случай): разбор и проверка .conf.
//
// Пакет ничего не запускает — это работа ядра (internal/engine/awggo). Здесь
// только формат: что написано в файле и годится ли оно для подключения.
package amneziawg

import (
	"strings"

	"github.com/user/amnezia-web-client/internal/tunnel"
)

// ID — идентификатор протокола, под которым хранятся его конфигурации.
const ID = "amneziawg"

// Protocol реализует tunnel.Protocol.
type Protocol struct{}

var _ tunnel.Protocol = Protocol{}

func (Protocol) ID() string    { return ID }
func (Protocol) Title() string { return "AmneziaWG" }

// Sniff узнаёт формат по секции [Interface] — она есть в любом .conf
// WireGuard и AmneziaWG.
func (Protocol) Sniff(raw string) bool {
	return strings.Contains(strings.ToLower(raw), "[interface]")
}

func (Protocol) Parse(raw string) (tunnel.Config, error) {
	cfg, err := Parse(raw)
	if err != nil {
		// Не (*Config)(nil): типизированный nil в интерфейсе не равен nil.
		return nil, err
	}
	return cfg, nil
}
