#!/usr/bin/env sh

set -eu

mkdir -p /app/config

envsubst < /app/config/config.template.yaml > /app/config/config.yml

exec /app/bin/__SERVICE_NAME__ serve --config /app/config/config.yml

