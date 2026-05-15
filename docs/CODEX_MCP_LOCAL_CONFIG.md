# Codex MCP Local Config

This repository keeps Codex MCP client configuration local to the workspace.

The local config file is:

```text
.codex/config.toml
```

`.codex` is ignored by git, so endpoint overrides and local client settings are not committed.

Current local example:

```toml
[mcp_servers.tracelab-remote]
url = "http://ip:port/mcp"
bearer_token_env_var = "LLM_TRACELAB_MCP_TOKEN"
```

Do not store tokens in this file. If the remote deployment requires auth, export the token in the shell before starting Codex:

```bash
export LLM_TRACELAB_MCP_TOKEN='...'
```

If the deployment does not require auth, leave `LLM_TRACELAB_MCP_TOKEN` unset.

To inspect the repository-local MCP config without touching global Codex config:

```bash
CODEX_HOME="$PWD/.codex" codex mcp list
CODEX_HOME="$PWD/.codex" codex mcp get tracelab-remote
```

To update the remote endpoint locally:

```bash
CODEX_HOME="$PWD/.codex" codex mcp remove tracelab-remote
CODEX_HOME="$PWD/.codex" codex mcp add tracelab-remote \
  --url http://HOST:PORT/mcp \
  --bearer-token-env-var LLM_TRACELAB_MCP_TOKEN
```
