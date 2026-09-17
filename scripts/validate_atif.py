#!/usr/bin/env python3
"""Validate ATIF-v1.8 JSONL with pinned, unmodified Harbor Pydantic models.

Install scripts/atif-requirements.txt once. Validation never accesses the network.
"""
import argparse
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / "atif_vendor"))
try:
    from pydantic import ValidationError
    from harbor.models.trajectories.trajectory import Trajectory
except ImportError as exc:
    raise SystemExit("Install dependencies: python3 -m pip install -r scripts/atif-requirements.txt") from exc

VERSION = "ATIF-v1.8"
COMMIT = "74cc6312018c349c6bd2400c89a0ac4983ac1085"


def validate_record(value):
    """Keep the upstream validators, additionally require explicit v1.8."""
    if not isinstance(value, dict) or value.get("schema_version") != VERSION:
        raise ValueError("schema_version must explicitly be ATIF-v1.8")
    result = Trajectory.model_validate(value, strict=True)
    for sub in value.get("subagent_trajectories") or []:
        validate_record(sub)
    return result


def generate_schema():
    schema = Trajectory.model_json_schema()
    schema["$defs"]["Trajectory"]["properties"]["schema_version"] = {"const": VERSION, "type": "string"}
    required = schema["$defs"]["Trajectory"]["required"]
    if "schema_version" not in required:
        required.insert(0, "schema_version")
    schema["$comment"] = f"Generated from Harbor commit {COMMIT}; explicit {VERSION} required. Regenerate with scripts/validate_atif.py --write-schema."
    return schema


def validate_file(path):
    count, failures = 0, 0
    with path.open(encoding="utf-8") as stream:
        for line_number, line in enumerate(stream, 1):
            try:
                if not line.strip():
                    raise ValueError("blank lines are not trajectory records")
                validate_record(json.loads(line))
                count += 1
            except (ValueError, ValidationError) as exc:
                failures += 1
                # Never print user messages or tool outputs from validation errors.
                if isinstance(exc, ValidationError):
                    detail = "; ".join(f"{'.'.join(map(str,e['loc']))}: {e['msg']}" for e in exc.errors(include_input=False, include_url=False))
                else:
                    detail = str(exc)
                print(f"INVALID {path}:{line_number}: {detail}", file=sys.stderr)
    if count == 0 and failures == 0:
        print(f"INVALID {path}: empty JSONL file", file=sys.stderr)
        failures = 1
    print(f"{path}: {count} valid {VERSION} trajectories, {failures} invalid records")
    return failures == 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("files", type=Path, nargs="*")
    parser.add_argument("--write-schema", type=Path, help="Regenerate the offline JSON Schema from the vendored models")
    args = parser.parse_args()
    if args.write_schema:
        args.write_schema.write_text(json.dumps(generate_schema(), ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    if not args.files and not args.write_schema:
        parser.error("provide one or more JSONL files")
    ok = True
    for path in args.files:
        try:
            ok = validate_file(path) and ok
        except OSError as exc:
            print(f"INVALID {path}: {exc.strerror}", file=sys.stderr)
            ok = False
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
