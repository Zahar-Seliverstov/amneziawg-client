package config

import (
	"strings"
	"testing"
)

// Настоящие ключи по 32 байта: проверка длины иначе отвергла бы образцы.
const (
	privKey = "qO8QDrIKR3vufYDHIRcbYSuVFPGqOcJ2P08S6r67dFA="
	pubKey  = "dGVzdHB1YmxpY2tleXRlc3RwdWJsaWNrZXkxMjM0NTY="
	pskKey  = "cHJlc2hhcmVka2V5cHJlc2hhcmVka2V5MTIzNDU2Nzg="
)

// full — конфигурация со всеми параметрами, включая AmneziaWG v2.
const full = `[Interface]
PrivateKey = ` + privKey + `
Address = 10.8.0.2/32, fd00::2/128
DNS = 10.8.0.1, 1.1.1.1
MTU = 1380
Jc = 4
Jmin = 40
Jmax = 70
S1 = 30
S2 = 60
S3 = 15
S4 = 25
H1 = 1234567890
H2 = 1234567891
H3 = 1234567892
H4 = 1234567893
HeaderProtectionKey = ` + pskKey + `
ContentPaddingAddition = 10-100
RekeyAfterTime = 100-140
RandomTrailers = on
DisableCookies = off

[Peer]
PublicKey = ` + pubKey + `
PresharedKey = ` + pskKey + `
Endpoint = vpn.example.com:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25-35
`

func TestParseFullConfig(t *testing.T) {
	cfg, err := ParseAmneziaConfig("", full)
	if err != nil {
		t.Fatalf("не разобрана: %v", err)
	}

	iface := cfg.Interface
	checks := []struct {
		what string
		got  any
		want any
	}{
		{"PrivateKey", iface.PrivateKey, privKey},
		{"адресов", len(iface.Address), 2},
		{"серверов имён", len(iface.DNS), 2},
		{"MTU", iface.MTU, 1380},
		{"Jc", iface.Jc, 4},
		{"S4", iface.S4, 25},
		{"H1", iface.H1, uint32(1234567890)},
		{"ContentPaddingAddition", iface.ContentPaddingAddition, "10-100"},
		{"RandomTrailers", iface.RandomTrailers, "on"},
		{"DisableCookies", iface.DisableCookies, "off"},
		{"пиров", len(cfg.Peers), 1},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: %v, ожидалось %v", c.what, c.got, c.want)
		}
	}

	peer := cfg.Peers[0]
	if peer.PublicKey != pubKey || peer.PresharedKey != pskKey {
		t.Errorf("ключи пира разобраны неверно: %+v", peer)
	}
	if len(peer.AllowedIPs) != 2 {
		t.Errorf("AllowedIPs: %v", peer.AllowedIPs)
	}
	// Диапазон сохраняется как есть, а в числовое поле идёт нижняя граница.
	if peer.PersistentKeepaliveRaw != "25-35" || peer.PersistentKeepalive != 25 {
		t.Errorf("PersistentKeepalive: %q / %d", peer.PersistentKeepaliveRaw, peer.PersistentKeepalive)
	}

	// Имя выводится из адреса сервера: по нему конфигурацию и узнают.
	if cfg.Name != "vpn.example.com" {
		t.Errorf("имя выведено как %q", cfg.Name)
	}
}

func TestParseMultiplePeers(t *testing.T) {
	raw := `[Interface]
PrivateKey = ` + privKey + `
Address = 10.8.0.2/32

[Peer]
PublicKey = ` + pubKey + `
AllowedIPs = 10.0.0.0/8

[Peer]
PublicKey = ` + pskKey + `
AllowedIPs = 192.168.0.0/16
`
	cfg, err := ParseAmneziaConfig("", raw)
	if err != nil {
		t.Fatalf("не разобрана: %v", err)
	}
	if len(cfg.Peers) != 2 {
		t.Fatalf("пиров %d, ожидалось 2", len(cfg.Peers))
	}
	if cfg.Peers[1].AllowedIPs[0] != "192.168.0.0/16" {
		t.Errorf("второй пир разобран неверно: %+v", cfg.Peers[1])
	}
}

func TestParseIgnoresCommentsAndBlankLines(t *testing.T) {
	raw := `# комментарий

[Interface]
PrivateKey = ` + privKey + `
Address = 10.8.0.2/32
# ещё комментарий

[Peer]
PublicKey = ` + pubKey + `
AllowedIPs = 0.0.0.0/0
`
	if _, err := ParseAmneziaConfig("", raw); err != nil {
		t.Fatalf("комментарии сбили разбор: %v", err)
	}
}

