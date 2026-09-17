# ATIF 会话导出

此包从客户端可见 Responses HTTP exchange 重建 **ATIF-v1.8**。一个 JSONL 行是一个完整会话，step_id 从 1 连续递增，工具结果引用同一步中的 tool_call_id。

每个已记录响应形成一个 agent 步骤，合并 SSE 文本、reasoning 与工具调用；终止事件的空或部分 output 不覆盖先前已接收内容。下一次请求携带的工具结果回挂到调用步骤。累计请求历史按有序重叠合并，忽略传输元数据差异，不全局去重用户文本。仅从历史恢复的 agent 内容标记 history_recovered，不推测调用次数或 usage。

usage 仅提取标准计数，每个响应计入一次 metrics，final_metrics 汇总；不导出逐 item attribution、重复 native_item、完整请求配置或加密 reasoning。可读 reasoning summary 在 extra.reasoning_summary，实际可读 reasoning 在 reasoning_content。每步保留 trace_id 等证据引用，完整原始内容仍在 cassette。多模态内容只保留文本与占位引用，不生成附件。未知类型与缺失数据报告 warnings；不推断任务成功或子 agent 树。

## 标准校验

scripts/validate_atif.py 调用固定版本的 Harbor 官方 Pydantic 模型（未经修改），额外要求显式 ATIF-v1.8。来源、提交与逐文件 SHA256 在 scripts/atif_vendor/upstream.json，许可证在同目录 LICENSE。JSON Schema 不能覆盖所有跨字段语义，官方模型验证作为补充；格式通过也不代表录制数据完整。

```sh
python3 -m venv .venv-atif
.venv-atif/bin/python -m pip install -r scripts/atif-requirements.txt
.venv-atif/bin/python scripts/validate_atif.py /tmp/session.jsonl
.venv-atif/bin/python -m unittest discover -s scripts/atif_tests
.venv-atif/bin/python scripts/validate_atif.py --write-schema internal/trajectory/testdata/atif.schema.json
```

依赖安装后，验证完全离线。激活虚拟环境后也可运行 task atif:validate -- 文件.jsonl 或 task test:atif。CI 使用官方模型校验 Go 生成的样例。

## 回归数据

常规 Go 测试使用合成数据与固定 JSON Schema，不访问网络：

```sh
ATIF_TEST_OUTPUT_DIR=/tmp go test ./internal/trajectory -run TestBuildCumulativeHistoryAndHumanCorrection -count=1
```

可选真实数据测试接受 JSON 数组清单，每项包含 path（本机 .http 绝对路径）与 trace_id；清单内请求须来自同一 Session-Id。默认跳过，不将私人 cassette 纳入仓库：

```sh
ATIF_CASSETTE_MANIFEST=/tmp/manifest.json ATIF_SESSION_OUTPUT=/tmp/session.jsonl go test ./internal/trajectory -run TestRecordedSessionATIF -v -count=1
```
