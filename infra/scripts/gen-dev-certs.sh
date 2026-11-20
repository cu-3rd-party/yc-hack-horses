#!/usr/bin/env sh
# gen-dev-certs.sh — генерация DEVELOPMENT-ONLY сертификатов для локальной
# проверки mTLS (ADR-0007). НЕ ДЛЯ PRODUCTION: CA самоподписной, ключи без
# пароля, срок CA 10 лет (для удобства локальной разработки), нодовые серты
# 90 дней согласно ADR-0007.
#
# Выход: ./secrets/ (игнорируется git). Идемпотентен: существующие файлы
# не перезаписываются; передайте --force для перегенерации.
#
# Использование:
#   ./gen-dev-certs.sh                       # CA + central-сервер + 2 ноды
#   ./gen-dev-certs.sh --node <uuid> [...]   # добавить ноды
#   ./gen-dev-certs.sh --force               # перегенерировать всё
set -eu

if ! command -v openssl >/dev/null 2>&1; then
  echo "openssl не найден; установите openssl (или step CLI вручную)" >&2
  exit 1
fi

OUT_DIR="secrets"
FORCE=0
NODES=""
while [ $# -gt 0 ]; do
  case "$1" in
    --force) FORCE=1; shift ;;
    --node) NODES="$NODES $2"; shift 2 ;;
    *) echo "неизвестный аргумент: $1" >&2; exit 1 ;;
  esac
done

# Дефолтные dev-ноды, если не указаны.
[ -z "$NODES" ] && NODES="00000000-0000-4000-8000-000000000001 00000000-0000-4000-8000-000000000002"

# Central hostname — должен совпадать с ADR-0008/config.example.yaml.
CENTRAL_HOST="yc-hack-horses.cu3rd.ru"
CA_CN="Przewalski DEV CA (development-only, NOT for production)"
DAYS_CA=3650   # 10 лет
DAYS_LEAF=90   # согласно ADR-0007

mkdir -p "$OUT_DIR"
chmod 700 "$OUT_DIR" 2>/dev/null || true

gen_if_missing() {
  # gen_if_missing <file> <cmd...>
  if [ -f "$OUT_DIR/$1" ] && [ "$FORCE" -eq 0 ]; then
    echo "exists: $OUT_DIR/$1 (идемпотентно пропущено)"
    return 0
  fi
  shift
  "$@"
}

# --- Dev CA ---------------------------------------------------------------
gen_if_missing ca.key openssl genrsa -out "$OUT_DIR/ca.key" 4096
if [ -f "$OUT_DIR/ca.crt" ] && [ "$FORCE" -eq 0 ]; then
  echo "exists: $OUT_DIR/ca.crt"
else
  # CA через req + x509 -req -signkey с extfile: на OpenSSL 1.1.1
  # `req -x509 -addext` даёт CA, который не проходит openssl verify.
  printf 'basicConstraints=critical,CA:TRUE\nkeyUsage=critical,keyCertSign,cRLSign\nsubjectKeyIdentifier=hash\nauthorityKeyIdentifier=keyid:always\n' > "$OUT_DIR/ca.ext"
  openssl req -new -key "$OUT_DIR/ca.key" \
    -subj "/CN=$CA_CN/O=Przewalski Dev" -out "$OUT_DIR/ca.csr"
  openssl x509 -req -in "$OUT_DIR/ca.csr" -signkey "$OUT_DIR/ca.key" -sha256 \
    -days "$DAYS_CA" -extfile "$OUT_DIR/ca.ext" -out "$OUT_DIR/ca.crt"
  rm -f "$OUT_DIR/ca.ext" "$OUT_DIR/ca.csr"
fi

# --- Central-серверный серт (DNS SAN = central hostname) -------------------
gen_if_missing central.key openssl genrsa -out "$OUT_DIR/central.key" 3072
if [ -f "$OUT_DIR/central.crt" ] && [ "$FORCE" -eq 0 ]; then
  echo "exists: $OUT_DIR/central.crt"
else
  openssl req -new -key "$OUT_DIR/central.key" \
    -subj "/CN=$CENTRAL_HOST/O=Przewalski Dev" -out "$OUT_DIR/central.csr"
  openssl x509 -req -in "$OUT_DIR/central.csr" -CA "$OUT_DIR/ca.crt" -CAkey "$OUT_DIR/ca.key" \
    -CAcreateserial -days "$DAYS_LEAF" -sha256 \
    -extfile /dev/stdin -out "$OUT_DIR/central.crt" <<EOF
basicConstraints=CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:$CENTRAL_HOST
EOF
  fi

# --- Нодовые серты: URI SAN przewalski://node/<uuid> ------------------------
for NODE_ID in $NODES; do
  NAME="node-$NODE_ID"
  gen_if_missing "$NAME.key" openssl genrsa -out "$OUT_DIR/$NAME.key" 3072
  if [ -f "$OUT_DIR/$NAME.crt" ] && [ "$FORCE" -eq 0 ]; then
    echo "exists: $OUT_DIR/$NAME.crt"
    continue
  fi
  openssl req -new -key "$OUT_DIR/$NAME.key" \
    -subj "/CN=$NODE_ID/O=Przewalski Dev Node" -out "$OUT_DIR/$NAME.csr"
  openssl x509 -req -in "$OUT_DIR/$NAME.csr" -CA "$OUT_DIR/ca.crt" -CAkey "$OUT_DIR/ca.key" \
    -CAcreateserial -days "$DAYS_LEAF" -sha256 \
    -extfile /dev/stdin -out "$OUT_DIR/$NAME.crt" <<EOF
basicConstraints=CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=clientAuth,serverAuth
subjectAltName=URI:przewalski://node/$NODE_ID
EOF
done

# Уборка CSR и serial-файлов — не секреты, но мусор.
rm -f "$OUT_DIR"/*.csr "$OUT_DIR"/*.srl

echo "Готово. DEVELOPMENT-ONLY сертификаты в $OUT_DIR:"
ls "$OUT_DIR"
echo
echo "НЕ используйте их в production. Production-enrollment — см. infra/scripts/README.md."
