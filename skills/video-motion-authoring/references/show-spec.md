# 演出规格（spec）字段说明

spec 是**面向脚本的紧凑编排输入**，由 IOStudio 展开成合法的 `.motion`。
它把「轨道 / 预设 / 节拍 / 门控 / 章节 / 事件」分开表达，避免手工拼 `.motion` 时那些
靠人肉保证的结构约束（clip 跨度、实例与预设一致、同轨重叠……）。

> 本文件是写 spec 时的字段参考。它由 IOStudio 的 `ShowSpec` 模型而来。
> **真正的权威是 IOStudio 自己** —— 写完必须用 `iostudio.motion.plan` 过一遍预检。

## 顶层

```jsonc
{
  "name": "影片名",
  "duration_ms": 226300,      // 影片时长
  "fps": 60,
  "device": "YDL-0",          // 目标设备名，应用到所有未单独覆盖的轨道

  "tracks":   [],   // 轨道：role(角色) → 通道
  "presets":  [],   // 可复用动作词汇
  "cues":     [],   // 节拍表：某时刻触发某个预设
  "gates":    [],   // 环境门控：特效通道的开关窗口
  "clips":    [],   // 预设/门控之外的逐通道原始曲线
  "chapters": [],   // 章节标记（给人看，不影响播放）
  "events":   []    // 事件（游戏状态宣告等）
}
```

## tracks —— 角色到通道的绑定

```jsonc
{
  "role": "heave",            // 逻辑角色；cues/gates 靠它定位
  "channel": "OAxis_06",      // 通道名。**必须来自 iostudio.devices 的实际声明**
  "output_type": "oaxis",     // "oaxis"（直连通道）| "oaction"（按动作名下发）
  "oaction": "",              // output_type=oaction 时填动作名
  "label": "电缸升降",
  "color": "#4FC3F7",
  "group": "平台机构",
  "value_type": "float",      // "float" | "bool"
  "neutral": 0,               // 中性/空闲值（float 常为 0.5；开关量为 0）
  "engineering_range": 1000,  // 工程量程（毫米/度），影响显示与限速换算
  "idle": { }                 // 可选：空闲时的待机循环（见下）
}
```

`output_type` 二选一：

- `oaxis`：`channel` 直接写通道名（如 `OAxis_06`）。写**轨道**时最常用。
- `oaction`：写 `oaction` 为设备表里的动作名（如 `Heave`）。

> **限速差异**：`SlewRate` 配在设备表的物理 Key 上，两种写法都会生效（插件已修），
> 但 `oaxis` 是现场验证最多的路径，不确定时优先用 `oaxis`。

### idle 待机循环

暂无动作段落时，让该轨道做小幅周期运动（"呼吸"）。**idle 只在没有 clip 覆盖的空窗生效；clip 内部即使曲线完全水平，idle 也不会参与。**

```jsonc
"idle": {
  "period_ms": 4000,
  "blend_ms": 500,              // 与主段落之间的淡入淡出
  "phase_mode": "continuous",   // 相位连续，循环之间不跳变
  "keyframes": [
    { "time_ms": 0,    "value": 0.04, "interpolation": "ease_in_out" },
    { "time_ms": 1000, "value": 0.08, "interpolation": "ease_in_out" }
  ]
}
```

## idle 不能解决什么

- 不能填补一个长 preset 内部的水平曲线；
- 不能给正在播放的 `clips` 叠加第二层动作；
- 不能替代事件之间的飞行底噪。

若需要全程持续感，应在 preset 内部生成低幅“飞行底噪”，并只为真正的无 clip 空窗配置 idle。

## presets —— 可复用动作

把词汇表里的动作写成预设，再用 `cues` 在多个时刻复用。

```jsonc
{
  "id": "impact_hit",              // cues 靠它引用
  "name": "受击",
  "category": "事件",
  "description": "急降回弹，配合震动",
  "duration_ms": 2000,
  "default_intensity": 0.8,        // 预设自带的默认强度
  "min_intensity": 0.2,
  "max_intensity": 1.2,
  "channels": [                    // 每个角色一条关键帧序列
    {
      "role": "heave",
      "scale": 1.0,
      "phase_offset_ms": 0,
      "keyframes": [
        { "time_ms": 0,    "value": 0.5,  "interpolation": "ease_in_out" },
        { "time_ms": 400,  "value": 0.05, "interpolation": "ease_in_out" },
        { "time_ms": 1100, "value": 0.72, "interpolation": "ease_in_out" },
        { "time_ms": 2000, "value": 0.3,  "interpolation": "ease_in_out" }
      ]
    }
  ]
}
```

`interpolation` 可取 `linear` / `step` / `ease_in_out`。**用 `ease_in_out` 起步**
——`step` 是阶跃，会把机构顶到速率上限；只有开关量或明确要瞬时跳变时才用。

## cues —— 节拍表

```jsonc
{ "time_ms": 30000, "preset": "impact_hit", "intensity": 1.0 }
```

`intensity` 会缩放该预设内所有关键帧**相对中性的偏移量**（不是绝对值），
所以同一个预设可以用不同强度复用。取值范围看预设的 `min/max_intensity`。

