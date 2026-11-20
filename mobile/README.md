# mobile — React Native клиент (Go Core, ADR-0011)

Offline-first мобильный клиент yc-hack-horses. Expo SDK 58 + Dev Client, TypeScript strict.

## Архитектура

```
mobile/
  app.json                     Expo-конфиг (dev client, plugin expo-image-picker)
  src/App.tsx                  Навигация + автозапуск foreground sync при старте
  src/screens/                 Dashboard / Observations / NewObservation / Sync
  src/shared/api/              Порт app/src/shared/api (client.ts, endpoints.ts)
  src/shared/config/           Адрес ноды (REST) и пира (gRPC)
  src/shared/go-core/          Мост к gomobile-библиотеке + каталог данных
  src/features/offline-queue/  Очередь наблюдений (AsyncStorage) + исполнители
  src/features/new-observation/ Фото (expo-image-picker) -> blob -> наблюдение
  src/features/sync-now/       Foreground sync
  __tests__/queue.test.ts      Jest-тесты очереди (4)
```

Типы общие с web — `@yc-hack-horses/contracts` (packages/contracts), не копируются.

### Решение по API-клиенту: копия в mobile/shared, не packages/api-client

Клиент портирован в `mobile/src/shared/api` (копия с адаптациями), а не вынесен
в общий пакет `packages/api-client`, потому что:

1. Веб-версия завязана на DOM-типы (`File`, `new URL`), которых нет в Hermes;
   RN-версия вместо `File` использует `{ uri, name, type }` и ручную сборку query.
   Общий пакет потребовал бы либо окружение-agnostic абстракцию (полиморфный
   fetch/FormData-адаптер), либо ломающие правки в app/ — обе outside зоны V0.
2. Различия всё равно останутся (конфиг через `import.meta.env` vs `EXPO_PUBLIC_*`).
   Когда появится третья реализация (desktop RN?), вынос в packages/api-client
   станет оправдан — сейчас это оверинжиниринг.

Отличия портированной копии помечены комментариями «отличия от веб-версии».

### Офлайн-очередь

Подход из `app/src/features/offline-queue` адаптирован: `localStorage` ->
`AsyncStorage`, API стал асинхронным. При недоступности ноды наблюдение падает в
очередь; `flushQueue` по порядку исполняет действия, `NodeUnavailableError`
останавливает прогон (остальное бессмысленно слать), попытки и последняя ошибка
сохраняются.

## Go Core (ADR-0011)

Фасад — `backend/cmd/mobilebind` (gomobile bind): `Init(dataDir)` (SQLite + миграции
из go:embed), `AppendObservation`, `StoreBlob`, `ForegroundSync(peerAddr)`,
`NodeStatus()`, `Close`. Логика записи и синхронизации не дублируется — фасад
собирает вызовы `sqlstore`, `fsblob`, `migrate`, `sync.Engine`.

Регистрация нативного модуля: в нативном проекте dev client (`expo prebuild` +
`expo run:android`) добавить generated-биндинг `YcMobileCore` (gomobile
`gobind -lang=go,java`), имя модуля в JS — `NativeModules.YcMobileCore`.
Если библиотека не вшита (Expo Go), приложение честно показывает состояние
«Go Core не вшит» и работает через REST ноды — заглушек нет.

## Ограничения V0 (ADR-0011)

- Нет фонового sync: только кнопка на экране Sync и автозапуск при открытии.
- Нет inbound gRPC-сервера на устройстве.
- Нет mDNS-обнаружения нод; адрес центральной ноды вводится вручную
  (`EXPO_PUBLIC_SYNC_PEER`, по умолчанию `10.0.2.2:9090` для эмулятора).
- gRPC-канал к центральной ноде пока plaintext — mTLS (ADR-0007) в мобильной
  сборке не настроен, включается после выпуска клиентских сертификатов.

## Сборка и проверки (без Android SDK)

```bash
cd mobile
npm install --cache $HOME/.npm-shared --maxsockets=4   # Node >= 20 (использован v24)
npx tsc --noEmit          # strict typecheck
npx jest                  # тесты офлайн-очереди
npx expo export --platform android   # полная компиляция JS-графа в Hermes-байткод
```

Полная нативная сборка (требует Android SDK / JDK 17):

```bash
npx expo prebuild --platform android   # генерирует android/
cd android && ./gradlew assembleDebug
# gomobile-библиотека:
#   go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init
#   cd backend && gomobile bind -target=android -o=mobile/android/app/libs/mobilecore.aar ./cmd/mobilebind
```

gomobile в CI этого репозитория не установлен, поэтому `.aar` не собирался;
команда выше — точная. JS-часть (bundler, types, tests) проверена полностью.
`expo-doctor`: единственное замечание — прямой dep `expo-modules-core`; он нужен
npm-хоистингу для jest-expo (peer зависимости npm не поднимает), безопасно.

## Сеть

- REST ноды: `EXPO_PUBLIC_NODE_API_BASE` (по умолчанию `http://10.0.2.2:8080` —
  эмулятор Android; на реальном устройстве — LAN-адрес машины с нодой).
- Foreground sync (gRPC): `EXPO_PUBLIC_SYNC_PEER` (по умолчанию `10.0.2.2:9090`).

## Что требует реального устройства

- Инициализация Go Core и запись наблюдений офлайн (SQLite в app data dir).
- Камера/медиатека через expo-image-picker и запись blob через мост.
- Foreground sync по gRPC с центральной нодой.
