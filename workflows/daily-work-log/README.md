# 每日工作日志

版本 0.9.1。工作日志插件 0.19.1 直接通过 Agent 会话与工作区服务生成素材，不再依赖独立采集器。

流程：可选业务项目读取 → `work-log.material.collect` → 只读 AI 总结 → `work-log.entry.upsert` → 周/月报 → 日报。

运行输入包括 `workspace_root`、`log_root`、`date` 或 `from/to`、`timezone`、`sources`、`workspace_project_id`、`include_children` 和 `strict`。`strict=true` 时来源不完整则停止。缺省回看两天；指定活动日时报告归到该日，多日条目保留各自日期。

采集提供 `session-digest` 和确定性候选 `entries`，AI 只补标题、摘要、功能与结论；时间、估算、工作区 ID 和证据以素材为准，锁定条目跳过自动覆盖。

客户端格式统一由 Agent 只读适配；来源配置与可用性由 Agent 报告。日志没有 legacy backend 或客户端私有文件回退。

需要 Agent 会话服务和内置 AI Runtime；真实 Runtime 或模型未就绪时应报告，不把静态合同通过当作 AI 总结已执行。
