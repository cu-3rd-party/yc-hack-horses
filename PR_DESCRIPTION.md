# feat(security): mTLS-транспорт и identity для межнодовых соединений

Agent C — Security и Identity. Реализует ADR-0006 (node identity), ADR-0007
(mTLS trust model), ADR-0008 (central node identification).

## Что сделано

### backend/internal/identity
- `NodeIDFromCert(cert) (string, error)` — извлечение node_id из URI SAN
  `przewalski://node/<uuid>` (ADR-0006). CN не используется.
- `VerifyPeerNodeID(cert, expectedNodeID) error` — проверка соответствия
  node_id пира. Идентичность узнавается только из серта, не из протокола.

### backend/internal/security
- `Config` + `Validate()` — TLS-материал ноды; пустой `ca_file` запрещён
  (self-signed auto-trust запрещён, ADR-0007).
- `LoadServerTLSConfig(cfg, expectedNodeID)` — gRPC-сервер: mTLS
  (`RequireAndVerifyClientCert`), `ClientCAs`, `VerifyPeerCertificate`
  с проверкой URI SAN/node_id. TLS >= 1.2.
- `LoadClientTLSConfig(cfg, serverName)` — клиент: предъявляет свой серт,
  валидирует серверный (`RootCAs`, `ServerName`).
- `LoadCAPool(caFile)` — PEM CA-пул.
- `CapabilitiesForCert(cert, caPool, now, expectedNodeID)` — полномочия только
  из сертификата: цепочка доверия + срок + URI SAN. Невалидный/недоверенный
  серт — `CapNone` (доступ к данным запрещён).
- `NodeCapabilities` (`CapSendEvents`, `CapReceiveEvents`, `CapRegisterPeers`),
  `SetPeerRegistrars` — регистр нод с правом RegisterPeers (конфиг роль
  полномочий не выдаёт).
- `VerifyCentralIdentity(serverName, certChain, caPool, now)` — central
  capabilities только если hostname == `yc-hack-horses.cu3rd.ru` И серверный
  серт валиден против caPool (nil → системный пул публичных CA) И DNS SAN
  совпадает (ADR-0008).
- `RejectLocalCentral(serverCert, caPool, now)` — локальная нода со
  self-signed сертом не проходит central-проверку, даже если `central_hostname`
  в конфиге совпадает; `role_hint` — подсказка, не источник полномочий.
- `ClientTLSConfigForCentral` — клиентский конфиг с фиксированным SNI.

### infra/scripts
- `gen-dev-certs.sh` — идемпотентная генерация **development-only** материалов
  в `./secrets/` (в `.gitignore`): dev-CA 10 лет (CN помечен development-only),
  нодовые серты с URI SAN `przewalski://node/<uuid>` (90 дней), серверный серт
  `yc-hack-horses.cu3rd.ru` (SAN DNS). `--node <uuid>` для своих нод, `--force`
  для перегенерации.
- `README.md` — таблица файлов, production-enrollment (явный, CSR + доверенный
  CA), проверка через openssl.

## Тесты (все офлайн, без сети)

- недоверенный CA отклоняется (`CapabilitiesForCert` → `CapNone`, handshake);
- серт другой ноды (node_id mismatch) отклоняется — и на уровне функции, и на
  уровне TLS handshake;
- peer-to-peer mTLS handshake доверенных нод проходит (net.Pipe + tls.Server/Client);
- central: валидный hostname+серт → capabilities; подмена hostname / серта на
  чужое имя / недоверенный CA → отказ;
- локальная нода не может объявить себя central (проверка против системного пула);
- просроченный серт, отсутствие URI SAN, отсутствие клиентского серта, регистр
  RegisterPeers, `String()` capabilities.

Результат: `go build ./... && go vet ./... && go test ./...` — чисто,
gofumpt применён.

## Чего не трогали
backend/internal/{sync,storage,config,transport,app,inference}, backend/cmd/,
Dockerfile, migrations, infra/compose, .forgejo — по зонам владения.
