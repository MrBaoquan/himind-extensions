# 工作日志真实补采验证（2026-11-18）

状态：本轮扩展构建、隔离安装、真实采集、日志读写和报告验证完成，48 项真实数据断言通过。AI 总结 Runtime 未执行，不能将本记录解释成包含 AI 总结的完整工作流生产验收。

## 修复与候选

Agent 清单校验此前把后端 `capability_dependencies` 交给视图外部能力白名单，导致 `workspace.session.activity` 依赖无法安装。Agent 源码已拆分校验，仅向后端 broker 增加四个精确只读 ID：`workspace.session.sources/list/messages/activity`。视图白名单、通配限制、重复检查和最多 32 条依赖的限制保留。真实验证使用隔离 Agent；该修复未由本任务部署到生产安装版。

真实导入又发现 Go JSON-RPC SDK 的 1 MiB 扫描行限制：53 条日志导出后作为 JSON 字符串封装的导入请求为 1,106,598 字节，插件因此无法返回响应。SDK 请求上限已改为 16 MiB，并显式拒绝超限请求；回归覆盖大请求后继续处理下一条请求、恰好达到上限，以及超限时不调用处理器。旧制品保留，修复以新补丁版本交付。

本次扩展工作副本起始基线为 `a82af92`，Agent 起始基线为 `af739fc`。两仓库其它任务已有和并行产生的修改不在本任务范围。

| 最终制品 | SHA-256 |
| --- | --- |
| `dist/work-log-real-20261118/patch/com.himind.work-log-collector-0.8.1.hmpkg` | `7cee379d88b5da72119481a98fb3d5276961c6882cead53efdb04ca874a3ad32` |
| `dist/work-log-real-20261118/patch/com.himind.work-log-0.18.1.hmpkg` | `fe50dd350cf9a55109b7fcb7a5799f8bdf658c322dd6d75c9bcd605abd907dcf` |
| `dist/work-log-real-20261118/patch/com.himind.workflow.daily-work-log-0.8.1.hmwf` | `b8a39411aba40d8be5b9396db0d6f5e114910d0e92d5d6c0b001078a75159397` |
| `dist/work-log-real-20261118/patch/com.himind.workflow.daily-work-log-0.8.1.extension-lock.json` | `9ad88cb59f634c0e81a24635b52ae008e3c53889a9ee4a39d3f289a6fef8bd14` |

通过 Agent 的 `extension.plugin.validate/build/package/candidate.save` 和 `extension.test` 生成、保存、验证两个插件候选，均为 passed。新插件已导入隔离实例，版本分别为工作日志 0.18.1、采集器 0.8.1。

工作流使用已有 `build-workflow-release.ps1` 构建本地候选，依赖声明及锁均要求工作日志 0.18.1、采集器 0.8.1，`extension.test` 和静态合同通过。隔离实例缺少 `himind.builtin` Runtime，预检为 `passed_with_warnings`，仍 `release_blocked=true`。未伪造发布 pin、签名或 catalog 记录；这些制品属于本地候选。

## 真实采集

通过真实 Agent Gateway 和每次调用 broker 运行本轮插件，没有使用模拟 broker。生产工作区登记表只复制到隔离数据根；最终验证时生产文件与只读快照哈希一致。

| 路径 | 来源 | 活动日期 | 会话日条目 | 结果 |
| --- | --- | --- | --- | --- |
| 默认 `auto` 的兼容回退 | Codex | 11 月 17–18 日 | 19 | 真实采集 |
| 默认 `auto` 的兼容回退 | OpenCode | 11 月 17–18 日 | 12 | 真实采集 |
| `backend=agent` | Claude Code | 11 月 5–12 日 | 22 | 全部含证据和真实 `workspace_project_id` |

共 53 条，跨 9 个活动日；按来源、原生会话 ID、日期去重。新版 0.8.1 采集器再次通过 Agent 完整读取相同历史范围，得到 22 条带工作区 ID 和证据的 Claude Code 条目，`complete=true`。

Codex 实时 Agent 分页第三页实测返回 `SESSION_SNAPSHOT_CHANGED`：当前会话仍在写入，快照发生变化。严格 Agent 模式报错；默认模式丢弃 Agent 部分素材后回退，返回 `provider=legacy`、`complete=false`。DSH 宿主未运行，隔离实例没有 ACP 历史；这些来源未计为真实采集通过。

