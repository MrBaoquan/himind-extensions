---
name: iostudio-debug-assistant
description: 驱动 IOStudio 设备调试工作台：定位安装与生效设备表、查设备动作通道、预检与核对动作文件、执行协议测试场景并解释失败步骤；当用户要求调试 IOStudio、查设备通道、预检动作文件或跑协议测试时使用。
---

# IOStudio 调试助手

IOStudio 是设备调试工作台，动作编排只是其中一个域。本技能负责**调试与排障**：
确认用的是哪一个 IOStudio 和哪一份设备表，把"设备不动""预检报错""协议测试失败"定位到具体环节。

## 它做什么、不做什么

| 做 | 不做 |
| --- | --- |
| 定位 IOStudio、判断生效设备表 | 修改设备表或现场配置 |
| 列设备 / 动作 / 通道，标出不可用动作 | 从视频生成编排（那是动作编排技能的职责） |
| 预检、轨道核对、构建前干跑 | 替代现场机械安全验收 |
| 执行协议测试场景并解释失败步骤 | 在真机上做无人监护的开环试动作 |

**这是调试，不是验收。** 预检通过只代表格式、通道、量程与联锁配置自洽，不代表机械安全合格。

## 0. 先认出用的是哪一个 IOStudio

```text
iostudio.runtime.status {}
```

读回 `cli`、`version`、`config_dir`、`device_table`，三步判断：

1. `available` 为假：先解决安装问题。把返回里的 `message` 原样转述——它已经写明要找的是哪个可执行文件。
2. `device_table` 为空：**停下来**。该安装目录下没有设备表，任何编排预检都会对所有设备报"未声明"。这时换 `cli_path` 指到现场那份，不要让用户去改编排。
3. 本机同时装了多份 IOStudio：`cli_path` 必须显式传。

关键因果：**`cli_path` 决定用哪份安装，而安装目录决定用哪份设备表。** 设备表才是"哪些设备与动作算已声明"的判据，所以换路径等于换了一套校验基准。

## 1. 设备表：唯一可信的通道来源

```text
iostudio.devices { device? }
```

- `oactions[].keys` 是通道名。**写任何编排前先查这里**，不要猜 `OAxis_xx`。
- `usable` 为 false 的动作没有 Key，下发时不会落到任何通道，表现为**设备不动且不报错**。这类动作不能进编排。
- `warnings` 要一并转述：动作缺 Key、表里没有设备、XML 损坏都会出现在这里。
- 报"所有设备都未声明"时，先怀疑设备表，而不是编排。只有个别动作报错才是编排问题。

不传 `device` 返回全部设备；传了就只回该设备，查不到时会附带表里的设备名清单。

## 2. 已有动作文件的排障顺序

按这个顺序走，不要跳步，也不要用 `build` 试探：

```text
iostudio.motion.validate { workspace_root, motion_path }   # 预检：格式、通道、量程、联锁
iostudio.motion.tracks   { workspace_root, motion_path }   # 轨道 → 通道映射，亚秒返回
iostudio.motion.plan     { workspace_root, spec_path }     # 干跑：构建 + 预检，不留产物
```

- `validate` 返回 `ok` 与 `validation.issues`（`code` / `severity` / `message`）。有 `severity=error` 就不得进入构建；warning 与 info 逐条解释或修正。
- `tracks` 不建设备、不开窗口，是核对"这条轨道到底落到哪个通道"的最快手段。通道被静默丢弃时，它通常是最先露馅的地方。
- `plan` 只在规格改动后、正式构建前用。它回报的 `ok` 已含预检结论，但**不回报产物路径**——不要向用户承诺工作区里存在这个文件。
- 用户明确要落地时才构建：

```text
iostudio.motion.build { workspace_root, spec_path, out_path? }
```

三个能力都会在工作区留下隐藏状态文件（如 `.validate-status.json`、`.motion-tracks-status.json`）。它们不是产物，不要提交，也不要当作交付物提及。

