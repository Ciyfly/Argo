#!/usr/bin/bash
# set -x
echo "build Argo"
go build -ldflags "-X main.Version=dev" -o argo  cmd/argo.go
