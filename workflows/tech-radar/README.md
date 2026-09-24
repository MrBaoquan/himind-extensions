# 科技雷达日报

每日采集公开科技热点仓库，按领域规则筛选并与历史对比，由内置 AI 结合公司项目上下文给出解读与相关性排序，产出可直接浏览的网页报告。

> 通知渠道（钉钉群）已从本条链路中移除。当前闭环终点是"编排好的网页报告"；需要推送时再接通知能力与对应的自动审批策略。

## 步骤

| 步骤 | 类型 | 风险 | 说明 |
| --- | --- | --- | --- |
| TR-COLLECT | capability `tech-radar.collect` | 只读 | 多源采集（公开 GitHub 搜索为首个来源）、归一化、领域规则过滤、去重、与历史台账对比得出新上榜与排名变化 |
| TR-INSIGHT | runtime `himind.builtin` | 只读 | 结合公司上下文做相关性判断与推荐理由，**失败即降级**为确定性排名 |
| TR-REPORT | capability `tech-radar.report` | 本地写 | 产出归档 HTML + 结构化 JSON + markdown 摘要，连同快照与解读写入扩展私有数据目录的按天索引 |

入口分三段（`collect` / `insight` / `publish`），任一段坏了可以按段重跑，不必重新采集。

入口的可验证前提按“平台能证明的事实”声明，而不是按业务参数声明：

- `collect` 需要 `workspace`（对应 run 的 `workspace_root`，也满足 `local_requirements.workspace`）；
- `insight` / `publish` 需要 seed Artifact `tech-radar-snapshot`；
- 业务参数（`domains`、`topics`、`window_days`、`top_n`、星数下限）作为 run 输入原样传给能力，不参与入口判定，因为平台只能校验“字符串事实”，而 `domains` 必须是数组。
- 这些参数**不写进 step input**：step input 会覆盖 run 输入，一旦写死就等于让启动表单字段失效。默认值集中在扩展侧（见 `defaultDomains` 与 `defaultMinStars`），用户填了才生效。

## 数据归属与承载页

报告归档、历史台账、快照与解读的**所有权在扩展**，不在工作流，也不在 Agent 主窗口：

- 存储根目录默认由 Agent 注入插件进程的 `HIMIND_PLUGIN_DATA_ROOT`（生产环境为 `%LOCALAPPDATA%\HiMindAgent\plugin-data\<plugin_id>`，升级与回滚都不影响）；`report_root` 作为**可选覆盖**出现在启动表单里，显式填写后会被扩展记住（`state.json`），写入位置与自带归档视图读取位置始终一致，不会出现「报告写到了别处、视图却读默认目录」的分叉；
- 工作流只负责“什么时候跑、按什么顺序跑”，产出 Run 与 Artifact 事实；
- 扩展自带的 **“科技雷达 · 历史归档”视图**（`contributes.views`，入口 `ui/index.html`）按期次浏览榜单、智能解读理由、摘要与网页原稿，并可手动补采一期；
- 视图通过 `tech-radar.archive.list` 与 `tech-radar.archive.read` 两条只读能力取数，打开条目链接走扩展自己的 `tech-radar.link.open`（`local_action`）。页面不依赖宿主页面上下文，因此单独打开或嵌入宿主行为一致。

## 采集信号与报告结构（0.4.0 起，默认值归位见 0.4.1 / workflow 0.4.2）

上一版只查 `created:>最近 N 天`，看到的是刚建仓、几十星的项目。现在按领域分两条赛道：

- **活跃基础盘**：`topic:<伞形标签> pushed:>窗口 stars:>下限 fork:false archived:false`，按总星数排序 —— 例如 OpenCV、Ultralytics、Transformers、three.js；
- **新星**：`created:>窗口 stars:>新星下限`，窗口内新建且已起量的项目。

领域预设（`domains`）：`cv`（computer-vision）、`llm`（llm）、`ar-vr`（virtual-reality + augmented-reality）、`general`（machine-learning）。GitHub 的 `topic:` **不支持 OR**（带括号返回 0 条，不带括号 422），所以实现上是「一个标签一次查询 + 本地去重」；未鉴权时按 7 秒间隔放慢节奏（可用 `query_interval_ms` 或 `HIMIND_TECH_RADAR_QUERY_INTERVAL_MS` 覆盖），单次运行默认最多 16 次查询。

