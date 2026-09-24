# 软件分发与发布

把已构建的软件制品接入组织分发、校验、发布并验证更新链路。面向 WPF、Unity Windows、Unity Android 与其它单文件制品。

## 为什么用工作流而不是一次对话

- 发布是**外发写操作**，需要审批、留痕和可追溯的版本事实。
- 制品必须**不可变**：同一版本不允许替换不同内容；发布前的预检收据把「校验过的制品」和「发布的制品」绑定成同一份摘要。
- 发布后必须验证「旧版本能拿到更新、当前版本返回无更新」，并记录目标端真实安装结果。

## 步骤

| 步骤 | 类型 | 风险 | 说明 |
| --- | --- | --- | --- |
| SD-PROJECT 识别项目与分发接入状态 | capability | 只读 | `software.distribution.project.inspect`，输出项目类型与客户端接入建议 |
| SD-ARTIFACT 校验制品并签发预检收据 | capability | 只读 | `software.distribution.artifact.inspect`，计算大小与 SHA-256，签发**一次性**收据 |
| SD-PUBLISH 发布到组织渠道 | capability | R3（需审批） | `software.distribution.release.publish`，使用 Agent 内部短时委托身份 |
| SD-RESOLVE 验证线上更新解析 | capability | R3（需审批） | `software.distribution.release.resolve`，验证旧版本有更新、当前版本无更新 |
| SD-ACCEPT 目标端安装与验收 | manual | R3（需审批） | 真实设备安装/替换结果的人工确认，失败即组织回滚 |

## 入口与出口（segmented）

入口：`inspect`（接入检查）、`artifact`（制品校验）、`publish`（发布）、`verify`（线上验证）。
出口：`integration_ready`、`artifact_ready`、`released`、`verified`、`accepted`。

支持从阶段入口开始，例如只做「制品校验」，或对已发布版本只做「线上验证」。

## 发布凭据为什么要手工带入

`release.publish` 要求 `inspection_receipt`、`expected_size`、`expected_sha256` 作为输入。当前引擎只会把**步骤自身声明的字段**传给能力（严格 Schema 会剥离 `workflow_context`），因此上一阶段的收据不会自动注入，需要从校验结果复制到「发布凭据」分组。

这是当前平台的已知摩擦点，改进方向是让引擎支持步骤输出绑定（例如 `input.inspection_receipt = "steps.SD-ARTIFACT.result.inspection_receipt"`）。在支持之前，请按两阶段使用：先跑 `artifact` 入口拿收据，再跑 `publish` 入口发布；收据短时有效且只能使用一次。

## 审批次数说明

发布与线上验证两步都需要审批，这不是重复设计：平台按能力风险等级判定门禁，`release.publish` 是网络写操作、`release.resolve` 被平台判为 R3 网络能力，两者都必须经过审批中心。如果本次只想发布不验证，可从 `publish` 入口运行并在 `released` 出口结束。

## 依赖与运行模式

- 插件：`com.himind.software-distribution`（提供 inspect / resolve 能力）。
- `software.distribution.release.publish` 属于 Dashboard 控制面能力，需要 **Connected 模式**与发布授权（`RELEASE_MANAGE_SCOPE`）。Independent 模式下该步骤不可用，Preflight 会阻断。
- 凭据由 Agent 内置 broker 以短时委托身份获取，AI、插件与工作流输入都不接触 Token。

## 灰度与回滚

- 公共匿名 resolve 只消费 `rollout_percent=100` 的 Release；设备级灰度必须走受管客户端协议，不能用 IP 或随机数代替稳定实例 ID。
- 本工作流不自动撤销 Release。发现错误发布时，停止后续验证并请有权限的管理员在 Dashboard 撤销；不要删除历史制品，也不要覆盖同版本包。

## 验收标准

1. 制品校验返回 `ready=true` 且大小、SHA-256 与本地一致。
2. 发布参数与校验结果完全一致，`confirmed=true` 由使用者显式提供，并额外经过审批中心。
3. 低于新版本的 `current_version` 能解析到更新；等于新版本的返回无更新。
4. 目标端真实下载并校验摘要，安装或替换成功；失败时记录并触发回滚。

Contract Double 与临时 Dashboard 集成通过，不等于真实环境已发布；结论必须区分「本地测试通过」「临时集成通过」「真实环境已发布」。
