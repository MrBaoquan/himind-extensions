# 模板与状态字段

复制即用。所有模板都保持在长度上限内：契约卡 ≤40 行，`task.md` ≤80 行，`state.json` ≤60 行，`handoff.md` ≤30 行。

## 1. 任务契约卡（立项阶段唯一输出）

```markdown
## 任务契约：<一句话标题>
状态：待确认 · 预算：<N> 轮 / <N> 小时 · 授权：<范围>

### 目标结果
<完成后可观察到的结果，一句话>

### 验收（逐条可验证）
- [ ] A1 <标准>            （命令 / 文件 / 外部 / 人工）
- [ ] A2 <标准>            （命令）
- [ ] A3 <标准>            （人工，需你确认）

### 范围与非目标
做：<路径、模块或交付物>
不做：<明确排除项>

### 已决（沿用讨论结论，执行期不再重开）
- D1 <结论> —— <理由>
- D2 <结论> —— <理由>

### 未决（按假设推进，冲突再回报）
- Q1 <问题> → 假设：<默认取法>

### 执行期规则
每轮推进一个可验证步；每 <3–5> 轮汇报一次；
只在「需要决策的阻塞 / 需超授权 / 目标矛盾 / 不可逆风险」时打断你；
停止条件：验收全绿 / 连续 2 轮无进展 / 预算耗尽。

回复「确认」开始，或指出要改的条目。
```

## 2. task.md

```markdown
# <任务标题>

## 目标
<可观察结果>

## 验收
- [ ] A1 <标准>（验证方式）
- [ ] A2 <标准>（验证方式）

## 范围
- 做：<...>
- 不做：<...>

## 已决
- D1 <结论>（理由：<...>）

## 未决与假设
- Q1 <问题> → 假设：<...>

## 授权
- 允许：只读；工作区内写
- 需确认：工作区外写、删除、提交、推送、发布、外发、付费

## 预算与停止
- 轮次上限：40
- 时长上限：240 分钟
- 停止规则：见 SKILL.md「停止与阻塞」
```

## 3. state.json

| 字段 | 含义 |
|---|---|
| `task_id` | 目录名，如 `20261020-auth-strategy` |
| `title` | 的一句话标题 |
| `status` | `pending_confirmation` / `active` / `paused` / `blocked` / `done` / `stopped` |
| `round` | 已完成的轮次数 |
| `phase` | 当前阶段，如 `explore` / `implement` / `verify` / `review` |
| `goal` | 目标摘要 |
| `acceptance` | `[{id, text, verify, done}]`，`verify` 取 `command` / `file` / `external` / `manual` |
| `next_action` | 下一轮的具体动作 |
| `blockers` | `[{reason, rounds, needs}]` |
| `no_progress_rounds` | 连续无进展轮数 |
| `budget` | `{max_rounds, max_minutes}` |
| `capabilities` | `{file_io, shell, auto_continue, background_job, subagent}` 布尔值 |
| `rollback_point` | 最近回滚点（提交号或备份路径） |
| `confirmed_at` / `updated_at` | 时间戳 |

示例：

```json
{
  "task_id": "20261020-auth-strategy",
  "title": "auth 模块改策略模式",
  "status": "active",
  "round": 3,
  "phase": "implement",
  "goal": "auth 拆成 strategy，测试全绿，行为不变",
  "acceptance": [
    {"id": "A1", "text": "测试全绿", "verify": "command", "done": true},
    {"id": "A2", "text": "无新增 lint error", "verify": "command", "done": false},
    {"id": "A3", "text": "登录/登出/刷新手工验证", "verify": "manual", "done": false}
  ],
  "next_action": "修掉 3 个 lint 错误",
  "blockers": [],
  "no_progress_rounds": 0,
  "budget": {"max_rounds": 40, "max_minutes": 240},
  "capabilities": {"file_io": true, "shell": true, "auto_continue": false, "background_job": false, "subagent": false},
  "rollback_point": "git:9f2c1ab",
  "confirmed_at": "2026-10-20T10:40:00+08:00",
  "updated_at": "2026-10-20T11:12:00+08:00"
}
```

## 4. progress.md 条目

```markdown
## 第 N 轮 · <阶段> · <时间>
- 目标：<本轮 next_action>
- 动作：<做了什么，关键文件与命令>
- 证据：evidence/<文件> 的关键行，或一行结论
- 结果：通过 / 失败（<原因>）
- 下一步：<下一轮 next_action>
```

## 5. decisions.md 条目

```markdown
- D<n> <决策内容>：<理由>；影响：<范围>；记录于第 <N> 轮
```

## 6. handoff.md（≤30 行）

```markdown
# 交接卡 · <task-id>
状态：<status> · 第 <N> 轮 · 阶段 <phase>
目标：<一句话>
已完成：A1、A2（证据见 progress.md）
下一步：<next_action>
阻塞/假设：<...>
回滚点：<...>
续跑：说「继续长任务 <task-id>」
```

## 7. 续跑块（L2/L3 轮次结束输出）

```markdown
[长任务] <task-id> · 第 N 轮 · <phase>
进度：<x/y> 验收项 · 本轮证据：<一行>
下一步：<next_action>
继续请说：「继续长任务 <task-id>」
```

## 8. 停止报告

```markdown
## 长任务 <task-id> 已停止：<状态>
原因：<一句话>

### 验收
| 编号 | 标准 | 状态 | 证据 |
|---|---|---|---|
| A1 | <...> | 通过 | evidence/... |

### 变更
<文件清单>

### 回滚点
<提交号或备份路径>

### 遗留
- 未完成：<...>
- 阻塞：<需要用户提供什么>
- 假设：<未决项当前取法>
```
