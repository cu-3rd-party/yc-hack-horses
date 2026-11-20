//! Tauri shell yc-hack-horses: UI + Go-нода sidecar.

use node_sidecar::{Sidecar, SidecarStatus};
use std::sync::Mutex;
use tauri::{Manager, RunEvent};

struct AppState {
    sidecar: Mutex<Sidecar>,
}

#[tauri::command]
fn node_sidecar_status(state: tauri::State<AppState>) -> SidecarStatus {
    state.sidecar.lock().unwrap().status()
}

fn main() {
    let mut sidecar = Sidecar::new();
    sidecar.start();

    let app = tauri::Builder::default()
        .manage(AppState {
            sidecar: Mutex::new(sidecar),
        })
        .invoke_handler(tauri::generate_handler![node_sidecar_status])
        .build(tauri::generate_context!())
        .expect("не удалось собрать приложение Tauri");

    // Строгая гарантия: sidecar не переживает выход Tauri. Shutdown
    // выполняется на любой из веток выхода (Exit/ExitRequested с выходом).
    app.run(|app_handle, event| {
        if let RunEvent::Exit = event {
            if let Some(state) = app_handle.try_state::<AppState>() {
                state.sidecar.lock().unwrap().shutdown();
            }
        }
    });
}
