// Package profiles связывает сохранённые конфигурации с протоколами:
// принимает текст от пользователя, определяет протокол и превращает запись
// хранилища в цель подключения.
//
// Одно место на всю службу: и API, и автоподключение при старте получают
// разобранную конфигурацию отсюда, а не собирают её каждый по-своему.
package profiles

import (
	"errors"
	"time"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/tunnel"
	"github.com/user/amnezia-web-client/internal/vpn"
)

// ErrNotFound — конфигурации с таким идентификатором нет.
var ErrNotFound = errors.New("конфигурация не найдена")

// Catalog — сохранённые конфигурации и протоколы, которыми они разбираются.
type Catalog struct {
	store     *config.AppConfig
	protocols *tunnel.Registry
}

// New собирает каталог.
func New(store *config.AppConfig, protocols *tunnel.Registry) *Catalog {
	return &Catalog{store: store, protocols: protocols}
}

// Prepare разбирает текст, вставленный пользователем, и готовит запись к
// сохранению: протокол определяется по тексту, пустое имя выводится из
// конфигурации, повторяющееся — дополняется номером. selfID — запись,
// которую правят (её собственное имя не считается занятым); "" — новая.
func (c *Catalog) Prepare(name, raw, selfID string) (config.Profile, error) {
	cfg, err := c.protocols.Parse("", raw)
	if err != nil {
		return config.Profile{}, err
	}
	if name == "" {
		name = tunnel.SuggestName(cfg.Spec())
	}
	return config.Profile{
		ID:        config.GenerateID(),
		Name:      c.store.UniqueConfigName(name, selfID),
		Protocol:  cfg.Protocol(),
		RawConfig: raw,
		CreatedAt: time.Now(),
	}, nil
}

// Protocol — протокол для людей: идентификатор и название.
type Protocol struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Protocols — протоколы, которые служба умеет разбирать, в порядке
// регистрации.
func (c *Catalog) Protocols() []Protocol {
	list := c.protocols.Protocols()
	out := make([]Protocol, len(list))
	for i, p := range list {
		out[i] = Protocol{ID: p.ID(), Title: p.Title()}
	}
	return out
}

// Target разбирает сохранённую конфигурацию для подключения.
func (c *Catalog) Target(id string) (vpn.Target, error) {
	p := c.store.GetConfig(id)
	if p == nil {
		return vpn.Target{}, ErrNotFound
	}
	cfg, err := c.protocols.Parse(p.Protocol, p.RawConfig)
	if err != nil {
		return vpn.Target{}, err
	}
	return vpn.Target{ID: p.ID, Name: p.Name, Config: cfg}, nil
}
