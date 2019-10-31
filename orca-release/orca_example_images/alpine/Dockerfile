FROM alpine:latest

RUN touch /123

LABEL orca.enabled=true
LABEL orca.kind=ssh
# LABEL orca.timeout.inactive=5s
# LABEL orca.timeout.session=10s
LABEL orca.users.concurrent=3
LABEL orca.users.total=4