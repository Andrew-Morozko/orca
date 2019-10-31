FROM nginx:latest

RUN touch /123

LABEL orca.enabled=true
LABEL orca.kind=web
LABEL orca.port=80

LABEL orca.timeout.inactive=10s
LABEL orca.timeout.session=30s