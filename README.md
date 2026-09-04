# HiMind Extensions

马宝全维护的 HiMind Agent 插件与 Skill 源码仓库。仓库统一版本控制和协作入口，每个扩展仍独立版本化、构建、测试和提交审核。

## 目录

- `plugins/<name>`：可独立构建和安装的插件工程。
- `skills/<name>`：可独立校验和打包的 Skill 工程。
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

Agent 不执行 Git 操作。开发者自行 clone、建分支、提交和发起 PR，随后在 Agent 中打开对应的插件或 Skill 子目录，完成构建和提审。

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
2. 在 Agent 面板选择一次聚合仓库，打开目标插件或 Skill 子目录。
3. 在 development profile 完成预检、构建、校验、打包和候选测试。
4. 需要组织审核或发布时切换 Connected 模式并提交；Independent 模式下可完成所有本地开发能力，但不显示控制面操作。
5. production profile 只安装审核发布版本，不把共享源码目录直接当作生产运行时。

## Agent 运行模式

扩展工程、插件 Capability 和 Skill 客户端适配属于 Agent 通用能力，与组织控制面解耦：

- Independent 模式可直接完成脚手架、校验、构建、打包、候选保存、候选测试，以及从 GitHub 导入本地插件和 Skill。
- Connected 模式在上述能力之上增加组织清单、协作、审核、发布和受管策略。
- 同一个工程和同一套 Capability 在两种模式下工作；只依赖组织控制面的能力由 Agent 按可用性边界隐藏或返回明确的控制面错误。

因此新插件应把 Capability 声明为 `local`、`network_service` 或 `control_plane`。本地检查和确定性处理放在 `local`，组织发布、审核和调度放在 `control_plane`，不要为两种模式复制两套实现。

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

插件构建产物、`.hmpkg`、`.hmskill`、`checksums.sha256` 和 Agent 草稿状态不进入 Git。构建产物由 Agent 生成并作为不可变审核制品提交。

## 发布规则

1. 从 `main` 创建功能分支，通过 Pull Request 合并。
2. 修改某个扩展时，只提升该扩展自己的语义版本。
3. 每个新版本必须更新 `release_notes`，已发布版本不得覆盖。
4. 作者和已授权贡献者都可以从 Agent 提交审核；GitHub 仓库权限和代码评审由 GitHub 管理。
5. Dashboard 只审核最新提交，并保存仓库、分支、子目录和 commit 快照。

## 独立模式分发

源码仓库和运行时分发目录彼此独立：`extensions.json` 用于本地多项目开发，`.himind/catalog.json` 用于 Agent 的 GitHub 扩展源。生产安装只使用签名的 GitHub Release 制品，不直接运行源码目录或仓库 ZIP。

每个扩展按自己的语义版本单独发布，不跟随 Agent 版本。仓库维护者在本机运行 `tools/release/publish-extension.ps1`，传入 `plugin` 或 `skill` 及仓库内路径。脚本会完成测试、打包、RSA-PSS/SHA-256 签名、不可变 Release 创建和公共目录更新，不依赖 GitHub Actions。同一提交重跑时会复用既有 Release 制品，不重新签名或覆盖版本；其他提交仍必须提升版本号。

示例：

```powershell
$env:HIMIND_EXTENSION_SIGNING_PRIVATE_KEY_PATH = 'C:\keys\himind-extension-private.pem'
$env:HIMIND_EXTENSION_SIGNING_KEY_ID = 'himind-production-2026'
./tools/release/publish-extension.ps1 -Kind skill -ExtensionPath skills/software-distribution
```

Release 创建后如果只需要修复目录，应在同一源提交上重跑同一命令；脚本会下载并复用不可变 Release 资产。若主分支已经前进，必须先恢复对应源提交或提升扩展版本，禁止用新提交伪装旧制品来源。

仓库需要配置：

- Secret `HIMIND_EXTENSION_SIGNING_PRIVATE_KEY_PEM_B64`：PKCS#8/PEM 私钥文件的 Base64 内容。
- Variable `HIMIND_EXTENSION_SIGNING_KEY_ID`：Agent 受信公钥目录中对应的稳定 key ID。

对应公钥及同一个 key ID 还必须配置到 `himind-dashboard` 的正式 Agent 安装包构建流程；安装器会把它写入本机 `trusted-keys`。私钥只属于本仓库发布流水线，不得交给 Agent、Dashboard 或扩展开发者。开发环境通过用户级 `HIMIND_TRUSTED_SIGNING_KEYS_DIR` 注入测试公钥。

Independent 和 Connected Agent 均可在扩展源面板一键添加 HiMind 扩展源，也可手动填写 `MrBaoquan/himind-extensions`、`main`、`.himind/catalog.json`。GitHub 目录项始终是用户可选、用户管理；组织必装、推荐、禁用和退出范围策略只由 Dashboard 控制面声明。目录中每个版本都必须提供完整签名元数据，且 key ID 必须已被本机信任，否则整个来源会显示为不可用而不会进入安装列表。
