package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/user/amnezia-web-client/internal/autostart"
	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/vpn"
)

// wsClient wraps a websocket connection with its own write mutex
type wsClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

// Server is the API server
type Server struct {
	router     *mux.Router
	handler    http.Handler
	config     *config.AppConfig
	vpnManager *vpn.Manager
	autostart  *autostart.Manager
	
	// WebSocket connections for real-time updates
	wsClients   map[*wsClient]bool
	wsMu        sync.RWMutex
	wsUpgrader  websocket.Upgrader
}

// NewServer creates a new API server
func NewServer(cfg *config.AppConfig, vpnMgr *vpn.Manager, autostartMgr *autostart.Manager) *Server {
	s := &Server{
		router:     mux.NewRouter(),
		config:     cfg,
		vpnManager: vpnMgr,
		autostart:  autostartMgr,
		wsClients:  make(map[*wsClient]bool),
		wsUpgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for development
			},
		},
	}
	
	s.setupRoutes()
	
	// Register for VPN status updates
	vpnMgr.OnStatusChange(func(status vpn.ConnectionStatus) {
		s.broadcastStatus(status)
	})
	
	return s
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	api := s.router.PathPrefix("/api").Subrouter()
	
	// Configs
	api.HandleFunc("/configs", s.handleGetConfigs).Methods("GET")
	api.HandleFunc("/configs", s.handleAddConfig).Methods("POST")
	api.HandleFunc("/configs/{id}", s.handleGetConfig).Methods("GET")
	api.HandleFunc("/configs/{id}", s.handleUpdateConfig).Methods("PUT")
	api.HandleFunc("/configs/{id}", s.handleDeleteConfig).Methods("DELETE")
	
	// VPN Control
	api.HandleFunc("/vpn/status", s.handleGetStatus).Methods("GET")
	api.HandleFunc("/vpn/connect", s.handleConnect).Methods("POST")
	api.HandleFunc("/vpn/disconnect", s.handleDisconnect).Methods("POST")
	
	// Routing
	api.HandleFunc("/routing", s.handleGetRouting).Methods("GET")
	api.HandleFunc("/routing", s.handleSetRouting).Methods("PUT")
	api.HandleFunc("/routing/rules", s.handleAddRoutingRule).Methods("POST")
	api.HandleFunc("/routing/rules/{id}", s.handleDeleteRoutingRule).Methods("DELETE")
	api.HandleFunc("/routing/mode", s.handleSetRoutingMode).Methods("PUT")
	
	// WebSocket for real-time updates
	api.HandleFunc("/ws", s.handleWebSocket)
	
	// Замер задержки до сервера VPN. Параметров нет: активный конфиг и
	// состояние подключения бэкенд знает сам, и цель выбирает по ним.
	api.HandleFunc("/ping", s.handlePing).Methods("GET")
	
	// Settings
	api.HandleFunc("/settings", s.handleGetSettings).Methods("GET")
	api.HandleFunc("/settings", s.handleSetSettings).Methods("PUT")
	
	// Автозапуск оболочки при входе в систему
	api.HandleFunc("/autostart", s.handleGetAutostart).Methods("GET")
	api.HandleFunc("/autostart", s.handleSetAutostart).Methods("PUT")
	
	// Конфиг, выбранный на главном экране (к нему идёт автоподключение)
	api.HandleFunc("/selected-config", s.handleGetSelectedConfig).Methods("GET")
	api.HandleFunc("/selected-config", s.handleSetSelectedConfig).Methods("PUT")
	
	// CORS оборачивает роутер целиком, а не подключается через router.Use:
	// mux вызывает middleware ТОЛЬКО для совпавших маршрутов. Маршрутов на
	// OPTIONS здесь нет, поэтому preflight-запрос уходил в NotFoundHandler и
	// отдавал 404 без Access-Control-Allow-Origin — браузер блокировал всё.
	s.handler = corsMiddleware(s.router)
}

