#!/usr/bin/env bash
set -euo pipefail

DEFAULT_PLATFORMS=(linux/amd64)
PLATFORMS=("${DEFAULT_PLATFORMS[@]}")
OUTPUT_DIR="bin"

usage() {
  cat <<EOF
Usage: $0 [options]

Options:
  -p, --platforms, -h   Comma separated GOOS/GOARCH list (default: linux/amd64)
                         Common values: linux/amd64, linux/arm64, darwin/amd64,
                         darwin/arm64, windows/amd64
  -o, --output          Output directory (default: bin)
      --help            Show this message

Examples:
  $0 -p linux/amd64,windows/amd64
  $0 -h darwin/arm64 -o dist
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --help)
      usage
      exit 0
      ;;
    -h)
      if [[ $# -gt 1 && $2 != -* ]]; then
        IFS=',' read -ra PLATFORMS <<< "$2"
        shift 2
      else
        usage
        exit 0
      fi
      ;;
    --platforms|-p)
      if [[ $# -lt 2 ]]; then
        echo -e "\033[31m[build] Missing value for $1\033[0m"
        usage
        exit 1
      fi
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
  if [[ "$GOOS" == "windows" ]]; then
    BIN_NAME+=".exe"
  fi
  echo -e "\033[34m[build] Building for $GOOS/$GOARCH\033[0m"
  env GOOS="$GOOS" GOARCH="$GOARCH" go build -ldflags "-X main.Version=dev" -o "$OUTPUT_DIR/$BIN_NAME" cmd/argo.go
  echo -e "\033[32m[build] Output -> $OUTPUT_DIR/$BIN_NAME\033[0m"
done
