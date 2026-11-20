# Provisioning сертификатов нод

## Dev (локальная разработка)

`./gen-dev-certs.sh` генерирует **development-only** материалы в `./secrets/`
(в `.gitignore`):

| файл | назначение |
|---|---|
| `ca.crt` / `ca.key` | dev-CA: самоподписной, 10 лет, CN помечен `development-only` |
| `central.crt` / `central.key` | серверный серт для `yc-hack-horses.cu3rd.ru` (SAN DNS) |
| `node-<uuid>.crt` / `node-<uuid>.key` | нодовые серты, SAN `URI:przewalski://node/<uuid>` (ADR-0006) |

Свойства: leaf-серты 90 дней (ADR-0007), idempotent (существующие файлы не
перезаписываются; `--force` — перегенерация), только `openssl`. Пример:

```sh
infra/scripts/gen-dev-certs.sh --node 0198a7b4-aaaa-7bbb-8ccc-ddddeee00001
```

## Production

Dev-скрипт и его CA в production не используются. Enrollment новых устройств —
явный: оператор подписывает CSR устройства доверенным production CA; никаких
самоподписных auto-trust. Отзыв — on-disk revocation list у CA + срок leaf 90
дней (ADR-0007). Central hostname фиксируется в ADR-0008:
`yc-hack-horses.cu3rd.ru`; central определяется hostname + публично валидным
сертом, а не конфигом ноды (`role_hint` — только подсказка для CLI).

## Проверка

```sh
openssl verify -CAfile secrets/ca.crt secrets/node-*.crt secrets/central.crt
openssl x509 -in secrets/node-<uuid>.crt -noout -text | grep "URI:"
```
