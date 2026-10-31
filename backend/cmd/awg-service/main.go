// awg-service — служба клиента: ядро AmneziaWG, маршруты, DNS и HTTP-API.
//
// Работает с правами администратора (root на Linux, LocalSystem на Windows)
// и слушает только петлевой адрес. Интерфейса в ней нет: его раздаёт
// отдельный процесс без прав, awg-ui, и пересылает запросы /api сюда.
//
//	awg-service             — запуск (так её запускает система)
//	awg-service install     — зарегистрировать службу с автозапуском
//	awg-service uninstall   — снять службу
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
	"path/filepath"
	"time"

	"github.com/user/amnezia-web-client/internal/api"
	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/geodata"
	"github.com/user/amnezia-web-client/internal/service"
	"github.com/user/amnezia-web-client/internal/version"
	"github.com/user/amnezia-web-client/internal/vpn"
)

// serviceName — имя службы в systemd и в диспетчере служб Windows.
const serviceName = "awg-service"

// DefaultListen — адрес API по умолчанию. Только петля: API отдаёт
// приватные ключи и управляет сетью машины.
const DefaultListen = "127.0.0.1:7071"

// Тайминги HTTP-сервера.
const (
	// readHeaderTimeout — предел на чтение заголовков запроса. Без него
	// соединение, открытое и замолчавшее, занимает горутину до бесконечности:
	// классический медленный отказ в обслуживании.
	readHeaderTimeout = 10 * time.Second

	// idleTimeout закрывает keep-alive соединения, которыми больше не
	// пользуются.
	idleTimeout = 2 * time.Minute

	// Общего WriteTimeout здесь намеренно нет: он обрывал бы поток
	// изменений статуса (/api/vpn/events), который держится открытым всё
	// время, пока открыт интерфейс.

	// shutdownTimeout — сколько ждём завершения запросов при остановке.
	shutdownTimeout = 5 * time.Second

	// autoconnectDelay даёт серверу начать слушать до того, как поднимется
	// туннель: интерфейс, открытый сразу после запуска, должен увидеть уже
	// идущее подключение, а не пустой ответ.
	autoconnectDelay = 500 * time.Millisecond
)

func main() {
	listen := flag.String("listen", DefaultListen, "Адрес HTTP-API (только петлевой)")
	configPath := flag.String("config", "", "Файл состояния (по умолчанию "+filepath.Join(config.DefaultDir(), "config.json")+")")
	showVersion := flag.Bool("version", false, "Показать версию и выйти")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Value)
		return
	}

	if err := checkLoopback(*listen); err != nil {
		log.Fatal(err)
	}

	path := *configPath
	if path == "" {
		path = filepath.Join(config.DefaultDir(), "config.json")
	}

	switch flag.Arg(0) {
	case "":
	case "install":
		spec := service.Spec{
			Name:        serviceName,
			DisplayName: "AWG Client",
			Description: "Служба AmneziaWG Client: туннель, маршруты и API для интерфейса",
		}
		if *listen != DefaultListen {
			spec.Args = append(spec.Args, "-listen", *listen)
		}
		if *configPath != "" {
			spec.Args = append(spec.Args, "-config", *configPath)
		}
		if err := service.Install(spec); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Служба установлена и запущена")
		return
	case "uninstall":
		if err := service.Uninstall(serviceName); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Служба удалена")
		return
	default:
		usage()
		os.Exit(2)
	}

	// Каталог закрываем до первой записи в него, включая журнал службы:
	// в нём будут лежать приватные ключи.
	if err := config.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		log.Fatalf("Не удалось подготовить каталог %s: %v", filepath.Dir(path), err)
	}

	err := service.Run(serviceName, func(ctx context.Context) error {
		return run(ctx, *listen, path)
	})
	if err != nil {
		log.Fatal(err)
	}
}

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, "Использование: %s [флаги] [install|uninstall]\n\n", filepath.Base(os.Args[0]))
	flag.PrintDefaults()
}

// checkLoopback не даёт открыть API наружу: в нём приватные ключи, и
// защиты от чужих машин у него нет — только от чужих страниц.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("адрес %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("API можно открыть только на петлевом адресе, а не на %q", host)
	}
	return nil
}

// run — тело службы: от загрузки состояния до разбора туннеля.
func run(ctx context.Context, listen, configPath string) error {
	log.Printf("AWG Client %s", version.Value)
	log.Printf("Файл состояния: %s", configPath)

	appConfig := config.NewAppConfig(configPath)
	if err := appConfig.Load(); err != nil {
		return fmt.Errorf("не удалось загрузить состояние: %w", err)
	}

	vpnManager := vpn.NewManager()

	// Базы geoip/geosite лежат рядом с состоянием: каталог уже закрыт от
	// чужих, и служба пишет туда от своего имени.
	geo := geodata.Open(filepath.Dir(configPath))
	vpnManager.SetGeo(geo)
	go geo.AutoUpdate(ctx, func(geodata.Kind) { vpnManager.RefreshGeo() })

	handler := api.NewServer(appConfig, vpnManager, geo)

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("не удалось открыть %s: %w", listen, err)
	}
	log.Printf("API на http://%s", ln.Addr())

	autoconnect(appConfig, vpnManager)

	return serve(ctx, ln, handler, vpnManager)
}

// autoconnect подключается к конфигу, выбранному на главном экране.
//
// Если выбирать нечего (конфигов ещё нет), просто ничего не делаем — настройка
// остаётся включённой и сработает при следующем запуске.
func autoconnect(appConfig *config.AppConfig, vpnManager *vpn.Manager) {
	id := appConfig.GetAutoconnectConfigID()
	if id == "" {
		if appConfig.GetSettings().Autoconnect {
			log.Printf("Автоподключение включено, но конфиг не выбран — пропускаем")
		}
		return
	}

	cfg := appConfig.GetConfig(id)
	if cfg == nil {
		return
	}

	log.Printf("Автоподключение к %s...", cfg.Name)

	// Копию правил берём через геттер: поле Routing защищено мьютексом
	// конфига, а прямое обращение к нему из горутины — гонка с любым
	// изменением правил из интерфейса.
	routing := appConfig.GetRouting()

	go func() {
		time.Sleep(autoconnectDelay)
		if err := vpnManager.Connect(cfg, &routing); err != nil {
			log.Printf("Автоподключение не удалось: %v", err)
		}
	}()
}

// serve обслуживает запросы до остановки службы и корректно всё разбирает.
//
// Порядок при остановке важен: сначала перестаём принимать новые запросы,
// потом разбираем туннель. Наоборот — значит дать интерфейсу поднять его
// заново ровно между двумя шагами.
func serve(ctx context.Context, ln net.Listener, handler http.Handler, vpnManager *vpn.Manager) error {
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          log.Default(),
	}

	// Буфер на единицу: горутина обязана суметь отдать ошибку и завершиться,
	// даже если её никто уже не читает.
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ln)
	}()

	select {
	case err := <-errCh:
		// Serve вернулся сам — это отказ. Туннель всё равно разбираем, иначе
		// он останется поднятым без единого способа им управлять.
		shutdownVPN(vpnManager)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err

	case <-ctx.Done():
		log.Printf("Остановка службы")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
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

	log.Println("Отключение VPN...")
	if err := vpnManager.Disconnect(); err != nil {
		log.Printf("Не удалось корректно отключить VPN: %v", err)
	}
}
