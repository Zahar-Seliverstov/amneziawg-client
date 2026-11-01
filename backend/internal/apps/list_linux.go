//go:build linux

package apps

// Список программ для окна выбора.
//
// Два источника. Ярлыки (.desktop) знают человеческое имя и иконку, но не
// знают, как программа выглядит процессом. Процессы знают, что работает
// сейчас, но зовутся как попало. Связывает их cgroup: KDE и GNOME запускают
// программу из меню в отдельный scope с идентификатором ярлыка в имени —
// app-org.prismlauncher.PrismLauncher@….scope, — и по нему процесс находит
// свой ярлык.

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// List собирает программы: запущенные сейчас и установленные.
func List() Catalog {
	entries := desktopEntries()

	byID := make(map[string]*desktopEntry, len(entries))
	for i := range entries {
		byID[strings.ToLower(entries[i].id)] = &entries[i]
	}

	apps := make(map[string]*App)
	add := func(name string) *App {
		a, ok := apps[name]
		if !ok {
			a = &App{Name: name, Title: name}
			apps[name] = a
		}
		return a
	}

	for _, e := range entries {
		a := add(e.name)
		a.Installed = true
		// Несколько ярлыков на одну программу (LibreOffice Base, Calc,
		// Writer — всё это soffice) — берём самое короткое название: оно и
		// есть общее, «LibreOffice».
		if a.Title == a.Name || len(e.title) < len(a.Title) {
			a.Title = e.title
		}
		if a.Icon == "" {
			a.Icon = e.icon
		}
	}

	self := os.Getpid()
	for _, pid := range ListPIDs() {
		if pid == self {
			continue
		}
		p, ok := ReadProc(pid)
		if !ok || !p.UserProcess() || ignoredProcess(p) {
			continue
		}

		name, hint := suggestName(p)
		if !ValidName(name) {
			continue
		}

		// В список идёт то, что пользователь узнает: программа с ярлыком
		// или процесс, чьё имя без подсказки не угадать (java, python,
		// Wine, Flatpak). Команды из терминала и службы сессии — нет: их
		// десятки, и за ними не видно программ.
		if _, known := apps[name]; !known && hint == "" {
			continue
		}

		a := add(name)
		a.Running++
		if hint != "" {
			a.Hint = hint
			// Иконка ярлыка, из которого запущен процесс: у Minecraft это
			// лаунчер. Названия не берём — у игры оно своё, не лаунчера.
			if e := byID[scopeAppID(p.Cgroup)]; e != nil && a.Icon == "" {
				a.Icon = e.icon
			}
		}
	}

	// Minecraft — частый случай, когда имя не угадать: процесс зовётся java.
	// Если стоит лаунчер, игра предлагается сразу, даже не запущенной.
	if launcher := minecraftLauncher(entries); launcher != nil {
		a := add("minecraft")
		a.Installed = true
		if a.Title == a.Name {
			a.Title = "Minecraft"
		}
		if a.Icon == "" {
			a.Icon = launcher.icon
		}
		a.Hint = "Сама игра: процесс java со словом minecraft в командной строке"
	}

	out := make([]App, 0, len(apps))
	for _, a := range apps {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Running > 0) != (out[j].Running > 0) {
			return out[i].Running > 0
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})

	return Catalog{Supported: true, Apps: out}
}

