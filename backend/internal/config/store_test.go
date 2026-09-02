package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/user/amnezia-web-client/internal/desktopuser"
)

const sampleConfig = `[Interface]
PrivateKey = qO8QDrIKR3vufYDHIRcbYSuVFPGqOcJ2P08S6r67dFA=
Address = 10.8.0.2/32
DNS = 10.8.0.1

[Peer]
PublicKey = dGVzdHB1YmxpY2tleXRlc3RwdWJsaWNrZXkxMjM0NTY=
Endpoint = vpn.example.com:51820
AllowedIPs = 0.0.0.0/0
`

func newStore(t *testing.T) *AppConfig {
	t.Helper()
	return NewAppConfig(filepath.Join(t.TempDir(), "config.json"), desktopuser.User{})
}

func addSample(t *testing.T, store *AppConfig, name string) AmneziaWGConfig {
	t.Helper()

	cfg, err := ParseAmneziaConfig(name, sampleConfig)
	if err != nil {
		t.Fatalf("не разобрать образец конфигурации: %v", err)
	}
	store.AddConfig(*cfg)

	return *cfg
}

// Сохранение обязано быть неделимым: обрыв на середине не должен оставлять
// файл, из которого не восстановить ни одной конфигурации.
func TestSaveIsAtomicAndPrivate(t *testing.T) {
	store := newStore(t)
	addSample(t, store, "первая")

	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatalf("файл не создан: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("права файла %o, ожидались 0600: внутри приватные ключи", perm)
	}

	// После удачной записи в каталоге не должно остаться временных файлов —
	// в них тот же приватный ключ.
	entries, err := os.ReadDir(filepath.Dir(store.Path()))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(store.Path()) {
			t.Errorf("в каталоге остался посторонний файл %q", entry.Name())
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	store := newStore(t)
	added := addSample(t, store, "рабочая")

	store.SetSelectedConfig(added.ID)
	store.SetSettings(AppSettings{Autoconnect: true})
	store.SetRouting(RoutingConfig{
		Mode:  RoutingModeDirectList,
		Rules: []RoutingRule{{ID: "r1", Type: RuleTypeDomain, Value: "example.com", Enabled: true}},
	})

	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded := NewAppConfig(store.Path(), desktopuser.User{})
	if err := loaded.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := loaded.GetSelectedConfigID(); got != added.ID {
		t.Errorf("выбранный конфиг %q, ожидался %q", got, added.ID)
	}
	if !loaded.GetSettings().Autoconnect {
		t.Error("настройка автоподключения не пережила перезапуск")
	}

	routing := loaded.GetRouting()
	if routing.Mode != RoutingModeDirectList || len(routing.Rules) != 1 {
		t.Fatalf("правила не пережили перезапуск: %+v", routing)
	}

	configs := loaded.GetAllConfigs()
	if len(configs) != 1 || configs[0].Interface.PrivateKey != added.Interface.PrivateKey {
		t.Fatalf("конфигурация не пережила перезапуск: %+v", configs)
	}
}

// Битый файл не должен делать приложение незапускаемым, но и терять ключи
// нельзя: он откладывается в сторону.
func TestLoadQuarantinesCorruptFile(t *testing.T) {
	store := newStore(t)
	if err := os.WriteFile(store.Path(), []byte("{это не JSON"), 0600); err != nil {
		t.Fatalf("подготовка: %v", err)
	}

	if err := store.Load(); err != nil {
		t.Fatalf("битый файл не должен мешать запуску, получили: %v", err)
	}
	if got := len(store.GetAllConfigs()); got != 0 {
		t.Errorf("после битого файла в списке %d конфигураций, ожидалось 0", got)
	}

	entries, err := os.ReadDir(filepath.Dir(store.Path()))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	var saved bool
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".corrupt-") {
			saved = true
		}
		if entry.Name() == filepath.Base(store.Path()) {
			t.Error("битый файл остался на месте — следующее сохранение затрёт его")
		}
	}
	if !saved {
		t.Errorf("битый файл не сохранён, в каталоге: %v", entries)
	}
}

// Правило с опечаткой, дописанное в файл руками, не должно ронять запуск —
// но и в маршрутизацию попасть не должно.
func TestLoadDropsUnusableRules(t *testing.T) {
	store := newStore(t)

	raw := `{
	  "routing": {
	    "mode": "какой-то",
	    "rules": [
	      {"id": "ok", "type": "ip", "value": "1.1.1.1", "enabled": true},
	      {"id": "bad", "type": "ip", "value": "не адрес", "enabled": true},
	      {"id": "ok", "type": "cidr", "value": "10.0.0.0/8", "enabled": true}
	    ]
	  }
	}`
	if err := os.WriteFile(store.Path(), []byte(raw), 0600); err != nil {
		t.Fatalf("подготовка: %v", err)
	}

	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	routing := store.GetRouting()
	if routing.Mode != RoutingModeVPNList {
		t.Errorf("неизвестный режим не заменён на умолчание: %q", routing.Mode)
	}
	if len(routing.Rules) != 2 {
		t.Fatalf("осталось %d правил, ожидалось 2: %+v", len(routing.Rules), routing.Rules)
	}
	if routing.Rules[0].ID == routing.Rules[1].ID {
		t.Error("повторяющийся идентификатор не исправлен: удаление одного правила убрало бы соседнее")
	}
}

