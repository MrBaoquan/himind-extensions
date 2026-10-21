# 场景路由

| 场景 | 主能力 | 说明 |
|---|---|---|
| 环境 | `wechat.miniprogram.devtools.environment.get` | 读版本关系、登录态、CLI 与项目配置 |
| 登录 | `wechat.miniprogram.devtools.login.start` + `task.get` | 扫码后按异步规则轮询 |
| 项目 | `project.list` / `project.import` / `project.remove` | 删除只移除列表项，不删磁盘 |
| 窗口 | `window.manage` | open/close/quit；quit 会影响整个工具 |
| 编译 | `compile.inspect` / `compile.run` | inspect 只读；run 会写构建产物 |
| 预览 | `preview.create` | auto 推送；qrcode 生成二维码 |
| 自动化 | `automation.element` / `.page` / `.game` | 小程序用 selector，小游戏用画布坐标 |
| 调试 | `debug.inspect` | runtime_info / console / network / screenshot |
| 调试写操作 | `debug.mutate` | clear_cache / wx_api / testaccount，需审批 |
| 云读取 | `cloud.read` | env / fn / db / storage 查询 |
| 云写入 | `cloud.write` | 部署、写库、存储管理，需审批 |
| 上传 | `wechat.miniprogram.upload` 或交付工作流 | 优先工作流；channel=auto/ci/wechatide |
| 配置 | 工作区文件工具 | 改 `project.config.json`，不调用 CLI |
| 安装检测 | `devtools.installer.check` | 只读；下载安装另走安装器 |
