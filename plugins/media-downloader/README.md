# 媒体下载工具

把公开网页中的在线音视频、字幕和播放列表下载到系统下载文件夹或用户指定的本机目录，并记录来源、格式、摘要和 yt-dlp 版本。

## 桌面界面

默认客户区 1100×760，建议工作尺寸不低于 860×600。导航与页头固定，内容区独立滚动；更窄窗口仍可通过折叠菜单进入各页。原生最小尺寸与窗口位置记忆由 Agent 宿主提供，当前插件不承诺强制限制窗口大小。

Ctrl+N 新建下载，Ctrl+F / Ctrl+K 搜索任务，F5 / Ctrl+R 刷新数据，Ctrl+, 打开设置，设置页 Ctrl+S 保存，F1 打开运行时与快捷键说明。刷新只重取数据，不重载页面。

未保存设置与未提交的链接在本机暂存为草稿，重开视图恢复；恢复的媒体链接必须重新解析。保存设置或点击“丢弃草稿”会清除设置草稿，重置表单或创建任务成功会清除链接草稿。本机存储不可用时显示提示。草稿不包含账号或凭据，不创建下载任务。

## 能力

- `media.downloader.runtime.status`：检查 yt-dlp 与 ffmpeg 状态。
- `media.downloader.runtime.install`：安装固定版本的 yt-dlp 运行时。
- `media.downloader.info.get`：解析链接并返回格式、字幕和条目摘要。
- `media.downloader.site.status`、`media.downloader.site.connect`、`media.downloader.site.disconnect`：查看、连接和断开抖音官网会话，接口不接收或返回 Cookie。
- `media.downloader.job.start`：创建受控下载任务。
- `media.downloader.job.status`：查询任务进度和产物。
- `media.downloader.job.cancel`：取消任务。
- `media.downloader.job.list`：分页、筛选并汇总任务记录。
- `media.downloader.job.cleanup`：按状态和保留天数批量清理已结束记录，支持 dry-run。
- `media.downloader.job.retry`、`media.downloader.job.remove`、`media.downloader.job.log`：重试、删除记录与读取诊断日志。

## 运行时策略

插件不把 PyInstaller 版 `yt-dlp.exe` 直接打进默认 `.hmpkg`。`runtime.install` 优先从 yt-dlp 官方 GitHub Release 下载固定版本、校验 SHA-256，并保存到插件私有数据目录。

