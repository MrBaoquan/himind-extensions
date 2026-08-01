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

## 本地验证

```powershell
go test ./...
go run ./tools/cmd/himind-repo-check
```

将仓库中的全部扩展注册到本机 Agent 工作台：

```powershell
go run ./tools/cmd/himind-agent-workspace-sync -commit (git rev-parse HEAD)
```

插件构建产物、`.hmpkg`、`.hmskill`、`checksums.sha256` 和 Agent 草稿状态不进入 Git。构建产物由 Agent 生成并作为不可变审核制品提交。

## 发布规则

1. 从 `main` 创建功能分支，通过 Pull Request 合并。
2. 修改某个扩展时，只提升该扩展自己的语义版本。
3. 每个新版本必须更新 `release_notes`，已发布版本不得覆盖。
4. 作者和已授权贡献者都可以从 Agent 提交审核；GitHub 仓库权限和代码评审由 GitHub 管理。
5. Dashboard 只审核最新提交，并保存仓库、分支、子目录和 commit 快照。
