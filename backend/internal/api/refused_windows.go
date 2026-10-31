package api

import (
	"errors"

	"golang.org/x/sys/windows"
)

// connRefused сообщает, что сервер ответил отказом (RST) — то есть ответил.
// Windows сообщает об этом своим кодом сокетов, а не ECONNREFUSED.
func connRefused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED)
}
