---
name: dev-workflow-orchestrator
description: 协调 DSH、外部 AI 工具、工程开发会话与 Workflow 生命周期。用户要求选择工程或展馆、持续开发、创建开发检查点、从指定阶段启动 Workflow、查询运行状态、提交开发反馈、取消运行或把开发结果交给交付流程时使用。
---

# 工程工作流编排

把对话式开发与确定性 Workflow 连接起来。不要用对话记录代替项目、检查点、Run 和 Artifact 事实。

## 基本原则

1. 先解析工程，再选择目标展馆、环境和 Workflow。
2. 编辑工作区前获取 Write Lease，编辑完成后释放。
3. 需要交付时先生成 DevelopmentCheckpoint，再启动 Workflow。
4. Workflow Run 必须使用稳定 `workflow_id` 和结构化 `input`。
5. 审批只由全局审批中心处理，不得代替用户批准。
6. Run 启动是异步的，调用后通过 `workflow.run.get` 跟踪状态。

## 推荐流程

1. 调用 `engineering.project.resolve`，参数至少包含 `workspace_root`，并提供用户提到的 `target` 和 `environment`。后续 Workflow 必须使用解析结果中的 `target.source_root` 作为代码目录，使用 `target.build_root` 作为小程序构建产物目录。
2. 如果只是修改、测试或排查，调用 `engineering.workspace.lease.acquire` 获取 `mode: write` 的 Lease。
3. 完成开发后调用 `engineering.checkpoint.create`，传入项目、目标和环境，并在 `created_by` 中携带当前客户端、Session 和 Lease ID。
4. 调用 `engineering.workspace.lease.release` 释放 Lease。
5. 调用 `workflow.catalog.list` 或 `workflow.catalog.describe` 选择交付 Workflow。
6. 从任意阶段启动时，传入：

```json
{
  "workflow_id": "com.himind.workflow.wechat-experience-upload",
  "input": {
    "workspace_root": "F:\\WebProjects\\kerun_user",
    "project_id": "kerun-user",
    "target_id": "szkjg",
    "environment": "development",
    "source_root": "F:\\WebProjects\\kerun_user",
    "project_root": "F:\\WebProjects\\kerun_user\\dist\\wx"
  },
  "execution": {
    "entrypoint": "upload",
    "exitpoint": "experience_version",
    "seed_artifacts": ["development-checkpoint"]
  }
}
```

7. 调用 `workflow.run.start`。返回 `accepted=true` 后不得等待同步完成。
8. 需要状态时调用 `workflow.run.get`，展示总状态、当前步骤、等待类型、审批 ID、Artifact 和错误。
9. 只有 Run 明确处于等待反馈时，才调用 `workflow.run.feedback`。
10. 用户明确要求停止时调用 `workflow.run.cancel`。

## 固定流程

`execution_policy=strict` 的 Workflow 不允许选择入口或出口。必须从完整流程启动，并原样遵守构建、Candidate、审批和发布的强制门禁。

## 交付结果

向用户返回：

- Workflow ID、Run ID 和最终状态。
- Project、Target 和 Environment。
- Candidate Commit SHA 和 Tree Digest。
- 体验版、审核材料或发布 Artifact。
- 等待审批时提供 Approval ID，并提示在统一审批中心处理。
- 失败时返回结构化错误和可继续开发的 DevelopmentCheckpoint。
