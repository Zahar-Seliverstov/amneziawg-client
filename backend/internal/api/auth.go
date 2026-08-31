package api

import (
	"net/http"
	"strings"

	"github.com/user/amnezia-web-client/internal/auth"
)

// authMiddleware пропускает дальше только тех, кто предъявил токен.
//
// Закрыто всё, а не только /api. Правило из одного предложения проверяется
// глазами, а список исключений — нет: стоит добавить эндпоинт и забыть про
// список, и дыра появится молча. Собранный интерфейс секретов не содержит, но
// и открывать его незачем — без доступа к API он всё равно бесполезен.
//
// Токен принимается тремя способами, и это не избыточность:
//   - cookie — как ходит браузер, включая рукопожатие WebSocket, где
//     заголовок задать нечем;
//   - Authorization: Bearer — как ходят значок в трее, curl и диагностика;
//   - параметр в адресе — единственный способ выдать cookie в первый раз.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Обмен токена на cookie возможен на любом пути, а не только на
		// главной: если cookie потеряется, вернуть доступ должно быть можно
		// тем же адресом, по которому пришли.
		presented := r.URL.Query().Get(auth.QueryParam)
		viaQuery := presented != "" && s.token.Matches(presented)

		if viaQuery {
			setTokenCookie(w, presented)

			// Убираем токен из адреса: он не должен осесть в истории
			// браузера, в заголовке Referer и в закладке.
			if redirect, ok := withoutToken(r); ok {
				http.Redirect(w, r, redirect, http.StatusFound)
				return
			}
		}

		if !viaQuery && !s.authorized(r) {
			s.denyAccess(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// authorized проверяет cookie и заголовок.
func (s *Server) authorized(r *http.Request) bool {
	if c, err := r.Cookie(auth.CookieName); err == nil && s.token.Matches(c.Value) {
		return true
	}

	if value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return s.token.Matches(strings.TrimSpace(value))
	}

	return false
}

func setTokenCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:  auth.CookieName,
		Value: token,
		Path:  "/",
		// HttpOnly: токен не нужен коду страницы, а так его не достать ни из
		// скрипта, ни из расширения браузера.
		HttpOnly: true,
		// SameSite=Strict: cookie не уедет по чужой ссылке, поэтому чужая
		// страница не обратится к API от имени пользователя.
		SameSite: http.SameSiteStrictMode,
		// Secure не ставим намеренно: соединение петлевое и без TLS, а с этим
		// признаком браузер просто не сохранил бы cookie.
	})
}

// withoutToken возвращает тот же адрес без параметра с токеном.
//
// Только для запросов, которые можно безопасно повторить: перенаправлять POST
// значило бы либо потерять тело, либо отправить его дважды.
func withoutToken(r *http.Request) (string, bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return "", false
	}

	// Рукопожатие WebSocket — тоже GET, но перенаправление его убивает:
	// клиент по адресу из Location не пойдёт, а увидит обрыв соединения.
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return "", false
	}

	url := *r.URL
	query := url.Query()
	query.Del(auth.QueryParam)
	url.RawQuery = query.Encode()

	return url.RequestURI(), true
}

// denyAccess объясняет отказ на языке того, кто спрашивал: клиенту API нужен
// разбираемый JSON, человеку в браузере — страница со словами.
func (s *Server) denyAccess(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api") {
		jsonError(w, "Нужен токен доступа", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)

	if r.Method != http.MethodHead {
		w.Write([]byte(deniedPage))
	}
}

const deniedPage = `<!doctype html>
<meta charset="utf-8">
<title>Нужен токен доступа</title>
<style>
  body { margin: 0; display: grid; place-items: center; min-height: 100vh;
         background: #0e0f13; color: #eceef1;
         font: 15px/1.55 system-ui, sans-serif; }
  main { max-width: 32rem; padding: 2rem; }
  h1 { font-size: 1.25rem; margin: 0 0 .9rem; }
  p { margin: 0 0 .8rem; color: #939aa6; }
  code { display: block; margin-top: .75rem; padding: .6rem .8rem;
         background: #101216; border: 1px solid #262a32; border-radius: 9px;
         color: #eceef1; font-size: 13px; word-break: break-all; }
</style>
<main>
  <h1>Нужен токен доступа</h1>
  <p>Клиент управляет сетью с правами администратора, поэтому обращаться к нему
     может только тот, кто его запустил.</p>
  <p>Откройте приложение по ссылке с токеном — она печатается при запуске.
     Сам токен лежит в файле, доступном только вам:</p>
  <code>~/.config/awg-client/token</code>
</main>
`
