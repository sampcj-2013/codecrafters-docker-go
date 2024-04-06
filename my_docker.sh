#!/bin/sh
#
: ${GOEXEC:=/home/scar8708/go/bin/go1.22.1}
set -e
tmpFile=$(mktemp)
$GOEXEC build  -ldflags "-X main.debugCapabilities=yes" -o "$tmpFile" app/*.go
echo GO has finished COMPILING
exec "$tmpFile" "$@"
