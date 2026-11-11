---
name: workspace-context
description: 判断当前目录属于哪个项目，按需把新路径登记进本机工作区表，并给出上级、同级与工程上下文索引。用户提到目录或项目名、切换仓库、写工作日志或启动交付流程时使用。
---

# 工作区上下文助手

把「这个路径是什么项目、它属于谁、该用什么命令」问清楚，并只在合适的时机把新事实登记下来。本 Skill 只做索引与确认，不改代码、不改项目规则、不改变任何会话权限。

## 工作边界

1. **当前目录不是靠猜的。** 会话的系统提示里有 `Current working directory: "<绝对路径>"`，直接用它。不要读环境变量，也不要在 skill 正文里假设工作区位置。
2. **登记不改变权限。** 登记表只回答「路径属于哪个项目」。把它当成「写完就能改这个目录」是错的：跨目录改代码要在 Agent 里为该目录单独开会话。
3. **一个路径只属于一个项目。** 出现 `PATH_CONFLICT` 就停下来问用户，不要自己挑一个。
4. **未命中就未命中。** 不要为了「有个结果」临时造一个项目。候选是建议，登记是决定。
5. 不写 `AGENTS.md`、不改项目规则文件、不删除登记表里的项目。

## 什么时候调用

在以下时机调用 `workspace.registry.context.get`（或 `workspace.registry.resolve`）：

- 任务开始时，尤其是「继续上次那个」这类没有明说路径的请求。
- 用户第一次给出绝对路径、项目名或别名时。
- 话题从 A 项目切到 B 项目时。
- 写工作日志、启动开发或交付工作流之前。

其他时候**不要**调用。每轮都解析一次只会挤占工具面，也让回答变啰嗦。

## 决策阶梯

先解析，再按命中的档位决定动作。**不要跳过档位直接建项目。**

| 档位 | 条件 | 动作 | 要问用户吗 |
| --- | --- | --- | --- |
| L0 复用 | 命中已登记项目 | 直接用规范名与上下文，静默 | 否 |
| L1 记候选 | 未命中，且解析返回了候选 | 候选已自动落盘，回复里一句话提示「已记为候选」 | 否 |
| L2 自动绑定 | 候选置信度为 `exact`（别名或末段精确一致，且该路径未绑定） | 调 `workspace.registry.path.bind`，`source: session`、`confidence: exact` | 否 |
| L3 新建节点 | 需要新项目、改上级、合并、删除 | 先说明要做什么，得到同意再执行 | **是** |

`resolve` 返回的 `resolution` 字段说明它是怎么判的：

- `strategy: longest_prefix` 表示命中了某条路径绑定，再看命中记录里的 `confidence` 字段；
- `strategy: none` 表示没命中，`candidates` 里是建议；
- `strategy: conflict` 表示同一路径绑了多个项目，**必须**让用户在插件窗口里收敛。

## 调用方式

必须显式传 `workspace_root`，它是绝对路径：

```text
workspace.registry.context.get  { "workspace_root": "F:\\WebProjects\\himind-extensions\\plugins\\work-log" }
workspace.registry.resolve      { "workspace_root": "<绝对路径>" }
```

`workspace_root` 从系统提示的 `Current working directory` 取；用户明确给了别的路径时以用户为准。

返回的 `brief` 是一段 ≤500 字的索引：

```text
[工作区] F:\WebProjects\himind-extensions\plugins\work-log
[项目] 工作日志 (work-log) · 子项目 · active
[上级] 扩展仓库
[同级] 科技雷达、媒体库
[技术栈] Go / JSON-RPC
[文档] docs/work-log-project-board-design.md
[规则] AGENTS.md
[提示] 这是索引；执行命令前请读实际文件。
```

**`brief` 是索引，不是知识库。** 它给出文档与规则文件的位置，读不读、怎么用仍然要打开真实文件确认。

## 登记新项目

只有用户明确同意时才写入。一次会话最多自动登记一次，其余都要先问。

```text
workspace.registry.project.upsert {
  "name": "工作日志",
  "kind": "subproject",
  "aliases": ["work-log"],
  "parent_id": "<上级项目 ID>",
  "stack": ["Go", "JavaScript"],
  "docs": [{"label": "设计稿", "path": "docs/design.md"}],
  "rules": [{"label": "AGENTS", "path": "AGENTS.md"}]
}
workspace.registry.path.bind {
  "project_ref": "<项目 ID>",
  "path": "F:\\WebProjects\\himind-extensions\\plugins\\work-log",
  "role": "root",
  "source": "session",
  "confidence": "exact"
}
```

`parent_id` 传 `null` 表示提升为根项目；不传表示保持原值。别用空字符串表达「清空」。

`role` 只在明确是特殊目录时改成 `worktree` / `release` / `mirror`，默认 `root`。

## 错误码处理

按码分支，不要解析中文句子：

| 错误码 | 含义 | 你该做什么 |
| --- | --- | --- |
| `PATH_CONFLICT` | 这个路径已绑到别的项目 | 告诉用户冲突的两个项目，让他决定 |
| `PROJECT_NOT_FOUND` | 项目不存在或刚被删 | 重新 `project.list` 确认，别猜 ID |
| `PARENT_CYCLE` | 会让两个项目互为上下级 | 换一个上级 |
| `ALIAS_CONFLICT` | 别名已被占用 | 换别名，或先确认是否同一个项目 |
| `PATH_NOT_ABSOLUTE` | 传了相对路径 | 从当前目录推出绝对路径再传，或直接问用户 |
| `PROJECT_HAS_CHILDREN` / `PROJECT_HAS_WORKSPACES` | 删除被拒 | 说明还有子项目或路径，问用户是否强制 |

## 与其他扩展的分工

- **work-log** 记录「我做了什么、花了多久」；本扩展记录「这个路径属于谁」。写日志时用本扩展给出的规范项目名，避免 work-log 里出现重复项目。
- **`.himind/project.json`** 是单仓库工程清单（构建目标、环境）；本扩展是设备级项目树，两者互不替代。
- **登记表本身** 由插件窗口管理（项目树、路径绑定、候选收件箱）。需要人来做决定时，告诉用户去「工作区」视图。

## 完成标准

- 命中的项目名与用户认知一致；不一致就先问，不硬套。
- 没有自动创建用户没同意的项目。
- 没有把「已登记」说成「已获得写入权限」。
- 回复里只提一句登记结果，不复述整张 `brief`。
