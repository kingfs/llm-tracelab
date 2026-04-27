#!/usr/bin/env bash

set -euo pipefail

usage() {
  echo "usage: $0 TARGET_DIR MODULE_PATH CLI_NAME [PRIMARY_COMMAND]" >&2
}

if [[ $# -lt 3 || $# -gt 4 ]]; then
  usage
  exit 1
fi

TARGET_DIR="$1"
MODULE_PATH="$2"
CLI_NAME="$3"
PRIMARY_COMMAND="${4:-run}"

if ! command -v go >/dev/null 2>&1; then
  echo "go toolchain is required; please install Go locally first" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE_DIR="$SCRIPT_DIR/../assets/templates"

python_output="$(
  python3 - "$CLI_NAME" "$PRIMARY_COMMAND" "$(go env GOVERSION)" <<'PY'
import re
import sys

cli_name = sys.argv[1]
primary_command = sys.argv[2]
go_version = sys.argv[3]

def pascal(value: str) -> str:
    parts = re.split(r"[^a-zA-Z0-9]+", value)
    merged = "".join(part[:1].upper() + part[1:] for part in parts if part)
    return merged or value[:1].upper() + value[1:]

def snake(value: str) -> str:
    value = re.sub(r"([a-z0-9])([A-Z])", r"\1_\2", value)
    value = re.sub(r"[^a-zA-Z0-9]+", "_", value)
    return value.lower().strip("_")

def kebab(value: str) -> str:
    value = re.sub(r"([a-z0-9])([A-Z])", r"\1-\2", value)
    value = re.sub(r"[^a-zA-Z0-9]+", "-", value)
    return value.lower().strip("-")

def snake_upper(value: str) -> str:
    return snake(value).upper()

normalized_go = go_version.removeprefix("go")
parts = normalized_go.split(".")
go_mod_version = ".".join(parts[:2]) if len(parts) >= 2 else normalized_go

cli_dir = kebab(cli_name)
cli_pkg = snake(cli_name)
cli_struct = pascal(cli_name)
command_file = snake(primary_command)
command_use = kebab(primary_command)
command_struct = pascal(primary_command)
env_prefix = snake_upper(cli_name)

print(cli_dir)
print(cli_pkg)
print(cli_struct)
print(command_file)
print(command_use)
print(command_struct)
print(env_prefix)
print(go_mod_version)
PY
)"

CLI_DIR_NAME="$(printf '%s\n' "$python_output" | sed -n '1p')"
CLI_PACKAGE_NAME="$(printf '%s\n' "$python_output" | sed -n '2p')"
CLI_STRUCT_NAME="$(printf '%s\n' "$python_output" | sed -n '3p')"
PRIMARY_COMMAND_FILE="$(printf '%s\n' "$python_output" | sed -n '4p')"
PRIMARY_COMMAND_USE="$(printf '%s\n' "$python_output" | sed -n '5p')"
PRIMARY_COMMAND_STRUCT="$(printf '%s\n' "$python_output" | sed -n '6p')"
ENV_PREFIX="$(printf '%s\n' "$python_output" | sed -n '7p')"
GO_MOD_VERSION="$(printf '%s\n' "$python_output" | sed -n '8p')"

mkdir -p "$TARGET_DIR"

while IFS= read -r -d '' template_path; do
  rel_path="${template_path#$TEMPLATE_DIR/}"
  mkdir -p "$(dirname "$TARGET_DIR/$rel_path")"
  cp "$template_path" "$TARGET_DIR/$rel_path"
done < <(find "$TEMPLATE_DIR" -type f -print0)

move_if_exists() {
  local src="$1"
  local dst="$2"

  if [[ -e "$src" ]]; then
    mkdir -p "$(dirname "$dst")"
    mv "$src" "$dst"
  fi
}

move_if_exists "$TARGET_DIR/README.md.tmpl" "$TARGET_DIR/README.md"
move_if_exists "$TARGET_DIR/AGENTS.md.tmpl" "$TARGET_DIR/AGENTS.md"
move_if_exists "$TARGET_DIR/ARCHITECTURE.md.tmpl" "$TARGET_DIR/ARCHITECTURE.md"
move_if_exists "$TARGET_DIR/cmd/service/main.go.tmpl" "$TARGET_DIR/cmd/$CLI_DIR_NAME/main.go"
move_if_exists "$TARGET_DIR/cmd/service/root.go.tmpl" "$TARGET_DIR/cmd/$CLI_DIR_NAME/root.go"
move_if_exists "$TARGET_DIR/cmd/service/version.go.tmpl" "$TARGET_DIR/cmd/$CLI_DIR_NAME/version.go"
move_if_exists "$TARGET_DIR/cmd/service/completion.go.tmpl" "$TARGET_DIR/cmd/$CLI_DIR_NAME/completion.go"
move_if_exists "$TARGET_DIR/cmd/service/run.go.tmpl" "$TARGET_DIR/cmd/$CLI_DIR_NAME/${PRIMARY_COMMAND_FILE}.go"
move_if_exists "$TARGET_DIR/internal/config/config.go.tmpl" "$TARGET_DIR/internal/config/config.go"
move_if_exists "$TARGET_DIR/internal/app/app.go.tmpl" "$TARGET_DIR/internal/app/app.go"
move_if_exists "$TARGET_DIR/internal/output/printer.go.tmpl" "$TARGET_DIR/internal/output/printer.go"
move_if_exists "$TARGET_DIR/internal/version/version.go.tmpl" "$TARGET_DIR/internal/version/version.go"
move_if_exists "$TARGET_DIR/internal/domain/runner.go.tmpl" "$TARGET_DIR/internal/$CLI_PACKAGE_NAME/runner.go"
move_if_exists "$TARGET_DIR/tests/e2e/cli_e2e_test.go.tmpl" "$TARGET_DIR/tests/e2e/${CLI_PACKAGE_NAME}_e2e_test.go"

rmdir "$TARGET_DIR/cmd/service" 2>/dev/null || true
rmdir "$TARGET_DIR/internal/domain" 2>/dev/null || true

while IFS= read -r -d '' template_path; do
  mv "$template_path" "${template_path%.tmpl}"
done < <(find "$TARGET_DIR" -type f -name '*.tmpl' -print0)

python3 - \
  "$TARGET_DIR" \
  "$MODULE_PATH" \
  "$CLI_NAME" \
  "$CLI_DIR_NAME" \
  "$CLI_PACKAGE_NAME" \
  "$CLI_STRUCT_NAME" \
  "$PRIMARY_COMMAND" \
  "$PRIMARY_COMMAND_FILE" \
  "$PRIMARY_COMMAND_USE" \
  "$PRIMARY_COMMAND_STRUCT" \
  "$ENV_PREFIX" \
  "$GO_MOD_VERSION" <<'PY'
import pathlib
import sys

target_dir = pathlib.Path(sys.argv[1])
replacements = {
    "__MODULE_PATH__": sys.argv[2],
    "__CLI_NAME__": sys.argv[3],
    "__CLI_DIR_NAME__": sys.argv[4],
    "__CLI_PACKAGE_NAME__": sys.argv[5],
    "__CLI_STRUCT_NAME__": sys.argv[6],
    "__PRIMARY_COMMAND__": sys.argv[7],
    "__PRIMARY_COMMAND_FILE__": sys.argv[8],
    "__PRIMARY_COMMAND_USE__": sys.argv[9],
    "__PRIMARY_COMMAND_STRUCT__": sys.argv[10],
    "__ENV_PREFIX__": sys.argv[11],
    "__GO_VERSION__": sys.argv[12],
}

for path in target_dir.rglob("*"):
    if not path.is_file():
        continue
    content = path.read_text()
    for old, new in replacements.items():
        content = content.replace(old, new)
    path.write_text(content)
PY

echo "initialized go cli meta scaffold in $TARGET_DIR"
echo "next: refine cmd/$CLI_DIR_NAME/${PRIMARY_COMMAND_FILE}.go and internal/$CLI_PACKAGE_NAME, then run 'task deps', 'task lint', 'task test', 'task build'"
