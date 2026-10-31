// Package localguard защищает петлевые HTTP-серверы от чужих страниц в
// браузере. Им пользуются и служба, и awg-ui.
package localguard

// Защита от чужих страниц в браузере.
//
// Служба слушает петлевой адрес, и до неё может дотянуться любая страница,
// открытая в браузере на этой же машине. Два способа этим воспользоваться
// закрываются здесь:
//
//   - подделка запроса (CSRF): чужая страница шлёт POST на 127.0.0.1 и
//     подключает, отключает или удаляет конфигурации. Ответ ей не нужен —
//     хватает побочного действия. Браузер помечает такой запрос заголовком
//     Origin с адресом чужого сайта;
//   - подмена имени (DNS rebinding): чужое имя начинает указывать на
//     127.0.0.1, и страница с этого имени читает ответы API как свои — в
//     том числе приватные ключи. Заголовок Host у такого запроса — чужое имя.
//
// CORS здесь не помогает и не нужен: интерфейс ходит в API через свой же
// адрес (awg-ui пересылает /api), то есть с того же источника.
//
// Ответ на отказ — JSON: интерфейс разбирает любой ответ API как JSON.

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Wrap пропускает только запросы, адресованные петлевому адресу и
// пришедшие не с чужого сайта.
func Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			deny(w, "Запрос адресован не локальному адресу")
			return
		}

		// Origin есть у запросов из браузера. Без него приходят curl и сам
		// awg-ui — им верим: процесс на этой машине и так мог бы прочитать
		// всё, что видит браузер.
		if origin := r.Header.Get("Origin"); origin != "" && !loopbackOrigin(origin) {
			deny(w, "Запрос с чужой страницы отклонён")
			return
		}

		// Sec-Fetch-Site — на случай браузера, не приславшего Origin (так
		// бывает с GET): чужой сайт браузер помечает именно здесь.
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			deny(w, "Запрос с чужой страницы отклонён")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// loopbackHost сообщает, назван ли в Host петлевой адрес. Порт не важен.
func loopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")

	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// loopbackOrigin сообщает, открыта ли страница-источник с петлевого адреса.
// "null" (файл с диска, песочница) сюда не проходит: у такой страницы нет
// адреса, по которому её можно было бы признать своей.
func loopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return loopbackHost(u.Host)
}

// deny отвечает отказом в том же виде, что и остальные ошибки API.
func deny(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
