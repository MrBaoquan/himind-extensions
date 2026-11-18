# 工作流创作

1. 确定 `execution_policy`：`strict` 完整运行，`segmented` 从固定阶段入口运行，`flexible` 使用声明的入口和出口。用 `extension.workflow.scaffold` 创建工程。当前内置工具生成的是占位骨架，需核对模板、版本、最低 Agent 版本、更新说明和分发目标是否真正写入清单。
2. 在 `workflow.json` 声明步骤依赖、入口出口和失败行为；采集、写入和发布失败默认终止。Runtime Step 声明 provider、prompt 和工作区，纯推理设置 `runtime.tool_policy: "none"`。Loop 声明最大次数和继续或退出条件。需审批时设置 `approval_required`；凭据通过 `credential_handles` 引用。使用 Candidate 时声明唯一冻结动作与不可变 Artifact；Artifact 明确 Schema、校验方式、大小限制。`ui.entry` 指向真实的 `workflow_view.v1` 文件。
3. 调用 `extension.workflow.validate` 和 `extension.workflow.build` 检查契约；`extension.workflow.package` 可导出 `.hmwf`。`extension.workflow.candidate.save` 从 `source_root` 冻结候选，再执行共用候选测试。记录依赖锁、预检警告和发布阻塞。静态检查、替身和真实执行分别报告；针对关键交付、审批及失败行为运行验收用例，环境不具备就标记未验证。
4. 按共用流程获得提审授权后，依次调用 `extension.workflow.candidate.confirm` 和 `extension.workflow.submission.submit`；提交成功后才查询 `extension.workflow.submission.status`。
