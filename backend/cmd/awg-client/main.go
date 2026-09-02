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
	"syscall"
	"time"

	"github.com/user/amnezia-web-client/internal/api"
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

	// Общего WriteTimeout здесь намеренно нет: он обрывал бы поток
	// изменений статуса (/api/vpn/events), который держится открытым всё
	// время работы приложения.

	// shutdownTimeout — сколько ждём завершения запросов при остановке.
	shutdownTimeout = 5 * time.Second

	// autoconnectDelay даёт серверу начать слушать до того, как поднимется
	// туннель: интерфейс, открытый сразу после запуска, должен увидеть уже
	// идущее подключение, а не пустой ответ.
	autoconnectDelay = 500 * time.Millisecond

	// parentPollInterval — как часто проверяем, жива ли оболочка.
	parentPollInterval = 2 * time.Second

	// maxSocketPath — предел длины пути сокета. Ядро хранит его в поле
	// фиксированного размера (sun_path, 108 байт на Linux), и bind на пути
	// длиннее отказывает невнятным «invalid argument». Берём с запасом.
	maxSocketPath = 104

	// staleSocketProbe — сколько ждём ответа от сокета, оставшегося от
	// прошлого запуска. Соединение петлевое и локальное: живая служба
	// принимает его мгновенно, а мёртвая отказывает сразу.
	staleSocketProbe = 300 * time.Millisecond
)

func main() {
	// Parse flags
	socketPath := flag.String("socket", "", "Path to the API unix socket (default: api.sock next to the config)")
	configPath := flag.String("config", "", "Path to config file (default: ~/.config/awg-client/config.json)")
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

	owner := desktopuser.Resolve(resolvedPath)
	socket := resolveSocketPath(*socketPath, resolvedPath)

	// Load configuration
	appConfig := config.NewAppConfig(resolvedPath, owner)
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

	// Create API server
	server := api.NewServer(appConfig, vpnManager, autostartMgr)

	autoconnect(appConfig, vpnManager)

	// Backend поднимают через pkexec, и убить его от имени пользователя уже
	// нельзя. Поэтому он сам следит за оболочкой: та исчезла — гасимся,
	// штатно разбирая VPN-соединение.
	if *parentPID > 0 {
		go watchParent(*parentPID)
	}

	if err := serve(socket, owner, server, vpnManager); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// resolveSocketPath выбирает, где открыть сокет API.
//
// Обычно путь передаёт оболочка: она знает каталог времени выполнения своей
// сессии ($XDG_RUNTIME_DIR), а backend работает от root и увидел бы там
// каталог root'а. Значение по умолчанию — рядом с настройками: так запуск из
// терминала работает без лишних флагов.
func resolveSocketPath(socketPath, configPath string) string {
	if socketPath != "" {
		return socketPath
	}
	return filepath.Join(filepath.Dir(configPath), "api.sock")
}

// resolveConfigPath подставляет путь конфига по умолчанию.
//
// Каталог здесь намеренно не создаётся. Служба работает от root, а созданный
// ею каталог принадлежал бы root — и заодно сбивал бы desktopuser.Resolve,
// для которого владелец каталога настроек служит подсказкой о том, чей это
// рабочий стол. Каталог создают те, кто кладёт в него файлы: сохранение
// настроек и открытие сокета, — и оба сразу передают его пользователю.
func resolveConfigPath(path string) (string, error) {
	if path != "" {
		return path, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("не удалось определить домашний каталог: %w", err)
	}

	return filepath.Join(home, ".config", "awg-client", "config.json"), nil
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
func serve(socket string, owner desktopuser.User, handler http.Handler, vpnManager *vpn.Manager) error {
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          log.Default(),
	}

	ln, err := listen(socket, owner)
	if err != nil {
		return err
	}
	log.Printf("API на %s", socket)

	// Буфер на единицу: горутина обязана суметь отдать ошибку и завершиться,
	// даже если её никто уже не читает.
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ln)
	}()

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

// listen открывает unix-сокет API.
//
// Права 0600 и владелец — пользователь рабочего стола: это единственная
// проверка доступа, какая у API есть. Backend работает от root и управляет
// сетью, а GET /api/configs отдаёт приватные ключи всех подключений, поэтому
// дотянуться до сокета не должен никто, кроме того, кто запустил клиент.
//
// Сам файл сокета Go снимает при закрытии слушателя, поэтому убирать его
// отдельно не нужно.
func listen(path string, owner desktopuser.User) (net.Listener, error) {
	if len(path) >= maxSocketPath {
		return nil, fmt.Errorf(
			"путь сокета длиннее %d байт, ядро такой не примет: %s", maxSocketPath, path)
	}

	// 0700 на каталог: сокет с правами 0600 внутри каталога, куда всем можно
	// войти, всё ещё защищён, но каталог заодно хранит настройки и ключи.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("не удалось создать каталог сокета: %w", err)
	}
	if err := owner.Own(dir); err != nil {
		return nil, fmt.Errorf("не удалось передать каталог сокета пользователю: %w", err)
	}

	if err := clearStaleSocket(path); err != nil {
		return nil, err
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}

	// Права выставляем явно и до того, как кто-то успел соединиться: umask
	// процесса нам не подчиняется и мог бы оставить сокет открытым для всех.
	if err := os.Chmod(path, 0600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("не удалось закрыть сокет от чужих: %w", err)
	}
	if err := owner.Own(path); err != nil {
		ln.Close()
		return nil, fmt.Errorf("не удалось передать сокет пользователю: %w", err)
	}

	return ln, nil
}

// clearStaleSocket убирает сокет прошлого запуска.
//
// Имя сокета остаётся в файловой системе и после смерти процесса, поэтому без
// этого второй запуск упирался бы в «address already in use» навсегда. Но
// снести файл вслепую нельзя: если служба ещё жива, это молча отобрало бы у
// неё сокет. Поэтому сначала стучимся — ответили, значит уходим сами.
func clearStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s занят файлом, который не является сокетом", path)
	}

	if conn, err := net.DialTimeout("unix", path, staleSocketProbe); err == nil {
		conn.Close()
		return fmt.Errorf("служба уже запущена (сокет %s отвечает)", path)
	}

	return os.Remove(path)
}
