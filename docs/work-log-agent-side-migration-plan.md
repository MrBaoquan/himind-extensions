# 工作日志插件迁移方案：废弃采集器，改用 Agent 侧工作区与会话能力

- 状态：待评审（不含代码改动）
- 日期：2026-11-17
- 涉及代码库：`F:\WebProjects\himind-extensions`
- 涉及扩展：`com.himind.work-log`、`com.himind.work-log-collector`、`com.himind.workflow.daily-work-log`、`com.himind.skill.work-log`

---

## 1. 背景与动机

当前工作日志链路依赖独立插件 `com.himind.work-log-collector`（下称"采集器"，Go 编写的 process-jsonrpc-stdio 插件）。它承担两件事：

| 能力 | 职责 |
| --- | --- |
| `work-log.session.scan` | 解析各 AI 客户端私有会话存储（Codex/Claude Code/Claude Desktop/OpenCode/Copilot/VS Code Chat + 声明式通用来源），提取用户提问与助手摘录，推断项目/功能/仓库路径，估算工时，落 `session-digest` 素材 artifact |
| `work-log.session.sources` | 探测本机客户端会话存储是否可用 |

采集器的维护成本集中在"逐个适配各客户端的私有 JSONL / SQLite 格式"。与此同时，Agent 内核已经具备完整的工作区/项目登记与解析能力（`workspace.registry.*`、`extension.workspace.*`）。因此提出：**废弃采集器，日志的项目归类与上下文走 Agent 侧内置能力。**

---

## 2. 核心核查结论：Agent 侧到底有没有"获取各 AI 客户端历史会话"的能力？

> 这是本方案能否成立的关键前提，做了穷尽核查，结论如下。

### 2.1 核查方法

1. 通过 `system.capability.search` 分页**全量枚举**当前 Agent 的能力目录，共 **472 项**（内置 builtin 173 项 + builtin:himind-media 24 项 + 各插件 275 项），而非关键词抽样。
2. 对所有命名可疑的能力逐个 `system.capability.describe` 核对输入 Schema 与真实描述（见 §11 证据清单）。

### 2.2 结论：按"会话"的三层含义区分

| 层次 | 是否存在 | 代表能力 | 能给出什么 |
| --- | --- | --- | --- |
| A. 会话 ↔ 工作区归属 | ✅ 有 | `extension.workspace.bind` / `current` / `clear` | 当前会话绑定到哪个工作区目录；按会话累加、互不覆盖。**只给归属，不给会话正文** |
| B. Agent 自身运行记录 | ✅ 有 | `workflow.run.list/get`、`skill.run.list/get`、`mission.events.list` | Agent 自己跑过的 Workflow / Skill / Mission 的步骤、事件、产物。**不是各 AI 客户端的会话** |
| C. 读取各 AI 客户端历史会话正文（prompt、时间戳、token、对话轮次） | ❌ **没有** | 无 | 无。全量目录中除采集器自身外，无任何能力读取 Codex/Claude/OpenCode/Copilot 等客户端的会话文件 |

补充排除项（容易误认为可以替代，实际都不行）：

- `ai.client.list` / `ai.client.status`：只检测"本机支持哪些客户端 / HiMind 接入状态与模型摘要"，明确不读取会话内容。
- `workbench.work.summary.get`：读取工作台里"我负责/关注的项目、展项、需求摘要"，是业务看板数据，不是会话记录。
- `document.inspect`：只提取 DOCX/TXT/Markdown 的正文与字数，且需显式传文件路径。
- `media.image.read`：只读取本地图片。Agent 侧**没有通用文件文本读取**能力。
- `workbench.knowledge.search`：检索知识空间，不是会话库。

**最终回答你的问题：是的，经全量核查确认，Agent 侧目前没有获取各个 AI 客户端历史会话正文的能力。** 有完整的工作区归属能力（A 层）和运行记录能力（B 层），但二者都不能替代采集器的"历史会话正文采集"。这意味着本次迁移存在一处**净能力缺口**（详见 §9），方案设计必须正面处理它，而不是假设能平替。

