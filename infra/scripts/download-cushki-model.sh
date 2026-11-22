#!/usr/bin/env bash
# download-cushki-model.sh — скачивание весов ReID CUshki v6 (файнтюн
# DINOv2-L, ONNX fp16) и fusion_config.json в $MODELS_DIR (по умолчанию
# DATA_DIR/models). Весов НЕ в git: файл внешний (611MB), целостность
# проверяется по pinned sha256. Экспорт/конвертация на ноде НЕ выполняются:
# скрипт качает готовый fp16 ONNX.
#
# Источник: статик-хост проекта (data.yc-hack-horses.cu3rd.ru), fp16-вариант
# предпочитателен (2x меньше, эквивалентность fp32 проверена экспортёром:
# fp16 max abs diff 2.36e-4). Переопределяется CUSHKI_URL/CUSHKI_CONFIG_URL.
#
# Использование в node.yaml:
#   inference:
#     reid_model_path: $DATA_DIR/models/dinov2_ft_s3.fp16.onnx
#     reid_cushki_config: $DATA_DIR/models/cushki_fusion_config.json
set -euo pipefail

MODELS_DIR="${MODELS_DIR:-${DATA_DIR:-.}/models}"
CUSHKI_FILENAME="${CUSHKI_FILENAME:-dinov2_ft_s3.fp16.onnx}"
CUSHKI_SHA256="${CUSHKI_SHA256:-e797a50a17fd708dd81e2bfbc4b2ebf9586be520b5c10ff55902178ddc0b9b7b}"
CUSHKI_URL="${CUSHKI_URL:-https://data.yc-hack-horses.cu3rd.ru/models/cushki-v6/$CUSHKI_FILENAME}"
CUSHKI_CONFIG_URL="${CUSHKI_CONFIG_URL:-https://data.yc-hack-horses.cu3rd.ru/models/cushki-v6/fusion_config.json}"
CUSHKI_CONFIG_NAME="${CUSHKI_CONFIG_NAME:-cushki_fusion_config.json}"

mkdir -p "$MODELS_DIR"

echo "[1/2] $CUSHKI_FILENAME"
MODEL_PATH="$MODELS_DIR/$CUSHKI_FILENAME"
if [ -f "$MODEL_PATH" ] && echo "$CUSHKI_SHA256  $MODEL_PATH" | sha256sum -c - >/dev/null 2>&1; then
  echo "  уже скачан, sha256 совпадает"
else
  model_tmp=$(mktemp "$MODELS_DIR/.cushki-download.XXXXXXXX")
  trap 'rm -f "$model_tmp"' EXIT
  curl -fsSL -o "$model_tmp" "$CUSHKI_URL"
  echo "$CUSHKI_SHA256  $model_tmp" | sha256sum -c -
  mv "$model_tmp" "$MODEL_PATH"
  trap - EXIT
fi

echo "[2/2] $CUSHKI_CONFIG_NAME (коэффициенты скоринга CUshki)"
CONFIG_PATH="$MODELS_DIR/$CUSHKI_CONFIG_NAME"
if [ ! -f "$CONFIG_PATH" ]; then
  config_tmp=$(mktemp "$MODELS_DIR/.cushki-config.XXXXXXXX")
  trap 'rm -f "$config_tmp"' EXIT
  curl -fsSL -o "$config_tmp" "$CUSHKI_CONFIG_URL"
  mv "$config_tmp" "$CONFIG_PATH"
  trap - EXIT
fi

echo "готово: $MODEL_PATH"
echo "готово: $CONFIG_PATH"
