# ADR-0001: Monorepo structure
**Status:** accepted
## Решение
Единый монорепозиторий: `backend/` (Go module), `app/` (Electron и React), `mobile/` (RN), `packages/contracts/` (TS-типы), `infra/`, `docs/adr/`. Proto-контракты — `backend/api/proto/`.
## Почему
Синхронные контракты между слоями и один PR-процесс дешевле мультирепо для команды из одного человека. Альтернатива — отдельные репо на клиент — отклонена: сложность версионирования контрактов.
