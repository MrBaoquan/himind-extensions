---
name: develop-himind-plugins
description: 自动设计、创建、实现、测试、校验、打包和受控提交 HiMind Agent 插件。用户要求开发插件、创建 Capability、生成 plugin.json、构建 JSON-RPC/stdio 插件、生成 .hmpkg、在空白目录开发、提交审核或排查插件工程问题时使用。展示名称和用户文案使用中文，稳定 ID、目录名、二进制名和 Capability ID 使用 ASCII。
---

# 插件开发助手

通过 HiMind Agent 提供的 Capability 完成插件全流程。不要假设当前项目包含 HiMind 源码、`AGENTS.md`、ADR、`docs/`、`agent-plugins/` 或任何脚手架；这些内容不存在时也必须正常工作。

## 工作边界

1. 先调用 `extension.workspace.current` 确认当前扩展工作区。外部 AI 工具若返回 Agent 主目录、`source=process_current_dir` 或 `bound=false`，调用 `extension.workspace.bind`，传入聚合仓库根目录或单个插件项目目录；随后重新调用 `extension.workspace.current`。`workspace.open.local` 只是打开目录，不是 MCP 工作区绑定能力。
2. 用户未给出目标目录时，先确认一个明确的工作区；不得猜测 Agent 安装目录、Dashboard 地址或凭据位置。
3. 插件承担确定性文件、进程和专业格式处理；纯操作知识与编排规则使用独立技能。
4. 用户可见名称、描述、命令标题和错误提示使用中文；`id`、目录名、Go 模块名、入口文件和 Capability ID 使用小写 ASCII。
5. 只在 `workspace_root` 内写文件。不得读取或传递 Token、Cookie、私钥和 Agent credential，不得直接修改 Distribution 数据库。
6. 调用 `extension.authoring.identity` 获取当前 Agent 的本地或 Dashboard 作者资料，将返回的 `user_name` 写入 `plugin.json.author`。独立模式使用本地作者资料，不因未连接 Dashboard 停止本地创作。
7. 每个新版本必须在 `plugin.json.release_notes` 中填写中文更新说明，并从功能分类 ID `software-engineering`、`visual-design`、`video-post`、`3d-animation`、`content-production`、`audio-sound`、`data-automation`、`docs-knowledge`、`testing-quality`、`collaboration-delivery`、`system-device` 中选择至少一个 `categories` 分类。分类描述能力领域，不填写岗位名称、权限或客户端名称。
8. Agent 不执行 Git clone、pull、commit、push 或凭据管理。开发者可自行用 Git 管理源码；本技能只处理当前 `workspace_root` 中的工作副本和不可变候选包。
9. `plugin.json` 必须显式声明插件依赖。只在确有运行时依赖时填写 `plugin_dependencies`；不得把本技能、技能开发助手或 AI 扩展开发工具声明为业务插件运行时依赖。

## 自动开发流程

1. 调用 `extension.workspace.current`；必要时先调用 `extension.workspace.bind` 绑定聚合仓库或插件目录。再调用 `extension.authoring.preflight`（`kind: plugin`）检查 Agent、三件套、工作区和运行模式；预检通过后调用 `extension.authoring.identity` 获取 `user_name`，再调用 `extension.environment.preflight` 检查 Go。任一预检出现 `state: blocked` 时停止写入并原样返回 `blockers` 与 `next_steps`。
2. 根据需求确定插件 ASCII 名称、中文展示名、用途说明、模板、Capability、输入 Schema、风险等级、最小权限、功能分类、必需/可选插件依赖和本版本更新说明。需要独立窗口或桌面快捷方式时使用 `ui-tool`；只向 AI 提供只读工具时使用 `readonly-tool`；长任务使用 `job-worker`。
3. 调用 `extension.plugin.scaffold`，传入绝对 `workspace_root`、工作区内的 `output_dir`、`name`、`display_name`、`description`、身份返回的 `author`、`categories`、`release_notes` 和 `template`。脚手架必须生成可在空白目录独立构建的工程。
4. 在生成工程内实现功能与测试。使用 JSON-RPC 2.0 stdin/stdout；业务日志只写 stderr 并脱敏。不得接受任意 Shell、任意 URL、明文凭据或越出工作区的路径。
5. 调用 `extension.plugin.validate` 校验工程，再调用 `extension.plugin.build` 执行 `go test ./...` 并构建 Manifest 声明的入口。修复失败后重复校验和构建，直到通过或出现需要用户决策的阻塞。
6. 调用 `extension.plugin.package` 生成工作区内的 `.hmpkg`，随后再次调用 `extension.plugin.validate` 校验制品。
7. 调用 `extension.plugin.candidate.save`，传入已校验 `.hmpkg` 的 `package_path` 保存候选包，再调用 `extension.test` 并传入 `kind: plugin`、稳定 ID 和版本。统一测试必须完成 Manifest、依赖、包、开发注册和运行时 Registry 门禁；记录候选包路径、SHA-256、结构化测试结果和失败原因。
8. 本地完成后向用户返回候选包、SHA-256 和测试结果。只有 Connected 模式下用户明确要求“提交审核”时，才调用 `extension.plugin.submission.submit`；独立模式保留候选，不提示连接 Dashboard。
9. 提交成功后才调用 `extension.plugin.submission.status` 返回审核状态和意见。管理员审核、签名、发布、撤回和同版本替换不属于本技能权限。

## 协作修订

外部 AI 工具也可以迭代三件套自身。先将 AI 会话工作区切换到对应的三件套工程目录，再按本流程修改源码或文档；调用 `extension.revision.create` 时传入 `kind: plugin`、稳定 `id` 和当前 `version`，Agent 会创建下一个补丁版本并清除旧测试、确认和提审状态。三件套的确定性文件/构建操作仍由 `com.himind.extension-development-tools` 执行，Agent 的 `extension.test` 才是候选闭环门禁。

1. 其他同事接手已上架插件时，仅在 Connected 模式下调用 `extension.plugin.submission.status` 定位产品、当前版本、提交 ID 和自己的协作角色；本地工程开发不依赖该状态。
2. 开发者自行取得并管理源码工作副本。基于已发布版本修改时提升语义版本，更新 `release_notes`，重新完成校验、构建和打包。
3. 保存候选时除 `package_path` 外传入 `revision_of_version` 和 `parent_submission_id`，使 Dashboard 能把新提交挂到同一产品和父版本。不得创建同版本替换包。

### 受阻点协议

所有 Agent 创作编排能力在失败时返回 JSON 诊断：`state`、`blockers[]`、`warnings[]` 和 `next_steps[]`。每个 blocker 含稳定 `code`、`stage`、`message`、`remediation` 和 `retryable`；外部 AI 必须按 code 分支处理，不解析中文错误句子。常见阶段包括 `workspace`、`toolchain`、`dependencies`、`package`、`runtime`、`cleanup` 和 `submission`。独立模式下提审不可用属于 warning，不是本地创作阻塞。

## 完成标准

- 工程不依赖创建它的仓库，可以从空白目录独立测试和构建。
- Manifest、依赖解析、代码测试、入口构建、目录校验、包校验和统一候选测试全部通过；禁用三件套后，候选插件仍能作为独立运行时能力进入 Agent Capability Registry。
- 已发布内容发生变化时提升语义版本，不覆盖原版本；`release_notes` 准确概括新增、改进、修复和兼容性变化。
- 输出中文摘要，列出插件名称、稳定 ID、版本、版本更新说明、Capability、文件路径、测试结果和制品 SHA-256。仅在已执行提审时补充审核状态。
