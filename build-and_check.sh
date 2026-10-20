#!/usr/bin/env bash
set -euo pipefail

# ── Build & verify script for crush ──────────────────────────────────
# Usage: ./build-and_check.sh [output-name]
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
TARGET="${SCRIPT_DIR}/../${OUTPUT}"
# 运行中的二进制无法被 cp 覆盖（ETXTBSY），先拷到同目录临时文件再 rename：
# rename 只替换目录项，旧进程继续持有旧 inode，新进程启动即用新文件。
TMP_TARGET="${TARGET}.tmp.$$"
trap 'rm -f "${TMP_TARGET}"' EXIT
cp "${OUTPUT}" "${TMP_TARGET}"
mv -f "${TMP_TARGET}" "${TARGET}"
trap - EXIT
echo "✅ Replaced: ${TARGET}"

echo ""
echo "✅ Build complete: ${SCRIPT_DIR}/${OUTPUT}"
ls -lh "${OUTPUT}"