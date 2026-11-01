package apps

import (
	"strings"
)

// NameFromImage — имя для правила по пути к исполняемому файлу Windows:
// «C:\Program Files\Steam\steam.exe» → «steam».
func NameFromImage(path string) string {
	base := path[strings.LastIndexAny(path, `\/`)+1:]
	return strings.TrimSuffix(strings.ToLower(base), ".exe")
}

// MatchImage возвращает имя из набора, под которое подходит процесс с этим
// исполняемым файлом, или пустую строку.
//
// Сначала точное имя файла, затем — имя в пути: так app:steam ловит и игры
// из библиотеки Steam, а app:minecraft — javaw.exe из каталога .minecraft.
// Это то же, что на Linux дают cgroup лаунчера и слово в командной строке.
func MatchImage(path string, names []string) string {
	name := NameFromImage(path)
	for _, n := range names {
		if n == name {
			return n
		}
	}
	lower := strings.ToLower(path)
	for _, n := range names {
		if len(n) >= MinNameInPath && strings.Contains(lower, n) {
			return n
		}
	}
	return ""
}

// MinNameInPath — с какой длины имя ищется внутри пути: короткое нашлось бы
// почти в любом.
const MinNameInPath = 4
