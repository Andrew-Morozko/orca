#!/bin/bash
cd "$( dirname "${BASH_SOURCE[0]}" )/.."

env go build -v -o orca-release/orca
retVal=$?
if [ $retVal -ne 0 ]; then
    exit $retVal
fi
