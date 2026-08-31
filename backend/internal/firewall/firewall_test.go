package firewall

import (
	"errors"
	"strings"
	"testing"
)

// recorder подменяет запуск системных команд: проверяем, ЧТО именно уходит в
// nft и iptables, не трогая настоящую машину и не требуя root.
type recorder struct {
	calls [][]string
	stdin []string
	fail  map[string]error
}

func (r *recorder) run(name string, args []string, stdin string) error {
	r.calls = append(r.calls, append([]string{name}, args...))
	r.stdin = append(r.stdin, stdin)

	if err, ok := r.fail[strings.Join(append([]string{name}, args...), " ")]; ok {
		return err
	}
	return nil
}

func (r *recorder) joined() string {
	var sb strings.Builder
	for _, c := range r.calls {
		sb.WriteString(strings.Join(c, " "))
		sb.WriteString("\n")
	}
	return sb.String()
}

func TestNftRulesetCoversEverythingThatMustPass(t *testing.T) {
	rec := &recorder{}
	d := &nftDriver{run: rec.run}

	if err := d.Apply(Rules{Interface: "awg0"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	script := rec.stdin[0]

	must := []struct{ what, fragment string }{
		{"политика по умолчанию — запрет", "policy drop"},
		{"петля", `oifname "lo" accept`},
		{"сам туннель", `oifname "awg0" accept`},
		{"метка своих пакетов", "meta mark 0xca6c accept"},
		{"аренда адреса IPv4", "udp dport 67 accept"},
		{"аренда адреса IPv6", "udp dport 547 accept"},
		{"обе версии протокола одной таблицей", "table inet " + tableName},
	}

	for _, m := range must {
		if !strings.Contains(script, m.fragment) {
			t.Errorf("в правилах нет: %s (%q)\n%s", m.what, m.fragment, script)
		}
	}
}

// Повторный вызов не должен наложить вторую блокировку поверх первой:
// правила сложились бы вдвое, а снятие убрало бы только одну.
func TestNftApplyReplacesPreviousRules(t *testing.T) {
	rec := &recorder{}
	d := &nftDriver{run: rec.run}

	if err := d.Apply(Rules{Interface: "awg0"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	script := rec.stdin[0]
	if !strings.HasPrefix(script, clearScript) {
		t.Errorf("набор правил не начинается со снятия прежнего:\n%s", script)
	}
}

// Снятие обязано быть безобидным, когда снимать нечего: после SIGKILL прошлого
// запуска убирать приходится вслепую.
func TestNftClearIsSafeWhenNothingApplied(t *testing.T) {
	rec := &recorder{}
	d := &nftDriver{run: rec.run}

	if err := d.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	script := rec.stdin[0]
	if !strings.Contains(script, "add table inet "+tableName) {
		t.Error("перед удалением таблица не создаётся — на отсутствующей nft вернёт ошибку")
	}
	if !strings.Contains(script, "delete table inet "+tableName) {
		t.Error("таблица не удаляется")
	}
}

// Перевод трафика на цепочку — последнее действие. Иначе между созданием
// пустой цепочки и наполнением её разрешающими правилами машина осталась бы
// отрезанной от сети.
func TestIptablesDivertsTrafficLast(t *testing.T) {
	rec := &recorder{}
	d := &iptablesDriver{run: rec.run}

	if err := d.Apply(Rules{Interface: "awg0"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	log := rec.joined()
	divert := strings.Index(log, "-I OUTPUT 1 -j "+tableName)
	drop := strings.Index(log, "-A "+tableName+" -j DROP")

	if divert < 0 || drop < 0 {
		t.Fatalf("не нашлись ключевые правила:\n%s", log)
	}
	if divert < drop {
		t.Errorf("трафик переведён на цепочку раньше, чем она наполнена:\n%s", log)
	}
}

// Недоделанную блокировку оставлять нельзя: она либо не защищает, либо режет
// лишнее — и второе хуже, потому что чинить придётся вслепую.
func TestIptablesRollsBackOnFailure(t *testing.T) {
	rec := &recorder{fail: map[string]error{
		"iptables -A " + tableName + " -j DROP": errors.New("отказ"),
	}}
	d := &iptablesDriver{run: rec.run}

	if err := d.Apply(Rules{Interface: "awg0"}); err == nil {
		t.Fatal("отказ посреди установки не возвращён")
	}

	// После неудачи должно идти снятие.
	log := rec.joined()
	last := strings.LastIndex(log, "-A "+tableName+" -j DROP")
	if !strings.Contains(log[last:], "-X "+tableName) {
		t.Errorf("после отказа цепочка не убрана:\n%s", log)
	}
}

func TestIptablesClearRemovesJumpBeforeChain(t *testing.T) {
	rec := &recorder{}
	d := &iptablesDriver{run: rec.run}

	if err := d.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	log := rec.joined()
	jump := strings.Index(log, "-D OUTPUT -j "+tableName)
	remove := strings.Index(log, "-X "+tableName)

	if jump < 0 || remove < 0 {
		t.Fatalf("не нашлись шаги снятия:\n%s", log)
	}
	if jump > remove {
		t.Error("цепочку удаляют раньше, чем убирают переход на неё — ядро такого не позволит")
	}
}

// Метка должна быть ненулевой: ноль означает «метки нет», и правило по нему
// пропускало бы наружу вообще всё.
func TestMarkIsNotZero(t *testing.T) {
	if Mark == 0 {
		t.Fatal("нулевая метка выключает блокировку целиком")
	}
}
