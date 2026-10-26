# HiMind Extensions 图标概念集

本目录由 HiMind Agent 的 `media.image.generate` 能力生成，使用服务
`ark-agent-plan` 与模型 `doubao-seedream-5.0-pro`。

## 文件

- `himind-extension-icon-atlas-0-c6947eea.jpg`：2048×2048 原始 5×7 图标母版。
- `ICON_MAP.png`：带扩展名称与索引的审阅图。
- `ICON_MAP.md`：35 个扩展的图标映射和设计主题。
- `mapping.json`：供脚本消费的机器可读索引。
- `icons/`：从母版切出的 35 个 256×256 RGBA PNG，外角透明，均小于 256 KB。
- `prompt.txt`：本次生图提示词。
- `generation-result.json`：生成服务、模型、路径和 SHA-256 的交付元数据。

## 使用

将对应 PNG 复制到扩展项目声明的包内路径，例如：

```json
{
  "icon": "assets/icon.png"
}
```

当前 35 个图标已分别应用到对应扩展：

- manifest：顶层 `icon` 为 `assets/icon.png`
- 文件：扩展目录下 `assets/icon.png`
- 技能：`contents` 同步声明该图标

正式发布前仍建议逐个检查 28px/48px 下的识别度，并确认符号没有不必要地复刻第三方
商标；重新发布扩展版本后，远端市场才会获得新的作者图标 sidecar。
