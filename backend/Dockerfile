# syntax=docker/dockerfile:1
# rebuild: delivery retry after transient runner failures
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

FROM debian:bookworm-slim AS runtime
# В образе только shared-библиотека onnxruntime (NuGet, 1.31.1). Сами веса
# (yolov8n/dinov2/part-heads/megadescriptor) в образ НЕ запекаются — качаются
# при старте менеджером modelassets в /data/models и живут на существующем PVC.
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends curl python3 ca-certificates >/dev/null \
    && rm -rf /var/lib/apt/lists/*
COPY infra/scripts/download-yolo-model.sh /tmp/download-yolo-model.sh
RUN ORT_ONLY=1 MODELS_DIR=/out/models bash /tmp/download-yolo-model.sh

# distroless/cc: glibc и libgcc — чтобы libonnxruntime.so загружался dlopen'ом
FROM gcr.io/distroless/cc-debian12:nonroot
# каталог данных: создаётся в builder'е и переносится с правами nonroot (65532),
# иначе sqlite не сможет открыть файл БД
COPY --from=builder --chown=65532:65532 /data /data
# миграции нужны ноде при старте (auto-migrate) и команде migrate
COPY --from=builder /src/migrations /app/migrations
COPY --from=builder /out/node /app/node
COPY --from=web /src/app/dist /app/web
COPY --from=runtime /out/models /app/models
ENV WEB_DIR=/app/web DATA_DIR=/data DATABASE_URL=/data/node.db LISTEN_ADDR=0.0.0.0:8080 \
    INFER_ENGINE=yolo INFER_RUNTIME_LIB=/app/models/libonnxruntime.so.1.31.1 \
    INFER_MODEL_PATH=/data/models/yolo26x.onnx INFER_REID_MODEL_DIR=/data/models
WORKDIR /app
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/app/node"]
CMD ["web"]
