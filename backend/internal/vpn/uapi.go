package vpn

// Перевод разобранной конфигурации в текст UAPI — формат, которым с ядром
// amneziawg-go говорит и сам wg(8).

import (
	"fmt"
	"log"
	"strings"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/firewall"
)

// buildUAPIConfig creates a UAPI configuration string
func (m *Manager) buildUAPIConfig(cfg *config.AmneziaWGConfig) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("private_key=%s\n", hexEncodeKey(cfg.Interface.PrivateKey)))

	// Метка на сокете ядра. По ней блокировка отличает наши же зашифрованные
	// пакеты к серверу VPN от всего остального трафика — см. internal/firewall.
	// Ставится всегда: на работу туннеля она не влияет, а привязывать её к
	// настройке значило бы получить блокировку, которая не пропускает сам
	// туннель, если включить её не в том порядке.
	sb.WriteString(fmt.Sprintf("fwmark=%d\n", firewall.Mark))

	// AmneziaWG specific parameters
	if cfg.Interface.Jc > 0 {
		sb.WriteString(fmt.Sprintf("jc=%d\n", cfg.Interface.Jc))
	}
	if cfg.Interface.Jmin > 0 {
		sb.WriteString(fmt.Sprintf("jmin=%d\n", cfg.Interface.Jmin))
	}
	if cfg.Interface.Jmax > 0 {
		sb.WriteString(fmt.Sprintf("jmax=%d\n", cfg.Interface.Jmax))
	}
	if cfg.Interface.S1 > 0 {
		sb.WriteString(fmt.Sprintf("s1=%d\n", cfg.Interface.S1))
	}
	if cfg.Interface.S2 > 0 {
		sb.WriteString(fmt.Sprintf("s2=%d\n", cfg.Interface.S2))
	}
	if cfg.Interface.S3 > 0 {
		sb.WriteString(fmt.Sprintf("s3=%d\n", cfg.Interface.S3))
	}
	if cfg.Interface.S4 > 0 {
		sb.WriteString(fmt.Sprintf("s4=%d\n", cfg.Interface.S4))
	}
	// AmneziaWG v2. Без этих параметров сервер v2 не примет наши пакеты:
	// заголовки шифруются header_protection_key, а размеры — padding'ом.
	if cfg.Interface.HeaderProtectionKey != "" {
		sb.WriteString(fmt.Sprintf("header_protection_key=%s\n", hexEncodeKey(cfg.Interface.HeaderProtectionKey)))
	}
	if cfg.Interface.ContentPaddingAddition != "" {
		sb.WriteString(fmt.Sprintf("content_padding_addition=%s\n", cfg.Interface.ContentPaddingAddition))
	}
	if cfg.Interface.RekeyAfterTime != "" {
		sb.WriteString(fmt.Sprintf("rekey_after_time=%s\n", cfg.Interface.RekeyAfterTime))
	}
	if cfg.Interface.RekeyTimeout != "" {
		sb.WriteString(fmt.Sprintf("rekey_timeout=%s\n", cfg.Interface.RekeyTimeout))
	}
	if cfg.Interface.RejectAfterTime != "" {
		sb.WriteString(fmt.Sprintf("reject_after_time=%s\n", cfg.Interface.RejectAfterTime))
	}
	if cfg.Interface.KeepaliveTimeout != "" {
		sb.WriteString(fmt.Sprintf("keepalive_timeout=%s\n", cfg.Interface.KeepaliveTimeout))
	}
	if cfg.Interface.MaxHandshakeAttempts != "" {
		sb.WriteString(fmt.Sprintf("max_handshake_attempts=%s\n", cfg.Interface.MaxHandshakeAttempts))
	}
	// Переключатели меняют формат пакетов: если сервер ждёт случайные хвосты,
	// а мы их не шлём, он просто выбрасывает наши пакеты и молчит в ответ.
	if v, ok := parseSwitch(cfg.Interface.RandomTrailers); ok {
		sb.WriteString(fmt.Sprintf("random_trailers=%s\n", v))
	}
	if v, ok := parseSwitch(cfg.Interface.DisableCookies); ok {
		sb.WriteString(fmt.Sprintf("disable_cookies=%s\n", v))
	}
	if cfg.Interface.H1 > 0 {
		sb.WriteString(fmt.Sprintf("h1=%d\n", cfg.Interface.H1))
	}
	if cfg.Interface.H2 > 0 {
		sb.WriteString(fmt.Sprintf("h2=%d\n", cfg.Interface.H2))
	}
	if cfg.Interface.H3 > 0 {
		sb.WriteString(fmt.Sprintf("h3=%d\n", cfg.Interface.H3))
	}
	if cfg.Interface.H4 > 0 {
		sb.WriteString(fmt.Sprintf("h4=%d\n", cfg.Interface.H4))
	}

	// Configure peers
	for _, peer := range cfg.Peers {
		sb.WriteString(fmt.Sprintf("public_key=%s\n", hexEncodeKey(peer.PublicKey)))

		if peer.PresharedKey != "" {
			sb.WriteString(fmt.Sprintf("preshared_key=%s\n", hexEncodeKey(peer.PresharedKey)))
		}

		if peer.Endpoint != "" {
			sb.WriteString(fmt.Sprintf("endpoint=%s\n", peer.Endpoint))
		}

		for _, ip := range peer.AllowedIPs {
			sb.WriteString(fmt.Sprintf("allowed_ip=%s\n", ip))
		}

		// В v2 интервал может быть диапазоном, UAPI принимает его строкой.
		if peer.PersistentKeepaliveRaw != "" {
			sb.WriteString(fmt.Sprintf("persistent_keepalive_interval=%s\n", peer.PersistentKeepaliveRaw))
		} else if peer.PersistentKeepalive > 0 {
			sb.WriteString(fmt.Sprintf("persistent_keepalive_interval=%d\n", peer.PersistentKeepalive))
		}
	}

	return sb.String()
}

// parseSwitch переводит значение вида on/off из конфига в "1"/"0" для UAPI.
// Второе значение — было ли задано вообще.
func parseSwitch(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "yes", "1":
		return "1", true
	case "off", "false", "no", "0":
		return "0", true
	default:
		return "", false
	}
}

// hexEncodeKey converts a base64 WireGuard key to hex
func hexEncodeKey(base64Key string) string {
	hexKey, err := KeyToHex(base64Key)
	if err != nil {
		log.Printf("Warning: failed to convert key to hex: %v, using original", err)
		return base64Key
	}
	return hexKey
}
