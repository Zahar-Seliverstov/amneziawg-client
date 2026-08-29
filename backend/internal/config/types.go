package config

import (
	"fmt"
	"log"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AmneziaWGConfig represents a parsed AmneziaWG configuration
type AmneziaWGConfig struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	RawConfig   string    `json:"raw_config"`
	CreatedAt   time.Time `json:"created_at"`
	
	// Parsed fields
	Interface   InterfaceConfig `json:"interface"`
	Peers       []PeerConfig    `json:"peers"`
}

type InterfaceConfig struct {
	PrivateKey string   `json:"private_key"`
	Address    []string `json:"address"`
	DNS        []string `json:"dns,omitempty"`
	MTU        int      `json:"mtu,omitempty"`
	
	// AmneziaWG specific parameters
	Jc  int `json:"jc,omitempty"`  // Junk packet count
	Jmin int `json:"jmin,omitempty"` // Junk packet minimum size
	Jmax int `json:"jmax,omitempty"` // Junk packet maximum size
	S1  int `json:"s1,omitempty"`  // Init packet junk size
	S2  int `json:"s2,omitempty"`  // Response packet junk size
	S3  int `json:"s3,omitempty"`  // Cookie reply packet junk size (v2)
	S4  int `json:"s4,omitempty"`  // Transport packet junk size (v2)
	H1  uint32 `json:"h1,omitempty"`  // Init packet magic header
	H2  uint32 `json:"h2,omitempty"`  // Response packet magic header
	H3  uint32 `json:"h3,omitempty"`  // Underload packet magic header
	H4  uint32 `json:"h4,omitempty"`  // Transport packet magic header
	
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

// RoutingMode defines how traffic should be routed
type RoutingMode string

const (
	// RoutingModeVPNList - route only listed items through VPN, rest direct
	RoutingModeVPNList RoutingMode = "vpn_list"
	// RoutingModeDirectList - route all through VPN except listed items
	RoutingModeDirectList RoutingMode = "direct_list"
)

// RoutingRule represents a single routing rule
type RoutingRule struct {
	ID       string `json:"id"`
	Type     string `json:"type"`      // "ip", "cidr", "domain", "zone"
	Value    string `json:"value"`     // actual value: 1.1.1.1, 10.0.0.0/8, google.com, .ru
	Enabled  bool   `json:"enabled"`
}

// RoutingConfig holds all routing configuration
type RoutingConfig struct {
	Mode  RoutingMode   `json:"mode"`
	Rules []RoutingRule `json:"rules"`
}

// AppSettings holds user preferences
type AppSettings struct {
	Autoconnect bool `json:"autoconnect"`
}

// AppConfig is the main application configuration
type AppConfig struct {
	mu       sync.RWMutex
	
	Configs          []AmneziaWGConfig `json:"configs"`
	// SelectedConfigID — конфиг, выбранный пользователем на главном экране.
	// Именно к нему подключается автоподключение при запуске.
	SelectedConfigID string            `json:"selected_config_id,omitempty"`
	Routing         RoutingConfig     `json:"routing"`
	Settings        AppSettings       `json:"settings"`
	
	configPath string
}

// NewAppConfig creates a new application configuration
func NewAppConfig(configPath string) *AppConfig {
	return &AppConfig{
		configPath: configPath,
		Configs:    []AmneziaWGConfig{},
		Routing: RoutingConfig{
			Mode:  RoutingModeVPNList,
			Rules: []RoutingRule{},
		},
	}
}

// Load loads configuration from disk
func (c *AppConfig) Load() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	data, err := os.ReadFile(c.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No config file yet, use defaults
		}
		return err
	}
	
	if err := json.Unmarshal(data, c); err != nil {
		return err
	}
	
	// Заново разбираем сохранённые конфиги из исходного текста.
	//
	// На диск кладётся уже разобранная структура, поэтому конфиг, добавленный
	// старой версией, навсегда остался бы без полей, которых та версия не
	// знала (например, параметров AmneziaWG v2). Перечитывание из raw_config
	// подтягивает их без ручного пересоздания конфига.
	for i := range c.Configs {
		raw := c.Configs[i].RawConfig
		if raw == "" {
			continue
		}
		
		reparsed, err := ParseAmneziaConfig(c.Configs[i].Name, raw)
		if err != nil {
			log.Printf("Warning: cannot re-parse config %q, keeping stored form: %v", c.Configs[i].Name, err)
			continue
		}
		
		// Идентификаторы и дату создания сохраняем.
		reparsed.ID = c.Configs[i].ID
		reparsed.CreatedAt = c.Configs[i].CreatedAt
		c.Configs[i] = *reparsed
	}
	
	return nil
}

