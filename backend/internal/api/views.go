package api

import (
	"time"

	"github.com/user/amnezia-web-client/internal/config"
)

// Здесь описано, что API отдаёт наружу вместо самой конфигурации.
//
// Хранимая конфигурация содержит секреты (у AmneziaWG — приватный ключ)
// внутри исходного текста. Окну он не нужен нигде, кроме формы правки,
// где пользователь видит собственный файл целиком. Поэтому список и ответы на
// изменения не несут секретов вовсе, а текст приходит только по явному
// запросу одной конфигурации.
//
// Дотянуться до сокета всё равно может лишь владелец, так что это не граница
// прав, а объём: ключ не расходится по ответам, которые никто об этом не
// просил, и не оседает в памяти окна на всё время его работы.

// configView — конфигурация в том виде, в каком её видит список.
type configView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Protocol  string    `json:"protocol"`
	CreatedAt time.Time `json:"created_at"`
}

// configDetail — то же плюс исходный текст .conf: ровно то, что нужно форме
// правки, и ровно то, что пользователь в неё когда-то вставил.
type configDetail struct {
	configView
	RawConfig string `json:"raw_config"`
}

func newConfigView(cfg *config.Profile) configView {
	return configView{ID: cfg.ID, Name: cfg.Name, Protocol: cfg.Protocol, CreatedAt: cfg.CreatedAt}
}

func newConfigViews(configs []config.Profile) []configView {
	// Не nil, а пустой срез: пустой список должен приходить как [], иначе
	// окну придётся отличать «конфигураций нет» от «поле не пришло».
	views := make([]configView, 0, len(configs))
	for i := range configs {
		views = append(views, newConfigView(&configs[i]))
	}
	return views
}

func newConfigDetail(cfg *config.Profile) configDetail {
	return configDetail{configView: newConfigView(cfg), RawConfig: cfg.RawConfig}
}
