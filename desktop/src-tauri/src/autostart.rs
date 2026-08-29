// Автозапуск через XDG: ярлык в ~/.config/autostart подхватывает любая
// современная среда рабочего стола, отдельной службы для этого не нужно.
use std::fs;
use std::io;
use std::path::PathBuf;

pub fn entry_path() -> PathBuf {
    let home = std::env::var_os("HOME").map(PathBuf::from).unwrap_or_default();
    home.join(".config/autostart/awg-client.desktop")
}

pub fn is_enabled() -> bool {
    entry_path().is_file()
}

pub fn set_enabled(enabled: bool) -> io::Result<()> {
    let path = entry_path();

    if !enabled {
        return match fs::remove_file(&path) {
            Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(()),
            other => other,
        };
    }

    if let Some(dir) = path.parent() {
        fs::create_dir_all(dir)?;
    }

    // --hidden: при входе в систему поднимаем только значок в трее,
    // окно пользователь откроет сам, если оно ему нужно.
    let exe = std::env::current_exe()?;
    fs::write(
        &path,
        format!(
            "[Desktop Entry]\n\
             Type=Application\n\
             Name=AWG Client\n\
             Comment=Клиент AmneziaWG\n\
             Exec=\"{}\" --hidden\n\
             Icon=awg-client\n\
             Terminal=false\n\
             X-GNOME-Autostart-enabled=true\n",
            exe.display()
        ),
    )
}
