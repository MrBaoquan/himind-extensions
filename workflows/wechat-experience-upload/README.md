# 微信小程序体验版上传

这是一条面向日常开发和 Bug 修复的短 Workflow：

1. 选择 `kerun_user` 工程。
2. 选择展馆和 `development` 或 `production` 环境。
3. 可选择通过 `himind.builtin` Runtime 启动 DSH 开发 Loop。
4. 按展馆和环境执行真实构建。
5. 冻结 Git Candidate。
6. 选择 `wechatide` 或 `ci` 上传通道并执行启动前检查。
7. 用户审批后上传微信体验版并记录上传 Artifact。

Workflow 使用 `segmented` 执行模式：

- 从 `build` 入口开始时，必须提供 DevelopmentCheckpoint，构建后的 Candidate 会校验 Commit SHA 和 Tree Digest。
- 从 `upload` 入口开始时，必须已有 Candidate、Readiness 和审批事实。
- 可在 `candidate_ready` 或 `experience_version` 出口结束，使用 `completion_mode=partial` 保留部分交付语义。

本地输入样例见 `examples/kerun-user.szkjg.input.json`。

默认使用微信开发者工具 `wechatide` 通道，不需要上传私钥；开发者工具需要已启动、登录并完成 CLI 授权。

调用方必须显式区分两个目录：

- `source_root`：业务源码根目录，构建脚本从这里执行。
- `project_root`：包含 `project.config.json` 的小程序构建产物目录，通常是 `target.build_root`。

`engineering.project.resolve` 会返回这两个路径。不要把二者都填成源码根目录，否则构建后的 AppID 校验和上传前检查会读取错误位置。

使用 `ci` 通道时，上传私钥通过 Agent Credential Broker 保存，Workflow 输入只保存句柄：

```json
{
  "credential_handles": {
    "private_key_path": "wechat-upload-private-key"
  }
}
```

构建命令由插件按目标自动执行：

```text
node scripts/wx-cli.js build --env <venue> --server <environment>
```

因此切换展馆后不会复用其他展馆的 `project.config.json`。

上传命令会按通道调用：

```text
wechatide -c Copilot upload --project <dist/wx> --upload-version <version> --desc <description>
```

或：

```text
miniprogram-ci upload
```
