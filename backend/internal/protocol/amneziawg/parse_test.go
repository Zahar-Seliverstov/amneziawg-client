package amneziawg

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

const goodIface = "[Interface]\nPrivateKey = " + privKey + "\nAddress = 10.8.0.2/32\n"
const goodPeer = "\n[Peer]\nPublicKey = " + pubKey + "\nAllowedIPs = 0.0.0.0/0\n"

// withIface — рабочая конфигурация с добавкой в [Interface].
func withIface(extra string) string { return goodIface + extra + goodPeer }

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
H2 = 100-200
H3 = 1234567892
H4 = 1234567893
I1 = <b 0xc10000000108><r 16>
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
	cfg, err := Parse(full)
	if err != nil {
		t.Fatalf("не разобрана: %v", err)
	}

	iface := cfg.Interface
	if iface.PrivateKey != privKey || len(iface.Addresses) != 2 || len(iface.DNS) != 2 || iface.MTU != 1380 {
		t.Errorf("основные поля: %+v", iface)
	}

	want := map[string]Param{
		"jc":                       {"jc", "4", KindNumber},
		"s4":                       {"s4", "25", KindNumber},
		"h1":                       {"h1", "1234567890", KindRange},
		"h2":                       {"h2", "100-200", KindRange},
		"i1":                       {"i1", "<b 0xc10000000108><r 16>", KindChain},
		"header_protection_key":    {"header_protection_key", pskKey, KindKey},
		"content_padding_addition": {"content_padding_addition", "10-100", KindRange},
		"random_trailers":          {"random_trailers", "true", KindSwitch},
		"disable_cookies":          {"disable_cookies", "false", KindSwitch},
	}
	got := map[string]Param{}
	for _, p := range iface.Obfuscation {
		got[p.Key] = p
	}
	if len(iface.Obfuscation) != 17 {
		t.Errorf("параметров маскировки %d, ожидалось 17", len(iface.Obfuscation))
	}
	for key, w := range want {
		if got[key] != w {
			t.Errorf("%s: %+v, ожидалось %+v", key, got[key], w)
		}
	}

	if len(cfg.Peers) != 1 {
		t.Fatalf("пиров %d", len(cfg.Peers))
	}
	peer := cfg.Peers[0]
	if peer.PresharedKey != pskKey || peer.Endpoint != "vpn.example.com:51820" ||
		len(peer.AllowedIPs) != 2 || peer.Keepalive != "25-35" {
		t.Errorf("пир: %+v", peer)
	}
}

// Порядок параметров сохраняется: ядро получает их так, как написано в файле.
func TestParseKeepsParamOrder(t *testing.T) {
	cfg, err := Parse(withIface("H4 = 4\nJc = 1\nH1 = 1\n"))
	if err != nil {
		t.Fatalf("не разобрана: %v", err)
	}
	var keys []string
	for _, p := range cfg.Interface.Obfuscation {
		keys = append(keys, p.Key)
	}
	if strings.Join(keys, ",") != "h4,jc,h1" {
		t.Errorf("порядок %v", keys)
	}
}

func TestParseMultiplePeersAndRepeatedLists(t *testing.T) {
	raw := goodIface + "Address = fd00::2/128\n" + goodPeer +
		"Endpoint = a.example.com:1\nAllowedIPs = 10.0.0.0/8\n" +
		"\n[Peer]\nPublicKey = " + pubKey + "\nAllowedIPs = 192.168.0.0/16\n"
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatalf("не разобрана: %v", err)
	}
	if len(cfg.Interface.Addresses) != 2 {
		t.Errorf("повторная строка Address потерялась: %v", cfg.Interface.Addresses)
	}
	if len(cfg.Peers) != 2 || len(cfg.Peers[0].AllowedIPs) != 2 {
		t.Errorf("пиры: %+v", cfg.Peers)
	}
}

// Комментарии, пустые строки и поля wg-quick, к подключению не относящиеся,
// разбору не мешают.
func TestParseSkipsNoise(t *testing.T) {
	raw := "# шапка\n; тоже комментарий\n\n" + withIface("ListenPort = 51820\nPostUp = iptables -A …\nTable = off\n")
	if _, err := Parse(raw); err != nil {
		t.Fatalf("не разобрана: %v", err)
	}
}

// Значения без диапазонов и IPv6-эндпоинт тоже проходят: конфигурации
// приходят от разных серверов.
func TestParseAcceptsPlainVariants(t *testing.T) {
	raw := goodIface + "\n[Peer]\nPublicKey = " + pubKey +
		"\nEndpoint = [2001:db8::1]:51820\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = 25\n"
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatalf("не разобрана: %v", err)
	}
	if cfg.Peers[0].Keepalive != "25" {
		t.Errorf("keepalive: %q", cfg.Peers[0].Keepalive)
	}
}

