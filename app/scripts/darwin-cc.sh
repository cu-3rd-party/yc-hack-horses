#!/bin/sh
# clang >= 11 нужен для таргетов arm64-apple-macos* (старые клэнги не понимают
# новый формат версий darwin). Берём самый свежий установленный clang.
# Линкер: -fuse-ld=lld ищет ld.lld в PATH — пакет lld в Ubuntu его ставит.
for ver in -20 -19 -18 -17 -16 -15 -14 -13 -12 -11 ""; do
  cc="clang$ver"
  if command -v "$cc" >/dev/null 2>&1; then
    exec "$cc" -target arm64-apple-macos11.0 -isysroot "$LOSHID_MAC_SDK" -fuse-ld=lld -Wno-unused-command-line-argument "$@"
  fi
done
echo "darwin-cc.sh: clang >= 11 not found" >&2
exit 1
