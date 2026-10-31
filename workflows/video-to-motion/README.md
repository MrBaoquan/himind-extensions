# 视频转动作

按具体 IOUI 动感平台能力，把视频证据转换为平台无关动作意图，再编译为 gentle / normal / intense 三档 IOStudio ShowSpec 和 `.motion`。

## 核心原则

- `device-profile` 只描述 IOStudio 通道映射。
- `platform-profile` 描述平台机构、有效 DOF、当前协议命令变体、量程、方向和环境特效。
- 平台缺失的轴不能静默换成别的轴；`approximate` 映射必须人工审批。
- `motion-composer` 是硬依赖；缺失时 preflight 失败。
- 自动化只做 dry-run 和静音预览，不驱动真实设备。

## 当前平台档案

| Profile | 有效能力 | 关键边界 |
|---|---|---|
| `mp-ydl-heave-v1` | Heave + 环境特效 | Heave-only；无 Pitch/Roll/Yaw；风/水/香合并下发 |
| `mp-njlj-a3t-v1` | Pitch + Roll + Z + Speed | 当前 `command=A3T`；不暴露理论 A6/A7 轴 |
| `mp-njqk-pitch-roll-v1` | Pitch + Roll + Speed | 无 Heave；失重只能近似 |
| `motion-plat-pitch-roll-v1` | Pitch + Roll | 旧平台；Yaw 未实现 |

## 阶段

```text
运行时预检 → 设备通道 → 平台档案 → 视频证据 → 行为时间线
→ 动作意图 → 平台映射 → 人工语义确认 → 三档 ShowSpec
→ motion-composer 质量循环（最多 3 次）→ IOStudio 构建/预检/轨道
→ 交付清单 → 现场人工验收
```

## 入口

- `analyze_video`：完整分析
- `from_intent`：已有行为时间线与动作意图
- `from_specs`：已有三档 ShowSpec
- `build_only`：已有已审计规格
- `review_delivery`：只做现场确认

## 依赖

- `com.himind.skill.video-motion-authoring >= 1.8.0`
- `com.himind.media-toolkit >= 1.4.0`
- `com.himind.iostudio >= 1.1.1`
- `com.himind.motion-composer >= 1.2.0`
- `com.himind.media-ai` 可选

本地开发仍应先构建并安装 Plugin，再启用 Skill/Workflow；`required` 不得改成可选绕过预检。

## 关键 Artifact

- `platform-profile`
- `device-profile`
- `media-evidence`
- `behavior-timeline`
- `motion-intent`
- `platform-binding`
- `motion-spec-set`
- `motion-audit-set`
- `motion-delivery`
- `field-feedback`

`platform-binding` 的 `exact / approximate / unsupported` 是进入 ShowSpec 生成的硬门禁。
