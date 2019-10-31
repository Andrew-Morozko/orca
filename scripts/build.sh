#!/bin/bash
cd "$( dirname "${BASH_SOURCE[0]}" )/.."

go build -v -o orca-release/orca
exit $?
