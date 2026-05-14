# Development Commands

This project keeps stable `task` entry points so humans and AI agents can run the same checks without guessing the right Go or UI commands.

## Daily Commands

Use these for normal local development:

```bash
task fmt
task check:quick
task build:go
task run
```

`task fmt` rewrites Go files. `task check:quick` does not rewrite files; it checks formatting, runs `golangci-lint`, and runs short tests.

`task run` uses the tracked `config/config.yaml` by default. Override explicitly with `CONFIG=path/to/config.yaml task run`.

## Validation Levels

Use the smallest validation level that matches the change:

```bash
task fmt:check
task lint
task lint:vet
task test:short
task test
task test:e2e
task test:race
task test:cover
task check:quick
task check:full
```

- `task check:quick` is the default pre-commit gate for focused changes.
- `task test:e2e` runs local end-to-end coverage for proxy recording/replay, CLI/management wiring, and cassette fixture workflows. It must not depend on real provider network access or API keys.
- `task test:race` should be run after changes to proxying, routing, recorder concurrency, SQLite access, or streaming behavior.
- `task lint` runs the tracked `golangci-lint` configuration.
- `task lint:vet` runs `go vet ./...` directly for troubleshooting.
- `task check:full` is the release-style gate: formatting check, lint, tests, explicit end-to-end tests, race tests, UI build, and Go build.

## Build Commands

```bash
task build:go
task ui:build
task ui:test
task build:all
task build
```

`task build:go` is useful when changing backend code only. `task build` and `task build:all` rebuild the embedded monitor UI before compiling the server.

For monitor UI changes, run:

```bash
task ui:build
task ui:test
go test ./internal/monitor
task build:go
```

`task ui:test` runs Playwright browser smoke tests with mocked Monitor APIs for the model/channel pages and trace routing links. `go test ./internal/monitor` includes an embedded UI smoke test that verifies the SPA entry routes and built JS/CSS assets are served from Go `embed.FS`.

## Benchmarks

```bash
task bench
task bench:core
```

Run `task bench:core` after changes to these hot paths:

- `internal/proxy`
- `internal/router`
- `internal/store`
- `pkg/llm`
- `pkg/recordfile`
- `pkg/replay`

Benchmarks must not depend on network access or real provider API keys.

## Dependency Commands

```bash
task deps:verify
task deps:tidy
```

Use `task deps:verify` in checks because it does not edit module files. Use `task deps:tidy` intentionally when dependencies changed.

## Agent Guidance

For AI agents, prefer these defaults:

- Small code change: `task check:quick`
- End-to-end behavior change: `task test:e2e`
- Record format, replay, or monitor parsing change: `go test ./pkg/recordfile ./pkg/replay ./internal/monitor ./unittest`
- Monitor UI source or embedded asset change: `task ui:build`, `task ui:test`, `go test ./internal/monitor`, and `task build:go`
- Proxy, router, recorder, or store change: `go test ./internal/proxy ./internal/router ./internal/recorder ./internal/store` and `task test:race`
- Performance-sensitive change: `task bench:core`
- Before handing off a broad change: `task check:full`
