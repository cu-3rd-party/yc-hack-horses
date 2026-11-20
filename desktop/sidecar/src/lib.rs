//! Sidecar-менеджмент Go-ноды.
//!
//! Запускает бинарь `node`, ждёт готовности health endpoint на 127.0.0.1,
//! рестартует при падении и гарантированно завершает процесс при выходе:
//! PR_SET_PDEATHSIG + kill на shutdown. Путь до бинаря — из переменной
//! окружения `YC_NODE_BIN` (или `bin/node` рядом с приложением).

use std::io::{Read, Write};
use std::net::{Ipv4Addr, TcpStream};
use std::os::unix::process::CommandExt;
use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{Duration, Instant};

#[cfg(test)]
mod tests;

const DEFAULT_PORT: u16 = 8080;
const HEALTH_TIMEOUT_MS: u64 = 2000;
const START_TIMEOUT_SECS: u64 = 30;
const RESTART_DELAY_SECS: u64 = 3;

#[derive(Debug, Clone, serde::Serialize)]
pub struct SidecarStatus {
    pub running: bool,
    pub pid: Option<u32>,
    pub port: u16,
    pub binary: String,
    pub last_error: Option<String>,
    pub restarts: u64,
}

fn node_port() -> u16 {
    std::env::var("YC_NODE_PORT")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(DEFAULT_PORT)
}

fn node_binary() -> PathBuf {
    if let Ok(p) = std::env::var("YC_NODE_BIN") {
        return PathBuf::from(p);
    }
    // По умолчанию: рядом с исполняемым файлом приложения.
    let mut dir = std::env::current_exe().ok().and_then(|p| p.parent().map(PathBuf::from)).unwrap_or_default();
    dir.push("bin");
    dir.join("node")
}

