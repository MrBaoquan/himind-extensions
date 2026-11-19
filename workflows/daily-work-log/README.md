# 每日工作日志

版本 0.10.4。工作日志插件 0.21.4 直接通过 Agent 会话与工作区服务生成素材，不再依赖独立采集器。

流程：`work-log.material.collect` → 只读 AI 总结 → `work-log.entry.upsert` → 周/月报 → 日报。项目身份和目录归属由 Agent 工作区统一管理；工作流只读取项目树，不维护项目、路径或层级。

运行输入包括 `workspace_root`、`log_root`、`date` 或 `from/to`、`timezone`、`sources`、`project_id`、`include_children` 和 `strict`。个别来源超时时，保留其他来源的会话并记录覆盖缺口。`strict=true` 时来源不完整则停止。缺省回看两天；指定活动日时报告归到该日，多日条目保留各自日期。

采集保留完整 `session-digest` 和确定性候选 `entries`。AI 使用 `session-summary-input`，按最近活动优先汇总最多 32 条会话，过滤会话启动上下文并保留多条有效请求，素材限制在 12 KB 并记录省略条数。时间、估算、工作区 ID 和证据以采集记录为准，锁定条目跳过自动覆盖。

客户端格式统一由 Agent 只读适配；来源配置与可用性由 Agent 报告。日志没有 legacy backend 或客户端私有文件回退。

需要 Agent 会话服务和内置 AI Runtime；真实 Runtime 或模型未就绪时应报告，不把静态合同通过当作 AI 总结已执行。

日报优先展示已观测的 Agent 累计作业。同一会话内合并重叠执行区间，独立会话分别累计。消息活动估算和人工工时保留各自口径。
