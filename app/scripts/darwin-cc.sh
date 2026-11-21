#!/bin/sh
exec clang -target arm64-apple-macos11 -isysroot "$LOSHID_MAC_SDK" -fuse-ld=lld -Wno-unused-command-line-argument "$@"
