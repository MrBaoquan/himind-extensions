# 素材套图生产

`com.himind.workflow.media-kit-production` 把「一句话 + 风格包」变成一批可交付的素材，
并把「生成前检查」「批量生成」「择优建议」拆成可单独重跑的三段。

依赖插件 `com.himind.media-studio`（媒体创作工具）提供的能力：

- `media.studio.runtime.status`
- `media.studio.batch.start`

## 三段式

```text
MK-CHECK    确认生图服务与风格包就绪（只读）
   ↓
MK-PRODUCE  按矩阵批量生成，产出 media-kit-batch
   ↓
MK-REVIEW   按风格一致性与可用性给出取舍建议，产出 media-kit-review
```

`execution_policy: segmented` 意味着三段可以各自作为入口单独跑：

| 入口 | 从哪一步开始 | 需要什么 |
| --- | --- | --- |
| `check` | MK-CHECK | 只要工作区 |
| `produce` | MK-PRODUCE | 工作区 + 生成需求 |
| `review` | MK-REVIEW | 已有 `media-kit-batch` 产物 |

## 输入

| 字段 | 说明 |
| --- | --- |
| `workspace_root` | 工作区，产物默认落在 `<工作区>/.himind/media` |
| `prompt` | 要生成什么（主体、场景、用途），风格交给风格包 |
| `styles` | 风格包 ID 列表，例如 `clean-product`、`night-city`；留空等于不加风格包 |
| `sizes` | 尺寸列表，例如 `1024x1024`；留空用风格包默认尺寸 |
| `variants` | 每格张数（1–4） |
| `filename_prefix` | 文件名前缀，留空按时间命名 |
| `output_dir` | 产物目录，留空用工作区默认目录 |

矩阵总量 = 风格数 × 尺寸数 × 每格张数，上限 36 张；超限时 MK-PRODUCE 会直接失败并
说明超了多少，不会悄悄裁掉一部分。

## 产物

- `media-kit-batch`：作业号、状态、逐项产物清单（含 media_id、路径、尺寸、摘要）。
- `media-kit-review`：择优建议。`picks` 里每条带用途（封面 / 正文配图 / 图标 / 背景板 /
  需要重做）与评分，`concerns` 记录批次共性问题。

择优建议只依据结果里的元数据（尺寸、重复度、命名、失败分布），**不看像素**：需要看图
才能下的结论它不会编。要真按画面挑，把产物交给能读图的模型或人工复核。

## 失败与降级

| 现象 | 含义 | 处理 |
| --- | --- | --- |
| MK-CHECK 通过但 MK-PRODUCE 立即失败 | 没有可用的生图服务 | 在 Agent 的 AI 服务里为某个服务绑定 `image.generate` |
| 单个单元格失败 | 该组合生图失败，其余继续 | 看 `media-kit-batch` 里该格的 error |
| 作业状态 `interrupted` | Agent 退出打断了生成 | 已完成的产物仍保留，重跑缺的部分 |
| 全批失败 | 服务不可用或提示词被平台拒绝 | 先跑 `check` 入口确认服务 |

MK-REVIEW 的 `on_failure: continue`：择优建议失败不影响已经生成的素材。

## 生成血缘

每一张产物都在媒体创作工具里留有生成血缘（提示词、风格包、能力、模型、服务、摘要）。
事后可以按 media_id 或路径反查，也可以从媒体资源库的素材详情一路回溯到当时的配方。

## 开发与验证

```text
himind-agent workflow validate workflows/media-kit-production
himind-agent workflow doctor workflows/media-kit-production
```

`doctor` 在未安装媒体创作工具时会报 `capability.unavailable` 与 `plugin.unavailable`，
这是预期行为：先装插件，再跑工作流。
