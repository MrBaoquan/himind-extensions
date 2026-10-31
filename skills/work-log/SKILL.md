---
name: work-log
description: 把当前 AI 会话整理成结构化工作日志并写入全局日志库，以按日日志为核心、可按日历回看，并可选择生成日报等汇总。当用户说“记今天的工作日志/整理这次会话/写日报”时使用。
---

# 工作日志助手

把已经发生的真实工作内容整理成结构化日志条目，写入全局日志库，尽量让人不做重复的手工记录。默认只根据当前会话中的事实生成条目；不确定的内容留空或标注，不要编造。

## 触发与边界

在以下请求中使用本 Skill：

- “整理今天的工作日志”“把这次会话记录一下”。
- “写今日日报 / 周报”，或需要在已有日志上补充今天的记录。
- “我刚才做了哪些事，帮我归档”。

本 Skill 负责把事实结构化成条目并调用 `com.himind.work-log` 插件的能力写入本地全局日志库：

- 只写本机全局日志库，不向任何网络服务上传。
- 不采集密钥、Token、Cookie、私钥、`.env` 或凭据内容；遇到疑似敏感内容先跳过并在汇报中说明。
- 不修改用户源码或仓库；写入动作只针对日志库。

## 工作流

### 1. 确认日期与范围

1. 用系统时间确认真实的当前日期，不要臆测；用户说“昨天”“本周”时换算成具体日期。
2. 确认要整理的范围：默认只整理当前会话中已完成的工作。用户指定某个时间段或某次会话时以用户为准。
3. 用户没有明确要求写入时，先把条目草稿展示出来，确认后再写入。

**归档口径**：条目按「本机本地时区下的日期」归到 `date`；`started_at` / `ended_at` 用 RFC3339 记录真实时刻。跨零点的会话以其**起始时刻所属日期**归档，避免同一次工作被拆到两天。

### 2. 从会话中提取事实

对每项工作回答清楚：

1. 做了什么：任务、模块、接口、文档或决策。
2. 产出与结果：交付物、验证结论、修复的问题。
3. 遇到的问题：阻塞、风险、待决事项。
4. 后续：下一步计划、需要别人配合的事。

只使用会话里出现过的信息（命令输出、讨论、结论、提交）。没有证据的结论标为“待确认”，不要补全成事实。

### 3. 组织条目

一次会话通常对应一条或多条条目，按独立工作项拆分。每条字段：

- `title`：动词开头的一句话，说明具体结果。
- `project`：项目或主题名，尽量稳定可检索。写入时插件会按项目登记表归一：命中登记名或别名就归到规范项目，未知名称自动登记。可用 `work-log.project.list` 查看已有项目，优先复用已有名字。
- `feature`：这条工作所属的功能 / 模块（如“日历视图”“项目登记表”），用于在项目下按功能归类与小计。能从会话里的目录、模块名判断就填；判断不了就留空，报告会归入“未分类功能”。
- `category`：受控类型，只能从中文枚举里选一个：`开发` / `修复` / `设计` / `调研` / `文档` / `评审` / `会议` / `运维` / `其他`；不确定就用 `其他`，不要自造类型。
- `source`：当前客户端标识（对齐 HiMind 标准客户端 ID），如 `codex`、`claude-code`、`claude-desktop`、`opencode`、`github-copilot`、`himind-ai`。
- `session_id`：能拿到会话标识时填写，用于同日重复整理时幂等合并。
- `started_at` / `ended_at`：只有明确时间才填，填 RFC3339 或能解析的时间。
- `minutes` / `minutes_source`：这条工作的工时（分钟）与来源。能用会话时间戳估算就用 `measured`（按消息间隔算活跃时长）；只有起止时间用 `span`；只能按轮次估用 `estimated`；人工填写用 `manual`。不确定工时就留空，不要编造。
- `summary`：一两句话说明成果与要点。
- `outcomes` / `decisions` / `learnings` / `blockers` / `next`：字符串数组，没有就留空。
- `tags`：可检索标签。
- `refs`：`{"type":"commit","value":"<hash>"}` 形式的引用，没有就留空。

