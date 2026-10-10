# 每日工作日志

采集本机 AI 客户端（Codex / Claude Code / OpenCode / Copilot / VS Code Copilot Chat）的会话素材，用内置 AI 总结成清晰的工作日志条目写入全局工作日志，并生成日报归档。适合每日收工或定时补记，把散落在各客户端里的工作痕迹沉淀成可检索的日志。

核心是**按日的工作日志**：采集、总结、写入、日报都以单日为单位。周 / 季度 / 年度汇总只是辅助视图（`work-log.report.render` 的 `period` 参数），不在这条链路的默认路径上；日常回看走插件按日历查看的月视图。

模板：`segmented`

## 步骤

| 步骤 | 类型 | 风险 | 说明 |
| --- | --- | --- | --- |
| WL-COLLECT | capability `work-log.session.scan` | 只读 | 探测本机会话来源，按目标日期提取会话摘要，落一份 `session-digest` 素材 artifact，并映射成带功能与工时的确定性条目草稿（`entries`） |
| WL-SUMMARIZE | runtime `himind.builtin` | 只读 | 读取 `session-digest`，为每个真实工作会话产出「清晰标题 + 中文小结 + 完成/决策/经验/后续」的条目，并给当天 `narrative` |
| WL-WRITE | capability `work-log.entry.upsert` | 本地写 | 取 AI 总结条目，按「来源+会话」叠加确定性素材（工时/起止时间），写入全局日志库；重复运行按稳定指纹合并 |
| WL-REPORT | capability `work-log.report.render` | 本地写 | 汇总当天条目产出 Markdown + 自包含 HTML 日报，按 项目 → 功能 聚合内容小结并统计工时，采用 AI `narrative` |

入口分三段（`collect` / `summarize` / `report`），任一段坏了可按段重跑，不必从头再来。
`date`、`source`、`sqlite3_path`、`log_root`、`period` 作为 run 输入原样传给能力；**不写进 step input**，避免覆盖启动表单字段。`date` 缺省为今天，`log_root` 缺省为文档目录下的全局日志库。

## 数据归属

工作日志库的所有权在 `com.himind.work-log` 插件：工作流只负责「什么时候采集、按什么顺序写入」，不直接读写日志文件。写入的条目与技能（`com.himind.skill.work-log`）会话导出写入的条目共享同一稳定 `entry_id`，因此 AI 精修后的条目会与自动采集结果合并，而不是重复。

会话来源：

- **Codex**：`~/.codex` 下的 rollout JSONL（原生解析，无需外部工具）。
- **Claude Code**：`~/.claude/projects` 下的会话索引。
- **OpenCode / Copilot**：SQLite 会话库，需要 `sqlite3` CLI；缺失时在 `skipped` 里如实标记，不伪造结果。
- **VS Code Copilot Chat**：`%APPDATA%/Code(- Insiders)/User/workspaceStorage/*/chatSessions/*.jsonl` 的会话补丁文件；按同目录 `workspace.json` 还原工作区路径，稳定版与 Insiders 一并覆盖，无需外部工具。
- **通用可配置来源**：用声明式规格（路径 + 格式 + 字段映射）接入任意客户端，配置写在 `<日志根>/config/sources.json`，也可用 run 输入的 `sources_config` 内联覆盖。内置预设覆盖 Cursor、Gemini CLI、Cline、Continue、Windsurf、通义灵码、CodeBuddy、Kiro、WorkBuddy 等（标注实验性，字段可按实际结构调整）。

## 项目归类

写入时按 `com.himind.work-log` 的项目登记表（`<日志根>/projects.json`）自动归类：命中登记名或别名就归一到规范项目，未知项目名自动登记。采集侧优先用会话所在 Git 仓库根目录名推断项目，把同一仓库的不同子目录合并到同一个项目。

## 功能与工时

- **功能 / 模块**：采集侧尽力从会话工作目录推断——取 Git 仓库内相对仓库根的末级目录；非仓库场景下目录名不同于项目名时取目录名。判断不了就留空，日报归入“未分类功能”。技能精修时可显式填写 `feature`。
- **工时**：优先按会话逐条消息的间隔实测“活跃时长”（超过 20 分钟的间隔视为离开，不计入），标为 `measured`；只有起止时间时用区间，标为 `span`；完全无时间信息时按轮次估算，标为 `estimated`；人工填写标为 `manual`。单条工时上限 8 小时（480 分钟），避免异常长会话或区间把统计拉爆。
- 日报按 **项目 → 功能** 两级聚合，每组给出确定性内容小结（标题 + 摘要 + 完成 / 决策 / 经验 / 后续）与本组工时；概览给出总工时与实测 / 估算拆分。技能可通过 `narratives` 传入按项目或 `项目/功能` 的模型小结叠加在确定性小结之上。

## 边界

- 采集器不写工作日志库：它至多把会话素材落一份临时 artifact（扩展私有数据目录）供 AI 总结步骤读取；写入统一由 `work-log.entry.upsert` 负责，保证日志库只有一个写入者。
- 未安装 `sqlite3` 时，OpenCode/Copilot 来源会跳过；可用启动表单里的 `sqlite3_path` 指定路径。
- 工作日志条目的标题与小结来自内置 AI 的总结（`WL-SUMMARIZE`），工时与起止时间取采集的确定性素材；若 AI 步骤失败，本次运行会失败而不是写入原始素材。没有内置 AI 时可用 `com.himind.skill.work-log` 技能从会话导出后再写回。
- 通用来源预设依赖各客户端本地格式，标注为实验性；路径或字段不匹配时在 `<日志根>/config/sources.json` 覆盖即可，无需改代码。

Workflow 源码使用 `workflow.json` 作为执行契约，`ui/workflow-view.json` 作为声明式 UI。标准目录包括 `schemas/`、`artifacts/`、`connectors/`、`examples/` 和 `tests/contract/`。