## 3. 协议测试

```text
iostudio.protocol.test { workspace_root, scenario_path, report_path?, keep_report? }
```

场景文件格式、可用操作与匹配类型见 `references/protocol-scenario.md`。

- 场景文件必须在 `workspace_root` 内，越界会被直接拒绝。
- 退出码 0 = 全部步骤通过；2 = 有失败步骤。**退出码 2 不是调用失败**——报告照常返回，照常分析。
- 报告默认**不落盘**，内容已结构化回传。只有用户要留证据时才传 `keep_report: true`。
- 返回里的 `failed_steps` 已经只含失败步骤；`report.steps` 才是全量。别把几十步全量复述给用户。
- `ok=false` 且 `message` 提到"未产出报告"，说明场景根本没跑起来（路径、JSON 或转义有问题），不是报文断言失败。
- 每个步骤看 `op`、`via`、`elapsedMs`、`reason`：`reason` 是这一步为什么没过，`elapsedMs` 接近超时说明对端没发报文。

第一次在一个环境里跑协议测试时，先用 IOStudio 安装目录 `Config/ProtocolTests` 下的 loopback 场景（TCP / UDP / WebSocket 自回环）验证链路与权限；这些场景自己起服务端也自己连客户端，不需要外部设备配合。真机侧场景要等外部报文，超时必须给足，失败后不要立刻重跑——先看 `reason` 和耗时。

## 4. 失败对照表

| 现象 | 多半是 |
| --- | --- |
| 所有段落都报设备未声明 | `device_table` 不对；换 `cli_path` 指到现场那份 |
| 单个动作报未声明 | 用了设备表里没有的动作；回 `iostudio.devices` 取真实动作 |
| 预检通过但设备不动 | 通道映射错，或动作缺 Key（`usable=false`）；用 `tracks` 核对 |
| `runtime.status` 说没找到命令行 | 没装 IOStudio、PATH 没有、也没设 `IOSTUDIO_CLI`；让用户给 `cli_path` |
| 协议测试退出码 2 | 场景跑完了但有失败步骤；看 `failed_steps`，不是重跑 |
| 协议测试"未产出报告" | 场景文件路径或 JSON 有错；先校验场景本身 |
| 某个 expect 正好卡在超时 | 对端没按预期发报文；核对端口、方向（`via`）和组帧方式 |
| 想确认某条通道值 | 用 `tracks` 看轨道映射，不要为此建设备或开窗口 |

## 5. 风险门禁

- **不用 `skip_validate`。** 它绕过的是设备声明、动作存在性、量程和联锁检查，正好是这类事故的防线。插件清单里已经没有这个参数——如果别处看到它，说明那份插件版本过旧或不是本仓构建。
- 协议测试会真的收发网络报文与串口数据。对生产设备执行前，先确认没有人在操作、也没有别的会话占用同一端口。
- 只读写 `workspace_root` 内的文件；路径越界会直接拒绝，不值得试探。
- 不修改设备表、不改 `IOStudioSettings.xml`、不改现场配置——这些属于 IOStudio 自身的职责。
- 强动作、首次输出、超限参数仍然要人工现场确认；插件与预检都不替代这一步。

## 6. 与其它技能的分工

- **从视频到动作文件**：由视频动作编排技能负责（媒体证据 → 行为时间线 → 动作意图 → 三档规格 → 构建预检）。它自己会调用构建、预检与轨道核对，本技能不重复那条流程。
- **本技能**：接管"已有产物或已有现场，出了问题"这一类请求——查设备、核对通道、预检、跑协议测试、定位失败步骤。
- 用户要改的是演出内容时，交回动作编排技能；要改的是设备表或协议对端时，交回 IOStudio 本身。

## 参考

- `references/protocol-scenario.md`：协议测试场景格式、可用操作与匹配类型