---

## 3. Agent 侧可用的替代能力清单（核查证据）

| Agent 能力（builtin core） | 替代采集器的哪一部分 | 关键 Schema / 备注 |
| --- | --- | --- |
| `workspace.registry.resolve` | `repo_path`/`project` 推断 | 入参 `workspace_root`，返回 resolution(strategy/confidence)、祖先/子项目/同级、工程上下文 |
| `workspace.registry.context.get` | 项目上下文索引 | 入参 `workspace_root`，返回 ≤500 字 brief（项目名、技术栈、文档/规则位置） |
| `workspace.registry.project.list/upsert/path.bind/delete` | work-log 自带的项目路径登记表 | 完整的项目树与路径绑定维护 |
| `workspace.registry.candidate.list/accept/dismiss`、`validate` | `project.autofill` 的路径候选/冲突处理 | 候选收件箱 + 冲突校验（`PATH_CONFLICT`） |
| `workspace.registry.import` | 存量数据迁移桥 | 入参 `source_path`（work-log `projects.json` 绝对路径）+ `dry_run`（默认 true） |
| `extension.workspace.bind/current/clear` | 会话归属识别 | 入参仅需 `workspace_root`；按会话累加，是"会话↔工作区"权威来源 |
| `engineering.project.resolve` | 工程上下文补充 | 读 `.himind/project.json`（项目/展馆/环境/默认 Workflow） |
| `ai.client.list/status` | `session.sources` 的"客户端探测"用途（部分） | 只探测客户端与接入状态，不读会话 |
| `skill.run` / `run.get` / `run.list` | Workflow 中 WL-SUMMARIZE 的内置 AI 步骤 | 一次性 AI 运行并落 `skill-runs` 记录 |
| `workbench.context.resolve` / `project.context.get` | 业务侧项目/展项消歧（可选） | control_plane，需工作台；按项目名/IP 解析稳定业务实体 |

> 遗留问题：`skills/workspace-context/skill.json` 把这些能力声明为插件 `com.himind.workspace-registry` 提供，但最新提交（34bfe2a）的说明已承认该插件"不在本仓、未发布"。实测这些能力是 **Agent builtin core**。迁移时应直接按内置 capability 依赖，去掉那个插件 ID，否则 release-plan 会被虚假依赖卡住。

---

## 4. 迁移目标与原则

1. **单一写入者不变**：工作日志库仍由 `com.himind.work-log` 独占写入（`work-log.entry.upsert`），Agent 侧能力只提供上下文与归类。
2. **归类权威收敛到内核**：项目名/路径绑定以 `workspace.registry` 为权威，work-log 登记表在过渡期只读兼容、不再新增维护入口。
3. **存量数据不丢**：用 `workspace.registry.import` 先 `dry_run` 预览再落盘，保证现有 `projects.json` 平滑迁入。
4. **可回退、可分阶段**：每个阶段独立可验收；P2/P3 之前保持采集器可用，随时可中止回退。
5. **诚实面对缺口**：不把"当前会话内联生成"包装成"历史采集的等价替代"，缺口作为待决策项显式列出。

---

## 5. 目标架构

```
                    ┌──────────────────────────────────────┐
                    │  Agent 侧（builtin core）             │
                    │                                      │
   当前会话上下文 ───►  extension.workspace.current/bind    │
   （agent 天然可见）  workspace.registry.resolve/context    │  → 规范项目名 + 工程上下文 brief
                    │  engineering.project.resolve          │
                    │  skill.run（可选，AI 总结）           │
                    └──────────────┬───────────────────────┘
                                   │ 结构化 entry（项目/功能/摘要/工时）
                                   ▼
                    ┌──────────────────────────────────────┐
                    │  com.himind.work-log（保留）          │
                    │  entry.upsert → report.render/aggregate│
                    │  （唯一日志库写入者）                  │
                    └──────────────────────────────────────┘

   一次性：workspace.registry.import { source_path: <log_root>/projects.json, dry_run }
```

