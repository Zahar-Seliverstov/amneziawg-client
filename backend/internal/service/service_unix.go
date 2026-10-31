//go:build !windows

package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

// unitDir — куда кладутся юниты, установленные администратором.
var unitDir = "/etc/systemd/system"

// Run выполняет службу до SIGINT или SIGTERM. systemd останавливает службу
// именно SIGTERM, так что отдельного режима для него не нужно.
func Run(_ string, run Runner) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return run(ctx)
}

// Install записывает юнит systemd, включает его и запускает. Повторная
// установка обновляет юнит и перезапускает службу.
func Install(spec Spec) error {
	exe, err := executable()
	if err != nil {
		return err
	}

	path := filepath.Join(unitDir, spec.Name+".service")
	if err := os.WriteFile(path, []byte(unitFile(spec, exe)), 0644); err != nil {
		return fmt.Errorf("не удалось записать %s: %w", path, err)
	}

	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	if err := systemctl("enable", spec.Name); err != nil {
		return err
	}
	return systemctl("restart", spec.Name)
}

// Uninstall останавливает службу и убирает её юнит.
func Uninstall(name string) error {
	path := filepath.Join(unitDir, name+".service")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	// Остановка и выключение могут не удаться, если служба уже снята
	// вручную, — это не повод оставлять юнит.
	systemctl("disable", "--now", name)

	if err := os.Remove(path); err != nil {
		return err
	}
	return systemctl("daemon-reload")
}

// unitFile собирает юнит systemd.
func unitFile(spec Spec, exe string) string {
	var sb strings.Builder

	sb.WriteString("[Unit]\n")
	fmt.Fprintf(&sb, "Description=%s\n", spec.Description)
	after := append([]string{"network-online.target"}, units(spec.After)...)
	fmt.Fprintf(&sb, "After=%s\n", strings.Join(after, " "))
	sb.WriteString("Wants=network-online.target\n")

	sb.WriteString("\n[Service]\n")
	cmd := []string{quote(exe)}
	for _, arg := range spec.Args {
		cmd = append(cmd, quote(arg))
	}
	fmt.Fprintf(&sb, "ExecStart=%s\n", strings.Join(cmd, " "))
	sb.WriteString("Restart=on-failure\n")
	sb.WriteString("RestartSec=3\n")
	if spec.Unprivileged {
		sb.WriteString("DynamicUser=yes\n")
		sb.WriteString("NoNewPrivileges=yes\n")
		sb.WriteString("ProtectSystem=strict\n")
		sb.WriteString("ProtectHome=read-only\n")
		sb.WriteString("PrivateTmp=yes\n")
	}

	sb.WriteString("\n[Install]\n")
	sb.WriteString("WantedBy=multi-user.target\n")

	return sb.String()
}

func units(names []string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = name + ".service"
	}
	return out
}

// quote берёт аргумент в кавычки, если в нём есть пробел: systemd делит
// ExecStart по пробелам так же, как оболочка.
func quote(arg string) string {
	if !strings.ContainsAny(arg, " \t\"\\") {
		return arg
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(arg) + `"`
}

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
