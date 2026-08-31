package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/user/amnezia-web-client/internal/autostart"
	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/version"
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
	wsClients  map[*wsClient]bool
	wsMu       sync.RWMutex
	wsUpgrader websocket.Upgrader
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
			// Без предела рукопожатие может висеть бесконечно, занимая
			// соединение и горутину.
			HandshakeTimeout: 10 * time.Second,
			CheckOrigin: func(r *http.Request) bool {
				return allowedOrigin(r.Header.Get("Origin"))
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

	// Версия сборки: интерфейс показывает её в настройках, а не хранит
	// собственную копию числа, которую некому обновлять.
	api.HandleFunc("/version", s.handleGetVersion).Methods("GET")

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

// allowedOrigin разрешает обращаться к API только страницам, открытым с этой
// же машины.
//
// Раньше здесь стояла звёздочка «разрешить всем», и это была дыра. У API нет
// аутентификации, работает он от root, а GET /api/configs отдаёт конфиги
// вместе с приватными ключами. Со звёздочкой любой сайт, открытый в браузере
// пользователя, мог сделать fetch на 127.0.0.1 И ПРОЧИТАТЬ ОТВЕТ — то есть
// увести ключи от всех подключений, не имея доступа к машине.
//
// Пустой Origin пропускаем: его не шлют запросы не из браузера — сама
// оболочка, curl, тесты.
func allowedOrigin(origin string) bool {
	if origin == "" {
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}

	switch parsed.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

// corsMiddleware отвечает заголовками CORS и отсекает чужие источники.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		if !allowedOrigin(origin) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}

		// nosniff: ответы API — это JSON, и браузер не должен угадывать в них
		// что-то исполняемое.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Страницу управления VPN, работающую от root, нельзя встраивать в
		// чужой документ: невидимый фрейм поверх ссылки — это чужие нажатия
		// на наши кнопки подключения и удаления.
		w.Header().Set("X-Frame-Options", "DENY")

		// Отражаем конкретный источник, а не звёздочку: со звёздочкой
		// браузер разрешил бы читать ответ кому угодно.
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

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

// maxRequestBody ограничивает тело запроса. У API нет аутентификации, и любой
// локальный процесс может послать ему поток без конца: без предела backend,
// работающий от root, съел бы всю память машины. Мегабайта хватает с запасом —
// самая крупная сущность здесь это текст .conf на несколько килобайт.
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
		jsonError(w, "Invalid JSON", http.StatusBadRequest)
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

	s.saveConfig()

	updated := s.config.GetConfig(id)
	if updated == nil {
		// Конфигурацию успели удалить между сохранением и чтением.
		jsonError(w, "Config not found", http.StatusNotFound)
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
	s.saveConfig()

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
	s.saveConfig()

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
		jsonError(w, "Rule not found", http.StatusNotFound)
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
		jsonError(w, "Invalid routing mode", http.StatusBadRequest)
		return
	}

	s.config.SetRoutingMode(req.Mode)
	s.saveConfig()
	s.applyRoutingNow()

	jsonResponse(w, map[string]config.RoutingMode{"mode": req.Mode})
}

// Параметры WebSocket-соединений.
const (
	// wsReadDeadline — сколько ждём хоть что-нибудь от клиента. Обновляется
	// каждым pong, поэтому живое соединение его никогда не достигает, а
	// оборванное (усыплённый ноутбук, закрытая крышка) перестаёт занимать
	// место в списке рассылки.
	wsReadDeadline = 60 * time.Second

	// wsWriteDeadline — предел на отправку одного сообщения. Без него
	// зависший клиент с переполненным окном TCP держал бы рассылку статуса
	// для всех остальных.
	wsWriteDeadline = 10 * time.Second

	// wsPingInterval должен быть заметно меньше wsReadDeadline, иначе клиент
	// не успевает ответить до истечения срока.
	wsPingInterval = 30 * time.Second

	// wsReadLimit — от клиента сюда не приходит ничего, кроме служебных
	// кадров, поэтому предел маленький: он не даёт послать сообщение,
	// которое пришлось бы целиком держать в памяти.
	wsReadLimit = 4 << 10

	// maxWSClients ограничивает число подписчиков. Смотрит на статус один
	// пользователь, максимум из нескольких вкладок; всё сверх этого — либо
	// утечка соединений, либо попытка исчерпать память backend'а.
	maxWSClients = 32
)

// WebSocket handlers
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Место проверяем до рукопожатия: отказ обычным HTTP клиент понимает,
	// а закрытие уже поднятого WebSocket выглядит как обрыв связи и
	// заставляет его переподключаться по кругу.
	s.wsMu.RLock()
	full := len(s.wsClients) >= maxWSClients
	s.wsMu.RUnlock()

	if full {
		jsonError(w, "Слишком много подписчиков", http.StatusServiceUnavailable)
		return
	}

	conn, err := s.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade уже ответил клиенту сам.
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	client := &wsClient{conn: conn}

	s.wsMu.Lock()
	s.wsClients[client] = true
	s.wsMu.Unlock()

	// Send current status
	s.sendWSMessage(client, "status", s.vpnManager.GetStatus())

	// Keep connection alive
	go s.wsReader(client)
}

