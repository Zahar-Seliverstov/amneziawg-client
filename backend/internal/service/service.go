// Package service запускает процесс системной службой и регистрирует его в
// системе: на Linux — юнитом systemd, на Windows — службой Windows.
//
// Им пользуются оба бинарника: awg-service (ядро и вся логика, работает с
// правами администратора) и awg-ui (только раздаёт интерфейс, прав не
// имеет). Поэтому и описание службы, и её запуск одинаковы для обоих.
package service

import (
	"context"
	"os"
	"path/filepath"
)

// Spec описывает службу для регистрации в системе.
type Spec struct {
	// Name — имя службы: юнит systemd и имя службы Windows.
	Name string
	// DisplayName и Description видны в списке служб.
	DisplayName string
	Description string
	// Args — аргументы, с которыми служба запускается.
	Args []string
	// Unprivileged — запускать без прав: на Linux под временным
	// пользователем systemd (DynamicUser), на Windows под LocalService.
	Unprivileged bool
	// After — службы, которые должны запуститься раньше этой.
	After []string
}

// Runner — тело службы. Должно вернуться вскоре после отмены ctx.
type Runner func(ctx context.Context) error

// executable — путь к этому же бинарнику, по которому его будет запускать
// система. Ссылки раскрываются: служба не должна сломаться оттого, что
// кто-то переставил ссылку.
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Abs(exe)
}
