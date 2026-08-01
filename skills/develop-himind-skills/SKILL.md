---
name: develop-himind-skills
description: 自动设计、创建、编辑、校验、跨客户端测试、打包和受控提交 HiMind 技能。用户要求制作技能、编写 SKILL.md、生成 skill.json、配置 Codex、GitHub Copilot 或 WorkBuddy 全局发现、创建 .hmskill、在空白目录开发、执行 Agent 候选测试、提交组织审核或排查技能创作问题时使用。展示名称和说明使用中文，frontmatter name、稳定 ID 和目录名使用 ASCII。
---

# 技能开发助手

通过 HiMind Agent 提供的 Capability 完成技能全流程。不要假设当前项目包含 HiMind 源码、`AGENTS.md`、ADR、`docs/`、`.agents/skills/` 或任何打包脚本；这些内容不存在时也必须正常工作。

## 工作边界

1. 将 `workspace_root` 只作为用户授权的开发目录和路径边界，不把它当成技能规范来源。用户说“当前目录”时使用客户端实际提供的工作目录，不向父目录搜索 HiMind 源码。
2. 用户未给出目标目录时，先确认一个明确的工作区；不得猜测 Agent 安装目录、Dashboard 地址或凭据位置。
3. 技能只承载触发条件、操作知识和 Capability 编排。确定性解析、转换、构建、设备或网络操作应由插件 Capability 提供。
4. 用户可见名称、说明、默认提示和正文使用中文；稳定 ID、目录名和 frontmatter `name` 使用小写 ASCII 连字符。
5. 只在 `workspace_root` 内写文件。不得携带安装脚本、账号、Token、Cookie、私钥、绝对用户路径或任意 Shell。
6. 先调用 `extension.authoring.identity` 获取当前 Agent 已授权工作台账号，将返回的 `user_name` 写入 `skill.json.author`。不得猜测、缓存或硬编码作者。未授权时停止创建并提示用户先完成账号授权。
7. 每个新版本必须在 `skill.json.release_notes` 中填写中文更新说明，并从功能分类 ID `software-engineering`、`visual-design`、`video-post`、`3d-animation`、`content-production`、`audio-sound`、`data-automation`、`docs-knowledge`、`testing-quality`、`collaboration-delivery`、`system-device` 中选择至少一个 `categories` 分类。分类描述能力领域，不填写岗位名称、权限或客户端名称。
8. Agent 不执行 Git clone、pull、commit、push 或凭据管理。开发者可自行用 Git 管理源码；本技能只处理当前 `workspace_root` 中的工作副本和不可变候选包。

## 自动开发流程

1. 调用 `extension.authoring.identity` 获取 `user_name`，再调用 `extension.environment.preflight` 并传入 `kind: skill`。Skill 脚手架、校验和打包不要求 Go 工具链；存在其他阻塞项时停止写入并返回明确修复项。
2. 根据用户场景确定触发语、必要输入、工作步骤、输出格式、风险门禁、支持客户端、Capability 和插件依赖。
3. 调用 `extension.skill.scaffold`，传入绝对 `workspace_root`、工作区内的 `output_dir`、ASCII `slug`、稳定 `id`、中文 `name`、版本、中文说明、身份返回的 `author`、`categories`、`release_notes` 和 `supported_clients`。默认同时支持 `codex`、`github-copilot` 与 `workbuddy`。
4. 编辑生成的 `SKILL.md`、`skill.json` 和 `agents/openai.yaml`。frontmatter 只包含 `name` 与 `description`；`description` 同时写明用途和触发场景。把 `agents/openai.yaml` 加入 `contents`，保持中文展示名、简短说明和默认提示与正文一致。
5. 调用 `extension.skill.validate` 校验工程，再调用 `extension.skill.package` 生成工作区内的 `.hmskill`，随后再次调用 `extension.skill.validate` 校验制品。
6. 调用 `extension.skill.candidate.save`，仅传入已校验 `.hmskill` 的 `package_path`。Agent 必须原样保存候选制品和 SHA-256，不再根据结构化字段重新生成包。再用返回的稳定 ID 和版本调用 `extension.skill.candidate.test`。
7. 候选测试必须覆盖 Manifest 声明的全部客户端：Codex 安装到全局 Skill 根目录下的托管 `current` 版本，GitHub Copilot 安装到全局 `~/.copilot/skills/<skill-name>` 目录，WorkBuddy 安装到全局 `~/.workbuddy/skills/<skill-name>` 目录。记录每个客户端的实际目标目录，不把项目级 `.agents/skills` 当作安装结果。
8. 提醒用户分别在声明的客户端完成真实对话测试。只有用户确认测试通过并明确要求“提交审核”时，才调用 `extension.skill.submission.submit`。该能力会显示包含中文名称、版本和 SHA-256 的本机确认窗口；用户取消时不得重试或绕过。
9. 提交后调用 `extension.skill.submission.status` 返回审核状态和意见。管理员审核、签名、发布、撤回和同版本替换不属于本技能权限。

## 协作修订

1. 其他同事接手已上架 Skill 时，先调用 `extension.skill.submission.status` 定位产品、当前版本、提交 ID 和自己的协作角色。作者与贡献者都可以准备、构建和提交新版本。
2. 开发者自行取得并管理源码工作副本。基于已发布版本修改时提升语义版本，更新 `release_notes`，重新完成校验、打包和所有声明客户端的测试。
3. 保存候选时除 `package_path` 外传入 `revision_of_version` 和 `parent_submission_id`，使 Dashboard 能把新提交挂到同一产品和父版本。不得创建同版本替换包。

## 完成标准

- 工程不依赖创建它的仓库，在没有上下文文档的空白目录也能校验和打包。
- Skill Creator 规则、工程校验、包校验、依赖预检以及 Codex、GitHub Copilot、WorkBuddy 渲染测试全部通过。
- 已发布内容发生变化时提升语义版本，不覆盖原版本；`release_notes` 准确概括新增、改进、修复和兼容性变化。
- 输出中文摘要，列出技能名称、稳定 ID、版本、版本更新说明、支持客户端、依赖、文件路径、测试结果、各客户端全局目录、制品 SHA-256、提审状态及仍需管理员处理的动作。
