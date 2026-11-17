---
name: media-downloader-assistant
description: 使用媒体资源库搜索、收藏并采集在线作品，选择格式、跟踪后台任务与来源；用户要求搜索视频、下载音视频字幕或沉淀素材时使用。
---

# 媒体采集助手

## 入口与范围

普通视频、音频、字幕、搜索与入库均使用 `media-library.*`。稳定技能 ID 保持不变。只有用户明确要求播放列表，或跟踪旧工具创建的任务，才使用可选的 `media.downloader.*`。

用户已明确授权下载、目标与数量时直接执行，不重复询问。只有目标库、范围或格式缺失且无法合理推断时再澄清；搜索不自动触发批量下载。不得索取或传递 Cookie、Token、密码、签名媒体地址或任意命令，不绕过验证码、付费或 DRM。

## 检查运行条件

1. 调用 `media-library.download.status`。缺组件时沿用已有安装授权；无授权时说明用途并确认后调用可选的 `media-library.download.install`，然后重查状态。不要改用 Shell 或自行拼接安装命令。
2. 音频提取和独立音视频合并需要 ffmpeg；缺失时解释影响，选择含音轨的原始格式或等用户安装。
3. 抖音需要连接时调用可选的 `media-library.account.status`、`media-library.account.connect`，由用户在官网窗口操作。连接后复用会话，模型不读取秘密；失败时给出设置中的连接入口。

## 搜索与收藏

1. 调用 `media-library.discovery.providers` 读取各平台搜索、预览及采集支持；不能把搜索成功当成下载可用。
2. 用户指定平台与关键词后调用 `media-library.discovery.search`，传 `provider`（douyin、bilibili、youtube）、`query` 和新的 32 位十六进制 `request_id`。只使用返回的稳定作品地址。
3. 下一页沿用同一平台和关键词，并传原样 `next_cursor`；过期、换平台、改词或换账号时重新搜索。不要跨账号复用游标。
4. 用户取消或改词时调用 `media-library.discovery.cancel` 并传原请求编号；忽略迟到结果。不高频重试平台限流或验证失败。
5. 摘要显示作品标题、平台、作者及真实时长。搜索无预览时明确可打开作品页面，不伪造缩略图或成功状态。
6. 用户要求收藏时调用 `media-library.bookmark.save`，传 `source_url` 与作品稳定字段；备注和标签仅在用户要求修改时传入，避免覆盖已有整理。调用 `media-library.bookmark.list` 检索已有收藏。收藏只保存作品线索，不表示已下载或已获许可。

## 解析与采集

1. 调用 `media-library.remote.resolve`，传用户提供或搜索返回的 `url`、`playlist:false`。根据真实结果选择 `quality`（best、1080p、720p、480p）、单个 `format_id`、`audio_only` 或存在的字幕语言。原始格式与清晰度、仅音频互斥；未知尺寸格式不要承诺符合某个上限。
2. 入库前调用 `media-library.repository.list`，只选择在线、可写的本机库。用户要入库时传 `mode:import` 和明确的 `library_id`；只保存则传 `mode:save` 与可选 `dest_dir`。未指定保存目录时使用系统下载文件夹，不将插件私有目录当作用户输出。
3. 调用 `media-library.acquisition.start`，必传 `url` 和新的 `idempotency_key`，再传模式、目标及已选格式。相同意图的网络重试保留同一幂等键；用户改变格式或目标时生成新键。默认单作品、有限大小，不设覆盖。
4. 记录返回的 `job_id`，通过 `media-library.acquisition.status` 跟踪，或 `media-library.acquisition.list` 查已有任务。5秒或更长间隔轮询；关闭界面仍由后台任务执行，不创建第二套队列。
5. 用户取消时调用 `media-library.acquisition.cancel` 并核对终态。失败修复后调用 `media-library.acquisition.retry`；待入库任务只继续保存/入库，不重复下载。
6. `SESSION_CHANGED` 需要用户明确选择当前账号后，重试才传 `use_current_account:true`。不能自动把原账号任务绑定到新账号。
7. 完成后检查 `files`、SHA256 和 `library_item_id`；入库素材用 `media-library.holdings.provenance`（`item_id`）核对来源。不把任务排队或下载结束等同于入库完成。

## 错误恢复

- 需要网页登录：给出连接或作品官网入口，等待用户操作；不索取凭据。
- 平台验证、访问拒绝、限流：说明实际原因，允许稍后重试，不循环抓取或绕过。
- 格式不可用：重新解析后选择可用格式；文件超限则缩小格式或按用户明确授权调整上限。
- 目标不可用或待入库：恢复目标后重试同一任务，保留已下载文件。
- 能力不存在：报告缺失的真实插件版本和能力，不自动编造别名或改走旧工具写入口。

## 旧工具兼容路径

仅在用户明确要求播放列表时检查可选的 `media.downloader.runtime.status`，调用 `media.downloader.info.get`（`playlist:true`、明确 `max_items`），再按授权调用 `media.downloader.job.start`。传已确认的工作区、URL、保存目录与列表上限，不自动采集整张大型列表。缺少旧插件时说明播放列表暂不可用，普通单作品采集不受影响。

旧任务只通过 `media.downloader.job.list`、`media.downloader.job.status` 跟踪，用户要求取消时调用 `media.downloader.job.cancel`。保留原 owner、输出与来源记录；不要将旧 `job_id` 交给新采集能力。旧历史的只读导入在媒体资源库“采集任务→旧下载记录”中操作，不接管在途任务，不移动文件。

## 交付

简短报告标题与平台、最终状态、输出文件路径、入库条目及来源、选用格式和未完成原因。搜索/收藏/文件/正式入库分别按实际状态表达；平台失败与未测试项不写成支持承诺。
