# ADR-0007: mTLS trust model
**Status:** accepted (amended: leaf-серты enrollment-нод — 365 дней, см. ADR-0014)
## Решение
Один dev/production CA. Каждая нода — собственный клиентский серт (URI SAN с node_id), каждое межнодовое gRPC-соединение — mTLS: обе стороны проверяют цепочку, срок, node_id пира и authorization (capabilities). Enrollment новых устройств — явный: подпись CSR доверенным CA (dev-скрипт `infra/scripts/gen-dev-certs.sh`; production-процедура — отдельная документация). Никакого самоподписного auto-trust.
## Почему
CA-based trust с явным enrollment минимален и достаточен для V0. Отзыв — крошечный on-disk revocation list у CA + срок действия сертов 90 дней (dev). Amendment (ADR-0014): leaf-серты, выдаваемые enrollment-службой central, живут 365 дней; выдачи фиксируются в on-disk списке enrollment-issued.jsonl — задел под revocation list.
## Self-service-специфика центральной ноды
Центральная нода стоит за ingress (TLS-терминация чартом, серт на `*.cu3rd.ru`). mTLS на уровне ingress-чарта недоступен, поэтому: серверная TLS-идентичность hostname проверяется клиентом по публичному цепочке — криптографически гарантировано; клиентская идентичность проверяется на стороне приложения: ingress (Traefik middleware passTLSClientCert) прокидывает клиентский серт заголовком, приложение валидирует цепочку против CA и URI SAN. Заголовок от произвольного клиента недоверен: middleware перезаписывает его, исходное значение клиента игнорируется. Развёрнутая схема — ADR-0008.
