package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/mux"
	"github.com/user/amnezia-web-client/internal/autostart"
	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/version"
	"github.com/user/amnezia-web-client/internal/vpn"
)

// Server is the API server
type Server struct {
	router     *mux.Router
	handler    http.Handler
	config     *config.AppConfig
	vpnManager *vpn.Manager
	autostart  *autostart.Manager

	// Подписчики на поток изменений статуса — см. events.go.
	eventClients map[*eventClient]bool
	eventsMu     sync.RWMutex
}

// NewServer creates a new API server
func NewServer(cfg *config.AppConfig, vpnMgr *vpn.Manager, autostartMgr *autostart.Manager) *Server {
	s := &Server{
		router:       mux.NewRouter(),
		config:       cfg,
		vpnManager:   vpnMgr,
		autostart:    autostartMgr,
		eventClients: make(map[*eventClient]bool),
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

	// Поток изменений статуса
	api.HandleFunc("/vpn/events", s.handleEvents).Methods("GET")

	// Замер задержки до сервера VPN. Параметров нет: активный конфиг и
	// состояние подключения бэкенд знает сам, и цель выбирает по ним.
	api.HandleFunc("/ping", s.handlePing).Methods("GET")

	// Версия сборки: интерфейс показывает её в настройках, а не хранит
	// собственную копию числа, которую некому обновлять.
	api.HandleFunc("/version", s.handleGetVersion).Methods("GET")

	// Settings
	api.HandleFunc("/settings", s.handleGetSettings).Methods("GET")
	api.HandleFunc("/settings", s.handleSetSettings).Methods("PUT")

	// Автозапуск оболочки при входе в систему
	api.HandleFunc("/autostart", s.handleGetAutostart).Methods("GET")
	api.HandleFunc("/autostart", s.handleSetAutostart).Methods("PUT")

	// Блокировка трафика мимо туннеля
	api.HandleFunc("/kill-switch", s.handleGetKillSwitch).Methods("GET")
	api.HandleFunc("/kill-switch", s.handleSetKillSwitch).Methods("PUT")

	// Конфиг, выбранный на главном экране (к нему идёт автоподключение)
	api.HandleFunc("/selected-config", s.handleGetSelectedConfig).Methods("GET")
	api.HandleFunc("/selected-config", s.handleSetSelectedConfig).Methods("PUT")

	// Ни проверки доступа, ни CORS: API живёт на unix-сокете с правами 0600,
	// и до него дотягивается только владелец. Промежуточных слоёв не
	// осталось вовсе — роутер и есть обработчик.
	s.handler = s.router

	// Неизвестный путь — ошибка запроса, и ответ на неё обязан быть JSON:
	// клиент разбирает любой ответ как JSON и на HTML упал бы.
	s.router.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonError(w, "Неизвестный эндпоинт", http.StatusNotFound)
	})
	s.router.MethodNotAllowedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonError(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
	})
}

// ServeHTTP implements http.Handler
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
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

// maxRequestBody ограничивает тело запроса. Клиент здесь свой, но ошибиться
// он может так же, как чужой: поток без конца съел бы всю память процесса,
// работающего от root. Мегабайта хватает с запасом — самая крупная сущность
// здесь это текст .conf на несколько килобайт.
const maxRequestBody = 1 << 20

// decodeJSON разбирает тело запроса и сам отвечает на ошибку. false означает,
// что ответ уже отправлен и обработчику остаётся только выйти.
//
// Незнакомые поля игнорируются намеренно: сюда попадают и файлы правил,
// выгруженные другими версиями, — отвергать их из-за лишнего ключа значило бы
// ломать перенос настроек между сборками.
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))

	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			jsonError(w, "Запрос слишком большой", http.StatusRequestEntityTooLarge)
			return false
		}
		jsonError(w, "Тело запроса не разобрано как JSON", http.StatusBadRequest)
		return false
	}

	return true
}

// JSON response helpers
func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		// Заголовки уже ушли, менять код ответа поздно — остаётся запись в лог.
		log.Printf("Не удалось отправить ответ: %v", err)
	}
}

