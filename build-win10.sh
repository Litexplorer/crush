#!/usr/bin/env bash
set -euo pipefail

# ── Build & verify script for crush (Windows x64 cross-compile) ──────
# Usage: ./build-win10.sh [output-name]
# Default output name: my-crush.exe

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BASE="${1:-my-crush}"
OUTPUT="${BASE%.exe}.exe"  # ensure .exe suffix

echo "==> 1/4: Checking Go version..."
go version

echo "==> 2/4: Cross-compiling '${OUTPUT}' for Windows x64..."
cd "$SCRIPT_DIR"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOEXPERIMENT=greenteagc \
   go build -ldflags="-s -w" -trimpath -o "${OUTPUT}" -v .

echo "==> 3/4: Verifying version (via go version -m)..."
go version -m "${OUTPUT}" 2>/dev/null | head -5 || \
   echo "   (Windows binary — cannot execute directly)"

echo "==> 4/4: Copying to parent directory..."
cp "${OUTPUT}" "${SCRIPT_DIR}/../${OUTPUT}"
echo "✅ Replaced: ${SCRIPT_DIR}/../${OUTPUT}"

echo ""
echo "✅ Build complete: ${SCRIPT_DIR}/${OUTPUT}"
ls -lh "${OUTPUT}"