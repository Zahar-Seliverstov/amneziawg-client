// Package auth закрывает локальный API от остальных пользователей машины.
//
// Зачем. API слушает петлевой интерфейс, а петлевой интерфейс — общий для
// всех, кто вошёл в систему. До сих пор проверялся только заголовок Origin,
// и он защищал ровно от одного: от страницы в браузере. Любой процесс любого
// другого пользователя мог обратиться к API обычным curl — без Origin — и
// получить `GET /api/configs`, то есть приватные ключи всех подключений, а
// заодно и управление соединением. Работает всё это от root.
//
// Как. При запуске рождается секрет и кладётся в файл с правами 0600 в доме
// пользователя рабочего стола. Прочитать его может только он; остальным
// остаётся отказ.
//
// Секрет обменивается на cookie: браузер получает её один раз по ссылке с
// токеном и дальше носит сам — в том числе на рукопожатие WebSocket, где
// заголовок задать нечем. Из адресной строки токен при этом исчезает и не
// оседает в истории.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/user/amnezia-web-client/internal/desktopuser"
)

// CookieName — имя cookie, в которую обменивается токен.
const CookieName = "awg_token"

// QueryParam — имя параметра, которым токен передаётся по ссылке.
const QueryParam = "token"

// tokenBytes — длина секрета. 32 байта это 256 бит: перебрать нельзя, а
// столько же берут все, кто решает ту же задачу.
const tokenBytes = 32

// Token — секрет доступа к API.
type Token struct {
	value string
	path  string
}

// New порождает новый секрет.
//
// Новый на каждый запуск: у токена нет срока, и единственное, что ограничивает
// его жизнь, — перезапуск клиента. Переиспользовать прежний значило бы хранить
// вечный ключ от root-процесса.
func New() (*Token, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("не удалось породить токен: %w", err)
	}

	// base64url без набивки: токен ездит в адресной строке, и знаки +/=
	// пришлось бы экранировать.
	return &Token{value: base64.RawURLEncoding.EncodeToString(buf)}, nil
}

// Value возвращает сам секрет.
func (t *Token) Value() string { return t.value }

// Path — файл, в который токен записан. Пусто, пока не записан.
func (t *Token) Path() string { return t.path }

// Matches сравнивает предъявленное значение с секретом.
//
// Сравнение постоянного времени: обычное прерывается на первом несовпавшем
// байте, и по времени ответа секрет подбирается посимвольно.
func (t *Token) Matches(candidate string) bool {
	if t == nil || candidate == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(t.value), []byte(candidate)) == 1
}

// Save кладёт токен в файл рядом с настройками и отдаёт его пользователю
// рабочего стола.
//
// Права 0600 — единственное, что отделяет API от остальных пользователей
// машины, поэтому файл создаётся сразу закрытым, а не открывается и потом
// закрывается: между этими двумя действиями его успели бы прочитать.
func (t *Token) Save(path string, owner desktopuser.User) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	// O_TRUNC, а не удаление с пересозданием: имя остаётся занятым всё время,
	// и подменить файл на свой между вызовами нельзя.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	// Права выставляем явно: существовавший файл мог иметь другие, а O_CREATE
	// на него не влияет.
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.WriteString(t.value + "\n"); err != nil {
		return err
	}

	t.path = path
	return owner.Own(path)
}

// FilePath — где лежит токен при заданном пути к настройкам.
func FilePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "token")
}