// wsReader держит соединение открытым и убирает клиента, когда оно порвалось.
// Ничего из присланного не читается: канал односторонний, команды приходят
// обычными запросами HTTP.
func (s *Server) wsReader(client *wsClient) {
	defer s.dropClient(client)

	client.conn.SetReadLimit(wsReadLimit)
	client.conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
	client.conn.SetPongHandler(func(string) error {
		return client.conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
	})

	for {
		if _, _, err := client.conn.ReadMessage(); err != nil {
			return
		}
	}
}

// dropClient убирает клиента из рассылки и закрывает соединение.
// Повторный вызов безопасен.
func (s *Server) dropClient(client *wsClient) {
	s.wsMu.Lock()
	delete(s.wsClients, client)
	s.wsMu.Unlock()

	client.conn.Close()
}

// clients возвращает снимок списка подписчиков. Рассылка идёт по копии, а не
// под блокировкой: отправка может застрять на десять секунд, и всё это время
// никто не смог бы ни подключиться, ни отвалиться.
func (s *Server) clients() []*wsClient {
	s.wsMu.RLock()
	defer s.wsMu.RUnlock()

	list := make([]*wsClient, 0, len(s.wsClients))
	for client := range s.wsClients {
		list = append(list, client)
	}
	return list
}

// sendWSMessage отправляет одно сообщение. Клиент, которому не удалось
// написать, отключается: его соединение уже нерабочее, и следующая рассылка
// снова упёрлась бы в тот же таймаут.
func (s *Server) sendWSMessage(client *wsClient, msgType string, data interface{}) {
	msg := map[string]interface{}{
		"type": msgType,
		"data": data,
	}

	client.mu.Lock()
	client.conn.SetWriteDeadline(time.Now().Add(wsWriteDeadline))
	err := client.conn.WriteJSON(msg)
	client.mu.Unlock()

	if err != nil {
		s.dropClient(client)
	}
}

func (s *Server) broadcastStatus(status vpn.ConnectionStatus) {
	for _, client := range s.clients() {
		s.sendWSMessage(client, "status", status)
	}
}

// StartPingLoop starts a goroutine that pings all WebSocket clients periodically
func (s *Server) StartPingLoop() {
	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()

		for range ticker.C {
			for _, client := range s.clients() {
				client.mu.Lock()
				client.conn.SetWriteDeadline(time.Now().Add(wsWriteDeadline))
				err := client.conn.WriteMessage(websocket.PingMessage, nil)
				client.mu.Unlock()

				if err != nil {
					s.dropClient(client)
				}
			}
		}
	}()
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
	if !decodeJSON(w, r, &settings) {
		return
	}

	s.config.SetSettings(settings)

	if err := s.config.Save(); err != nil {
		jsonError(w, "Failed to save settings", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, settings)
}
