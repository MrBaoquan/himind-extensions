# 工作日志直接消费 Agent 会话服务

2026-11-18 更新：独立 `com.himind.work-log-collector` 退役。当前最终候选为 work-log 0.19.1、工作日志技能 0.7.2、daily-work-log 0.9.1，要求包含本轮会话服务改动的 Agent 0.3.92 开发构建。仅比较版本字符串不足以识别已安装版是否包含这些改动，应检查能力版本和构建哈希。

调用链：`daily-work-log → work-log.material.collect → scoped broker → workspace.session.sources/activity → Agent 客户端适配器`。工作日志插件不再解析客户端私有 JSON、JSONL 或 SQLite，也没有 legacy 回退。Agent 服务不可用时明确失败，不以空日志冒充成功。

## 归属与配置

Agent 负责客户端发现、原生会话身份、工作区登记归属、会话列表、正文分页、消息时间和证据。原有 DSH、ACP、Claude Code、Codex、OpenCode 与新增客户端使用同一服务。新增客户端预设包括 Claude Desktop、Copilot、VS Code Chat、WorkBuddy 等；有声明式 JSON 来源支持。预设存在不等于每种客户端均完成真实环境验收，验收结果单独记录。

`workspace.session.source.get/configure` 管理 Agent 私有 `data/session-sources.json`，支持来源映射和原生客户端根目录覆盖。工作日志原 `source.list/save` 是委托门面。旧采集器的 sources 配置需要按新契约导入；不自动读取已退役插件的数据目录。旧 `project.autofill` 入口已移除，路径登记和项目从属统一由 Agent 工作区注册表管理。

工作日志负责隐私过滤、素材压缩、业务项目/展项分类、AI 整理、人工修订、日志和报表。`workspace_project_id` 与业务 `project_id/exhibit_id` 各自保持原有身份，不互相赋值。

## 身份、分页和完整性

日志幂等键是 `source + native session_id + 自然日`。Agent 的 `claude_code` 在日志中沿用 `claude-code`，兼容旧日志。缺消息时间不以文件修改时间补造日期。跨日按请求时区拆分。

Agent 活动游标引用有期限的元数据快照，正文继续读取原客户端；不建第二份正文库。页边界固定，追加消息不应影响已经开始的分页；原页正文改写或工作区登记变化拒绝旧游标。元数据快照仅缓存 15 分钟，最多 32 份。

`complete=false`、跳过原因、截断和来源状态必须保留。严格模式遇到不完整素材停止。对账按来源、原生 ID、日期匹配；只有完整、全范围、无隐私筛选且来源可用的覆盖，才会把缺失条目列入 stale，其余列入 unverified。对账仅供预览，不删除日志。

隐私配置读取失败即停止。路径/关键词过滤先于持久化和工时分配。关键词排除遇到正文截断时保守排除整条。素材上限 4 MiB，每次最多 240 次 broker 调用和 20,000 条消息；超出则要求缩小日期或工作区范围。

## AI 与工时

AI 只补充人类可读字段。`base_step` 保留素材的来源、原生 ID、活动日、工作区归属、消息证据及工时方法，AI 不可改写这些确定性字段。锁定记录不被补跑覆盖。

工时是相邻消息活动区间估算，方法为 `message_intervals_v1`，排除长空闲，对本批选定会话扣除重叠；零时间证据不按轮次补造分钟。它不能证明实际在岗或人类投入。人工工时保留 manual 口径。

## 退役与交付

采集器不再列入活动源码、扩展清单或能力包。`retired_plugin_ids` 让目录同步忽略历史采集器 Release。历史制品和旧工作流锁不改写；升级时需要安装新工作日志与工作流，并卸载旧采集器。源码及本机原始证据保存在忽略目录中，不打入发布包。

本轮验收证据和具体限制见 [退役验证报告](work-log-collector-retirement-20261118.md)。此前 [真实补采报告](work-log-real-backfill-20261118.md) 属于兼容采集器阶段，不代表本轮 Agent 直连验收。
