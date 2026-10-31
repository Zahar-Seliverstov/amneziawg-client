package api

import (
	"net/http"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/version"
)

func (s *Server) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]string{"version": version.Value})
}

// Settings handlers
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings := s.config.GetSettings()
	jsonResponse(w, settings)
}

func (s *Server) handleSetSettings(w http.ResponseWriter, r *http.Request) {
	var settings config.AppSettings
	if !decodeJSON(w, r, &settings) {
		return
	}

	s.config.SetSettings(settings)

	if err := s.config.Save(); err != nil {
		jsonError(w, "Не удалось сохранить настройки", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, settings)
}
