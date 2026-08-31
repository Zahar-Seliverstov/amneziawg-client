// Десктопная оболочка AmneziaWG Web Client.
//
// Задача осознанно минимальна: поднять backend с правами root через polkit и
// показать в окне тот же самый веб-интерфейс, что и в браузере. Frontend при
// этом не меняется — backend раздаёт собранный UI на своём порту, поэтому
// window.location.hostname внутри страницы указывает ровно туда, куда
// composables и ждут (127.0.0.1:8081).
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod api;
mod tray;

use std::io;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::thread;
use std::time::{Duration, Instant};

use tauri::{Manager, Url, WindowEvent};

/// Сколько ждём backend. Запас большой: пользователь ещё вводит пароль в
/// диалоге polkit.
const STARTUP_TIMEOUT: Duration = Duration::from_secs(180);

/// Ищем бинарник backend'а: сначала рядом с оболочкой (так его кладёт
/// сборка Tauri), затем в ресурсах, затем — в дереве исходников, чтобы
/// `tauri dev` работал без установки.
fn locate_backend(app: &tauri::AppHandle) -> Option<PathBuf> {
    let mut candidates: Vec<PathBuf> = Vec::new();

    if let Some(path) = std::env::var_os("AWG_CLIENT_BIN") {
        candidates.push(PathBuf::from(path));
    }

    // Системная копия идёт первой: именно её путь прописан в polkit-политике
    // (make desktop-nopasswd), и только с ней запуск обходится без пароля.
    for dir in ["/usr/local/lib/awg-client", "/usr/lib/awg-client"] {
        candidates.push(PathBuf::from(dir).join("awg-client"));
    }

    let exe = std::env::current_exe().ok();
    let exe_dir = exe.as_deref().and_then(Path::parent).map(Path::to_path_buf);

    if let Some(dir) = &exe_dir {
        candidates.push(dir.join("awg-client"));
    }
    if let Ok(dir) = app.path().resource_dir() {
        candidates.push(dir.join("awg-client"));
    }

    // Дерево разработки: target/debug/... -> корень проекта.
    if let Some(dir) = &exe_dir {
        let mut up = dir.as_path();
        for _ in 0..6 {
            candidates.push(up.join("backend/build/awg-client"));
            match up.parent() {
                Some(parent) => up = parent,
                None => break,
            }
        }
    }

    candidates.into_iter().find(|p| is_executable(p))
}

fn is_executable(path: &Path) -> bool {
    path.metadata()
        .map(|m| m.is_file() && m.permissions().mode() & 0o111 != 0)
        .unwrap_or(false)
}

fn config_path() -> PathBuf {
    let home = std::env::var_os("HOME").map(PathBuf::from).unwrap_or_default();
    let dir = home.join(".config/awg-client");

    // Каталог создаём от пользователя: иначе его создаст root, и запуск
    // из терминала (./start.sh) потом упрётся в чужие права.
    let _ = std::fs::create_dir_all(&dir);
    let _ = std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o700));

    dir.join("config.json")
}

/// Запускаем backend от root. pkexec сам покажет системный диалог, а на
/// машине без polkit пробуем запустить напрямую — этого хватает, если
/// оболочку уже запустили с правами root.
///
/// Файл с токеном доступа backend пишет до того, как начинает слушать порт,
/// поэтому к моменту, когда порт откликнулся, токен уже на месте.
fn spawn_backend(bin: &Path) -> io::Result<Child> {
    let config = config_path();
    let parent_pid = std::process::id().to_string();
    // Путь к самой оболочке: backend прописывает его в ярлык автозапуска,
    // когда переключатель включают из настроек в окне.
    let desktop_exe = std::env::current_exe()
        .map(|p| p.display().to_string())
        .unwrap_or_default();

    let args = |cmd: &mut Command| {
        cmd.arg("-host")
            .arg("127.0.0.1")
            .arg("-port")
            .arg(api::PORT.to_string())
            .arg("-config")
            .arg(&config)
            // Backend работает от root, и убить его от имени пользователя уже
            // нельзя — поэтому он сам следит за оболочкой и гасится вместе с
            // ней, корректно разбирая VPN-соединение.
            .arg("-parent-pid")
            .arg(&parent_pid)
            .arg("-desktop-exe")
            .arg(&desktop_exe)
            .stdin(Stdio::null())
            .stdout(Stdio::inherit())
            .stderr(Stdio::inherit());
    };

    let mut pkexec = Command::new("pkexec");
    pkexec.arg(bin);
    args(&mut pkexec);

    match pkexec.spawn() {
        Ok(child) => Ok(child),
        Err(e) if e.kind() == io::ErrorKind::NotFound => {
            let mut direct = Command::new(bin);
            args(&mut direct);
            direct.spawn()
        }
        Err(e) => Err(e),
    }
}

/// Поднимаем backend и ждём, пока он реально начнёт отвечать.
fn ensure_backend(app: &tauri::AppHandle) -> Result<(), String> {
    if api::is_up() {
        return Ok(());
    }

    let bin = locate_backend(app)
        .ok_or_else(|| "Не найден бинарник awg-client. Собери его: make build".to_string())?;

    let mut child =
        spawn_backend(&bin).map_err(|e| format!("Не удалось запустить {}: {e}", bin.display()))?;

    let deadline = Instant::now() + STARTUP_TIMEOUT;
    loop {
        if api::is_up() {
            return Ok(());
        }

        // pkexec завершился раньше, чем поднялся порт: отказ в авторизации
        // или падение самого backend'а.
        match child.try_wait() {
            Ok(Some(status)) if !api::is_up() => {
                return Err(match status.code() {
                    Some(126) => "Запуск не подтверждён в диалоге polkit.".to_string(),
                    Some(127) => "polkit отказал в правах администратора.".to_string(),
                    Some(code) => format!("Служба завершилась с кодом {code}."),
                    None => "Служба была остановлена сигналом.".to_string(),
                });
            }
            Err(e) => return Err(format!("Не удалось дождаться службы: {e}")),
            _ => {}
        }

        if Instant::now() >= deadline {
            let _ = child.kill();
            return Err("Служба не ответила вовремя.".to_string());
        }

        thread::sleep(Duration::from_millis(150));
    }
}

