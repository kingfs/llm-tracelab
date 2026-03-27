# Architecture

## Goal

`llm-tracelab` records LLM HTTP traffic once and reuses it many times.

## Data Flow

1. Client SDK sends an OpenAI-compatible request to the local proxy.
2. Proxy may normalize the request, for example injecting `stream_options.include_usage=true`.
3. Recorder writes the raw request and response into a `.http` cassette.
4. Recorder writes compact metadata into the cassette prelude and indexes summary fields into SQLite.
5. Monitor reads list/statistics from SQLite and reads the raw cassette only for detail pages.
6. Unit tests use `pkg/replay.Transport` to replay the recorded response from the cassette.

## Storage Model

- Raw cassette: `<output_dir>/<host>/<model>/<yyyy>/<mm>/<dd>/*.http`
- Metadata index: `<output_dir>/trace_index.sqlite3`
- Container convention: `/app/config/config.yaml` + `/app/data/traces`

The cassette remains the canonical replay artifact.
SQLite exists to avoid expensive aggregate rescans and to support fast monitor queries.

## Key Packages

- `internal/proxy`: reverse proxy, response interception, request normalization
- `internal/recorder`: file writer and metadata finalization
- `internal/store`: SQLite schema, sync, and query layer
- `internal/monitor`: HTML monitor and cassette detail parsing
- `pkg/recordfile`: shared V2/V3 parsing and V3 prelude writer
- `pkg/replay`: HTTP response replay transport for tests

## Compatibility

- V3 is the active write format.
- V2 is still a supported read format for monitor and replay.
- `migrate` can explicitly rewrite V2 cassettes to V3 and rebuild SQLite from raw files.
