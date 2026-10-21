---
name: wechat-miniprogram-assistant
description: 面向微信小程序与小游戏开发，指导项目检查、开发者工具控制、编译预览、页面自动化、调试取证、云开发与体验版交付；当用户提到微信开发者工具、小程序模拟器、预览上传、console/network、云函数或云数据库时使用。
---

# 微信小程序开发助手

## 用途

把微信小程序/小游戏开发任务路由到 `com.himind.wechat-miniprogram-tools` 插件提供的确定性能力；需要审批和交付证据的固定流程交给工作流。

应使用：项目检查、开发者工具登录与开窗、编译/刷新、预览、页面自动化、console/network/截图、云环境与云函数、体验版交付、`project.config.json` 调整。

不应使用：通用编码辅助、与微信小程序无关的桌面自动化、任意 shell、未注册的工具调用。

## 运行前检查

任何需要 `wechat.miniprogram.devtools.*` 的任务，先调用：

1. `wechat.miniprogram.devtools.environment.get`，确认 CLI 可用、版本关系、登录态与项目配置。
2. 若 `ready=false`，按 `blockers[].code` 处理，不要猜参数或反复重试。
3. 需要登录时调用 `wechat.miniprogram.devtools.login.start`，再按异步规则处理。
4. 项目路径必须位于 `workspace_root` 内；不要使用工作区外路径。

`wechat.miniprogram.devtools.project.list` 是只读查询，不需要登录检查以外的额外门禁。

## 场景路由

| 用户意图 | 能力或工作流 |
|---|---|
| 检查环境、版本、登录 | `wechat.miniprogram.devtools.environment.get` / `login.start` |
| 查项目列表、导入、分享、删除 | `wechat.miniprogram.devtools.project.*` |
| 打开/关闭项目窗口、退出工具 | `wechat.miniprogram.devtools.window.manage` |
| 编译单文件、刷新、构建 npm | `wechat.miniprogram.devtools.compile.inspect` / `compile.run` |
| 预览、二维码 | `wechat.miniprogram.devtools.preview.create` |
| 点击、输入、滚动、页面断言 | `wechat.miniprogram.devtools.automation.element` / `.page` |
| 小游戏画布触摸 | `wechat.miniprogram.devtools.automation.game` |
| console / network / 截图 / 运行时 | `wechat.miniprogram.devtools.debug.inspect` |
| mock wx API、清缓存、测试号 | `wechat.miniprogram.devtools.debug.mutate` |
| 云环境、云函数、云数据库、云存储 | `wechat.miniprogram.cloud.read` / `cloud.write` |
| 上传体验版 | 优先走 `com.himind.workflow.wechat-miniprogram-delivery` |
| 只改 `project.config.json` | 直接用工作区文件工具，不调用 CLI |

详细场景说明见 `references/scenes.md`。

## 异步任务

任何能力返回 `status: pending` 和 `task_id` 时：

- 不要把“已发起”当成成功。
- 登录：提醒用户扫码，然后用 `wechat.miniprogram.devtools.task.get` 短等待轮询；只有 `success` 才继续依赖步骤。
- 上传、删除、云写、预览确认：提醒用户在微信开发者工具内确认，不要主动长轮询。
- 用户回来继续时，先用旧 `task_id` 查询；`pending` 时不得重发原操作。
- 只有明确进入 `failed` 且用户仍要求重试时，才重新发起。

完整规则见 `references/async-tasks.md`。

## 安全与审批

- 上传体验版、云函数部署、云数据库/存储写入、删除项目、清缓存、mock wx API、执行任意 JS：必须先审批。
- `wechat.miniprogram.devtools.automation.evaluate` 是代码执行能力，默认不主动使用；只有用户明确要求且审批通过才调用。
- `wechat.miniprogram.devtools.project.import` 的 `import_minicode` 导入的是不可信代码；先检查项目配置、依赖脚本、可执行代码和网络请求，再打开窗口。
- `project.config.json` 的 `scripts.beforeCompile/beforePreview/beforeUpload` 可能执行本地命令；构建前先向用户展示并要求确认。
- 不读取、不记录、不复述 token、ticket 或私钥内容；凭据只通过安全存储或凭据句柄使用。
- 不写微信开发者工具安装目录，不执行任意 shell，不接受工作区外路径。

完整规则见 `references/approval-policy.md`。

## 失败处理

- 按 `blockers[].code` 分支，不要解析中文错误句子。
- `WECHATIDE_CLI_UNAVAILABLE`：引导安装或修复开发者工具 CLI，不要继续业务调用。
- `WECHATIDE_LOGIN_REQUIRED`：先登录，再重试一次。
- `WECHATIDE_TOKEN_REQUIRED`：让用户在安全设置中配置 CLI 令牌，不要在对话中粘贴。
- `PROJECT_CONFIG_INVALID` / `APPID_MISMATCH`：先修正项目配置。
- `ENV_AMBIGUOUS`：列出环境让用户选择，禁止自动挑。
- `TASK_PENDING`：保留任务上下文，不要重发。
- 错误码速查见 `references/error-codes.md`。

## 交付原则

- 预览、上传、云发布至少保留：操作、版本、环境、任务号、结果与证据路径。
- 上传体验版优先使用工作流，由工作流负责候选冻结、审批、通道选择和记录。
- 只读诊断输出 `summary`、`issueClass`、`evidence`、`likelyCause`、`recoveryPath`、`nextActions`。
- 不把未确认的 pending 任务写成成功。

## 参考

- 场景路由：`references/scenes.md`
- 异步任务：`references/async-tasks.md`
- 安全与审批：`references/approval-policy.md`
- 错误码：`references/error-codes.md`
- 项目配置字段：`references/project-config-fields.md`
