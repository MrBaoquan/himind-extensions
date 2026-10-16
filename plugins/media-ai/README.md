# 媒体理解工具

用**按需下载的本机模型**做媒体内容推理。当前提供：把视频里的语音与歌词转成带时间戳的文本。

## 这个插件管什么、不管什么

与 `media-toolkit` 的分界是**要不要模型**（这条线同时决定了安装体积与离线可用性）：

| | media-toolkit | **media-ai** |
| --- | --- | --- |
| 做什么 | 确定性处理与测量 | **模型推理** |
| 依赖 | 只要 ffmpeg | 模型运行时 + 权重（按需下载） |
| 安装体积 | 几 MB | 75MB–1.5GB（按所选模型） |
| 离线 | 装好 ffmpeg 即可 | 需先获取模型 |

**一条规则**：加功能加能力，**加运行时才加插件**。所以后续的模型能力都归这里，不必新建插件：

| 规划中的能力 | 能力 ID |
| --- | --- |
| 音频事件分类（枪声/尖叫/爆炸） | `media.ai.audio.classify` |
| 节拍与 BPM 检测 | `media.ai.audio.beats` |
| 画面语义描述 | `media.ai.scene.describe` |
| 目标检测 | `media.ai.detect` |
| 语音翻译 / 字幕 / 说话人分离 | `media.ai.translate` / `.subtitle` / `.diarize` |

它们与转写共用同一套运行时供给（`runtime.status` / `runtime.install` / `models`）与作业模型
（`job.*`），所以新增一个只是加一个能力，不是加一个插件。

> **调用平台已配置的 AI 服务**（生图/生视频）走的是内核内置的 `media.image.*` / `media.video.generate`，
> 不归本插件 —— 那些是"用已配置的服务"，这里是"在本机跑模型"。

## 它解决什么（以当前能力为例）

前面几个媒体工具能"看"（抽帧拼图）与"量"（画面活动、响度），但**听不见**。
转写补上这个模态：它把"这段在唱什么、在说什么"从猜测变成可读的文本。

这不只是锦上添花。实测一段音乐片用画面活动与响度都找不到段落结构
（曲线平在中位附近、全片最强画面变化只有 9.0，按常规阈值得到 0 个切点），
而**歌词给出了稳定的 4-5 秒一句的节律** —— 那正是编排动作时最需要的时点依据。

## 依赖（都不随包分发）

| 依赖 | 用途 | 获取方式 |
| --- | --- | --- |
| whisper.cpp | 语音识别 | `media.ai.runtime.install` 按锁定版本下载 |
| ggml 模型 | 识别权重 | 同上，按 `models` 参数选择 |
| **ffmpeg** | 抽音频 | **需自行安装**（见下） |

三者都是独立许可的第三方产物。二进制与模型**不打进插件包**（体积 20MB–1.5GB），
而是按 `runtime-lock.json` 锁定的版本与 SHA-256 在首次使用时下载并校验。

> **ffmpeg 不在自动获取范围内** —— 它与 media-toolkit 共用，且多数现场已有。
> 没有时 `runtime.status` 会明确指出。

### 为什么必须过 ffmpeg

实测 whisper-cli 自带解码器**读不了 mp4**（内含 AAC 时报 `1 input file(s) failed`），
所以不能把视频直接丢给它。统一先转 16kHz 单声道 PCM wav —— 这也是 whisper 的原生输入格式。

## 能力

| 能力 | 说明 |
| --- | --- |
| `media.ai.runtime.status` | 运行时是否就绪、缺什么、许可信息 |
| `media.ai.runtime.install` | 按锁定版本获取二进制与模型（校验 SHA-256） |
| `media.ai.models` | 可选模型及本机安装状态 |
| `media.ai.transcribe` | 转写，返回可跟踪的作业 |
| `media.ai.job.status` / `cancel` / `list` | 作业查询、取消、列举 |

## 用法

```
runtime.status                                  # 先看缺什么
runtime.install { models: ["small"] }           # 首次获取（小模型 75MB / small 466MB）
transcribe { workspace_root, path: "show.mp4", language: "zh" }
job.status { job_id }                           # 长任务，轮询到 completed
```

### 显式指定 language，别依赖自动检测

**这是最容易踩的一条。** 实测把一首中文歌交给自动检测，它判成 `en`，
结果整片只得到 `(singing in foreign language)` 这类占位标注、拿不到任何词。
指定 `language: "zh"` 后同一段音频稳定转出 39 段带时间戳的中文歌词。

对**歌曲与带背景音乐的内容**，自动检测基本不可用。

### 结果里的三段东西

`job.status` 完成后 `result` 含：

| 字段 | 含义 |
| --- | --- |
| `segments` | 全部段落（含 `(upbeat music)` 这类标注） |
| `content_segments` | **只保留有实际内容的段落**（过滤掉标注） |
| `placeholder_only` | 只有标注、没有词 —— 多半是语言没指定对 |
| `beats_ms` | 各内容段的起点序列（音乐/语义分句节律） |
| `full_text` | 全部内容拼成的文本 |
| `vocal_ratio` | 有人声的时间占比 |

`segments` 里的 `(music)` / `(inaudible)` 之类**不是歌词**，但"哪一段有人声"本身有用。
`content_segments` 与 `placeholder_only` 就是为区分这两者准备的 ——
否则很容易把占位文本当成歌词依据写进编排。

## 已知限制与取舍

**单线程执行。** 实测多线程在该环境会**段错误崩溃**（exit 139），单线程稳定。
代价是慢：226 秒音频约 95 秒（small 模型）。这是**环境兼容性**问题，
不是 whisper 本身的限制；`Options.Threads` 已留出参数，换环境可调。

**模型按 id 下载，不认任意路径。** 锁定文件里的模型才有校验值；
用户自带模型会绕过完整性校验，所以本版不做。

**不做说话人分离、不做翻译。** 前者需要额外模型与对齐；后者对动作编排没有价值。
需要译文时用 `-tr` 语义的能力可以后续加，但那是另一个判断。

## 开发

```bash
go test ./...
```

集成测试会真的下载运行时（约 500MB）并转写一段自合成音频，耗时较长；
在 `go test` 里默认跳过，需要时用 `MEDIA_AI_INTEGRATION=1` 打开。
