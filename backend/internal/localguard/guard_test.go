package localguard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWrap(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	h := Wrap(ok)

	cases := []struct {
		name    string
		host    string
		headers map[string]string
		want    int
	}{
		{"петлевой адрес", "127.0.0.1:7071", nil, http.StatusOK},
		{"localhost", "localhost:7070", nil, http.StatusOK},
		{"IPv6", "[::1]:7071", nil, http.StatusOK},
		{"свой интерфейс", "127.0.0.1:7071", map[string]string{"Origin": "http://127.0.0.1:7070"}, http.StatusOK},
		{"подмена имени", "evil.example:7071", nil, http.StatusForbidden},
		{"чужой адрес в Host", "192.168.1.5:7071", nil, http.StatusForbidden},
		{"чужая страница", "127.0.0.1:7071", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"страница без адреса", "127.0.0.1:7071", map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"чужой сайт без Origin", "127.0.0.1:7071", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
	}

	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/vpn/connect", nil)
		req.Host = c.host
		for k, v := range c.headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != c.want {
			t.Errorf("%s: код %d, ожидали %d", c.name, rec.Code, c.want)
		}
	}
}
