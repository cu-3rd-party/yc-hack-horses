# ADR-0002: Go Sync Engine architecture
**Status:** accepted
## Решение
Один Go-пакет `internal/sync` для всех типов нод. Различия — через `storage.EventStore`, capabilities, транспорт и конфигурацию. Никаких отдельных реализаций sync для desktop/mobile/central.
## Почему
Дублирование протокола между нодами гарантированно ведёт к расхождению поведения. Альтернатива — отдельные движки — отклонена.
