package api

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/user/amnezia-web-client/internal/web"
)

// SetupStatic подключает раздачу собранного интерфейса на том же порту, что и
// API. Это важно не только для удобства: фронтенд строит адреса запросов от
// window.location.hostname, поэтому UI и API обязаны жить на одном origin —
// иначе десктопная оболочка (tauri://) не найдёт backend.
//
// dir != "" — раздаём из каталога на диске (удобно в разработке),
// иначе берём UI, вшитый в бинарник на сборке.
func (s *Server) SetupStatic(dir string) error {
	var fsys fs.FS

	if dir != "" {
		info, err := os.Stat(path.Join(dir, "index.html"))
		if err != nil || info.IsDir() {
			return fmt.Errorf("в %s нет index.html", dir)
		}
		fsys = os.DirFS(dir)
	} else {
		var ok bool
		fsys, ok = web.Dist()
		if !ok {
			return errors.New("интерфейс не вшит в бинарник (собери: make build-ui)")
		}
	}

	// PathPrefix("/") ловит всё, что не разобрали маршруты /api выше:
	// mux проверяет маршруты в порядке регистрации, поэтому API не страдает.
	s.router.PathPrefix("/").Handler(staticHandler(fsys))
	return nil
}

// staticHandler отдаёт файлы UI, а на неизвестные пути возвращает index.html —
// клиентский роутер Nuxt разберётся с ними сам.
func staticHandler(fsys fs.FS) http.Handler {
	files := http.FileServer(http.FS(fsys))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Неизвестный путь под /api — это ошибка запроса, а не адрес страницы.
		// Отдать здесь index.html нельзя: клиент получит HTML вместо JSON и
		// упадёт на разборе ответа. Ровно так выглядит вызов эндпоинта,
		// которого ещё нет в устаревшем backend'е.
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			jsonError(w, "Unknown API endpoint", http.StatusNotFound)
			return
		}

		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")

		if name != "" {
			// Ассеты Nuxt содержат хеш в имени — их можно кешировать навсегда.
			if strings.HasPrefix(name, "_nuxt/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}

			if info, err := fs.Stat(fsys, name); err == nil && !info.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}

		serveIndex(w, r, fsys)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	data, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		http.Error(w, "UI не найден", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(data)
	}
}
