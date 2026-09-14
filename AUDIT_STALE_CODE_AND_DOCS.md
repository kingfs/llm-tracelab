# 过期代码 / 死代码 / 失准文档 审计报告

- **仓库**：`github.com/kingfs/llm-tracelab`
- **审计基线**：`2ef783b`（工作区未做任何修改）
- **范围**：`cmd/`、`internal/`、`pkg/`（175 个非生成 `.go` 文件）、`web/monitor-ui/`、`docs/**`、`README*.md`、`AGENTS.md`、`config/**`、`Taskfile.yml`、`docker-compose*.yml`
- **方法**：`go build`/`go vet` + `golangci-lint`(`staticcheck`/`unused`) + 全仓库符号交叉引用 + 文档声明逐条回溯源码 + 6 条独立只读调查线交叉验证

---

## 0. 总体结论

代码库在“传统死代码”维度上是**干净的**：编译通过、`go vet` 干净、`golangci-lint`（含 `staticcheck` + `unused`）**0 issue**，不存在未被引用的非导出符号、未使用局部变量或成片注释代码。

真正的问题集中在三个层面：

| 层面 | 问题性质 | 数量级 |
|---|---|---|
| **失效配置** | 文档/示例中仍在宣传、但代码完全不读取的配置键 | `startup_policy`（10 处）、`responses_server.enabled`（5 处）、`--no-input` |
| **未接线功能** | 已实现存储 + 库，但没有任何 CLI/HTTP/MCP/UI 入口，只有测试可达 | `internal/evals`（442 行）、dataset/eval/experiment（4 张 ent 表 + 13 个 store 方法）、`ListCompactProvenance` |
| **失准文档** | 文档把“路线图/设计稿/已移除开关”当作当前事实，且 `docs/README.md` 把它们归类为“当前事实文档” | 结构性根因，牵出 7 类事实错误 |

**根因判断**：文档层面的主要根因是 `docs/README.md:17-40` 把 11 篇 Roadmap / Gap-Analysis / Design-Draft 文档列入“**当前事实文档**”，而它们自己都在开头声明“不是当前实现事实清单”。这直接导致已移除的开关、未落地的能力被持续当作事实引用。代码层面的主要根因是“**加了替代实现但没有删除旧包装**”和“**功能先建存储层、后置产品入口且入口从未落地**”。

---

## 1. 高优先级问题（建议先处理）

### H1. `responses_server.enabled` 已被彻底移除，但 5 处配置示例仍在设置它（静默 no-op）

`responses_server.enabled` 字段与 `LLM_TRACELAB_RESPONSES_ENABLED` 环境变量已在 `9401a95` 中彻底删除。`ResponsesServerConfig`（`internal/config/config.go:159-170`）没有该字段，且加载使用非严格 `yaml.Unmarshal`（`config.go:319`），因此该键**被静默忽略**。

| 位置 | 内容 |
|---|---|
| `README.md:120` | `responses_server:` → `enabled: true` |
| `README_EN.md:116` | 同上 |
| `docs/CURRENT_IMPLEMENTATION.md:52` | 同上（**同文件 `:34` 明说该字段已彻底移除**，自相矛盾） |
| `docs/CODEX_RESPONSES_COMPATIBILITY.md:143` | 同上 |
| `docs/HOSTED_TOOLS_ROADMAP.md:577` | 同上 |

另有 3 处文字性错误声明：

- `docs/RESPONSES_SERVER_DESIGN.md:15` —— 称 `enabled`（已废弃，**仅保留兼容解析**）仍是字段之一 → 实际既不解析也不兼容。
- `docs/RESPONSES_SERVER_DESIGN.md:9` —— 称 `config/config.yaml` 的 `responses_server` 块包含（已废弃的）`enabled` → `config/config.yaml:29-52` 完全没有该键。
- `docs/RESPONSES_SERVER_DESIGN.md:357` —— 称装配**默认 `enabled: false`** → 不存在任何开关。

**修复**：删除 5 处示例中的 `enabled: true`；修正上述 3 句。正确的退出方式应写成 Monitor 的 Routing 设置（见 H4）。

---

### H2. `router.model_discovery.startup_policy` 是纯 no-op 配置，却出现在 10 处文档/示例中

`RouterConfig.StartupPolicy`（`internal/config/config.go:138`）在整个非测试 Go 代码中**只有声明、没有任何读取点**（`grep -rn "StartupPolicy|startup_policy"` 返回 1 命中）。

但它仍被写入：
`README.md:132`、`README_EN.md:128`、`config/config.yaml:72`、`config/config.dev.yaml:28`、`config/examples/{local-sqlite,openai,anthropic,azure_openai,google_genai,openai-compatible-vllm-postgres,vertex}.yaml`（6 个示例）。

归档文档 `docs/archive/completed-plans/MULTI_UPSTREAM_PLAN.md:102` 还对它做了 `strict | best_effort | lazy` 的语义说明——这些语义**从未实现**。

**修复**：二选一——(a) 从 README/config/examples 移除该键，并从结构体删除字段；(b) 真正实现 `startup_policy` 语义。当前状态是最坏的：用户以为配了 `strict`，实际行为不变。

---

### H3. 整块“eval / dataset / experiment”功能已建存储、未接任何入口

这是仓库中最大的一块“已实现但不可达”代码：

- **`internal/evals` 包（`evals.go`，442 行）零 importer**，唯一消费者是它自己的 `evals_test.go`。包内仍保留 `baseline_v1/v2/v3` 等旧 profile（`evals.go:13` 的 `BaselineEvaluatorSet` 为 `baseline_v4`）。
- **4 张 ent schema**：`dataset`、`dataset_example`、`eval_run`、`experiment_run`（`ent/schema/`）。
- **13 个 store 方法零生产调用者**（仅测试）：`CreateDataset`、`ListDatasets`、`AppendDatasetExamples`、`GetDatasetExamples`、`CreateEvalRun`、`FinalizeEvalRun`、`AddScore`、`GetEvalRun`、`ListEvalRuns`、`ListScores`、`CreateExperimentRun`、`GetExperimentRun`、`ListExperimentRuns`。
- **无任何产品入口**：`internal/monitor`、`cmd/server`、`internal/mcpserver`、`internal/reanalysis` 中 **零** `Eval|Dataset|Experiment` 引用（已用 grep 确认）。
- 同时存在 SQLite raw DDL（`internal/store/store.go:3676-3734, 4276-4291`）与 Postgres migration。

**根因**：`docs/archive/design-notes/AGENT_EVOLUTION_ROADMAP.md:392` 的 M3“scores/evals/experiments”里程碑只落地了存储与评估库，产品面从未接入。

**文档冲突**：`docs/CURRENT_IMPLEMENTATION.md:112,149,120` 与 `docs/PROJECT_BASELINE.md:56,69`、`README.md:164` 都把 eval 结果/表描述为“当前已实现”，而 `docs/v1/status.md:108` 把“更丰富的 eval profile”列为**未来演进**。两者不能同时为真。

**修复**：若功能仍在计划中，明确标注为“存储已就绪、入口未接线”；若已取消，整块删除（包 + 4 ent schema + store 方法 + DDL/migration + 测试）。

---

### H4. `routing.settings.responses_strategy` 被当作 YAML 配置项，实际只存在于应用库 `app_settings`

`AGENTS.md:29`、`docs/CURRENT_IMPLEMENTATION.md:34`、`docs/RESPONSES_SERVER_DESIGN.md:118`、`config/config.yaml:27`、`config/examples/local-sqlite.yaml:25`、`config/examples/openai-compatible-vllm-postgres.yaml:25` 都以 `routing.settings.responses_strategy=native_only` 的写法指示用户“设置它”。

**实际机制**：
- 配置结构体中**没有 `routing` 段**（`grep '"routing"' internal/config/config.go` 无命中；只有 `router`）。
- 该值存放在应用库 `app_settings` 表的 `routing.settings` 键（JSON），常量见 `internal/proxy/handler.go:122`、`internal/monitor/server.go:3049`。
- 读取发生在每次请求：`handler.go:1114-1129`。
- 唯一写入路径：`PATCH /api/settings/routing`（`internal/monitor/server.go:1510`，处理函数 `:3049-3090`），即 Monitor 的 Routing → Settings 面板。

