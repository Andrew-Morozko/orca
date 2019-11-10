#!/bin/bash
cd "$(dirname "${BASH_SOURCE[0]}")/.."

echo "*** building ldap-server ***"
go build -v -o ./ldap/ldap-server-release/ldap-server ./ldap/server/
code=$?

if [ $code -ne 0 ]; then
    exit $code
fi
