package amneziawg

import (
	"strings"

	"github.com/user/amnezia-web-client/internal/tunnel"
)

// Config — разобранный .conf.
type Config struct {
	Interface Interface
	Peers     []Peer
}

// Interface — секция [Interface].
type Interface struct {
	PrivateKey string
	Addresses  []string
	DNS        []string
	MTU        int
	// Obfuscation — параметры маскировки AmneziaWG в порядке появления в
	// файле. У обычного WireGuard их нет.
	Obfuscation []Param
}

// Peer — секция [Peer].
type Peer struct {
	PublicKey    string
	PresharedKey string
	Endpoint     string
	AllowedIPs   []string
	// Keepalive — PersistentKeepalive как написан: число или, в AmneziaWG
	// v2, диапазон ("25-35"). Пусто — не задан.
	Keepalive string
}

var _ tunnel.Config = (*Config)(nil)

// Protocol реализует tunnel.Config.
func (c *Config) Protocol() string { return ID }

// Spec реализует tunnel.Config.
func (c *Config) Spec() tunnel.Spec {
	spec := tunnel.Spec{
		Addresses: clone(c.Interface.Addresses),
		DNS:       clone(c.Interface.DNS),
		MTU:       c.Interface.MTU,
	}
	for _, peer := range c.Peers {
		if peer.Endpoint != "" {
			spec.Endpoints = append(spec.Endpoints, peer.Endpoint)
		}
		spec.AllowedIPs = append(spec.AllowedIPs, peer.AllowedIPs...)
		// Пир с keepalive шлёт первый пакет сам и рукопожатие начинает без
		// пользовательского трафика.
		if keepaliveOn(peer.Keepalive) {
			spec.SelfStarting = true
		}
	}
	return spec
}

// keepaliveOn: "0" и "0-0" значат «выключено».
func keepaliveOn(value string) bool {
	return strings.Trim(value, "0- ") != ""
}

func clone(values []string) []string {
	return append([]string(nil), values...)
}
