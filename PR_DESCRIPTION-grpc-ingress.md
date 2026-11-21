# PR: gRPC центральной ноды за ingress (h2c-мультиплекс + mTLS через заголовок)

## Проблема
Central-нода в self-service за Traefik ingress: gRPC-клиенты приходят на
`yc-hack-horses.cu3rd.ru:443`, ingress терминирует TLS и гонит запрос на 8080,
где живёт только REST — gRPC-handshake получал 404 от REST-роутера
(`unexpected HTTP status 404 application/json`). Прямой mTLS-порт gRPC (9443)
за ingress не проброшен.

## Решение
Мультиплекс-режим на общем порту (аддитивно, по умолчанию выключен):

- `network.grpc_multiplex` (bool, default false) + env `GRPC_MULTIPLEX`.
  При `true` на `network.listen_addr` работает обёртка
  `golang.org/x/net/http2/h2c` (`grpcHandlerFunc`): `ProtoMajor==2` +
  `content-type application/grpc` → gRPC-сервер, иначе REST.
  Прямой `grpc_listen_addr` (9443, mTLS/insecure-loopback как раньше) не тронут.
- `GRPC_MULTIPLEX=true` требует `security.ca_file` — конфиг-валидация отвергает
  мультиплекс без CA (fail closed).
- mTLS через ingress: Traefik `passTLSClientCert` (`pem: true`) прокидывает
  клиентский серт в `X-Forwarded-Tls-Client-Cert`; unary/stream-интерцепторы
  мультиплекс-gRPC-сервера проверяют заголовок против `security.ca_file`:
  chain, срок действия, URI SAN `przewalski://node/<id>`
  (`identity.NodeIDFromCert`), полномочия SendEvents+ReceiveEvents
  (`security.CapabilitiesForCert`). Нет заголовка / битый серт →
  `Unauthenticated`.
- Мультиплекс-gRPC-сервер — отдельный экземпляр с insecure-credentials
  (TLS терминирует ingress); прямой 9443-сервер работает как раньше, локальные
  loopback-подключения не сломаны.

## Изменения
- `backend/internal/app/grpcmux.go` (новый): h2c-мультиплекс-хендлер,
  cert-интерцепторы, сборка mux-gRPC-сервера.
- `backend/internal/app/app.go`: сборка мультиплекса в `New` при флаге.
- `backend/internal/security/headercert.go` (новый): разбор экранированного PEM
  из заголовка (`X-Forwarded-Tls-Client-Cert`); экспортирует
  `ClientCertHeaderName`, `ParseClientCertHeader`, `ErrNoClientCert`.
- `backend/internal/config/config.go`: `network.grpc_multiplex`, env
  `GRPC_MULTIPLEX`, валидация (multiplex ⇒ ca_file обязателен).
- `backend/config.example.yaml`, `README.md`: документация режима.

## Traefik-конфигурация (self-service)
- values `projects/yc-hack-horses.yaml` (v7):
  - `serviceAnnotations: traefik.ingress.kubernetes.io/service.serversscheme: h2c`
    (возвращён, снят в #418 — теперь бэкенд умеет HTTP/2);
  - `ingress.extraAnnotations`-путь не годится: аннотация
    `traefik.ingress.kubernetes.io/router.middlewares` уже выставляется чартом
    (redirect-https), дубликат аннотации недопустим. В `charts/app` аддитивно
    добавлены опциональные values (рендер-идентичность для чужих проектов
    сохранена, проверено `helm template` до/после):
    - `ingress.extraMiddlewares` (список, дописывается в цепочку middleware);
    - `ingress.clientCertPassthrough: true` — рендерит
      Middleware `passTLSClientCert: {pem: true}` и TLSOption
      `clientAuth: {clientAuthType: RequestClientCert}` (без него Traefik не
      запрашивает клиентский серт, заголовок был бы пуст), цепочка middleware
      и `router.tls.options` дополняются.
- Форма middleware в кластере: Middleware CRD
  (`apiVersion: traefik.io/v1alpha1`, namespace-scoped), ссылка из аннотации —
  `<namespace>-<name>@kubernetescrd`.
- Про `perms: flag:false`: в установленной Traefik CRD (v3.7) такого поля нет
  (проверено по схеме CRD `middlewares.traefik.io`); подмена заголовка клиентом
  отсекается тем, что Traefik сам перезаписывает
  `X-Forwarded-Tls-Client-Cert` значением из TLS-соединения. Доверие заголовку
  в бэкенде оправдано только при трафике через ingress; прямой in-cluster
  доступ к 8080 не даёт forge-аутентификацию без валидного серта dev-CA
  (сёрт — сам credential).

## Тесты
`backend/internal/app/grpcmux_test.go`: реальный HTTP/2 (grpc-go client,
insecure creds + ContextDialer на httptest.NewServer с h2c):
- REST (HTTP/1.1) через мультиплекс → 200;
- gRPC Handshake с валидным X-Forwarded-Tls-Client-Cert → OK;
- валидный серт другой ноды → OK;
- без заголовка / битый PEM / серт без URI SAN → `Unauthenticated`;
- разбор экранированной формы заголовка (`security.ParseClientCertHeader`).

`go build ./... && go vet ./... && go test ./...` — чисто, gofumpt чисто.

## Проверка после доставки
1. В логах пода нет `unexpected HTTP status 404 application/json`.
2. Локальная нода (dev-серты, `sync.central_url=https://yc-hack-horses.cu3rd.ru`)
   выполняет handshake через ingress, события доходят до central
   (`curl https://yc-hack-horses.cu3rd.ru/api/v1/horses`).
