package apps

import "testing"

func TestMatchImage(t *testing.T) {
	names := []string{"steam", "minecraft", "go", "discord"}
	cases := map[string]string{
		`C:\Program Files (x86)\Steam\steam.exe`:                           "steam",
		`D:\SteamLibrary\steamapps\common\Dota 2\game\bin\win64\dota2.exe`: "steam",
		`C:\Users\a\AppData\Roaming\.minecraft\runtime\java\bin\javaw.exe`: "minecraft",
		`C:\Users\a\AppData\Local\Discord\app-1.0\Discord.exe`:             "discord",
		`C:\Go\bin\go.exe`:                  "go",
		`C:\Program Files\Mongo\mongod.exe`: "",
		`C:\Windows\explorer.exe`:           "",
	}
	for path, want := range cases {
		if got := MatchImage(path, names); got != want {
			t.Errorf("%s: %q, ожидалось %q", path, got, want)
		}
	}
}

func TestNameFromImage(t *testing.T) {
	if got := NameFromImage(`C:\X\Telegram.EXE`); got != "telegram" {
		t.Errorf("%q", got)
	}
}