与现状相比，被摘掉的是"扫描各客户端私有会话文件 → 生成 `session-digest`"这一段；保留的是"归类 → 写入 → 报告"。

---

## 6. 分阶段实施方案

### P0 · 存量数据迁移（低风险，可独立先行）

- 调 `workspace.registry.import { "source_path": "<日志根>/projects.json", "dry_run": true }` 预览迁移计划。
- 核对计划无误后，`dry_run: false` 落盘。
- 调 `workspace.registry.validate` 检查父子环、别名冲突、重复绑定、失效路径。
- **验收**：内核 `workspace.registry.project.list` 能看到全部存量项目与路径绑定，except 冲突项已人工收敛。
- **回退**：内核登记表独立存储，work-log `projects.json` 原样保留，无需回退。

### P1 · 归类逻辑切换到 Agent 侧

- `skills/work-log/SKILL.md`：写入前新增一步"用 `workspace.registry.context.get` / `resolve` 拿规范项目名"，替换原"由插件按登记表归一"的描述；明确 `project` 用内核返回的规范名。
- `skills/workspace-context/skill.json`：把 `provider: com.himind.workspace-registry` 的四个 capability 依赖改为内置能力依赖，删除 `plugin_dependencies` 里的虚假插件。
- work-log 插件中依赖采集素材的能力进入"废弃预告"：
  - `work-log.project.autofill`（数据源是采集器落盘的 `session-digest`）→ 标记废弃，UI"自动填充路径"按钮下架。
  - `work-log.entry.reconcile`（读 `session-digest-*.json` 对账）→ 标记废弃。
  - `work-log.source.list/save`（`config/sources.json` 只服务采集器）→ 标记废弃。
- **验收**：新路径写入日志时，项目名取自内核 registry；旧能力在废弃预告期仍可用。
- **回退**：skill 描述回退即可，插件能力未删。

### P2 · 工作流改造（`daily-work-log`）

- 删除 WL-COLLECT 步（`work-log.session.scan`）与 artifact `session-digest`。
- WL-SUMMARIZE 改为基于运行输入的工作事实 + 内核上下文总结，不再依赖 `input_artifacts: ["session-digest"]`；`base_step` 引用同步清理。
- `workflow.json`：`capabilities` 去掉 `work-log.session.scan`；`dependencies.plugins` 去掉 `com.himind.work-log-collector`；`exits` 去掉 `collected`；`entrypoints` 去掉 `collect`；`release_notes`/`version`/`description` 更新。
- `.himind/catalog.json`：移除该 workflow 对采集器的 `dependencies`、`extension_lock.dependencies`、`capability_ids` 中的 `work-log.session.scan`。
- **验收**：workflow candidate 校验通过，依赖锁不含采集器；启动前检查不再要求 `work-log.session.scan`。
- **回退**：保留上一版 `.hmwf` 候选即可恢复。

### P3 · 采集器与下游代码清理

- 删除目录 `plugins/work-log-collector/`（含 `main.go`、`generic.go`、`claude_desktop.go`、测试、`bin/`、`dist/`、`assets/`）。
- work-log 插件移除 P1 中标记废弃的 3 组能力及其 handler、入参字段（`MaterialRoot`、`Sources`/`SourceID`）、UI 按钮与维护弹窗里的"重扫对账"，并清理对 `session-digest` 的读取逻辑（`manage.go` 的 `sessionMaterialDir`/`projectAutofillCap`、`maintenance.go` 的 `entryReconcileCap`、`sources_config.go`）。相应单测同步更新。
- **验收**：`go test ./...` 通过；`work-log` 能力清单不再含 session/source/reconcile/autofill 相关项。
- **回退**：从 git 恢复目录（回退代价较高，故放在 P2 验收之后）。