// Минимальный HTTP GET /health на std TcpStream.
fn health_ok(port: u16) -> bool {
    let addr = std::net::SocketAddr::new(Ipv4Addr::LOCALHOST.into(), port);
    let timeout = Duration::from_millis(HEALTH_TIMEOUT_MS);
    let mut stream = match TcpStream::connect_timeout(&addr, timeout) {
        Ok(s) => s,
        Err(_) => return false,
    };
    stream.set_read_timeout(Some(timeout)).ok();
    stream.set_write_timeout(Some(timeout)).ok();
    if stream
        .write_all(b"GET /health HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
        .is_err()
    {
        return false;
    }
    let mut buf = String::new();
    match stream.read_to_string(&mut buf) {
        Ok(_) => buf.starts_with("HTTP/1.1 200") || buf.starts_with("HTTP/1.0 200"),
        Err(_) => false,
    }
}

fn wait_healthy(port: u16, deadline: Instant) -> Result<(), String> {
    while Instant::now() < deadline {
        if health_ok(port) {
            return Ok(());
        }
        thread::sleep(Duration::from_millis(300));
    }
    Err(format!("health endpoint 127.0.0.1:{port} не ответил за {START_TIMEOUT_SECS} с"))
}

fn spawn_node(binary: &PathBuf, port: u16) -> Result<Child, String> {
    let mut cmd = Command::new(binary);
    cmd.stdout(Stdio::null()).stderr(Stdio::null());
    cmd.env("NODE_PORT", port.to_string());
    // Гарантия смерти процесса вместе с родителем (Tauri): SIGKILL по
    // смерти родителя применяется в дочернем процессе до exec.
    unsafe {
        cmd.pre_exec(|| {
            libc::prctl(libc::PR_SET_PDEATHSIG, libc::SIGKILL);
            Ok(())
        });
    }
    // Дополнительно процесс убивается при выходе приложения (см. shutdown).
    cmd.spawn().map_err(|e| format!("не удалось запустить {}: {e}", binary.display()))
}

fn kill_child(child: &mut Child) {
    // Сначала мягко, затем жёстко.
    let _ = child.kill();
    let _ = child.wait();
}

pub struct Sidecar {
    port: u16,
    binary: PathBuf,
    child: Arc<Mutex<Option<Child>>>,
    stop_flag: Arc<AtomicBool>,
    state: Arc<Mutex<SidecarStatus>>,
    handle: Option<thread::JoinHandle<()>>,
}

impl Sidecar {
    pub fn new() -> Self {
        let port = node_port();
        let binary = node_binary();
        let state = SidecarStatus {
            running: false,
            pid: None,
            port,
            binary: binary.display().to_string(),
            last_error: None,
            restarts: 0,
        };
        Self {
            port,
            binary,
            child: Arc::new(Mutex::new(None)),
            stop_flag: Arc::new(AtomicBool::new(false)),
            state: Arc::new(Mutex::new(state)),
            handle: None,
        }
    }

    // Запуск supervising-потока: spawn -> ожидание health -> мониторинг,
    // при падении — рестарт с задержкой.
    pub fn start(&mut self) {
        let port = self.port;
        let binary = self.binary.clone();
        let child_slot = Arc::clone(&self.child);
        let stop = Arc::clone(&self.stop_flag);
        let state = Arc::clone(&self.state);

        let handle = thread::spawn(move || {
            let mut first = true;
            while !stop.load(Ordering::SeqCst) {
                if !first {
                    thread::sleep(Duration::from_secs(RESTART_DELAY_SECS));
                }
                first = false;
                if stop.load(Ordering::SeqCst) {
                    break;
                }
                match spawn_node(&binary, port) {
                    Err(e) => {
                        let mut s = state.lock().unwrap();
                        s.running = false;
                        s.last_error = Some(e);
                        continue;
                    }
                    Ok(child) => {
                        let pid = child.id();
                        *child_slot.lock().unwrap() = Some(child);
                        {
                            let mut s = state.lock().unwrap();
                            s.running = true;
                            s.pid = Some(pid);
                            s.last_error = None;
                            s.restarts = s.restarts.saturating_add(1);
                        }
                        match wait_healthy(port, Instant::now() + Duration::from_secs(START_TIMEOUT_SECS)) {
                            Ok(()) => {
                                // Ждём выхода процесса.
                                loop {
                                    if stop.load(Ordering::SeqCst) {
                                        break;
                                    }
                                    let mut slot = child_slot.lock().unwrap();
                                    if let Some(c) = slot.as_mut() {
                                        match c.try_wait() {
                                            Ok(Some(_status)) => {
                                                *slot = None;
                                                let mut s = state.lock().unwrap();
                                                s.running = false;
                                                s.pid = None;
                                                s.last_error =
                                                    Some("процесс ноды завершился".to_string());
                                                break;
                                            }
                                            Ok(None) => {}
                                            Err(e) => {
                                                let mut s = state.lock().unwrap();
                                                s.last_error =
                                                    Some(format!("try_wait: {e}"));
                                                break;
                                            }
                                        }
                                    } else {
                                        break;
                                    }
                                    drop(slot);
                                    thread::sleep(Duration::from_millis(500));
                                }
                            }
                            Err(e) => {
                                let mut slot = child_slot.lock().unwrap();
                                if let Some(c) = slot.as_mut() {
                                    kill_child(c);
                                }
                                *slot = None;
                                let mut s = state.lock().unwrap();
                                s.running = false;
                                s.pid = None;
                                s.last_error = Some(e);
                            }
                        }
                    }
                }
            }
            // Финальная уборка в потоке.
            let mut slot = child_slot.lock().unwrap();
            if let Some(c) = slot.as_mut() {
                kill_child(c);
            }
            *slot = None;
        });
        self.handle = Some(handle);
    }

    pub fn status(&self) -> SidecarStatus {
        self.state.lock().unwrap().clone()
    }

    // Полная остановка: флаг + kill процесса + join потока.
    pub fn shutdown(&mut self) {
        self.stop_flag.store(true, Ordering::SeqCst);
        {
            let mut slot = self.child.lock().unwrap();
            if let Some(c) = slot.as_mut() {
                kill_child(c);
            }
            *slot = None;
        }
        if let Some(h) = self.handle.take() {
            let _ = h.join();
        }
        let mut s = self.state.lock().unwrap();
        s.running = false;
        s.pid = None;
    }
}

impl Drop for Sidecar {
    fn drop(&mut self) {
        self.shutdown();
    }
}
