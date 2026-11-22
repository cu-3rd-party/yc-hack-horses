# syntax=docker/dockerfile:1
# Мультистейдж. Вариант central (self-service): CGO-сборка + YOLO/onnxruntime в образе,
# чтобы серверное распознавание работало (запрос пользователя 2026-11-22; отступление
# от ADR-0012 «CGO-free central» — там деградация была сознательной, здесь осознанно включаем).
# Локальные ноды/desktop собираются из исходников (build-node.sh), этот Dockerfile — только central.
FROM node:22-bookworm-slim AS web
WORKDIR /src/app
ENV ELECTRON_SKIP_BINARY_DOWNLOAD=1
COPY app/package*.json ./
RUN npm ci --no-audit --no-fund --maxsockets=4
COPY packages/contracts/src /src/packages/contracts/src
COPY app/ ./
RUN npm run build:web

FROM golang:1.26-bookworm AS builder
WORKDIR /src
# CGO включён: onnxruntime-go грузит libonnxruntime.so через dlopen
ENV CGO_ENABLED=1 GOFLAGS=-p=4
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN go build -trimpath -ldflags '-s -w' -o /out/node ./cmd/node \
    && mkdir -p /data && chown 65532:65532 /data

FROM debian:bookworm-slim AS models
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends curl python3 ca-certificates >/dev/null \
    && rm -rf /var/lib/apt/lists/*
COPY infra/scripts/download-yolo-model.sh /tmp/download-yolo-model.sh
RUN MODELS_DIR=/out/models bash /tmp/download-yolo-model.sh
# ReID: сегментация (dinov2-small + part-heads) и MegaDescriptor (кандидаты).
# SHA — те же пины, что в backend/internal/modelassets/manager.go.
RUN curl -fsSL -o /out/models/dinov2-small.onnx "https://huggingface.co/Xenova/dinov2-small/resolve/c2bb04a51fab207c420665f1946016107bffc701/onnx/model.onnx" \
    && echo "83141175ec78b4ff9a2bb58a4c7c264ba0054d1c2e122e5a8114b79a8d4179ea  /out/models/dinov2-small.onnx" | sha256sum -c - \
    && curl -fsSL -o /out/models/part-heads.f32 "https://www.kaggle.com/api/v1/datasets/download/sorrowdev/loshid-horse-part-heads/part-heads.f32?datasetVersionNumber=1" \
    && echo "17766f17e8a6b8fb6741b47fbc67f25c90e097043609b2891e57b3ad82d6a92d  /out/models/part-heads.f32" | sha256sum -c - \
    && curl -fsSL -o /out/models/megadescriptor-l-384.bin "https://huggingface.co/BVRA/MegaDescriptor-L-384/resolve/33b3c6f4ee0c386a4126cc3dcd23843920613fa1/pytorch_model.bin" \
    && echo "ccfe757f50f7984a115ffe00921cd4c09e260e645215463b23814586040227a3  /out/models/megadescriptor-l-384.bin" | sha256sum -c -

# distroless/cc: glibc и libgcc — чтобы libonnxruntime.so загружался dlopen'ом
FROM gcr.io/distroless/cc-debian12:nonroot
# каталог данных: создаётся в builder'е и переносится с правами nonroot (65532),
# иначе sqlite не сможет открыть файл БД
COPY --from=builder --chown=65532:65532 /data /data
# миграции нужны ноде при старте (auto-migrate) и команде migrate
COPY --from=builder /src/migrations /app/migrations
COPY --from=builder /out/node /app/node
COPY --from=web /src/app/dist /app/web
COPY --from=models /out/models /app/models
ENV WEB_DIR=/app/web DATA_DIR=/data DATABASE_URL=/data/node.db LISTEN_ADDR=0.0.0.0:8080 \
    INFER_ENGINE=yolo INFER_MODEL_PATH=/app/models/yolov8n.onnx INFER_RUNTIME_LIB=/app/models/libonnxruntime.so.1.31.1 \
    INFER_REID_MODEL_DIR=/app/models
WORKDIR /app
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/app/node"]
CMD ["web"]
