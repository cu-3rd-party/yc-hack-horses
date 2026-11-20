# CI-база для сборки Tauri-клиента: Rust + системные зависимости webkit2gtk + node/npm.
# Только для CI-джобы build-desktop, деплоится не приложение, поэтому pull-секрет чарту не нужен.
FROM docker.io/library/rust:1.83-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        libwebkit2gtk-4.1-dev \
        libayatana-appindicator3-dev \
        librsvg2-dev \
        build-essential \
        libssl-dev \
        libgtk-3-dev \
        nodejs \
        npm \
        patchelf \
        xdg-utils \
    && rm -rf /var/lib/apt/lists/*

RUN npm install -g @tauri-apps/cli
