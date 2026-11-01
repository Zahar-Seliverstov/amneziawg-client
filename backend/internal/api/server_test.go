package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user/amnezia-web-client/internal/config"
)

func request(t *testing.T, s *Server, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, path, reader)
	// httptest подставляет example.com, а guard пропускает только петлевой
	// адрес.
	req.Host = "127.0.0.1:7071"
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
	s := newTestServer(t, testProfile("c1", "тест", ""))

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

// batchResponse повторяет ответ пакетной правки — в тесте он разбирается
// целиком, чтобы проверить и взятое, и отвергнутое.
type batchResponse struct {
	Added    []config.RoutingRule `json:"added"`
	Rejected []rejectedRule       `json:"rejected"`
	Removed  int                  `json:"removed"`
}

func doBatch(t *testing.T, s *Server, body string) batchResponse {
	t.Helper()

	rec := request(t, s, http.MethodPost, "/api/routing/rules/batch", body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("пакет отвергнут целиком, код %d: %s", rec.Code, rec.Body.String())
	}

	var got batchResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("невалидный JSON: %v", err)
	}
	return got
}

// Ради этого пакетный ввод и заводился: одна плохая строка в списке из ста не
// должна отвергать остальные девяносто девять.
func TestBatchRejectsPerRule(t *testing.T) {
	s := newTestServer(t)

	got := doBatch(t, s, `{"add":["example.org",".ru","10.0.0.0/8","1.1.1.1","10.0.0.0/44","exa mple.com","ru"]}`)

	if len(got.Added) != 4 {
		t.Fatalf("взято %d правил, ожидалось 4: %+v", len(got.Added), got.Added)
	}
	if len(got.Rejected) != 3 {
		t.Fatalf("отвергнуто %d, ожидалось 3: %+v", len(got.Rejected), got.Rejected)
	}

	// Вид выводится из значения, руками его никто не проставлял.
	want := map[string]string{
		"example.org": config.RuleTypeDomain,
		".ru":         config.RuleTypeZone,
		"10.0.0.0/8":  config.RuleTypeCIDR,
		"1.1.1.1":     config.RuleTypeIP,
	}
	for _, rule := range got.Added {
		if want[rule.Value] != rule.Type {
			t.Errorf("%q определено как %q, ожидалось %q", rule.Value, rule.Type, want[rule.Value])
		}
		if rule.ID == "" {
			t.Errorf("правило %q без идентификатора", rule.Value)
		}
		if !rule.Enabled {
			t.Errorf("правило %q добавлено выключенным", rule.Value)
		}
	}

	// Отвергнутое должно быть названо: «не взяли что-то» пользователю не
	// говорит ничего.
	for _, bad := range got.Rejected {
		if bad.Value == "" || bad.Reason == "" {
			t.Errorf("отказ без значения или причины: %+v", bad)
		}
	}
}

// Дедупликация на службе: сейчас её нет вовсе, дубли ловит только окно.
func TestBatchDeduplicates(t *testing.T) {
	s := newTestServer(t)

	doBatch(t, s, `{"add":["example.org"]}`)
	got := doBatch(t, s, `{"add":["EXAMPLE.ORG","git.example.org","git.example.org"]}`)

	if len(got.Added) != 1 || got.Added[0].Value != "git.example.org" {
		t.Fatalf("взято не то: %+v", got.Added)
	}
	if len(got.Rejected) != 2 {
		t.Fatalf("отвергнуто %d повторов, ожидалось 2: %+v", len(got.Rejected), got.Rejected)
	}

	routing := s.config.GetRouting()
	if len(routing.Rules) != 2 {
		t.Errorf("в списке %d правил, ожидалось 2: %+v", len(routing.Rules), routing.Rules)
	}
}

// Удаление тем же запросом: множественный выбор в списке шлёт один пакет.
func TestBatchRemoves(t *testing.T) {
	s := newTestServer(t)

	added := doBatch(t, s, `{"add":["a.example.org","b.example.org","c.example.org"]}`).Added

	body := `{"remove":["` + added[0].ID + `","` + added[2].ID + `","нет-такого"]}`
	got := doBatch(t, s, body)

	if got.Removed != 2 {
		t.Errorf("удалено %d, ожидалось 2", got.Removed)
	}

	routing := s.config.GetRouting()
	if len(routing.Rules) != 1 || routing.Rules[0].Value != "b.example.org" {
		t.Errorf("в списке осталось не то: %+v", routing.Rules)
	}
}

// Объект принимается наравне со строкой: файл с другой машины несёт вид уже
// проставленным.
func TestBatchAcceptsObjects(t *testing.T) {
	s := newTestServer(t)

	got := doBatch(t, s, `{"add":[{"type":"cidr","value":"10.0.0.0/8"},{"value":"example.org"},{"type":"выдумка","value":"x"}]}`)

	if len(got.Added) != 2 {
		t.Fatalf("взято %d, ожидалось 2: %+v", len(got.Added), got.Added)
	}
	if len(got.Rejected) != 1 || got.Rejected[0].Value != "x" {
		t.Errorf("отказ не про выдуманный вид: %+v", got.Rejected)
	}
}

// Пустые списки в ответе обязаны быть списками, а не null: окно перебирает
// их не глядя.
func TestBatchAnswersWithLists(t *testing.T) {
	s := newTestServer(t)

	rec := request(t, s, http.MethodPost, "/api/routing/rules/batch", `{}`, nil)
	if body := rec.Body.String(); strings.Contains(body, "null") {
		t.Errorf("в ответе null вместо пустого списка: %s", body)
	}
}

// applyRecorder считает применения маршрутизации и запоминает, с каким
// списком правил применение случилось.
type applyRecorder struct {
	mu    sync.Mutex
	calls int
	last  config.RoutingConfig
	done  chan struct{}
}