**影响**：遵循 `AGENTS.md` 的 AI agent 或运维人员会往 `config.yaml` 里加 `routing:` 段，得到静默 no-op，且**无法通过 YAML/环境变量关闭本地 Responses 翻译**。

**修复**：把措辞改为“在 Monitor 的 Routing 设置（`PATCH /api/settings/routing`）中设置”，并同步修正 3 处 YAML 注释；如确需声明式配置，应补一条 YAML→DB 引导。

---

### H5. `docs/PRODUCTION_DEPLOYMENT.md` 的 `docker compose run` 命令必然失败

`Dockerfile:50` 设置了 `ENTRYPOINT ["/app/bin/llm-tracelab"]`，而 `docker compose run` **只覆盖 CMD、不覆盖 ENTRYPOINT**。因此文档中的：

```bash
docker compose run --rm llm-tracelab /app/bin/llm-tracelab -c /app/config/config.yaml db migrate up
```

实际 argv 变成 `/app/bin/llm-tracelab /app/bin/llm-tracelab …`。已用仓库内二进制复现：

```
$ ./llm-tracelab /app/bin/llm-tracelab -c /tmp/x.yaml db migrate up
unknown command "/app/bin/llm-tracelab" for "llm-tracelab"
```

受影响位置：`docs/PRODUCTION_DEPLOYMENT.md:57`（以及 `:105`、`:106`、`:107` 同模式）。只有 `:59` 的 `docker compose exec` 写法正确。

**修复**：去掉重复的二进制路径，改为 `docker compose run --rm llm-tracelab db migrate up`。

---

## 2. 代码层发现

### 2.1 零引用导出符号（18 个 + 约 20 个常量）——建议删除

| 符号 | 位置 | 说明 |
|---|---|---|
| `HasLocalResponsesServerBackend` | `internal/router/router.go:559` | 已被 `SupportsLocalResponsesServerBackend`(`:604`) 取代 |
| `AuthDatabasePath` | `internal/config/config.go:787` | 与在用的 `Config.DatabasePath()`(`:815`) 重复 |
| `ResponsesCodexCompatEnabled` | `internal/config/config.go:1008` | 调用方直接用 `ResponsesCodexCompatConfig().Enabled` |
| `DefaultDatabasePath` | `internal/auth/store.go:57` | 返回 `control.sqlite3`，与在用的 `llm_tracelab.sqlite3` 不一致 |
| `WriteMetaFile` | `internal/recorder/recorder.go:284` | **函数体就是 `return nil`** 的空实现 |
| `FunctionToolExecutorSnapshot` | `internal/responses/runtime/runtime.go:159` | 实际路径是非导出 `functionToolExecutorSnapshot()`(`:137`) |
| `(*Runtime).FunctionToolExecutorRegistry`（方法） | `runtime.go:150` | 同名**类型**在用，方法无用 |
| `SetChannelEnabled` | `internal/store/store.go:1013` | 渠道开关走 `UpsertChannelConfig` |
| `PathByID` | `internal/store/store.go:7596` | 零引用，且逻辑可疑：用 ent 主键 `OnlyID` 当作 cassette **路径**返回 |
| `ModelAliasPatch`（类型） | `internal/store/store.go:383` | 只剩 `ModelAliasRecord` + `SetModelAliasEnabled` 在用 |
| `DiscoverModels` | `internal/upstream/checker.go:32` | 已被 `DiscoverModelsResolved` 取代 |
| `CheckConnectivity` | `internal/upstream/checker.go:28` | 其唯一被调者 `checkConnectivity`(`:71`，带 `os.Stdout` 参数) 也随之沦为测试专用 |
| `AggregateChatCompletionStream` | `internal/responses/chatclient/stream.go:15` | 调用方全部走 `…WithCallback` |
| `FromGeminiRequest` | `pkg/llm/google.go:157` | 包装 `fromGenerateContentRequest`(`:161`)，后者才是 `adapter.go:276/305` 调的 |
| `ToGeminiResponse` | `pkg/llm/google.go:331` | 包装 `toGenerateContentResponse`(`:335`) |
| `(*Registry).Parsers` | `pkg/observe/parser.go:65` | 调用方用 `Select`/`Parse` |
| `ListProfiles` | `internal/evals/evals.go:97` | 随死包一起删 |
| `ConversationContinuationStore`（接口） | `internal/responses/runtime/store.go:19` | 实现存在但无消费者，已被 `Store.ContinuationItems` 取代 |

**死常量**（均为“仅声明”）：
`HeaderLen`(`recorder.go:25`)、`PhaseToolCall` + `StatusRequested/Started/Submitted/Failed/Rejected`(`tools/hosted/contract.go:26,32-37`；注意 `StatusCompleted:35` **在用**，运行时其余状态直接写裸字符串)、`ConnectivityPathVertexModels`(`upstream/resolved.go:50`)、`ParseStatusRecorded/Indexed/ParseQueued/ParseFailed/AnalysisQueued/Analyzed/AnalysisFailed` 与 `NodePatch/NodeAudio/NodeVideo`、`ToolOwnerInferred`(`pkg/observe/ir.go:14-21,36-50`；`ParseStatusParsed:17` 在用)。

> `pkg/llm`、`pkg/observe` 是对外包，删除前需确认外部消费者；建议按“API 收窄”而非“安全删除”流程处理。

### 2.2 仅测试可达 / 被取代的实现

- **`SupportsEndpoint`（`internal/upstream/resolved.go:583`）与在用的 `SupportsEndpointForModel`（`:566`）已经行为漂移**：旧版 `/v1/responses` 分支用 `u.APIType == APITypeChatCompletions`，新版用 `SupportsChatCompletionsAPIForModel(model)`。仅 `router_test.go:383` 调用旧版。**建议删除旧版，避免测试断言与生产语义不一致。**
- **`upstream.mode` 已退化为“只校验 + 只记日志”，不再影响任何路由**：`NativeResponsesServerMode`(`resolved.go:470`)、`ChatCompletionsServerMode`(`:475`)、`isServerMode`(`:770`) 仅被 `resolved_test.go` 调用；其余 `.Mode` 引用只有赋值、事件属性（`handler.go:1381/1467/1546`）和 `validateAPISurface` 白名单（`resolved.go:748`）。
  但 `docs/RESPONSES_SERVER_DESIGN.md:113` 仍称“`mode: responses_server`：由 TraceLab 接管 Responses 语义”，`:183` 仍把 `proxy/record_only/server/responses_server` 列为有效处理模式。**这是一处高影响的事实错误**——`72c26cf`（按模型解析 native-vs-local）之后，`mode` 已无路由作用。
- 其余测试专用入口：`PrepareLogFile`/`PrepareLogFileWithOptions`/`UpdateLogFile`（`recorder.go:73,77,227`）、`WithFunctionToolExecutor`（`runtime.go:96`）、`Counts`（`limit/limiter.go:104`）、`MigrateDown`（`auth/migrate.go:199`、`appdbmigrate/migrate.go:103`）、`boolToInt`（`store.go:8595`，建议移入测试文件）、`codexfixtures.{Names,CreateRequestNames,ValidateAll}`、`GeminiToLLM`、`ParseResponseForPath`/`ParseStreamResponseForPath`（`pkg/llm/adapter.go:104,112`）。
  其中 `newRootCommand`、`runServe`、`runMigrate`、`newManagementMux`、`RunOnce` 等属**有意保留的测试接缝**，不建议删除。

### 2.3 `ListCompactProvenance`：文档宣称的 read model，没有任何暴露面