### P4 · 登记、目录与发布一致性清理

- `extensions.json`：删除采集器扩展登记项；feature `com.himind.feature.work-log.plugin_ids` 去掉采集器。
- `.himind/catalog.json`：删除采集器插件条目（约 L422-L460）及其在各类 artifact_id / lock 中的引用。
- `design/extension-icon-concepts/mapping.json`、`ICON_MAP.md`：删除采集器图标条目。
- 文档：`skills/work-log/SKILL.md` §采集来源、`workflows/daily-work-log/README.md` 的来源/边界章节、`docs/work-log-ui-review.md` —— 删除采集器相关描述，改为"项目归类走 Agent 工作区能力；日志仅基于当前会话事实生成"。
- 版本对齐：当前 `plugins/work-log/plugin.json` 已是 0.17.0，而 catalog 记录为 0.15.0，发布前必须对齐，否则依赖锁会指向错误制品。
- 走发布流程：`extension.plugin.validate` → `candidate.save/test/confirm` → `submission.submit`（workflow、skill 同理），刷新依赖锁。
- **验收**：release-plan 无阻塞；catalog 各版本号与 manifest 一致；新装用户不再需要采集器依赖。

---

## 7. 变更清单（按文件）

| 文件 | 动作 | 阶段 |
| --- | --- | --- |
| `docs/work-log-agent-side-migration-plan.md` | 新增（本文档） | 现在 |
| `skills/workspace-context/skill.json` | 依赖改为内置能力，删虚假插件依赖 | P1 |
| `skills/work-log/SKILL.md` | 归类改走内核 registry；重写 §采集来源 | P1/P4 |
| `plugins/work-log/{manage.go,maintenance.go,sources_config.go,main.go}` | 清理 session/source/reconcile/autofill 能力与字段 | P3 |
| `plugins/work-log/plugin.json` | 删 3 组废弃能力、UI 命令；版本对齐 | P3/P4 |
| `plugins/work-log/ui/{index.html,app.js,app.css}` | 下架相关按钮与弹窗 | P3 |
| `plugins/work-log/main_test.go` 等测试 | 同步更新断言 | P3 |
| `plugins/work-log-collector/**` | 整目录删除 | P3 |
| `workflows/daily-work-log/workflow.json` | 删 WL-COLLECT、session-digest、采集器依赖 | P2 |
| `workflows/daily-work-log/README.md` | 重写来源/边界章节 | P4 |
| `extensions.json` | 删采集器登记与 feature 引用 | P4 |
| `.himind/catalog.json` | 删采集器条目与所有依赖引用；版本对齐 | P2/P4 |
| `design/extension-icon-concepts/{mapping.json,ICON_MAP.md}` | 删采集器图标条目 | P4 |

---

## 8. 数据迁移细节

- work-log 项目表实际文件：`<日志根>/projects.json`（见 `plugins/work-log/projects.go: projectsPath(root)`）。
- 迁移调用：
  ```
  workspace.registry.import  { "source_path": "F:\\...\\HiMind\\工作日志\\projects.json", "dry_run": true }
  ```
  确认计划后去掉 `dry_run`。该能力风险级别 `local_write`、幂等不保证、keyed 并发，因此**只执行一次**，成功后再校验。
- 双写期权威约定：以 `workspace.registry` 为准；work-log 侧 `work-log.project.resolve/claim/path.set` 等仅作只读兼容，不再作为新入口推广。
- 迁移后校验：`workspace.registry.project.list` + `workspace.registry.validate`。

---

## 9. 风险、缺口与待确认事项

### 9.1 净能力缺口（必须正面决策）

废弃采集器后，以下能力将**无 Agent 侧替代**（见 §2 结论）：

