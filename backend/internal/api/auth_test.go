package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/amnezia-web-client/internal/auth"
	"github.com/user/amnezia-web-client/internal/config"
)

// bare отправляет запрос без всякой аутентификации — в отличие от request,
// который токен подставляет.
func bare(s *Server, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

// Ради этого всё и затевалось: другой пользователь машины доходит до порта,
// но не до приватных ключей.
func TestAPIRequiresToken(t *testing.T) {
	s := newTestServer(t, config.AmneziaWGConfig{
		ID:        "c1",
		Name:      "тест",
		Interface: config.InterfaceConfig{PrivateKey: "СЕКРЕТ"},
	})

	rec := bare(s, http.MethodGet, "/api/configs", nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("код %d, ожидался 401", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "СЕКРЕТ") {
		t.Fatal("приватный ключ утёк в ответе на запрос без токена")
	}
}

func TestWrongTokenIsRejected(t *testing.T) {
	s := newTestServer(t)

	cases := map[string]map[string]string{
		"чужой Bearer":  {"Authorization": "Bearer не-тот-токен"},
		"пустой Bearer": {"Authorization": "Bearer "},
		"чужая схема":   {"Authorization": "Basic " + s.token.Value()},
	}

	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			if rec := bare(s, http.MethodGet, "/api/vpn/status", headers); rec.Code != http.StatusUnauthorized {
				t.Errorf("код %d, ожидался 401", rec.Code)
			}
		})
	}

	rec := bare(s, http.MethodGet, "/api/vpn/status?"+auth.QueryParam+"=не-тот", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("чужой токен в адресе принят с кодом %d", rec.Code)
	}
}

// Токен из адреса обменивается на cookie и из адреса убирается: иначе он
// осел бы в истории браузера и в закладках.
func TestTokenInURLBecomesCookie(t *testing.T) {
	s := newTestServer(t)

	rec := bare(s, http.MethodGet, "/?"+auth.QueryParam+"="+s.token.Value(), nil)

	if rec.Code != http.StatusFound {
		t.Fatalf("код %d, ожидалось перенаправление 302", rec.Code)
	}
	if location := rec.Header().Get("Location"); strings.Contains(location, auth.QueryParam) {
		t.Errorf("токен остался в адресе: %q", location)
	}

	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("cookie не выдана")
	}
	if cookie.Value != s.token.Value() {
		t.Error("в cookie не тот токен")
	}
	if !cookie.HttpOnly {
		t.Error("cookie доступна скриптам страницы")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Error("cookie уедет по чужой ссылке: нет SameSite=Strict")
	}
}

// Cookie — единственный способ пройти проверку для WebSocket: заголовок в
// браузерном API задать нечем.
func TestCookieAuthorizesAPI(t *testing.T) {
	s := newTestServer(t)

	rec := bare(s, http.MethodGet, "/api/vpn/status", map[string]string{
		"Cookie": auth.CookieName + "=" + s.token.Value(),
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("cookie не принята: код %d, тело %s", rec.Code, rec.Body.String())
	}
}

// Человек в браузере должен увидеть слова, а клиент API — разбираемый JSON.
func TestDenialSpeaksTheRightLanguage(t *testing.T) {
	s := newTestServer(t)

	api := bare(s, http.MethodGet, "/api/configs", nil)
	if ct := api.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("отказ по API отдан как %q — клиент упадёт на разборе", ct)
	}

	page := bare(s, http.MethodGet, "/", nil)
	if ct := page.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("отказ в браузере отдан как %q", ct)
	}
	if !strings.Contains(page.Body.String(), "токен") {
		t.Error("страница отказа не объясняет, что делать")
	}
}

// Preflight приходит без cookie: браузер их в него не кладёт. Отказ здесь
// означал бы, что настоящий запрос браузер даже не отправит.
func TestPreflightPassesWithoutToken(t *testing.T) {
	s := newTestServer(t)

	rec := bare(s, http.MethodOptions, "/api/configs", map[string]string{
		"Origin": "http://localhost:3000",
	})

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight отвергнут с кодом %d", rec.Code)
	}
}

// Токен в адресе POST-запроса не должен приводить к перенаправлению: тело
// либо потерялось бы, либо ушло дважды.
func TestTokenInURLDoesNotRedirectPost(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/vpn/disconnect?"+auth.QueryParam+"="+s.token.Value(),
		strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code == http.StatusFound {
		t.Fatal("POST перенаправлен — тело запроса потеряно")
	}
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("токен в адресе не принят")
	}
}

// Токен нулевой длины или отсутствующий сервер отказывает всем: забытая
// настройка обязана ломать доступ заметно, а не открывать его молча.
func TestNilTokenDeniesEverything(t *testing.T) {
	var token *auth.Token
	if token.Matches("") || token.Matches("что угодно") {
		t.Fatal("незаданный токен кого-то пропустил")
	}
}
