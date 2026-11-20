# ADR-0010: Desktop sidecar architecture
**Status:** accepted
## Решение
Tauri (Rust shell) + Go-нода как sidecar: Tauri запускает скомпилированный бинарник `node`, ждёт готовности (health endpoint на loopback), рестартует при падении, завершает при выходе. React UI общается с нодой по HTTP/WS на `127.0.0.1:<port>`; на внешние интерфейсы локальный API не слушается без явной причины.
## Почему
Разделение UI (Rust/WebView) и состояния (Go) сохраняет один Sync Engine без встраивания Go в Tauri-процесс. Альтернатива — Wails — отклонена: Tauri устойчивее и знакомее.