/// Экранирование для передачи текста в JS без зависимости от serde.
fn js_string(value: &str) -> String {
    let mut out = String::with_capacity(value.len() + 2);
    out.push('"');
    for ch in value.chars() {
        match ch {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            c if (c as u32) < 0x20 => out.push_str(&format!("\\u{:04x}", c as u32)),
            c => out.push(c),
        }
    }
    out.push('"');
    out
}

/// Запуск в фоне: показываем только значок в трее (автозапуск при входе).
fn started_hidden() -> bool {
    std::env::args().skip(1).any(|arg| arg == "--hidden")
}

/// Настройка окружения под конкретный Linux-десктоп. Делается до инициализации
/// GTK, иначе поздно.
fn tune_environment() {
    let wayland = std::env::var_os("WAYLAND_DISPLAY").is_some();
    let kde = std::env::var("XDG_CURRENT_DESKTOP")
        .map(|d| d.to_uppercase().contains("KDE"))
        .unwrap_or(false);

    // Plasma сама выставляет GDK_BACKEND=wayland всем GTK-приложениям, поэтому
    // это значение считаем умолчанием сессии, а не осознанным выбором.
    // Осознанный выбор — любое другое значение или AWG_KEEP_WAYLAND=1.
    let backend_forced = match std::env::var("GDK_BACKEND") {
        Ok(value) => value.trim() != "wayland" && !value.trim().is_empty(),
        Err(_) => false,
    } || std::env::var_os("AWG_KEEP_WAYLAND").is_some();

    // GTK3 не умеет протокол xdg-decoration, поэтому под Wayland рисует
    // заголовок сам (CSD) — он почти вдвое выше системного и выглядит чужим
    // рядом с остальными окнами KDE. Через XWayland заголовок рисует KWin,
    // и окно получает ровно те же декорации, что у любого нативного
    // приложения. GDK_BACKEND из окружения не трогаем.
    if wayland && kde && !backend_forced {
        std::env::set_var("GDK_BACKEND", "x11");
    }

    // WebKitGTK с проприетарным драйвером NVIDIA рисует пустое окно, а под
    // Wayland ещё и падает («Error 71 dispatching to Wayland display»).
    // Отключение dmabuf-рендера лечит оба случая и нужно на любом backend'е.
    if Path::new("/sys/module/nvidia").exists()
        && std::env::var_os("WEBKIT_DISABLE_DMABUF_RENDERER").is_none()
    {
        std::env::set_var("WEBKIT_DISABLE_DMABUF_RENDERER", "1");
    }
}

fn main() {
    tune_environment();

    tauri::Builder::default()
        // Второй запуск (из меню, пока окно свёрнуто в трей) не плодит окно,
        // а показывает уже работающее.
        .plugin(tauri_plugin_single_instance::init(|app, args, _cwd| {
            // Второй запуск из автозапуска окно не открывает — только первый
            // экземпляр и так уже работает.
            if args.iter().any(|arg| arg == "--hidden") {
                return;
            }
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.show();
                let _ = window.unminimize();
                let _ = window.set_focus();
            }
        }))
        .setup(|app| {
            let handle = app.handle().clone();

            // Значок показывает оболочка рабочего стола, и поддержки SNI в
            // ней может не быть. Приложение из-за этого падать не должно.
            if let Err(e) = tray::setup(&handle) {
                eprintln!("Трей недоступен: {e}");
            }

            // Окно объявлено скрытым: так автозапуск не мигает им при входе,
            // а обычный запуск показывает его сразу со сплэшем.
            if !started_hidden() {
                if let Some(window) = app.get_webview_window("main") {
                    let _ = window.show();
                }
            }

            // Значок приложения и так стоит в трее и в панели задач — в
            // заголовке окна он лишний. GTK иначе подставит свой дефолтный.
            if let Some(window) = app.get_webview_window("main") {
                if let Ok(gtk_window) = window.gtk_window() {
                    gtk::prelude::GtkWindowExt::set_icon(&gtk_window, None::<&gtk::gdk_pixbuf::Pixbuf>);
                }
            }

            // Отдельный поток: окно со сплэшем должно отрисоваться сразу,
            // пока пользователь возится с диалогом polkit.
            thread::spawn(move || {
                let result = ensure_backend(&handle);
                let Some(window) = handle.get_webview_window("main") else {
                    return;
                };

                match result {
                    Ok(()) => match Url::parse(&api::url_with_token()) {
                        Ok(url) => {
                            if let Err(e) = window.navigate(url) {
                                let _ =
                                    window.eval(format!("window.awgError({})", js_string(&e.to_string())));
                            }
                        }
                        Err(e) => {
                            let _ = window.eval(format!("window.awgError({})", js_string(&e.to_string())));
                        }
                    },
                    Err(message) => {
                        let _ = window.eval(format!("window.awgError({})", js_string(&message)));
                    }
                }
            });

            Ok(())
        })
        .on_window_event(|window, event| {
            // Крестик прячет окно в трей: VPN должен продолжать работать.
            // Полное завершение — пункт «Выход» в меню значка.
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .run(tauri::generate_context!())
        .expect("не удалось запустить приложение");
}
