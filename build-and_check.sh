#!/usr/bin/env bash
set -euo pipefail

# ── Build & verify script for crush ──────────────────────────────────
# Usage: ./build-and-verify.sh [output-name]
# Default output name: my-crush

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OUTPUT="${1:-my-crush}"

echo "==> 1/4: Checking Go version..."
go version

echo "==> 2/4: Building '${OUTPUT}'..."
cd "$SCRIPT_DIR"
CGO_ENABLED=0 GOEXPERIMENT=greenteagc go build -ldflags="-s -w" -trimpath -o "${OUTPUT}" -v .

echo "==> 3/4: Verifying version..."
./"${OUTPUT}" --version

echo "==> 4/4: Copying to parent directory..."
cp "${OUTPUT}" "${SCRIPT_DIR}/../${OUTPUT}"
echo "✅ Replaced: ${SCRIPT_DIR}/../${OUTPUT}"

echo ""
echo "✅ Build complete: ${SCRIPT_DIR}/${OUTPUT}"
ls -lh "${OUTPUT}"