func (a *applyRecorder) apply(routing *config.RoutingConfig) error {
	a.mu.Lock()
	a.calls++
	a.last = routing.Clone()
	a.mu.Unlock()

	select {
	case a.done <- struct{}{}:
	default:
	}
	return nil
}

func (a *applyRecorder) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// watchApplies подменяет применение маршрутизации счётчиком и ускоряет
// дебаунс: настоящие двести миллисекунд превратили бы тест в ожидание.
// Меньше 100 мс нельзя: на Windows каждая правка с записью файла состояния
// идёт дольше 15 мс, и таймер успевал сработать между правками.
func watchApplies(s *Server) *applyRecorder {
	rec := &applyRecorder{done: make(chan struct{}, 1)}
	s.applyRouting = rec.apply
	s.routingDelay = 100 * time.Millisecond
	return rec
}

// Ради этого дебаунс и заведён: пересборка маршрутизации гасит посредника DNS
// и перебирает весь список, а двадцать правок подряд — обычное дело.
func TestRapidEditsCollapseIntoOneApply(t *testing.T) {
	s := newTestServer(t)
	rec := watchApplies(s)

	for i := 0; i < 20; i++ {
		body := fmt.Sprintf(`{"type":"domain","value":"host%d.example.org"}`, i)
		if rec := request(t, s, http.MethodPost, "/api/routing/rules", body, nil); rec.Code != http.StatusOK {
			t.Fatalf("правило %d отвергнуто: %s", i, rec.Body.String())
		}
	}

	select {
	case <-rec.done:
	case <-time.After(2 * time.Second):
		t.Fatal("маршрутизация так и не применилась")
	}

	// Ждём заведомо дольше паузы: за это время пришли бы и остальные
	// девятнадцать применений, если бы они были.
	time.Sleep(300 * time.Millisecond)

	if got := rec.count(); got != 1 {
		t.Errorf("применений: %d, ожидалось одно", got)
	}

	// Применяется последнее состояние списка, а не то, на котором сработал
	// первый таймер.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.last.Rules) != 20 {
		t.Errorf("применено %d правил, ожидалось 20", len(rec.last.Rules))
	}
}

// Ответ HTTP больше не ждёт пересборки: правило считается принятым, когда оно
// записано, а не когда маршрут встал.
func TestRuleAcceptedBeforeRoutesAreBuilt(t *testing.T) {
	s := newTestServer(t)

	release := make(chan struct{})
	applied := make(chan struct{})
	s.routingDelay = time.Millisecond
	s.applyRouting = func(*config.RoutingConfig) error {
		close(applied)
		<-release
		return nil
	}

	if rec := request(t, s, http.MethodPost, "/api/routing/rules", `{"type":"domain","value":"example.org"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("правило отвергнуто: %s", rec.Body.String())
	}

	// Обработчик вернулся — правило уже в списке, хотя применение ещё идёт.
	if len(s.config.GetRouting().Rules) != 1 {
		t.Error("правило не записано к моменту ответа")
	}

	select {
	case <-applied:
	case <-time.After(2 * time.Second):
		t.Fatal("применение не началось")
	}
	close(release)
}

// Пакет из ста правил — тоже одна пересборка, а не сто.
func TestBatchAppliesOnce(t *testing.T) {
	s := newTestServer(t)
	rec := watchApplies(s)

	var values []string
	for i := 0; i < 100; i++ {
		values = append(values, fmt.Sprintf(`"host%d.example.org"`, i))
	}
	body := `{"add":[` + strings.Join(values, ",") + `]}`

	if got := doBatch(t, s, body); len(got.Added) != 100 {
		t.Fatalf("взято %d правил, ожидалось 100", len(got.Added))
	}

	select {
	case <-rec.done:
	case <-time.After(2 * time.Second):
		t.Fatal("маршрутизация так и не применилась")
	}
	time.Sleep(300 * time.Millisecond)

	if got := rec.count(); got != 1 {
		t.Errorf("применений: %d, ожидалось одно", got)
	}
}

// Пакет, который ничего не изменил, пересборку не запускает: список из одних
// повторов не повод гасить посредника DNS.
func TestUnchangedBatchDoesNotApply(t *testing.T) {
	s := newTestServer(t)
	rec := watchApplies(s)

	doBatch(t, s, `{"add":["example.org"]}`)

	select {
	case <-rec.done:
	case <-time.After(2 * time.Second):
		t.Fatal("первое применение не случилось")
	}

	doBatch(t, s, `{"add":["example.org","мусор с пробелами"]}`)
	time.Sleep(300 * time.Millisecond)

	if got := rec.count(); got != 1 {
		t.Errorf("применений: %d — пересборка пошла зря", got)
	}
}

// Правило на набор без базы не действовало бы молча — оно отвергается сразу,
// с причиной.
func TestBatchRejectsGeoWithoutBase(t *testing.T) {
	s := newTestServer(t)

	rec := request(t, s, http.MethodPost, "/api/routing/rules/batch", `{"add": ["geosite:youtube", "example.com"]}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("пакет не принят: %d %s", rec.Code, rec.Body.String())
	}

	var result struct {
		Added    []config.RoutingRule `json:"added"`
		Rejected []rejectedRule       `json:"rejected"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 1 || len(result.Rejected) != 1 || !strings.Contains(result.Rejected[0].Reason, "не загружена") {
		t.Errorf("ждали одно принятое и geosite с причиной «не загружена»: %+v", result)
	}
}

func TestProtocolsListed(t *testing.T) {
	s := newTestServer(t)
	rec := request(t, s, http.MethodGet, "/api/protocols", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"amneziawg"`) {
		t.Errorf("код %d, тело %s", rec.Code, rec.Body)
	}
}