// Всё, что не отсеяно здесь, всплывает уже после нажатия «Подключить» —
// текстом от ядра туннеля в системном журнале, которого пользователь не видит.
func TestParseRejectsBrokenConfigs(t *testing.T) {
	base := func(iface, peer string) string {
		return "[Interface]\n" + iface + "\n[Peer]\n" + peer
	}
	goodIface := "PrivateKey = " + privKey + "\nAddress = 10.8.0.2/32\n"
	goodPeer := "PublicKey = " + pubKey + "\nAllowedIPs = 0.0.0.0/0\n"

	cases := []struct{ name, raw, mentions string }{
		{"пустой текст", "", "PrivateKey"},
		{"нет PrivateKey", base("Address = 10.8.0.2/32\n", goodPeer), "PrivateKey"},
		{"PrivateKey не base64", base("PrivateKey = не-ключ!!\nAddress = 10.8.0.2/32\n", goodPeer), "PrivateKey"},
		{"PrivateKey не той длины", base("PrivateKey = c2hvcnQ=\nAddress = 10.8.0.2/32\n", goodPeer), "PrivateKey"},
		{"нет Address", base("PrivateKey = "+privKey+"\n", goodPeer), "Address"},
		{"Address без маски", base("PrivateKey = "+privKey+"\nAddress = 10.8.0.2\n", goodPeer), "Address"},
		{"DNS не адрес", base(goodIface+"DNS = не-адрес\n", goodPeer), "DNS"},
		{"MTU с лишним нулём", base(goodIface+"MTU = 99999\n", goodPeer), "MTU"},
		{"MTU слишком мал", base(goodIface+"MTU = 68\n", goodPeer), "MTU"},
		{"нет пиров", "[Interface]\n" + goodIface, "Peer"},
		{"нет PublicKey", base(goodIface, "AllowedIPs = 0.0.0.0/0\n"), "PublicKey"},
		{"PublicKey не той длины", base(goodIface, "PublicKey = c2hvcnQ=\nAllowedIPs = 0.0.0.0/0\n"), "PublicKey"},
		{"PresharedKey битый", base(goodIface, goodPeer+"PresharedKey = c2hvcnQ=\n"), "PresharedKey"},
		{"нет AllowedIPs", base(goodIface, "PublicKey = "+pubKey+"\n"), "AllowedIPs"},
		{"AllowedIPs без маски", base(goodIface, "PublicKey = "+pubKey+"\nAllowedIPs = 1.1.1.1\n"), "AllowedIPs"},
		{"Endpoint без порта", base(goodIface, goodPeer+"Endpoint = vpn.example.com\n"), "Endpoint"},
		{"Endpoint с чужим портом", base(goodIface, goodPeer+"Endpoint = vpn.example.com:порт\n"), "Endpoint"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseAmneziaConfig("", tc.raw)
			if err == nil {
				t.Fatal("непригодная конфигурация принята")
			}
			// Сообщение обязано называть поле: «неверная конфигурация» не
			// подсказывает, что именно чинить.
			if !strings.Contains(err.Error(), tc.mentions) {
				t.Errorf("в ошибке нет %q: %v", tc.mentions, err)
			}
		})
	}
}

// Значения без диапазонов, IPv6-эндпоинт и одиночный keepalive тоже должны
// проходить: конфигурации приходят от разных серверов.
func TestParseAcceptsPlainVariants(t *testing.T) {
	raw := `[Interface]
PrivateKey = ` + privKey + `
Address = 10.8.0.2/32

[Peer]
PublicKey = ` + pubKey + `
Endpoint = [2001:db8::1]:51820
AllowedIPs = 0.0.0.0/0
PersistentKeepalive = 25
`
	cfg, err := ParseAmneziaConfig("", raw)
	if err != nil {
		t.Fatalf("не разобрана: %v", err)
	}
	if cfg.Peers[0].PersistentKeepalive != 25 {
		t.Errorf("keepalive: %d", cfg.Peers[0].PersistentKeepalive)
	}
	if cfg.Name != "2001:db8::1" {
		t.Errorf("имя выведено как %q", cfg.Name)
	}
}

// Имя без эндпоинта берётся из адреса интерфейса, а если и его нет — общее.
func TestDeriveNameFallsBack(t *testing.T) {
	raw := `[Interface]
PrivateKey = ` + privKey + `
Address = 10.8.0.2/32

[Peer]
PublicKey = ` + pubKey + `
AllowedIPs = 0.0.0.0/0
`
	cfg, err := ParseAmneziaConfig("", raw)
	if err != nil {
		t.Fatalf("не разобрана: %v", err)
	}
	if cfg.Name != "10.8.0.2" {
		t.Errorf("имя выведено как %q", cfg.Name)
	}
}

// Заданное имя не подменяется выведенным.
func TestParseKeepsGivenName(t *testing.T) {
	cfg, err := ParseAmneziaConfig("Моя настройка", full)
	if err != nil {
		t.Fatalf("не разобрана: %v", err)
	}
	if cfg.Name != "Моя настройка" {
		t.Errorf("имя подменено на %q", cfg.Name)
	}
}

func TestGenerateIDIsUnique(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := GenerateID()
		if id == "" {
			t.Fatal("пустой идентификатор")
		}
		if seen[id] {
			t.Fatalf("идентификатор %q повторился — правила и конфигурации начнут затирать друг друга", id)
		}
		seen[id] = true
	}
}
