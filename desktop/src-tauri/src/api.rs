// Тонкий клиент службы: единственное место, где оболочка говорит с backend'ом.
//
// Транспорт — unix-сокет с правами 0600 в каталоге времени выполнения сессии.
// Права файла и есть проверка доступа: служба работает от root, управляет
// сетью и отдаёт приватные ключи всех подключений, поэтому дотянуться до неё
// должен только тот, кто её запустил. Ни токена, ни cookie, ни TCP-порта для
// этого не нужно.
//
// HTTP пишем руками поверх сокета: соединение локальное, без TLS и редиректов,
// а тащить ради десятка запросов полноценный http-клиент незачем. Запрос
// уходит как HTTP/1.0 — Go тогда не включает chunked-кодирование и просто
// закрывает соединение, так что тело читается до EOF.
use std::io::{self, BufRead, BufReader, Read, Write};
use std::os::unix::net::UnixStream;
use std::path::PathBuf;
use std::time::Duration;

use serde::Deserialize;

/// Предел на обычный запрос. Подключение к VPN отвечает не мгновенно.
const REQUEST_TIMEOUT: Duration = Duration::from_secs(15);

/// Предел на проверку готовности. Короткий намеренно: проверка идёт в цикле
/// опроса, и повиснуть в ней на общем сроке значит не дождаться готовности.
const READY_TIMEOUT: Duration = Duration::from_millis(700);

const WRITE_TIMEOUT: Duration = Duration::from_secs(5);

/// Сокет API.
///
/// $XDG_RUNTIME_DIR — правильное место для сокетов: каталог принадлежит
/// пользователю, живёт ровно столько же, сколько сессия, и очищается системой.
/// Без него (сессия без systemd-logind) откатываемся к каталогу настроек.
pub fn socket_path() -> PathBuf {
    if let Some(runtime) = std::env::var_os("XDG_RUNTIME_DIR") {
        if !runtime.is_empty() {
            return PathBuf::from(runtime).join("awg-client/api.sock");
        }
    }

    let home = std::env::var_os("HOME").map(PathBuf::from).unwrap_or_default();
    home.join(".config/awg-client/api.sock")
}

#[derive(Debug, Clone, Deserialize, PartialEq, Eq)]
pub struct Status {
    pub state: String,
    #[serde(default)]
    pub config_name: Option<String>,
    #[serde(default)]
    pub error: Option<String>,
}

#[derive(Debug, Clone, Deserialize, PartialEq, Eq)]
pub struct Config {
    pub id: String,
    pub name: String,
}

/// Служба отвечает НАМ — по тому сокету, который сейчас на месте.
///
/// Одного существующего файла сокета мало: имя переживает смерть процесса, и
/// проверка «файл есть» проходила бы по службе, которой уже нет. Ответ 200
/// означает разом и что служба жива, и что это именно она заняла сокет.
pub fn is_ready() -> bool {
    matches!(exchange("GET", "/api/vpn/status", "", READY_TIMEOUT), Ok((200, _)))
}

pub fn status() -> io::Result<Status> {
    parse(&request("GET", "/api/vpn/status", None)?)
}

pub fn configs() -> io::Result<Vec<Config>> {
    parse(&request("GET", "/api/configs", None)?)
}

pub fn connect(config_id: &str) -> io::Result<()> {
    // Тело собирает serde_json, а не format!. Идентификатор приходит из
    // ответа службы, но склеивать JSON строками всё равно нельзя: вырезание
    // кавычек руками — это ровно тот приём, который однажды пропускает
    // управляющий символ и превращает запрос в другой запрос.
    let body = serde_json::json!({ "config_id": config_id }).to_string();
    request("POST", "/api/vpn/connect", Some(&body)).map(|_| ())
}

pub fn disconnect() -> io::Result<()> {
    request("POST", "/api/vpn/disconnect", Some("{}")).map(|_| ())
}

/// Запрос вместе с кодом ответа: интерфейсу нужен и код, и тело — по коду он
/// отличает отказ от успеха, а тело разбирает как JSON в обоих случаях.
pub fn request_raw(method: &str, path: &str, body: Option<&str>) -> io::Result<(u16, String)> {
    exchange(method, path, body.unwrap_or_default(), REQUEST_TIMEOUT)
}

/// Поток изменений статуса: ответ остаётся открытым, и в него построчно
/// приходит JSON. Возвращается читатель, стоящий на первой строке тела.
///
/// Срока чтения нет намеренно: между сменами состояния поток молчит сколько
/// угодно, и любой таймаут здесь означал бы разрыв на ровном месте.
pub fn open_events() -> io::Result<BufReader<UnixStream>> {
    let stream = UnixStream::connect(socket_path())?;
    stream.set_read_timeout(None)?;
    stream.set_write_timeout(Some(WRITE_TIMEOUT))?;

    write_request(&stream, "GET", "/api/vpn/events", "")?;

    let mut reader = BufReader::new(stream);
    match read_head(&mut reader)? {
        200 => Ok(reader),
        code => Err(io::Error::other(format!("поток событий отвечает {code}"))),
    }
}

fn request(method: &str, path: &str, body: Option<&str>) -> io::Result<String> {
    let (code, body) = request_raw(method, path, body)?;

    if code == 200 || code == 204 {
        return Ok(body);
    }
    Err(io::Error::other(format!("HTTP {code}: {}", body.trim())))
}

fn exchange(
    method: &str,
    path: &str,
    body: &str,
    read_timeout: Duration,
) -> io::Result<(u16, String)> {
    // Своего connect_timeout у UnixStream нет, но он и не нужен: локальный
    // сокет либо принимает соединение сразу, либо отказывает.
    let stream = UnixStream::connect(socket_path())?;
    stream.set_read_timeout(Some(read_timeout))?;
    stream.set_write_timeout(Some(WRITE_TIMEOUT))?;

    write_request(&stream, method, path, body)?;

    let mut reader = BufReader::new(stream);
    let code = read_head(&mut reader)?;

    let mut raw = Vec::new();
    reader.read_to_end(&mut raw)?;

    Ok((code, String::from_utf8_lossy(&raw).into_owned()))
}

fn write_request(mut out: impl Write, method: &str, path: &str, body: &str) -> io::Result<()> {
    let head = format!(
        "{method} {path} HTTP/1.0\r\n\
         Host: localhost\r\n\
         Content-Type: application/json\r\n\
         Content-Length: {}\r\n\r\n",
        body.len()
    );

    out.write_all(head.as_bytes())?;
    out.write_all(body.as_bytes())?;
    out.flush()
}

/// Читает строку статуса и заголовки, оставляя читатель на начале тела.
fn read_head(reader: &mut BufReader<UnixStream>) -> io::Result<u16> {
    let mut line = String::new();
    reader.read_line(&mut line)?;

    let code = line
        .split_whitespace()
        .nth(1)
        .and_then(|code| code.parse().ok())
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, "битый ответ службы"))?;

    loop {
        let mut header = String::new();
        // Конец заголовков — пустая строка; ноль означает обрыв соединения.
        if reader.read_line(&mut header)? == 0 || header.trim().is_empty() {
            return Ok(code);
        }
    }
}

fn parse<T: for<'de> Deserialize<'de>>(body: &str) -> io::Result<T> {
    serde_json::from_str(body).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))
}
