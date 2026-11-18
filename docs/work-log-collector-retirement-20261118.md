# 工作日志采集器退役与 Agent 直连验收

日期：2026-11-18
结论：源码迁移、采集、日志写入、去重、报表与候选门禁已完成；真实 AI Workflow Runtime 执行受当前 Windows 子进程权限边界阻挡，不能宣称全链路通过。生产日志未改写，真实样本留在被忽略的任务目录。

## 架构变更

- Agent `workspace.session.*` 统一管理 DSH、ACP、Claude Code、Codex、OpenCode 和 12 个额外客户端预设，以及声明式 JSON 来源。客户端根目录和来源映射由 `workspace.session.source.get/configure` 管理。
- 会话服务返回原生会话身份、工作区归属、消息时间与证据。活动元数据快照有期限且不保存正文；分页固定已读取页边界。OpenCode 支持真实数据库的 `session_v2`/`session_message` 表和原生消息 ID。
- Work-log 0.19.1 只调用 Agent Sources/Activity API，不再读取客户端 JSONL/SQLite，也没有 legacy 采集回退。来源不完整、Agent 不可用或超预算会显式返回状态/错误。隐私配置读取失败时拒绝生成素材。
- 日志幂等键为 `source + native session_id + natural date`。跨日按真实消息时间拆分；自动工时只标为活动区间估算。对账只有在完整、全范围、无隐私筛选且来源可用时才判 stale，否则标 unverified。
- 删除 `plugins/work-log-collector` 活动源码。源码备份保存在 `.himind/long-tasks/20261118-retire-work-log-collector/`。市场活动插件列表移除 collector，`retired_plugin_ids` 防止同步时重现；历史 Release 和旧 Workflow 依赖快照不改写。
- 移除项目路径 autofill 能力与 UI 按钮，工作区路径归属统一改由 Agent 注册表管理。历史来源配置不隐式迁移或扫描。
- 工作日志技能升为 0.7.2，修正自然日和工时口径，并依赖 work-log 0.19.1；旧迁移计划标记为被本报告取代。

## 候选制品

候选在 `dist/work-log-agent-20261118/`，均未确认、提审或发布。

| 制品 | SHA-256 | Candidate Test |
| --- | --- | --- |
| `com.himind.work-log-0.19.1.hmpkg` | `5f8d49609e608269a2002dc77fbd915f1017d9fb22162737bc296077deba59be` | passed |
| `com.himind.skill.work-log-0.7.2.hmskill` | `5d8a8b311a485bb4412a53c141afdcd960cc48912c0505e954a51941ac89ff9f` | passed |
| `com.himind.workflow.daily-work-log-0.9.1.hmwf` | `8c79e261e5e1ffdff5c031390eb4a35386e2701a72e6af6cd89a8706474594ea` | passed |

Skill 在隔离 Codex/Copilot/WorkBuddy 目标完成注册和清理。三个 Candidate Test 的结构、依赖、契约检查通过。Workflow Candidate Test 另有测试 Profile 缺少 Runtime 的本地预检警告；独立模式下真实 Workflow Doctor 后续报告 `ready=true`，但不代表实际 AI 已运行。

最终 Agent 候选二进制：`.himind/long-tasks/20261118-retire-work-log-collector/agent-bin/himind-agent.exe`，SHA-256 `366194c83390edbcf091ebe8043df24ef770c56fb15683c0db557eb4999f6fda1`。同为 0.3.92 版本号，需核对二进制 SHA-256/能力版本，而非仅凭 SemVer 判断是否含本轮改动。`dist/` 仍保留本任务先前保存的 0.19.0/0.7.0/0.7.1/0.9.0 旧 Candidate 文件；最终交付采用上表新版本，不覆盖旧 Candidate。

## 验证

- `go test ./plugins/work-log` 通过，覆盖 Agent-only 调用、跨日分页、隐私、失败不回退、来源委托、估算、AI 字段保护、锁定及对账。
- `go test ./tools/cmd/himind-catalog-sync` 通过，新增 collector 历史 Release 退役过滤测试；`go test ./sdk/capability ./sdk/jsonrpc` 通过。
- `go test ./tooling/commands` 通过，包含本次 Work-log 命令标题长度门禁和 Release Pipeline。
- Agent 最终 `session_` 测试 88 项通过，包含 `session_v2` 原生消息 ID 分页回归。Agent 与 MCP 二进制最终构建通过，日志见任务证据目录。
- 前端 TypeScript 检查及 Playwright 会话历史浏览器集成通过。
- 会话服务真机能力完整性不等于全量数据完整，真实补跑结果如下。

