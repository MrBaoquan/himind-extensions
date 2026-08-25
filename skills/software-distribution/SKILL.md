---
name: software-distribution
description: 为 WPF、Unity Windows 或 Unity Android 项目完成软件分发接入检查、制品校验，并在连接组织分发服务后创建产品、发布版本和验证更新链路。用户提到软件分发、自更新、发布渠道、MediaResolver、Unity APK 或 Windows 更新包时使用。
---

# 软件分发对接

HiMind Agent 将本地工程检查与组织分发服务分成两个能力层。独立模式可以完成项目识别、制品校验和本地客户端接入准备；连接组织分发服务后，才继续执行产品、渠道、制品发布和更新解析。

软件分发采用单一分层链路：Skill 负责编排；官方软件分发插件负责项目、制品和 Manifest 领域规则；Agent 内核负责当前工作区、预检收据、本机确认、OAuth broker 和同一文件句柄上传。不得在 Skill 或插件中复制发布鉴权逻辑，也不得在 Agent 内核中加入具体软件项目规则。

## 能力边界

- `software.distribution.project.inspect`：识别项目类型并给出接入建议，只读。
- `software.distribution.artifact.inspect`：检查工作区内制品，计算大小和 SHA-256，只读。
- `software.distribution.release.publish`：由 Agent 内置 broker 使用短时用户委托身份创建产品、上传制品并发布，属于网络写操作。
- `software.distribution.release.resolve`：调用 Agent 配置的组织分发服务解析接口验证发布，只读网络操作；独立模式不可用属于正常边界。

Skill 和插件都不得接收、读取、输出或保存 Token、Cookie、Agent credential、私钥。不得让用户把凭据写入项目配置。发布 broker 在 Agent 内部取得短时 OAuth 令牌，AI 和插件进程不可见。

## 接入流程

1. 确认明确的项目工作区、应用类型、稳定 `product_id`、版本、渠道、平台和架构。稳定 ID 使用小写 ASCII，例如 `com.himind.media-resolver`。
2. 先调用 `workspace.current` 获取 Agent 认可的当前 AI 工作区，再调用 `software.distribution.project.inspect`；`workspace_root` 必须与前者完全一致，`project_path` 必须位于工作区内。
3. 检查项目是否已有分发客户端：
   - .NET/WPF 使用 `HiMind.Distribution`；Windows 文件替换使用 `HiMind.Distribution.Windows` 和独立 Updater。
   - Unity Windows 可复用 `netstandard2.0` 协议层，安装仍使用 Windows 平台适配器。
   - Unity Android 使用相同 resolve manifest；APK 安装、商店更新或 MDM 策略由 Android 适配层负责，不调用 Windows Updater。
4. 应用配置必须包含分发服务 HTTPS 基址、`productId`、渠道、平台、架构和 `/api/software-distribution/v1/updates/resolve`。不得包含后台身份凭据。
5. 构建不可变制品。Windows 目录包使用 `directory-zip`，Android 使用 `apk`，Addressables 使用 `unity-addressables`，其他单文件使用 `content`。
6. 调用 `software.distribution.artifact.inspect`。只有返回 `ready=true`、`inspection_receipt`，且文件名、版本、目标、大小和 SHA-256 与预期一致时才能进入发布。预检收据短时有效、绑定当前 AI 会话和工作区，并且只能使用一次。
7. 向用户展示发布摘要：产品、版本、渠道、平台、架构、包类型、文件、大小、SHA-256、是否强制、灰度比例。没有用户本轮明确确认时，不得调用发布能力，也不得自行把 `confirmed` 设为 `true`。
8. 用户确认后调用 `software.distribution.release.publish`，参数必须与检查结果完全一致，并传入 `inspection_receipt`、`expected_size`、`expected_sha256` 和 `confirmed=true`。Agent 仍会显示本机原生确认摘要；模型不得代替用户确认。禁止同版本替换不同内容；内容变化必须提升版本。
9. 发布成功后调用 `software.distribution.release.resolve`，用低于新版本的 `current_version` 验证有更新，再用等于新版本的版本验证 `update=null`。
10. 在目标项目运行真实客户端测试：下载短时 URL、校验大小和 SHA-256，并按平台执行安装或模拟替换。只有连接真实组织分发服务完成 resolve 与下载后，才能报告完整更新链路打通；独立模式应明确报告本地检查已通过。

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

1. 组织分发服务返回 camelCase `{ "update": manifest }`。
2. `manifest.productId` 等于 `com.himind.media-resolver`。
3. 下载大小与 SHA-256 匹配。
4. 旧版本能解析到更新，当前版本返回无更新。
5. Updater 能替换测试目录内容且立即失败时回滚。

## 灰度与回滚

公共匿名 resolve 只消费 `rollout_percent=100` 的 Release。设备级灰度、定向升级和结果上报必须走受管客户端协议，不能用 IP 或随机数代替稳定实例 ID。

发现错误发布时停止继续验证，并要求有权限的管理员在组织分发服务撤销 Release。当前 Skill 不自动撤销、不删除历史制品、不覆盖同版本包。

## 输出格式

返回中文摘要，依次列出：项目识别、客户端接入状态、制品摘要、发布确认、组织分发版本、resolve/download 验证、安装测试、未完成风险。明确区分“本地检查通过”“组织分发集成通过”和“真实环境已发布”。
