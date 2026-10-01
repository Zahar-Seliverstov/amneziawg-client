package iproute

import (
	"errors"
	"strings"
	"testing"
)

// recordingTool собирает то, что ушло бы настоящей команде ip.
type recordingTool struct {
	*cmdTool
	single  [][]string
	scripts []string
	// fail — что вернуть на пакетный запуск: вывод и ошибка.
	failOutput string
	failErr    error
}

func newRecordingTool() *recordingTool {
	r := &recordingTool{}
	r.cmdTool = &cmdTool{
		run: func(args ...string) (string, error) {
			r.single = append(r.single, args)
			return "", nil
		},
		runBatch: func(script string) (string, error) {
			r.scripts = append(r.scripts, script)
			return r.failOutput, r.failErr
		},
	}
	return r
}

// Ради этого пакетные операции и заведены: один запуск на весь список.
func TestBatchSendsOneScript(t *testing.T) {
	r := newRecordingTool()

	errs := r.AddRoutes([][]string{
		{"1.1.1.1/32", "dev", "awg0"},
		{"8.8.8.8/32", "dev", "awg0"},
		{"10.0.0.0/8", "dev", "awg0"},
	})

	if len(r.scripts) != 1 {
		t.Fatalf("запусков ip: %d, ожидался один", len(r.scripts))
	}
	if len(r.single) != 0 {
		t.Errorf("часть маршрутов ушла по одному: %v", r.single)
	}

	want := "route add 1.1.1.1/32 dev awg0\nroute add 8.8.8.8/32 dev awg0\nroute add 10.0.0.0/8 dev awg0\n"
	if r.scripts[0] != want {
		t.Errorf("список команд:\n%q\nожидался:\n%q", r.scripts[0], want)
	}

	for i, err := range errs {
		if err != nil {
			t.Errorf("маршрут %d отвергнут без причины: %v", i, err)
		}
	}
}

// Учёт маршрутов ведёт вызывающий: он обязан узнать, какие именно не легли,
// иначе снимет чужое или оставит своё.
func TestBatchAttributesFailuresToRoutes(t *testing.T) {
	r := newRecordingTool()
	r.failErr = errors.New("exit status 2")
	r.failOutput = "RTNETLINK answers: File exists\nCommand failed -:2\nRTNETLINK answers: Network is unreachable\nCommand failed -:4\n"

	errs := r.AddRoutes([][]string{
		{"1.1.1.1/32", "dev", "awg0"},
		{"8.8.8.8/32", "dev", "awg0"},
		{"9.9.9.9/32", "dev", "awg0"},
		{"10.0.0.0/8", "dev", "awg0"},
	})

	if errs[0] != nil || errs[2] != nil {
		t.Errorf("удавшиеся маршруты помечены неудачей: %v", errs)
	}
	if errs[1] == nil || errs[3] == nil {
		t.Errorf("неудавшиеся маршруты не помечены: %v", errs)
	}
	// В тексте ошибки должен быть сам маршрут: номер строки в журнале
	// пользователю ничего не говорит.
	if !strings.Contains(errs[1].Error(), "8.8.8.8/32") {
		t.Errorf("в ошибке нет маршрута: %v", errs[1])
	}
}

// Команда отказала целиком — не нашлась, не приняла ключ. Записать в учёт
// маршрут, которого нет, опаснее, чем не записать поставленный.
func TestBatchFailsWholeListWhenUnattributable(t *testing.T) {
	r := newRecordingTool()
	r.failErr = errors.New("executable file not found")
	r.failOutput = ""

	errs := r.DelRoutes([][]string{
		{"1.1.1.1/32", "dev", "awg0"},
		{"8.8.8.8/32", "dev", "awg0"},
	})

	for i, err := range errs {
		if err == nil {
			t.Errorf("маршрут %d считается снятым, хотя ip не отработал", i)
		}
	}
}

// Один маршрут пакетом гнать незачем: обычный вызов даёт внятный текст
// ошибки вместо номера строки.
func TestBatchOfOneGoesDirect(t *testing.T) {
	r := newRecordingTool()

	if errs := r.AddRoutes([][]string{{"1.1.1.1/32", "dev", "awg0"}}); len(errs) != 1 || errs[0] != nil {
		t.Fatalf("одиночный маршрут не прошёл: %v", errs)
	}
	if len(r.scripts) != 0 {
		t.Errorf("одиночный маршрут ушёл пакетом: %v", r.scripts)
	}
	if len(r.single) != 1 {
		t.Fatalf("вызовов ip: %d, ожидался один", len(r.single))
	}
	if got := strings.Join(r.single[0], " "); got != "route add 1.1.1.1/32 dev awg0" {
		t.Errorf("команда %q", got)
	}
}

func TestBatchOfNoneDoesNothing(t *testing.T) {
	r := newRecordingTool()

	if errs := r.AddRoutes(nil); len(errs) != 0 {
		t.Errorf("на пустой список пришло %d ответов", len(errs))
	}
	if len(r.scripts) != 0 || len(r.single) != 0 {
		t.Error("на пустой список запускали ip")
	}
}
