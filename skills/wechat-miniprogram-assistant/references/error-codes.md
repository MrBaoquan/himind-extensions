# 错误码

| 错误码 | 含义 | 处理 |
|---|---|---|
| `WECHATIDE_NOT_INSTALLED` | 未找到开发者工具 | 引导安装，或提供安装目录后重查 |
| `WECHATIDE_CLI_UNAVAILABLE` | CLI 不可执行 | 修复/更新开发者工具，不要继续业务调用 |
| `WECHATIDE_TIMEOUT` | CLI 调用超时 | 报告超时，询问是否重试一次 |
| `WECHATIDE_COMMAND_FAILED` | CLI 返回失败 | 原样保留关键错误，不要猜修复 |
| `WECHATIDE_LOGIN_REQUIRED` | 登录态失效 | 触发登录并轮询 |
| `WECHATIDE_TOKEN_REQUIRED` | 需要 CLI 令牌 | 让用户在安全设置中配置，不要粘贴到对话 |
| `WECHATIDE_CLIENT_INVALID` | clientName 不合法 | 使用插件配置的固定客户端名 |
| `PROJECT_PATH_INVALID` | 项目路径无效或越界 | 确认项目位于 workspace_root 内 |
| `PROJECT_CONFIG_INVALID` | project.config.json 无法解析 | 修正 JSON 后重试 |
| `APPID_MISMATCH` | AppID 与项目配置不一致 | 确认目标 AppID |
| `PRIVATE_KEY_NOT_FOUND` | CI 上传私钥不可读 | 通过凭据句柄提供文件 |
| `MINIPROGRAM_CI_UNAVAILABLE` | 无法解析 miniprogram-ci | 在项目中安装依赖后重试 |
| `NODE_UNAVAILABLE` | 未找到 node | 安装 Node.js 并加入 PATH |
| `ENV_REQUIRED` / `ENV_AMBIGUOUS` | 云环境缺失或不唯一 | 先列环境，让用户选择 |
| `APPROVAL_REQUIRED` | 操作需要审批 | 走审批中心，不要绕过 |
| `TASK_PENDING` | 异步任务未完成 | 保留 task_id，不重发 |
| `TASK_EXPIRED` | 异步任务过期 | 由用户决定是否重新发起 |
