# 团队工作流迁移与契约交付

负责人：本轮 workflow worker。状态：实现与定向验证完成，等待父任务整合。通用仓基线 `f79496f84089ab8933e1c6d317b5f55accf45e86`；Agent 基线 `4025ba953a8d72ccaddf2eb9382e79c8fa7c4495`。其他 worker 的未提交修改保留。

## 真实写集

- `workflows/software-delivery/`：完整迁入 3 文件，保留版本 `1.1.1`。
- `workflows/ui-craft-team/`：完整迁入 8 文件；副本版本提升至 `1.2.1`，删除固定读屏要求，验收以用户确认范围为准；读屏和 200% 缩放不作为固定门禁。
- `extensions.json`：在专家 worker 释放后仅追加上述两条 workflow 登记，保留其专家与技能登记改动。
- `tooling/workflowproject/workflow.go`、`team.go`、`team_test.go`：角色、角色标注、委派分支及默认并行策略；同步角色 optional/model、Runtime 模型/权限/files_only、网络约束三态、Loop 上限、能力候选字段。校验 provider 声明、模型路由、分支结构和 schema 资产；拒绝未知执行字段。打包前执行同一校验，非法工程不能覆盖已有制品。
- Agent `contracts/agent-core/v1/workflow-package.schema.json` 及 `tests/test_workflow_package_schema.py`：同步现行 Rust 已支持的字段并补 schema 正反例。

迁入时逐文件 SHA-256 与 Agent 原包一致，随后仅修改通用 UI 副本。Agent 顶层示例转 fixtures 及 Rust 测试路径调整由父任务负责。

## 实际归属

现役 Go workflow 代码是本仓 `tooling/workflowproject`；`tooling/commands/repo_check.go` 与 `workflows/video-to-motion/tests/contract` 直接调用它。命令入口位于 `tools/cmd/`，本仓没有 `cmd/`。

Agent 当前 `extension.workflow.*` 由 `src/extension_toolchain.rs` 调用原生 `extension_scaffold`；邻接 `agent-plugins/tooling` 没有另一份现役 workflow Go 模型。本轮未从归档恢复任何实现。

## 本轮验证

- `go test -json ./tooling/... ./tools/cmd/...`：退出 0；203 个测试及子测试通过、0 失败、1 跳过；14 个含测试包通过，9 个命令包无测试。包括 expert、skill、workflow 校验器。跳过项为 `packagedigest/TestPayloadDigestOfRealArtifact`。日志：本机临时目录 `himind-workflow-tooling-checks.jsonl`。父任务无需重复此组检查。
- `go test ./tooling/workflowproject ./tooling/commands ./tooling/catalog ./tooling/distribution ./tooling/releaseplan ./workflows/video-to-motion/tests/contract`：全部通过；迁入包目录读取、临时归档打包与重验、全文件 payload 比对均通过；6 个原有登记 workflow 继续通过。
- Agent `python contracts/agent-core/v1/tests/test_workflow_package_schema.py`：6 组测试通过。使用本机已有 `jsonschema`，无依赖安装。
- 两个迁入包及现有 `software-release` 通过更新的 workflow schema；UI 包内 5 份结果 schema 通过元校验。
- 变更文件执行 `git -c core.whitespace=cr-at-eol diff --check` 通过；保留 Agent schema 原有 CRLF。

## 未解决的其他写集问题

- `go run ./tools/cmd/himind-repo-check` 失败：未跟踪的 `plugins/media-studio` 未登记到 `extensions.json`。未改媒体目录或代为登记；由父任务协调该目录负责人。
- 按贡献指南运行 `go test ./...` 后，媒体模块 3 项失败：`internal/mediadownload/TestTransferCollectsOnlyFinalMediaAndItsSubtitles`、`plugins/media-library/TestImageEditorImmutableVersionsReplayAndAdoption`、`plugins/media-library/TestAudioCompletedReceiptRecoversIntoLibraryWithProvenance`。其他包完成；没有修改这些模块，未将本轮整体标为全绿。
- 未运行 cargo、真实模型任务、读屏、200% 缩放、部署或 push；未修改 Rust、构建脚本、媒体目录或其他 worker 的源码。
