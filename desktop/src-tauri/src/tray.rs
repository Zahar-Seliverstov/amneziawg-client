// Значок в трее: состояние VPN и быстрое управление без открытия окна.
use std::sync::Mutex;
use std::thread;
use std::time::Duration;

use tauri::image::Image;
use tauri::menu::{CheckMenuItemBuilder, MenuBuilder, MenuItemBuilder, SubmenuBuilder};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Manager, Runtime};

use crate::{api, autostart};

const TRAY_ID: &str = "awg-main";

/// Всё, из чего собирается меню и подсказка. Меню перестраиваем только когда
/// снимок изменился — иначе бы дёргали GTK каждые две секунды впустую.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
struct Snapshot {
    online: bool,
    state: String,
    active: Option<String>,
    configs: Vec<api::Config>,
    autostart: bool,
}

impl Snapshot {
    fn fetch() -> Self {
        let autostart = autostart::is_enabled();

        let Ok(status) = api::status() else {
            return Snapshot {
                autostart,
                ..Snapshot::default()
            };
        };

        Snapshot {
            online: true,
            state: status.state.clone(),
            active: status.config_name.clone(),
            configs: api::configs().unwrap_or_default(),
            autostart,
        }
    }

    fn connected(&self) -> bool {
        self.state == "connected"
    }

    fn tooltip(&self) -> String {
        if !self.online {
            return "AWG Client — служба не запущена".into();
        }

        match self.state.as_str() {
            "connected" => match &self.active {
                Some(name) => format!("AWG Client — подключено: {name}"),
                None => "AWG Client — подключено".into(),
            },
            "connecting" => "AWG Client — подключение…".into(),
            "disconnecting" => "AWG Client — отключение…".into(),
            "error" => "AWG Client — ошибка подключения".into(),
            _ => "AWG Client — отключено".into(),
        }
    }
}

pub fn setup<R: Runtime>(app: &AppHandle<R>) -> tauri::Result<()> {
    let tray = TrayIconBuilder::with_id(TRAY_ID)
        .icon(idle_icon())
        .tooltip("AWG Client — запуск…")
        .menu(&menu(app, &Snapshot::default())?)
        .show_menu_on_left_click(false)
        .on_menu_event(on_menu_event)
        .on_tray_icon_event(|tray, event| {
            // Левый клик — показать окно: привычное поведение для трея.
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = event
            {
                show_window(tray.app_handle());
            }
        })
        .build(app)?;

    let handle = app.clone();
    thread::spawn(move || {
        let last: Mutex<Option<Snapshot>> = Mutex::new(None);

        loop {
            let snapshot = Snapshot::fetch();
            let changed = {
                let mut last = last.lock().unwrap();
                let changed = last.as_ref() != Some(&snapshot);
                if changed {
                    *last = Some(snapshot.clone());
                }
                changed
            };

            if changed {
                let handle = handle.clone();
                // Меню и значок трогаем только из главного потока: GTK
                // из чужого потока падает.
                let _ = handle.clone().run_on_main_thread(move || {
                    apply(&handle, &snapshot);
                });
            }

            thread::sleep(Duration::from_secs(2));
        }
    });

    let _ = tray;
    Ok(())
}

fn apply<R: Runtime>(app: &AppHandle<R>, snapshot: &Snapshot) {
    let Some(tray) = app.tray_by_id(TRAY_ID) else {
        return;
    };

    if let Ok(menu) = menu(app, snapshot) {
        let _ = tray.set_menu(Some(menu));
    }
    let _ = tray.set_tooltip(Some(snapshot.tooltip()));
    let _ = tray.set_icon(Some(if snapshot.connected() {
        connected_icon()
    } else {
        idle_icon()
    }));
}

fn menu<R: Runtime>(app: &AppHandle<R>, snapshot: &Snapshot) -> tauri::Result<tauri::menu::Menu<R>> {
    let open = MenuItemBuilder::with_id("open", "Открыть окно").build(app)?;

    let items = snapshot
        .configs
        .iter()
        .map(|config| {
            MenuItemBuilder::with_id(format!("connect:{}", config.id), &config.name)
                .enabled(!snapshot.connected())
                .build(app)
        })
        .collect::<tauri::Result<Vec<_>>>()?;

    let mut connect = SubmenuBuilder::new(app, "Подключить").enabled(!items.is_empty());
    for item in &items {
        connect = connect.item(item);
    }

    let disconnect = MenuItemBuilder::with_id("disconnect", "Отключить")
        .enabled(snapshot.connected())
        .build(app)?;
    let autostart = CheckMenuItemBuilder::with_id("autostart", "Запускать при входе в систему")
        .checked(snapshot.autostart)
        .build(app)?;
    let quit = MenuItemBuilder::with_id("quit", "Выход").build(app)?;

    MenuBuilder::new(app)
        .item(&open)
        .separator()
        .item(&connect.build()?)
        .item(&disconnect)
        .separator()
        .item(&autostart)
        .item(&quit)
        .build()
}

fn on_menu_event<R: Runtime>(app: &AppHandle<R>, event: tauri::menu::MenuEvent) {
    match event.id().as_ref() {
        "open" => show_window(app),
        "quit" => app.exit(0),
        "autostart" => {
            if let Err(e) = autostart::set_enabled(!autostart::is_enabled()) {
                eprintln!("Не удалось изменить автозапуск: {e}");
            }
        }
        "disconnect" => {
            // Запрос может занять секунды — из главного потока нельзя,
            // иначе замрёт всё окно.
            thread::spawn(|| {
                let _ = api::disconnect();
            });
        }
        id => {
            if let Some(config_id) = id.strip_prefix("connect:") {
                let config_id = config_id.to_string();
                thread::spawn(move || {
                    let _ = api::connect(&config_id);
                });
            }
        }
    }
}

fn show_window<R: Runtime>(app: &AppHandle<R>) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.show();
        let _ = window.unminimize();
        let _ = window.set_focus();
    }
}

fn idle_icon() -> Image<'static> {
    Image::from_bytes(include_bytes!("../icons/tray.png")).expect("значок трея не читается")
}

fn connected_icon() -> Image<'static> {
    Image::from_bytes(include_bytes!("../icons/tray-connected.png"))
        .expect("значок трея не читается")
}