## gates —— 环境特效开关窗口

```jsonc
{ "role": "wind", "start_ms": 16000, "end_ms": 30000, "fade_ms": 0, "level": 1 }
```

环境类通道（风扇/喷水/震动/香味/灯光…）是**开关量**，用 gates 表达，
**不要用曲线**去模拟强度。`fade_ms` 为 0 是硬切（冲击类常用），
需要柔和过渡时给几百毫秒。

### 环境通道的 value_type 与 gate level

环境门控的 `level` 是**归一化值**，最终工程量由轨道类型和 `engineering_range` 决定。

- `value_type: "bool"`：只能使用 `level: 0` 或 `level: 1`。香氛、水、热风、震动通常是这一类。
- `value_type: "float"`：可以用 `level: 0..1` 表达连续档位，但必须配置正确的量程。
- 风机风速不能建成 bool，否则只能输出开/关。

YDL 风机的正确轨道示例：

```json
{
  "role": "wind",
  "channel": "OAxis_18",
  "output_type": "oaxis",
  "value_type": "float",
  "neutral": 0,
  "engineering_range": 100
}
```

对应门控：

```json
{ "role": "wind", "start_ms": 64000, "end_ms": 80000, "fade_ms": 0, "level": 0.65 }
```

这里的 `0.65` 在 `engineering_range=100` 时表示 65% 风速。若误写为 bool，或漏掉量程，输出会被压成 0/1，现场通常表现为“风机开了但没有风速层次”。

香氛应使用**短窗口**与场景转义点对齐，例如木屋、水面或终章；不要从进入场景一直常开到片尾。


### 片尾复位与特效关闭

每个动作文件都应把“结束”作为独立阶段，而不是让最后一帧停在动作中间：

- Heave 在片尾前回到轨道 `neutral`；
- 风机、水、热风、震动、香氛在所有事件结束后关闭；
- 至少给 `motion-composer` 默认的 1500ms 关机余量；
- 需要更严格时，在 `motion.composer.audit` 中提高 `end_shutdown_margin_ms`。

示例：

```text
247.5s  全部环境 gate 关闭
248.5s  Heave 开始复位
249.7s  Heave 到达 neutral=0
250.933s 片尾保持中性
```

门禁错误码：

- `motion.end_not_neutral`：结束时 Heave 没回中
- `motion.end_effects_not_closed`：片尾仍存在开启的特效窗口

## clips —— 逐通道原始曲线

预设与门控之外还需要手工曲线时用（如速度级别、复位/急停脉冲）。值**不按强度缩放**。

```jsonc
{
  "role": "speed",
  "start_ms": 0, "end_ms": 72000,
  "keyframes": [ { "time_ms": 0, "value": 1.0, "interpolation": "linear" } ]
}
```

## chapters —— 章节标记

```jsonc
{ "time_ms": 16000, "name": "追击行进", "note": "风扇常开 + 射击脉冲" }
```

给人看的段落划分。**把 AI 的推断依据写进 `note`**，作者复核时最需要看的就是这个。

## events —— 事件

```jsonc
{
  "time_ms": 6000,
  "name": "ydl.gameState",
  "data": "{\"GameState\":\"Playing\"}",
  "lane": "游戏状态",
  "color": "#22c55e",
  "data_type": "json"
}
```

游戏状态宣告（Playing / Pause / GameOver）等按设备协议发给对端的事件。

## 常见错误

下面这些**都实测踩过**，且都能被 `spec.plan` 拦下 —— 所以每次改完都要过一遍预检。

| 写法 | 预检报的码 | 后果 |
| --- | --- | --- |
| 通道名靠猜或照抄别的设备表 | `track.device_undeclared` / `track.oaction_undeclared` | 输出被**静默丢弃**，设备不动、无报错 |
| 把纯环境动作（`form=gate`）写成预设 | `preset.no_channels` | 它没有姿态通道，预设无法展开 |
| **同一轨道上两个节拍时间重叠** | `clip.overlap` | 后一个被拒；调整节拍时要重算整条轨的占用区间 |
| 最后一个节拍 + 预设时长超过片长 | `spec.content_beyond_duration` | 收尾动作被截断；末尾要留出**整个预设时长**再加余量 |
| 同一 role 重复添加同一门控 | — | 重复窗口，行为不可预期；加门控前先检查已有 |
| 环境特效写成曲线 | — | 开关量被当模拟量，行为不可预期 |
| 用 `step` 插值做大幅位移 | — | 阶跃触发速率上限，动作被削成斜坡或机构报错 |
| `cues` 引用了不存在的 `preset` id | — | 该节拍被忽略 |
| 改完 spec 不跑 `spec.plan` | — | 上面这些问题本来都能在这里拦下，却被带到现场 |

### 节拍占用区间怎么算

同一角色上，节拍的占用区间是 **[cue 时刻, cue 时刻 + 预设时长)**。
排节拍前先把每条 role 的占用区间列出来，按时间排序检查有没有交叠 —— 尤其是
「一个长预设（如 `vehicle_travel` 8000ms）后面紧跟一个短事件」这种情况最容易撞。