`internal/responses/audit/query.go:388`（及 `ListCompactProvenanceParams:73`、`CompactProvenanceView`）仅被 `query_test.go:697` 调用，**没有 CLI / HTTP / MCP / UI 入口**。但 `docs/CURRENT_IMPLEMENTATION.md` 与 `docs/PROJECT_BASELINE.md` 都把它作为已落地的 compact provenance 查询能力对外宣称。属“实现先于入口、入口从未落地”。

### 2.4 未读取的结构体字段

- `RouterConfig.StartupPolicy`（`internal/config/config.go:138`）——见 H2，**影响最大**。
- `aggregatedModelArchitecture.Tokenizer`/`.InstructType`、`aggregatedModelTopProvider.IsModerated`（`internal/proxy/handler.go:75,76,82`）：仅解码 OpenRouter 响应，从不读取。
- `SessionSummaryRebuildStats.RebuiltAll`/`.RebuiltOne`（`internal/store/store.go:275,276`）：生产者只设置 `WouldDeleteAll`/`WouldDeleteOne`。
- `cliError.Suggestions`（`cmd/server/root.go:255`，JSON `suggested_commands`）：任何 `cliError` 字面量都未填充它。
- `ObservationSafety.Categories`/`.ProviderRaw`（`pkg/observe/ir.go:220,221`）。
- `LLMContent.ImageData`/`.AudioData`/`.VideoData`、`LLMUsage.AudioTokens`（`pkg/llm/llm.go:47-49,115`）、`AnthropicResponse.StopSequence`（`pkg/llm/anthropic.go:61`）。

### 2.5 死分支与 CLI 死标志

- **`internal/proxy/handler.go:1101`** 的 `default:` 分支不可达：switch 值来自 `h.responsesStrategy()`(`:1114`)，该函数只返回 5 个合法策略或回落到 `auto`(`:1127-1129`)。
- **`--no-input`**（`cmd/server/root.go:70`，绑定在 `:73`）在全仓库中**从未被读取**——声明的“非交互式保护”实际不存在。

### 2.6 真实行为缺陷（排查过程中顺带发现，非“过期”但优先级高）

1. **Monitor 缓存 Token 徽标恒为 0**：`web/monitor-ui/src/routes/TraceDetailPage.jsx:192` 读取 `usage?.prompt_token_details?.cached_tokens`，而 Go 侧 JSON tag 是 `prompt_tokens_details`（`pkg/recordfile/recordfile.go:29`）。键名少一个 `s`，前端永远取到 0。这是全仓 244 个前端字段读取与 940 个 Go JSON tag 对照后**唯一**的近似拼写不匹配。
2. **三态 capability 的“inherit”无法清除已设置值**：`ModelDetailPage.jsx:304,327-337` 对 “inherit” 发送 `null`，但后端 `channelModelPatchRequest` 用 `*bool`（`internal/monitor/server.go:1308-1310,3420-3422`），JSON `null` 与“键缺失”都解码为 `nil`，而 `internal/store/store.go:1502-1509` 对 `nil` 采取“保持原值”。结果是：`8a6cc65` 想修的 tri-state 问题在**写入路径上依然存在**——已固定的 true/false 无法通过 UI 恢复为“继承”。
3. **`limits.scope` 文档值 `token` 无实现**：`docs/CREDENTIAL_ROUTING_OPERATOR_GUIDE.md:144-150` 列出 `global, token, channel, route_target, credential`；代码实际处理 `global`+`header`（`internal/proxy/handler.go:2231-2233`）与 `channel`/`route_target`/`credential`（`:2255-2265`）。`token` 落入 default → **限流被静默忽略**；同时真正实现的 `header` 反而未被文档记载。
4. `docs/CREDENTIAL_ROUTING_OPERATOR_GUIDE.md:47` 的 `model_discovery: static` 是非法值：合法值为 `list_models|static_only|disabled`（`internal/router/router.go:27-29`），非法值被 `normalizeDiscoveryMode`(`:2008-2018`) 静默改写为 `list_models`，示例意图（仅静态模型）不会生效。

### 2.7 前端死代码

- **整个组件 `UpstreamOverview`（`web/monitor-ui/src/components/routing/UpstreamOverview.jsx`，233 行）零引用**，只被自身定义命中；已被 tree-shaking 剔除出构建产物。
- **约 46 条 CSS 规则无消费者**，包括：随该组件一起死亡的 16 条 `upstream-*`/`routing-failure-*` 规则；`channels→providers` 改名残留的 `channel-form*`、`channel-model-card*`、`channel-probe-*`、`channel-grid`；已移除的 `provider-batch-probe-panel`、`usage-bar*`，以及 `action-button`、`error-box`、`meta-list` 等。它们**仍被打进 78KB 的 CSS 产物**。
- **14 个 `apiPaths` 条目零调用**（`src/lib/api.js`）：其中 `channels`/`channel`/`channelProbe`/`channelModels`/`channelModelsBatch` 与 `provider*` 是**完全相同的 URL 重复别名**；`localSecretKey`/`localSecretKeyExport`/`localSecretKeyRotate` 对应的 UI 面板已在 `1c26f8c` 移除，但键仍留在 `api.js:62-64`。
- **Playwright 测试断言的是已删除的 UI**：`web/monitor-ui/tests/monitor-smoke.spec.js:68-76,164-166,499` 仍 mock 并断言 `/api/secrets/local-key` 与 “Rotate key”；`tests-real/monitor-real-server.spec.js:28-38` 同；两处还在用 “Edit channel”/“Probe channel”，而当前 UI 是 “Edit provider”/“Probe provider”。**这些测试要么已失效、要么在 CI 中被跳过**——建议核实其执行状态。
- 其余低优先：`listTotal`(`api.js:151`)、`traceTools.js` 4 个仅内部使用的过度导出、`hooks/useJSON.js:4` 的死 re-export、`ChannelDetailPage.jsx:617` 的死函数 `setEditValue`、`ProvidersPage`/`ProviderDetailPage` 冗余导出别名、5 个未使用 i18n key、`normalizeTraceTab`(`lib/monitor.js:241-243`) 的 `summary`/`tools` 死分支。

### 2.8 后端已注册但无前端调用者的路由

`POST /api/router/reload`（`internal/monitor/server.go:1533`）**在 src、mcpserver、CLI、甚至 Go 测试中均无调用者**，唯一提及是归档设计稿——最可能真正废弃。

其余无前端调用者但**有 Go 测试覆盖，疑似有意保留的公开 API**：`/api/secrets/local-key[?export|?rotate]`、`/api/analysis/jobs/{id}`(+cancel)、`/api/sessions/{id}/analysis`、`/api/traces/{id}/reparse|scan`、`DELETE /api/model-aliases/{id}`；以及**有意供 MCP/CLI 使用**的 `/api/responses/audit/tool-calls`、`/api/upstreams`。建议逐个标注“公开 API / 待删”，不要一律当死代码。

---

## 3. 文档层发现

### D1. 结构性根因：`docs/README.md` 把 11 篇路线图/设计稿列为“当前事实文档”

`docs/README.md:17-40` 的“当前事实文档”清单包含：`RESPONSES_SERVER_DESIGN.md`、`GATEWAY_ROUTING_UI_DESIGN.md`、`ENTRY_MODEL_EXCHANGE_RECORDING_PLAN.md`、`EXCHANGE_RECORDING_MODEL_DESIGN.md`、`RESPONSES_ENTRY_RECORDING_DESIGN.md`、`EXCHANGE_QUERY_REPLAY_DESIGN.md`、`HOSTED_TOOLS_ROADMAP.md`、`FINAL_COMPLETION_AUDIT.md`、`RESPONSES_GATEWAY_COMPLETION_PLAN.md`、`RESPONSES_GATEWAY_ROADMAP.md`、`RESPONSES_GATEWAY_GAP_ANALYSIS.md`。

但其中多篇自己就声明不是事实源：

