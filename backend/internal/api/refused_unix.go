//go:build !windows

package api

import (
	"errors"
	"syscall"
)

// connRefused сообщает, что сервер ответил отказом (RST) — то есть ответил.
func connRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