同一天已经写过的条目再次整理时，复用相同的 `source` + `session_id`（或相同的 `title`），插件会按稳定标识合并而不是产生重复。

### 4. 写入全局日志库

调用插件能力写入：

```json
{
  "id": "work-log.entry.upsert",
  "arguments": {
    "date": "<YYYY-MM-DD>",
    "entries": [
      {
        "source": "codex",
        "session_id": "<可选>",
        "project": "<项目>",
        "feature": "<功能/模块，可选>",
        "category": "开发",
        "title": "<一句话结果>",
        "summary": "<要点>",
        "minutes": 60,
        "minutes_source": "measured",
        "outcomes": ["<产出>"],
        "next": ["<后续>"]
      }
    ]
  }
}
```

- `date` 使用步骤 1 确认的日期。
- 写入成功后再读取一次 `work-log.entry.read` 确认当天条目，避免重复或漏写。
- 用户指定了自定义日志目录时，透传 `log_root`。

纠正已写入的条目：

- 修改：`work-log.entry.upsert` 带原 `entry_id` 且 `overwrite: true`，整条替换（可以把某字段清空）；不带 `overwrite` 时是字段级合并，空值不会冲掉旧值。
- 删除：`work-log.entry.delete`，传 `date` + `entry_id`（或 `entry_ids`）；当天条目删空后文件会一并移除，日历上不再残留空白日期。

### 5. 需要日报时生成报告

默认只需日报。用户明确要月/季/年度汇总时再生成辅助报告：

```json
{
  "id": "work-log.report.render",
  "arguments": {
    "period": "daily",
    "date": "<YYYY-MM-DD>",
    "narrative": "<由你提炼的一段主线叙述>",
    "narratives": {
      "<项目>": "<该项目的整体小结>",
      "<项目>/<功能>": "<该功能的小结>"
    }
  }
}
```

- `period` 默认 `daily`；`weekly` / `quarterly` / `yearly` 是弱化的辅助视图，只在用户点名时使用，日常回看走“工作日志 · 日历回看”的月历。
- `date` 用目标周期内的任意一天。
- `narrative` 是你基于真实条目提炼的主线，不要写成空泛套话；不确定就省略，插件会退回确定性聚合。
- `narratives` 是分项目 / 分功能的模型小结：键用项目名，或 `项目/功能` 精确到功能。报告会先给确定性的“按项目 · 按功能”内容小结，再在此基础上叠加你的叙述；没有就不写，不要编造。
- 报告会同时生成 Markdown 与自包含网页，落在全局日志库的 `reports/` 下。

#### 季度与年度的起算月

季度 / 年度按「日志库级偏好」切分，缺省是自然季度（1 月起算）。公司按 2 月起算时，Q1 = 2/3/4 月，Q4 跨年到次年 1 月。

- `work-log.preferences.get`：先读口径，返回 `fiscal_year_start_month`（1-12）与 `quarter_rule`（如 `Q1 2-4 月 · Q2 5-7 月 · …`）。
- `work-log.preferences.save`：设置起算月，参数 `fiscal_year_start_month`，1-12 之外一律报错（不会静默收敛）。

```json
{"id": "work-log.preferences.save", "arguments": {"fiscal_year_start_month": 2}}
```

用户说「季度从 2 月算」「我们财年 2 月开始」时先 `get` 确认当前口径；需要改才 `save`，不要每次报告都重写。

**读报告键时务必先读口径**：起算月不是 1 时，键里的年份是「财年起始年」，`2026-01-15` 属于 `2025-Q4`，`2026 年度` 覆盖 `2026-02-01 ~ 2027-01-31`。只按自然年解释键会算错归属。报告的 `fiscal_year_start_month` / `quarter_rule` 字段与正文「口径：」行都可用于核对。

### 6. 汇报

按顺序返回：

1. `日期`：本次整理的日期与范围。
2. `写入条目`：按项目 → 功能分组列出标题，并标出 `category` 与工时（标明实测 / 区间 / 估算）。
3. `日志库`：写入的全局目录或报告路径。
4. `报告`：是否生成日报（默认），以及键名（如 `2026-10-09`）。
5. `待确认`：跳过或无法确认的内容，以及工时无法判断的部分。

