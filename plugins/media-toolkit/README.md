# 媒体处理工具（com.himind.media-toolkit）

HiMind Agent 的本地通用媒体工具。目标不是把 ffmpeg 参数摊给用户，而是把常见媒体任务收敛成四个清晰入口：

1. **图片转换**：JPEG / PNG / WebP / BMP / TIFF / GIF 批量转换；首屏只保留格式、尺寸和质量预设。
2. **视频转换**：MP4 / MOV / MKV / WebM；首屏只保留格式、尺寸和质量预设，支持提取音轨。
3. **图片转视频**：单张图片循环，或 `%04d` 编号图片序列合成视频；首屏只保留尺寸、时长和可选音轨。
4. **媒体分析**：返回归一化的容器、时长、体积、视频/音频参数和告警，不暴露原始 ffprobe JSON。

旧的专业能力仍保留给 AI 或高级调用：全景主前向投影、抽帧拼图、画面活动曲线、响度曲线、通用 recipe 作业等。

## 设计边界

- 模型推理属于 `media-ai`，本插件只做确定性媒体处理。
- 不提供任意 ffmpeg 参数透传，不通过 Shell 拼接命令。
- 所有输入、输出必须落在调用方提供的 `workspace_root`。
- 默认不覆盖输入文件；高价值产物先写同目录临时文件，成功后原子替换。
- 长任务有 `job_id`、进度、取消、持久化和进程重启后的 `interrupted` 状态。
- FFmpeg 构建若含 GPL/额外编解码器，市场发布或对外分发前必须完成许可证评审。

## 独立运行

插件进程通过 JSON-RPC/stdio 调用 ffmpeg / ffprobe。运行时按以下顺序查找：

```text
插件包内置 → 插件私有数据目录 → 系统 PATH
```

推荐分发结构：

```text
media-toolkit/
├─ bin/media-toolkit.exe
├─ ui/
└─ runtime/
   └─ windows-x64/
      ├─ ffmpeg.exe
      ├─ ffprobe.exe
      └─ manifest.json
```

`manifest.json` 可选。一旦存在，插件会校验平台、文件大小和 SHA-256；缺失 manifest 时仍可使用本地开发构建，但 `runtime.status` 会明确提示“无法证明来源与完整性”。

```json
{
  "version": "6.1-full",
  "platform": "windows-x64",
  "files": {
    "ffmpeg.exe": { "sha256": "<64位sha256>", "size": 123456 },
    "ffprobe.exe": { "sha256": "<64位sha256>", "size": 123456 }
  }
}
```

插件数据目录也可放同一结构，用于不便重新打包时的本机运行时托管。`media.toolkit.runtime.status` 会返回实际来源、根目录、版本、风险提示和解析顺序。

## 能力

| 能力 | 说明 |
| --- | --- |
| `media.toolkit.runtime.status` | 运行时来源、版本、风险与可用能力 |
| `media.toolkit.image.convert` | 图片格式批量转换 |
| `media.toolkit.video.convert` | 高层视频转换，返回可跟踪作业 |
| `media.toolkit.images.to-video` | 单图/序列转视频，返回可跟踪作业 |
| `media.toolkit.media.analyze` | 归一化媒体参数报告 |
| `media.toolkit.probe` | 原始探测结果，供 AI 或兼容调用 |
| `media.toolkit.files.pick` | 打开原生文件选择器，并把工作区外素材导入受控收件目录 |
| `media.toolkit.files.list` | 列出工作区媒体素材（供 AI/兼容调用，不在主界面展示） |
| `media.toolkit.recipe.plan` | 只编译 recipe，不执行 |
| `media.toolkit.recipe.run` | 执行通用 recipe 作业 |
| `media.toolkit.job.status / cancel / list` | 作业查询、取消和历史 |
| `media.toolkit.video.project` | 全景等距柱状视频主前向投影 |
| `media.toolkit.scene.score` | 画面活动强度曲线 |
| `media.toolkit.audio.loudness` | 响度曲线与整体响度 |
| `media.toolkit.frames.contact-sheet` | 缩略图网格 |
| `media.toolkit.frames.extract` | 指定时刻抽帧 |
| `media.toolkit.preview.image` | 受约束的图片预览 |

