# HiMind Extensions

马宝全维护的 HiMind Agent 插件、Skill 与 Workflow 源码仓库。仓库统一版本控制和协作入口，每个扩展仍独立版本化、构建、测试和发布。

扩展创作采用 1+3 四件套：一个共享的 `com.himind.extension-development-tools` 工具插件，加上插件开发助手、技能开发助手和工作流开发助手。三类创作共用 Workspace、Candidate、Extension Lock、测试报告和提审生命周期，但保留各自的确定性校验与运行语义。

稳定 ID、目录名、显示名称、用途说明、触发说明和更新说明都有长度上限，脚手架和校验超限即拒绝；阈值与文案规则以 `tooling/metaguide` 为准。

## 目录

- `plugins/<name>`：可独立构建和安装的插件工程。
- `skills/<name>`：可独立校验和打包的 Skill 工程。
- `workflows/<name>`：可独立生成 `.hmwf + extension-lock` 的 Workflow 工程。
- `sdk`：插件共用的最小运行时 SDK。
- `tooling`：脚手架、校验和打包实现。
- `tools/cmd`：仓库维护命令。
- `extensions.json`：扩展 ID 与源码子目录的权威清单。

Agent 工作台绑定同一个仓库地址，并为每个工程保存自己的仓库内目录。例如：

```text
仓库：https://github.com/MrBaoquan/himind-extensions.git
分支：main
仓库内目录：skills/develop-himind-skills
源码版本：提交审核时对应的 commit SHA
```

Agent 不执行 Git 操作。开发者自行 clone、建分支、提交和发起 PR，随后在 Agent 中打开对应的插件、Skill 或 Workflow 子目录，完成构建和提审。

## 一份源码仓库，多套 Agent Profile

在同一台电脑同时运行 production Agent 和 development Agent 时，不要为两个 profile 分别 clone 仓库。两者共享一个聚合仓库配置文件：

```text
%LOCALAPPDATA%\\HiMindAgent\\extension-workspace.json
```

配置内容只记录聚合仓库根目录，例如 `F:\\WebProjects\\himind-extensions`。在 Agent 的“扩展”页面选择一次包含 `extensions.json` 的目录即可；Agent 会校验清单并自动发现其中的插件和 Skill。命令行的 `HIMIND_EXTENSIONS_ROOT` 仅用于临时调试覆盖，不应作为日常配置。

共享的是源码和 `extensions.json`，以下内容仍按 profile 隔离：

- 插件运行时副本和二进制缓存；
- Skill 安装目录；
- 草稿、候选包和测试结果；
- Agent 状态、连接和控制面会话。

因此 development 可以直接在共享源码上构建和验证，production 不会因为源码变化自动加载开发中的二进制。生产发布仍应使用已审核的不可变包；开发者只需维护这一份 Git 工作副本。

推荐流程：

1. 开发者在聚合仓库中切换分支、编辑、提交和发起 PR。
2. 在 Agent 面板选择一次聚合仓库，打开目标插件、Skill 或 Workflow 子目录。
3. 在 development profile 完成预检、构建、校验、打包和候选测试。
4. 需要组织审核或发布时对接 AI 工作台并提交；未对接时本地开发能力完整，只是不显示控制面操作。
5. production profile 只安装审核发布版本，不把共享源码目录直接当作生产运行时。

## 与 AI 工作台的关系

扩展工程、插件 Capability 和 Skill 客户端适配属于 Agent 通用能力，与组织控制面解耦。对接 AI 工作台后在此基础上增加组织清单、协作、审核、发布和受管策略；未对接时本地能力完整可用，只隐藏控制面操作。

因此新插件应把 Capability 声明为 `local`、`network_service` 或 `control_plane`。本地检查和确定性处理放在 `local`，组织发布、审核和调度放在 `control_plane`，不要为两种形态复制两套实现。

## 本地验证

```powershell
go test ./...
go run ./tools/cmd/himind-repo-check
```

将仓库中的全部扩展注册到本机 Agent 工作台：

```powershell
$env:HIMIND_AGENT_PROFILE = "development"
go run ./tools/cmd/himind-agent-workspace-sync -commit (git rev-parse HEAD)
```

插件、Skill 和 Workflow 构建产物、`checksums.sha256`、`extension-lock.json` 和 Agent 草稿状态不进入 Git。构建产物由 Agent 生成并作为不可变审核制品提交。

## 发布规则

1. 从 `main` 创建功能分支，通过 Pull Request 合并。
2. 修改某个扩展时，只提升该扩展自己的语义版本。
3. 每个新版本必须更新 `release_notes`，已发布版本不得覆盖。
4. 作者和已授权贡献者都可以从 Agent 提交审核；GitHub 仓库权限和代码评审由 GitHub 管理。
5. Dashboard 只审核最新提交，并保存仓库、分支、子目录和 commit 快照。

## 扩展分发

源码仓库和运行时分发目录彼此独立：`extensions.json` 用于本地多项目开发，`.himind/catalog.json` 用于 Agent 的 GitHub 扩展源。生产安装只使用签名制品，不直接运行源码目录或仓库 ZIP。

