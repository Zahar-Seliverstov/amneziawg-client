// Тонкий клиент backend'а: только то, что нужно трею.
//
// HTTP пишем руками поверх TcpStream — соединение локальное, без TLS и
// редиректов, а тащить ради трёх запросов полноценный http-клиент незачем.
// Запрос уходит как HTTP/1.0: Go тогда не включает chunked-кодирование и
// просто закрывает соединение, так что тело читается до EOF.
use std::io::{self, Read, Write};
use std::net::{Ipv4Addr, SocketAddr, TcpStream};
use std::path::PathBuf;
use std::time::Duration;

use serde::Deserialize;

pub const PORT: u16 = 8081;

pub fn url() -> String {
    format!("http://127.0.0.1:{PORT}/")
}

pub fn addr() -> SocketAddr {
    SocketAddr::from((Ipv4Addr::LOCALHOST, PORT))
}

/// Файл с токеном доступа. Его пишет backend при запуске с правами 0600 на
/// имя пользователя рабочего стола — прочитать может только он.
pub fn token_path() -> PathBuf {
    let home = std::env::var_os("HOME").map(PathBuf::from).unwrap_or_default();
    home.join(".config/awg-client/token")
}

/// Токен доступа к API.
///
/// Читается при каждом запросе, а не запоминается: backend рождает новый
/// токен на каждый запуск, и запомненный протух бы после его перезапуска.
/// Файл крошечный, а запросов здесь единицы в секунду.
pub fn token() -> Option<String> {
    let raw = std::fs::read_to_string(token_path()).ok()?;
    let trimmed = raw.trim().to_string();
    (!trimmed.is_empty()).then_some(trimmed)
}

/// Адрес интерфейса вместе с токеном: по нему backend выдаёт cookie и
/// перенаправляет на чистый адрес, чтобы токен не осел в истории.
pub fn url_with_token() -> String {
    match token() {
        Some(t) => format!("http://127.0.0.1:{PORT}/?token={t}"),
        None => url(),
    }
}

/// Backend уже слушает порт?
pub fn is_up() -> bool {
    TcpStream::connect_timeout(&addr(), Duration::from_millis(300)).is_ok()
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

pub fn status() -> io::Result<Status> {
    parse(&request("GET", "/api/vpn/status", None)?)
}

pub fn configs() -> io::Result<Vec<Config>> {
    parse(&request("GET", "/api/configs", None)?)
}

pub fn connect(config_id: &str) -> io::Result<()> {
    // Тело собирает serde_json, а не format!. Идентификатор приходит из
    // ответа backend'а, но склеивать JSON строками всё равно нельзя: вырезание
    // кавычек руками — это ровно тот приём, который однажды пропускает
    // управляющий символ и превращает запрос в другой запрос.
    let body = serde_json::json!({ "config_id": config_id }).to_string();
    request("POST", "/api/vpn/connect", Some(&body)).map(|_| ())
}

pub fn disconnect() -> io::Result<()> {
    request("POST", "/api/vpn/disconnect", Some("{}")).map(|_| ())
}

fn parse<T: for<'de> Deserialize<'de>>(body: &str) -> io::Result<T> {
    serde_json::from_str(body).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))
}

fn request(method: &str, path: &str, body: Option<&str>) -> io::Result<String> {
    let mut stream = TcpStream::connect_timeout(&addr(), Duration::from_millis(500))?;
    stream.set_read_timeout(Some(Duration::from_secs(15)))?;
    stream.set_write_timeout(Some(Duration::from_secs(5)))?;

    let body = body.unwrap_or_default();

    // Без токена API отвечает отказом: он закрыт от остальных пользователей
    // машины. Здесь заголовок, а не cookie, — так ходят все клиенты, кроме
    // браузера.
    let authorization = match token() {
        Some(t) => format!("Authorization: Bearer {t}\r\n"),
        None => String::new(),
    };

    let head = format!(
        "{method} {path} HTTP/1.0\r\n\
         Host: 127.0.0.1:{PORT}\r\n\
         Content-Type: application/json\r\n\
         {authorization}\
         Content-Length: {}\r\n\r\n",
        body.len()
    );
    stream.write_all(head.as_bytes())?;
    stream.write_all(body.as_bytes())?;
    stream.flush()?;

    let mut raw = Vec::new();
    stream.read_to_end(&mut raw)?;
    let raw = String::from_utf8_lossy(&raw).into_owned();

    let (head, body) = raw
        .split_once("\r\n\r\n")
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, "битый ответ backend'а"))?;

    let ok = head
        .lines()
        .next()
        .map(|line| line.contains(" 200 ") || line.contains(" 204 "))
        .unwrap_or(false);

    if !ok {
        let first = head.lines().next().unwrap_or("нет статуса");
        return Err(io::Error::other(format!("{first}: {}", body.trim())));
    }

    Ok(body.to_string())
}
