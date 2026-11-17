# 视频封面交付

把选题或新闻稿先变成可确认的主副标题，再基于真实、纯净的视频画面生成统一封面。

## 固定顺序

1. `封面标题助手` 提取事实并生成 6 组候选；
2. 用户明确选择或修改候选；
3. 选定后才进入视频选帧与封面渲染；
4. 使用 `wanmei-d-v1` 品牌模板输出 1080×1440 与 1728×2304；
5. 用户确认封面效果；
6. 冻结不可变交付候选。

## 依赖

- Skill：`com.himind.cover-title-assistant`
- Plugin：`com.himind.video-cover`
- Plugin：`com.himind.media-toolkit`
- Optional：`com.himind.media-ai`（视频转写）

## 输出

- 标题候选 Artifact；
- 已确认文案 Artifact；
- 封面交付 Artifact，含视频路径、选帧时间、源帧、输出哈希和质检；
- 不可变候选。

## 边界

- 标题未确认不得渲染；
- 使用真实视频帧，不使用 AI 重绘图替代底图；
- 不在未确认时覆盖用户选择；
- 不提交、发布或外发。
