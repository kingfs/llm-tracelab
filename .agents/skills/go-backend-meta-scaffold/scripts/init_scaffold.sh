#!/usr/bin/env bash

set -euo pipefail

usage() {
  echo "usage: $0 [phase1|phase2] TARGET_DIR MODULE_PATH SERVICE_NAME PROTO_PACKAGE RPC_SERVICE_NAME [PROTO_VERSION] [BOUNDED_CONTEXT] [DEFAULT_GRPC_PORT] [DEFAULT_HTTP_PORT]" >&2
}

STAGE="phase1"
if [[ "${1:-}" == "phase1" || "${1:-}" == "phase2" ]]; then
  STAGE="$1"
  shift
fi

if [[ $# -lt 5 || $# -gt 9 ]]; then
  usage
  exit 1
fi

TARGET_DIR="$1"
MODULE_PATH="$2"
SERVICE_NAME="$3"
PROTO_PACKAGE="$4"
RPC_SERVICE_NAME="$5"
PROTO_VERSION="${6:-v1}"
BOUNDED_CONTEXT="${7:-$SERVICE_NAME}"
DEFAULT_GRPC_PORT="${8:-50051}"
DEFAULT_HTTP_PORT="${9:-8080}"

if ! command -v go >/dev/null 2>&1; then
  echo "go toolchain is required; please install Go locally first" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE_DIR="$SCRIPT_DIR/../assets/templates"

should_defer_file() {
  local rel="$1"
  case "$rel" in
    cmd/service/*.tmpl| \
    ent/helper/tx.go| \
    ent/migrate/README.md| \
    ent/migrate/main.go| \
    ent/migrate/update_hash.go| \
    internal/app/app.go.tmpl| \
    internal/app/providers.go.tmpl| \
    internal/app/wire.go.tmpl| \
    internal/infrastructure/persistence/ent/query_repository.go.tmpl| \
    internal/infrastructure/persistence/ent/repository.go.tmpl)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

should_copy_file() {
  local stage="$1"
  local rel="$2"

  if should_defer_file "$rel"; then
    [[ "$stage" == "phase2" ]]
    return
  fi

  [[ "$stage" == "phase1" ]]
}

copy_templates_for_stage() {
  local stage="$1"

  while IFS= read -r -d '' template_path; do
    local rel_path="${template_path#$TEMPLATE_DIR/}"

    if ! should_copy_file "$stage" "$rel_path"; then
      continue
    fi

    mkdir -p "$(dirname "$TARGET_DIR/$rel_path")"
    cp "$template_path" "$TARGET_DIR/$rel_path"
  done < <(find "$TEMPLATE_DIR" -type f -print0)
}

move_if_exists() {
  local src="$1"
  local dst="$2"

  if [[ -e "$src" ]]; then
    mkdir -p "$(dirname "$dst")"
    mv "$src" "$dst"
  fi
}

if [[ "$STAGE" == "phase2" ]]; then
  if [[ ! -d "$TARGET_DIR" ]]; then
    echo "target directory does not exist: $TARGET_DIR" >&2
    exit 1
  fi

  if [[ -z "$(find "$TARGET_DIR/ent/dao" -type f -print -quit 2>/dev/null)" ]]; then
    echo "phase2 requires generated ent dao code; define ent/schema and run 'go generate ./ent/...' first" >&2
    exit 1
  fi
fi

python_output="$(
  python3 - "$SERVICE_NAME" "$PROTO_PACKAGE" "$RPC_SERVICE_NAME" "$BOUNDED_CONTEXT" "$(go env GOVERSION)" <<'PY'
import re
import sys

service_name = sys.argv[1]
proto_package = sys.argv[2]
rpc_service_name = sys.argv[3]
bounded_context = sys.argv[4]
go_version = sys.argv[5]

def pascal(value: str) -> str:
    parts = re.split(r"[^a-zA-Z0-9]+", value)
    merged = "".join(part[:1].upper() + part[1:] for part in parts if part)
    return merged or value[:1].upper() + value[1:]

def snake(value: str) -> str:
    value = re.sub(r"([a-z0-9])([A-Z])", r"\1_\2", value)
    value = re.sub(r"[^a-zA-Z0-9]+", "_", value)
    return value.lower().strip("_")

def snake_upper(value: str) -> str:
    return snake(value).upper()

normalized_go = go_version.removeprefix("go")
parts = normalized_go.split(".")
go_mod_version = ".".join(parts[:2]) if len(parts) >= 2 else normalized_go

service_file = snake(service_name)
service_struct = pascal(service_name)
proto_package_lower = snake(proto_package)
bounded_context_snake = snake(bounded_context)
bounded_context_pascal = pascal(bounded_context)
env_prefix = snake_upper(service_name)

print(service_file)
print(service_struct)
print(proto_package_lower)
print(bounded_context_snake)
print(bounded_context_pascal)
print(env_prefix)
print(go_mod_version)
print(rpc_service_name)
PY
)"

SERVICE_FILE_NAME="$(printf '%s\n' "$python_output" | sed -n '1p')"
SERVICE_STRUCT_NAME="$(printf '%s\n' "$python_output" | sed -n '2p')"
PROTO_PACKAGE_LOWER="$(printf '%s\n' "$python_output" | sed -n '3p')"
BOUNDED_CONTEXT_SNAKE="$(printf '%s\n' "$python_output" | sed -n '4p')"
BOUNDED_CONTEXT_PASCAL="$(printf '%s\n' "$python_output" | sed -n '5p')"
ENV_PREFIX="$(printf '%s\n' "$python_output" | sed -n '6p')"
GO_MOD_VERSION="$(printf '%s\n' "$python_output" | sed -n '7p')"
RPC_SERVICE_NAME="$(printf '%s\n' "$python_output" | sed -n '8p')"

mkdir -p "$TARGET_DIR"
copy_templates_for_stage "$STAGE"

mkdir -p \
  "$TARGET_DIR/internal/config" \
  "$TARGET_DIR/internal/domain" \
  "$TARGET_DIR/proto/$PROTO_PACKAGE_LOWER/$PROTO_VERSION" \
  "$TARGET_DIR/tests/e2e"

move_if_exists "$TARGET_DIR/README.md.tmpl" "$TARGET_DIR/README.md"
move_if_exists "$TARGET_DIR/AGENTS.md.tmpl" "$TARGET_DIR/AGENTS.md"
move_if_exists "$TARGET_DIR/ARCHITECTURE.md.tmpl" "$TARGET_DIR/ARCHITECTURE.md"
move_if_exists "$TARGET_DIR/cmd/service/main.go.tmpl" "$TARGET_DIR/cmd/$SERVICE_NAME/main.go"
move_if_exists "$TARGET_DIR/cmd/service/root.go.tmpl" "$TARGET_DIR/cmd/$SERVICE_NAME/root.go"
move_if_exists "$TARGET_DIR/cmd/service/serve.go.tmpl" "$TARGET_DIR/cmd/$SERVICE_NAME/serve.go"
move_if_exists "$TARGET_DIR/cmd/service/migrate.go.tmpl" "$TARGET_DIR/cmd/$SERVICE_NAME/migrate.go"
move_if_exists "$TARGET_DIR/cmd/service/migrate_up.go.tmpl" "$TARGET_DIR/cmd/$SERVICE_NAME/migrate_up.go"
move_if_exists "$TARGET_DIR/cmd/service/migrate_down.go.tmpl" "$TARGET_DIR/cmd/$SERVICE_NAME/migrate_down.go"
move_if_exists "$TARGET_DIR/internal/config/config.go.tmpl" "$TARGET_DIR/internal/config/config.go"
move_if_exists "$TARGET_DIR/internal/app/app.go.tmpl" "$TARGET_DIR/internal/app/app.go"
move_if_exists "$TARGET_DIR/internal/app/wire.go.tmpl" "$TARGET_DIR/internal/app/wire.go"
move_if_exists "$TARGET_DIR/internal/domain/context" "$TARGET_DIR/internal/domain/$BOUNDED_CONTEXT_SNAKE"
move_if_exists "$TARGET_DIR/ent/schema/context.go.tmpl" "$TARGET_DIR/ent/schema/${BOUNDED_CONTEXT_SNAKE}.go"
move_if_exists "$TARGET_DIR/proto/service.proto.tmpl" "$TARGET_DIR/proto/$PROTO_PACKAGE_LOWER/$PROTO_VERSION/$SERVICE_FILE_NAME.proto"
move_if_exists "$TARGET_DIR/tests/e2e/service_e2e_test.go.tmpl" "$TARGET_DIR/tests/e2e/${SERVICE_FILE_NAME}_e2e_test.go"

rmdir "$TARGET_DIR/cmd/service" 2>/dev/null || true

while IFS= read -r -d '' template_path; do
  mv "$template_path" "${template_path%.tmpl}"
done < <(find "$TARGET_DIR" -type f -name '*.tmpl' -print0)

python3 - \
  "$TARGET_DIR" \
  "$MODULE_PATH" \
  "$SERVICE_NAME" \
  "$SERVICE_FILE_NAME" \
  "$SERVICE_STRUCT_NAME" \
  "$PROTO_PACKAGE" \
  "$PROTO_PACKAGE_LOWER" \
  "$PROTO_VERSION" \
  "$RPC_SERVICE_NAME" \
  "$BOUNDED_CONTEXT" \
  "$BOUNDED_CONTEXT_SNAKE" \
  "$BOUNDED_CONTEXT_PASCAL" \
  "$ENV_PREFIX" \
  "$GO_MOD_VERSION" \
  "$DEFAULT_GRPC_PORT" \
  "$DEFAULT_HTTP_PORT" <<'PY'
import pathlib
import sys

target_dir = pathlib.Path(sys.argv[1])
replacements = {
    "__MODULE_PATH__": sys.argv[2],
    "__SERVICE_NAME__": sys.argv[3],
    "__SERVICE_FILE_NAME__": sys.argv[4],
    "__SERVICE_STRUCT_NAME__": sys.argv[5],
    "__PROTO_PACKAGE__": sys.argv[6],
    "__PROTO_PACKAGE_LOWER__": sys.argv[7],
    "__PROTO_VERSION__": sys.argv[8],
    "__RPC_SERVICE_NAME__": sys.argv[9],
    "__BOUNDED_CONTEXT__": sys.argv[10],
    "__BOUNDED_CONTEXT_SNAKE__": sys.argv[11],
    "__BOUNDED_CONTEXT_PASCAL__": sys.argv[12],
    "__ENV_PREFIX__": sys.argv[13],
    "__GO_VERSION__": sys.argv[14],
    "__DEFAULT_GRPC_PORT__": sys.argv[15],
    "__DEFAULT_HTTP_PORT__": sys.argv[16],
}

for path in target_dir.rglob("*"):
    if not path.is_file():
        continue
    content = path.read_text()
    for old, new in replacements.items():
        content = content.replace(old, new)
    path.write_text(content)
PY

if [[ -f "$TARGET_DIR/docker/entrypoint.sh" ]]; then
  chmod +x "$TARGET_DIR/docker/entrypoint.sh"
fi

if [[ "$STAGE" == "phase1" ]]; then
  echo "initialized go backend meta scaffold phase1 in $TARGET_DIR"
  echo "next: refine proto and ent/schema, then run 'go generate ./ent/...' (or 'task generate:ent'), then rerun this script with phase2"
else
  echo "initialized go backend meta scaffold phase2 in $TARGET_DIR"
  echo "next: run 'task generate', then create migrations with 'go run -mod=mod ent/migrate/main.go <migration_name>' and refresh atlas.sum with 'go run -mod=mod ent/migrate/update_hash.go ent/migrations'"
fi
