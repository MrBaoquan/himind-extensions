# 动作编排质量门禁

在 **motion-intent / ShowSpec** 与 **IOStudio 构建**之间做确定性审计。它不负责生成动作文件，也不替代 IOStudio 的通道、量程、联锁和首帧安全校验。

## 为什么需要它

“构建成功、0 Error”只能证明规格合法，不能证明现场体验成立。一个 30 秒段落可以有 0.5 的峰值、持续在动，却只是低幅正弦，观众感受到的仍然是平缓。

本插件把以下经验变成可执行门禁：

- 高能段落必须有足够的 Heave 动态范围；
- 10 秒以上段落至少包含一次可识别的抬升→下降→回弹脉冲；
- 下降速度不能超过当前 `device-profile.max_slew_rate`；该值是设备配置，不是永久物理上限；
- 相邻 cue 的边界值不能大幅跳变；
- bool 环境通道只能使用 0/1，float 通道必须有量程；
- 三档变体必须保持片长、cue 数量和 cue 时点一致；
- 片尾 Heave 必须回到中性，全部环境特效必须提前关闭。

## 能力

```text
motion.composer.runtime.status {}
motion.composer.audit {
  workspace_root, spec_path,
  behavior_timeline_path?, device_profile_path?, thresholds?
}
motion.composer.compare {
  workspace_root, spec_paths, behavior_timeline_path?, device_profile_path?, thresholds?
}
motion.composer.platform.resolve {
  workspace_root, platform_profile_path, motion_intent_path, device_profile_path
}
```

输入路径必须位于 `workspace_root` 内。`behavior_timeline_path` 提供段落能量后，才能判断“高能段是否平缓”。

`motion.composer.platform.resolve` 把动作意图映射到具体 IOUI 平台：

- `exact`：平台具备精确对应轴；
- `approximate`：使用替代轴表达，必须人工审批；
- `unsupported`：缺少必要能力，禁止生成 ShowSpec。


## 指标

- `peak_heave`：段内最高归一化位置
- `dynamic_range`：最高与最低位置之差
- `active_ratio`：高于起始基准 0.08 的时间占比
- `max_down_rate_per_second`：最大下降斜率
- `strong_pulses`：满足幅度、下降时间和回弹条件的脉冲数
- `continuity_gap`：相邻 cue 的边界值差
- `end_not_neutral`：动作结束时 Heave 未回中
- `end_effects_not_closed`：风机/水/热风/震动/香氛在片尾仍然开着
- `long_static_gap`：动作曲线在预设内部长时间没有可感知变化
- `evidence_aligned`：下降谷底是否落在行为时间线事件附近
- `weak_high_energy_segments`：有高能量但没有明确动作峰值的段数

## 边界

- 这是编排质量门禁，不是机械安全验证。
- 默认要求所有 gate 在片尾前至少 1500ms 关闭；可用 `end_shutdown_margin_ms` 调整。
- 默认静止区间上限 3000ms；可用 `max_static_gap_ms` 调整。
- IOStudio idle 只填充 clip 外空窗。clip 内部的持续感由飞行底噪负责，不能只靠 idle。
- `max_slew_rate` 必须来自当前设备 profile。调整配置后先做空载分级验证，再重新审计；插件不会内置某个固定斜率作为行业真理。
- 阈值必须结合设备、节目和现场反馈校准；默认值只适合作为首轮粗排。
- 审计通过后仍必须执行 `iostudio.motion.plan/build/validate/tracks`，并进行静音预览和现场人工确认。
- 插件不读取或修改 IOStudio 设备表。
