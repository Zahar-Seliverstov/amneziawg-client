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

// Приватный ключ не должен уезжать в окно без нужды: списку он не нужен, а
// текст .conf нужен только форме правки — по отдельному запросу.
func TestConfigResponsesHideSecrets(t *testing.T) {
	const raw = "[Interface]\nPrivateKey = qO8QDrIKR3vufYDHIRcbYSuVFPGqOcJ2P08S6r67dFA=\nAddress = 10.8.0.2/32\n\n[Peer]\nPublicKey = dGVzdHB1YmxpY2tleXRlc3RwdWJsaWNrZXkxMjM0NTY=\nEndpoint = vpn.example.com:51820\nAllowedIPs = 0.0.0.0/0\n"

	s := newTestServer(t)

	body, _ := json.Marshal(map[string]string{"name": "srv", "raw_config": raw})
	rec := request(t, s, http.MethodPost, "/api/configs", string(body), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("конфигурация не добавлена: %d %s", rec.Code, rec.Body.String())
	}

	var added struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil {
		t.Fatalf("ответ не разобран: %v", err)
	}

	for _, path := range []string{"/api/configs", "/api/configs/" + added.ID} {
		got := request(t, s, http.MethodGet, path, "", nil).Body.String()
		if strings.Contains(got, "private_key") {
			t.Errorf("%s отдаёт разобранный приватный ключ: %s", path, got)
		}
	}

	if got := rec.Body.String(); strings.Contains(got, "PrivateKey") {
		t.Errorf("ответ на добавление несёт текст .conf с ключом: %s", got)
	}
	if got := request(t, s, http.MethodGet, "/api/configs", "", nil).Body.String(); strings.Contains(got, "PrivateKey") {
		t.Errorf("список несёт текст .conf с ключом: %s", got)
	}

	// А форме правки текст обязан приходить: иначе править нечего.
	if got := request(t, s, http.MethodGet, "/api/configs/"+added.ID, "", nil).Body.String(); !strings.Contains(got, "PrivateKey") {
		t.Errorf("подробности пришли без текста .conf: %s", got)
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

// Ответ на неизвестный путь обязан быть JSON: клиент разбирает как JSON
// любой ответ и на HTML упал бы с невнятной ошибкой парсера.
func TestUnknownAPIPathReturnsJSON(t *testing.T) {
	s := newTestServer(t)

	rec := request(t, s, http.MethodGet, "/api/такого-нет", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("код %d, ожидался 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("тип ответа %q — клиент упадёт на разборе", ct)
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