func TestParseRejectsBrokenConfigs(t *testing.T) {
	peer := func(extra string) string { return goodIface + goodPeer + extra }

	cases := []struct{ name, raw, mentions string }{
		{"пустой текст", "", "PrivateKey"},
		{"нет PrivateKey", "[Interface]\nAddress = 10.8.0.2/32\n" + goodPeer, "PrivateKey"},
		{"PrivateKey не base64", "[Interface]\nPrivateKey = не-ключ!!\nAddress = 10.8.0.2/32\n" + goodPeer, "PrivateKey"},
		{"PrivateKey не той длины", "[Interface]\nPrivateKey = c2hvcnQ=\nAddress = 10.8.0.2/32\n" + goodPeer, "PrivateKey"},
		{"нет Address", "[Interface]\nPrivateKey = " + privKey + "\n" + goodPeer, "Address"},
		{"Address без маски", "[Interface]\nPrivateKey = " + privKey + "\nAddress = 10.8.0.2\n" + goodPeer, "Address"},
		{"DNS не адрес", withIface("DNS = не-адрес\n"), "DNS"},
		{"MTU не число", withIface("MTU = много\n"), "MTU"},
		{"MTU с лишним нулём", withIface("MTU = 99999\n"), "MTU"},
		{"MTU слишком мал", withIface("MTU = 68\n"), "MTU"},
		{"Jc не число", withIface("Jc = abc\n"), "Jc"},
		{"S1 больше 16 бит", withIface("S1 = 70000\n"), "S1"},
		{"H1 перевёрнутый диапазон", withIface("H1 = 200-100\n"), "H1"},
		{"I1 без цепочки", withIface("I1 = мусор\n"), "I1"},
		{"переключатель мусор", withIface("RandomTrailers = может\n"), "RandomTrailers"},
		{"HeaderProtectionKey битый", withIface("HeaderProtectionKey = c2hvcnQ=\n"), "HeaderProtectionKey"},
		{"нет пиров", goodIface, "Peer"},
		{"нет PublicKey", goodIface + "\n[Peer]\nAllowedIPs = 0.0.0.0/0\n", "PublicKey"},
		{"PublicKey не той длины", goodIface + "\n[Peer]\nPublicKey = c2hvcnQ=\nAllowedIPs = 0.0.0.0/0\n", "PublicKey"},
		{"PresharedKey битый", peer("PresharedKey = c2hvcnQ=\n"), "PresharedKey"},
		{"нет AllowedIPs", goodIface + "\n[Peer]\nPublicKey = " + pubKey + "\n", "AllowedIPs"},
		{"AllowedIPs без маски", goodIface + "\n[Peer]\nPublicKey = " + pubKey + "\nAllowedIPs = 1.1.1.1\n", "AllowedIPs"},
		{"Endpoint без порта", peer("Endpoint = vpn.example.com\n"), "Endpoint"},
		{"Endpoint с чужим портом", peer("Endpoint = vpn.example.com:порт\n"), "Endpoint"},
		{"Endpoint с нулевым портом", peer("Endpoint = vpn.example.com:0\n"), "Endpoint"},
		{"PersistentKeepalive мусор", peer("PersistentKeepalive = abc\n"), "PersistentKeepalive"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.raw)
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

// Ошибка значения называет строку: в длинном файле иначе не найти, где она.
func TestParseErrorNamesLine(t *testing.T) {
	_, err := Parse(withIface("Jc = abc\n"))
	if err == nil || !strings.Contains(err.Error(), "строка 4") {
		t.Errorf("ошибка без номера строки: %v", err)
	}
}

func TestSpec(t *testing.T) {
	raw := goodIface + "DNS = 1.1.1.1\n" + goodPeer + "Endpoint = a.example.com:1\nPersistentKeepalive = 0\n" +
		"\n[Peer]\nPublicKey = " + pubKey + "\nAllowedIPs = 10.0.0.0/8\n"
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatalf("не разобрана: %v", err)
	}

	spec := cfg.Spec()
	if len(spec.Endpoints) != 1 || spec.Endpoints[0] != "a.example.com:1" {
		t.Errorf("Endpoints: %v — пустые адреса попадать не должны", spec.Endpoints)
	}
	if strings.Join(spec.AllowedIPs, ",") != "0.0.0.0/0,10.0.0.0/8" {
		t.Errorf("AllowedIPs: %v", spec.AllowedIPs)
	}
	if spec.SelfStarting {
		t.Error("keepalive 0 признан включённым")
	}

	spec.Addresses[0], spec.DNS[0] = "чужое", "чужое"
	if cfg.Interface.Addresses[0] != "10.8.0.2/32" || cfg.Interface.DNS[0] != "1.1.1.1" {
		t.Error("Spec делит срезы с конфигурацией")
	}
}

func TestSpecSelfStarting(t *testing.T) {
	cases := map[string]bool{"": false, "0": false, "0-0": false, "25": true, "25-35": true}
	for keepalive, want := range cases {
		cfg := &Config{Peers: []Peer{{Keepalive: keepalive}}}
		if got := cfg.Spec().SelfStarting; got != want {
			t.Errorf("keepalive %q: %v, ожидалось %v", keepalive, got, want)
		}
	}
}

func TestProtocol(t *testing.T) {
	p := Protocol{}
	if !p.Sniff("[INTERFACE]\n") || p.Sniff("hello") {
		t.Error("Sniff ошибается")
	}
	// Типизированный nil в интерфейсе не равен nil — вызывающий проверяет
	// именно cfg == nil.
	if cfg, err := p.Parse("мусор"); err == nil || cfg != nil {
		t.Errorf("на мусоре: cfg=%v err=%v", cfg, err)
	}
	if cfg, err := p.Parse(full); err != nil || cfg.Protocol() != ID {
		t.Errorf("на рабочем: cfg=%v err=%v", cfg, err)
	}
}
