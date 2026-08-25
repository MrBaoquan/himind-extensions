# 贡献指南

## 开发流程

1. clone 本仓库，从 `main` 创建短期功能分支。
2. 只修改目标扩展及确有必要的共享 SDK 或工具链。
3. 提升目标扩展的语义版本，并填写简洁的中文 `release_notes`。
4. 运行 `go test ./...` 和 `go run ./tools/cmd/himind-repo-check`。
5. 提交 Pull Request，说明变更、风险、兼容性和验证结果。
6. 合并后在 Agent 工作台打开对应子目录，构建并提交审核。

独立分发由仓库维护者在 `main` 上运行 `Release extension` 工作流。贡献者不提交 `.hmpkg`、`.hmskill`、签名或目录摘要，也不复用已发布版本号。Release 已创建但 catalog 更新失败时，由维护者重跑同一提交，或在主分支已经前进后运行 `Repair extension catalog`；恢复只复用既有签名制品。

## 边界

- 不提交 Token、Cookie、私钥、`.env` 或用户目录绝对路径。
- 不提交 `.hmpkg`、`.hmskill`、可执行文件、校验和或 Agent 草稿数据。
- 不修改已发布版本的源码后继续使用原版本号。
- Skill 只编排知识和能力；确定性处理应由插件 Capability 承担。
- 插件可以独立提供 AI 工具、桌面 UI 或系统能力，也可以被 Skill 或其他插件依赖。

## 协作角色

HiMind 仅使用作者和贡献者两个角色。作者负责仓库绑定及贡献者管理；作者和贡献者均可构建与提审。代码读取、分支权限和 Pull Request 审核完全由 GitHub 管理。
