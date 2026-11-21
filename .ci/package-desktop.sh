#!/usr/bin/env bash
# Build downloadable desktop packages on the linux-amd64 Forgejo runner.
set -euo pipefail
cd "$(dirname "$0")/../app"
version=$(node -p "require('./package.json').version")
release="$PWD/release/downloads"
mkdir -p "$release"
tools_dir=$(mktemp -d)
trap 'rm -rf "$tools_dir"' EXIT

npm run build
builder=./node_modules/.bin/electron-builder
"$builder" --linux AppImage deb --x64 --publish never
"$builder" --mac dir --arm64 --publish never

# electron-builder assembles the Mac app on Linux but skips native codesign.
# Re-sign the Electron bundle and nested helpers after their names/plists change.
signer=apple-codesign-0.29.0-x86_64-unknown-linux-musl
curl -fsSL --retry 3 "https://github.com/indygreg/apple-platform-rs/releases/download/apple-codesign%2F0.29.0/$signer.tar.gz" -o "$tools_dir/signer.tar.gz"
(cd "$tools_dir" && echo 'dbe85cedd8ee4217b64e9a0e4c2aef92ab8bcaaa41f20bde99781ff02e600002  signer.tar.gz' | sha256sum --check && tar -xzf signer.tar.gz)
"$tools_dir/$signer/rcodesign" sign --entitlements-xml-file node_modules/app-builder-lib/templates/entitlements.mac.plist release/mac-arm64/yc-hack-horses.app
mac="ЛошID-$version-macOS-arm64.zip"
appimage="ЛошID-$version-Linux-x86_64.AppImage"
deb="ЛошID-$version-Linux-amd64.deb"
(cd release/mac-arm64 && zip -qr -y "$tools_dir/$mac" yc-hack-horses.app)
mv "$tools_dir/$mac" "$release/$mac"
cp "release/yc-hack-horses-$version.AppImage" "$release/$appimage"
cp "release/yc-hack-horses_${version}_amd64.deb" "$release/$deb"
(cd "$release" && sha256sum "$mac" "$appimage" "$deb" > SHA256SUMS)
