package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/geodata"
	"github.com/user/amnezia-web-client/internal/localguard"
	"github.com/user/amnezia-web-client/internal/profiles"
	"github.com/user/amnezia-web-client/internal/vpn"
)

// Server is the API server
type Server struct {
	router     *mux.Router
	handler    http.Handler
	config     *config.AppConfig
	profiles   *profiles.Catalog
	vpnManager *vpn.Manager

	// Подписчики на поток изменений статуса — см. events.go.
	eventClients map[*eventClient]bool
	eventsMu     sync.RWMutex

	// geo — базы geoip.dat и geosite.dat для правил-наборов, см. geo.go.
	geo *geodata.Store

	// Отложенное применение правил, см. applyRoutingSoon.
	//
	// routingDelay полем, а не константой: иначе проверить схлопывание правок
	// можно было бы только настоящим ожиданием в тесте. applyRouting тоже —
	// менеджер VPN тип конкретный, и без этой щели не увидеть, сколько раз
	// применение вообще случилось.
	routingTimer *time.Timer
	routingMu    sync.Mutex
	routingDelay time.Duration
	applyMu      sync.Mutex
	applyRouting func(*config.RoutingConfig) error
}

// NewServer creates a new API server
func NewServer(cfg *config.AppConfig, catalog *profiles.Catalog, vpnMgr *vpn.Manager, geo *geodata.Store) *Server {
	s := &Server{
		router:       mux.NewRouter(),
		config:       cfg,
		profiles:     catalog,
		vpnManager:   vpnMgr,
		eventClients: make(map[*eventClient]bool),
		geo:          geo,
		routingDelay: routingApplyDelay,
	}
	s.applyRouting = func(routing *config.RoutingConfig) error {
		return vpnMgr.ApplyRouting(routing)
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

	// Протоколы, которые служба умеет разбирать: интерфейс не зашивает их у
	// себя и подписывает конфигурации по этому списку.
	api.HandleFunc("/protocols", s.handleGetProtocols).Methods("GET")

	// VPN Control
	api.HandleFunc("/vpn/status", s.handleGetStatus).Methods("GET")
	api.HandleFunc("/vpn/connect", s.handleConnect).Methods("POST")
	api.HandleFunc("/vpn/disconnect", s.handleDisconnect).Methods("POST")

	// Routing
	api.HandleFunc("/routing", s.handleGetRouting).Methods("GET")
	api.HandleFunc("/routing", s.handleSetRouting).Methods("PUT")
	api.HandleFunc("/routing/rules", s.handleAddRoutingRule).Methods("POST")
	api.HandleFunc("/routing/rules/batch", s.handleBatchRoutingRules).Methods("POST")
	api.HandleFunc("/routing/rules/{id}", s.handleDeleteRoutingRule).Methods("DELETE")
	api.HandleFunc("/routing/mode", s.handleSetRoutingMode).Methods("PUT")

	// Базы geoip/geosite: состояние, скачивание и загрузка файлом.
	api.HandleFunc("/apps", s.handleGetApps).Methods("GET")
	api.HandleFunc("/apps/icon/{key}", s.handleGetAppIcon).Methods("GET")

	api.HandleFunc("/geo", s.handleGetGeo).Methods("GET")
	api.HandleFunc("/geo/{kind}/update", s.handleUpdateGeo).Methods("POST")
	api.HandleFunc("/geo/{kind}", s.handleUploadGeo).Methods("PUT")

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

	// Конфиг, выбранный на главном экране (к нему идёт автоподключение)
	api.HandleFunc("/selected-config", s.handleGetSelectedConfig).Methods("GET")
	api.HandleFunc("/selected-config", s.handleSetSelectedConfig).Methods("PUT")

	// API слушает петлевой адрес, и браузер на этой же машине дотянется до
	// него с любой открытой страницы. CORS здесь нет намеренно, а защита
	// отсекает то, что CORS не закрывает: см. localguard.
	s.handler = localguard.Wrap(s.router)

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
