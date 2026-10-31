package api

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"
	"github.com/user/amnezia-web-client/internal/apps"
)

// handleGetApps отдаёт программы для окна выбора правила app:. Собирается на
// каждый запрос: окно открывают редко, а список запущенного меняется всё
// время.
func (s *Server) handleGetApps(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, apps.List())
}

// handleGetAppIcon отдаёт иконку по ключу из списка. Только по ключу: путь к
// файлу знает служба, и наружу он не выходит (см. apps.IconPath).
func (s *Server) handleGetAppIcon(w http.ResponseWriter, r *http.Request) {
	path, ok := apps.IconPath(mux.Vars(r)["key"])
	if !ok {
		http.NotFound(w, r)
		return
	}

	if strings.EqualFold(filepath.Ext(path), ".svg") {
		w.Header().Set("Content-Type", "image/svg+xml")
		// SVG — это документ: скрипт внутри чужой иконки не должен
		// исполниться, даже если её откроют отдельной вкладкой.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	} else {
		w.Header().Set("Content-Type", "image/png")
	}
	w.Header().Set("Cache-Control", "max-age=3600")
	http.ServeFile(w, r, path)
}
