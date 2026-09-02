// Команды, доступные интерфейсу.
//
// Окно живёт на tauri://localhost и до unix-сокета службы дотянуться не может
// — сокеты браузеру недоступны. Поэтому единственная дверь к API проходит
// здесь: интерфейс зовёт api_request, оболочка ходит по сокету и возвращает
// ответ как есть. Заодно это и вся «авторизация» интерфейса: чужая страница
// команд Tauri не видит, а своей не нужен ни токен, ни cookie.
use std::sync::Mutex;

use serde::Serialize;
use tauri::State;

use crate::api;

/// Разрешённые методы. Список, а не проверка «не пусто»: строка приходит из
/// интерфейса, и слать в сокет что угодно под видом метода не стоит.
const METHODS: [&str; 4] = ["GET", "POST", "PUT", "DELETE"];

/// Ответ службы в том виде, в каком его ждёт интерфейс: код отдельно, тело
/// отдельно. Разбирать JSON здесь незачем — оболочка в содержимое не смотрит.
#[derive(Debug, Serialize)]
pub struct ApiResponse {
    pub status: u16,
    pub body: String,
}

/// Запрос к службе.
///
/// Команда асинхронная, а сам поход по сокету уходит в отдельный поток, и это
/// не украшение. Синхронная команда Tauri выполняется в главном потоке — том
/// самом, который рисует окно. Каждый запрос к службе останавливал всё окно
/// на своё время, а запросы к тому же ещё и выстраивались в очередь: при
/// открытии главного экрана их уходит сразу несколько, и один из них —
/// замер задержки до сервера, который честно идёт по сети. Интерфейс замирал
/// на секунду-две ровно там, где должен был просто перерисоваться.
#[tauri::command]
pub async fn api_request(
    method: String,
    path: String,
    body: Option<String>,
) -> Result<ApiResponse, String> {
    let method = method.to_uppercase();
    if !METHODS.contains(&method.as_str()) {
        return Err(format!("недопустимый метод {method}"));
    }

    // Только /api: поток событий (/api/vpn/events) держит ответ открытым, и
    // обычный запрос к нему завис бы навсегда — за ним ходит events.rs.
    if !path.starts_with("/api/") || path.starts_with("/api/vpn/events") {
        return Err(format!("недопустимый путь {path}"));
    }

    // spawn_blocking, а не просто async: внутри обычные блокирующие чтение и
    // запись в сокет, и в асинхронном потоке они заняли бы рабочий поток
    // среды выполнения вместо главного — беда та же, только незаметнее.
    tauri::async_runtime::spawn_blocking(move || {
        api::request_raw(&method, &path, body.as_deref())
            .map(|(status, body)| ApiResponse { status, body })
            .map_err(|e| e.to_string())
    })
    .await
    .map_err(|e| format!("запрос не выполнен: {e}"))?
}

/// Состояние запуска службы.
///
/// Интерфейс показывает экран запуска, пока служба поднимается: пользователь
/// в это время вводит пароль в диалоге polkit, и это может занять минуту.
#[derive(Debug, Clone, Serialize)]
#[serde(tag = "state", rename_all = "lowercase")]
pub enum BootState {
    /// Служба поднимается.
    Starting,
    /// Служба отвечает — можно показывать интерфейс.
    Ready,
    /// Поднять не удалось.
    Failed { message: String },
}

/// Общее состояние запуска.
pub struct Boot(Mutex<BootState>);

impl Boot {
    pub fn new() -> Self {
        Boot(Mutex::new(BootState::Starting))
    }

    pub fn set(&self, state: BootState) {
        // Отравленный мьютекс здесь не беда: внутри простое значение, и
        // потерять его хуже, чем прочитать записанное упавшим потоком.
        let mut guard = self.0.lock().unwrap_or_else(|e| e.into_inner());
        *guard = state;
    }

    pub fn get(&self) -> BootState {
        self.0.lock().unwrap_or_else(|e| e.into_inner()).clone()
    }
}

/// Состояние запуска по запросу.
///
/// Событие о готовности может уйти раньше, чем интерфейс успел на него
/// подписаться, — поэтому при монтировании он спрашивает состояние сам, а
/// событиями только узнаёт о переменах.
#[tauri::command]
pub fn backend_state(boot: State<Boot>) -> BootState {
    boot.get()
}
