# syntax=docker/dockerfile:1
# Мультистейдж: сборка без CGO (pure-Go sqlite), runtime — distroless nonroot.
FROM golang:1.26-alpine AS builder
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-p=4
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags '-s -w' -o /out/node ./cmd/node \
    && mkdir -p /data && chown 65532:65532 /data

FROM gcr.io/distroless/static-debian12:nonroot
# каталог данных: создаётся в builder'е и переносится с правами nonroot (65532),
# иначе sqlite не сможет открыть файл БД
COPY --from=builder --chown=65532:65532 /data /data
# миграции нужны ноде при старте (auto-migrate) и команде migrate
COPY --from=builder /src/migrations /app/migrations
COPY --from=builder /out/node /app/node
ENV DATA_DIR=/data DATABASE_URL=/data/node.db LISTEN_ADDR=0.0.0.0:8080
WORKDIR /app
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/app/node"]
CMD ["web"]

# no-op: re-delivery для обновлённого env (mTLS-переменные центральной ноды)
