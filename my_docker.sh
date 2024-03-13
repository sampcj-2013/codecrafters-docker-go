#!/bin/sh
set -e
tmpFile=$(mktemp)
go build  -ldflags "-X main.debugCapabilities=yes" -o "$tmpFile" app/*.go
exec "$tmpFile" "$@"