## 真实素材补跑

使用 Agent 候选 + work-log 候选，日期仅限 2026-11-18，日志根为隔离任务目录：

- Codex：3 条素材；来源 `partial`，`complete=false`、`truncated=true`，Agent 返回 `metadata_scan_partial` 和 5 项跳过/告警。
- OpenCode：3 条素材；来源 `partial`，`complete=false`、`truncated=true`，Agent 返回 `database_scan_partial` 和 2 项大体量会话跳过。
- Claude Code：来源可用，当天 0 条活动，`complete=true`。
- 使用最终 work-log 0.19.1 候选重新采集；将 Codex/OpenCode 六条真实素材写入隔离日志后重复 upsert，再读取仍为 6 条，六个 `(source, session_id, date)` 键唯一。
- 实际生成日报、`2026-W47` 周报和 `2026-11` 月报，共 9 个 Markdown/HTML/JSON 文件。

**上述采集、写入及报表验收没有执行 AI 总结。** 日报 narrative 明确标注这一点。素材来自真实本机历史，但有来源/大体量会话未覆盖，不代表完整补采；生产日志未改写。

全仓 `go test ./...` 复跑后，`internal/mediadownload` 的最终文件完成状态断言以及 `plugins/media-library` 两个媒体产物状态/校验断言仍失败。第一次全仓运行另发现 Work-log 命令标题超长；已修正为 0.19.1，并通过完整 `tooling/commands` 测试。Work-log、catalog sync、SDK、tooling commands 均通过；未修改两个无关媒体模块。

## AI Workflow 阻塞

独立配置下 Workflow Doctor 通过，使用独立 DSH Home 时 Agent 报 Runtime ready。但实际 `workflow run` 在创建测试 Workflow 安装版本目录时以 `os error 5 / Access Denied` 失败；预建空目录后仍失败。隔离子进程经 Agent `ai.service.list` 正式能力读取生产 AI 服务也被拒绝；单独 AI ask 的 Windows 工作区 ACL 预热同样被宿主令牌拒绝。

推理步骤未执行。为绕过此限制修改 ACL、提升权限、搬用凭据或切换生产安装都不安全，因此没有尝试。AI 输出、Workflow 内 AI→素材合并及 Workflow 驱动的写入/报表仍未验证。任务结论为“候选及 Agent 采集/隔离写入/报表通过；真实 AI Workflow 待具备可执行权限的 Agent 环境后验收”。

失败启动在生产 Agent `workflows/` 下留下本任务命名的空测试目录，只有空 `versions/`，没有包或日志；清理被宿主策略拒绝。未部署、提交或发布。所有素材、隔离日志、报表与命令回执留在 `.himind/long-tasks/20261118-retire-work-log-collector/`，不在候选包中。

## 正式 Agent 续跑

用户要求在正式生产 Agent 执行后，已通过其现有 MCP 服务（Connected，Agent 0.3.92）重新检查：Agent 会话服务为 1.1.0，返回 17 个客户端来源；生产当日日志原有 0 条。生产已安装的仍是 work-log 0.17.0、collector 0.7.3 和 daily-work-log 0.7.8。

已用正式 Agent 的 Candidate API 保存 work-log 0.19.1 与 daily-work-log 0.9.1 候选。work-log 0.19.1 的正式 Agent Candidate Test 通过，哈希与本地候选一致。daily-work-log 0.9.1 的正式 Agent Candidate Test 被阻止：因为 production 仍未激活插件 0.19.1，所需的 `work-log.material.collect` 不可用。候选只保存在 Agent 的 drafts，没有确认、激活、安装或提审；production 日志仍未写入。

正式 Agent MCP 没有本地 Candidate 的开发激活接口。工作流开发版需在 Agent 桌面端扩展工作区激活；插件也需在激活后重测 Workflow。完成这两项桌面激活后，才能通过正式 `workflow.run.start` 启动 0.9.1 并验证真实 AI、写入和报表。当前未能代替用户完成该 UI 操作，也未把旧 0.7.8 工作流当作新流程通过。
