#!/usr/bin/env bash
set -euo pipefail

echo -e "\033[34m[test] go test ./...\033[0m"
go test ./... -v