不要输出会话原文、凭据、完整本机路径清单或与日志无关的细节。

## 项目归类

项目是一份可管理的登记表，存在日志库根目录的 `projects.json`：

- `work-log.project.list`：列出项目、每个项目的条目数与未归类项。
- `work-log.project.upsert`：新增或更新项目名、别名、状态（`active` / `archived`）。
- `work-log.project.resolve`：把一个原始项目名按登记表归一。
- `work-log.project.assign`：把一批历史条目重新归到目标项目（清理归类时用）。

当用户说“把某堆工作归到某项目”“统一项目叫法”时，用别名合并或 `work-log.project.assign` 处理，不要逐条重写。

## 当前工作台与计划

当用户问「现在在做什么」「哪些项目在推进」「优先级」「下一步」「今天怎么安排」时，先读当前工作台，不要只翻历史日志：

```json
{"id": "work-log.project.board", "arguments": {}}
```

工作台返回项目状态（`planned` / `in_progress` / `blocked` / `review` / `done` / `paused`）、优先级、目标日期、下一步、阻塞、最近活动和近 7 天工时。默认只把最近 14 天有日志的项目视为「正在推进」（`is_current:true`）；更早的项目是 `is_other:true`，不要把它们当作未完成工作汇报。阶段（`stage`）和进度（`progress`）由最近日志与下一步智能推断，`progress_source` 标明是手工、按下一步还是阶段推断；回答时说明这是推断值。

### 当前工作口径

`work-log.project.board` 默认只把最近 14 天有日志的项目视为「正在推进」（`is_current:true`）。更早的项目会进入 `recent` 或 `history`，不要把它们当作当前工作。

每个项目还会返回：

- `stage` / `stage_label` / `stage_confidence`：根据最近日志、下一步和工作状态推断的阶段（规划、设计、开发、联调、测试、验收、交付、维护）。
- `progress` / `progress_source` / `progress_confidence`：完成进度。`progress_source` 为 `manual` 时是用户手工设置；`next_steps` 表示按下一步完成度推断；`stage` 表示按阶段 + 近期活动推断。
- `activity_bucket`：`current` / `recent` / `done` / `history`。

回答「现在在做什么」时，只列 `is_current:true` 的项目；回答「以前做过什么」或需要全量时再查 `work-log.project.list`。推断的阶段和进度要明确标注为「推断」，不要当成用户确认的事实。

### 更新项目工作状态

用户明确要求「把 A 标记完成」「A 改成 P0」「下一步加 X」时，调用：

```json
{
  "id": "work-log.project.work.update",
  "arguments": {
    "project_id": "<项目 ID>",
    "patch": {
      "state": "in_progress",
      "priority": "P1",
      "goal": "完成当前工作台第一版",
      "next_steps": [{"text": "实现计划保存"}],
      "blockers": ["等待设计确认"]
    }
  }
}
```

规则：

- `patch` 只改显式传入的字段。
- `state=done` 会记录完成时间；从 `done` 改回其它状态会清空完成时间与完成摘要。
- 不根据日志文本自动判定完成；日志里出现「完成」只作为建议，必须向用户确认。
- 完成项目后可以再用 `work-log.entry.upsert` 写一条完成日志。

### 工作项与每日计划

- `work-log.item.list`：查看待排工作项。
- `work-log.item.upsert`：新增工作项，字段含 `estimate_minutes`、`remaining_minutes`、`due_date`、`priority`。
- `work-log.item.update`：字段级更新，例如 `{"status":"done"}`。
- `work-log.plan.get`：读取某天计划。
- `work-log.plan.save`：保存计划与时间块；插件会校验时间重叠和容量。
- `work-log.plan.validate`：只校验不写盘。

时间块使用 `start` / `end` 的 `HH:MM`，类型为 `focus` / `meeting` / `admin` / `break` / `buffer` / `personal`。计划是意图，日志仍然是事实；不要用计划代替实际日志。

## AI 日程规划

当用户说「帮我安排今天」「今天先做什么」「这周怎么排」时，按下面的闭环执行：

