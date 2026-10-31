package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func get(t *testing.T, h http.Handler, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "127.0.0.1:7070"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandler(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>интерфейс</html>"), 0644)
	os.MkdirAll(filepath.Join(dir, "_nuxt"), 0755)
	os.WriteFile(filepath.Join(dir, "_nuxt", "app.js"), []byte("js"), 0644)

	var gotHost, gotOrigin string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotOrigin = r.Host, r.Header.Get("Origin")
		io.WriteString(w, `{"path":"`+r.URL.Path+`"}`)
	}))
	defer api.Close()

	target, _ := url.Parse(api.URL)
	h := newHandler(dir, target)

	t.Run("API уходит службе", func(t *testing.T) {
		rec := get(t, h, "/api/vpn/status", map[string]string{"Origin": "http://127.0.0.1:7070"})
		if !strings.Contains(rec.Body.String(), `"/api/vpn/status"`) {
			t.Fatalf("ответ %d %q", rec.Code, rec.Body.String())
		}
		if gotHost != target.Host {
			t.Errorf("служба получила Host %q, ожидали %q", gotHost, target.Host)
		}
		if gotOrigin != "http://127.0.0.1:7070" {
			t.Errorf("служба получила Origin %q", gotOrigin)
		}
	})

	t.Run("неизвестный путь отдаёт интерфейс", func(t *testing.T) {
		rec := get(t, h, "/settings", nil)
		if !strings.Contains(rec.Body.String(), "интерфейс") {
			t.Errorf("ответ %d %q", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("index.html не должен кэшироваться")
		}
	})

	t.Run("файлы сборки кэшируются", func(t *testing.T) {
		rec := get(t, h, "/_nuxt/app.js", nil)
		if rec.Body.String() != "js" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
			t.Errorf("ответ %d %q, Cache-Control %q", rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
		}
	})

	t.Run("чужая страница не доходит до службы", func(t *testing.T) {
		gotHost = ""
		rec := get(t, h, "/api/configs", map[string]string{"Origin": "https://evil.example"})
		if rec.Code != http.StatusForbidden || gotHost != "" {
			t.Errorf("код %d, служба получила запрос: %v", rec.Code, gotHost != "")
		}
	})

	t.Run("служба лежит", func(t *testing.T) {
		api.Close()
		rec := get(t, h, "/api/vpn/status", nil)
		if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), `"offline":true`) {
			t.Errorf("код %d %q", rec.Code, rec.Body.String())
		}
	})
}