安装下载优先遵循 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` 环境设置；未配置这些变量时，在 Windows 读取已启用的用户系统静态代理及绕过列表（不修改代理设置，不执行 PAC）。连接中断、超时及临时 HTTP 错误最多尝试三次，摘要不匹配不会重试或覆盖原运行时。

当官方二进制网络不可达时，插件会尝试在私有目录创建 Python 虚拟环境，并安装固定版本 `yt-dlp==<version>` 作为备用运行时。备用路径只执行固定的 PyPI 安装命令，不接受任意包名、任意 URL 或任意命令参数。

备用安装前验证 Python 3.10+ 及 `venv`，跳过 Windows Store 占位命令。除 PATH 上的 Python/py 外，也可通过 uv 离线查找已有解释器；插件不会因此下载 Python，不依赖特定 AI 客户端的内置解释器。

当前固定版本记录在 `runtime-lock.json`。组合的 PyInstaller 发布二进制包含 GPLv3+ 代码；使用和再分发时请遵守 yt-dlp 的许可证说明。

## 安全边界

- 只接受 `http` / `https` URL，拒绝本机、私网、链路本地和云元数据地址。
- 不接受明文 Cookie、浏览器数据库、代理凭据、DRM 或登录绕过参数。抖音会话只能通过用户打开的官方站点窗口建立。
- 不执行任意 Shell、任意 yt-dlp 参数或任意外部下载器。
- 默认保存到系统下载文件夹；也可以选择其他本机目录。相对目录仍限制在工作区内。
- 当 Agent 沙箱不允许写入目标目录时，会自动回退到插件目录下的 `.himind-downloads`，任务记录会保留实际保存路径。
- 默认不下载播放列表；启用时限制条目数、文件大小和并发行为。
- 记录清理只删除任务元数据与日志，不删除已下载的媒体文件。

## 抖音站点连接（0.2.6）

解析抖音链接时若尚未连接，点击解析区“连接抖音”；也可从“设置 → 站点连接”进入。窗口仅允许抖音官网 HTTPS 导航，不提供任意网址、脚本桥接、下载或浏览器凭据提取。可正常浏览或在官网登录；会话就绪后自动返回，也可点击“完成连接”。保留当前链接和下载选项，只在输入仍匹配时继续解析。连接过期可重新连接，不自动循环重试。

会话保存在 Agent 提供的稳定插件私有数据目录，使用 Windows 当前用户 DPAPI 加密并限制目录 ACL；不进入能力参数、响应、localStorage、草稿、任务 JSON 或来源文件。辅助窗口使用独立的临时 WebView2 profile，关闭时清除 Cookie 与浏览数据并移除 profile，不打开系统浏览器的用户数据目录。

解析与脱离调用的后台 worker 各自读取有效会话，不依赖临时 Broker 生命周期。执行前仅将 `.douyin.com` 及其子域的有效 Cookie 写入私有临时 Jar；使用 Secure Cookie，不使用全局 Cookie 请求头，不向其他站点传递会话。成功、失败或取消均清理，崩溃遗留在后续状态读取/请求时按进程存活情况回收。带会话的下载关闭 yt-dlp 原始 info.json 导出，日志只记录固定说明与清洗后的数值进度。站点原始报错不落盘。插件自己的 `.source.json` 保留公开来源、文件摘要和下载参数。

“断开”立即撤销后续会话获取，并停止使用该会话的后台下载；清除加密会话，未关闭的连接窗口会随之退出并清理临时 profile。重新连接不会让旧窗口的晚返回恢复已撤销会话。连接最多复用七天；站点可能提前使其失效。连接状态代表本机存在可用会话，不保证某条视频可下载，私有、付费、DRM 内容和站点风控仍不支持。

支持官网 `/video/<id>`、移动分享视频页及搜索页/个人主页中带 `modal_id` 的单视频链接；`v.douyin.com` 分享链接只跟随官网 HTTPS 跳转并规范为公开视频地址。个人主页列表、图文和合集不因此自动获得支持。

0.2.7 起，yt-dlp 的 `Fresh cookies (not necessarily logged in) are needed` 不再被直接判为登录过期，此类失败保留连接。0.2.6 的旧模糊过期标记升级后会重新尝试一次；明确的登录失效错误仍要求重新连接。

0.2.8 在插件网络层适配抖音官网详情网关，仅对固定官方详情接口附加 `x-tt-argus` 及当前会话中的 `UIFID`（或 `UIFID_TEMP`）请求头，不跟随该接口重定向。兼容依据见 [F2 issue #443](https://github.com/Johnserf-Seed/f2/issues/443)，未引入 F2 包或第三方解析服务。详情只写入同一私有临时目录，再由随包适配器调用固定 yt-dlp 的内置格式解析器和下载器；官方独立版自带 Python，无需用户另装 Python。网关身份不写入媒体请求头、命令参数、返回元数据或日志；详情文件与 Cookie Jar 在结束时一起清理。解析与独立 worker 均重新取得当前详情，避免保存已过期的签名媒体地址。站点仍可能变更接口或限制特定内容，本适配仅覆盖公开单视频。

构建站点窗口需要 .NET SDK（目标 .NET Framework 4.8）和固定版本 Microsoft WebView2 SDK。在插件目录运行 `tools/session-browser/build.ps1`，再使用生产 Agent 的 `extension.plugin.build`、`package` 和候选测试；Gateway 当前只提供 Go 构建，因此辅助组件通过该脚本预构建。辅助源码与脚本位于插件目录，可独立构建。安装包自带辅助程序和 SDK 程序集，用户无需安装 .NET SDK；Windows 需要 .NET Framework 4.8 与 WebView2 Runtime（现有 Agent 已使用 WebView2）。SDK 的许可与 NOTICE 随包附带。