// ServeHTTP implements http.Handler
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// corsMiddleware adds CORS headers
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		
		// Preflight завершаем здесь: до роутера он всё равно не дойдёт.
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		
		next.ServeHTTP(w, r)
	})
}

// applyRoutingNow пересобирает маршруты на живом туннеле сразу после
// изменения правил. Если VPN не подключён, менеджер ничего не делает —
// правила вступят в силу при следующем подключении.
func (s *Server) applyRoutingNow() {
	routing := s.config.GetRouting()
	if err := s.vpnManager.ApplyRouting(&routing); err != nil {
		log.Printf("Failed to apply routing: %v", err)
	}
}

// JSON response helpers
func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// Config handlers
func (s *Server) handleGetConfigs(w http.ResponseWriter, r *http.Request) {
	configs := s.config.GetAllConfigs()
	jsonResponse(w, configs)
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	
	var req struct {
		Name      string `json:"name"`
		RawConfig string `json:"raw_config"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	
	if req.RawConfig == "" {
		jsonError(w, "Config is required", http.StatusBadRequest)
		return
	}
	
	// Правку подключённого конфига запрещаем: туннель уже поднят по старым
	// параметрам, и содержимое разъехалось бы с тем, что работает.
	if status := s.vpnManager.GetStatus(); status.ConfigID == id && status.State != vpn.StateDisconnected {
		jsonError(w, "Cannot edit a config while it is in use", http.StatusConflict)
		return
	}
	
	cfg, err := config.ParseAmneziaConfig(req.Name, req.RawConfig)
	if err != nil {
		jsonError(w, "Failed to parse config: "+err.Error(), http.StatusBadRequest)
		return
	}
	
	cfg.Name = s.config.UniqueConfigName(cfg.Name, id)
	
	if !s.config.UpdateConfig(id, *cfg) {
		jsonError(w, "Config not found", http.StatusNotFound)
		return
	}
	
	if err := s.config.Save(); err != nil {
		log.Printf("Failed to save config: %v", err)
	}
	
	jsonResponse(w, s.config.GetConfig(id))
}

func (s *Server) handleAddConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		RawConfig string `json:"raw_config"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	
	if req.RawConfig == "" {
		jsonError(w, "Config is required", http.StatusBadRequest)
		return
	}
	
	// Имя необязательно: парсер выведет его из адреса сервера.
	cfg, err := config.ParseAmneziaConfig(req.Name, req.RawConfig)
	if err != nil {
		jsonError(w, "Failed to parse config: "+err.Error(), http.StatusBadRequest)
		return
	}
	
	cfg.Name = s.config.UniqueConfigName(cfg.Name, "")
	
	s.config.AddConfig(*cfg)
	if err := s.config.Save(); err != nil {
		log.Printf("Failed to save config: %v", err)
	}
	
	jsonResponse(w, cfg)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	
	cfg := s.config.GetConfig(id)
	if cfg == nil {
		jsonError(w, "Config not found", http.StatusNotFound)
		return
	}
	
	jsonResponse(w, cfg)
}

func (s *Server) handleDeleteConfig(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	
	// Check if this config is currently active
	status := s.vpnManager.GetStatus()
	if status.ConfigID == id && status.State != vpn.StateDisconnected {
		jsonError(w, "Cannot delete active config", http.StatusBadRequest)
		return
	}
	
	if !s.config.DeleteConfig(id) {
		jsonError(w, "Config not found", http.StatusNotFound)
		return
	}
	
	if err := s.config.Save(); err != nil {
		log.Printf("Failed to save config: %v", err)
	}
	
	jsonResponse(w, map[string]bool{"success": true})
}

// VPN handlers
func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	status := s.vpnManager.GetStatus()
	jsonResponse(w, status)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConfigID string `json:"config_id"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	
	cfg := s.config.GetConfig(req.ConfigID)
	if cfg == nil {
		jsonError(w, "Config not found", http.StatusNotFound)
		return
	}
	
	routing := s.config.GetRouting()
	
	if err := s.vpnManager.Connect(cfg, &routing); err != nil {
		jsonError(w, "Failed to connect: "+err.Error(), http.StatusInternalServerError)
		return
	}
	
	// Подключение вручную — это тоже выбор конфига.
	s.config.SetSelectedConfig(req.ConfigID)
	s.config.Save()
	
	jsonResponse(w, map[string]bool{"success": true})
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.vpnManager.Disconnect(); err != nil {
		jsonError(w, "Failed to disconnect: "+err.Error(), http.StatusInternalServerError)
		return
	}
	
	// Выбор конфига при отключении не сбрасываем: он остаётся выбранным
	// на главном экране и используется для автоподключения.
	jsonResponse(w, map[string]bool{"success": true})
}

// Routing handlers
func (s *Server) handleGetRouting(w http.ResponseWriter, r *http.Request) {
	routing := s.config.GetRouting()
	jsonResponse(w, routing)
}

func (s *Server) handleSetRouting(w http.ResponseWriter, r *http.Request) {
	var routing config.RoutingConfig
	
	if err := json.NewDecoder(r.Body).Decode(&routing); err != nil {
		jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	
	// Сюда приходит и загруженный пользователем файл, поэтому содержимое
	// проверяем целиком, а не полагаемся на то, что его собрал интерфейс.
	if routing.Mode != config.RoutingModeVPNList && routing.Mode != config.RoutingModeDirectList {
		jsonError(w, "Invalid routing mode", http.StatusBadRequest)
		return
	}
	
	validTypes := map[string]bool{"ip": true, "cidr": true, "domain": true, "zone": true}
	seen := make(map[string]bool, len(routing.Rules))
	
	for i := range routing.Rules {
		rule := &routing.Rules[i]
		
		if !validTypes[rule.Type] {
			jsonError(w, "Invalid rule type: "+rule.Type, http.StatusBadRequest)
			return
		}
		
		if rule.Value == "" {
			jsonError(w, "Rule value is required", http.StatusBadRequest)
			return
		}
		
		// В чужом файле идентификаторов может не быть или они могут
		// повторяться — тогда удаление одного правила убирало бы соседнее.
		if rule.ID == "" || seen[rule.ID] {
			rule.ID = config.GenerateID()
		}
		seen[rule.ID] = true
	}
	
	s.config.SetRouting(routing)
	if err := s.config.Save(); err != nil {
		log.Printf("Failed to save config: %v", err)
	}
	s.applyRoutingNow()
	
	jsonResponse(w, routing)
}

func (s *Server) handleAddRoutingRule(w http.ResponseWriter, r *http.Request) {
	var rule config.RoutingRule
	
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	
	// Validate rule type
	validTypes := map[string]bool{"ip": true, "cidr": true, "domain": true, "zone": true}
	if !validTypes[rule.Type] {
		jsonError(w, "Invalid rule type", http.StatusBadRequest)
		return
	}
	
	if rule.Value == "" {
		jsonError(w, "Value is required", http.StatusBadRequest)
		return
	}
	
	rule.ID = config.GenerateID()
	rule.Enabled = true
	
	s.config.AddRoutingRule(rule)
	if err := s.config.Save(); err != nil {
		log.Printf("Failed to save config: %v", err)
	}
	s.applyRoutingNow()
	
	jsonResponse(w, rule)
}

func (s *Server) handleDeleteRoutingRule(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	
	if !s.config.DeleteRoutingRule(id) {
		jsonError(w, "Rule not found", http.StatusNotFound)
		return
	}
	
	if err := s.config.Save(); err != nil {
		log.Printf("Failed to save config: %v", err)
	}
	s.applyRoutingNow()
	
	jsonResponse(w, map[string]bool{"success": true})
}

func (s *Server) handleSetRoutingMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode config.RoutingMode `json:"mode"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	
	if req.Mode != config.RoutingModeVPNList && req.Mode != config.RoutingModeDirectList {
		jsonError(w, "Invalid routing mode", http.StatusBadRequest)
		return
	}
	
	s.config.SetRoutingMode(req.Mode)
	if err := s.config.Save(); err != nil {
		log.Printf("Failed to save config: %v", err)
	}
	s.applyRoutingNow()
	
	jsonResponse(w, map[string]config.RoutingMode{"mode": req.Mode})
}

