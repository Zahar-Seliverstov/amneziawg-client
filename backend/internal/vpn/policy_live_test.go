//go:build linux

package vpn

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Живая проверка правил и nftables. Трогает настоящую сеть, поэтому
// запускается только в отдельном сетевом пространстве:
//
//	AWG_LIVE_POLICY=1 unshare -rn go test -run TestLivePolicy ./internal/vpn/
func TestLivePolicy(t *testing.T) {
	if os.Getenv("AWG_LIVE_POLICY") != "1" {
		t.Skip("только в отдельном сетевом пространстве: AWG_LIVE_POLICY=1 unshare -rn")
	}

	p := &linuxPolicy{apps: newAppWatcher("awg-live-test")}

	if err := p.Up(true); err != nil {
		t.Fatalf("Up: %v", err)
	}

	rules, _ := exec.Command("ip", "rule").CombinedOutput()
	for _, want := range []string{
		"5200:\tfrom all fwmark 0x51820 lookup main",
		"5210:\tfrom all lookup main suppress_prefixlength 0",
		"5220:\tfrom all lookup 51821",
	} {
		if !strings.Contains(string(rules), want) {
			t.Errorf("нет правила %q:\n%s", want, rules)
		}
	}

	if err := ipRun("link", "add", "awgt0", "type", "dummy"); err != nil {
		t.Fatal(err)
	}
	_ = ipRun("link", "set", "awgt0", "up")
	for _, args := range [][]string{
		{"route", "add", "0.0.0.0/0", "dev", "awgt0", "table", routeTable},
		{"route", "add", "throw", "93.184.216.34/32", "table", routeTable},
	} {
		if err := ipRun(args...); err != nil {
			t.Fatal(err)
		}
	}

	table, _ := exec.Command("nft", "list", "table", "inet", nftTable).CombinedOutput()
	if !strings.Contains(string(table), "ct mark set meta mark") {
		t.Errorf("перенос меток не встал:\n%s", table)
	}

	// Повторный Up на живых правилах — пересборка — не должен плодить дубли.
	if err := p.Up(false); err != nil {
		t.Fatalf("повторный Up: %v", err)
	}
	rules, _ = exec.Command("ip", "rule").CombinedOutput()
	if strings.Count(string(rules), "lookup 51821") != 1 || strings.Contains(string(rules), "suppress_prefixlength") {
		t.Errorf("после повторного Up(false):\n%s", rules)
	}

	p.Down()
	rules, _ = exec.Command("ip", "rule").CombinedOutput()
	routes, _ := exec.Command("ip", "route", "show", "table", routeTable).CombinedOutput()
	if strings.Contains(string(rules), "5220") || strings.TrimSpace(string(routes)) != "" {
		t.Errorf("после Down остались правила или маршруты:\n%s\n%s", rules, routes)
	}
	if out, err := exec.Command("nft", "list", "table", "inet", nftTable).CombinedOutput(); err == nil {
		t.Errorf("после Down осталась таблица nftables:\n%s", out)
	}
}

// Программы: метка, подмена адреса и правило на метку встают и снимаются.
func TestLivePolicyApps(t *testing.T) {
	if os.Getenv("AWG_LIVE_POLICY") != "1" {
		t.Skip("только в отдельном сетевом пространстве: AWG_LIVE_POLICY=1 unshare -rn")
	}

	// cgroup в отдельном пространстве не создать — берём существующие,
	// nftables нужны только их пути.
	w := newAppWatcher("user.slice")
	w.all, w.udp = "user.slice", "init.scope"
	p := &linuxPolicy{apps: w}
	if err := p.installAppsRouting("main", "lo", false); err != nil {
		t.Fatalf("installAppsRouting: %v", err)
	}

	table, _ := exec.Command("nft", "list", "table", "inet", nftAppsTable).CombinedOutput()
	if !strings.Contains(string(table), "masquerade") || strings.Count(string(table), "socket cgroupv2") != 2 ||
		!strings.Contains(string(table), "meta l4proto udp") {
		t.Errorf("таблица программ неполная:\n%s", table)
	}
	rules, _ := exec.Command("ip", "rule").CombinedOutput()
	if !strings.Contains(string(rules), "5201:\tfrom all fwmark 0x51821 lookup main") {
		t.Errorf("нет правила на метку программ:\n%s", rules)
	}

	p.dropAppsRouting()
	if out, err := exec.Command("nft", "list", "table", "inet", nftAppsTable).CombinedOutput(); err == nil {
		t.Errorf("таблица программ осталась:\n%s", out)
	}
}