// suggestName — имя для правила, которое поймает этот процесс, и пояснение,
// если имя неочевидно.
func suggestName(p Proc) (string, string) {
	scope := strings.ToLower(p.Cgroup)
	if m := flatpakScope.FindStringSubmatch(scope); m != nil {
		return m[1], "Приложение Flatpak"
	}

	exe := p.Exe
	if exe == "" {
		exe = p.Comm
	}

	switch {
	case strings.HasPrefix(exe, "java"):
		if strings.Contains(p.Cmdline, "minecraft") {
			return "minecraft", "Сама игра: процесс java со словом minecraft в командной строке"
		}
		if jar := argAfter(p.Args, "-jar"); jar != "" {
			name := strings.ToLower(strings.TrimSuffix(filepath.Base(jar), ".jar"))
			return name, "Программа на Java: " + filepath.Base(jar)
		}
	case strings.HasPrefix(exe, "python"):
		if len(p.Args) > 1 && strings.HasSuffix(p.Args[1], ".py") {
			name := strings.ToLower(strings.TrimSuffix(filepath.Base(p.Args[1]), ".py"))
			return name, "Скрипт на Python: " + filepath.Base(p.Args[1])
		}
	case strings.HasSuffix(exe, ".exe") || strings.HasPrefix(exe, "wine"):
		for _, arg := range p.Args {
			if strings.HasSuffix(strings.ToLower(arg), ".exe") {
				base := strings.ToLower(filepath.Base(strings.ReplaceAll(arg, `\`, "/")))
				return base, "Программа Windows"
			}
		}
	}

	return exe, ""
}

func argAfter(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

var flatpakScope = regexp.MustCompile(`app-flatpak-([a-z0-9._-]+?)-\d+\.scope`)

// scopeAppID — идентификатор ярлыка из имени scope:
// app-org.kde.konsole-2991.scope → org.kde.konsole,
// app-org.prismlauncher.PrismLauncher@4f2c.service → org.prismlauncher.prismlauncher.
func scopeAppID(cgroup string) string {
	for _, part := range strings.Split(strings.ToLower(cgroup), "/") {
		rest, ok := strings.CutPrefix(part, "app-")
		if !ok || strings.HasPrefix(rest, "flatpak-") {
			continue
		}
		rest = strings.TrimSuffix(strings.TrimSuffix(rest, ".scope"), ".service")
		if at := strings.IndexByte(rest, '@'); at >= 0 {
			rest = rest[:at]
		}
		if i := strings.LastIndexByte(rest, '-'); i > 0 && allDigits(rest[i+1:]) {
			rest = rest[:i]
		}
		return rest
	}
	return ""
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ignoredProcess — оболочки, служебные процессы сессии и утилиты: правило на
// них никому не нужно, а в списке они заслонили бы программы.
func ignoredProcess(p Proc) bool {
	// Служебное сессии живёт в session.slice и background.slice, программы
	// пользователя — в app.slice.
	if !strings.Contains(p.Cgroup, "/app.slice/") {
		return true
	}

	name := p.Exe
	if name == "" {
		name = p.Comm
	}
	if ignoredNames[name] {
		return true
	}
	for _, prefix := range ignoredPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

var ignoredNames = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "fish": true, "dash": true, "nu": true,
	"sudo": true, "su": true, "login": true, "env": true, "sleep": true, "cat": true,
	"less": true, "tail": true, "grep": true, "ps": true, "top": true, "htop": true,
	"btop": true, "watch": true, "timeout": true, "nohup": true, "setsid": true,
	"xdg-open": true, "kdialog": true, "systemd-inhibit": true, "powerprofilesctl": true,
	"bwrap": true, "flatpak-bwrap": true, "flatpak-session-helper": true,
	"kioworker": true, "kiod6": true, "kded6": true, "baloorunner": true,
	"tmux": true, "screen": true, "ssh-agent": true, "gpg-agent": true,
}

var ignoredPrefixes = []string{"xdg-", "dbus-", "at-spi", "gvfs", "kio", "pipewire", "wireplumber", "polkit", "zypak"}

// ─── Ярлыки ─────────────────────────────────────────────────────────────

type desktopEntry struct {
	id    string // имя файла без .desktop
	name  string // для правила
	title string
	icon  string // ключ иконки
	exec  string
}

func desktopEntries() []desktopEntry {
	// Свои ярлыки пользователя идут первыми: одноимённый системный они
	// перекрывают, как и в меню.
	var dirs []string
	for _, home := range userHomes() {
		dirs = append(dirs,
			filepath.Join(home, ".local/share/applications"),
			filepath.Join(home, ".local/share/flatpak/exports/share/applications"),
		)
	}
	dirs = append(dirs,
		"/usr/local/share/applications",
		"/var/lib/flatpak/exports/share/applications",
		"/usr/share/applications",
	)

	seen := make(map[string]bool)
	var out []desktopEntry

	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*.desktop"))
		for _, file := range files {
			id := strings.TrimSuffix(filepath.Base(file), ".desktop")
			if seen[id] {
				continue
			}
			e, ok := parseDesktop(file)
			if !ok {
				continue
			}
			seen[id] = true
			e.id = id
			out = append(out, e)
		}
	}

	return out
}

func parseDesktop(file string) (desktopEntry, bool) {
	f, err := os.Open(file)
	if err != nil {
		return desktopEntry{}, false
	}
	defer f.Close()

	var (
		e                        desktopEntry
		inMain                   bool
		title, titleRu, iconName string
		typ, noDisplay, hidden   string
	)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			inMain = line == "[Desktop Entry]"
			continue
		}
		if !inMain {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Name":
			title = value
		case "Name[ru]":
			titleRu = value
		case "Exec":
			e.exec = value
		case "Icon":
			iconName = value
		case "Type":
			typ = value
		case "NoDisplay":
			noDisplay = value
		case "Hidden":
			hidden = value
		}
	}

	if typ != "Application" || noDisplay == "true" || hidden == "true" || e.exec == "" {
		return desktopEntry{}, false
	}

	e.name = nameFromExec(e.exec)
	if !ValidName(e.name) {
		return desktopEntry{}, false
	}

	e.title = title
	if titleRu != "" {
		e.title = titleRu
	}
	if e.title == "" {
		e.title = e.name
	}
	if path := findIcon(iconName); path != "" {
		e.icon = registerIcon(path)
	}
	return e, true
}

// nameFromExec — как программа из ярлыка будет выглядеть процессом.
//
//	prismlauncher %U                        → prismlauncher
//	env GDK_BACKEND=x11 /opt/app/bin/app    → app
//	flatpak run --branch=stable org.x.App   → org.x.app (узнаётся по cgroup)
func nameFromExec(exec string) string {
	fields := execFields(exec)
	i := 0
	for i < len(fields) && (fields[i] == "env" || strings.Contains(fields[i], "=")) {
		i++
	}
	if i >= len(fields) {
		return ""
	}

	bin := strings.ToLower(filepath.Base(fields[i]))

	// Ярлык, открывающий ссылку или файл: процесса у такой «программы» нет.
	switch bin {
	case "xdg-open", "gio", "kde-open", "kde-open5", "kioclient", "kioclient5", "sh", "bash":
		return ""
	}

	// Интерпретатор со скриптом — программа это скрипт, и правило ловит
	// его по слову в командной строке.
	if strings.HasPrefix(bin, "python") || bin == "node" || bin == "java" {
		for _, f := range fields[i+1:] {
			if strings.HasPrefix(f, "-") {
				continue
			}
			base := strings.ToLower(filepath.Base(f))
			return strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(base, ".py"), ".js"), ".jar")
		}
		return ""
	}

	if bin == "flatpak" {
		for _, f := range fields[i+1:] {
			if f == "run" || strings.HasPrefix(f, "-") || strings.HasPrefix(f, "@@") {
				continue
			}
			return strings.ToLower(f)
		}
		return ""
	}
	return bin
}

// execFields делит строку Exec на слова с учётом кавычек: путь с пробелом
// пишется в ярлыке как "/opt/My App/app".
func execFields(exec string) []string {
	var (
		fields []string
		cur    strings.Builder
		quoted bool
		inWord bool
	)
	for _, c := range exec {
		switch {
		case c == '"':
			quoted = !quoted
			inWord = true
		case (c == ' ' || c == '\t') && !quoted:
			if inWord {
				fields = append(fields, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	if inWord {
		fields = append(fields, cur.String())
	}
	return fields
}

func minecraftLauncher(entries []desktopEntry) *desktopEntry {
	for i, e := range entries {
		for _, known := range []string{"prismlauncher", "multimc", "atlauncher", "minecraft-launcher", "polymc", "modrinth-app", "org.prismlauncher.prismlauncher"} {
			if e.name == known {
				return &entries[i]
			}
		}
	}
	return nil
}

// userHomes — домашние каталоги людей: служба работает от root и своего
// дома пользователя не знает.
func userHomes() []string {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return nil
	}
	defer f.Close()

	var homes []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), ":")
		if len(parts) < 7 || !strings.HasPrefix(parts[5], "/home/") {
			continue
		}
		homes = append(homes, parts[5])
	}
	return homes
}

// ─── Иконки ─────────────────────────────────────────────────────────────

var (
	iconMu    sync.Mutex
	iconCache = map[string]string{}
)

// findIcon ищет файл иконки по имени из ярлыка. Сначала растровые размеры
// около 64, потом векторные: окну нужна иконка в 32 точки, и 64 — с запасом
// под плотный экран.
func findIcon(name string) string {
	if name == "" {
		return ""
	}
	iconMu.Lock()
	path, ok := iconCache[name]
	iconMu.Unlock()
	if ok {
		return path
	}

	path = lookupIcon(name)

	iconMu.Lock()
	iconCache[name] = path
	iconMu.Unlock()
	return path
}

func lookupIcon(name string) string {
	if filepath.IsAbs(name) {
		if usableIcon(name) {
			return name
		}
		return ""
	}

	var bases []string
	for _, home := range userHomes() {
		bases = append(bases,
			filepath.Join(home, ".local/share/icons"),
			filepath.Join(home, ".local/share/flatpak/exports/share/icons"),
		)
	}
	bases = append(bases, "/usr/share/icons", "/var/lib/flatpak/exports/share/icons")

	themes := []string{"hicolor", "breeze", "Papirus", "Adwaita"}
	sizes := []string{"64x64", "48x48", "128x128", "96x96", "256x256", "scalable"}

	for _, base := range bases {
		for _, theme := range themes {
			for _, size := range sizes {
				for _, ext := range []string{".png", ".svg"} {
					p := filepath.Join(base, theme, size, "apps", name+ext)
					if usableIcon(p) {
						return p
					}
				}
			}
			// Раскладка breeze и Papirus: apps/48/имя.svg.
			for _, size := range []string{"64", "48", "32"} {
				p := filepath.Join(base, theme, "apps", size, name+".svg")
				if usableIcon(p) {
					return p
				}
			}
		}
	}

	for _, ext := range []string{".png", ".svg"} {
		p := filepath.Join("/usr/share/pixmaps", name+ext)
		if usableIcon(p) {
			return p
		}
	}
	return ""
}

func usableIcon(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".png" && ext != ".svg" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() < 2<<20
}

// registerIcon выдаёт ключ для найденного службой пути к иконке.
func registerIcon(path string) string {
	sum := sha1.Sum([]byte(path))
	key := hex.EncodeToString(sum[:8])
	icons.Store(key, path)
	return key
}
