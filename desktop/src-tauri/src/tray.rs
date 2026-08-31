// Значок в трее: состояние VPN и быстрое управление без открытия окна.
//
// Реализация — собственный StatusNotifierItem (ksni), а не трей из Tauri.
// Тот на Linux работает через libayatana-appindicator, которая не различает
// кнопки мыши: меню открывается и по левому клику, а сами события клика до
// приложения не доходят вовсе. SNI отдаёт левый клик как Activate, поэтому
// левая кнопка открывает окно, а правая — меню.
use std::sync::OnceLock;
use std::thread;
use std::time::Duration;

use ksni::blocking::TrayMethods;
use ksni::menu::{StandardItem, SubMenu};
use ksni::{Icon, MenuItem, ToolTip};
use tauri::{AppHandle, Manager, Runtime};

use crate::api;

const POLL: Duration = Duration::from_secs(2);

/// Всё, из чего собирается меню и подсказка.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
struct Snapshot {
    online: bool,
    state: String,
    active: Option<String>,
    configs: Vec<api::Config>,
}

impl Snapshot {
    fn fetch() -> Self {
        let Ok(status) = api::status() else {
            return Snapshot::default();
        };

        Snapshot {
            online: true,
            state: status.state.clone(),
            active: status.config_name.clone(),
            configs: api::configs().unwrap_or_default(),
        }
    }

    fn connected(&self) -> bool {
        self.state == "connected"
    }

    /// Соединение «живёт»: установлено либо восстанавливается. Отключить его
    /// нужно уметь в обоих случаях — иначе бесконечное переподключение из
    /// трея не прервать.
    fn live(&self) -> bool {
        matches!(self.state.as_str(), "connected" | "connecting" | "reconnecting")
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
            "reconnecting" => "AWG Client — связь потеряна, восстанавливаю…".into(),
            "disconnecting" => "AWG Client — отключение…".into(),
            "error" => "AWG Client — ошибка подключения".into(),
            _ => "AWG Client — отключено".into(),
        }
    }
}

struct Tray<R: Runtime> {
    app: AppHandle<R>,
    snapshot: Snapshot,
}

impl<R: Runtime> ksni::Tray for Tray<R> {
    fn id(&self) -> String {
        "awg-client".into()
    }

    fn title(&self) -> String {
        "AWG Client".into()
    }

    /// Левый клик по значку — показать окно.
    fn activate(&mut self, _x: i32, _y: i32) {
        on_main(&self.app, show_window);
    }

    fn icon_pixmap(&self) -> Vec<Icon> {
        vec![if self.snapshot.connected() {
            connected_icon()
        } else {
            idle_icon()
        }]
    }

    fn tool_tip(&self) -> ToolTip {
        ToolTip {
            title: self.snapshot.tooltip(),
            ..Default::default()
        }
    }

    fn menu(&self) -> Vec<MenuItem<Self>> {
        let live = self.snapshot.live();

        let configs: Vec<MenuItem<Self>> = self
            .snapshot
            .configs
            .iter()
            .map(|config| {
                let id = config.id.clone();
                StandardItem {
                    label: config.name.clone(),
                    activate: Box::new(move |_: &mut Self| {
                        // Запрос может занять секунды, а пока обработчик не
                        // вернулся — меню висит.
                        let id = id.clone();
                        thread::spawn(move || {
                            let _ = api::connect(&id);
                        });
                    }),
                    ..Default::default()
                }
                .into()
            })
            .collect();

        vec![
            SubMenu {
                label: "Подключить".into(),
                enabled: !live && !configs.is_empty(),
                submenu: configs,
                ..Default::default()
            }
            .into(),
            StandardItem {
                label: "Отключить".into(),
                enabled: live,
                activate: Box::new(|_: &mut Self| {
                    thread::spawn(|| {
                        let _ = api::disconnect();
                    });
                }),
                ..Default::default()
            }
            .into(),
            MenuItem::Separator,
            StandardItem {
                label: "Выход".into(),
                icon_name: "application-exit".into(),
                activate: Box::new(|this: &mut Self| on_main(&this.app, |app| app.exit(0))),
                ..Default::default()
            }
            .into(),
        ]
    }
}

pub fn setup<R: Runtime>(app: &AppHandle<R>) -> Result<(), ksni::Error> {
    let handle = Tray {
        app: app.clone(),
        snapshot: Snapshot::default(),
    }
    .spawn()?;

    thread::spawn(move || {
        let mut last: Option<Snapshot> = None;

        loop {
            let snapshot = Snapshot::fetch();

            // Значок и меню перерисовываем только при изменениях: иначе
            // каждые две секунды дёргали бы D-Bus впустую.
            if last.as_ref() != Some(&snapshot) {
                last = Some(snapshot.clone());
                // None — служба трея остановлена, следить больше не за чем.
                if handle
                    .update(move |tray: &mut Tray<R>| tray.snapshot = snapshot)
                    .is_none()
                {
                    return;
                }
            }

            thread::sleep(POLL);
        }
    });

    Ok(())
}

/// Окно и завершение приложения трогаем только из главного потока: GTK из
/// чужого потока падает, а обработчики ksni живут в своём.
fn on_main<R: Runtime, F: FnOnce(&AppHandle<R>) + Send + 'static>(app: &AppHandle<R>, f: F) {
    let app = app.clone();
    let _ = app.clone().run_on_main_thread(move || f(&app));
}

fn show_window<R: Runtime>(app: &AppHandle<R>) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.show();
        let _ = window.unminimize();
        let _ = window.set_focus();
    }
}

fn idle_icon() -> Icon {
    static ICON: OnceLock<Icon> = OnceLock::new();
    ICON.get_or_init(|| decode(include_bytes!("../icons/tray.png")))
        .clone()
}

fn connected_icon() -> Icon {
    static ICON: OnceLock<Icon> = OnceLock::new();
    ICON.get_or_init(|| decode(include_bytes!("../icons/tray-connected.png")))
        .clone()
}

/// SNI принимает только сырой ARGB32, поэтому PNG раскодируем сами.
fn decode(bytes: &[u8]) -> Icon {
    let mut reader = png::Decoder::new(bytes)
        .read_info()
        .expect("значок трея не читается");
    let mut data = vec![0; reader.output_buffer_size()];
    let info = reader.next_frame(&mut data).expect("значок трея не читается");

    assert_eq!(info.color_type, png::ColorType::Rgba, "значок трея не RGBA");
    assert_eq!(
        info.bit_depth,
        png::BitDepth::Eight,
        "значок трея не 8-битный"
    );

    data.truncate(info.buffer_size());
    for pixel in data.chunks_exact_mut(4) {
        pixel.rotate_right(1); // RGBA -> ARGB
    }

    Icon {
        width: info.width as i32,
        height: info.height as i32,
        data,
    }
}