- `HOSTED_TOOLS_ROADMAP.md:3-7`：“分阶段实现路线图……**它不是当前实现事实清单**”
- `GATEWAY_ROUTING_UI_DESIGN.md:3-6`：“目标设计与实施拆分……不以当前过渡实现为约束”
- `ENTRY_MODEL_EXCHANGE_RECORDING_PLAN.md:3`：“实施蓝图”
- `EXCHANGE_RECORDING_MODEL_DESIGN.md:3` / `EXCHANGE_QUERY_REPLAY_DESIGN.md:3`：“design draft”
- `RESPONSES_GATEWAY_COMPLETION_PLAN.md:3`：“最终态收敛总设计”
- `FINAL_COMPLETION_AUDIT.md`：有明确日期戳（2026-06-23）的一次性审计快照

**修复**：在 `docs/README.md` 中拆出独立的“设计与路线图（非事实源）”区块，把上述文档移入；只把 `CURRENT_IMPLEMENTATION.md`、`PROJECT_BASELINE.md`、`ARCHITECTURE.md`、`MAINTAINER_BASELINE.md`、`DEVELOPMENT_COMMANDS.md`、协议参考、运维指南留在“当前事实”。这一条修好后，H1、D3、D5 这类问题会自然收敛。

另：`docs/README.md:15` 说 `RESPONSES_SERVER_DESIGN.md` 记录“截至 2026-06-23”的状态，而该文件头部已是“更新：2026-09-14”。

### D2. `AGENTS.md` 的架构描述已落后于 Postgres-first 现实

| 位置 | 声明 | 实际 |
|---|---|---|
| `AGENTS.md:21` | “Metadata index: `internal/store` using **SQLite** at `{{output_dir}}/trace_index.sqlite3`” | Postgres 是生产结构化状态主路径；真实 SQLite fallback 文件是 `llm_tracelab.sqlite3`（`internal/config/config.go:824`）。`trace_index.sqlite3` 仅存活于测试辅助 `store.New`(`store.go:2831`)，其唯一调用者是 UI 的 real-server fixture。`MAINTAINER_BASELINE.md:70` 自己称它是“旧本地文件” |
| `AGENTS.md:54` | “SQLite is the source for monitor list/statistics” | 与 `CURRENT_IMPLEMENTATION.md:112` 冲突 |
| `AGENTS.md:76` | “Prefer indexing metadata in SQLite rather than rescanning every file” | 同上 |
| `AGENTS.md:17` | CLI 命令文件只列 6 个 | 实际还有 `analyze.go`、`audit.go`、`config.go`、`doctor.go`、`models.go`、`provider.go`、`provider_startup_probe.go`、`schema.go`、`tools.go`（均在 `root.go:74-91` 接线），agent 按此清单找不到 provider/audit/analyze 的接线 |

`AGENTS.md` 是给 AI agent 的项目地图，出错影响面最大，建议优先修。

### D3. 其他“已移除开关”残留（同一类，H1 之外）

- `docs/PROJECT_BASELINE.md:35`：“Responses server-mode **默认关闭；关闭时** `/v1/responses` 仍按普通 OpenAI-compatible endpoint 代理透传”；`:16`：“**可选** Responses server-mode”。实际没有开关，本地/原生按模型二选一。
- `docs/CURRENT_IMPLEMENTATION.md:38`：“**开启 server-mode 后**，当前已支持非流式 Responses 请求经本地 runtime 映射……”——同文件 `:34` 已说开关被移除。
- `docs/CURRENT_IMPLEMENTATION.md:42`：称 `responses_server.model_catalog_drift` “Responses server 关闭时 pass”、`http_guard` “关闭时 pass 并标注 skipped reason”。实际两者**无条件执行**，`checkDoctorResponsesHTTPGuard`(`cmd/server/doctor.go:301-361`) 无任何 skip 分支；`model_catalog_drift`(`:777-817`) 只在 `default_model` 为空时跳过。
- `docs/RESPONSES_GATEWAY_GAP_ANALYSIS.md:99,148`：重复同样的“关闭时 pass/skipped_reason”描述。
- `docs/FINAL_COMPLETION_AUDIT.md:16`：“Local server-mode does not pass `/v1/responses` through **when enabled**”。
- `docs/GATEWAY_ROUTING_UI_DESIGN.md:308`：“从‘全局开关进入本地 handler’收敛”。

另：`LLM_TRACELAB_RESPONSES_ENABLED` 在代码中**只**出现在一个断言它被忽略的测试里（`internal/config/config_test.go:935-937`），代码侧清理是干净的。

### D4. 能力声明与代码冲突

1. **MCP hosted tool executor：6+ 篇文档说“未实现”，实际已实现并接线。**
   `internal/responses/tools/mcp/executor.go`（695 行）存在，`executeMCPToolCall`（`internal/responses/runtime/runtime.go:1400,1580`）已接入，并在 `internal/proxy/handler.go:35,517` 装配。但：
   - `docs/CODEX_RESPONSES_COMPATIBILITY.md:131`：“当前 MCP 是对外排障 server，**不是 Responses runtime 内部 tool executor**”——已为假。
   - 同族 stale 声明：`CODEX_RESPONSES_COMPATIBILITY.md:102-103`；`RESPONSES_GATEWAY_GAP_ANALYSIS.md:125,167,187`；`RESPONSES_GATEWAY_ROADMAP.md:138,144`；`FINAL_COMPLETION_AUDIT.md:49`；`RESPONSES_GATEWAY_COMPLETION_PLAN.md:20,89`。
   （真正仍未实现的只剩 `file_search` / `code_interpreter` / `computer_use_preview`。）
2. **`upstream.mode` 语义**：见 2.2，`RESPONSES_SERVER_DESIGN.md:113,169,183` 仍按“有路由语义”描述。
3. **`docs/POSTGRES_OPERATIONS_RUNBOOK.md:250-271`** 把 `session_summaries` 当作“新增派生 read model”提议，并列出 `first_recorded_at`、`trace_count`、`prompt_tokens` 等列——该表**早已上线且列名完全不同**（`session_id, session_source, request_count, first_seen, last_seen, last_model, providers, success_request, failed_request, success_rate, total_tokens, avg_ttft, total_duration, stream_count, updated_at`，见 `ent/postgres-migrations/20260703090000_add_session_summaries.up.sql`）。文档也从未提及已有的 `db summary rebuild sessions` 命令。
4. **`ListCompactProvenance`** 被当作已交付 read model 宣称（见 2.3）。
5. **模型 profile 字段不全**：`PROJECT_BASELINE.md:38` / `CURRENT_IMPLEMENTATION.md:88` 未列出 `tool_output_token_limit`、`model_reasoning_effort`（存在并用于 `cmd/server/models.go:296-297`）。

### D5. 会执行失败或误导的运维指令

| 位置 | 问题 |
|---|---|
| `docs/PRODUCTION_DEPLOYMENT.md:57,105-107` | `docker compose run` 重复二进制，必然失败（见 H5） |
| `docs/MONITOR_GUIDE.md:16`、`docs/PROXY_USAGE_EXAMPLES.md:8`、`docs/MCP_GUIDE.md:32` | 本地 quick-start 用 `-c config/config.yaml`，但该配置是 `driver: postgres` + `dsn: ""`，命令会报 “postgres … dsn is required”（`internal/auth/store.go:117`、`internal/appdbmigrate/migrate.go:125`）。缺少 `LLM_TRACELAB_DATABASE_DSN` 前置条件说明，或应指向 `config/examples/local-sqlite.yaml` |
| `docs/POSTGRES_STORAGE_MIGRATION.md:127-132` | 称 compose 会在 `serve` 前跑 `db migrate up`（实际只有 `serve`，靠 `auto_migrate`）；称 `config/config.yaml` 有 `dsn: $env:…`、`responses_server` 块和 vLLM upstream（实际 `dsn: ""`、无 upstream）。它描述的是 `config/examples/openai-compatible-vllm-postgres.yaml` |
| `docs/CREDENTIAL_ROUTING_OPERATOR_GUIDE.md:47` | `model_discovery: static` 非法（见 2.6.4） |
| `docs/INTEGRATION_TEST_RUNBOOK.md:72-73` | 断言 `tools.web_search.readiness.status` / `tools.mcp.readiness.status`，但 `readiness` 是字符串字段（`cmd/server/tools.go:59,67`），不存在 `.status` |
| `docs/MONITOR_GUIDE.md:33` | 仍称 “SQLite：列表、过滤、分页、聚合……” 为 Monitor 数据源，与同文件 `:104` 和 Postgres-first 现实冲突 |
| `docs/MONITOR_GUIDE.md:86,99` | 页面名 “Channels”，实际 UI 路由/标签是 Providers（`App.jsx:122-125`，`/channels` 已重定向到 `/providers`）；已上线的 `Connect` 页面从未被文档列出 |
| `docs/CODEX_MCP_LOCAL_CONFIG.md:29,11` | 称“远端部署不需要认证时可不设 `LLM_TRACELAB_MCP_TOKEN`”，但 MCP handler 始终包在 `auth.Middleware` 中（`cmd/server/management.go:51-63`），无 token 返回 401；且称 `.codex` 已被 git 忽略——实际 `.codex/config.toml` **被 git 跟踪** |

