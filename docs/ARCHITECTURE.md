# Architecture

## Goal

`llm-tracelab` records LLM HTTP traffic once and reuses it many times.

## Data Flow

1. Client SDK sends an OpenAI-compatible request to the local proxy.
2. Proxy may normalize the request, for example injecting `stream_options.include_usage=true`.
3. Recorder writes the raw request and response into a `.http` cassette.
4. `pkg/llm` normalizes provider-specific request/response semantics, stream transcripts, token usage, and event timelines.
5. `internal/upstream` resolves config into protocol family, routing profile, auth headers, and upstream URL behavior.
6. Recorder writes compact metadata plus `# event:` timeline lines into the cassette prelude and indexes summary fields into SQLite.
7. Monitor reads list/statistics from SQLite and reads the raw cassette only for detail pages.
8. Session-oriented monitor views are aggregated from SQLite using group metadata extracted from raw request headers such as `Session_id`.
8. Unit tests use `pkg/replay.Transport` to replay the recorded response from the cassette.

## Storage Model

- Raw cassette: `<output_dir>/<host>/<model>/<yyyy>/<mm>/<dd>/*.http`
- Metadata index: `<output_dir>/trace_index.sqlite3`
- Container convention: `/app/config/config.yaml` + `/app/data/traces`

The cassette remains the canonical replay artifact.
SQLite exists to avoid expensive aggregate rescans and to support fast monitor queries.
It also stores additive grouping metadata such as `session_id` so the monitor can switch between request and session perspectives without rescanning raw files.

## V3 Prelude And Timeline

`LLM_PROXY_V3` now has three logical layers before the raw payload:

1. `# meta:` compact request summary and normalized usage
2. `# event:` timeline rows
3. blank line + raw HTTP request/response bytes

The base recorder still emits request/response lifecycle events, but the proxy pipeline also appends provider-normalized `llm.*` events, for example:

- `llm.output_text.delta`
- `llm.reasoning.delta`
- `llm.tool_call`
- `llm.tool_call.delta`
- `llm.usage`

These events are generated in `pkg/llm.ResponsePipeline`, recorded into the cassette, and surfaced by the monitor detail API.

## Token Usage Normalization

The monitor, cassette prelude, and SQLite index all use a shared usage shape:

- `prompt_tokens`
- `completion_tokens`
- `total_tokens`
- `prompt_tokens_details.cached_tokens`

This shape is normalized from provider-specific response payloads inside `pkg/llm`.

### OpenAI-compatible chat completions

OpenAI-style usage is recorded directly:

- `prompt_tokens = usage.prompt_tokens`
- `completion_tokens = usage.completion_tokens`
- `total_tokens = usage.total_tokens`
- `prompt_tokens_details.cached_tokens = usage.prompt_tokens_details.cached_tokens`

Example:

```json
{
  "usage": {
    "prompt_tokens": 14851,
    "completion_tokens": 67,
    "total_tokens": 14918,
    "prompt_tokens_details": {
      "cached_tokens": 14656
    }
  }
}
```

### Anthropic / Claude messages

Claude reports prompt cache usage separately from `input_tokens`, so the proxy folds cache-related fields back into the shared prompt view:

- `prompt_tokens = input_tokens + cache_creation_input_tokens + cache_read_input_tokens`
- `completion_tokens = output_tokens`
- `total_tokens = prompt_tokens + completion_tokens` when Anthropic does not provide `total_tokens`
- `prompt_tokens_details.cached_tokens = cache_read_input_tokens`

Notes:

- `cache_read_input_tokens` means prompt tokens served from cache, which is the closest equivalent to OpenAI's `cached_tokens`
- `cache_creation_input_tokens` is included in `prompt_tokens` so the recorded prompt total reflects the full prompt-side token cost/volume for that request
- SQLite currently indexes only one cache field: `cached_tokens`, which stores cache hits (`cache_read_input_tokens`) for Claude and `prompt_tokens_details.cached_tokens` for OpenAI-style payloads

Example:

```json
{
  "usage": {
    "input_tokens": 17430,
    "output_tokens": 194,
    "cache_read_input_tokens": 18560
  }
}
```

This is recorded as:

```json
{
  "prompt_tokens": 35990,
  "completion_tokens": 194,
  "total_tokens": 36184,
  "prompt_tokens_details": {
    "cached_tokens": 18560
  }
}
```

## Key Packages

- `internal/proxy`: reverse proxy, response interception, and cassette byte capture
- `internal/upstream`: upstream config resolution, auth/header application, and path/query rewriting
- `internal/recorder`: file writer and metadata finalization
- `internal/store`: SQLite schema, sync, and query layer
- `internal/monitor`: embedded React monitor and cassette detail projection
- `pkg/recordfile`: shared V2/V3 parsing and V3 prelude writer
- `pkg/llm`: provider adapters, stream transcript normalization, usage pipeline, and event timeline generation
- `pkg/replay`: HTTP response replay transport for tests

## Compatibility

- V3 is the active write format.
- V2 is still a supported read format for monitor and replay.
- `migrate` can explicitly rewrite V2 cassettes to V3 and rebuild SQLite from raw files.
