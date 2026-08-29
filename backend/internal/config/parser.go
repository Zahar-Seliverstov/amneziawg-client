package config

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// GenerateID generates a random ID for configs and rules
func GenerateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ParseAmneziaConfig parses a raw AmneziaWG configuration file
func ParseAmneziaConfig(name, rawConfig string) (*AmneziaWGConfig, error) {
	cfg := &AmneziaWGConfig{
		ID:        GenerateID(),
		Name:      name,
		RawConfig: rawConfig,
		CreatedAt: time.Now(),
		Peers:     []PeerConfig{},
	}

	scanner := bufio.NewScanner(strings.NewReader(rawConfig))
	var currentSection string
	var currentPeer *PeerConfig

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check for section headers
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.ToLower(strings.Trim(line, "[]"))
			currentSection = section

			if section == "peer" {
				if currentPeer != nil {
					cfg.Peers = append(cfg.Peers, *currentPeer)
				}
				currentPeer = &PeerConfig{}
			}
			continue
		}

		// Parse key-value pairs
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(strings.ToLower(parts[0]))
		value := strings.TrimSpace(parts[1])

		switch currentSection {
		case "interface":
			parseInterfaceField(&cfg.Interface, key, value)
		case "peer":
			if currentPeer != nil {
				parsePeerField(currentPeer, key, value)
			}
		}
	}

	// Don't forget the last peer
	if currentPeer != nil {
		cfg.Peers = append(cfg.Peers, *currentPeer)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning config: %w", err)
	}

	// Validate required fields
	if cfg.Interface.PrivateKey == "" {
		return nil, fmt.Errorf("missing required field: PrivateKey")
	}

	if len(cfg.Peers) == 0 {
		return nil, fmt.Errorf("no peers defined in configuration")
	}

	if cfg.Name == "" {
		cfg.Name = deriveName(cfg)
	}

	return cfg, nil
}

// deriveName придумывает имя, когда пользователь его не задал: берём адрес
// сервера — именно по нему конфигурацию и узнают. Если endpoint'а нет,
// остаётся адрес самого интерфейса.
func deriveName(cfg *AmneziaWGConfig) string {
	for _, peer := range cfg.Peers {
		if host := endpointHost(peer.Endpoint); host != "" {
			return host
		}
	}

	for _, addr := range cfg.Interface.Address {
		if host := strings.TrimSpace(strings.SplitN(addr, "/", 2)[0]); host != "" {
			return host
		}
	}

	return "Конфигурация"
}

// endpointHost вырезает хост из "host:port" и "[ipv6]:port".
func endpointHost(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}

	if host, _, err := net.SplitHostPort(endpoint); err == nil {
		return host
	}

	// Адрес без порта: у IPv6 он мог прийти в скобках.
	return strings.Trim(endpoint, "[]")
}

func parseInterfaceField(iface *InterfaceConfig, key, value string) {
	switch key {
	case "privatekey":
		iface.PrivateKey = value
	case "address":
		iface.Address = parseCSV(value)
	case "dns":
		iface.DNS = parseCSV(value)
	case "mtu":
		if v, err := strconv.Atoi(value); err == nil {
			iface.MTU = v
		}
	// AmneziaWG specific parameters
	case "jc":
		if v, err := strconv.Atoi(value); err == nil {
			iface.Jc = v
		}
	case "jmin":
		if v, err := strconv.Atoi(value); err == nil {
			iface.Jmin = v
		}
	case "jmax":
		if v, err := strconv.Atoi(value); err == nil {
			iface.Jmax = v
		}
	case "s1":
		if v, err := strconv.Atoi(value); err == nil {
			iface.S1 = v
		}
	case "s2":
		if v, err := strconv.Atoi(value); err == nil {
			iface.S2 = v
		}
	case "s3":
		if v, err := strconv.Atoi(value); err == nil {
			iface.S3 = v
		}
	case "s4":
		if v, err := strconv.Atoi(value); err == nil {
			iface.S4 = v
		}
	// AmneziaWG v2
	case "headerprotectionkey":
		iface.HeaderProtectionKey = value
	case "contentpaddingaddition":
		iface.ContentPaddingAddition = value
	case "rekeyaftertime":
		iface.RekeyAfterTime = value
	case "rekeytimeout":
		iface.RekeyTimeout = value
	case "rejectaftertime":
		iface.RejectAfterTime = value
	case "keepalivetimeout":
		iface.KeepaliveTimeout = value
	case "maxhandshakeattempts":
		iface.MaxHandshakeAttempts = value
	case "randomtrailers":
		iface.RandomTrailers = value
	case "disablecookies":
		iface.DisableCookies = value
	case "h1":
		if v, err := strconv.ParseUint(value, 10, 32); err == nil {
			iface.H1 = uint32(v)
		}
	case "h2":
		if v, err := strconv.ParseUint(value, 10, 32); err == nil {
			iface.H2 = uint32(v)
		}
	case "h3":
		if v, err := strconv.ParseUint(value, 10, 32); err == nil {
			iface.H3 = uint32(v)
		}
	case "h4":
		if v, err := strconv.ParseUint(value, 10, 32); err == nil {
			iface.H4 = uint32(v)
		}
	}
}

func parsePeerField(peer *PeerConfig, key, value string) {
	switch key {
	case "publickey":
		peer.PublicKey = value
	case "presharedkey":
		peer.PresharedKey = value
	case "endpoint":
		peer.Endpoint = value
	case "allowedips":
		peer.AllowedIPs = parseCSV(value)
	case "persistentkeepalive":
		// В v2 значение может быть диапазоном ("25-35") — сохраняем как есть,
		// а в числовое поле кладём нижнюю границу, для отображения.
		peer.PersistentKeepaliveRaw = value
		if v, err := strconv.Atoi(value); err == nil {
			peer.PersistentKeepalive = v
		} else if lo, _, found := strings.Cut(value, "-"); found {
			if v, err := strconv.Atoi(strings.TrimSpace(lo)); err == nil {
				peer.PersistentKeepalive = v
			}
		}
	}
}

func parseCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
