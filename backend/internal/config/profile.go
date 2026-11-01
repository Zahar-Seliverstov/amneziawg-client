// Package config хранит пользовательские данные приложения: сохранённые
// конфигурации подключений, правила маршрутизации и настройки.
//
// Пакет не знает ни одного протокола VPN: конфигурация хранится исходным
// текстом, а разбирает её протокол (internal/tunnel). Поэтому новый протокол
// не требует ни правок здесь, ни переделки файла состояния.
//
// Пакет разделён по ответственности:
//   - profile.go — сохранённая конфигурация,
//   - rules.go   — правила маршрутизации и их проверка,
//   - store.go   — потокобезопасное хранилище и запись на диск.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Profile — сохранённая конфигурация подключения.
type Profile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Protocol — идентификатор протокола (tunnel.Protocol.ID). Пусто у
	// записей, сохранённых до появления поля: протокол тогда узнаётся по
	// тексту.
	Protocol  string    `json:"protocol,omitempty"`
	RawConfig string    `json:"raw_config"`
	CreatedAt time.Time `json:"created_at"`
}

// GenerateID возвращает случайный идентификатор для конфигураций и правил.
func GenerateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