| 丢失的能力 | 影响 |
| --- | --- |
| 跨客户端历史会话正文采集 | 只能记录"当前这次会话"已完成的工作，事后补记、跨客户端汇总、每日定时全量采集明显变弱 |
| `measured` 工时（按消息时间戳实测） | 工时退化为 `manual`/`estimated` 或留空，统计精度下降 |
| 声明式通用来源（Cursor/Gemini CLI/WorkBuddy 等） | 全部失效，无替代 |
| `session-digest` artifact | AI 总结不再有确定性素材输入，标题/摘要稳定性下降 |

### 9.2 素材来源的三种路线（待拍板）

| 路线 | 说明 | 取舍 |
| --- | --- | --- |
| A. 当前会话内联提取（推荐） | 以 `skills/work-log` 现有模式为准：基于当前会话上下文生成日志，归类走 `workspace.registry` | 纯 Agent 侧、干净；接受历史采集与 measured 工时的损失 |
| B. 下沉到内核 | 向 Agent 内核提需求新增"会话记录读取"能力，本仓只做消费方 | 跨客户端覆盖不丢；受内核排期约束，本仓改造需分期等待 |
| C. 混合过渡 | 日常走 A；历史/补记场景暂留精简采集入口，内核就绪后再删 | 平滑但拖长废弃周期，P3/P4 需拆成两段 |

> 本方案默认按 **路线 A** 撰写（P1-P4）。若选 B 或 C，P2/P3 需要相应调整或延后。

### 9.3 其他风险

- **发布一致性**：catalog 中 work-log 为 0.15.0、manifest 为 0.17.0，当前已不一致；迁移放大该问题，必须在 P4 对齐。
- **回归面广**：采集器的删除牵动 workflow、skill、plugin、catalog、icons、docs 七类文件，建议严格按 P0→P4 顺序推进，每阶段独立验收。
- **用户习惯**：依赖"自动采集历史会话"的用户会感到功能退化，需在 release_notes 中明确说明与给出替代用法。

---

## 10. 验收标准

1. `workspace.registry.import` 迁移计划干净，内核登记表包含全部存量项目/路径，`validate` 无致命冲突。
2. 新写入的日志条目 `project` 来自内核 registry 规范名，不再出现仅凭目录名推断的项目。
3. `workflows/daily-work-log` 候选校验通过，依赖锁与能力清单不含 `work-log.session.scan` / 采集器插件。
4. `go test ./...` 全绿；work-log 能力清单无 session/source/reconcile/autofill 残留。
5. `extensions.json` / `.himind/catalog.json` / manifest 三处版本号与依赖关系一致，release-plan 无阻塞。
6. 文档与技能不再声称"自动采集各客户端历史会话"。

---

## 11. 附录：核查方法与证据

- 全量能力枚举：分页调用 `system.capability.search`（limit=100，跟随 nextCursor），共 472 项；来源分布包含 builtin(173)、builtin:himind-media(24)、dashboard:catalog(6)、plugin:*(269)。
- 逐项 `describe` 核实的"疑似会话/工作记录"能力：
  - `workbench.work.summary.get` → 工作台业务摘要，非会话
  - `workbench.context.resolve` / `project.context.get` / `exhibit.context.get` → 业务实体解析，非会话
  - `workbench.knowledge.search` → 知识检索，非会话
  - `document.inspect` → 文档正文提取（DOCX/TXT/MD），非会话
  - `ai.client.list/status` → 客户端接入检测，明确不读会话
  - `extension.workspace.bind/current/clear` → 会话↔工作区归属，无正文
  - `workflow.run.*` / `skill.run.*` / `mission.events.list` → Agent 自身运行记录，非客户端会话
  - 通用文件读取能力：搜索"读取文件/文件内容/read file content/读取文本"均返回空（仅图片读取），确认无通用文本读取能力
- 采集器自身能力清单即全量目录中唯一的 `work-log.session.*`：`work-log.session.scan`、`work-log.session.sources`（source = `plugin:com.himind.work-log-collector`）。