// Save saves configuration to disk
func (c *AppConfig) Save() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	// Ensure directory exists
	dir := filepath.Dir(c.configPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	
	return os.WriteFile(c.configPath, data, 0600)
}

// AddConfig adds a new WireGuard config
func (c *AppConfig) AddConfig(cfg AmneziaWGConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Configs = append(c.Configs, cfg)
}

// GetConfig returns a config by ID
func (c *AppConfig) GetConfig(id string) *AmneziaWGConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	for i := range c.Configs {
		if c.Configs[i].ID == id {
			return &c.Configs[i]
		}
	}
	return nil
}

// UpdateConfig заменяет конфиг по ID, сохраняя идентификатор и дату создания.
func (c *AppConfig) UpdateConfig(id string, cfg AmneziaWGConfig) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	for i := range c.Configs {
		if c.Configs[i].ID != id {
			continue
		}
		
		cfg.ID = id
		cfg.CreatedAt = c.Configs[i].CreatedAt
		c.Configs[i] = cfg
		return true
	}
	
	return false
}

// DeleteConfig removes a config by ID
func (c *AppConfig) DeleteConfig(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	for i := range c.Configs {
		if c.Configs[i].ID == id {
			c.Configs = append(c.Configs[:i], c.Configs[i+1:]...)
			if c.SelectedConfigID == id {
				c.SelectedConfigID = ""
			}
			return true
		}
	}
	return false
}

// UniqueConfigName делает имя неповторяющимся: одинаковые имена в списке
// не различить глазами. excludeID — конфиг, который это имя уже носит сам
// (нужно при правке, иначе своё же имя считалось бы занятым).
func (c *AppConfig) UniqueConfigName(name, excludeID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	taken := func(candidate string) bool {
		for _, cfg := range c.Configs {
			if cfg.ID != excludeID && strings.EqualFold(cfg.Name, candidate) {
				return true
			}
		}
		return false
	}
	
	if !taken(name) {
		return name
	}
	
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s %d", name, i)
		if !taken(candidate) {
			return candidate
		}
	}
}

// GetAllConfigs returns all configs
func (c *AppConfig) GetAllConfigs() []AmneziaWGConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Configs
}

// SetSelectedConfig запоминает конфиг, выбранный на главном экране.
func (c *AppConfig) SetSelectedConfig(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.SelectedConfigID = id
}

// GetSelectedConfigID возвращает выбранный конфиг, если он ещё существует.
func (c *AppConfig) GetSelectedConfigID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	for _, cfg := range c.Configs {
		if cfg.ID == c.SelectedConfigID {
			return cfg.ID
		}
	}
	return ""
}

// GetRouting returns the routing configuration
func (c *AppConfig) GetRouting() RoutingConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Routing
}

// SetRouting sets the routing configuration
func (c *AppConfig) SetRouting(routing RoutingConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Routing = routing
}

// AddRoutingRule adds a routing rule
func (c *AppConfig) AddRoutingRule(rule RoutingRule) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Routing.Rules = append(c.Routing.Rules, rule)
}

// DeleteRoutingRule removes a routing rule by ID
func (c *AppConfig) DeleteRoutingRule(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	for i := range c.Routing.Rules {
		if c.Routing.Rules[i].ID == id {
			c.Routing.Rules = append(c.Routing.Rules[:i], c.Routing.Rules[i+1:]...)
			return true
		}
	}
	return false
}

// SetRoutingMode sets the routing mode
func (c *AppConfig) SetRoutingMode(mode RoutingMode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Routing.Mode = mode
}

// GetSettings returns the current settings
func (c *AppConfig) GetSettings() AppSettings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Settings
}

// SetSettings updates the settings
func (c *AppConfig) SetSettings(settings AppSettings) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Settings = settings
}

// GetAutoconnectConfigID возвращает конфиг для автоподключения: это конфиг,
// выбранный на главном экране. Пустая строка — автоподключение выключено либо
// выбирать пока нечего (конфигов нет), тогда оно просто ничего не делает.
func (c *AppConfig) GetAutoconnectConfigID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	if !c.Settings.Autoconnect {
		return ""
	}
	
	// Verify config still exists
	for _, cfg := range c.Configs {
		if cfg.ID == c.SelectedConfigID {
			return cfg.ID
		}
	}
	
	return ""
}
