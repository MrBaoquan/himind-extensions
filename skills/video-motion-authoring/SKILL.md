---
name: video-motion-authoring
description: 根据视频画面与声音分析行为、生成平台动作意图、三档演出规格并驱动 IOStudio 构建预检；当用户要求按视频编动作、配动感平台/4D 特效或生成 .motion 时使用。
---

# 视频动作编排助手

把视频变成平台动作文件，但不是“看图直接写关键帧”。必须经过：

```text
媒体证据 → behavior-timeline.json → motion-intent.json → ShowSpec → .motion
```

AI 负责语义判断、动作意图和不确定性标注；确定性代码负责时间量化、包络、通道映射、限速和连续性。

## 它做什么、不做什么

| 做 | 不做 |
| --- | --- |
| 分析镜头/段落/事件、找动作时点、选动作家族、给强度初值 | 帧级精修 |
| 生成行为时间线和平台无关动作意图 | 判断现场机械实际行程 |
| 生成 gentle/normal/intense 三档 ShowSpec | 替代安全评审 |
| 驱动 IOStudio 构建、预检、轨道核对 | 用 `skip_validate` 绕过安全门禁 |

**这是粗排，不是终稿。** 必须让作者复核行为时间线、动作意图和强度档位后再落地。

## 必要输入

- 视频/音频文件，位于调用方给出的 `workspace_root` 内
- IOStudio 安装及目标设备表
- 可选：转写模型、全景主前向参数、设备 profile

## 关键前提：设备表决定一切

设备表说明有哪些设备/动作/通道，以及值类型、量程和限速：

```text
iostudio.runtime.status {}
iostudio.devices        {}
```

通道名必须来自 `iostudio.devices`。不要猜 `OAxis_xx` 或 `OAction`。若动作没有 Key、通道是 bool 还是 float、工程范围如何，都必须记录。

### 环境通道

- bool 通道只能 0/1。
- float 通道可表达连续值，但必须配置正确的 `engineering_range`。
- 例如风机风速：`OAxis_18` 使用 `value_type=float`、`engineering_range=100`、gate level `0.65`，输出才是 65% 而不是 0/1。
- 水、热风、震动、香氛通常按开关量处理；香氛要短而明确，不要长时间常开。

## 全景与主视觉

如果影片是等距柱状/球幕全景：

```text
media.toolkit.video.project {
  workspace_root, path, output,
  yaw_deg: 0, pitch_deg: 0,
  h_fov_deg: 100, v_fov_deg: 65,
  width: 640, height: 360
}
```

后续探片、拼图、信号分析全部使用这个主前向代理片。不要用 2:1 平铺图的左右或前后位移直接推导平台姿态。平台动作跟随主叙事视觉，不跟随观众自由转头。

## 本地开发与安装顺序

```text
1. 构建 motion-composer 插件 .hmpkg
2. 将插件安装/注册到本地 HiMind 运行时
3. 确认 motion.composer.runtime.status 可用
4. 构建 video-motion-authoring 技能 .hmskill
5. 启用技能并运行候选测试
```

`.hmpkg` 与 `.hmskill` 只是构建产物，不等于已经安装。构建技能不需要插件二进制进入技能包，但**启用技能前**必须完成依赖插件安装和运行时注册。依赖未安装时预检失败是正确行为，不得通过删除 `required` 绕过。

## 工作流程

### 0. 前置检查

```text
media.toolkit.runtime.status {}
iostudio.runtime.status      {}
```

另检查 `media.ai.runtime.status` 与 `motion.composer.runtime.status` 是否可用。`motion-composer` 是必需质量门禁：未安装时停止，先构建并安装该插件，再构建或启用本技能。

### 1. 查目标设备能力

```text
iostudio.devices {}
```

记录：

- 设备与动作
- 每个动作的通道
- bool/float
- 量程/中性/限速
- 环境动作是否可用

将设备能力落成 `device-profile.json`，参考 `references/device-profile.schema.json`。

### 2. 探片与代理片

```text
media.toolkit.probe { workspace_root, path }
```

按码率/分辨率决定是否造分析代理片：

```text
media.toolkit.recipe.run { workspace_root, kind: "proxy", recipe: { ... } }
```

