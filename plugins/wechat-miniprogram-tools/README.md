# 微信小程序开发工具

`com.himind.wechat-miniprogram-tools` 是微信小程序/小游戏的本地能力插件，不和 Agent Core 或 Dashboard 业务域耦合。

## 交付能力

- `wechat.miniprogram.requirements.snapshot`
- `wechat.miniprogram.project.inspect`
- `wechat.miniprogram.ci.readiness`
- `wechat.miniprogram.development.record`
- `wechat.miniprogram.dependencies.prepare`
- `wechat.miniprogram.test`
- `wechat.miniprogram.build`
- `wechat.miniprogram.preview`
- `wechat.miniprogram.upload`
- `wechat.miniprogram.acceptance.record`
- `wechat.miniprogram.review.prepare`
- `wechat.miniprogram.review.submit`
- `wechat.miniprogram.release.record`
- `wechat.miniprogram.rollback`
- `wechat.miniprogram.delivery.verify`

## 开发者工具能力

- `wechat.miniprogram.devtools.env.get`
- `wechat.miniprogram.devtools.login.start`
- `wechat.miniprogram.devtools.task.get`
- `wechat.miniprogram.devtools.project.list`
- `wechat.miniprogram.devtools.project.add`
- `wechat.miniprogram.devtools.project.del`
- `wechat.miniprogram.devtools.window.ctrl`
- `wechat.miniprogram.devtools.compile.get`
- `wechat.miniprogram.devtools.compile.run`
- `wechat.miniprogram.devtools.preview.make`
- `wechat.miniprogram.devtools.debug.read`
- `wechat.miniprogram.devtools.debug.mutate`
- `wechat.miniprogram.devtools.auto.click`
- `wechat.miniprogram.devtools.auto.page`
- `wechat.miniprogram.devtools.auto.game`
- `wechat.miniprogram.devtools.auto.eval`
- `wechat.miniprogram.devtools.install.get`

## 云开发能力

- `wechat.miniprogram.cloud.read`
- `wechat.miniprogram.cloud.write`

## 构建与校验

```powershell
go test ./plugins/wechat-miniprogram-tools
go build -o plugins/wechat-miniprogram-tools/wechat-miniprogram-tools.exe ./plugins/wechat-miniprogram-tools
go run ./tools/cmd/himind-plugin-validate-cli -path plugins/wechat-miniprogram-tools
```

## 打包

```powershell
./tools/release/build-extension.ps1 `
  -Kind plugin `
  -ExtensionPath plugins/wechat-miniprogram-tools `
  -OutputDirectory dist
```

## 安全边界

1. `private_key_path` 只传路径，插件不把私钥内容写入日志或 Artifact。
2. `preview` 和 `upload` 解析工程本地的 `miniprogram-ci`，不把凭据拼入 Shell。
3. 微信审核没有稳定公开自动提交接口时，`review.submit` 必须要求人工回执或证据，不能猜测成功。
4. `release.record` 只记录真实版本、Commit SHA 和证据，不伪造平台审核状态。
5. `devtools.auto.eval`、`devtools.debug.mutate`、`cloud.write`、`devtools.project.del` 属于高风险能力，需审批。
6. `devtools.project.add` 的 `import_minicode` 导入的是不可信代码，打开窗口前必须先审查。