1. 读取 `work-log.project.board`，拿到当前项目状态、优先级、下一步和本周投入。
2. 读取 `work-log.item.list`（`include_done:false`），拿到待排工作项、预计耗时、剩余耗时和截止日期。
3. 读取 `work-log.plan.get`，保留已有时间块和 `locked:true` 的块。
4. 先给用户一段简短计划说明，再给出时间块方案；不要直接写库。
5. 用户确认后调用 `work-log.plan.validate`，通过后再调用 `work-log.plan.save`。

规划约束（默认值，用户有特殊要求时以用户为准）：

- 今日聚焦最多 3 个工作项，优先 P0/P1、临近截止和阻塞他人的事项。
- 只承诺可用容量的 70%-80%，保留 15%-25% 缓冲。
- 单个专注块不超过 90 分钟；大任务拆成多个块。
- 会议前后预留准备/收尾时间；不移动 `locked:true` 的时间块。
- 同一天时间块不能重叠；`work-log.plan.save` 会做确定性校验。
- 预计耗时优先使用工作项的 `remaining_minutes`；没有时用 `estimate_minutes`。
- 不要编造工作项或日志；缺少信息时先问用户。

计划 JSON 结构：

```json
{
  "date": "2026-10-19",
  "mode": "balanced",
  "capacity": {"work_minutes": 480},
  "blocks": [
    {"kind": "focus", "work_item_id": "wi-xxx", "start": "09:30", "end": "11:00"},
    {"kind": "break", "title": "休息", "start": "11:00", "end": "11:15"},
    {"kind": "meeting", "title": "周会", "start": "13:00", "end": "14:00"},
    {"kind": "buffer", "title": "缓冲", "start": "16:00", "end": "16:30"}
  ]
}
```

保存后把 `plan.capacity.remaining_minutes` 和风险告诉用户：如果为负，说明今天排不下，应减少承诺或调整截止时间。
需要一键生成可执行排期时，优先调用 `work-log.plan.auto`（仅支持当天；历史/未来日期请手动调整）。它会：

- 只使用当前/待开始项目下的未完成工作项；如果项目还没有工作项，会把「最近工作但未标记结束」的当前项目直接作为可排期项，标题优先用它的下一步，默认时长按优先级为 P0 90 分钟 / P1 60 分钟 / P2 45 分钟 / P3 30 分钟；
- 按项目状态、项目优先级、工作项优先级、截止日期和剩余耗时排序；
- 保留会议、个人事务和锁定块；
- 默认只承诺约 80% 的可用时间，留出缓冲；
- 自动拆分超过 90 分钟的工作项，并在长块之间插入短休。

调用后把生成的时间块、已排/未排分钟数和风险告诉用户。用户仍可手工调整。
计划的实际完成情况以**当日结束后的日志**为准。查看历史计划时调用 `work-log.plan.review`，它会返回每个专注块的实际状态（完成/部分完成/未完成）和完成率；当天计划只返回 `pending`，不要提前把计划块说成已完成。

## 采集来源

自动采集走 `com.himind.work-log-collector`，来源分两类：

- 原生解析：Codex、Claude Code、Claude Desktop、OpenCode、GitHub Copilot（含 VS Code 会话）。
- 通用可配置来源：用声明式规格接入其他客户端，配置在 `<日志根>/config/sources.json`，可用 `work-log.source.list` / `work-log.source.save` 读取与保存（内置 Cursor、Gemini CLI、Cline、Continue、Windsurf、通义灵码、CodeBuddy CLI、Kiro、WorkBuddy 等预设）。

需要新增来源时，先看是否已有预设匹配；没有再用 `work-log.source.save` 增加一条规格（路径 + 格式 + 字段映射），不要改代码。

## 纯手工路径

用户只想手工记一条、不涉及会话时，同样调用 `work-log.entry.upsert` 写入单条 `entry`；也可以引导用户打开“工作日志 · 日历回看”视图，在“记录一条”表单里手工录入。两条路径写入同一个全局日志库。

视图顶部支持按关键词搜索当月日志（回车执行）；侧栏可点击复制日志目录路径，或用“打开目录”在文件管理器中定位日志库。
