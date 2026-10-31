# 视频动作编排链路与拓展生态

## 1. 问题定义

当前链路已经能把视频变成 `.motion`，但现场反馈暴露出一个结构性问题：**大部分片段动作过于平缓，画面内容和平台体感之间缺少可复核的中间层。** 直接让模型从画面信号跳到关键帧，会得到“看起来一直在动，但没有戏剧起伏”的结果。

目标不是让模型逐帧复刻画面，而是形成一条可重复的链路：

```text
媒体理解 → 行为时间线 → 动作意图 → 设备能力映射 → 变体编译 → IOStudio 预检/模拟 → 现场反馈
```

## 2. 当前链路

```text
media-toolkit
  probe / scene.score / audio.loudness / frames.contact-sheet / frames.extract
        ↓
media-ai
  transcribe（可选）
        ↓
video-motion-authoring
  行为时间线 / 动作意图 / 失重语法 / 三档策略
        ↓
ShowSpec
        ↓
motion-composer
  audit / compare（确定性质量门禁）
        ↓
iostudio
  motion.plan / motion.build / motion.validate / motion.tracks
        ↓
.motion + IOStudio / UNIPlayer
```

### 当前成熟点

- 媒体工具和 IOStudio 都有稳定的结构化能力。
- 构建、预检、轨道核对和仿真可形成闭环。
- 动作词汇表已经覆盖一批常见影视/飞行动作。
- 风、水、热、震动、香氛等环境通道可以被实际设备声明。

### 当前缺口

1. 没有正式的 `behavior timeline` 和 `motion intent` 产物。
2. 没有全景主前向分析的默认流程，平面拼图会制造错误语义。
3. 动作词汇偏静态模板，缺少“升力 → 失重 → 回弹 → 收势”的节拍语法。
4. 没有面向现场的 gentle/normal/intense 变体协议。
5. 没有按段落评价“是否太平缓”的可计算指标。
6. 视频语义分析仍主要依赖模型看拼图，缺少镜头、主体、动作和镜头运动的结构化能力。
7. 现场反馈没有回写到可重复的编排参数。

## 3. 目标分层与契约

### L1 媒体证据层

能力：

- `media.toolkit.probe`
- `media.toolkit.video.project`（新增：全景主前向）
- `media.toolkit.recipe.plan/run`
- `media.toolkit.job.status`
- `media.toolkit.scene.score`
- `media.toolkit.audio.loudness`
- `media.toolkit.frames.contact-sheet`
- `media.toolkit.frames.extract`
- `media.toolkit.preview.image`
- `media.ai.transcribe`（可选）
- 后续：镜头检测、光学流、动作识别、音频事件分类

产物：

- 代理片与主前向视图
- 时间戳信号
- 带证据时间点的视觉/音频摘要

### L2 行为时间线

契约：`references/behavior-timeline.schema.json`

记录：

- 来源、时长、投影方式、主前向参数
- 段落边界、场景、行为标签
- 事件时间点、来源和置信度
- 证据帧、信号说明、可选转写证据
- 高风险段落

### L3 动作意图

契约：`references/motion-intent.schema.json`

记录：

- 设备能力约束
- gentle/normal/intense 三档参数
- 每段的动作意图、能量、Heave 形状、环境门控和香氛窗口
- 置信度和必须人工确认的项

动作意图不直接包含设备通道名；设备和通道绑定在下一步完成。

### L4 设备映射与变体编译

契约：`references/device-profile.schema.json`

输出：

- 设备 profile
- 角色到 `OAction/OAxis` 的显式映射
- bool/float、量程、中性、限速
- 三档 ShowSpec 和 `.motion`

当前已有的实现点：

- `engineering_range` 将归一化值转换为工程量
- `value_type=bool` 只能输出 0/1
- `value_type=float` 可用于风速等连续通道
- IOUI/MP-YDL 已支持 `FanStrength 0–1`

### L5 执行与验证

- `iostudio.motion.plan`
- `iostudio.motion.build`
- `iostudio.motion.validate`
- `iostudio.motion.tracks`
- Simulator/现场预览
- 设备运行状态、未解析通道、限速通道、tick 错误

## 4. 失重编排原则

Heave-only 设备的目标不是“持续动”，而是形成可感知的力变化：

```text
准备 → 快速抬升/蓄势 → 高位停留 → 快速下降 → 回弹 → 恢复
```

建议动作原语：

- `lift`
- `float`
- `drop`
- `rebound`
- `impulse`
- `oscillation`
- `settle`

动作词汇表只负责选择动作家族；具体时间点、包络和峰值由编排器计算。禁止让模型直接手写最终通道关键帧作为唯一事实源。

## 5. 强度分级协议

三档必须保持相同的片长、段落和事件时点，只改变可控参数：

| 参数 | gentle | normal | intense |
| --- | --- | --- | --- |
| Heave 偏差 | 0.7× | 1.0× | 1.15× |
| 震动门控 | 缩短 | 基准 | 延长 |
| 风机 | 低档 | 中档 | 高档 |
| 香氛 | 保留关键窗口 | 完整窗口 | 可增加转场窗口 |

