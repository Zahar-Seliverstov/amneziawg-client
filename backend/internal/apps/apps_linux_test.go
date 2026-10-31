//go:build linux

package apps

import "testing"

var minecraftJava = Proc{
	Comm:    "java",
	Exe:     "java",
	Argv0:   "java",
	Args:    []string{"/usr/lib/jvm/java-25-openjdk/bin/java", "-Xmx8192m", "net.minecraft.client.main.Main"},
	Cmdline: "/usr/lib/jvm/java-25-openjdk/bin/java -xmx8192m -djava.library.path=/home/me/.local/share/prismlauncher/instances/pvp 26.2/natives net.minecraft.client.main.main",
	Cgroup:  "/user.slice/user-1000.slice/user@1000.service/app.slice/app-org.prismlauncher.PrismLauncher@4f2c.service",
}

func TestMatch(t *testing.T) {
	cases := []struct {
		proc  Proc
		names []string
		want  string
	}{
		// Minecraft узнаётся только по командной строке: имя процесса — java.
		{minecraftJava, []string{"minecraft"}, "minecraft"},
		{minecraftJava, []string{"java"}, "java"},
		{Proc{Comm: "firefox", Exe: "firefox-bin", Argv0: "firefox"}, []string{"firefox"}, "firefox"},
		// Flatpak: внутри песочницы процесс зовётся как угодно, ловится по
		// идентификатору в имени cgroup.
		{Proc{Comm: "telegram-deskto", Cgroup: "/user.slice/user-1000.slice/user@1000.service/app.slice/app-flatpak-org.telegram.desktop-123.scope"},
			[]string{"org.telegram.desktop"}, "org.telegram.desktop"},
		// Короткое имя в командной строке не ищется: «go» нашлось бы везде.
		{Proc{Comm: "bash", Cmdline: "bash -c go build"}, []string{"go"}, ""},
		{Proc{Comm: "steam", Cmdline: "steam"}, []string{"minecraft"}, ""},
	}

	for _, tc := range cases {
		if got := Match(tc.proc, tc.names); got != tc.want {
			t.Errorf("Match(%s, %v) = %q, ожидали %q", tc.proc.Comm, tc.names, got, tc.want)
		}
	}
}

// Предложенное окну имя обязано ловить тот самый процесс, рядом с которым
// его показали: иначе выбор из списка — та же угадайка.
func TestSuggestedNameMatchesItsProcess(t *testing.T) {
	procs := []Proc{
		minecraftJava,
		{Comm: "firefox", Exe: "firefox", Argv0: "firefox", Args: []string{"/usr/lib/firefox/firefox"}, Cmdline: "/usr/lib/firefox/firefox"},
		{Comm: "java", Exe: "java", Argv0: "java", Args: []string{"java", "-jar", "/opt/tools/Burp.jar"}, Cmdline: "java -jar /opt/tools/burp.jar"},
		{Comm: "python3", Exe: "python3.13", Argv0: "python3", Args: []string{"python3", "/home/me/bot/tg_bot.py"}, Cmdline: "python3 /home/me/bot/tg_bot.py"},
		{Comm: "telegram-deskto", Exe: "telegram-desktop", Cgroup: "/user.slice/x/app.slice/app-flatpak-org.telegram.desktop-77.scope"},
	}

	for _, p := range procs {
		name, _ := suggestName(p)
		if !ValidName(name) {
			t.Errorf("%s: предложено негодное имя %q", p.Comm, name)
			continue
		}
		if Match(p, []string{name}) != name {
			t.Errorf("%s: предложено %q, но правило с ним процесс не поймает", p.Comm, name)
		}
	}
}

func TestNameFromExec(t *testing.T) {
	cases := map[string]string{
		"prismlauncher %U":                         "prismlauncher",
		"env GDK_BACKEND=x11 /opt/Discord/Discord": "discord",
		`"/opt/My App/app" --flag`:                 "app",
		"/usr/bin/flatpak run --branch=stable --arch=x86_64 --command=telegram-desktop org.telegram.desktop -- %u": "org.telegram.desktop",
		"firefox %u": "firefox",
	}
	for exec, want := range cases {
		if got := nameFromExec(exec); got != want {
			t.Errorf("nameFromExec(%q) = %q, ожидали %q", exec, got, want)
		}
	}
}

func TestScopeAppID(t *testing.T) {
	cases := map[string]string{
		"/user.slice/user-1000.slice/user@1000.service/app.slice/app-org.kde.konsole-2991.scope/tab(3021).scope":   "org.kde.konsole",
		"/user.slice/user-1000.slice/user@1000.service/app.slice/app-org.prismlauncher.PrismLauncher@4f2c.service": "org.prismlauncher.prismlauncher",
		"/user.slice/user-1000.slice/user@1000.service/app.slice/app-firefox.scope":                                "firefox",
		"/system.slice/awg-service.service": "",
	}
	for cg, want := range cases {
		if got := scopeAppID(cg); got != want {
			t.Errorf("scopeAppID(%q) = %q, ожидали %q", cg, got, want)
		}
	}
}

func TestParseProcFields(t *testing.T) {
	if got := ParseCgroupV2("0::/user.slice/user-1000.slice/app.slice/app-prism.scope\n"); got != "/user.slice/user-1000.slice/app.slice/app-prism.scope" {
		t.Errorf("ParseCgroupV2 = %q", got)
	}
	// Имя процесса со скобками и пробелами не сбивает разбор.
	if got := ParsePPID("4242 (my (odd) name) S 1717 4242 4242 0 -1"); got != 1717 {
		t.Errorf("ParsePPID = %d, ожидали 1717", got)
	}
}
