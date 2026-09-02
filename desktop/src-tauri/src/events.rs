// Мост между потоком статуса службы и событиями окна.
//
// Служба держит открытым /api/vpn/events и построчно пишет туда состояние
// подключения (NDJSON). Читает поток оболочка, а не страница: до unix-сокета
// из окна не дотянуться, да и переподключение уместнее здесь — окно может
// быть спрятано в трей или перезагружено, а следить за VPN нужно всё равно.
use std::io::BufRead;
use std::thread;
use std::time::Duration;

use tauri::{AppHandle, Emitter, Runtime};

use crate::api;

/// Событие с новым состоянием подключения. Тело — ответ службы как есть.
pub const STATUS_EVENT: &str = "vpn:status";

/// Событие обрыва: службы нет или поток закрылся.
pub const OFFLINE_EVENT: &str = "vpn:offline";

/// Пауза перед повторной попыткой. Растёт до полуминуты, но после удачного
/// соединения сбрасывается: короткий обрыв восстанавливается почти мгновенно,
/// а исчезнувшая служба не заставляет стучаться в неё без конца.
const RETRY_MIN: Duration = Duration::from_secs(1);
const RETRY_MAX: Duration = Duration::from_secs(30);

/// Запускает чтение потока в отдельном потоке ОС. Возврата не предполагает:
/// живёт столько же, сколько приложение.
pub fn watch<R: Runtime>(app: AppHandle<R>) {
    thread::spawn(move || {
        let mut retry = RETRY_MIN;

        loop {
            match api::open_events() {
                Ok(reader) => {
                    retry = RETRY_MIN;

                    for line in reader.lines() {
                        let Ok(line) = line else { break };
                        if line.trim().is_empty() {
                            continue;
                        }

                        match serde_json::from_str::<serde_json::Value>(&line) {
                            Ok(status) => {
                                let _ = app.emit(STATUS_EVENT, status);
                            }
                            Err(e) => eprintln!("Строка потока статуса не разобрана: {e}"),
                        }
                    }
                }
                Err(e) => eprintln!("Поток статуса недоступен: {e}"),
            }

            // Сюда попадаем и после обрыва потока, и после неудачи связи —
            // в обоих случаях интерфейс должен узнать, что данные устарели.
            let _ = app.emit(OFFLINE_EVENT, ());

            thread::sleep(retry);
            retry = (retry * 2).min(RETRY_MAX);
        }
    });
}
