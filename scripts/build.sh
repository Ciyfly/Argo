#!/usr/bin/env bash
set -euo pipefail

# 默认构建：linux/amd64
DEFAULT_PLATFORMS=(linux/amd64)
# all = 全平台（交叉编译默认关闭 cgo，JSluice 将自动降级为正则提取，保证可构建）
ALL_PLATFORMS=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64)
PLATFORMS=("${DEFAULT_PLATFORMS[@]}")
OUTPUT_DIR="bin"

usage() {
  cat <<EOF
用法: $0 [platform] [options]

platform（可选，快捷写法）:
  (不填)     -> linux/amd64
  linux     -> linux/amd64
  arm64     -> linux/arm64
  windows   -> windows/amd64
  all       -> linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
  也支持直接传 GOOS/GOARCH 或逗号分隔列表，例如: linux/amd64,windows/amd64

Options:
  -p, --platforms, -h   逗号分隔的 GOOS/GOARCH 列表（默认: linux/amd64）
  -o, --output          输出目录（默认: bin）
      --help            显示帮助

Examples:
  $0
  $0 windows
  $0 arm64
  $0 all
  $0 -p linux/amd64,windows/amd64
  $0 linux/amd64,windows/amd64 -o dist
EOF
}

apply_platform_preset() {
  local preset="$1"
  case "$preset" in
    ""|linux)
      PLATFORMS=(linux/amd64)
      ;;
    arm64|linux/arm64)
      PLATFORMS=(linux/arm64)
      ;;
    windows|windows/amd64)
      PLATFORMS=(windows/amd64)
      ;;
    all)
      PLATFORMS=("${ALL_PLATFORMS[@]}")
      ;;
    *)
      if [[ "$preset" == *","* ]]; then
        IFS=',' read -ra PLATFORMS <<< "$preset"
      elif [[ "$preset" == */* ]]; then
        PLATFORMS=("$preset")
      else
        echo -e "\033[31m[build] Unknown platform preset: $preset\033[0m"
        usage
        exit 1
      fi
      ;;
  esac
}

# 支持 positional platform：./scripts/build.sh windows|arm64|all
if [[ $# -gt 0 && "${1:0:1}" != "-" ]]; then
  apply_platform_preset "$1"
  shift
fi

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
