package rulesource

import "strings"

// RegistrableDomain возвращает имя, по которому имя относят к владельцу:
// git.dtel.ru → dtel.ru, www.bbc.co.uk → bbc.co.uk.
//
// Это намеренно приблизительный ответ. Точный требует списка общественных
// суффиксов (Public Suffix List) — мегабайта данных, который к тому же надо
// обновлять; ради группировки списка правил на экране это не окупается.
// Берём две последние части имени, а для суффиксов вида co.uk — три.
//
// Ошибка приблизительности стоит недорого: две записи не сойдутся в одну
// группу или сойдутся лишние. Ни маршрутизация, ни сами правила от этого не
// зависят — только то, как они разложены на экране.
func RegistrableDomain(name string) string {
	name = strings.Trim(strings.TrimSpace(strings.ToLower(name)), ".")
	if name == "" {
		return ""
	}

	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return name
	}

	tail := strings.Join(labels[len(labels)-2:], ".")
	if len(labels) > 2 && twoLabelSuffixes[tail] {
		return strings.Join(labels[len(labels)-3:], ".")
	}

	return tail
}

// twoLabelSuffixes — суффиксы, у которых обе части общие, а имя владельца
// стоит третьим: bbc.co.uk, а не co.uk. Список короткий и покрывает то, что
// реально встречается; всё остальное разбирается общим правилом.
var twoLabelSuffixes = map[string]bool{
	"co.uk": true, "org.uk": true, "ac.uk": true, "gov.uk": true, "net.uk": true,
	"com.au": true, "net.au": true, "org.au": true, "edu.au": true,
	"com.br": true, "com.cn": true, "net.cn": true, "org.cn": true,
	"com.tr": true, "com.mx": true, "com.ar": true, "com.sg": true,
	"com.hk": true, "com.tw": true, "com.ua": true, "com.pl": true,
	"co.jp": true, "co.kr": true, "co.nz": true, "co.za": true,
	"co.il": true, "co.in": true, "co.id": true, "co.th": true,
	"org.ru": true, "net.ru": true, "com.ru": true, "edu.ru": true, "gov.ru": true,
}
