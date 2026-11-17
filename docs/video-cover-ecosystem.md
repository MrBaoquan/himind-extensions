# 视频封面扩展生态

## 组成

### 视频封面工具

- ID：`com.himind.video-cover`
- 版本：`0.1.6`
- 类型：ui-tool 插件
- 负责：九宫格选帧、3:4 裁剪、固定模板、标题渲染、四字印章、批量导出、标题安全评分。
- 视频探测、抽帧和图片预览复用 `media-toolkit`。
- `0.1.5` 修复取消选视频后的忙碌态；`0.1.6` 修复生成后隐藏占位层被 CSS 覆盖的问题。

### 封面标题助手

- ID：`com.himind.cover-title-assistant`
- 版本：`0.1.0`
- 负责：从思路/新闻稿/转写提炼事实，生成 6 组主副标题候选；等待用户明确确认后输出结构化封面文案。
- 不负责：选帧、制图、导出。

### 视频封面交付

- ID：`com.himind.video-cover-delivery`
- 版本：`0.1.4`
- 类型：segmented 工作流
- 顺序：标题候选 → 用户确认 → 选帧渲染 → 用户确认 → 冻结候选。
- 使用技能 `cover-title-assistant`、插件 `video-cover 0.1.6`、`media-toolkit`，可选 `media-ai` 转写。
- 按实际运行方式允许网络；生产预检已无 warnings / blockers。

## 默认品牌模板

`wanmei-d-v1`

- 左上：`皖美拾光 · LIGHT, GATHERED IN ANHUI`
- 标题：Noto Serif SC，108/100/92 三档字号逻辑
- 补充信息：68px，暖金色
- 右下：四字仿古印「皖美拾光」
- 3:4、1080×1440 与 1728×2304
- 固定安全区与抖音播放量避让

## 使用方式

1. 在 HiMind Agent「全部工具 → 视频封面」打开插件；
2. 选择纯净、无字幕视频；
3. 生成九宫格联系表，手动点击一帧；
4. 输入已经由「封面标题助手」确认的主副标题；
5. 调整 3:4 画面焦点；
6. 生成并检查 1080×1440 / 1728×2304 输出。

## 生产验证

使用 HiMind Agent `production` profile，端口 `18181`：

- 插件 `0.1.6`：构建、试运行、确认、激活通过。
- 真实纯净视频：`D:\视频输出\达芬奇\皖北-麦田.mov`。
- 九宫格候选：9 格；手动选择第 2 格，约 1.564 秒。
- 标题：`皖北的春天，藏在麦田里`；补充：`风吹麦浪，也吹醒了故乡`。
- 输出：1080×1440 与 1728×2304 PNG，标题 1 行、补充 1 行，标题安全分 68。
- 工作流 `0.1.4`：测试 `passed`，`release_blocked=false`，5 步界面正常。
- 技能 `0.1.0`：安装、注册、MCP 契约、客户端渲染测试通过。

## 已知限制

- 生产 Agent 的 HiMind AI 会话当前受内置 Runtime 清单不一致影响：期望 `0.2.2-rc.1`，实际 `0.2.1-rc.2`。标题技能的真实对话候选确认仍需在修复 Runtime 后人工验收。
- 历史成片中若已烧录标题，不应直接作为最终封面底图；实际封面仍应使用用户确认的纯净画面。

## 产物

- `dist/video-cover-0.1.6.hmpkg`
- `dist/cover-title-assistant-0.1.0.hmskill`
- `dist/video-cover-delivery-0.1.4.hmwf`
- `dist/video-cover-delivery-0.1.4.extension-lock.json`
