# 媒体下载工具

把公开网页中的在线音视频、字幕和播放列表下载到系统下载文件夹或用户指定的本机目录，并记录来源、格式、摘要和 yt-dlp 版本。

## 能力

- `media.downloader.runtime.status`：检查 yt-dlp 与 ffmpeg 状态。
- `media.downloader.runtime.install`：安装固定版本的 yt-dlp 运行时。
- `media.downloader.info.get`：解析链接并返回格式、字幕和条目摘要。
- `media.downloader.job.start`：创建受控下载任务。
- `media.downloader.job.status`：查询任务进度和产物。
- `media.downloader.job.cancel`：取消任务。
- `media.downloader.job.list`：分页、筛选并汇总任务记录。
- `media.downloader.job.cleanup`：按状态和保留天数批量清理已结束记录，支持 dry-run。
- `media.downloader.job.retry`、`media.downloader.job.remove`、`media.downloader.job.log`：重试、删除记录与读取诊断日志。

## 运行时策略

插件不把 PyInstaller 版 `yt-dlp.exe` 直接打进默认 `.hmpkg`。`runtime.install` 优先从 yt-dlp 官方 GitHub Release 下载固定版本、校验 SHA-256，并保存到插件私有数据目录。

当官方二进制网络不可达时，插件会尝试在私有目录创建 Python 虚拟环境，并安装固定版本 `yt-dlp==<version>` 作为备用运行时。备用路径只执行固定的 PyPI 安装命令，不接受任意包名、任意 URL 或任意命令参数。

当前固定版本记录在 `runtime-lock.json`。组合的 PyInstaller 发布二进制包含 GPLv3+ 代码；使用和再分发时请遵守 yt-dlp 的许可证说明。

## 安全边界

- 只接受 `http` / `https` URL，拒绝本机、私网、链路本地和云元数据地址。
- 不接受 Cookie、浏览器凭据、代理凭据、DRM 或登录绕过参数。
- 不执行任意 Shell、任意 yt-dlp 参数或任意外部下载器。
- 默认保存到系统下载文件夹；也可以选择其他本机目录。相对目录仍限制在工作区内。
- 当 Agent 沙箱不允许写入目标目录时，会自动回退到插件目录下的 `.himind-downloads`，任务记录会保留实际保存路径。
- 默认不下载播放列表；启用时限制条目数、文件大小和并发行为。
- 记录清理只删除任务元数据与日志，不删除已下载的媒体文件。
