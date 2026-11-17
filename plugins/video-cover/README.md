# 视频封面工具

用真实、纯净的视频画面生成统一的 3:4 封面。插件只负责确定性渲染，标题编创由「封面标题助手」完成。

## 工作流

1. 选择视频；
2. 通过 `media-toolkit` 抽取 9 张候选画面；
3. 用户点选一帧；
4. 输入已确认的主标题与补充信息；
5. 调整 3:4 裁剪焦点；
6. 渲染并预览；
7. 导出 1080×1440 与 1728×2304。

## 默认模板

`wanmei-d-v1`：

- 中文署名：皖美拾光
- 英文署名：LIGHT, GATHERED IN ANHUI
- 左上标题锚点
- 右下四字仿古印
- 暖纸白边框、局部压暗、胶片颗粒

## 能力

- `media.cover.template.list`
- `media.cover.layout.render`
- `media.cover.layout.batch`

视频探测、抽帧与图片预览复用 `com.himind.media-toolkit`。
