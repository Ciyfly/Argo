#!/usr/bin/env bash
set -euo pipefail

DEFAULT_PLATFORMS=(linux/amd64)
PLATFORMS=("${DEFAULT_PLATFORMS[@]}")
OUTPUT_DIR="bin"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --platforms|-p)
      IFS=',' read -ra PLATFORMS <<< "$2"
      shift 2
      ;;
    --output|-o)
      OUTPUT_DIR="$2"
      shift 2
      ;;
    *)
      echo -e "\033[31m[build] Unknown argument: $1\033[0m"
      exit 1
      ;;
  esac
done

mkdir -p "$OUTPUT_DIR"

for platform in "${PLATFORMS[@]}"; do
  IFS='/' read -r GOOS GOARCH <<< "$platform"
  BIN_NAME="argo-$GOOS-$GOARCH"
  echo -e "\033[34m[build] Building for $GOOS/$GOARCH\033[0m"
  env GOOS="$GOOS" GOARCH="$GOARCH" go build -ldflags "-X main.Version=dev" -o "$OUTPUT_DIR/$BIN_NAME" cmd/argo.go
  echo -e "\033[32m[build] Output -> $OUTPUT_DIR/$BIN_NAME\033[0m"
done
