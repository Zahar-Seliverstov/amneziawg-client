package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	// stopWait — сколько ждём остановки службы при удалении.
	stopWait = 15 * time.Second

	// maxLogSize — предел журнала: больше него прежний журнал отодвигается
	// при запуске. У службы Windows нет journald, и без предела файл рос бы
	// всё время жизни машины.
	maxLogSize = 10 << 20
)

// Run выполняет службу. Запущенный диспетчером служб процесс работает по его
// протоколу и пишет журнал в файл; запущенный из консоли — до Ctrl+C.
func Run(name string, run Runner) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}

	if !isService {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return run(ctx)
	}

	if f := openLog(name); f != nil {
		log.SetOutput(f)
		defer f.Close()
	}

	h := &handler{run: run}
	if err := svc.Run(name, h); err != nil {
		return err
	}
	return h.err
}

// openLog открывает журнал службы рядом с её данными, а если туда нельзя —
// во временной папке её учётной записи. Не вышло нигде — журнал пропадает,
// но служба работает: это не повод ей не запускаться.
//
// Запасной путь нужен awg-ui: он работает под LocalService, а папка
// %ProgramData%\awg-client закрыта для всех, кроме SYSTEM и
// администраторов. Проверять только создание папки мало: logs уже создана
// службой awg-service, MkdirAll проходит, а открыть файл в ней LocalService
// не может.
func openLog(name string) *os.File {
	dirs := []string{filepath.Join(os.Getenv("ProgramData"), "awg-client", "logs"), os.TempDir()}
	for _, dir := range dirs {
		if f := openLogIn(dir, name); f != nil {
			return f
		}
	}
	return nil
}

func openLogIn(dir, name string) *os.File {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil
	}

	path := filepath.Join(dir, name+".log")
	if info, err := os.Stat(path); err == nil && info.Size() > maxLogSize {
		os.Rename(path, path+".old")
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil
	}
	return f
}

// handler переводит команды диспетчера служб в отмену контекста.
type handler struct {
	run Runner
	err error
}

func (h *handler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-done:
			// Тело службы вышло само — значит, отказало.
			h.err = err
			if err != nil {
				log.Printf("Служба завершилась с ошибкой: %v", err)
				return false, 1
			}
			return false, 0

		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				h.err = <-done
				return false, 0
			}
		}
	}
}

// Install регистрирует службу с автозапуском и запускает её. Повторная
// установка обновляет параметры и перезапускает службу.
func Install(spec Spec) error {
	exe, err := executable()
	if err != nil {
		return err
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("нет доступа к диспетчеру служб (запустите от имени администратора): %w", err)
	}
	defer m.Disconnect()

	cfg := mgr.Config{
		DisplayName:  spec.DisplayName,
		Description:  spec.Description,
		StartType:    mgr.StartAutomatic,
		Dependencies: spec.After,
	}
	if spec.Unprivileged {
		cfg.ServiceStartName = `NT AUTHORITY\LocalService`
	}

	s, err := m.OpenService(spec.Name)
	if err == nil {
		// Уже установлена — останавливаем и переписываем параметры.
		stop(s)
		cfg.BinaryPathName = quoteArgs(exe, spec.Args)
		if err := s.UpdateConfig(cfg); err != nil {
			s.Close()
			return fmt.Errorf("не удалось обновить службу %s: %w", spec.Name, err)
		}
	} else {
		s, err = m.CreateService(spec.Name, exe, cfg, spec.Args...)
		if err != nil {
			return fmt.Errorf("не удалось создать службу %s: %w", spec.Name, err)
		}
	}
	defer s.Close()

	// Упала — поднять снова через пять секунд. Без этого служба, упавшая
	// однажды, оставалась бы лежать до перезагрузки.
	restart := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}
	if err := s.SetRecoveryActions(restart, uint32((24 * time.Hour).Seconds())); err != nil {
		log.Printf("Не удалось задать перезапуск службы %s: %v", spec.Name, err)
	}

	if err := s.Start(); err != nil {
		return fmt.Errorf("служба %s установлена, но не запустилась: %w", spec.Name, err)
	}
	return nil
}

// Uninstall останавливает и удаляет службу.
func Uninstall(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("нет доступа к диспетчеру служб (запустите от имени администратора): %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil
		}
		return err
	}
	defer s.Close()

	stop(s)
	return s.Delete()
}

// stop останавливает службу и ждёт, пока она действительно остановится:
// иначе exe остаётся занят, и заменить его при обновлении нельзя.
func stop(s *mgr.Service) {
	status, err := s.Control(svc.Stop)
	if err != nil {
		return
	}

	deadline := time.Now().Add(stopWait)
	for status.State != svc.Stopped && time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		if status, err = s.Query(); err != nil {
			return
		}
	}
}

// quoteArgs собирает командную строку службы так же, как CreateService.
func quoteArgs(exe string, args []string) string {
	cmd := windows.EscapeArg(exe)
	for _, arg := range args {
		cmd += " " + windows.EscapeArg(arg)
	}
	return cmd
}
