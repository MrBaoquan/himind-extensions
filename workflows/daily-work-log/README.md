# 每日工作日志

采集本机 AI 客户端（Codex / Claude Code / OpenCode / Copilot）的会话素材，按会话整理成结构化条目写入全局工作日志，并生成日报归档。适合每日收工或定时补记，把散落在各客户端里的工作痕迹沉淀成可检索的日志。

核心是**按日的工作日志**：采集、写入、日报都以单日为单位。周 / 季度 / 年度汇总只是辅助视图（`work-log.report.render` 的 `period` 参数），不在这条链路的默认路径上；日常回看走插件按日历查看的月视图。

模板：`segmented`

## 步骤

| 步骤 | 类型 | 风险 | 说明 |
| --- | --- | --- | --- |
| WL-COLLECT | capability `work-log.session.scan` | 只读 | 探测本机会话来源，按目标日期提取会话摘要，并映射成日志条目草稿（`entries`） |
| WL-WRITE | capability `work-log.entry.upsert` | 本地写 | 取上游 `entries` 写入全局日志库；同会话重复运行会按稳定指纹合并 |
| WL-REPORT | capability `work-log.report.render` | 本地写 | 汇总当天条目产出 Markdown + 自包含 HTML 日报 |

入口分三段（`collect` / `write` / `report`），任一段坏了可按段重跑，不必重新采集。
`date`、`source`、`sqlite3_path`、`log_root`、`period` 作为 run 输入原样传给能力；**不写进 step input**，避免覆盖启动表单字段。`date` 缺省为今天，`log_root` 缺省为文档目录下的全局日志库。

## 数据归属

工作日志库的所有权在 `com.himind.work-log` 插件：工作流只负责「什么时候采集、按什么顺序写入」，不直接读写日志文件。写入的条目与技能（`com.himind.skill.work-log`）会话导出写入的条目共享同一稳定 `entry_id`，因此 AI 精修后的条目会与自动采集结果合并，而不是重复。

会话来源：

- **Codex**：`~/.codex` 下的 rollout JSONL（原生解析，无需外部工具）。
- **Claude Code**：`~/.claude/projects` 下的会话索引。
- **OpenCode / Copilot**：SQLite 会话库，需要 `sqlite3` CLI；缺失时在 `skipped` 里如实标记，不伪造结果。
- **通用可配置来源**：用声明式规格（路径 + 格式 + 字段映射）接入任意客户端，配置写在 `<日志根>/config/sources.json`，也可用 run 输入的 `sources_config` 内联覆盖。内置预设覆盖 Cursor、Gemini CLI、Cline、Continue、Windsurf、通义灵码、CodeBuddy、Kiro、WorkBuddy 等（标注实验性，字段可按实际结构调整）。

## 项目归类

写入时按 `com.himind.work-log` 的项目登记表（`<日志根>/projects.json`）自动归类：命中登记名或别名就归一到规范项目，未知项目名自动登记。采集侧优先用会话所在 Git 仓库根目录名推断项目，把同一仓库的不同子目录合并到同一个项目。

## 边界

- 采集器只读，不写任何日志文件；写入统一由 `work-log.entry.upsert` 负责，保证日志库只有一个写入者。
- 未安装 `sqlite3` 时，OpenCode/Copilot 来源会跳过；可用启动表单里的 `sqlite3_path` 指定路径。
- 结构化程度是确定性的：标题取首条用户提示，摘要取助手回复摘录。需要更高质量的提炼时，用 `com.himind.skill.work-log` 技能从会话导出后再写回。
- 通用来源预设依赖各客户端本地格式，标注为实验性；路径或字段不匹配时在 `<日志根>/config/sources.json` 覆盖即可，无需改代码。

Workflow 源码使用 `workflow.json` 作为执行契约，`ui/workflow-view.json` 作为声明式 UI。标准目录包括 `schemas/`、`artifacts/`、`connectors/`、`examples/` 和 `tests/contract/`。
