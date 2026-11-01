package profiles

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/protocol/amneziawg"
	"github.com/user/amnezia-web-client/internal/tunnel"
)

const sample = `[Interface]
PrivateKey = qO8QDrIKR3vufYDHIRcbYSuVFPGqOcJ2P08S6r67dFA=
Address = 10.8.0.2/32

[Peer]
PublicKey = dGVzdHB1YmxpY2tleXRlc3RwdWJsaWNrZXkxMjM0NTY=
Endpoint = vpn.example.com:51820
AllowedIPs = 0.0.0.0/0
`

func newCatalog(t *testing.T) (*Catalog, *config.AppConfig) {
	t.Helper()
	store := config.NewAppConfig(filepath.Join(t.TempDir(), "config.json"))
	reg := tunnel.NewRegistry([]tunnel.Protocol{amneziawg.Protocol{}}, nil)
	return New(store, reg), store
}

func TestPrepareDetectsProtocolAndName(t *testing.T) {
	c, store := newCatalog(t)

	p, err := c.Prepare("", sample, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if p.Protocol != amneziawg.ID || p.Name != "vpn.example.com" || p.ID == "" || p.CreatedAt.IsZero() {
		t.Errorf("запись: %+v", p)
	}

	// Повтор имени дополняется номером, а своё имя при правке не занято.
	store.AddConfig(p)
	if again, _ := c.Prepare("", sample, ""); again.Name != "vpn.example.com 2" {
		t.Errorf("повтор имени: %q", again.Name)
	}
	if self, _ := c.Prepare("", sample, p.ID); self.Name != "vpn.example.com" {
		t.Errorf("своё имя сочтено занятым: %q", self.Name)
	}
	if named, _ := c.Prepare("Дом", sample, ""); named.Name != "Дом" {
		t.Errorf("заданное имя подменено: %q", named.Name)
	}
}

func TestPrepareRejectsGarbage(t *testing.T) {
	c, _ := newCatalog(t)
	if _, err := c.Prepare("", "не конфигурация", ""); !errors.Is(err, tunnel.ErrUnknownFormat) {
		t.Errorf("мусор: %v", err)
	}
}

func TestTarget(t *testing.T) {
	c, store := newCatalog(t)

	if _, err := c.Target("нет"); !errors.Is(err, ErrNotFound) {
		t.Errorf("несуществующая: %v", err)
	}

	// Запись старой версии — без протокола: он узнаётся по тексту.
	store.AddConfig(config.Profile{ID: "old", Name: "старая", RawConfig: sample})
	target, err := c.Target("old")
	if err != nil {
		t.Fatalf("Target: %v", err)
	}
	if target.ID != "old" || target.Name != "старая" || target.Config.Protocol() != amneziawg.ID {
		t.Errorf("цель: %+v", target)
	}
	if eps := target.Config.Spec().Endpoints; len(eps) != 1 || eps[0] != "vpn.example.com:51820" {
		t.Errorf("Endpoints: %v", eps)
	}
}
