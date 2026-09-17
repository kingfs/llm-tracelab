# ATIF 会话导出

此包从客户端可见 Responses HTTP exchange 重建 ATIF-v1.7。结构依据 Harbor 的 Pydantic 模型：

https://github.com/laude-institute/harbor/tree/74cc6312018c349c6bd2400c89a0ac4983ac1085/src/harbor/models/trajectories

`schema_version` 固定为 ATIF-v1.7。一个 JSONL 行包含一个完整 trajectory；step_id 从 1 连续递增；工具结果的 source_call_id 引用同一步中的 tool_call_id。项目扩展只放在 ATIF 的 extra 字段中。agent 身份/版本无法从证据确定时使用 unknown。

导出不推断任务成功、用户纠正意图或子 agent 树。相同请求历史的合并是有序对齐，不是文本全局去重；每次 HTTP 请求仍在 extra.exchanges 中留有记录。完整原始证据仍是 cassette。

回归测试完全离线，使用 `testdata/atif.schema.json` 验证导出结构。该 schema 由上述固定提交的 `Trajectory.model_json_schema()` 生成，仅将 schema_version 收窄为 ATIF-v1.7；上游模型支持多个版本，字段的版本语义仍以 v1.7 为准。上游许可证保存在 `testdata/HARBOR-LICENSE`。Go 测试另行验证连续 step_id 与同一步工具结果引用（JSON Schema 无法表达这些约束）。

`ATIF_TEST_OUTPUT_DIR` 可选环境变量让指定测试将样例 JSONL 输出到既有目录，供上游 Harbor 模型校验：

```sh
ATIF_TEST_OUTPUT_DIR=/tmp go test ./internal/trajectory -run TestBuildCumulativeHistoryAndHumanCorrection -count=1
```
