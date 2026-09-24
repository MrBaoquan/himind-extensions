---
name: develop-himind-workflows
description: 开发 HiMind Agent Workflow：设计 Step DAG、入口出口、Loop、Candidate 和 Artifact Schema，校验、测试、打包 .hmwf 并受控提交审核。用户要求新建或修改工作流时使用。
---

# 工作流开发助手

通过 HiMind Agent 和 AI 扩展开发工具完成 Workflow 全流程。不要假设当前项目包含 HiMind Agent 源码、历史设计文档或已有 Workflow；空白目录也必须能独立创建、校验、测试和打包。

## 工作边界

1. 先调用 `extension.workspace.current` 确认当前扩展工作区。外部 AI 工具若返回 Agent 主目录、`source=process_current_dir` 或 `bound=false`，调用 `extension.workspace.bind` 绑定聚合仓库或单个 Workflow 目录，然后重新确认。
2. Workflow 描述跨步骤执行、审批、Artifact 和交付状态，不复制 Plugin 的实现，也不把一次性操作知识塞入 Workflow。重复使用的 AI 操作规则应放入独立 Skill，确定性外部能力应放入 Plugin 或 Connector。
3. 用户可见名称、步骤标题、说明和阻塞提示使用中文；Workflow ID、目录名、Step ID、Artifact ID 和 Capability ID 使用小写 ASCII。
4. 只在 `workspace_root` 内写文件。不得读取或输出 Token、Cookie、私钥、真实认证头或 Agent credential。
5. Workflow 源码统一位于聚合仓库的 `workflows/<slug>`。标准目录为 `workflow.json`、`README.md`、`schemas/`、`artifacts/`、`connectors/`、`examples/`、`tests/contract/` 和 `ui/workflow-view.json`。
6. 优先使用已安装且稳定发布的 Capability、Plugin、Skill、Connector 和 Runtime。不得自动安装缺失依赖，也不得为了通过测试伪造平台状态。
7. Workflow 的 `execution_policy` 必须明确：`strict` 只能完整启动并完整结束；`segmented` 按合法入口和出口执行固定阶段；`flexible` 允许从声明的任意入口开始并在任意合法出口结束。
8. Candidate、Loop、Approval 和 Artifact 是执行事实，不由 UI、对话记录或 Dashboard 状态代替。

## 命名与文案约束

长度按字符计，1 个汉字算 1 个字符。超过上限时脚手架和校验直接拒绝，建议值只作提示。

| 字段 | 建议 | 上限 |
| --- | --- | --- |
| 稳定 ID | 48 | 64 |
| 目录名 / ID 末段 | 28 | 32 |
| 显示名称 | 14 | 18 |
| 用途说明 `description` | 60 | 120 |
| `release_notes` | 60 | 120 |
| 步骤标题 | 12 | 16 |

名称用名词短语，只说是什么；说明写“做什么 + 什么时候用”一句，不复述需求、不列功能清单；更新说明只写本次变化；不用“一站式、全方位、赋能、助力”这类空词。阈值定义在 `tooling/metaguide`，超限报错会带上限和建议值。存量扩展已按上表对齐。

## 自动开发流程

1. 调用 `extension.workspace.current`，必要时调用 `extension.workspace.bind`。再调用 `extension.authoring.preflight`（`kind: workflow`）检查工作区、四件套、Agent 和运行模式。任一预检返回 `state: blocked` 时停止写入并原样返回结构化 `blockers` 与 `next_steps`。
2. 调用 `extension.environment.preflight`（`kind: workflow`），确认 Workflow 工具链可用。
3. 先确认业务场景，再选择模板：
   - `strict`：不可跳步的固定流程。
   - `segmented`：创建、开发、交付等稳定阶段，可从阶段入口开始。
   - `flexible`：允许从任意声明入口开始并在任意合法出口结束。
   - `development-loop`：修改、验证、用户反馈、再修改的多轮开发流程。
   - `capability-pipeline`：以 Capability 组合为主的确定性流程。
