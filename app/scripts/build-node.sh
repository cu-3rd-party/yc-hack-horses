#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root/backend"
target_os=$1
case "$2" in arm64) target_cpu=arm64;; x64) target_cpu=amd64;; *) exit 1;; esac
out="$root/app/build/node/$target_os-$2"
mkdir -p "$out"
go_bin=${GO_BINARY:-go}
# Copy SQL so the packaged executable can resolve migrations relative to itself.
cp -R "$root/backend/migrations" "$out/"
case "$target_os-$target_cpu" in
 darwin-arm64)
    rid=osx-arm64; lib=libonnxruntime.dylib
    if [ "$(uname -s)" != Darwin ]; then
      sdk="$root/app/build/macos-sdk"
      mkdir -p "$sdk"
      if [ ! -d "$sdk/MacOSX14.5.sdk" ]; then
        curl -fsSL --retry 3 -o "$sdk/sdk.tar.xz" 'https://github.com/joseluisq/macosx-sdks/releases/download/14.5/MacOSX14.5.sdk.tar.xz'
        (cd "$sdk" && echo '6e146275d19f027faa2e8354da5e0267513abf013b8f16ad65a231653a2b1c5d  sdk.tar.xz' | sha256sum -c - && tar -xf sdk.tar.xz)
      fi
      export LOSHID_MAC_SDK="$sdk/MacOSX14.5.sdk" CC="$root/app/scripts/darwin-cc.sh"
    fi;;
 linux-amd64)
    rid=linux-x64; lib=libonnxruntime.so;;
 *) echo "Unsupported desktop target: $target_os-$target_cpu" >&2; exit 1;;
esac
if [ -n "${ONNXRUNTIME_BUNDLE_DIR:-}" ]; then
  # A local distribution can also supply the library and its notices for offline builds.
  rm -f "$out/ort-1.31.1.ready"
  for file in "$lib" LICENSE ThirdPartyNotices.txt; do
    cp "$ONNXRUNTIME_BUNDLE_DIR/$file" "$out/$file.tmp"
    mv -f "$out/$file.tmp" "$out/$file"
  done
elif [ ! -f "$out/ort-1.31.1.ready" ] || [ ! -f "$out/$lib" ]; then
  pkg=$(mktemp); trap 'rm -f "$pkg"' EXIT
  curl -fsSL --retry 3 -o "$pkg" 'https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime/1.31.1/microsoft.ml.onnxruntime.1.31.1.nupkg'
  python3 - "$pkg" "$rid" "$out" "$lib" <<'PYTHON'
import sys, zipfile, hashlib, shutil, pathlib, os
archive, rid, directory, lib = sys.argv[1:]
out = pathlib.Path(directory)
with open(archive, 'rb') as f:
    digest = hashlib.sha256()
    for block in iter(lambda: f.read(1 << 20), b''):
        digest.update(block)
    if digest.hexdigest() != '6871ccbc97f3d5bdb4095a1dcec33aa78d56f6ed8de5980ef5fa1d9f2e7c679a':
        raise SystemExit('unexpected ONNX Runtime archive')
    with zipfile.ZipFile(f) as z:
        with z.open(f'runtimes/{rid}/native/{lib}') as source, (out/(lib+'.tmp')).open('wb') as target:
            shutil.copyfileobj(source,target)
        os.replace(out/(lib+'.tmp'),out/lib)
        for name in ('LICENSE','ThirdPartyNotices.txt'):
            (out/name).write_bytes(z.read(name))
(out/'ort-1.31.1.ready').touch()
PYTHON
fi
GOOS="$target_os" GOARCH="$target_cpu" CGO_ENABLED=1 "$go_bin" build -trimpath -tags netgo,osusergo -o "$out/loshid-node" ./cmd/node
