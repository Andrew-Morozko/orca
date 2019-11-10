#!/bin/bash
cd "$(dirname "${BASH_SOURCE[0]}")/.."

echo "*** building orca ***"
go build -v -o orca-release/orca
code=$?

if [ $code -ne 0 ]; then
    exit $code
fi
