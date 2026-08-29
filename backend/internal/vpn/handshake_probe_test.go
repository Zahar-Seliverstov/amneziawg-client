package vpn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/conn"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun"

	"github.com/user/amnezia-web-client/internal/config"
)

// stubTUN заменяет /dev/net/tun: рукопожатие идёт по UDP и прав root не
// требует, а сам туннель для этой проверки не нужен.
type stubTUN struct {
	events chan tun.Event
	done   chan struct{}
	once   sync.Once
}

func newStubTUN() *stubTUN {
	return &stubTUN{events: make(chan tun.Event, 4), done: make(chan struct{})}
}

func (s *stubTUN) File() *os.File { return nil }

// Read висит до закрытия, а не вечно: иначе Close ждёт эту горутину бесконечно.
func (s *stubTUN) Read(_ [][]byte, _ []int, _ int) (int, error) {
	<-s.done
	return 0, os.ErrClosed
}

func (s *stubTUN) Write(b [][]byte, _ int) (int, error) { return len(b), nil }
func (s *stubTUN) MTU() (int, error)                    { return 1420, nil }
func (s *stubTUN) Name() (string, error)                { return "probe0", nil }
func (s *stubTUN) Events() <-chan tun.Event             { return s.events }
func (s *stubTUN) BatchSize() int                       { return 1 }

func (s *stubTUN) Close() error {
	s.once.Do(func() { close(s.done); close(s.events) })
	return nil
}

// TestRealHandshake берёт настоящий конфиг пользователя и проверяет, дойдёт
// ли рукопожатие до сервера на текущей версии ядра. Запускать вручную:
//
//	go test ./internal/vpn/ -run RealHandshake -v -tags=probe
//
// Ключи не печатаются: наружу идут только счётчики.
func TestRealHandshake(t *testing.T) {
	if os.Getenv("AWG_PROBE") == "" {
		t.Skip("проверка ходит в сеть; включается через AWG_PROBE=1")
	}

	home, _ := os.UserHomeDir()
	raw, err := os.ReadFile(filepath.Join(home, ".config", "awg-client", "config.json"))
	if err != nil {
		t.Skipf("конфига нет: %v", err)
	}

	var stored struct {
		Configs []struct {
			Name      string `json:"name"`
			RawConfig string `json:"raw_config"`
		} `json:"configs"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("не разобрать config.json: %v", err)
	}
	if len(stored.Configs) == 0 {
		t.Skip("в конфиге нет ни одной записи")
	}

	for _, entry := range stored.Configs {
		t.Run(entry.Name, func(t *testing.T) {
			cfg, err := config.ParseAmneziaConfig(entry.Name, entry.RawConfig)
			if err != nil {
				t.Fatalf("не разобрать конфиг: %v", err)
			}

			m := NewManager()
			dev := device.NewDevice(newStubTUN(), conn.NewDefaultBind(), &device.Logger{
				Verbosef: func(f string, a ...any) {},
				Errorf:   func(f string, a ...any) { t.Logf("ядро: "+f, a...) },
			})
			defer dev.Close()

			if err := dev.IpcSet(m.buildUAPIConfig(cfg)); err != nil {
				t.Fatalf("ядро отвергло конфигурацию: %v", err)
			}
			if err := dev.Up(); err != nil {
				t.Fatalf("Up: %v", err)
			}

			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				time.Sleep(time.Second)

				state, err := dev.IpcGet()
				if err != nil {
					continue
				}
				st := parseDeviceStats(state)

				if !st.LastHandshake.IsZero() {
					t.Logf("РУКОПОЖАТИЕ ЕСТЬ через %v: tx=%d rx=%d",
						time.Since(deadline.Add(-20*time.Second)).Round(time.Second), st.TxBytes, st.RxBytes)
					return
				}
			}

			state, _ := dev.IpcGet()
			st := parseDeviceStats(state)
			t.Errorf("рукопожатия НЕТ за 20с: tx=%d rx=%d (сервер молчит на этой версии ядра)", st.TxBytes, st.RxBytes)
		})
	}
}