### D6. 失效链接与陈旧引用文件

- **`README.md:59,61,62`** 三个链接指向 `docs/GATEWAY_REFERENCE_EVOLUTION_DESIGN.md`、`docs/AGENT_EVOLUTION_ROADMAP.md`、`docs/AI_BRANCH_BASELINE.md`——文件都已移到 `docs/archive/{design-notes,branch-notes}/`。README 是入口，链接失效影响首屏体验。
- `docs/v1/reference-materials/upstream/*/schema-index-2026-05-13.md` 中的链接写成 `/docs/sources/upstream/...`（一个**不存在的目录树**），而目标文件其实是同目录的兄弟文件。已用脚本扫描 80 个 md 文件，共 30 个失效相对链接，其中 27 个属这两类。
- `docs/v1/reference-materials/` 是 **4.3MB 的 2026-05-13 快照**，已被 `docs/protocol-reference/upstream/*-2026-06-02.*`（5.8MB）取代。AGENTS.md 已正确标注其为历史材料，但若不做归档/删除，它会持续占用仓库体积并制造“哪个是权威”的歧义。其 `README.md` 末尾“parser 实现**优先**以这些原始材料为依据”与开头“当前参考已迁至 protocol-reference”自相矛盾。

### D7. 环境/内部信息泄漏进文档与仓库

- **`rtk` 包装器**：`docs/FINAL_COMPLETION_AUDIT.md:30-36`、`docs/RESPONSES_GATEWAY_COMPLETION_PLAN.md:218-222`、`docs/RESPONSES_SERVER_DESIGN.md:287`、`docs/RESPONSES_GATEWAY_ROADMAP.md:64` 的验证命令写作 `rtk env -u GOROOT task check:quick`、`rtk git diff --check`。`rtk` 不在仓库中，是某个开发/agent 环境的私有包装器，普通贡献者无法复现。
- **`.codex/config.toml` 被 git 跟踪，且含真实内网地址**：内容为 `url = "http://10.2.68.223:38081/mcp"`（token 走环境变量，未泄漏密钥）。`git check-ignore` 确认它**未被忽略**。内网 IP 不应入库。
- `docs/INTEGRATION_TEST_RUNBOOK.md:48` 的 MCP 示例 IP `10.2.69.245` 与上述 `10.2.68.223` 不一致，至少一处陈旧。
- `images/*.png`（4 张，共 2.9MB）最近一次提交是 `2dfa706`（**2026-01-30**），而 UI 此后有约 131 次提交、并在 6 月与 9 月经历重大重设计。README 截图基本确定已不代表当前界面。
- `config/config.dev.yaml` 含**两枚真实形态的上游 API key** 与内网/第三方 base URL。它已被 `.gitignore:11` 正确忽略、未入库，但文件仍留在工作区，建议清理并改用 `$env:` 占位。
- 工作区存在 **31MB 的陈旧二进制 `./llm-tracelab`**（2026-08-05，已 gitignore）。它已不认识新标志（如 `db migrate status --check-db` 报 `unknown flag`），若有人照 runbook 直接执行 `llm-tracelab …` 会静默用到旧版本。

### D8. 其他文档一致性问题（低优先）

- `docs/MCP_GUIDE.md:74-117` 的工具清单（19 个）漏掉已注册的 `responses_audit_trace`、`responses_audit_tool_calls`（`internal/mcpserver/server.go:405-412`）。对照：`README.md:183-188` 只列了 6 个工具，而实际注册 **21 个**。
- `docs/POSTGRES_BASELINE_SQL.md:54-67,88-101,118-128` 的表清单漏掉 `tool_call_audits`、`analysis_jobs`、`app_settings`、`channel_configs`、`channel_models`、`model_catalog`、`model_aliases`——其中多张被 `POSTGRES_OPERATIONS_RUNBOOK.md:108-110` 自己标为热点表。
- `docs/UPSTREAM_PROVIDERS.md:90-114` 漏掉已注册的 preset 别名 `github`、`google_ai_studio`（`internal/upstream/resolved.go:71,75`）。
- `docs/ARCHITECTURE.md:110-138` 的存储边界与关键包清单缺 `internal/responses`、`internal/channel`、`internal/appdbmigrate` 及 Responses semantic/audit 表——该文读起来像 Responses 之前的状态。
- `docs/MAINTAINER_BASELINE.md:5-15` 的适用范围漏 `internal/responses`、`internal/channel`、`internal/upstream`、`internal/appdbmigrate`。
- `docs/CURRENT_IMPLEMENTATION.md:159,162` 用 “Requests”/“Channels” 指代 Monitor 页面，实际是 “Traces”/“Providers”；`:203` 自己已用 “Providers”。
- `docs/CURRENT_IMPLEMENTATION.md:134-150` 的“当前重要表包括”漏掉同文讨论的 `request_audits`、`execution_events`、`upstream_exchanges`、`tool_call_audits`、`app_settings` 等。
- `docs/PROJECT_BASELINE.md:74,78` / `docs/CURRENT_IMPLEMENTATION.md:44` 仍用 “Stage 9/10A/11A/12A/13A/14A” 这类历史阶段编号来描述“当前基线”。
- `README.md:183-188` MCP 工具清单严重不全（见上）。
- `README.md:59-62` + `README.md:8,11` 仍以 “Responses server-mode” 表述已改称 “local Responses runtime” 的机制（`AGENTS.md` 已统一为 “local Responses runtime”）。
- `docs/DEVELOPMENT_COMMANDS.md` 对 `bench:core` 等描述与 Taskfile 一致；Taskfile 中 `default`、`generate:ent`、`migrate:ent:sqlite`、`migrate:db:up`、`auth:init-user`、`auth:create-token`、`clean` 从未被文档提及（低优先，非错误）。
- 术语债务：“server-mode” 在当前事实文档中仍出现约 35 处（`CURRENT_IMPLEMENTATION` 9、`PROJECT_BASELINE` 5、`RESPONSES_SERVER_DESIGN` 19），`cmd/server/root.go:53` 的 CLI `Short` 与 `cmd/server/main_test.go:86` 也仍是 “Responses server-mode”。建议统一为 “local Responses runtime/execution mode”。

---

## 4. 已验证为**正确**、不要“修”的内容

为避免误改，以下经逐条回溯源码确认仍然成立：

