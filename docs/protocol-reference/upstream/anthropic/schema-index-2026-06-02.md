# Anthropic Messages Snapshot 2026-06-02

## Official Source

- Messages API reference: https://platform.claude.com/docs/en/api/messages
- Streaming Messages guide: https://docs.anthropic.com/en/docs/build-with-claude/streaming
- Tool use guide: https://docs.anthropic.com/en/docs/agents-and-tools/tool-use/overview

## Files

- [`messages-api-2026-06-02.html`](./messages-api-2026-06-02.html): raw Messages API reference HTML snapshot.

## TraceLab Coverage

Current TraceLab parser coverage targets:

- `/v1/messages` request bodies
- non-streaming Messages responses
- streaming Messages events
- content blocks including text, thinking/redacted thinking, tool use, and tool results
- Anthropic usage fields including cache read/create token accounting

The proxy forwards Anthropic Messages traffic as Anthropic Messages traffic. It does not translate Messages requests into OpenAI-compatible requests.
