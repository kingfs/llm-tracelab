FROM alpine:3.22

RUN apk add --no-cache bash gettext

WORKDIR /app

COPY dist/bin/__SERVICE_NAME__ /app/bin/__SERVICE_NAME__
COPY docker/entrypoint.sh /app/bin/entrypoint.sh
COPY docker/config.template.yaml /app/config/config.template.yaml
COPY ent /app/ent

RUN chmod +x /app/bin/__SERVICE_NAME__ /app/bin/entrypoint.sh

ENTRYPOINT ["/app/bin/entrypoint.sh"]
