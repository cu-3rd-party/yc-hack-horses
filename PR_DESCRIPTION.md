# PR: Mobile Foundation (Agent G)

## Что сделано

Мобильная основа V0 по ADR-0011 — без фиктивных заглушек: любой путь в UI
доходит до реального кода (Go Core, REST ноды или AsyncStorage-очередь).

### Стек

- Expo SDK 58, dev client (повторяемая сборка, нативные модули через config plugins)
- React Native 0.88.1, React 19.3, TypeScript ~6.0.3 strict
- @react-navigation/native + native-stack
- expo-image-picker (фото), expo-file-system (новый API SDK 58)
- jest 30 + jest-expo (тесты очереди)
- Go: backend/cmd/mobilebind (gomobile bind фасад)

### Структура

- `mobile/` — весь RN-клиент: экраны Dashboard/Observations/NewObservation/Sync,
  портированный API-клиент (`src/shared/api`), офлайн-очередь
  (`src/features/offline-queue`, AsyncStorage вместо localStorage),
  мост к Go Core (`src/shared/go-core`), логика создания наблюдения с фото
  (`src/features/new-observation`), foreground sync (`src/features/sync-now`).
- `backend/cmd/mobilebind/` — API-фасад Go Core для gomobile: Init (SQLite в app
  data dir + миграции из go:embed), AppendObservation (офлайн-запись: событие +
  проекция в одной транзакции), StoreBlob, ForegroundSync (исходящий gRPC через
  sync.Engine — движок не дублируется), NodeStatus, Close. С тестами на реальном SQLite.
- `backend/migrations/embed.go` (новый файл) — go:embed SQL-миграций для
  мобильной библиотеки.
- `backend/internal/storage/migrate/embedded.go` (новый файл) + extract `applyAll`
  в `migrate.go` — `ApplyFS(fs.FS)`: тот же путь применения миграций, что и Apply.
- `mobile/README.md` — архитектура, ограничения, сборка, подключение Go Core.

### Решение: API-клиент скопирован в mobile/shared, а не вынесен в packages/api-client

Веб-клиент завязан на DOM-типы (`File`, `new URL`), которых нет в Hermes; общий
пакет потребовал бы окружение-agnostic абстракцию — outside зоны V0. Обоснование
и отличия копии — в `mobile/README.md`.

### Ограничения V0 (ADR-0011)

- Foreground sync только: кнопка + автозапуск при открытии приложения. Ни
  фонового демона, ни inbound gRPC, ни mDNS — прямо сказано в README.
- gRPC-канал к центральной ноде plaintext (mTLS ADR-0007 не настроен для мобильных).
- gomobile в CI/окружении не установлен — `.aar` не собран; точная команда в README.

## Проверки

- `go build ./...`, `go vet ./cmd/mobilebind/` — чисто
- `go test ./cmd/mobilebind/ ./internal/sync/ ./internal/storage/...` — зелёные
  (фасад протестирован на реальном SQLite: init/re-init, blob, observation, status)
- `tsc --noEmit` (strict) — чисто
- `jest` — 4/4 теста очереди (enqueue roundtrip, flush, обрыв по
  NodeUnavailableError, markAttempt)
- `npx expo export --platform android` — JS-граф полностью компилируется в
  Hermes-байткод; нативная сборка Android SDK не требует, команды в README
- `expo-doctor`: 19/20 (прямой dep `expo-modules-core` нужен npm-хоистингу
  для jest-expo — задокументировано)

## Требует реального устройства / окружения

- Инициализация Go Core (SQLite в app data dir) и офлайн-запись наблюдений.
- Камера/медиатека (expo-image-picker) и blob через мост.
- Foreground sync по gRPC с центральной нодой.
- Сборка `.aar` через gomobile и dev client (`expo prebuild` + `expo run:android`).