// WebSocket handlers
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	
	client := &wsClient{conn: conn}
	
	s.wsMu.Lock()
	s.wsClients[client] = true
	s.wsMu.Unlock()
	
	// Send current status
	status := s.vpnManager.GetStatus()
	s.sendWSMessage(client, "status", status)
	
	// Keep connection alive
	go s.wsReader(client)
}

func (s *Server) wsReader(client *wsClient) {
	defer func() {
		s.wsMu.Lock()
		delete(s.wsClients, client)
		s.wsMu.Unlock()
		client.conn.Close()
	}()
	
	client.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	client.conn.SetPongHandler(func(string) error {
		client.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	
	for {
		_, _, err := client.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (s *Server) sendWSMessage(client *wsClient, msgType string, data interface{}) {
	msg := map[string]interface{}{
		"type": msgType,
		"data": data,
	}
	
	client.mu.Lock()
	defer client.mu.Unlock()
	
	client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	client.conn.WriteJSON(msg)
}

func (s *Server) broadcastStatus(status vpn.ConnectionStatus) {
	s.wsMu.RLock()
	clients := make([]*wsClient, 0, len(s.wsClients))
	for client := range s.wsClients {
		clients = append(clients, client)
	}
	s.wsMu.RUnlock()
	
	for _, client := range clients {
		s.sendWSMessage(client, "status", status)
	}
}

// StartPingLoop starts a goroutine that pings all WebSocket clients periodically
func (s *Server) StartPingLoop() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		
		for range ticker.C {
			s.wsMu.RLock()
			clients := make([]*wsClient, 0, len(s.wsClients))
			for client := range s.wsClients {
				clients = append(clients, client)
			}
			s.wsMu.RUnlock()
			
			for _, client := range clients {
				client.mu.Lock()
				client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					client.conn.Close()
				}
				client.mu.Unlock()
			}
		}
	}()
}

// Settings handlers
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings := s.config.GetSettings()
	jsonResponse(w, settings)
}

