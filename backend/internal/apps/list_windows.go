package apps

import (
	"os"
	"sort"
	"strings"
)

// List на Windows — программы, запущенные пользователем сейчас.
//
// Установленные не перечисляются: ярлыки меню «Пуск» ведут куда угодно
// (на лаунчеры, сайты, деинсталляторы), а правило app: нужно имени exe.
// Программу, которой нет в списке, можно вписать вручную — app:имя.
func List() Catalog {
	self := uint32(os.Getpid())
	apps := make(map[string]*App)

	for _, p := range Processes() {
		// Сеанс 0 — службы, системные пути — сама Windows: правило на них
		// не нужно, а в списке они заслонили бы программы.
		if p.PID == self || p.Session == 0 || p.Path == "" || systemImage(p.Path) {
			continue
		}
		name := NameFromImage(p.Path)
		if !ValidName(name) || ignoredWindows[name] {
			continue
		}
		a, ok := apps[name]
		if !ok {
			a = &App{Name: name, Title: name}
			apps[name] = a
		}
		a.Running++
	}

	out := make([]App, 0, len(apps))
	for _, a := range apps {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })

	return Catalog{Supported: true, Apps: out}
}

func systemImage(path string) bool {
	lower := strings.ToLower(path)
	return strings.Contains(lower, `\windows\`) || strings.Contains(lower, `\windowsapps\microsoft.`)
}

var ignoredWindows = map[string]bool{
	"cmd": true, "powershell": true, "pwsh": true, "conhost": true, "windowsterminal": true,
	"openconsole": true, "awg-ui": true, "awg-service": true,
}