- **`responses_server.enabled` 的代码侧清理是干净的**：`responses_server.enabled`、`ResponsesServerEnabled`、`LLM_TRACELAB_RESPONSES_ENABLED`、`responsesServerConfigFromServeConfig`、`ResponsesLocalExecutionAvailable` 在 Go 代码中零命中；无任何 YAML 在 `responses_server` 下设置 `enabled`。残留仅在文档。
- 记录格式：V3 前导 `# llm-tracelab/v3` 与 V2 2KB 兼容（`pkg/recordfile/recordfile.go:15,18`）与 `AGENTS.md`、`ARCHITECTURE.md` 描述一致。
- 五档 `responses_strategy` 取值 `auto|prefer_native|prefer_local_server|native_only|local_server_only` 在 `internal/routeplan/routeplan.go:36-40` 定义与校验，`GATEWAY_ROUTING_UI_DESIGN.md:117-123` 文档正确（问题只在于“它是不是 YAML 键”）。
- `responses_server` 下 `default_model`、`force_store`、`max_request_body_bytes`、`path`、`auto_compact`、`compact_history_item_threshold`、`model_profiles`、`adopt_channel_model_profiles`、`function_executors.*`、`codex_compat.*`、`tokenize_counter.enabled` **均真实存在且被读取**（`internal/config/config.go:159-213,484-555`）。
- doctor 检查名与 `--check-db` 必需表集合、`model_catalog_drift`/`codex_config_drift` 的 `skipped_reason` 行为（`cmd/server/doctor.go:783,827`）准确；warning 不导致默认失败（`--fail-on-fail=true`、`--fail-on-warn=false`，`:118-119`）。
- 全部 `LLM_TRACELAB_*` 文档变量在代码中存在；`HOSTED_TOOLS_ROADMAP.md:216` 关于 `config/config.yaml` “默认全开测试”的说法**属实**（`mcp.enabled`、`tools.web_search.enabled`、`tools.mcp.enabled`、`codex_compat.enabled` 均为 true）。
- 会话 ID 抽取顺序、分析任务类型、deterministic detectors、存储/认证契约字符串、Codex 诊断字段、`mock`/`searxng` provider、`static_response`/`external_command` executor、`ErrIncrementalStreamUnsupported`、tokenize 响应形状、协议族与 preset→protocol_family 映射、MCP 协议版本与 19 个文档化工具、compose 服务/端口/环境变量、`db migrate down` 确实被阻止——均与代码一致。
- 所有 Taskfile 任务名、文档引用的 CLI 命令与标志（`db migrate status --check-db`、`auth migrate status --check-db`、`analyze backfill-exchanges --dry-run`、`audit query`/`audit tool-calls --latest-by-call`、`provider probe|probe-report|probe-apply`、`models codex-config`、`doctor` 各标志等）**均存在**；没有“文档提到但 Taskfile/CLI 缺失”的任务。
- 前端所有实际发出的请求路径都能解析到已注册的后端路由（唯一匹配项是 `POST /api/router/reload` 无人调用）；所有 fetch/URL 字面量集中在 `api.js`。
- 提交的前端产物 `internal/monitor/ui/dist` 与 `web/monitor-ui/src` **当前是同步的**（dist JS 与 tri-state 改动同在 `8a6cc65`，CSS 同在 `82aa962`）；`vite.config.js:7` 的 `outDir`、`server.go:37` 的 `//go:embed ui/dist/*`、Taskfile `ui:build` 三者一致，不存在 outDir/embed 错配。
- `pkg/recordfile`、`pkg/replay`、`internal/responses/audit.QueryService.ListCompactProvenance`、`internal/responses/chatclient`、`internal/responses/codexfixtures`、`tests/fixtures/codex`、`internal/appdbmigrate`、golang-migrate、`ent/postgres-migrations` 均存在。

---

## 5. 建议处置顺序

**第 0 阶段（防止问题再生产）**
1. 修 `docs/README.md:17-40` 的分类（D1）——这是 7 类事实错误的共同根因。
2. 修正 `AGENTS.md:17,21,29,54,76`（D2、D3、H4）——AI agent 与人都按它行事，错误代价最高。

**第 1 阶段（静默 no-op / 必然失败，用户会直接踩到）**
3. 删除 5 处 `responses_server.enabled: true`（H1）。
4. 处理 `startup_policy`：删配置键或实现语义，二选一（H2）。
5. `docker compose run` 命令修正（H5）。
6. `routing.settings.responses_strategy` 措辞改为 Monitor 设置（H4）。
7. `limits.scope` 的 `token`（静默失效）与 `header`（未记载）（2.6.3）。
8. `model_discovery: static` → `static_only`（2.6.4）。

**第 2 阶段（真实行为缺陷）**
9. `TraceDetailPage.jsx:192` 的 `prompt_token_details` → `prompt_tokens_details`（2.6.1，一行修复）。
10. 三态 capability 的 “inherit” 清除语义（2.6.2）。
11. 修/删 `tests/monitor-smoke.spec.js` 与 `tests-real/monitor-real-server.spec.js` 中对已删除 secret-storage UI 的断言（2.7）——并核实这些测试是否还在 CI 中执行。

**第 3 阶段（代码清理）**
12. 删除 `internal/evals` 与 dataset/eval/experiment 集群，或明确标注“已建存储、入口待接线”（H3）。
13. 删除 18 个零引用导出符号 + 死常量 + `WriteMetaFile` 空实现（2.1）。
14. 删除漂移的 `SupportsEndpoint`（2.2），明确 `upstream.mode` 的废弃去留（2.2、D4.2）。
15. 删除 `UpstreamOverview.jsx` + 约 46 条死 CSS + 14 个死 `apiPaths`（2.7）。
16. 删除 `--no-input` 或实现其语义（2.5）。

**第 4 阶段（仓库卫生）**
17. `.codex/config.toml` 取消跟踪并加 ignore；清理 `config/config.dev.yaml` 明文密钥；删除 31MB 陈旧二进制与 `test-results/`（D7）。
18. 修 `README.md:59-62` 死链；处理 `docs/v1/reference-materials/` 的 4.3MB 被取代快照；重拍 README 截图（D6、D7）。

**可选加固**
19. CI（`.github/workflows/ci.yml:43`）只跑 `go test -v ./...`。建议加入 `task fmt:check`、`task lint` 与前端单测，否则上述前端测试失效、格式/lint 告警都不会被发现。
20. 配置加载为非严格 `yaml.Unmarshal`（`internal/config/config.go:319`），未知键静默忽略——这正是 H1/H2 能被长期忽视的机制。建议改为 `KnownFields(true)` 或对未知键产出启动告警，可一次性消除整类“文档写了但代码不读”的问题。

---

## 6. 验证方法与工具限制（可复核）

- `go build ./...` 通过；`go vet ./...` 无输出。
- `golangci-lint run ./...`（配置启用 `govet`、`ineffassign`、`staticcheck`、`unused`，见 `.golangci.yml`）：**0 issues**。
- `staticcheck` 直接运行在本环境**不可用**：默认缓存 `/root/.cache/staticcheck` 只读，且内置 Go 1.26 与项目 `go1.25.0` 不匹配。子调查线改用 `GOROOT=<go1.25.9 toolchain>` + `STATICCHECK_CACHE=/tmp/scache` 重跑，`-checks=U1000` 与 `-checks=all` 均为 **0 findings**（已做阳性对照）。因此“无非导出死符号”这一结论是经过验证的，而非工具失败导致的假阴性。
- 符号引用统计方式：`grep -rn "\bSym\b"` 覆盖 `.go`/`.md`/`.yaml`/`.json`/`.toml`/`.sql`，排除 `ent/` 生成代码；对“仅测试引用”与“完全无引用”分别计数。
- 前端字段校验：提取 244 个前端 snake_case 属性读取，对照 940 个 Go `json:` tag 做近似匹配，得到唯一不匹配项 `prompt_token_details` ↔ `prompt_tokens_details`。
- 文档链接校验：脚本解析 80 个 md 文件的相对链接并逐个 `os.path.exists`。
- 关键结论均已在本机直接复现，包括：`unknown command "/app/bin/llm-tracelab"`、`startup_policy` 零读取点、`UpstreamOverview`/`localSecretKey` 零引用、`prompt_tokens_details` tag、`native_only` 等五档策略取值、`mode` 无路由分支、`SupportsEndpoint` 与 `SupportsEndpointForModel` 分歧、`.codex/config.toml` 被跟踪。

**本次审计本身未修改任何被审计的文件**；仅新增本报告。第 7 节记录审计之后的修复落地情况。

## 7. 修复落地记录（审计后执行）