func (s *Server) handleGetAutostart(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, s.autostart.State())
}

func (s *Server) handleSetAutostart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	
	if err := s.autostart.SetEnabled(req.Enabled); err != nil {
		jsonError(w, "Не удалось изменить автозапуск: "+err.Error(), http.StatusInternalServerError)
		return
	}
	
	jsonResponse(w, s.autostart.State())
}

func (s *Server) handleGetSelectedConfig(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]string{"config_id": s.config.GetSelectedConfigID()})
}

func (s *Server) handleSetSelectedConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConfigID string `json:"config_id"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	
	if req.ConfigID != "" && s.config.GetConfig(req.ConfigID) == nil {
		jsonError(w, "Config not found", http.StatusNotFound)
		return
	}
	
	s.config.SetSelectedConfig(req.ConfigID)
	if err := s.config.Save(); err != nil {
		jsonError(w, "Failed to save settings", http.StatusInternalServerError)
		return
	}
	
	jsonResponse(w, map[string]string{"config_id": req.ConfigID})
}

func (s *Server) handleSetSettings(w http.ResponseWriter, r *http.Request) {
	var settings config.AppSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	
	s.config.SetSettings(settings)
	
	if err := s.config.Save(); err != nil {
		jsonError(w, "Failed to save settings", http.StatusInternalServerError)
		return
	}
	
	jsonResponse(w, settings)
}
