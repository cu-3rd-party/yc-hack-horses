# ADR-0014: Автоматический enrollment и first-run опыт клиента

**Status:** accepted (amends ADR-0007: leaf-серты enrollment-нод — 365 дней)

## Решение

Enrollment автоматизирован: после установки .deb/.AppImage из релиза приложение
при первом запуске само поднимает локальную ноду, само проходит enrollment
против central (`https://yc-hack-horses.cu3rd.ru`) и работает — без ручных
сертификатов и конфигов.

### Enrollment API (central)

`POST /api/v1/enroll` — включается конфигом central (только central; без
конфигурации эндпоинт отвечает 404):

```yaml
enrollment:
  enabled: true
  token: "<секрет>"        # env ENROLLMENT_TOKEN
  ca_key_file: /run/secrets/ca.key   # env ENROLLMENT_CA_KEY_FILE
  cert_days: 365           # срок жизни выдаваемого leaf-серта
```

Клиент генерирует RSA-ключ (3072) и CSR (CN=node_id, URI SAN
`przewalski://node/<node_id>`, ADR-0006) и присылает `{node_id, csr, token}`.
Сервер валидирует токен (constant-time), подпись CSR, совпадение CN и URI SAN
с присланным node_id, минимальную длину ключа — и подписывает серт CA-ключом.
Ответ: `{node_cert, ca_cert, expires_at}` — ca.crt возвращается для удобства
(клиент кладёт его в security.ca_file).

Выданные серты фиксируются в on-disk списке
`<data_dir>/enrollment-issued.jsonl` (issued_at, node_id, serial, not_after) —
задел под revocation list из ADR-0007.

### Срок жизни сертов

Leaf-серты, выдаваемые enrollment'ом, живут **365 дней (1 год)** — отступление
от прежнего правила ADR-0007 «90 дней (dev)»; ADR-0007 переведён в статус
amended. Серверный серт central и dev-сертификаты не затронуты. Пере-enroll —
за 7 дней до конца срока.

### Клиент (`node enroll`)

Подкоманда ноды: `node enroll --config node.yaml --token <t>` — если серт уже
жив, сетевой запрос не выполняется (`--status` — только статус без сети, JSON
в stdout). Статус: `enrolled`, `node_id`, `not_after`, `needs_reenroll`,
`reason`. node.json (UUID ноды) переиспользуется — node_id не меняется между
enrollment'ами.

### Electron-хост (first-run)

При запуске пакета бандлится нода (`app/electron/local-node.cjs`, бинарь
`loshid-node` собирается beforePack `app/scripts/build-node.sh` — linux-amd64
и darwin-arm64, с onnxruntime и миграциями; веса моделей НЕ в бандле —
скачиваются из UI через modelassets/Kaggle-релиз, отсутствие — штатная
деградация):

1. Если на `127.0.0.1:8080` уже отвечает нода — используем её (старый режим).
2. Иначе `node enroll --status`; серт жив → спавн ноды.
3. Серта нет / истекает / < 7 дней — enrollment: сохранённый токен (после
   первой успешной регистрации хранится в `userData/node-host.json`) или
   диалог ввода (пользователь вводит токен один раз).
4. Ошибка enrollment/спавна — нода поднимается plain (только loopback),
   приложение работает офлайн; синк начнётся после успешного enrollment.

Конфиг ноды передаётся целиком через env (LISTEN_ADDR, NODE_DSN, CERT_FILE/
KEY_FILE/CA_FILE, MIGRATIONS_DIR, INFER_*, SYNC_*), gRPC — на loopback. Дев-режим
`npm start` не меняется: бандла нет → хост no-op, старый поток с отдельно
запущенной нодой. Логика решения — кроссплатформенный TS
(`app/electron/enroll.ts`); macOS — тот же флоу, darwin-бинарь ноды собирается
тем же beforePack (osx-arm64).

## Почему

Ручное подписание CSR не масштабируется на полевые устройства и несовместимо
с установкой из релиза. Токен + привязка node_id к URI SAN CSR сохраняют
криптографическую модель ADR-0006/0007: доверие по-прежнему выдаёт только CA,
компрометация токена даёт лишь легитимный серт новой ноде, а не доступ к чужим.
Срок 365 дней соответствует полевому циклу устройств без постоянной связи
(пере-enroll при случае, не по расписанию).
