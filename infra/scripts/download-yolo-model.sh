#!/usr/bin/env bash
# download-yolo-model.sh — скачивание YOLO-весов и onnxruntime shared library.
# Весов НЕ в git: они лежат в infra/models/ и игнорируются (.gitignore).
#
# Источники:
#  - yolov8n.onnx: ultralytics/assets release v8.4.0 (GitHub).
#    Лицензия ultralytics — AGPL-3.0 (веса как производные). Применимость
#    оценена в docs/adr/0012: проект offline-first, веса не распространяются
#    как часть репозитория; решение зафиксировано в ADR.
#  - libonnxruntime.so: пакет Microsoft.ML.OnnxRuntime (NuGet), версия 1.31.1
#    (GitHub-релизы onnxruntime недоступны с 2026-11-17, NuGet остаётся).
set -euo pipefail

MODELS_DIR="${MODELS_DIR:-infra/models}"
YOLO_URL="${YOLO_URL:-https://github.com/ultralytics/assets/releases/download/v8.4.0/yolov8n.onnx}"
YOLO_SHA256="${YOLO_SHA256:-b2bc52f40e8e1c532427d5bde3575a5d5b571b739fab2c6df443733ed1589cbd}"
ORT_VERSION="${ORT_VERSION:-1.31.1}"
ORT_NUPKG_URL="https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime/${ORT_VERSION}/microsoft.ml.onnxruntime.${ORT_VERSION}.nupkg"
ORT_SHA256="${ORT_SHA256:-6871ccbc97f3d5bdb4095a1dcec33aa78d56f6ed8de5980ef5fa1d9f2e7c679a}"

mkdir -p "$MODELS_DIR"

echo "[1/2] yolov8n.onnx"
YOLO_PATH="$MODELS_DIR/yolov8n.onnx"
if [ -f "$YOLO_PATH" ] && echo "$YOLO_SHA256  $YOLO_PATH" | sha256sum -c --status 2>/dev/null; then
  echo "  уже скачан, sha256 совпадает"
else
  curl -fsSL -o "$YOLO_PATH" "$YOLO_URL"
  echo "$YOLO_SHA256  $YOLO_PATH" | sha256sum -c -
fi

echo "[2/2] libonnxruntime.so ($ORT_VERSION, NuGet)"
ORT_PATH="$MODELS_DIR/libonnxruntime.so.$ORT_VERSION"
if [ -f "$ORT_PATH" ]; then
  echo "  уже скачан"
else
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  curl -fsSL -o "$TMP/ort.nupkg" "$ORT_NUPKG_URL"
  if [ -n "$ORT_SHA256" ]; then
    echo "$ORT_SHA256  $TMP/ort.nupkg" | sha256sum -c -
  fi
  python3 - "$TMP" "$ORT_PATH" <<'EOF'
import shutil, sys, zipfile
tmp, out = sys.argv[1], sys.argv[2]
z = zipfile.ZipFile(f"{tmp}/ort.nupkg")
with z.open("runtimes/linux-x64/native/libonnxruntime.so") as src, open(out, "wb") as dst:
    shutil.copyfileobj(src, dst)
EOF
fi
echo "готово: $YOLO_PATH, $ORT_PATH"