范围约定：**`internal/evals` 整块保留不动**（H3 的存储层、eval/dataset/experiment 相关 ent schema 与 store 方法、以及相关文档声明均不触碰）。以下为实际改动，全部经 `go build` / `go vet` / `golangci-lint` / `go test` / Playwright 复验。

### 7.1 代码：删除死符号与失效配置

- `cmd/server/root.go`：删除死标志 `--no-input` 及其 `mustBindPFlag` 调用；删除从未被填充的 `cliError.Suggestions` 字段。
- `internal/config/config.go`：删除 `RouterConfig.ModelDiscovery.StartupPolicy`（H2 的根因）；删除 `Config.AuthDatabasePath()`、`Config.ResponsesCodexCompatEnabled()`；新增 `validateLimits()` —— 当 `limits.enabled` 为真时拒绝未知 `limits.scope`，并要求 `header` scope 配置 `limits.channel_key_header`。
- `internal/proxy/handler.go`：删除 `responsesRoutingDecision` switch 中不可达的 `default` 分支。
- `internal/router/router.go`：删除零引用 `(*Router).HasLocalResponsesServerBackend`；`router_test.go` 改用 `SupportsEndpointForModel`。
- `internal/auth/store.go`：删除零引用 `DefaultDatabasePath`。
- `internal/recorder/recorder.go`：删除零引用常量块与 no-op `(*Recorder).WriteMetaFile`。
- `internal/responses/chatclient/stream.go`：删除零引用 `AggregateChatCompletionStream`。
- `internal/responses/runtime/runtime.go`：删除零引用 `FunctionToolExecutorRegistry` / `FunctionToolExecutorSnapshot`（保留内部 `functionToolExecutorSnapshot()`）。
- `internal/responses/runtime/store.go`：删除未使用接口 `ConversationContinuationStore`。
- `internal/responses/tools/hosted/contract.go`：删除未使用 `Phase` 类型及其常量、5 个未使用 `Status*` 常量。
- `internal/store/store.go`：删除 `ModelAliasPatch`、`(*Store).SetChannelEnabled`、`SessionSummaryRebuildStats.RebuiltAll/.RebuiltOne`、`(*Store).PathByID`（后者返回 ent 主键冒充 cassette 路径，属潜在缺陷）。
- `internal/upstream/checker.go` / `checker_test.go`：删除 `CheckConnectivity` / `checkConnectivity` / `DiscoverModels` 及对应测试（`DiscoverModelsResolved` 是唯一活跃路径）。
- `internal/upstream/resolved.go`：删除零引用 `ConnectivityPathVertexModels`，以及漂移的重复 `SupportsEndpoint`。
- `pkg/llm/google.go`：删除零引用包装函数 `FromGeminiRequest` / `(*LLMResponse).ToGeminiResponse`。
- `pkg/observe/parser.go`：删除零引用 `(*Registry).Parsers`。

### 7.2 代码：真实缺陷修正

- **三态能力清除**（2.6）：`ChannelModelProfilePatch` 新增 `ClearSupports*` 三个字段；`UpdateChannelModelProfile` 改为 `switch` 语义，使 PATCH 中显式 `null` 能清除 pin 并回落到继承，而省略字段保持原值。`internal/monitor/server.go` 的 PATCH 处理器改为先读原始 body，再解析出显式 `null` 的键。新增 `TestChannelModelCapabilityExplicitNullClearsToInherit` 覆盖 pin / 省略 / 清除三种情况。
- **前端字段名**（2.6）：`TraceDetailPage.jsx` 的 `prompt_token_details` → `prompt_tokens_details`，与 `pkg/recordfile` 的 JSON tag 一致（原写法导致缓存 token 详情永远读不到）。

### 7.3 代码：前端死代码

- 删除死组件 `src/components/routing/UpstreamOverview.jsx` 及空目录。
- `src/styles.css`：删除 69 条整规则 + 19 组选择器中的死类名 + 1 个空 `@media` 块（434 → 378 个类选择器，83 KB → 71 KB）；保留 18 个由模板字符串动态拼装的类名前缀（`event-severity-*` 等），它们不是死代码。
- `src/lib/api.js`：删除 15 个零引用键，其中 `channel*` 系列是 `provider*` 的重复别名。
- `src/lib/monitor.js`：删除 legacy `/channels/...` 链接构造器 `buildChannelLink`；移除 `normalizeTraceTab` 中与 `default` 等价的分支。
- `src/routes/ModelDetailPage.jsx`：`apiPaths.channelModel` → `apiPaths.providerModel`。

### 7.4 配置与文档

- **H1**：`startup_policy` 从 README/README_EN/config 及 6 个 `config/examples/*.yaml` 中删除（结构体字段同时删除）。
- **H4**：3 处配置注释不再把 `routing.settings.responses_strategy` 写成 YAML 键，改为说明它存在于应用库 `app_settings`、经 Monitor Routing 设置写入；`AGENTS.md` 同步。
- **H5**：`docs/PRODUCTION_DEPLOYMENT.md` 的 `docker compose run` 命令已修正为可执行形式。
- **D1**：`docs/README.md` 把 11 篇路线图/设计稿从“当前事实文档”移入新增的“设计与路线图（非事实源）”分区，并补齐遗漏条目。
- **D2**：`AGENTS.md` 架构段改为 Postgres-first，补全 CLI 命令文件、`internal/upstream`/`internal/channel`/`internal/responses`/`internal/appdbmigrate`，新增 Storage 小节，并修正 SQLite 文件名为 `llm_tracelab.sqlite3`。
- **D3**：清理“Responses server-mode 是可选/开关”表述；`cmd/server` 的 CLI `Short` 文案改为 `local Responses runtime`（含测试断言）。
- **D4**：修正与代码冲突的能力声明 —— MCP hosted tool executor 已实现（原文档列为未实现/future）：`README.md`、`README_EN.md`、`CURRENT_IMPLEMENTATION.md`、`PROJECT_BASELINE.md`、`PRODUCTION_DEPLOYMENT.md`、`POSTGRES_STORAGE_MIGRATION.md`、`RESPONSES_GATEWAY_GAP_ANALYSIS.md`；`PROVIDER_PROTOCOL_ENTRYPOINTS.md` 的“Implementation Plan”改为“Implementation Status”（5 项均已落地）。
- **D5**：`DEVELOPMENT_COMMANDS.md` 的 `task run` 补充 DSN 前置条件；`MAINTAINER_BASELINE.md` 的 SQLite 文件名修正；`UPSTREAM_PROVIDERS.md` 的“SQLite 是长期配置事实源”改为应用数据库（生产 Postgres）。
- **D6**：README 中 3 个已归档文档链接指向 `docs/archive/...` 真实路径；`docs/v1/reference-materials` 的 11 个 `/docs/sources/...` 死链改为同级相对路径。
- **D7**：`.codex/config.toml` 取消跟踪并加入 `.gitignore`（文件内是环境相关的内部 MCP 地址；注意该地址仍存在于既有 git 历史中，彻底清除需重写历史）；`INTEGRATION_TEST_RUNBOOK.md` 去掉内部环境名与旧内网 IP。
- **D8**：`MONITOR_GUIDE.md` 的 “Requests” 标题改为 “Traces”；`README.md`/`README_EN.md` 的 MCP 工具清单从 6 个补全为实际注册的 21 个；截图区补充“为较早版本界面”的说明。

### 7.5 测试修复（审计中发现的陈旧测试）

- `web/monitor-ui/tests/monitor-smoke.spec.js`：UI 默认语言是中文，而该套件断言英文标签，导致 **HEAD 上就有 6/8 用例失败**（CI 不运行 UI 测试，因此长期未被发现）。修复：把语言初始化提升到全局 `beforeEach`；同步更新已改名的标签（`Apply detected suggestions`→批量探测改为按 provider 的模态框、`Repair missing usage`→`Repair token stats`、`Protocol family`→`protocol`）；补齐 `/api/routing/exchanges`、`/api/routing/summary` mock（Routing 页已从 `/api/traces` 迁移）；按当前“显式协议面可免校验创建”的行为修正按钮启用断言。修复后 8/8 通过。
- `tests-real/monitor-real-server.spec.js`：删除已移除的本地 secret key 用例，`/channels/*` 断言改为 `/providers/*`。

