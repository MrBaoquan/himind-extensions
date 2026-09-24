# 微信小程序交付工具

`com.himind.wechat-miniprogram-tools` 是微信小程序 Workflow 的本地能力插件，不和 Agent Core 或 Dashboard 业务域耦合。

提供的稳定 Capability：

- `wechat.miniprogram.requirements.snapshot`
- `wechat.miniprogram.project.inspect`
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

构建与校验：

```powershell
go test ./plugins/wechat-miniprogram-tools
go build -o plugins/wechat-miniprogram-tools/wechat-miniprogram-tools.exe ./plugins/wechat-miniprogram-tools
go run ./tools/cmd/himind-plugin-validate-cli -path plugins/wechat-miniprogram-tools
```

打包：

```powershell
./tools/release/build-extension.ps1 `
  -Kind plugin `
  -ExtensionPath plugins/wechat-miniprogram-tools `
  -OutputDirectory dist
```

安全边界：

1. `private_key_path` 只传路径，插件不把私钥内容写入日志或 Artifact。
2. `preview` 和 `upload` 解析工程本地的 `miniprogram-ci`，不把凭据拼入 Shell。
3. 微信审核没有稳定公开自动提交接口时，`review.submit` 必须要求人工回执或证据，不能猜测成功。
4. `release.record` 只记录真实版本、Commit SHA 和证据，不伪造平台审核状态。