现场验证顺序固定为 gentle → normal → intense。每一档都记录动作同步、失重感、舒适度、疲劳度、环境氛围和机械风险。

## 6. 质量门禁

### 自动门禁

- 所有通道必须来自设备声明
- cue 连续、无重叠、无越界
- 相邻 cue 数值连续
- 三档结构一致
- `motion.plan/build/validate/tracks` 全绿
- 播放时 `unresolvedChannels` 为空

### 编排质量指标

每个段落至少计算：

- `peak_heave`
- `dynamic_range`
- `active_ratio`
- `event_alignment`
- `continuity_gap`
- `max_static_gap`

这些指标用于发现“动作存在但没有戏剧起伏”的段落。阈值因设备、影片和场景而异，必须结合现场反馈校准，不能写死成所有内容通用的绝对值。

### 人工门禁

- 主前向和投影方式确认
- 高风险段落确认
- 机械行程、噪声、急停和观众体感确认
- 真实设备执行前的安全评审

## 7. 拓展生态路线

### 已具备

- `media.toolkit`: 代理、探片、全景投影、信号、拼图
- `media-ai`: 转写
- `video-motion-authoring`: 媒体证据 → 行为时间线 → 动作意图 → ShowSpec 的编排知识
- `motion-composer`: ShowSpec 质量门禁与三档一致性比较（已落地 1.0.0）
- `iostudio`: 官方构建、预检、轨道核对
- `IOUI/MP-YDL`: 实际设备通道和风速协议

### 下一步

1. **motion-composer 1.0 已落地质量门禁**
   - 输入 ShowSpec + behavior timeline + device profile
   - 输出 `high_energy_flat`、`no_weightless_pulse`、`drop_rate_exceeds_slew`、`continuity_gap` 和三档时序漂移
   - 后续版本再扩展“从 intent 确定性生成 ShowSpec”的编译能力

2. **视频语义分析能力**
   - 镜头边界/镜头运动
   - 主体检测与跟踪
   - 动作识别
   - 音频事件分类
   - 对低置信度标出证据帧，不强行给结论

3. **现场反馈数据**
   - `field-feedback.json`
   - 按段落记录评分与调整项
   - 下一轮只改对应动作意图，不直接改最终 `.motion`

4. **设备能力 profile**
   - 由 IOStudio 设备表生成或校验
   - 明确 bool/float、量程、中性、限速、安全能力和环境特效

## 8. 结论

视频动作编排不应是一条“模型看片 → 生成关键帧”的单跳链路，而应是由多个可验证层组成的内容生产生态：

```text
证据 → 行为 → 意图 → 设备映射 → 变体 → 预检 → 模拟 → 现场反馈
```

这套分层能让现场反馈回到正确的层：如果大部分片段太平缓，应修改动作意图和编排参数，而不是在最终 `.motion` 上做不可复现的手工补丁。

## 9. idle 的适用边界

IOStudio 的 idle 循环是**clip 空窗兜底**。运行时先求值 clip；只要时间落在 clip 内，就直接返回 clip 曲线，不会叠加或回退到 idle。

因此：

- 长 preset 内部的事件空隙，不能靠 idle 解决；
- 事件驱动编排切成短 clip 后，可以用 idle 填 cue 之间；
- 更自然的方案是整段 clip 内加入低幅、非周期飞行底噪，idle 只处理真正未编排的空窗。

质量门禁新增 `motion.long_static_gap`，默认禁止预设内部超过 3000ms 没有可感知变化。

## 10. 案例校准：v4、v5 与 v6

《飞跃雪山》正常版的同一质量门禁结果：

| 指标 | v4 正常 | v5 正常 | v5 激烈 |
| --- | ---: | ---: | ---: |
| 合格失重脉冲 | 1 | 21 | 22 |
| 高能偏平段 | 5 | 0 | 0 |
| 平均动态范围 | 0.341 | 0.534 | 0.575 |
| 最大下降斜率 | 1.200/s | 0.693/s | 0.822/s |

v4 的问题不是峰值不够，而是多数段落在做平滑往返；v5 把下降谷底绑定到画面/声音事件，并通过压缩“高位停留→下降”的时间突破旧 0.5/s 配置。0.9/s 是待现场分级验证的候选工作点，不是生态默认常量。

片尾另设独立复位阶段：247.5s 前全部环境特效关闭，248.5s 开始 Heave 复位，249.7s 到中性并保持到 250.933s。`motion-composer 1.1.0` 会把未回中或特效未关闭判定为 Error。

v6 保持 v5 的事件峰值与高斜率，但加入 clip 内飞行底噪。同一正常档：

| 指标 | v5 | v6 |
| --- | ---: | ---: |
| 最长静止区 | 12.9s | 2.4s |
| 超过 3s 的静止段 | 11 | 0 |
| 合格失重脉冲 | 21 | 21 |
| 最大下降斜率 | 0.693/s | 0.693/s |

这说明“持续感”和“事件冲击”不是互斥的：底噪负责飞行连续性，峰值负责失重叙事。
