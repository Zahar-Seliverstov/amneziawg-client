package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/user/amnezia-web-client/internal/api"
	"github.com/user/amnezia-web-client/internal/auth"
	"github.com/user/amnezia-web-client/internal/autostart"
	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/desktopuser"
	"github.com/user/amnezia-web-client/internal/version"
	"github.com/user/amnezia-web-client/internal/vpn"
)

// Тайминги HTTP-сервера.
const (
	// readHeaderTimeout — предел на чтение заголовков запроса. Без него
	// соединение, открытое и замолчавшее, занимает горутину и файловый
	// дескриптор до бесконечности: классический медленный отказ в
	// обслуживании, устроить который здесь может любой локальный процесс.
	readHeaderTimeout = 10 * time.Second

	// idleTimeout закрывает keep-alive соединения, которыми больше не
	// пользуются.
	idleTimeout = 2 * time.Minute

	// Общего WriteTimeout здесь намеренно нет: он обрывал бы и WebSocket,
	// который живёт всё время работы приложения. Предельные сроки на запись
	// стоят на самих кадрах WebSocket, в пакете api.

	// shutdownTimeout — сколько ждём завершения запросов при остановке.
	shutdownTimeout = 5 * time.Second

	// autoconnectDelay даёт серверу начать слушать до того, как поднимется
	// туннель: интерфейс, открытый сразу после запуска, должен увидеть уже
	// идущее подключение, а не пустой ответ.
	autoconnectDelay = 500 * time.Millisecond

	// parentPollInterval — как часто проверяем, жива ли оболочка.
	parentPollInterval = 2 * time.Second
)

func main() {
	// Parse flags
	port := flag.Int("port", 8080, "HTTP server port")
	// Только петлевой интерфейс: у API нет аутентификации, работает он от
	// root и отдаёт конфиги с приватными ключами. Значение по умолчанию
	// 0.0.0.0 открывало всё это любому в локальной сети.
	host := flag.String("host", "127.0.0.1", "HTTP server host")
	configPath := flag.String("config", "", "Path to config file (default: ~/.config/awg-client/config.json)")
	webDir := flag.String("web", "", "Directory with built web UI (default: UI embedded at build time)")
	parentPID := flag.Int("parent-pid", 0, "Exit when this PID disappears (used by the desktop shell)")
	desktopExe := flag.String("desktop-exe", "", "Path to the desktop shell binary (used for the autostart entry)")
	showVersion := flag.Bool("version", false, "Print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Value)
		return
	}

	log.Printf("AWG Client %s", version.Value)

	resolvedPath, err := resolveConfigPath(*configPath)
	if err != nil {
		log.Fatalf("Failed to determine config path: %v", err)
	}
	log.Printf("Using config file: %s", resolvedPath)

	warnIfExposed(*host)

	// Load configuration
	appConfig := config.NewAppConfig(resolvedPath)
	if err := appConfig.Load(); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create VPN manager
	vpnManager := vpn.NewManager()

	// Настройка блокировки переживает перезапуск, поэтому менеджер должен
	// узнать о ней до первого подключения — включая автоподключение ниже.
	vpnManager.SetKillSwitchEnabled(appConfig.GetSettings().KillSwitch)

	// Автозапуск оболочки: ярлык лежит в доме пользователя, а backend работает
	// от root — поэтому менеджеру нужен путь конфига, по владельцу которого он
	// опознаёт пользователя рабочего стола.
	autostartMgr := autostart.NewManager(resolvedPath, *desktopExe)

	// Токен доступа. Рождается до того, как откроется хоть один сокет:
	// иначе между началом приёма запросов и появлением проверки была бы
	// щель, в которую пролезает кто угодно.
	token, err := newToken(resolvedPath)
	if err != nil {
		log.Fatalf("Не удалось подготовить токен доступа: %v", err)
	}

	// Create API server
	server := api.NewServer(appConfig, vpnManager, autostartMgr, token)
	server.StartPingLoop()

	autoconnect(appConfig, vpnManager)

	// Статический UI на том же порту: фронтенд обращается к API по адресу
	// страницы, поэтому один origin — обязательное условие для оболочки.
	if err := server.SetupStatic(*webDir); err != nil {
		log.Printf("Web UI не подключён (%v) — доступен только API", err)
	} else {
		log.Printf("Web UI подключён")
	}

	// Backend поднимают через pkexec, и убить его от имени пользователя уже
	// нельзя. Поэтому он сам следит за оболочкой: та исчезла — гасимся,
	// штатно разбирая VPN-соединение.
	if *parentPID > 0 {
		go watchParent(*parentPID)
	}

	log.Printf("Starting API server on %s:%d", *host, *port)
	log.Printf("Интерфейс: http://127.0.0.1:%d/?%s=%s", *port, auth.QueryParam, token.Value())

	if err := serve(*host, *port, server, vpnManager); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// newToken готовит секрет доступа и кладёт его в файл, читаемый только
// пользователем рабочего стола.
//
// Backend работает от root, поэтому файл ему же и принадлежал бы: оболочка и
// диагностика, работающие от пользователя, не смогли бы его прочитать, а
// значит и обратиться к API.
func newToken(configPath string) (*auth.Token, error) {
	token, err := auth.New()
	if err != nil {
		return nil, err
	}

	path := auth.FilePath(configPath)
	if err := token.Save(path, desktopuser.Resolve(configPath)); err != nil {
		return nil, fmt.Errorf("не удалось записать %s: %w", path, err)
	}

	log.Printf("Токен доступа записан в %s", path)
	return token, nil
}

// resolveConfigPath доводит путь конфига до пригодного к использованию:
// подставляет значение по умолчанию и создаёт каталог.
func resolveConfigPath(path string) (string, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("не удалось определить домашний каталог: %w", err)
		}
		path = filepath.Join(home, ".config", "awg-client", "config.json")
	}

	// 0700: внутри лежат приватные ключи всех подключений.
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", fmt.Errorf("не удалось создать каталог настроек: %w", err)
	}

	return path, nil
}

