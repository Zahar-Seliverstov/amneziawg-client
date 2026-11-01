package awggo

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/user/amnezia-web-client/internal/protocol/amneziawg"
)

const (
	privKey = "qO8QDrIKR3vufYDHIRcbYSuVFPGqOcJ2P08S6r67dFA="
	pubKey  = "dGVzdHB1YmxpY2tleXRlc3RwdWJsaWNrZXkxMjM0NTY="
	pskKey  = "cHJlc2hhcmVka2V5cHJlc2hhcmVka2V5MTIzNDU2Nzg="
)

func hexKey(b64 string) string {
	raw, _ := base64.StdEncoding.DecodeString(b64)
	return hex.EncodeToString(raw)
}

func TestBuildUAPI(t *testing.T) {
	cfg := &amneziawg.Config{
		Interface: amneziawg.Interface{
			PrivateKey: privKey,
			Obfuscation: []amneziawg.Param{
				{Key: "jc", Value: "4", Kind: amneziawg.KindNumber},
				{Key: "h1", Value: "100-200", Kind: amneziawg.KindRange},
				{Key: "header_protection_key", Value: pskKey, Kind: amneziawg.KindKey},
				{Key: "random_trailers", Value: "true", Kind: amneziawg.KindSwitch},
			},
		},
		Peers: []amneziawg.Peer{
			{PublicKey: pubKey, PresharedKey: pskKey, Endpoint: "vpn.example.com:51820",
				AllowedIPs: []string{"0.0.0.0/0", "::/0"}, Keepalive: "25-35"},
			{PublicKey: pubKey, AllowedIPs: []string{"10.0.0.0/8"}},
		},
	}

	got, err := buildUAPI(cfg, 51820)
	if err != nil {
		t.Fatalf("buildUAPI: %v", err)
	}

	want := strings.Join([]string{
		"private_key=" + hexKey(privKey),
		"fwmark=51820",
		"jc=4",
		"h1=100-200",
		"header_protection_key=" + hexKey(pskKey),
		"random_trailers=true",
		"public_key=" + hexKey(pubKey),
		"preshared_key=" + hexKey(pskKey),
		"endpoint=vpn.example.com:51820",
		"persistent_keepalive_interval=25-35",
		"allowed_ip=0.0.0.0/0",
		"allowed_ip=::/0",
		"public_key=" + hexKey(pubKey),
		"allowed_ip=10.0.0.0/8",
	}, "\n") + "\n"

	if got != want {
		t.Errorf("UAPI:\n%s\nожидалось:\n%s", got, want)
	}
}

// Без метки строки fwmark нет вовсе: ноль ядро поняло бы как «снять метку».
func TestBuildUAPIWithoutMark(t *testing.T) {
	cfg := &amneziawg.Config{Interface: amneziawg.Interface{PrivateKey: privKey}}
	got, err := buildUAPI(cfg, 0)
	if err != nil || strings.Contains(got, "fwmark") {
		t.Errorf("UAPI %q, ошибка %v", got, err)
	}
}

func TestBuildUAPIRejectsBadKey(t *testing.T) {
	cfg := &amneziawg.Config{
		Interface: amneziawg.Interface{PrivateKey: privKey},
		Peers:     []amneziawg.Peer{{PublicKey: "c2hvcnQ="}},
	}
	if _, err := buildUAPI(cfg, 0); err == nil || !strings.Contains(err.Error(), "public_key") {
		t.Errorf("битый ключ: %v", err)
	}
}
