// Package config хранит пользовательские данные приложения: конфигурации
// AmneziaWG, правила маршрутизации и настройки.
//
// Пакет разделён по ответственности:
//   - types.go  — описание конфигурации AmneziaWG,
//   - rules.go  — правила маршрутизации и их проверка,
//   - store.go  — потокобезопасное хранилище и запись на диск,
//   - parser.go — разбор текста .conf.
package config

import (
	"time"
)

// AmneziaWGConfig represents a parsed AmneziaWG configuration
type AmneziaWGConfig struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	RawConfig string    `json:"raw_config"`
	CreatedAt time.Time `json:"created_at"`

	// Parsed fields
	Interface InterfaceConfig `json:"interface"`
	Peers     []PeerConfig    `json:"peers"`
}

type InterfaceConfig struct {
	PrivateKey string   `json:"private_key"`
	Address    []string `json:"address"`
	DNS        []string `json:"dns,omitempty"`
	MTU        int      `json:"mtu,omitempty"`

	// AmneziaWG specific parameters
	Jc   int    `json:"jc,omitempty"`   // Junk packet count
	Jmin int    `json:"jmin,omitempty"` // Junk packet minimum size
	Jmax int    `json:"jmax,omitempty"` // Junk packet maximum size
	S1   int    `json:"s1,omitempty"`   // Init packet junk size
	S2   int    `json:"s2,omitempty"`   // Response packet junk size
	S3   int    `json:"s3,omitempty"`   // Cookie reply packet junk size (v2)
	S4   int    `json:"s4,omitempty"`   // Transport packet junk size (v2)
	H1   uint32 `json:"h1,omitempty"`   // Init packet magic header
	H2   uint32 `json:"h2,omitempty"`   // Response packet magic header
	H3   uint32 `json:"h3,omitempty"`   // Underload packet magic header
	H4   uint32 `json:"h4,omitempty"`   // Transport packet magic header

	// AmneziaWG v2 parameters. Хранятся строками: значения задаются
	// диапазонами ("10-100"), а не одним числом.
	HeaderProtectionKey    string `json:"header_protection_key,omitempty"`
	ContentPaddingAddition string `json:"content_padding_addition,omitempty"`
	RekeyAfterTime         string `json:"rekey_after_time,omitempty"`
	RekeyTimeout           string `json:"rekey_timeout,omitempty"`
	RejectAfterTime        string `json:"reject_after_time,omitempty"`
	KeepaliveTimeout       string `json:"keepalive_timeout,omitempty"`
	MaxHandshakeAttempts   string `json:"max_handshake_attempts,omitempty"`

	// Переключатели: меняют формат пакетов на проводе. Хранятся как в
	// конфиге ("on"/"off"), пустая строка = параметра не было.
	RandomTrailers string `json:"random_trailers,omitempty"`
	DisableCookies string `json:"disable_cookies,omitempty"`
}

// Clone возвращает независимую копию конфигурации.
//
// Простое присваивание структуры даёт поверхностную копию: срезы адресов,
// DNS и AllowedIPs продолжали бы делить массивы с оригиналом, и защита
// мьютексом переставала бы что-либо значить, как только копия ушла наружу.
func (c AmneziaWGConfig) Clone() AmneziaWGConfig {
	clone := c
	clone.Interface.Address = cloneStrings(c.Interface.Address)
	clone.Interface.DNS = cloneStrings(c.Interface.DNS)

	clone.Peers = make([]PeerConfig, len(c.Peers))
	for i, peer := range c.Peers {
		clone.Peers[i] = peer
		clone.Peers[i].AllowedIPs = cloneStrings(peer.AllowedIPs)
	}

	return clone
}

type PeerConfig struct {
	PublicKey           string   `json:"public_key"`
	PresharedKey        string   `json:"preshared_key,omitempty"`
	Endpoint            string   `json:"endpoint,omitempty"`
	AllowedIPs          []string `json:"allowed_ips"`
	PersistentKeepalive int      `json:"persistent_keepalive,omitempty"`

	// В AmneziaWG v2 PersistentKeepalive может быть диапазоном ("25-35"),
	// поэтому исходное значение храним отдельно строкой.
	PersistentKeepaliveRaw string `json:"persistent_keepalive_raw,omitempty"`
}

// cloneStrings копирует срез, сохраняя nil как nil: пустой не-nil срез
// сериализуется в [] вместо отсутствия поля и менял бы формат файла.
func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}