**星数增量（`stars_gained`）由扩展自己的历史快照计算**：GitHub 搜索接口不提供增长速度，所以「升温榜」从第二期开始有值，第一期会在报告里说明原因。

报告分三段呈现：**升温榜**（按 `stars_gained`）、**活跃基础盘**（按领域）、**新星**；每段写明筛选依据，页脚注明来源与查询次数。

解读步骤不再把快照塞进提示词，而是声明 `input_artifacts: ["tech-radar-snapshot"]`：平台把快照的文件路径注入步骤输入，模型读文件后作答（见 ADR 0075）。数据走文件、提示词只说任务，提示词长度因此与报告体量无关。

启动成本：包内声明了 `default_entrypoint: collect` / `default_exitpoint: published`，**默认值一律由扩展自己持有**（默认领域 `cv`+`llm`+`ar-vr`、基础盘星数下限 800、新星下限 150、窗口 7 天、每段 10 条），工作流步骤输入不再写死这些参数——写死的 step input 会覆盖用户输入，等于让表单字段失效。因此 **`workflow run com.himind.workflow.tech-radar '{}'` 就能跑完一期**，表单里每个字段都预填默认值，用户只在想改的时候改；需要固定归档位置时才填 `report_root`（也可以存成启动预设，日常只改其中一两个字段）。

## 三个待建能力的契约（实现依据）

### `tech-radar.collect`

输入：`domains`(list，默认 `cv`/`llm`/`ar-vr`)、`topics`(list，自定义标签)、`languages`(list)、`exclude_owners`(list)、`min_stars`(int, 默认 800)、`new_min_stars`(int, 默认 150)、`window_days`(int, 默认 7)、`top_n`(int, 默认 10)、`report_root`(text, 可选，默认扩展数据目录且会被记住)、`report_date`(可选)、`credential_handles`(可选 GitHub token 句柄)。

行为要求：

1. 使用公开搜索接口按时间窗与主题白名单采集，**token 只从 Agent Secret Store 取**，绝不进入工作流输入；
2. 归一化后按领域规则过滤（排除 `exclude_owners`），并按星数增量排序；
3. 与本机历史台账对比，计算 `is_new`、`rank_delta`、`first_seen`；
4. 无 token 时限流必须可降级（少量请求 + 缓存 + 退避），并把 `rate_limit` 写进快照；
5. 产出 Artifact `tech_radar_snapshot`，并写入历史台账（按天一条）。

产出：符合 `artifacts/tech-radar-snapshot.schema.json` 的 JSON，同时以 `artifacts` 信封返回。

### `tech-radar.report`

输入：`snapshot`（来自上一步的 Artifact 或入口重跑时的历史快照）、`insight`（可选，缺失即降级）、`report_date`。

行为要求：渲染自包含 HTML（内联样式，无外部依赖、无脚本执行）、结构化 JSON、以及 markdown 摘要（Top 5–8 + 新上榜 + 链接）；按数据目录 `YYYY-MM-DD/` 归档 `index.html`、`report.json`、`snapshot.json`、`insight.json`，并维护 `index.json` 期次索引；把 `snapshot_sha256`、`rule_version`、`prompt_version` 写进报告产物，保证结论可回溯。

### Artifact 交付契约（严格校验）

工作流对 `strict` Artifact 做三件事：读 `uri` 指向的文件、用 `sha256` 核对字节、拿内容过 schema。因此：

1. `tech-radar.collect` 交付的是 `snapshots/YYYY-MM-DD.json`（对象，含 `entries`），不是扩展内部的 `tech-radar-history.json` 台账数组；
2. `tech-radar.report` 交付的是 `YYYY-MM-DD/report.json`（含 `report_date`、`html_path`、`entry_count`、`snapshot_sha256`），不是网页本身；网页路径作为事实字段保留在 JSON 里；
3. 两处 `sha256` 都是对**落盘文件字节**计算，而不是对内存结构再序列化一次。

这三条由 `TestStepArtifactsMatchDeclaredSchemaAndDigest` 锁定，避免再次出现“内容形状对、摘要对不上”的隐性问题。