`recipe.run` 是异步作业。必须轮询：

```text
media.toolkit.job.status { job_id }
```

直到 `completed` 且产物存在后才能用于后续分析。

### 3. 主前向与全片概览

全景先用 `media.toolkit.video.project`。平面视频直接使用源片或代理片。

生成全片和关键段拼图：

```text
media.toolkit.frames.contact-sheet {
  workspace_root, path, output,
  columns: 8, rows: 6, tile_width: 320
}
```

需要看细节时：

```text
media.toolkit.frames.extract { workspace_root, path, output_dir, times_ms }
```

若宿主不能直接打开文件，使用：

```text
media.toolkit.preview.image { workspace_root, path }
```

### 4. 信号与转写

```text
media.toolkit.scene.score    { workspace_root, path, max_points: 2000 }
media.toolkit.audio.loudness { workspace_root, path, max_points: 2000 }
```

信号只用于找候选时点，不能单独决定“发生了什么”。有对白/歌词时：

```text
media.ai.transcribe { workspace_root, path, language: "zh" }
media.ai.job.status { job_id }
```

转写同样要轮询到完成。看 `content_segments`、`beats_ms`、`full_text` 和 `vocal_ratio`。

### 5. 产出行为时间线

写 `behavior-timeline.json`，符合 `references/behavior-timeline.schema.json`。

每段至少包含：

- `start_ms` / `end_ms`
- `behavior` / `scene`
- `energy`
- `confidence`
- 证据时间点和说明

事件包含类型、时间、来源、强度和置信度。低置信度必须明确标出，不要用补全代替观察。

全景来源必须写清投影类型、主前向 yaw/pitch/FOV。

### 6. 生成动作意图与三档

按 `references/choreography-playbook.md` 写 `motion-intent.json`，符合 `references/motion-intent.schema.json`。

重点：

- 选动作家族，不直接写最终通道关键帧
- Heave 失重序列：`准备 → 抬升/蓄势 → 停留 → 下降 → 回弹 → 恢复`
- 环境动作要有明确场景语义
- 香氛只安排短窗口
- 长段事件之间加入低幅飞行底噪，禁止只要峰值、不填平缓空窗
- idle 只用于 clip 之外的真实空窗，不能替代 preset 内部的持续运动
- 同时生成 `gentle` / `normal` / `intense` 三档参数

三档保持片长、段落和 cue 时点一致，可改变：

- Heave 幅度/斜率
- 震动门控密度
- 风机档位
- 环境特效窗口

### 7. 编译 ShowSpec

按 `references/show-spec.md` 生成 spec，放在视频旁边。动作意图映射到设备 profile 的实际角色和通道。

规则：

- `pose` 写 presets + cues
- `gate` 只写 gates
- 离散环境通道用 0/1；风速等 float 通道使用 `engineering_range` 和 gate level
- 同一轨道 cue 不得重叠或越界
- 相邻 cue 数值连续
- 所有通道必须来自设备表
- 不得用 `skip_validate` 绕过预检（插件清单已移除该参数，能力层也不接受）
- 片尾必须有复位策略：所有环境 gate 提前关闭，Heave 回到 `neutral` 并保持

### 8. 编排质量门禁

ShowSpec 写完后，必须先过确定性质量门禁，再交给 IOStudio：

> `motion-composer` 缺失时不得继续启用技能或进入正式构建。先安装插件；不要用“手工检查”冒充自动门禁通过。

```text
motion.composer.audit {
  workspace_root,
  spec_path,
  behavior_timeline_path,
  device_profile_path
}
```

三档齐全后：

```text
motion.composer.compare {
  workspace_root,
  spec_paths: [gentle, normal, intense],
  behavior_timeline_path,
  device_profile_path
}
```

`ok=false` 时不得进入构建。重点看：

- `high_energy_flat`：高能段有峰值但没有足够的动态范围
- `no_weightless_pulse`：长段没有可识别的抬升→下降→回弹
- `drop_rate_exceeds_slew`：下降斜率超过设备声明
- `continuity_gap`：相邻 cue 边界跳变
- `long_static_gap`：preset 内部长时间没有可感知变化
- `end_not_neutral`：片尾 Heave 未回中
- `end_effects_not_closed`：片尾仍有风机/水/热风/震动/香氛开启
- `same_cue_timing=false`：三档的节拍结构漂移