// Конфиг, добавленный старой версией, не знавшей полей v2, должен подтянуть
// их из сохранённого текста при следующем запуске.
func TestLoadReparsesStoredConfigs(t *testing.T) {
	store := newStore(t)

	raw := `{"configs": [{"id": "x1", "name": "старая", "raw_config": ` +
		mustJSON(t, sampleConfig) + `, "interface": {"private_key": ""}, "peers": []}]}`
	if err := os.WriteFile(store.Path(), []byte(raw), 0600); err != nil {
		t.Fatalf("подготовка: %v", err)
	}

	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg := store.GetConfig("x1")
	if cfg == nil {
		t.Fatal("конфигурация потерялась")
	}
	if cfg.Interface.PrivateKey == "" || len(cfg.Peers) != 1 {
		t.Errorf("конфигурация не перечитана из raw_config: %+v", cfg)
	}
	if cfg.Name != "старая" {
		t.Errorf("имя не сохранено: %q", cfg.Name)
	}
}

// Хранилище обязано отдавать копии: иначе менеджер VPN и интерфейс работали бы
// с одним массивом мимо мьютекса.
func TestGettersReturnIndependentCopies(t *testing.T) {
	store := newStore(t)
	added := addSample(t, store, "исходная")

	got := store.GetConfig(added.ID)
	got.Interface.Address[0] = "192.0.2.1/32"
	got.Peers[0].AllowedIPs[0] = "192.0.2.0/24"

	again := store.GetConfig(added.ID)
	if again.Interface.Address[0] == "192.0.2.1/32" {
		t.Error("правка копии изменила адрес в хранилище")
	}
	if again.Peers[0].AllowedIPs[0] == "192.0.2.0/24" {
		t.Error("правка копии изменила AllowedIPs в хранилище")
	}

	list := store.GetAllConfigs()
	list[0].Interface.DNS[0] = "8.8.8.8"
	if store.GetAllConfigs()[0].Interface.DNS[0] == "8.8.8.8" {
		t.Error("GetAllConfigs отдал срез, связанный с хранилищем")
	}
}

// То же в обратную сторону: положенное в хранилище не должно оставаться
// связанным с тем, что держит вызывающий.
func TestSettersCopyInput(t *testing.T) {
	store := newStore(t)

	routing := RoutingConfig{
		Mode:  RoutingModeVPNList,
		Rules: []RoutingRule{{ID: "r1", Type: RuleTypeIP, Value: "1.1.1.1", Enabled: true}},
	}
	store.SetRouting(routing)
	routing.Rules[0].Value = "9.9.9.9"

	if store.GetRouting().Rules[0].Value != "1.1.1.1" {
		t.Error("SetRouting сохранил ссылку на срез вызывающего")
	}
}

func TestDeleteConfigClearsSelection(t *testing.T) {
	store := newStore(t)
	added := addSample(t, store, "единственная")
	store.SetSelectedConfig(added.ID)

	if !store.DeleteConfig(added.ID) {
		t.Fatal("DeleteConfig вернул false для существующей конфигурации")
	}
	if got := store.GetSelectedConfigID(); got != "" {
		t.Errorf("выбранной осталась удалённая конфигурация %q", got)
	}
	if store.DeleteConfig(added.ID) {
		t.Error("повторное удаление вернуло true")
	}
}

func TestUniqueConfigName(t *testing.T) {
	store := newStore(t)
	first := addSample(t, store, "vpn")

	if got := store.UniqueConfigName("vpn", ""); got != "vpn 2" {
		t.Errorf("занятое имя не разведено: %q", got)
	}
	// Своё же имя занятым считаться не должно, иначе правка переименовывала бы.
	if got := store.UniqueConfigName("vpn", first.ID); got != "vpn" {
		t.Errorf("правка переименовала конфигурацию: %q", got)
	}
	if got := store.UniqueConfigName("VPN", ""); got != "VPN 2" {
		t.Errorf("регистр имени должен учитываться при сравнении: %q", got)
	}
}

func TestAutoconnectRequiresExistingSelection(t *testing.T) {
	store := newStore(t)
	added := addSample(t, store, "рабочая")

	store.SetSelectedConfig(added.ID)
	if got := store.GetAutoconnectConfigID(); got != "" {
		t.Errorf("автоподключение выключено, а конфиг выдан: %q", got)
	}

	store.SetSettings(AppSettings{Autoconnect: true})
	if got := store.GetAutoconnectConfigID(); got != added.ID {
		t.Errorf("автоподключение включено, ожидался %q, получен %q", added.ID, got)
	}

	store.DeleteConfig(added.ID)
	if got := store.GetAutoconnectConfigID(); got != "" {
		t.Errorf("выдан удалённый конфиг: %q", got)
	}
}

// Хранилище читают и пишут из разных горутин: HTTP-обработчики, менеджер VPN
// и автоподключение. Тест осмыслен под -race.
func TestConcurrentAccess(t *testing.T) {
	store := newStore(t)
	addSample(t, store, "общая")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				store.GetAllConfigs()
				store.GetRouting()
				store.GetSelectedConfigID()
				store.AddRoutingRule(RoutingRule{ID: GenerateID(), Type: RuleTypeIP, Value: "1.1.1.1", Enabled: true})
				store.SetSettings(AppSettings{Autoconnect: i%2 == 0})
			}
		}(i)
	}
	wg.Wait()
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(encoded)
}