素材和候选输入均通过对应 JSON Schema。此次采用采集器确定性候选摘要；没有把它标记成 AI 润色结果。自动分钟数属于活动估算，兼容来源之间没有统一扣除重叠，不能作为人工在岗时长。

## 真实读写和报告验收

用户在隔离实例确认完全放行后，所有操作继续经过 Agent Gateway。此前的审批超时记录保留为历史，不再是当前阻塞。

- 53 条记录实际写入隔离日志库；重复写入后仍是 53 条，稳定 ID 和来源/会话/日期身份保持一致。
- 22 条 Agent 记录的工作区 ID、证据、估算方法、时间区间和工时标记保留；工作区 ID 没有被误作日志业务项目 ID。
- 锁定一条真实 Agent 记录后尝试改写标题和分钟数，被跳过，内容未改变。
- 导出全部记录后，以单个超过 1 MiB 的请求成功导入；往返后仍为 53 条，证据和工作区关联保持一致。
- 生成 9 份日报、2 份周报、1 份月报，含 Markdown、HTML 和 JSON。按覆盖日期核对条目数与估算分钟数，并验证报告 Artifact SHA-256。
- Agent 的零估算记录保持零值，没有按轮次补造分钟数；报告的可选 false 字段按 Go 的省略语义比较，不把缺省值误判为丢失。

真实业务脚本共 48 项断言通过，0 失败。另对全部 12 份报告完成 Schema、内容统计和 Artifact 摘要一致性检查。

本机测试日志库：`F:/WebProjects/himind-extensions/.tmp/work-log-real-20261118/test-journal`。导入往返目录为同级 `roundtrip-journal`。写入使用 `resolve_projects=false`；本任务没有改写生产日志、生产插件安装或项目归类登记。

## 自动回归与边界

- 本轮 SDK、工作日志和采集器 Go 回归：157 passed，0 failed，0 skipped（包含子测试）；对应 `go vet` 通过。
- 扩大至所有 SDK 调用方：338 passed，2 failed，1 skipped。失败为媒体库的 `TestImageEditorImmutableVersionsReplayAndAdoption`、`TestAudioCompletedReceiptRecoversIntoLibraryWithProvenance`。通过 Go overlay 换回修改前 SDK 对照运行，同样失败，未改动这些独立问题。
- 扩大 vet 发现微信工具 `devtools.go` 的 `QueryDoc` 与其它字段重复使用 `query` tag。修改前 SDK 对照同样报错；本轮受影响模块 vet 无此错误。
- 复用本任务前轮 Rust 插件模块 51、broker 3、跨仓集成 1 项成功证据，共 55 项。此次 Go 传输修复未改动 Rust，未重复运行这 55 项。
- 任务文件格式/差异检查、工作日志 UI JS 语法检查通过。候选包验证未包含本机素材、测试日志或临时目录。
- 没有执行 AI 总结 Runtime、发布、生产安装更新或生产数据补写。

## 证据

本机已忽略的 `.tmp/work-log-real-20261118/` 含私人会话素材，不属于源码或发行载荷：

- `delivery.json`：最终结果、版本、范围、制品摘要和已知边界。
- `real-verification.json`：48 项真实数据断言和日期/来源统计。
- `verification-calls/`：实际 Gateway 输入输出、导入大请求、重复写入、锁定和 12 份报告响应。
- `patch-build-calls/`：Agent 插件校验、构建、打包、候选保存和门禁结果。
- `workflow-patch-build.json`、`workflow-patch-extension-test.json`：工作流候选合同、依赖锁、预检与门禁。
- `collector-patch-real-scan.json`：0.8.1 采集器真实 Agent 历史补采结果。
- `regression/go-jsonrpc-callers.jsonl`、`media-baseline.jsonl`、`baseline-control.json`、`go-vet-work-log.log`：Go 回归和基线对照证据。
- `regression/plugin-tests.log`、`broker-tests.log`、`mcp-collector-integration.log`：前轮 Rust 成功输出。
- `real-verification-pending.json`：已被最终交付结果取代的历史待审批记录。