### `tech-radar.archive.list` / `tech-radar.archive.read`

视图的数据入口，均为 `read_only`：`list` 返回期次索引（可选按日期过滤），`read` 返回某一期的报告、快照、解读、HTML 与路径（缺省取最新一期），并拒绝非法 `report_date`。

产出：Artifact `tech_radar_report`。

### `dingtalk.group.notify`

输入：`report`（或 `markdown` + `title`）、`dingtalk_group`（白名单标识）、`credential_handles`。

行为要求：只允许发送到**配置内白名单群**；每日同一报告只发一次（幂等键 = 报告日期 + 摘要哈希）；失败重试但不刷屏；凭据只通过句柄引用。

## 降级与边界（按当前平台实际行为描述）

- **渲染层降级**：`tech-radar.report` 没收到 `insight` 时，报告仍会生成，只是标记 `degraded=true`（规则稿）；
- **采集层降级**：无 GitHub token 时限流可降级（记录 `rate_limit`），历史台账缺失时按“全部新上榜”处理；
- **通知未接入**：报告仍归档，需要时再补通知步骤，不影响证据链；
- **防幻觉**：prompt 要求只引用快照里的 `entry_id`，越界即视为无解读；
- **解读步骤失败即降级**：TR-INSIGHT 声明 `on_failure: "continue"`，AI 不可用或超时时该步记为 `skipped`（`error` 写明 `degraded after failure: …`，同时写入 `error` 事件），TR-REPORT 继续执行，产出 `degraded=true` 的规则稿报告。失败可见、流程不中断。

### 网络声明

解读步骤显式声明 `allow_network: true`：当前 Runtime 提供方没有可证明的网络隔离能力，若声明 `false` 会被预检直接阻断。这是**如实声明**这一步的模型调用可能访问网络，而不是绕过限制。相应的防护是：输入侧只包含公开数据与公司技术栈摘要，不在快照里放入凭据或业务敏感明细；prompt 明确要求忽略仓库描述中出现的任何指令（防提示注入）；结论必须引用快照 `entry_id`，越界即视为无解读。

如后续接入可证明隔离的 Runtime Provider，应把该步骤改回 `allow_network: false` 以获得更强的声明能力。

## 尚未实现

`tech-radar.collect`、`tech-radar.report`、归档读取、自带视图与**定时触发**都已实现（见下）。每天自动执行不再依赖外部计划任务，用平台的定时任务即可（Agent 左侧「工作 → 定时任务」，或命令行）：

```powershell
himind-agent.exe schedule set '{"id":"daily-tech-radar","kind":"workflow","target_id":"com.himind.workflow.tech-radar","cron":"0 9 * * *","input":{"workspace_root":"F:\\WebProjects\\项目看板","topics":["ai-agent","llm"],"window_days":14,"top_n":12},"execution":{"entrypoint":"collect","exitpoint":"published"}}'
```

计划保存在 `schedules.json`，桌面 Agent 每 30 秒检查一次，到点后走与手动启动完全相同的 Run 路径（`_origin.source=scheduler`）。定时是平台级能力，工作流只是当前支持的一种目标类型；详见 `项目看板/docs/scheduling.md` 与 ADR 0074。

### 本轮实测暴露的底座缺口

1. **步骤级降级缺失（已补）**：Runner 原先没有 `on_failure`，任何 Step 失败都会终止 Run。现已在平台侧补上步骤级降级能力（Harness 的 `workflow-package.v1` + Runner + Go 校验器 + 契约文档），本工作流是第一个使用者。
2. **缺少定时触发（已补）**：平台新增目标化定时任务与 Agent 内调度线程，并已有独立「定时任务」页面（ADR 0072 → ADR 0074），本工作流是第一个目标。
3. **Runtime 步骤没有工具策略（已补）**：本轮 TR-INSIGHT 曾经“卡住”正是因为 prompt 承诺了输入里没有的公司上下文，模型转而去翻工作区文件/调用工具，而 headless 运行没有审批面，于是永不返回。现在平台支持 `runtime.tool_policy`：`none` 会为这一步生成禁用全部模型可见工具的 overlay（文件、Shell、网络、技能、待办、目标、Workflow、子代理、提问/计划模式、HiMind MCP），模型看不到任何工具 schema。TR-INSIGHT 已声明 `tool_policy: "none"`；无法兑现该声明的 Provider 会 fail closed，不会退化成“尽力而为”。prompt 里的“禁止工具”说明保留为第二道防线。

