//! Оболочка Tauri: запускает Go-бэкенд (sidecar) и показывает его интерфейс в окне.

use std::net::{SocketAddr, TcpStream};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use tauri::{Manager, RunEvent, Url, WebviewUrl, WebviewWindowBuilder};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

const BACKEND_ADDR: &str = "127.0.0.1:8765";

/// Процесс бэкенда, запущенный приложением (None — бэкенд уже работал, например `go run` в dev).
struct Backend(Mutex<Option<CommandChild>>);

fn backend_up() -> bool {
    let addr: SocketAddr = BACKEND_ADDR.parse().expect("адрес бэкенда");
    TcpStream::connect_timeout(&addr, Duration::from_millis(300)).is_ok()
}

fn wait_backend(timeout: Duration, want_up: bool) -> bool {
    let start = Instant::now();
    while start.elapsed() < timeout {
        if backend_up() == want_up {
            return true;
        }
        std::thread::sleep(Duration::from_millis(150));
    }
    false
}

/// Мягко останавливает бэкенд (SIGTERM: сессия сохраняется), при зависании — kill.
fn stop_backend(child: CommandChild) {
    #[cfg(unix)]
    {
        let _ = std::process::Command::new("kill")
            .args(["-TERM", &child.pid().to_string()])
            .status();
        if wait_backend(Duration::from_secs(8), false) {
            return;
        }
    }
    let _ = child.kill();
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .setup(|app| {
            let window = WebviewWindowBuilder::new(app, "main", WebviewUrl::App("index.html".into()))
                .title("УЗИ-парсер")
                .inner_size(1280.0, 880.0)
                .min_inner_size(900.0, 600.0)
                .build()?;

            let mut child_slot = None;
            if !backend_up() {
                let (mut rx, child) = app
                    .shell()
                    .sidecar("uzi-backend")?
                    .args(["--watch-parent"])
                    .spawn()?;
                child_slot = Some(child);
                tauri::async_runtime::spawn(async move {
                    while let Some(ev) = rx.recv().await {
                        match ev {
                            CommandEvent::Stdout(line) | CommandEvent::Stderr(line) => {
                                eprintln!("[backend] {}", String::from_utf8_lossy(&line).trim_end());
                            }
                            CommandEvent::Terminated(p) => eprintln!("[backend] завершён: {:?}", p.code),
                            _ => {}
                        }
                    }
                });
            }
            app.manage(Backend(Mutex::new(child_slot)));

            std::thread::spawn(move || {
                if wait_backend(Duration::from_secs(20), true) {
                    let url = Url::parse(&format!("http://{BACKEND_ADDR}/")).expect("url");
                    let _ = window.navigate(url);
                } else {
                    let _ = window.eval(
                        "document.getElementById('t').textContent='Не удалось запустить бэкенд (порт 8765 занят?)'",
                    );
                }
            });
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("ошибка запуска Tauri");

    app.run(|handle, event| {
        if let RunEvent::Exit = event {
            if let Some(state) = handle.try_state::<Backend>() {
                if let Some(child) = state.0.lock().expect("mutex").take() {
                    stop_backend(child);
                }
            }
        }
    });
}