func jsonError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// saveConfig сохраняет состояние и сообщает о неудаче в лог.
//
// Отдельный помощник, потому что вызывается из десятка обработчиков: раньше
// каждый писал ту же пару строк, и один из них молча ронял ошибку на пол.
func (s *Server) saveConfig() {
	if err := s.config.Save(); err != nil {
		log.Printf("Не удалось сохранить настройки: %v", err)
	}
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

	jsonResponse(w, updated)
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

	jsonResponse(w, cfg)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	cfg := s.config.GetConfig(id)
	if cfg == nil {
		jsonError(w, "Конфигурация не найдена", http.StatusNotFound)
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

// Routing handlers
func (s *Server) handleGetRouting(w http.ResponseWriter, r *http.Request) {
	routing := s.config.GetRouting()
	jsonResponse(w, routing)
}

func (s *Server) handleSetRouting(w http.ResponseWriter, r *http.Request) {
	var routing config.RoutingConfig

	if !decodeJSON(w, r, &routing) {
		return
	}

	// Сюда приходит и загруженный пользователем файл, поэтому содержимое
	// проверяем целиком, а не полагаемся на то, что его собрал интерфейс.
	if err := routing.Validate(); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.config.SetRouting(routing)
	s.saveConfig()
	s.applyRoutingNow()

	jsonResponse(w, routing)
}

func (s *Server) handleAddRoutingRule(w http.ResponseWriter, r *http.Request) {
	var rule config.RoutingRule

	if !decodeJSON(w, r, &rule) {
		return
	}

	if err := rule.Validate(); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	rule.ID = config.GenerateID()
	rule.Enabled = true

	s.config.AddRoutingRule(rule)
	s.saveConfig()
	s.applyRoutingNow()

	jsonResponse(w, rule)
}

func (s *Server) handleDeleteRoutingRule(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	if !s.config.DeleteRoutingRule(id) {
		jsonError(w, "Правило не найдено", http.StatusNotFound)
		return
	}

	s.saveConfig()
	s.applyRoutingNow()

	jsonResponse(w, map[string]bool{"success": true})
}

func (s *Server) handleSetRoutingMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode config.RoutingMode `json:"mode"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	if !config.ValidRoutingMode(req.Mode) {
		jsonError(w, "Неизвестный режим маршрутизации", http.StatusBadRequest)
		return
	}

	s.config.SetRoutingMode(req.Mode)
	s.saveConfig()
	s.applyRoutingNow()

	jsonResponse(w, map[string]config.RoutingMode{"mode": req.Mode})
}

func (s *Server) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]string{"version": version.Value})
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

	if !decodeJSON(w, r, &req) {
		return
	}

	if err := s.autostart.SetEnabled(req.Enabled); err != nil {
		jsonError(w, "Не удалось изменить автозапуск: "+err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, s.autostart.State())
}

func (s *Server) handleGetKillSwitch(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, s.vpnManager.KillSwitchState())
}

func (s *Server) handleSetKillSwitch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	// Сохраняем до применения: настройка обязана пережить перезапуск, даже
	// если применить её прямо сейчас не к чему.
	settings := s.config.GetSettings()
	settings.KillSwitch = req.Enabled
	s.config.SetSettings(settings)

	if err := s.config.Save(); err != nil {
		jsonError(w, "Не удалось сохранить настройку", http.StatusInternalServerError)
		return
	}

	s.vpnManager.SetKillSwitchEnabled(req.Enabled)

	jsonResponse(w, s.vpnManager.KillSwitchState())
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

func (s *Server) handleSetSettings(w http.ResponseWriter, r *http.Request) {
	var settings config.AppSettings
	if !decodeJSON(w, r, &settings) {
		return
	}

	// Блокировка живёт за своим эндпоинтом: у неё есть доступность и
	// применение на живом туннеле. Сюда она попадает только в ответе, и
	// перезаписать её случайным PUT настроек нельзя.
	settings.KillSwitch = s.config.GetSettings().KillSwitch

	s.config.SetSettings(settings)

	if err := s.config.Save(); err != nil {
		jsonError(w, "Не удалось сохранить настройки", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, settings)
}
