package api

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/vpn"
)

// Config handlers
func (s *Server) handleGetConfigs(w http.ResponseWriter, r *http.Request) {
	configs := s.config.GetAllConfigs()
	jsonResponse(w, newConfigViews(configs))
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	var req struct {
		Name      string `json:"name"`
		RawConfig string `json:"raw_config"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	if req.RawConfig == "" {
		jsonError(w, "Вставьте содержимое .conf файла", http.StatusBadRequest)
		return
	}

	// Правку подключённого конфига запрещаем: туннель уже поднят по старым
	// параметрам, и содержимое разъехалось бы с тем, что работает.
	if status := s.vpnManager.GetStatus(); status.ConfigID == id && status.State != vpn.StateDisconnected {
		jsonError(w, "Нельзя менять конфигурацию, пока она подключена", http.StatusConflict)
		return
	}

	cfg, err := config.ParseAmneziaConfig(req.Name, req.RawConfig)
	if err != nil {
		jsonError(w, "Не удалось разобрать конфигурацию: "+err.Error(), http.StatusBadRequest)
		return
	}

	cfg.Name = s.config.UniqueConfigName(cfg.Name, id)

	if !s.config.UpdateConfig(id, *cfg) {
		jsonError(w, "Конфигурация не найдена", http.StatusNotFound)
		return
	}

	s.saveConfig()

	updated := s.config.GetConfig(id)
	if updated == nil {
		// Конфигурацию успели удалить между сохранением и чтением.
		jsonError(w, "Конфигурация не найдена", http.StatusNotFound)
		return
	}

	jsonResponse(w, newConfigView(updated))
}

func (s *Server) handleAddConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		RawConfig string `json:"raw_config"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	if req.RawConfig == "" {
		jsonError(w, "Вставьте содержимое .conf файла", http.StatusBadRequest)
		return
	}

	// Имя необязательно: парсер выведет его из адреса сервера.
	cfg, err := config.ParseAmneziaConfig(req.Name, req.RawConfig)
	if err != nil {
		jsonError(w, "Не удалось разобрать конфигурацию: "+err.Error(), http.StatusBadRequest)
		return
	}

	cfg.Name = s.config.UniqueConfigName(cfg.Name, "")

	s.config.AddConfig(*cfg)
	s.saveConfig()

	jsonResponse(w, newConfigView(cfg))
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	cfg := s.config.GetConfig(id)
	if cfg == nil {
		jsonError(w, "Конфигурация не найдена", http.StatusNotFound)
		return
	}

	jsonResponse(w, newConfigDetail(cfg))
}

func (s *Server) handleDeleteConfig(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	// Check if this config is currently active
	status := s.vpnManager.GetStatus()
	if status.ConfigID == id && status.State != vpn.StateDisconnected {
		jsonError(w, "Нельзя удалить подключённую конфигурацию", http.StatusBadRequest)
		return
	}

	if !s.config.DeleteConfig(id) {
		jsonError(w, "Конфигурация не найдена", http.StatusNotFound)
		return
	}

	s.saveConfig()

	jsonResponse(w, map[string]bool{"success": true})
}

func (s *Server) handleGetSelectedConfig(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]string{"config_id": s.config.GetSelectedConfigID()})
}

func (s *Server) handleSetSelectedConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConfigID string `json:"config_id"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	if req.ConfigID != "" && s.config.GetConfig(req.ConfigID) == nil {
		jsonError(w, "Конфигурация не найдена", http.StatusNotFound)
		return
	}

	s.config.SetSelectedConfig(req.ConfigID)
	if err := s.config.Save(); err != nil {
		jsonError(w, "Не удалось сохранить настройки", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]string{"config_id": req.ConfigID})
}