## 工作区是什么

`workspace_root` 是 HiMind Agent 注入给插件的内部安全边界，不是要求用户选择或管理的文件夹。插件只允许在这个根目录内读写，避免越界访问；主界面不展示该路径。工作区外的素材由原生文件选择器导入受控 inbox 后再处理。

## 素材选择

- 主界面不再把整个工作区塞进下拉框。
- 素材区本身是唯一的拖拽与点击入口：点击可打开 Windows 原生文件对话框，也可直接把文件拖入。
- 图片转换支持一次选择多张；其他任务一次选择一个素材。
- 已选素材单独展示，可逐项移除；切换任务时保留各任务自己的选择。
- 选择素材后不自动展开参数表，避免主任务被挤出视野。
- 工作区内的文件直接引用；工作区外的文件复制到 `.himind/media-toolkit/inbox/` 后再处理。
- 图片转视频的可选音轨也只通过“添加音轨”按钮选择，不再提供手填路径。

## 少参数原则

- 默认保持原尺寸，不自动放大素材。
- 质量使用“高 / 标准 / 更小体积”三档意图，不要求普通用户理解 CRF 或编码器质量参数。
- 编码器、码率、帧率、命名模板、输出路径和覆盖策略均位于“更多设置”。
- 手工修改专业字段时会自动切换为“自定义”并展开高级设置。

## 图片转换约定

- 输入扩展名：`.jpg`、`.jpeg`、`.png`、`.webp`、`.bmp`、`.tif`、`.tiff`、`.gif`。
- 输出扩展名由目标格式决定，例如 JPEG → `.jpg`、TIFF → `.tiff`。
- 默认后缀 `-converted`，可用 `name_template` 覆盖，占位符为 `{name}`、`{index}`、`{ext}`。
- 同时设置宽高时按比例缩放并用背景色留白；只设置一边时保持另一边比例。
- 宽高必须是偶数，避免部分编码器静默取整。
- 批量任务先预检全部输出名；命名碰撞、路径穿越、覆盖输入或同名产物都会在开始前拒绝。

## 视频转换约定

- MP4 / MOV：H.264、H.265 或流复制；音频 AAC、复制或移除。
- MKV：H.264、H.265、VP9 或流复制；音频 AAC、Opus、MP3、复制或移除。
- WebM：VP9 或流复制；音频 Opus、复制或移除。
- CRF 与目标码率互斥；流复制时不能同时缩放、改帧率或重设质量。
- 输出扩展名必须与声明容器一致。
- “只提取音轨”走受控 recipe，输出 MP3 / M4A / WAV。

## 图片转视频约定

- 单张图片默认循环 5 秒，可指定毫秒时长。
- 序列使用 ffmpeg 占位符，例如 `frames/shot_%04d.png`。
- 输出 `.mp4` / `.mov` / `.mkv`。
- 可选音轨；加入音轨后，输出以画面和音频中较短的一条结束，并会返回提示。

## 开发与验证

```powershell
cd plugins/media-toolkit
go test ./...

go build -o bin/media-toolkit.exe .

cd ../..
go run ./tools/cmd/himind-plugin-validate-cli -path ./plugins/media-toolkit
go run ./tools/cmd/himind-plugin-package -path ./plugins/media-toolkit -output ./dist/media-toolkit-2.3.2.hmpkg
```

测试包含真实 ffmpeg 合成素材的图片转换、视频转换、图片转视频、参数分析和作业原子写入；没有成对 ffmpeg/ffprobe 时相关集成测试自动跳过。

独立的运行时测试会把测试进程复制成假的 `ffmpeg.exe` / `ffprobe.exe`，清空 PATH 后验证“插件内置运行时”仍能定位并执行，避免把 PATH 回退误当成独立运行能力。
