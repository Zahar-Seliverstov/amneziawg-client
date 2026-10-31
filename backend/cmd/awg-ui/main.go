// awg-ui — веб-интерфейс клиента.
//
// Раздаёт собранный интерфейс и пересылает запросы /api службе awg-service.
// Прав не имеет и ничего, кроме этого, не делает: вся логика — в службе.
// Пересылка, а не прямые запросы из браузера в службу, нужна ради одного
// источника: страница и API живут на одном адресе, и CORS не требуется.
//
//	awg-ui             — запуск (так его запускает система)
//	awg-ui install     — зарегистрировать службу с автозапуском
//	awg-ui uninstall   — снять службу
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/user/amnezia-web-client/internal/localguard"
	"github.com/user/amnezia-web-client/internal/service"
	"github.com/user/amnezia-web-client/internal/version"
)

const (
	serviceName = "awg-ui"

	// DefaultListen — адрес интерфейса по умолчанию.
	DefaultListen = "127.0.0.1:7070"

	// DefaultAPI — где слушает служба.
	DefaultAPI = "http://127.0.0.1:7071"

	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 2 * time.Minute
	shutdownTimeout   = 5 * time.Second
)

func main() {
	listen := flag.String("listen", DefaultListen, "Адрес интерфейса (только петлевой)")
	apiURL := flag.String("api", DefaultAPI, "Адрес API службы awg-service")
	www := flag.String("www", "", "Каталог собранного интерфейса (по умолчанию www рядом с программой)")
	showVersion := flag.Bool("version", false, "Показать версию и выйти")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Value)
		return
	}

	dir, err := wwwDir(*www)
	if err != nil {
		log.Fatal(err)
	}

	target, err := url.Parse(*apiURL)
	if err != nil || target.Host == "" {
		log.Fatalf("Адрес API %q не разобрать", *apiURL)
	}

	if err := checkLoopback(*listen); err != nil {
		log.Fatal(err)
	}

	switch flag.Arg(0) {
	case "":
	case "install":
		spec := service.Spec{
			Name:         serviceName,
			DisplayName:  "AWG Client UI",
			Description:  "Веб-интерфейс AmneziaWG Client на http://" + *listen,
			Args:         []string{"-www", dir},
			Unprivileged: true,
			After:        []string{"awg-service"},
		}
		if *listen != DefaultListen {
			spec.Args = append(spec.Args, "-listen", *listen)
		}
		if *apiURL != DefaultAPI {
			spec.Args = append(spec.Args, "-api", *apiURL)
		}
		if err := service.Install(spec); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Интерфейс установлен: http://%s\n", *listen)
		return
	case "uninstall":
		if err := service.Uninstall(serviceName); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Интерфейс удалён")
		return
	default:
		usage()
		os.Exit(2)
	}

	err = service.Run(serviceName, func(ctx context.Context) error {
		return serve(ctx, *listen, newHandler(dir, target))
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

// wwwDir находит каталог интерфейса и проверяет, что он собран.
func wwwDir(flagValue string) (string, error) {
	dir := flagValue
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		dir = filepath.Join(filepath.Dir(exe), "www")
	}

	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		return "", fmt.Errorf("в %s нет собранного интерфейса (index.html): %w", dir, err)
	}
	return dir, nil
}

func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("адрес %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("интерфейс можно открыть только на петлевом адресе, а не на %q", host)
	}
	return nil
}

// newHandler собирает обработчик: /api/ уходит службе, остальное — файлы.
//
// Защита стоит и здесь, а не только в службе: подменив имя на 127.0.0.1,
// чужая страница читала бы ответы API через пересылку, и защита службы этого
// бы не увидела — Host до неё доходит уже наш.
func newHandler(dir string, api *url.URL) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", newProxy(api))
	mux.Handle("/", spa(dir))
	return localguard.Wrap(mux)
}

// newProxy пересылает запросы службе.
func newProxy(target *url.URL) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			// Заголовки, включая Origin, уходят как есть: служба сверяет их
			// сама. Host SetURL меняет на адрес службы.
			r.SetURL(target)
		},
		// Поток событий статуса (/api/vpn/events) должен уходить в браузер
		// построчно, а не копиться в буфере.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("Служба недоступна: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"error":"Служба awg-service не отвечает","offline":true}`)
		},
	}
}

// spa раздаёт файлы интерфейса. Неизвестный путь получает index.html:
// маршрутизацией страниц занимается сам интерфейс.
func spa(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(dir, filepath.FromSlash(filepath.Clean("/"+r.URL.Path)))
		if info, err := os.Stat(path); err != nil || info.IsDir() && !hasIndex(path) {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}

		// Сборка Nuxt кладёт хэш в имя файла, и такие файлы можно держать
		// в кэше вечно. index.html — никогда: иначе после обновления браузер
		// открывал бы старый интерфейс, ссылающийся на исчезнувшие файлы.
		if strings.HasPrefix(r.URL.Path, "/_nuxt/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

func hasIndex(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil
}

func serve(ctx context.Context, listen string, handler http.Handler) error {
	server := &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          log.Default(),
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("AWG Client UI %s на http://%s", version.Value, listen)
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	// Открытый поток событий Shutdown не дождётся — он не кончается сам.
	// Ждём остальные запросы, а его обрываем.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		server.Close()
	}
	return nil
}