// warnIfExposed предупреждает о запуске на адресе, доступном извне машины.
//
// Запрещать не стали — сценарии вроде проброса через ssh существуют, — но
// молчать здесь нельзя: у API нет аутентификации, работает он от root, и
// GET /api/configs отдаёт приватные ключи всех подключений.
func warnIfExposed(host string) {
	switch host {
	case "localhost", "127.0.0.1", "::1", "":
		return
	}

	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return
	}

	log.Printf("ВНИМАНИЕ: API открыт на %s, а не только на этой машине. "+
		"У него нет аутентификации, и он отдаёт приватные ключи всех конфигураций — "+
		"любой, кто дотянется до этого адреса, получит их и сможет управлять VPN", host)
}

// autoconnect подключается к конфигу, выбранному на главном экране.
//
// Если выбирать нечего (конфигов ещё нет), просто ничего не делаем — настройка
// остаётся включённой и сработает при следующем запуске.
func autoconnect(appConfig *config.AppConfig, vpnManager *vpn.Manager) {
	id := appConfig.GetAutoconnectConfigID()
	if id == "" {
		if appConfig.GetSettings().Autoconnect {
			log.Printf("Autoconnect включён, но конфиг не выбран — пропускаем")
		}
		return
	}

	cfg := appConfig.GetConfig(id)
	if cfg == nil {
		return
	}

	log.Printf("Autoconnecting to %s...", cfg.Name)

	// Копию правил берём через геттер: поле Routing защищено мьютексом
	// конфига, а прямое обращение к нему из горутины — гонка с любым
	// изменением правил из интерфейса.
	routing := appConfig.GetRouting()

	go func() {
		time.Sleep(autoconnectDelay)
		if err := vpnManager.Connect(cfg, &routing); err != nil {
			log.Printf("Autoconnect failed: %v", err)
		}
	}()
}

// watchParent завершает процесс, когда родительская оболочка исчезла.
func watchParent(pid int) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		log.Printf("Не удалось следить за процессом %d: %v", pid, err)
		return
	}

	ticker := time.NewTicker(parentPollInterval)
	defer ticker.Stop()

	for range ticker.C {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			log.Printf("Родительский процесс %d завершился — выходим", pid)
			// SIGTERM самому себе: в обработчике уже написано корректное
			// отключение VPN и снятие маршрутов.
			syscall.Kill(os.Getpid(), syscall.SIGTERM)
			return
		}
	}
}

// serve обслуживает запросы до сигнала завершения и корректно всё разбирает.
//
// Порядок при остановке важен: сначала перестаём принимать новые запросы,
// потом разбираем туннель. Наоборот — значит дать интерфейсу поднять его
// заново ровно между двумя шагами.
func serve(host string, port int, handler http.Handler, vpnManager *vpn.Manager) error {
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          log.Default(),
	}

	listeners, err := listen(host, port)
	if err != nil {
		return err
	}

	// Буфер на каждый слушатель: горутина обязана суметь отдать ошибку и
	// завершиться, даже если её никто уже не читает.
	errCh := make(chan error, len(listeners))
	for _, ln := range listeners {
		go func(ln net.Listener) {
			errCh <- server.Serve(ln)
		}(ln)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		// Serve вернулся сам — это отказ: сокет закрыли извне, кончились
		// дескрипторы. Туннель всё равно разбираем, иначе он останется
		// поднятым без единого способа им управлять.
		shutdownVPN(vpnManager)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err

	case sig := <-signals:
		log.Printf("Получен сигнал %s — завершаемся", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Сервер остановлен принудительно: %v", err)
	}

	shutdownVPN(vpnManager)
	return nil
}

// shutdownVPN разбирает туннель, если он поднят: снимает маршруты и
// возвращает системе прежние серверы имён.
func shutdownVPN(vpnManager *vpn.Manager) {
	if vpnManager.GetStatus().State == vpn.StateDisconnected {
		return
	}

	log.Println("Disconnecting VPN...")
	if err := vpnManager.Disconnect(); err != nil {
		log.Printf("Не удалось корректно отключить VPN: %v", err)
	}
}

// listen открывает слушающие сокеты.
//
// Для loopback открываются ОБА адреса — 127.0.0.1 и ::1. Это важно: в браузере
// "localhost" часто резолвится в ::1 первым, и запрос к сокету, открытому
// только на IPv4, не доходит вообще (в вебе это видно как NetworkError).
func listen(host string, port int) ([]net.Listener, error) {
	hosts := []string{host}
	switch host {
	case "localhost", "127.0.0.1", "::1":
		hosts = []string{"127.0.0.1", "::1"}
	}

	var listeners []net.Listener

	for _, h := range hosts {
		addr := net.JoinHostPort(h, strconv.Itoa(port))

		ln, err := net.Listen("tcp", addr)
		if err != nil {
			// Единственный адрес не открылся — это фатально.
			if len(hosts) == 1 {
				return nil, err
			}
			// Один из loopback-адресов может отсутствовать (например, IPv6
			// выключен в ядре) — этого достаточно, чтобы продолжить.
			log.Printf("Skipping %s: %v", addr, err)
			continue
		}

		listeners = append(listeners, ln)
		log.Printf("Listening on http://%s", addr)
	}

	if len(listeners) == 0 {
		return nil, fmt.Errorf("no listening socket could be opened on port %d", port)
	}

	return listeners, nil
}
