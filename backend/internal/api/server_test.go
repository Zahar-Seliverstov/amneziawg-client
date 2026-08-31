package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/amnezia-web-client/internal/config"
)

func request(t *testing.T, s *Server, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

// Чужой сайт не должен ни читать конфигурации (в них приватные ключи), ни
// управлять подключением.
func TestForeignOriginIsRejected(t *testing.T) {
	s := newTestServer(t, config.AmneziaWGConfig{ID: "c1", Name: "тест"})

	rec := request(t, s, http.MethodGet, "/api/configs", "", map[string]string{
		"Origin": "https://evil.example",
	})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("чужой origin получил код %d вместо %d", rec.Code, http.StatusForbidden)
	}
	if strings.Contains(rec.Body.String(), "c1") {
		t.Error("ответ утёк чужому источнику")
	}
}

func TestLocalOriginIsAllowed(t *testing.T) {
	s := newTestServer(t, config.AmneziaWGConfig{ID: "c1", Name: "тест"})

	for _, origin := range []string{"http://localhost:3000", "http://127.0.0.1:8081", "http://[::1]:8081"} {
		rec := request(t, s, http.MethodGet, "/api/configs", "", map[string]string{"Origin": origin})
		if rec.Code != http.StatusOK {
			t.Errorf("origin %q отвергнут с кодом %d", origin, rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("origin %q: заголовок ответа %q", origin, got)
		}
	}
}

// Preflight обязан отвечать сам: маршрута на OPTIONS в роутере нет, и без
// этого браузер получал бы 404 без заголовков CORS и блокировал всё.
func TestPreflight(t *testing.T) {
	s := newTestServer(t)

	rec := request(t, s, http.MethodOptions, "/api/configs", "", map[string]string{
		"Origin": "http://localhost:3000",
	})

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight ответил кодом %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("в ответе на preflight нет списка методов")
	}
}

// Тело без предела означало бы, что любой локальный процесс может исчерпать
// память backend'а, работающего от root.
func TestOversizedBodyIsRejected(t *testing.T) {
	s := newTestServer(t)

	body := `{"raw_config": "` + strings.Repeat("x", maxRequestBody+1024) + `"}`
	rec := request(t, s, http.MethodPost, "/api/configs", body, nil)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("огромное тело принято с кодом %d", rec.Code)
	}
}

func TestAddConfigRejectsGarbage(t *testing.T) {
	s := newTestServer(t)

	cases := []struct {
		name, body string
		want       int
	}{
		{"не JSON", "{", http.StatusBadRequest},
		{"пустой конфиг", `{"raw_config": ""}`, http.StatusBadRequest},
		{"текст без секций", `{"raw_config": "просто текст"}`, http.StatusBadRequest},
		{"без пиров", `{"raw_config": "[Interface]\nPrivateKey = qO8QDrIKR3vufYDHIRcbYSuVFPGqOcJ2P08S6r67dFA="}`, http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := request(t, s, http.MethodPost, "/api/configs", tc.body, nil); rec.Code != tc.want {
				t.Errorf("код %d, ожидался %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	if got := len(s.config.GetAllConfigs()); got != 0 {
		t.Errorf("мусор всё же сохранён: %d конфигураций", got)
	}
}

// Правила приходят и из интерфейса, и файлом от пользователя: непригодное
// значение обязано быть отвергнуто, а не уехать в таблицу маршрутизации.
func TestRoutingValidationOnHTTP(t *testing.T) {
	s := newTestServer(t)

	bad := []string{
		`{"mode": "нечто", "rules": []}`,
		`{"mode": "vpn_list", "rules": [{"type": "ip", "value": "не адрес"}]}`,
		`{"mode": "vpn_list", "rules": [{"type": "выдумка", "value": "1.1.1.1"}]}`,
	}
	for _, body := range bad {
		if rec := request(t, s, http.MethodPut, "/api/routing", body, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("принято непригодное: %s (код %d)", body, rec.Code)
		}
	}

	// Повторяющиеся идентификаторы чинятся, а не отвергаются: файл мог
	// прийти из другой сборки.
	good := `{"mode": "vpn_list", "rules": [
		{"id": "same", "type": "ip", "value": "1.1.1.1"},
		{"id": "same", "type": "cidr", "value": "10.0.0.0/8"}
	]}`
	rec := request(t, s, http.MethodPut, "/api/routing", good, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("пригодные правила отвергнуты: %s", rec.Body.String())
	}

	stored := s.config.GetRouting()
	if len(stored.Rules) != 2 || stored.Rules[0].ID == stored.Rules[1].ID {
		t.Errorf("повторяющиеся идентификаторы не исправлены: %+v", stored.Rules)
	}
}

func TestAddRoutingRuleValidates(t *testing.T) {
	s := newTestServer(t)

	if rec := request(t, s, http.MethodPost, "/api/routing/rules", `{"type":"domain","value":"exa mple.com"}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("принято непригодное имя, код %d", rec.Code)
	}

	rec := request(t, s, http.MethodPost, "/api/routing/rules", `{"type":"domain","value":" example.com "}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("правило отвергнуто: %s", rec.Body.String())
	}

	var rule config.RoutingRule
	if err := json.NewDecoder(rec.Body).Decode(&rule); err != nil {
		t.Fatalf("невалидный JSON: %v", err)
	}
	if rule.Value != "example.com" {
		t.Errorf("значение не нормализовано: %q", rule.Value)
	}
	if rule.ID == "" {
		t.Error("правило создано без идентификатора")
	}
}

// Неизвестный путь под /api — ошибка запроса, а не адрес страницы: отдать
// здесь index.html значит вернуть клиенту HTML вместо JSON.
func TestUnknownAPIPathReturnsJSON(t *testing.T) {
	s := newTestServer(t)
	if err := s.SetupStatic(""); err != nil {
		t.Skipf("интерфейс не вшит в сборку: %v", err)
	}

	rec := request(t, s, http.MethodGet, "/api/такого-нет", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("код %d, ожидался 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("тип ответа %q — клиент упадёт на разборе", ct)
	}
}

func TestSecurityHeaders(t *testing.T) {
	s := newTestServer(t)
	rec := request(t, s, http.MethodGet, "/api/vpn/status", "", nil)

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options: %q", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options: %q", got)
	}
}

func TestDeleteMissingEntities(t *testing.T) {
	s := newTestServer(t)

	if rec := request(t, s, http.MethodDelete, "/api/configs/нет-такого", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("удаление несуществующей конфигурации: код %d", rec.Code)
	}
	if rec := request(t, s, http.MethodDelete, "/api/routing/rules/нет-такого", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("удаление несуществующего правила: код %d", rec.Code)
	}
}

func TestSelectedConfigMustExist(t *testing.T) {
	s := newTestServer(t, config.AmneziaWGConfig{ID: "c1", Name: "тест"})

	if rec := request(t, s, http.MethodPut, "/api/selected-config", `{"config_id":"нет-такого"}`, nil); rec.Code != http.StatusNotFound {
		t.Errorf("выбран несуществующий конфиг, код %d", rec.Code)
	}
	if rec := request(t, s, http.MethodPut, "/api/selected-config", `{"config_id":"c1"}`, nil); rec.Code != http.StatusOK {
		t.Errorf("существующий конфиг не выбрался: %d", rec.Code)
	}
	if got := s.config.GetSelectedConfigID(); got != "c1" {
		t.Errorf("выбранный конфиг %q", got)
	}
}
