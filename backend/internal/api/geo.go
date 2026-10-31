package api

// Базы geoip.dat и geosite.dat: что загружено, скачать заново, поставить из
// файла.

import (
	"context"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/geodata"
)

// maxGeoUpload — предел на присланный файл базы. Отдельный от maxRequestBody:
// база — десятки мегабайт, а не текст .conf.
const maxGeoUpload = 256 << 20

func (s *Server) handleGetGeo(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, s.geo.Info())
}

// handleUpdateGeo скачивает базу. Синхронно: ждать скачивания пользователь
// готов — он сам нажал кнопку, а ошибку надо показать ему, а не журналу.
func (s *Server) handleUpdateGeo(w http.ResponseWriter, r *http.Request) {
	kind := geodata.Kind(mux.Vars(r)["kind"])

	var req struct {
		URL string `json:"url"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}

	// Не контекст запроса: закрытая вкладка не должна обрывать скачивание
	// на середине.
	if err := s.geo.Download(context.Background(), kind, req.URL); err != nil {
		jsonError(w, err.Error(), http.StatusBadGateway)
		return
	}

	go s.vpnManager.RefreshGeo()
	jsonResponse(w, s.geo.Info())
}

func (s *Server) handleUploadGeo(w http.ResponseWriter, r *http.Request) {
	kind := geodata.Kind(mux.Vars(r)["kind"])

	if err := s.geo.Replace(kind, http.MaxBytesReader(w, r.Body, maxGeoUpload)); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	go s.vpnManager.RefreshGeo()
	jsonResponse(w, s.geo.Info())
}

// geoProblem отвечает, годится ли ссылка правила на базу. Правило на
// отсутствующую категорию не действовало бы молча — лучше сказать сразу.
func (s *Server) geoProblem(rule config.RoutingRule) error {
	switch rule.Type {
	case config.RuleTypeGeoIP:
		return s.geo.Check(geodata.IP, rule.Value)
	case config.RuleTypeGeoSite:
		return s.geo.Check(geodata.Site, rule.Value)
	}
	return nil
}

// checkGeoRules отсеивает из пакета правила на отсутствующие категории.
func (s *Server) checkGeoRules(rules []config.RoutingRule, rejected []rejectedRule) ([]config.RoutingRule, []rejectedRule) {
	kept := rules[:0]
	for _, rule := range rules {
		if err := s.geoProblem(rule); err != nil {
			rejected = append(rejected, rejectedRule{Value: rule.Type + ":" + rule.Value, Reason: err.Error()})
			continue
		}
		kept = append(kept, rule)
	}
	return kept, rejected
}
