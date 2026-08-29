package main

import (
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
	"github.com/user/amnezia-web-client/internal/autostart"
	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/vpn"
)

func main() {
	// Parse flags
	port := flag.Int("port", 8080, "HTTP server port")
	host := flag.String("host", "0.0.0.0", "HTTP server host (0.0.0.0 for all interfaces)")
	configPath := flag.String("config", "", "Path to config file (default: ~/.config/awg-client/config.json)")
	webDir := flag.String("web", "", "Directory with built web UI (default: UI embedded at build time)")
	parentPID := flag.Int("parent-pid", 0, "Exit when this PID disappears (used by the desktop shell)")
	desktopExe := flag.String("desktop-exe", "", "Path to the desktop shell binary (used for the autostart entry)")
	flag.Parse()
	
	// Determine config path
	if *configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Failed to get home directory: %v", err)
		}
		*configPath = filepath.Join(home, ".config", "awg-client", "config.json")
	}
	
	// Ensure config directory exists
	configDir := filepath.Dir(*configPath)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		log.Fatalf("Failed to create config directory: %v", err)
	}
	
	log.Printf("Using config file: %s", *configPath)
	
	// Load configuration
	appConfig := config.NewAppConfig(*configPath)
	if err := appConfig.Load(); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	
	// Create VPN manager
	vpnManager := vpn.NewManager()
	
	// Автозапуск оболочки: ярлык лежит в доме пользователя, а backend работает
	// от root — поэтому менеджеру нужен путь конфига, по владельцу которого он
	// опознаёт пользователя рабочего стола.
	autostartMgr := autostart.NewManager(*configPath, *desktopExe)
	
	// Create API server
	server := api.NewServer(appConfig, vpnManager, autostartMgr)
	server.StartPingLoop()
	
	// Автоподключение: подключаемся к конфигу, выбранному на главном экране.
	// Если выбирать нечего (конфигов ещё нет), просто ничего не делаем —
	// настройка остаётся включённой и сработает при следующем запуске.
	if autoConnectID := appConfig.GetAutoconnectConfigID(); autoConnectID != "" {
		cfg := appConfig.GetConfig(autoConnectID)
		if cfg != nil {
			log.Printf("Autoconnecting to %s...", cfg.Name)
			go func() {
				// Small delay to let the server start
				time.Sleep(500 * time.Millisecond)
				if err := vpnManager.Connect(cfg, &appConfig.Routing); err != nil {
					log.Printf("Autoconnect failed: %v", err)
				}
			}()
		}
	} else if appConfig.GetSettings().Autoconnect {
		log.Printf("Autoconnect включён, но конфиг не выбран — пропускаем")
	}

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
	
	// Start HTTP server
	log.Printf("Starting API server on %s:%d", *host, *port)
	
	// Handle shutdown gracefully
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		
		log.Println("Shutting down...")
		
		// Disconnect VPN if connected
		status := vpnManager.GetStatus()
		if status.State != vpn.StateDisconnected {
			log.Println("Disconnecting VPN...")
			vpnManager.Disconnect()
		}
		
		os.Exit(0)
	}()
	
	if err := listenAndServe(*host, *port, server); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// watchParent завершает процесс, когда родительская оболочка исчезла.
func watchParent(pid int) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		log.Printf("Не удалось следить за процессом %d: %v", pid, err)
		return
	}

	for range time.Tick(2 * time.Second) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			log.Printf("Родительский процесс %d завершился — выходим", pid)
			// SIGTERM самому себе: в обработчике уже написано корректное
			// отключение VPN и снятие маршрутов.
			syscall.Kill(os.Getpid(), syscall.SIGTERM)
			return
		}
	}
}

// listenAndServe открывает слушающие сокеты и обслуживает их одним handler'ом.
//
// Для loopback поднимаются ОБА адреса — 127.0.0.1 и ::1. Это важно: в браузере
// "localhost" часто резолвится в ::1 первым, и запрос к сокету, открытому
// только на IPv4, не доходит вообще (в вебе это видно как NetworkError).
func listenAndServe(host string, port int, handler http.Handler) error {
	hosts := []string{host}
	switch host {
	case "localhost", "127.0.0.1", "::1":
		hosts = []string{"127.0.0.1", "::1"}
	}

	errCh := make(chan error, len(hosts))
	started := 0

	for _, h := range hosts {
		addr := net.JoinHostPort(h, strconv.Itoa(port))

		ln, err := net.Listen("tcp", addr)
		if err != nil {
			// Единственный адрес не открылся — это фатально.
			if len(hosts) == 1 {
				return err
			}
			// Один из loopback-адресов может отсутствовать (например, IPv6
			// выключен в ядре) — этого достаточно, чтобы продолжить.
			log.Printf("Skipping %s: %v", addr, err)
			continue
		}

		started++
		log.Printf("Listening on http://%s", addr)

		go func(ln net.Listener) {
			errCh <- http.Serve(ln, handler)
		}(ln)
	}

	if started == 0 {
		return fmt.Errorf("no listening socket could be opened on port %d", port)
	}

	return <-errCh
}