### 分发落点由清单声明

每个扩展在自己的清单里用 `distribution_targets` 声明允许分发到哪里，缺这个字段仓库校验会直接报错，避免「忘了写」被当成「按默认发」。取值只有两个：

| 取值 | 含义 | 发布动作 |
| --- | --- | --- |
| `workbench` | 面向组织内审核与安装 | 生成签名制品，由 Agent 扩展工作区提交审核后对内分发 |
| `github` | 对外公开发布 | 创建不可变 GitHub Release，并更新 `.himind/catalog.json` |

两个都写就是双落点：先创建 GitHub Release，再走工作台提审。声明是硬约束，不只是默认值——Agent 侧的项目设置只能在声明范围内收窄（例如声明了两个、本机只发工作台），不能扩权；越界的选择会被拒绝并提示先改清单。

`extensions.json` 里的 `default_distribution_targets` 是新建扩展时的脚手架默认值，不参与运行时分发判断：实际落点只认各扩展清单的声明，且 `go run ./tools/cmd/himind-repo-check` 会拒绝缺少该字段的清单。

### 发布命令

每个扩展按自己的语义版本单独发布，不跟随 Agent 版本。仓库维护者在本机运行 `tools/release/publish-extension.ps1`，传入 `plugin`、`skill` 或 `workflow` 及仓库内路径。脚本按清单声明执行，完成测试、打包、RSA-PSS/SHA-256 签名、Release 创建和公共目录更新，不依赖 GitHub Actions。Workflow 发布还会生成并上传 `extension_lock.v1`，Catalog 同时写入制品和 Lock。

示例：

```powershell
$env:HIMIND_EXTENSION_SIGNING_PRIVATE_KEY_PATH = 'C:\keys\himind-extension-private.pem'
$env:HIMIND_EXTENSION_SIGNING_KEY_ID = 'himind-production-2026'
./tools/release/publish-extension.ps1 -Kind skill -ExtensionPath skills/software-distribution
./tools/release/publish-extension.ps1 -Kind workflow -ExtensionPath workflows/wechat-miniprogram-delivery
```

只声明 `workbench` 的扩展不会创建 Release，也不会写入 Catalog——公开目录条目的定位信息就是 Release 资产，没有 Release 就没有可写入的条目。脚本会输出 JSON 说明下一步（在 Agent 扩展工作区提审）。

签名材料通过进程环境变量提供：`HIMIND_EXTENSION_SIGNING_PRIVATE_KEY_PATH` 指向 PKCS#8/PEM 私钥文件，`HIMIND_EXTENSION_SIGNING_KEY_ID` 是该密钥在 Agent 受信公钥目录中的稳定 key ID。私钥只属于本仓库发布流水线，不得交给 Agent、Dashboard 或扩展开发者。对应公钥及同一个 key ID 还必须配置到 `himind-dashboard` 的正式 Agent 安装包构建流程，安装器会把它写入本机 `trusted-keys`；开发环境通过用户级 `HIMIND_TRUSTED_SIGNING_KEYS_DIR` 注入测试公钥。

Release 创建后如果只需要修复目录，应在同一源提交上重跑同一命令；脚本会下载并复用不可变 Release 资产。若主分支已经前进，必须先恢复对应源提交或提升扩展版本，禁止用新提交伪装旧制品来源。

新建 Release 必须有签名材料：缺 `HIMIND_EXTENSION_SIGNING_PRIVATE_KEY_PATH` 或 `HIMIND_EXTENSION_SIGNING_KEY_ID` 时脚本直接失败，不会产出未签名制品。复用已存在的 Release 时才不需要私钥。

### Agent 内置发布器

扩展开发者不走本仓库时，可以在 Agent 的扩展开发工作区直接发布，落点约束、签名元数据结构与这里完全一致（同一份契约 `contracts/agent-core/v1/extension-release-manifest.schema.json`），签名也读同一套环境变量。区别只在命名空间：Agent 内置发布器用 `<kind>/<id>@<version>` 作 Tag、同时上传 `<id>@<version>.json` 发布清单和 `<id>-<version>.<ext>.signature.json`；本仓库脚本用 `<kind>-<id>-v<version>` 作 Tag，目录条目写在 `.himind/catalog.json`。两者互不覆盖，也不会互相复用 Release。

### 安装侧

无论 Agent 是否对接 AI 工作台，都可以在扩展源面板一键添加 HiMind 扩展源，也可手动填写 `MrBaoquan/himind-extensions`、`main`、`.himind/catalog.json`。GitHub 目录项始终是用户可选、用户管理；组织必装、推荐、禁用和退出范围策略只由 Dashboard 控制面声明。目录中每个版本都必须提供完整签名元数据，且 key ID 必须已被本机信任，否则整个来源会显示为不可用而不会进入安装列表。

按 Tag 直装（不走目录清单）时口径相同：发布清单里出现 `signature` 就必须验签通过，否则安装失败并回滚；没有签名时由 `HIMIND_REQUIRE_SIGNED_EXTENSIONS` 决定是否放行，默认与 Agent 更新一致——内嵌了生产公钥就要求签名。