门禁通过只代表编排语义合格，不代表机械安全合格。片尾至少保留 `end_shutdown_margin_ms`（默认 1500ms）的关机余量。

### 9. 干跑和人工复核

```text
iostudio.motion.plan { workspace_root, spec_path }
```

把 behavior timeline、动作意图、三档峰值和 validation.issues 一起给作者看。每个 issue 必须解释或修正，有 Error 时不得继续。

除了 IOStudio 的格式/通道检查，还要做编排质量检查：

- 高能段落是否存在明确峰值
- 是否有整段低幅无事件的“平缓噪声”
- 事件时点是否有画面/声音证据
- 三档是否结构一致
- 连续段落是否有数值和速度跳变

### 10. 构建、预检与轨道核对

复核通过后：

```text
iostudio.motion.build    { workspace_root, spec_path, out_path }
iostudio.motion.validate { workspace_root, motion_path }
iostudio.motion.tracks   { workspace_root, motion_path }
```

三档都要构建，分别核对。报告：

- `device_table`
- `validation`
- 轨道映射
- 播放时间/错误/未解析通道
- 三档参数矩阵
- 需要现场确认的高风险段

### 11. 现场反馈

按 gentle → normal → intense 顺序验证。每个版本回中复位。

按 `references/field-feedback.schema.json` 记录动作同步、失重感、舒适度、疲劳度、环境氛围、香氛时点和机械风险。反馈回到 `motion-intent.json` 对应段落，不直接手改最终 `.motion`。

## 风险门禁

- 只配设备表真实声明的通道。
- 大幅动作前确认 SlewRate。当前配置值来自设备 profile，不是不可变物理上限；三档可以提高，但必须按 `references/slew-rate-validation.md` 分级测试。
- 动作文件只约束下降斜率，实际上限由 IODevice 设备表的 `SlewRate` 决定；0.9/s 仅是示例候选。
- 首次输出、配置变更和更极限值都必须先空载验证，不得用 `skip_validate` 绕过。
- 每个版本结束时必须关闭全部环境特效并把 Heave 回中；不能只依赖播放器停止或人工急停。
- 开场不要放强动作。
- 涉及联锁时检查使能先行、急停不重叠。
- 三档必须先测试平缓，再测试正常/激烈。
- `iostudio.motion.validate` 当前不等于完整机械安全验收，现场仍需人工确认。
- 任何物理动作不得用自动构建替代现场评审。

## 参考

- `references/behavior-timeline.schema.json`
- `references/motion-intent.schema.json`
- `references/device-profile.schema.json`
- `references/orchestration-profile.example.json`
- `references/choreography-playbook.md`
- `references/show-spec.md`
- `references/action-vocabulary.json`

## 遇到问题时

| 现象 | 多半是 |
| --- | --- |
| 大部分片段太平缓 | 动作意图仍在使用低幅正弦/模板；先跑 `motion.composer.audit`，重建失重脉冲和强度档位 |
| 画面和动作不像 | 投影/主前向错了，或没看 1 秒级分镜 |
| 所有段落都报设备未声明 | 设备表不对，看 `device_table` |
| 某个动作报未声明 | 改用设备表真实动作，不猜通道 |
| 风机没有风速 | 检查是否被建为 bool；应使用 float + `engineering_range=100` |
| 香氛一直开 | 门控窗口过长；改为和转场对齐的短窗口 |
| 构建成功但设备不动 | 通道映射错误或输出被静默丢弃，回到设备表核对 |
| 质量门禁报高能平缓 | 峰值可能不低，但缺少短时下降和回弹；增加脉冲而非整体放大正弦 |
| 三档只有幅度不同但体感没差异 | 风、震动、环境窗口也需随档位变化，且检查下降斜率 |
| 转写只有占位文本 | 显式指定 language，检查 `content_segments` |

现场排障不走这条流水线：设备表判定、通道核对、协议测试失败定位交给 `iostudio-debug-assistant` 技能。
