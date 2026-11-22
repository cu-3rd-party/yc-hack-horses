#!/usr/bin/env bash
# download-yolo-model.sh — скачивание YOLO-весов и onnxruntime shared library.
# Весов НЕ в git: они лежат в infra/models/ и игнорируются (.gitignore).
#
# Источники:
#  - yolo26x.onnx и yolov8n.onnx: ultralytics/assets release v8.4.0 (GitHub).
#    Лицензия ultralytics — AGPL-3.0 (веса как производные). Применимость
#    оценена в docs/adr/0012: проект offline-first, веса не распространяются
#    как часть репозитория; решение зафиксировано в ADR.
#  - libonnxruntime.so: пакет Microsoft.ML.OnnxRuntime (NuGet), версия 1.31.1
#    (GitHub-релизы onnxruntime недоступны с 2026-11-17, NuGet остаётся).
# ORT_ONLY=1 устанавливает только библиотеку для серверного образа без весов.
set -euo pipefail

MODELS_DIR="${MODELS_DIR:-infra/models}"
legacy_sha=b2bc52f40e8e1c532427d5bde3575a5d5b571b739fab2c6df443733ed1589cbd
standard_sha=88568299de91d4967f239a062c9f1619f695ebd05de73cd66b8f589591aaeb0a
# Earlier calls using a custom mirror URL implicitly requested v8n.
if [ -z "${YOLO_FILENAME:-}" ]; then
  YOLO_FILENAME=yolo26x.onnx
  if [ "${YOLO_SHA256:-}" = "$legacy_sha" ]; then
    YOLO_FILENAME=yolov8n.onnx
  elif [[ -n ${YOLO_URL:-} && $YOLO_URL != *yolo26x.onnx* && ${YOLO_SHA256:-} != "$standard_sha" ]]; then
    YOLO_FILENAME=yolov8n.onnx
  fi
fi
case "$YOLO_FILENAME" in
  yolo26x.onnx) YOLO_SHA256="${YOLO_SHA256:-$standard_sha}" ;;
  yolov8n.onnx) YOLO_SHA256="${YOLO_SHA256:-$legacy_sha}" ;;
  *) echo "Unknown detector filename: $YOLO_FILENAME" >&2; exit 2 ;;
esac
YOLO_URL="${YOLO_URL:-https://github.com/ultralytics/assets/releases/download/v8.4.0/$YOLO_FILENAME}"
ORT_VERSION="${ORT_VERSION:-1.31.1}"
ORT_NUPKG_URL="https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime/${ORT_VERSION}/microsoft.ml.onnxruntime.${ORT_VERSION}.nupkg"
ORT_SHA256="${ORT_SHA256:-6871ccbc97f3d5bdb4095a1dcec33aa78d56f6ed8de5980ef5fa1d9f2e7c679a}"

mkdir -p "$MODELS_DIR"

if [ "${ORT_ONLY:-0}" != 1 ]; then
  echo "[1/2] $YOLO_FILENAME"
  YOLO_PATH="$MODELS_DIR/$YOLO_FILENAME"
  if [ -f "$YOLO_PATH" ] && echo "$YOLO_SHA256  $YOLO_PATH" | sha256sum -c - >/dev/null 2>&1; then
    echo "  уже скачан, sha256 совпадает"
  else
    detector_tmp=$(mktemp "$MODELS_DIR/.yolo-download.XXXXXXXX")
    trap 'rm -f "$detector_tmp"' EXIT
    curl -fsSL -o "$detector_tmp" "$YOLO_URL"
    echo "$YOLO_SHA256  $detector_tmp" | sha256sum -c -
    mv "$detector_tmp" "$YOLO_PATH"
    trap - EXIT
  fi
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
echo "готово: $MODELS_DIR, $ORT_PATH"