4. 调用 `extension.workflow.scaffold`，传入工作区内 `output_dir`、ASCII `slug`、稳定 `id`、中文名称和说明、当前作者、版本、`min_agent_version`、`release_notes` 和模板。创建后必须编辑生成工程，不能把占位步骤当成完成品。
5. 设计 `workflow.json`：
   - 为 Step 分配稳定且唯一的 ASCII ID，明确 `depends_on`。
   - Runtime Step 必须指定 `provider`、`prompt`、工作区来源和可选 `result_schema`。
   - 纯推理/生成类 Runtime Step 设置 `runtime.tool_policy: "none"`，只给模型必要上下文并禁止工具探索；需要读写仓库或联网检索的步骤保持 `default`。
   - Loop 必须设置 `max_iterations`，并至少声明 `continue_when` 或 `exit_when`。
   - 必须审批的步骤设置 `approval_required`，审批统一走 Agent 审批中心。
   - 只有结果可缺省的步骤（AI 解读、可选通知等）才设置 `on_failure: "continue"`；采集、写盘、发布这类步骤保持默认 `fail`，缺了事实必须终止。
   - Candidate 必须声明不可变 Artifact，并且恰好有一个 `candidate_action: freeze`。
   - Artifact 必须声明 JSON Schema、严格或建议校验方式和大小限制。
   - Connector 凭据只通过 `credential_handles` 引用，不得把真实值写入输入。
6. 编辑声明式 UI。`ui.entry` 必须指向真实存在的 `workflow_view.v1` 文件；输入字段只描述用户需要提供的事实，不展示实现说明或内部提示。
7. 调用 `extension.workflow.validate` 校验工程，再调用 `extension.workflow.build` 执行契约检查。修复失败后重复执行，直到通过或出现需要用户决策的阻塞。
8. 调用 `extension.workflow.package` 生成工作区内的 `.hmwf`，再次调用 `extension.workflow.validate` 校验制品。
9. 调用 `extension.workflow.candidate.save`，传入 Workflow 源码目录。Agent 会生成不可变 Candidate。随后调用 `extension.test` 或 `extension.workflow.candidate.test`，必须检查 Manifest、资产、Step DAG、入口出口、Loop、Candidate、Artifact Schema、依赖和 Preflight；记录失败原因和未覆盖的真实平台验收。
10. 本地完成后返回 Workflow ID、版本、模板、入口出口、步骤数、Artifact、依赖、Candidate SHA-256 和测试结果。只有 Connected 模式下用户明确要求“提交审核”时，才调用 `extension.workflow.candidate.confirm` 和 `extension.workflow.submission.submit`。
11. 提交成功后才调用 `extension.workflow.submission.status` 返回审核状态。管理员审核、签名、发布、撤回和同版本替换不属于本技能权限。

## 协作修订

1. 调用 `extension.revision.create` 时传入 `kind: workflow`、稳定 ID 和当前版本；Agent 会创建下一个补丁版本并清除旧测试、确认和提审状态。
2. 已发布内容发生变化时必须提升语义版本并更新 `release_notes`，不得覆盖原版本。
3. 开发者自行取得和提交源码；Agent 不执行 Git clone、commit、push 或凭据管理。
4. 其他同事接手时，仅在 Connected 模式下调用 `extension.workflow.submission.status` 查看提交和协作状态；本地创作不依赖 Dashboard。

### 受阻点协议

失败时读取结构化诊断：`state`、`blockers[]`、`warnings[]` 和 `next_steps[]`。每个 blocker 含稳定 `code`、`stage`、`message`、`remediation` 和 `retryable`。按 code 分支处理，不解析中文错误句子。常见阶段包括 `workspace`、`toolchain`、`dependencies`、`package`、`preflight`、`runtime`、`connector`、`contract` 和 `submission`。

## 完成标准

- 工程位于 `workflows/<slug>`，不依赖创建它的仓库上下文即可独立校验、打包和安装。
- `workflow.json` 与 `ui/workflow-view.json` 一致，所有相对路径有效，入口出口、Loop、Candidate 和 Artifact 契约完整。
- `extension.workflow.validate`、`extension.workflow.build`、`extension.workflow.package` 和 Agent 候选测试全部通过。
- 依赖 Preflight 不把缺失 Connector、Runtime、Plugin 或 Skill 当作成功；真实平台验收与 Contract Double 明确区分。
- 输出中文摘要，列出 Workflow 名称、稳定 ID、版本、模板、入口出口、步骤、Artifact、依赖、文件路径、候选 SHA-256 和测试结果。