关于 Runtime 超时：实测 `runtime.timeout_seconds` 是生效的（用 `HIMIND_DSH_TIMEOUT_SECONDS=45` 强制验证，45 秒后进程树被终止并返回 `execution exceeded 45 seconds and was terminated`）。此前观察到的“卡住 30 分钟”是 Run 租约先到期 + 人工 `workflow recover` 的结果，不是超时失效。

关于工具策略的验证：把**旧的会跑飞的 prompt** 配上 `tool_policy: "none"` 单独跑 DSH，18 秒返回合法 JSON（模型明确说明自己没有任何文件读取工具，不会编造）。也就是说这个失效模式现在由平台兜住，不再依赖 prompt 写得刚好。

### 解读步骤卡住的真实根因（2026-09-19 定位）

现象：TR-INSIGHT 内调用内置 AI（DSH headless）长时间不返回，Run 只能靠超时/回收结束。

定位过程：

1. 手工用同一份凭据跑 DSH 小任务：5 秒返回，说明 DSH、凭据、网络都正常；
2. 手工跑**完整 14KB 解读 prompt**：DSH 正常启动、正常流式输出推理，但推理内容是“输入里没有公司信息，让我去工作区找找文件”——随后进入工具探索；
3. headless 运行由 Agent 以 `stdin=null` 启动，没有审批面，工具调用无法收敛，于是 Run 一直等；
4. 把 prompt 改成“只依据本条消息里的 JSON 作答、禁止读取文件/执行命令/调用工具、没有公司信息就按通用相关性排序”后，同一份 14KB 上下文 **10 秒返回合法 JSON**。

结论：不是平台 bug，也不是模型或网络问题，而是**prompt 承诺了并不存在的输入**，把 agentic runtime 引向工具探索。修复方式是把 runtime prompt 写成自洽、封闭、可判定的任务，并显式禁止工具使用。

## 已完成的端到端验证（2026-09-19）

用真实 GitHub 公开搜索跑通两段链路：

1. `tech-radar.collect`（topics=ai-agent+llm，窗口 14 天，Top 12）返回 12 条真实仓库，写入历史台账；
2. `tech-radar.report` 产出 `YYYY-MM-DD/index.html`（5934 字节，自包含、内联样式、描述中的 HTML 已转义）、`report.json` 与 markdown 摘要；未传解读时 `degraded=true`，降级路径符合设计。

工作流编排层验证（同一日）：

1. `workflow run workflows\tech-radar` 以 `entrypoint=collect`、`exitpoint=published`、`workspace_root=F:\WebProjects\项目看板` 启动，
   `TR-COLLECT` 通过严格 Artifact 校验（内容过 schema、`sha256` 与文件字节一致）并在 2 秒内完成；
2. 产物落在 Agent 注入的扩展私有数据目录：`snapshots/2026-09-19.json`（Artifact 交付物）、`tech-radar-history.json`（扩展内部台账）；
3. `TR-INSIGHT` 在 45 秒（测试用短超时）后被终止，Step 记为 `skipped` + `degraded after failure: …`；
4. `TR-REPORT` 仍执行并成功：`2026-09-19/report.json`（`entry_count=12`、`degraded=true`、自带说明性 summary）、`insight.json = {}`、`index.json` 期次索引更新；Run 状态 `succeeded`、`error` 为空，`tech-radar-report` Artifact 通过严格校验；
5. 未降级路径由单测覆盖：`TestRenderResolvesUpstreamSnapshotAndInsightFromWorkflowContext` 断言 `workflow_context` 里的解读会被渲染进报告且 `degraded=false`。
6. 修正 prompt 后重跑：**全链路 17 秒完成**，`TR-COLLECT`/`TR-INSIGHT`/`TR-REPORT` 三步全部 `succeeded`，`degraded=false`，报告含 8 条带理由的解读、5 条 highlights 与 AI 总结，`insight.json` 已归档真实解读；`index.html` 里能看到 `class="reason"` 的推荐理由区块。
