package api

import "net/http"

// VPN handlers
func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	status := s.vpnManager.GetStatus()
	jsonResponse(w, status)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConfigID string `json:"config_id"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	cfg := s.config.GetConfig(req.ConfigID)
	if cfg == nil {
		jsonError(w, "Конфигурация не найдена", http.StatusNotFound)
		return
	}

	routing := s.config.GetRouting()

	if err := s.vpnManager.Connect(cfg, &routing); err != nil {
		jsonError(w, "Не удалось подключиться: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Подключение вручную — это тоже выбор конфига.
	s.config.SetSelectedConfig(req.ConfigID)
	s.saveConfig()

	jsonResponse(w, map[string]bool{"success": true})
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.vpnManager.Disconnect(); err != nil {
		jsonError(w, "Не удалось отключиться: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Выбор конфига при отключении не сбрасываем: он остаётся выбранным
	// на главном экране и используется для автоподключения.
	jsonResponse(w, map[string]bool{"success": true})
}
