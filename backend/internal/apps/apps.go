// Package apps знает про программы пользователя: какие запущены, какие
// установлены и как правило app:имя узнаёт их процессы.
//
// Зачем отдельно от vpn. Правило app: сравнивает имя с процессами, а окно
// предлагает имена на выбор, и решать они обязаны одинаково: предложенное
// имя должно ловить ровно ту программу, рядом с которой его показали. Одна
// логика на двоих живёт здесь.
package apps

import (
	"regexp"
	"sync"
)

// App — программа, которую можно выбрать для правила.
type App struct {
	// Name — то, что уйдёт в правило app:Name.
	Name string `json:"name"`
	// Title — как программу называет система: «Prism Launcher», «Firefox».
	Title string `json:"title"`
	// Icon — ключ иконки для /api/apps/icon/{key}. Пусто — иконки нет.
	Icon string `json:"icon,omitempty"`
	// Running — сколько процессов программы работает прямо сейчас.
	Running int `json:"running"`
	// Installed — у программы есть ярлык в меню.
	Installed bool `json:"installed"`
	// Hint — пояснение, если имя неочевидно: «процесс java со словом
	// minecraft в командной строке».
	Hint string `json:"hint,omitempty"`
}

// Catalog — ответ окну.
type Catalog struct {
	// Supported — умеет ли система правила для программ вообще.
	Supported bool  `json:"supported"`
	Apps      []App `json:"apps"`
}

// ValidName — годится ли имя для правила. Та же проверка, что в
// config.RoutingRule.Validate: предложить имя, которое служба отвергнет,
// нельзя.
var ValidName = regexp.MustCompile(`^[a-z0-9][a-z0-9._+-]{1,63}$`).MatchString

// Иконки отдаются по ключу, а не по пути: путь пришёл из чужого файла
// ярлыка, и отдавать по запросу произвольный файл с диска служба от root не
// должна. Ключ появляется только для пути, который нашла она сама.
var icons sync.Map // ключ → путь

// IconPath возвращает путь к файлу иконки по ключу из списка.
func IconPath(key string) (string, bool) {
	v, ok := icons.Load(key)
	if !ok {
		return "", false
	}
	return v.(string), true
}
