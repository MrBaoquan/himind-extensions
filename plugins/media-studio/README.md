# 媒体创作工具

`com.himind.media-studio` 把生图组织成**可复现的交付**：固化风格包、按矩阵批量出图、
为每个产物留下生成血缘，并对已有素材（包括媒体资源库里的素材）按描述改图。

它自己**不保管任何 Provider 密钥、也不直连模型服务**：所有生成都通过 Agent 注入的
能力代理调用内核的 `media.image.generate` / `media.image.edit`，由内核按用户配置的
服务、适配器与路由执行，插件只在清单的 `capability_dependencies` 里声明依赖。

## 能力

| 能力 | 做什么 |
| --- | --- |
| `media.studio.runtime.status` | 报告风格包、作业与生图服务的就绪状态 |
| `media.studio.style.list` | 列出内置与自定义风格包 |
| `media.studio.style.save` | 新增或覆盖一个风格包 |
| `media.studio.style.remove` | 删除自定义风格包（内置包不可删除） |
| `media.studio.batch.start` | 按「描述 × 风格包 × 尺寸」启动矩阵，返回作业号 |
| `media.studio.batch.status` | 查询作业进度与逐项结果 |
| `media.studio.batch.cancel` | 取消尚未完成的作业 |
| `media.studio.batch.list` | 列出最近的作业 |
| `media.studio.asset.lineage` | 按媒体 ID、路径或摘要反查生成记录 |
| `media.studio.image.rework` | 对已有图片按描述与风格包改图 |

## 风格包

风格包固定三件会漂移的东西：提示词骨架、负向描述、默认尺寸与 Provider 参数。

内置 6 个起步配方：`clean-product`、`exhibit-keyvisual`、`icon-tile`、`night-city`、
`ink-poster`、`brand-minimal`。用户可以保存同 ID 的自定义包覆盖内置包的效果，
内置包本身不可删除。

最终提示词按固定顺序拼装：

```text
<主体描述>。风格要求：<prompt_suffix>。不要出现：<avoid>
```

顺序固定是有意的：模型对靠后的约束更敏感，避免项放最后能显著减少跑偏。

风格包保存在 Agent 注入的插件私有目录 `styles.json`，跨版本升级与回滚保持不变。

## 矩阵批量

一次矩阵 = 描述 × 风格包 × 尺寸，每格再乘变体数（1–4 张/格）。

- 单次上限 36 张：超过会被拒绝，并提示减少风格包、尺寸或变体数；
- 每个单元格只调用一次生图能力（用 `n` 传变体数），减少往返；
- 作业异步执行：关掉视图、`start` 返回后进程继续跑，用 `batch.status` 查进度；
- 每个单元格独立记录成功或失败原因，一项失败不会中断整批；
- Agent 退出时未完成的作业会被改判为 `interrupted`，不会永远停在某个百分比。

产物默认落在 `<workspace_root>/.himind/media`，文件名带上风格包与尺寸，脱离插件也能辨认。

## 生成血缘

血缘是追加式 JSONL（`lineage.jsonl`），记录每个产物的提示词、风格包、能力、模型、
服务、摘要、尺寸与父素材。用途有三条：

1. 复现：同一批素材换尺寸时知道当时用的配方；
2. 溯源：素材被采纳进资源库后仍能回答「它怎么来的」；
3. 接续：改图产生的产物保留 `parent_media_id`，多轮修改可以一路回溯。

单行损坏会被跳过而不是让整次查询失败——血缘是旁证，不该变成故障点。

## 与媒体资源库的联动

媒体资源库（`com.himind.media-library` 2.1.0 起）的素材详情里有「AI 处理」入口，
条件是本插件已安装：

- 媒体库视图在 `contributes.views[].external_capabilities` 里声明 `media.studio.*`，
  宿主据此允许它调用本插件的能力；
- 入口通过一次 `style.list` 探测本插件是否存在，不存在就整块隐藏，不报错、不占位；
- 处理时把 `workspace_root` 一起传过来，工作区外的资源库素材会先复制到
  `.himind/media/studio-inputs/` 再改图——内核要求参考图位于会话工作区内。

## 界面

插件自带视图（`ui/index.html`），包含生成设置、矩阵预估、进度、结果网格与血缘面板。
四种降级状态都显式处理：

1. 桥接不可用：提示从 Agent 打开，而不是抛脚本异常；
2. 缺生图服务：说明去 AI 服务里绑定 `image.generate`；
3. 没绑定工作区：说明产物需要工作区，并禁用开始按钮；
4. 单元格失败：把失败原因写在卡片上，不伪造结果。

## 安全与限制

- 只读写 `workspace_root` 内与本插件私有数据目录中的文件；
- 不接收任意 URL、不执行 Shell、不读取凭据；
- 生成能力按声明校验：代理只放行 `capability_dependencies` 中列出的能力；
- 产物目录与应用输入目录都做越界校验；
- 当前目标平台为 Windows x64；
- 不含视频与语音编排：那部分由内核 `media.video.generate`、`media.audio.speech`
  与媒体处理工具承担。

## 开发与验证

```text
go test -count=1 ./plugins/media-studio
go build ./plugins/media-studio
```

单测覆盖风格包合并与覆盖、提示词拼装、矩阵展开与上限、逐项失败、取消、重启后中断、
血缘写入与反查、改图的工作区内外素材处理，以及视图契约（入口、桥接调用、降级状态、
可访问性与视觉令牌）。
