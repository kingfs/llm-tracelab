# Codex Responses fixtures

These fixtures document the first offline Codex/OpenAI SDK compatibility
profile for llm-tracelab Responses server-mode.

They are wired into focused offline Go tests under `internal/responses/httpapi`
and `internal/responses/runtime`. The tests validate fixture schema/contract
shape and a small runtime/parser alignment surface without depending on real
Codex, real model providers, wall clock time, or network access.

Coverage:

- `text_create_request.json` and `text_create_expected_response.json`: basic
  non-streaming Responses create.
- `stream_text_request.json` and `stream_text_events.ndjson`: minimal typed SSE
  text event sequence.
- `function_call_request.json` and `function_call_expected_response.json`:
  client-owned function tool call emission.
- `function_call_output_continuation_request.json`: client tool result
  continuation using `previous_response_id`.
- `ordinary_web_search_descriptor_request.json`: ordinary hosted web search
  descriptor accepted as compatibility input.
- `unsupported_hosted_tool_expected_error.json`: expected stable error shape for
  unsupported hosted tools such as `mcp`, `file_search`, and
  `code_interpreter` once runtime gating is added. The current fixture tests
  treat this as a pending compatibility contract and do not assert that runtime
  gating for those hosted tools is already implemented.
