#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

: "${ANDROID_HOME:?set ANDROID_HOME}"
if [[ -z "${ANDROID_NDK_HOME:-}" && -d "${ANDROID_HOME}/ndk" ]]; then
  ANDROID_NDK_HOME="${ANDROID_HOME}/ndk/$(ls "${ANDROID_HOME}/ndk" | sort -V | tail -1)"
fi
: "${ANDROID_NDK_HOME:?set ANDROID_NDK_HOME}"

OUT="${1:-$ROOT/mobile/build/xproxy.aar}"
mkdir -p "$(dirname "$OUT")"
JAVAPKG="${JAVAPKG:-xproxy}"

gomobile bind -target=android -androidapi=21 -javapkg="$JAVAPKG" -o "$OUT" ./mobile
echo "wrote $OUT"
