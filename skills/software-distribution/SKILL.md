---
name: software-distribution
description: 为 WPF、Unity Windows 或 Unity Android 项目接入 HiMind Dashboard 软件分发，检查项目与制品，受控创建产品和发布版本，并验证真实更新链路。用户提到软件分发、自更新、发布渠道、MediaResolver、Unity APK、Windows 更新包或 Dashboard Release 时使用。
---

# 软件分发对接

使用 Dashboard 作为产品、渠道、制品和 Release 的唯一事实源。HiMind Agent 只向 AI 暴露规则与受控 Capability；应用运行时必须直接调用 Dashboard 公共分发 API，不得依赖 Agent 常驻。

## 能力边界

- `software.distribution.project.inspect`：识别项目类型并给出接入建议，只读。
- `software.distribution.artifact.inspect`：检查工作区内制品，计算大小和 SHA-256，只读。
- `software.distribution.release.publish`：由 Agent 内置 broker 使用短时用户委托身份创建产品、上传制品并发布，属于网络写操作。
- `software.distribution.release.resolve`：调用 Agent 配置的 Dashboard 公共解析接口验证发布，只读网络操作。

Skill 和插件都不得接收、读取、输出或保存 Token、Cookie、Agent credential、私钥。不得让用户把凭据写入项目配置。发布 broker 在 Agent 内部取得短时 OAuth 令牌，AI 和插件进程不可见。

## 接入流程

1. 确认明确的项目工作区、应用类型、稳定 `product_id`、版本、渠道、平台和架构。稳定 ID 使用小写 ASCII，例如 `com.himind.media-resolver`。
2. 调用 `software.distribution.project.inspect`，传入 `workspace_root`、工作区内 `project_path` 和 `product_id`。
3. 检查项目是否已有分发客户端：
   - .NET/WPF 使用 `HiMind.Distribution`；Windows 文件替换使用 `HiMind.Distribution.Windows` 和独立 Updater。
   - Unity Windows 可复用 `netstandard2.0` 协议层，安装仍使用 Windows 平台适配器。
   - Unity Android 使用相同 resolve manifest；APK 安装、商店更新或 MDM 策略由 Android 适配层负责，不调用 Windows Updater。
4. 应用配置必须包含 Dashboard HTTPS 基址、`productId`、渠道、平台、架构和 `/api/software-distribution/v1/updates/resolve`。不得包含后台身份凭据。
5. 构建不可变制品。Windows 目录包使用 `directory-zip`，Android 使用 `apk`，Addressables 使用 `unity-addressables`，其他单文件使用 `content`。
6. 调用 `software.distribution.artifact.inspect`。只有返回 `ready=true` 且文件名、版本、目标、大小和 SHA-256 与预期一致时才能进入发布。
7. 向用户展示发布摘要：产品、版本、渠道、平台、架构、包类型、文件、大小、SHA-256、是否强制、灰度比例。没有用户本轮明确确认时，不得调用发布能力，也不得自行把 `confirmed` 设为 `true`。
8. 用户确认后调用 `software.distribution.release.publish`，参数必须与检查结果完全一致，并传入 `confirmed=true`。禁止同版本替换不同内容；内容变化必须提升版本。
9. 发布成功后调用 `software.distribution.release.resolve`，用低于新版本的 `current_version` 验证有更新，再用等于新版本的版本验证 `update=null`。
10. 在目标项目运行真实客户端测试：下载短时 URL、校验大小和 SHA-256，并按平台执行安装或模拟替换。只有真实 Dashboard 地址完成 resolve 与下载后，才能报告链路打通。

## MediaResolver 验收

MediaResolver 使用以下固定契约：

```text
product_id: com.himind.media-resolver
channel: stable
platform: windows
architecture: x64
package_type: directory-zip
```

发布包必须包含 `MediaResolver.exe`、`distribution.sample.json` 和 `updater/HiMind.Distribution.Updater.exe`。验收至少覆盖：

1. Dashboard 返回 camelCase `{ "update": manifest }`。
2. `manifest.productId` 等于 `com.himind.media-resolver`。
3. 下载大小与 SHA-256 匹配。
4. 旧版本能解析到更新，当前版本返回无更新。
5. Updater 能替换测试目录内容且立即失败时回滚。

## 灰度与回滚

公共匿名 resolve 只消费 `rollout_percent=100` 的 Release。设备级灰度、定向升级和结果上报必须走受管客户端协议，不能用 IP 或随机数代替稳定实例 ID。

发现错误发布时停止继续验证，并要求有权限的管理员在 Dashboard 撤销 Release。当前 Skill 不自动撤销、不删除历史制品、不覆盖同版本包。

## 输出格式

返回中文摘要，依次列出：项目识别、客户端接入状态、制品摘要、发布确认、Dashboard Release、resolve/download 验证、安装测试、未完成风险。明确区分“本地测试通过”“临时 Dashboard 集成通过”和“真实环境已发布”。
