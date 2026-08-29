// Package web хранит собранный статический интерфейс, вшитый в бинарник.
//
// Каталог dist наполняется на сборке: `make build-ui` кладёт туда результат
// `nuxt generate`. Если UI не собирали, внутри лежит только .gitkeep —
// Dist() вернёт ok=false, и сервер просто отдаст один API без интерфейса.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// Dist возвращает файловую систему UI. ok=false — интерфейс не вшит.
func Dist() (fs.FS, bool) {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}
