# Codex MCP 本地配置

本仓库把 Codex MCP client 配置保存在工作区本地。

本地配置文件：

```text
.codex/config.toml
```

`.codex/config.toml` 由 git 跟踪，因此不要把 token 或敏感 endpoint 写入该文件。

## 示例

```toml
[mcp_servers.tracelab-remote]
url = "http://ip:port/mcp"
bearer_token_env_var = "LLM_TRACELAB_MCP_TOKEN"
```

不要把 token 直接写入该文件。

MCP endpoint 始终要求有效的 `Authorization: Bearer <token>`，缺失或无效 token 的请求返回 401。
启动 Codex 前导出 token：

```bash
export LLM_TRACELAB_MCP_TOKEN='...'
```

认证要求见 [MCP 使用指南](./MCP_GUIDE.md)。

## 查看本地配置

只检查仓库本地 MCP 配置，不修改全局 Codex 配置：

```bash
CODEX_HOME="$PWD/.codex" codex mcp list
CODEX_HOME="$PWD/.codex" codex mcp get tracelab-remote
```

## 更新远端 endpoint

```bash
CODEX_HOME="$PWD/.codex" codex mcp remove tracelab-remote
CODEX_HOME="$PWD/.codex" codex mcp add tracelab-remote \
  --url http://HOST:PORT/mcp \
  --bearer-token-env-var LLM_TRACELAB_MCP_TOKEN
```

## 相关文档

- [MCP 使用指南](./MCP_GUIDE.md)
- [当前实现概览](./CURRENT_IMPLEMENTATION.md)