### 7.6 明确保留、不修改的内容

- **`internal/evals` 整块**及相关 eval/dataset/experiment 存储与文档（按约定保留）。
- **截图 PNG**：保留旧图，仅在 README 注明其对应较早界面；用户决定等界面稳定后再替换。
- `config/config.dev.yaml`（含明文密钥，已被 gitignore）：未删除，可能是本地开发配置。

> 说明：第二轮曾把 `pkg/llm`/`pkg/observe` 的零使用结构体字段列为“保留待决策”，第三轮经用户确认后已删除，见 7.9。

### 7.7 复核结果

- `gofmt -l ./cmd ./internal ./pkg ./unittest ./web/monitor-ui/test-fixtures`：无输出。
- `go build ./...`、`go vet ./...`：通过。
- `golangci-lint run ./...`：**0 issues**。
- `go test ./...`：全部通过。
- `bun run build`：UI 产物已重建（`internal/monitor/ui/dist`，CSS 78 KB → 70.85 KB），旧哈希资源已删除。
- Playwright：`monitor-smoke` desktop 8/8 + mobile 8/8；`monitor-real-server` 5/5。
- `.github/workflows/ci.yml` 经 `yaml.safe_load` 校验结构有效，`build` job 共 15 步。

### 7.8 第二轮：审计剩余项收尾

- **2.1 死常量**：删除 `pkg/observe/ir.go` 中 11 个零引用常量 —— `ParseStatusRecorded/Indexed/ParseQueued/ParseFailed/AnalysisQueued/Analyzed/AnalysisFailed`、`NodePatch/NodeAudio/NodeVideo`、`ToolOwnerInferred`（在用的 `ParseStatusParsed` 保留）。
- **2.4 未读取字段（内部包）**：删除 `internal/proxy/handler.go` 的 `aggregatedModelArchitecture.Tokenizer/.InstructType` 与 `aggregatedModelTopProvider.IsModerated`。这三个字段由 `model_metadata.go` 本地构造后序列化进 `/v1/models` 响应，但**从不被赋值、也从不被读取**，且都带 `omitempty`，因此删除不改变任何响应载荷（此前的“JSON 透传字段”说法经复核不成立，已更正）。
- **2.7 前端残余死代码**：`api.js` 删除零引用 `listTotal`；`traceTools.js` 的 `getDeclaredToolName`/`getDeclaredToolDescription`/`getDeclaredToolParameters`/`normalizeToolName` 仅内部使用，去掉多余 `export`（不是删除）；`hooks/useJSON.js` 删除无人引用的 `monitorAuthHeaders`/`MONITOR_TOKEN_KEY` 再导出；`ChannelDetailPage.jsx` 删除死函数 `setEditValue`；`App.jsx` 改用规范名 `ProvidersPage`/`ProviderDetailPage` 并删除两个重复导出别名（`/channels/*` 兼容路由保留）；`i18n.jsx` 删除 4 个零引用键（`account.monitorUser`、`audit.warnings`、`common.loading`、`common.updated`），并把 `TokensPage.jsx` 中硬编码的 "Token inventory" 接回 `t("tokens.inventory")`。**注意**：`theme.dark/light/system` 经 `t(\`theme.${option.value}\`)` 动态查表在用，未删除。
- **2.8 无调用方路由**：删除 `POST /api/router/reload` 及其处理器 `routerReloadAPIHandler`（该路由在此之前零调用方）。共享辅助函数 `reloadRouterFromChannels`、`effectiveChannelService` 仍被其它处理器广泛使用，保留。
- **2.6.3 文档同步**：`CREDENTIAL_ROUTING_OPERATOR_GUIDE.md` 中“未识别 scope 不会被处理、限流会被静默忽略”的说法已随新增的 `validateLimits()` 失效，改为说明未知 scope 与缺少 `limits.channel_key_header` 都会在配置加载阶段直接报错。
- **D6 残余**：该目录（`docs/v1/reference-materials/`）曾先补说明 `/docs/en/...` 是上游站点路径；第三轮确认 `docs/protocol-reference/` 为唯一权威来源后整目录删除，见 7.9。
- **审计第 20 项（根因加固）**：`internal/config` 新增 `unknownConfigKeys()` / `warnUnknownConfigKeys()` —— 用 `KnownFields(true)` 二次解码，把“YAML 里有、结构体里没有”的键在启动时打成告警，而不是继续静默忽略（加载本身仍保持非严格，避免破坏既有部署）。新增 3 个测试：能识别已删除的 `responses_server.enabled` 与 `router.model_discovery.startup_policy`、不误报已知键、且 `config/config.yaml` 与全部 7 个 `config/examples/*.yaml` 均无未知键。
- **审计第 19 项（CI 加固）**：`.github/workflows/ci.yml` 增加 `gofmt` 检查、`go vet`、`golangci-lint`（v2.11.4）与 Playwright 两套 UI 套件 —— 这正是 UI 测试长期腐坏却无人发现的直接原因。同时 `playwright.config.js` / `playwright.real.config.js` 不再硬编码本机路径 `/snap/bin/chromium`，改为仅在设置 `PLAYWRIGHT_CHROMIUM_EXECUTABLE` 时覆盖，否则使用 Playwright 托管浏览器（CI 由 `bunx playwright install --with-deps chromium` 提供）。

### 7.9 第三轮：按用户决策完成的收尾

- **删除 `docs/v1/reference-materials/`（4.3MB，2026-05-13 快照）**：确认 `docs/protocol-reference/`（2026-06-02 快照）为唯一权威来源后整目录删除。同步处理全部引用：`docs/v1/protocol-parsers.md:9` 改指 `../protocol-reference/README.md`；`docs/v1/development-plan.md` 的 “Reference Materials 使用规则” 整节改写为指向 `docs/protocol-reference/upstream/{openai,anthropic,google-gemini,google-vertex}` 的实际文件；`docs/protocol-reference/README.md` 删除 “Historical Materials” 段并把“本目录是唯一快照来源、v1 快照已删除”写入 Source Policy；`AGENTS.md` 文档目标清单删除该目录条目。全仓已无指向该目录的有效引用。
- **删除 `pkg/llm`/`pkg/observe` 的零使用结构体字段**：经全仓核对（Go 代码、前端、fixture、SQL、文档、JSON key）确认以下字段既无写入方也无读取方后删除 —— `pkg/llm` 的 `LLMContent.ImageData/.AudioData/.VideoData`、`LLMUsage.AudioTokens`、`AnthropicResponse.StopSequence`；`pkg/observe/ir.go` 的 `ObservationSafety.Categories/.ProviderRaw` 及随之失去唯一引用者的 `SafetySignal` 类型。核对方式：`grep -rn "\bFieldName\b" --include="*.go" .` 全部只命中声明行；对应 JSON key（`image_data`/`audio_data`/`video_data`/`audio_tokens`/`stop_sequence`/`provider_raw`/`safety_signal`）在仓库自有文件中也零命中（`audio_tokens`/`stop_sequence` 仅出现在上游 vendor 快照里，那是供应商自己的 API schema）。`AnthropicToLLM` 本就只读 `ID/Model/Role/StopReason/Content/Usage`，不读 `StopSequence`。同步删除 `docs/v1/protocol-parsers.md` 中 Anthropic 响应“关注字段”里的 `stop_sequence`（该字段从未被抓取）。
- **删除 `.codex/config.toml`**：文件已从索引与工作区一并删除（内网 MCP 地址只存在于既有 git 历史中，按用户要求不重写历史）。

### 7.10 仍未处理

- **README 截图**：`images/*.png` 仍是较早界面（`Channels` 时期）。用户决定“等界面稳定后再替换”，本次仅在 README 注明其对应较早版本。
- **`config/config.dev.yaml`**（含明文密钥、已被 gitignore）：未删除，可能是本地开发配置，需人工确认。
