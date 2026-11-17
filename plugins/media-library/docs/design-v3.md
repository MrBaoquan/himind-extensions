# 媒体资源库 v3 设计稿（M0）

- 日期：2026-10-31
- 状态：待评审
- 插件：`com.himind.media-library` 2.0.0 → 3.0.0
- 依赖：HiMind Agent ≥ 0.3.92（新增本机媒体端点）；`ffmpeg`/`ffprobe` 必需；`exiftool` 可选
- 目标平台：Windows x64

## 1. 目标与非目标

### 目标

1. 图片、视频、音频共用一套资产模型、一套索引、一套检索和一套界面。
2. 把 `E:\航拍素材` 这类大文件素材接进来：只读索引、原地不动、可引用入库。
3. 10 万条可验收、50 万条可演进的检索与容量设计。
4. 用户数据（标签/评分/集合/采纳）支持本机 DB 与共享库 JSONL 两种落点。
5. 智能能力进入正式范围：转写、AI 打标、语义检索、相似检索、OCR；按预算闸门执行。
6. 所有长任务可进度、可取消、可恢复；索引可重建，用户数据可导出。

### 非目标

- 不做视频剪辑、转码、合成（交给 `media-toolkit` / `motion-composer`）。
- 不做云端多端同步、账号体系、公网访问。
- 不做 SMB 之外的多人权限模型。
- 不把 AI 结果写回源文件；不修改用户素材。

## 2. 已决事项

| 编号 | 决策 | 理由 |
| --- | --- | --- |
| D1 | 媒体预览走 Agent 统一端点（先做 spike） | 单一监听面、集中鉴权、可复用；避免每个插件各开端口 |
| D2 | `ffmpeg`/`ffprobe` 必需，`exiftool` 可选，WIC 兜底 | 本机已有 ffmpeg；HEIC/RAW 依赖系统扩展；exiftool 只补深度元数据 |
| D3 | 用户数据支持 `db` / `jsonl` / `both` 三种模式 | 本机要快，共享库要可搬走、可合并 |
| D4 | 按 50 万容量设计，按 10 万验收 | 结构一次做对，避免 50 万时返工 |
| D5 | AI 能力正式纳入；默认本机 ASR、本机向量；视觉走已接入模型；全部成本闸门 | 用户确认 |
| D6 | E 盘用「本机来源 + 引用型资源库」 | 86GB 不搬家，E 盘离线时只标不可用 |
| D7 | 精选采用「精选集 + 精选库」两层；默认同卷 hardlink、跨卷 copy、reference 可选 | 既保留决策与血缘，又能逐步沉淀成真实文件夹 |
| D8 | 自动预选优先本机（pHash/质量/多样性），视觉评分可选且受预算闸门 | 50 选 10 零成本可用，大规模不被 API 费用绑架 |

## 3. 架构

```text
来源层  volumes / source_roots / libraries(local|smb)
  │      卷序列号、文件 ID、排除规则、增量
  ▼
作业层  jobs(scan|probe|thumb|waveform|transcribe|tag|embed)
  │      优先级、限流、断点、取消、错误分类
  ▼
资产层  assets + asset_meta + 缓存（thumb/wave）+ AI 结果
  │      派生数据，可重建
  ▼
检索层  assets_fts_v3(CJK) + facets + 查询编译 + 排序
  │
  ▼
视图层  网格 / 列表 / 文件夹 / 检查器 / 批量 / 任务中心
```

关键原则：

- **派生数据可丢弃**：`assets`、FTS、缩略图/波形、AI 向量都可重建。
- **用户数据不可丢**：标签、评分、集合、保存搜索、资源库采纳；必须可导出。
- **身份优先于路径**：`(volume_serial, file_id)` 为主，路径只用于展示和兜底。

## 4. 数据模型 v3

### 4.1 资产身份

```text
asset_key = "vol:<volume_serial>/fid:<128-bit file id>"
fallback  = "path:<normalized-path>|size:<bytes>|mtime:<unix>"
```

- Windows：`GetFileInformationByHandleEx(FILE_ID_INFO)` 取 128 位文件 ID；`GetVolumeInformationByHandleW` 取卷序列号。
- 文件 ID 在重命名、移动、盘符变化后仍稳定；跨卷复制会变化，属预期。
- 取不到（网络盘、权限、非 NTFS）时回退路径指纹，并在资产上标 `identity=fallback`。

### 4.2 表结构（草案）

> 实现说明：v3 第一期沿用 2.x 的 `candidates` 表作为逻辑资产表，通过新增列承载元数据；
> 这样现有扫描/检索/采纳 SQL 不被一次性重写。等检索层迁移完成后再做物理重命名或建
> `assets` 视图，避免大爆炸式改造。

```sql
CREATE TABLE volumes (
  id TEXT PRIMARY KEY,              -- vol:<serial>
  serial TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  filesystem TEXT NOT NULL DEFAULT '',
  last_seen_at TEXT NOT NULL,
  online INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE source_roots (
  id TEXT PRIMARY KEY,
  volume_id TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL,
  scan_generation TEXT NOT NULL DEFAULT '',
  scan_status TEXT NOT NULL DEFAULT 'pending',
  last_scan_at TEXT NOT NULL DEFAULT '',
  metadata_mode TEXT NOT NULL DEFAULT 'db',   -- db|jsonl|both
  created_at TEXT NOT NULL
);

CREATE TABLE assets (
  id TEXT PRIMARY KEY,              -- asset:<vol>/<fid>
  asset_key TEXT NOT NULL UNIQUE,
  source_id TEXT NOT NULL,
  volume_id TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL,
  relative_path TEXT NOT NULL,
  folder TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  kind TEXT NOT NULL,               -- image|video|audio|other
  extension TEXT NOT NULL,
  mime TEXT NOT NULL DEFAULT '',
  bytes INTEGER NOT NULL,
  modified_at TEXT NOT NULL,
  captured_at TEXT NOT NULL DEFAULT '',
  identity TEXT NOT NULL DEFAULT 'file_id',
  probe_state TEXT NOT NULL DEFAULT 'pending',   -- pending|ok|unsupported|failed
  thumb_state TEXT NOT NULL DEFAULT 'pending',
  missing INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE asset_meta (
  asset_id TEXT PRIMARY KEY,
  width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  fps REAL NOT NULL DEFAULT 0,
  codec TEXT NOT NULL DEFAULT '',
  bit_depth INTEGER NOT NULL DEFAULT 0,
  hdr TEXT NOT NULL DEFAULT '',
  rotation INTEGER NOT NULL DEFAULT 0,
  camera TEXT NOT NULL DEFAULT '',
  lens TEXT NOT NULL DEFAULT '',
  iso INTEGER NOT NULL DEFAULT 0,
  aperture TEXT NOT NULL DEFAULT '',
  shutter TEXT NOT NULL DEFAULT '',
  gps_lat REAL, gps_lng REAL,
  sample_rate INTEGER NOT NULL DEFAULT 0,
  channels INTEGER NOT NULL DEFAULT 0,
  bitrate INTEGER NOT NULL DEFAULT 0,
  tags_json TEXT NOT NULL DEFAULT '{}',   -- ID3/Vorbis/XMP 原始标签
  extra_json TEXT NOT NULL DEFAULT '{}',
  model TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);

CREATE TABLE jobs (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL,               -- scan|probe|thumb|waveform|transcribe|tag|embed
  scope TEXT NOT NULL,              -- source:<id>|asset:<id>|library:<id>
  priority INTEGER NOT NULL DEFAULT 100,
  status TEXT NOT NULL DEFAULT 'queued',
  total INTEGER NOT NULL DEFAULT 0,
  done INTEGER NOT NULL DEFAULT 0,
  failed INTEGER NOT NULL DEFAULT 0,
  checkpoint TEXT NOT NULL DEFAULT '',
  error_class TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE thumb_cache (
  asset_id TEXT NOT NULL,
  size TEXT NOT NULL,               -- grid|preview
  path TEXT NOT NULL,
  bytes INTEGER NOT NULL,
  last_used_at TEXT NOT NULL,
  PRIMARY KEY(asset_id, size)
);

CREATE TABLE asset_ai (
  asset_id TEXT NOT NULL,
  kind TEXT NOT NULL,               -- tag|description|ocr|similar
  payload_json TEXT NOT NULL,
  confidence REAL NOT NULL DEFAULT 0,
  model TEXT NOT NULL DEFAULT '',
  model_version TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'ok',
  created_at TEXT NOT NULL,
  PRIMARY KEY(asset_id, kind, model)
);

CREATE TABLE asset_transcript (
  asset_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  start_ms INTEGER NOT NULL,
  end_ms INTEGER NOT NULL,
  text TEXT NOT NULL,
  lang TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(asset_id, seq)
);

CREATE TABLE asset_embedding (
  asset_id TEXT NOT NULL,
  modality TEXT NOT NULL,           -- image|video_keyframe|audio|text
  vector BLOB NOT NULL,
  dim INTEGER NOT NULL,
  model TEXT NOT NULL,
  model_version TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY(asset_id, modality, model)
);
```

精选与沉淀：

```sql
CREATE TABLE selections (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  scope_json TEXT NOT NULL,          -- source_ids/folder/query/asset_ids
  target_kind TEXT NOT NULL,         -- folder|library
  target_path TEXT NOT NULL,
  promote_mode TEXT NOT NULL,        -- reference|hardlink|copy
  target_count INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'draft',  -- draft|reviewing|done|archived
  criteria_json TEXT NOT NULL DEFAULT '{}',
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE selection_items (
  selection_id TEXT NOT NULL,
  asset_id TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'candidate',  -- candidate|included|reserve|excluded
  rank INTEGER,
  score REAL,
  score_json TEXT NOT NULL DEFAULT '{}',   -- sharpness/exposure/stability/aesthetic/similarity
  reason TEXT NOT NULL DEFAULT '',
  decided_by TEXT NOT NULL DEFAULT '',
  decided_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(selection_id, asset_id)
);

CREATE TABLE selection_runs (
  id TEXT PRIMARY KEY,
  selection_id TEXT NOT NULL,
  algorithm TEXT NOT NULL,
  version TEXT NOT NULL,
  params_json TEXT NOT NULL DEFAULT '{}',
  result_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

-- 同一资产可以有多个物理位置：原始路径、精选镜像（硬链接）、复制副本
CREATE TABLE asset_locations (
  asset_id TEXT NOT NULL,
  path TEXT NOT NULL,
  source_id TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'original',  -- original|curated|mirror
  identity TEXT NOT NULL DEFAULT 'file_id',
  PRIMARY KEY(asset_id, path)
);

CREATE TABLE curated_entries (
  selection_id TEXT NOT NULL,
  asset_id TEXT NOT NULL,
  location_path TEXT NOT NULL,
  mode TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY(selection_id, asset_id)
);
```

用户数据（本机 DB 模式）：

```sql
CREATE TABLE user_asset_state (
  asset_key TEXT PRIMARY KEY,
  tags_json TEXT NOT NULL DEFAULT '[]',
  rating INTEGER NOT NULL DEFAULT 0,
  collections_json TEXT NOT NULL DEFAULT '[]',
  note TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
CREATE TABLE saved_searches (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, query_json TEXT NOT NULL, created_at TEXT NOT NULL
);
```

共享库模式以 JSONL 事件为权威，DB 只是物化视图；表结构与上面一致，但写入先落事件、再更新物化表。

### 4.3 索引

```sql
CREATE INDEX idx_assets_source_recent ON assets(source_id, missing, modified_at DESC);
CREATE INDEX idx_assets_kind_recent   ON assets(kind, missing, modified_at DESC);
CREATE INDEX idx_assets_folder        ON assets(source_id, folder);
CREATE INDEX idx_assets_captured      ON assets(captured_at DESC);
CREATE INDEX idx_assets_resolution    ON assets(kind, width, height);
CREATE INDEX idx_meta_duration        ON asset_meta(duration_ms);
```

FTS v3：

```sql
CREATE VIRTUAL TABLE assets_fts_v3 USING fts5(
  entry_type UNINDEXED, entry_id UNINDEXED, source_id UNINDEXED,
  kind UNINDEXED, updated_at UNINDEXED,
  title, filename, folder, path, tags, collection, cjk,
  tokenize='unicode61 remove_diacritics 2'
);
```

- `cjk` 存标题/文件名/目录链/标签的“逐字展开”文本；
- 中文查询编译为短语（如 `"龙 舟"`），英文/数字用前缀；
- 已验证 `modernc.org/sqlite v1.54` 下 1 字、2 字、跨字短语均可命中。

### 4.4 迁移

- 2.0.0 的 `candidates`/`search_fts*` 属于派生数据：升级时**重建**，不清空 `libraries`/`library_items`。
- 首次启动 v3：建新表 → 为每个 source 入队 scan → 旧 `candidates` 保留只读一个版本，成功后删除。
- `schema_migrations` 记录 `v3_assets`、`v3_fts`、`v3_user_state`。
- 升级前导出用户数据快照（tags/ratings/collections）到 `plugin-data/backup-v2-<ts>.json`。

## 5. 提取管线

### 5.1 工具链契约

| 工具 | 必需 | 用途 | 缺失行为 |
| --- | --- | --- | --- |
| `ffprobe` | 是 | 时长/分辨率/fps/编码/音轨/图片尺寸 | 扫描与检索可用；元数据标 `probe_state=unsupported` |
| `ffmpeg` | 是 | 视频首帧、波形、PSD/TIFF 解码 | 缩略图降级图标；不阻塞索引 |
| `exiftool` | 否 | RAW/HEIC/XMP/GPS 深度元数据 | 用 Go EXIF + ffprobe 覆盖常见格式 |
| WIC + 系统扩展 | 否 | HEIC/HEIF/RAW 解码 | 标记“系统缺解码器” |

- 启动时 `runtime.status` 返回版本、路径、编码器矩阵、缺失项。
- 执行统一走参数数组、超时、取消杀进程、临时文件清理；不拼 shell。
- 并发默认 2（可配 1–8），优先处理可见区域和用户选中项。

### 5.2 类型矩阵

| 类型 | 探测 | 缩略 | 预览 | 专属筛选 |
| --- | --- | --- | --- | --- |
| 图片 | 尺寸、EXIF、方向、色彩 | 缩略图（Go/WIC，EXIF 旋转） | 原图/适应窗口 | 尺寸、宽高比、拍摄年月、相机 |
| 视频 | 宽高、时长、fps、编码、位深、HDR、旋转 | 1s 帧海报 | Range 流 | 分辨率档、时长、fps、编码、HDR |
| 音频 | 时长、采样率、声道、码率、标签、封面 | 封面或波形 + peaks | Range 流 + 波形拖动 | 时长、采样率、声道、码率 |
| 其他 | best-effort | 首帧/直出 | 只读 | 按类型 |

### 5.3 作业模型

状态机：

```text
queued → running → done
                 ↘ failed(retryable|permanent)
                 ↘ canceled
```

- 每类作业有 `checkpoint`（最后处理的 asset id / 时间戳），进程重启后继续。
- 扫描与探测分离：先让“文件名/目录”可搜，再补元数据与缩略图。
- 错误分类：`tool_missing`、`decode_failed`、`permission_denied`、`volume_offline`、`path_too_long`、`corrupt`。
- 任务中心展示：类型、范围、进度、失败数、耗时、错误样本。

### 5.4 缓存

- 缩略图：`grid` 256px、`preview` 640px；WebP/JPEG；按 `asset_id + mtime` 命名；LRU 配额（默认 2GB，可配）。
- 波形：1 张 PNG + 800 个 peaks（JSON/binary），供拖动定位。
- 缓存清理只删缓存，不动源文件；任务中心显示占用。

## 6. 用户数据双存储

### 6.1 模式

| 模式 | 权威 | 写入 | 适用 |
| --- | --- | --- | --- |
| `db` | SQLite | 直接写 DB | 纯本机 |
| `jsonl` | 资源库 `.himind-media/catalog/users/<actor>.jsonl` | 只追加事件，DB 物化 | SMB/共享 |
| `both` | JSONL | 事件 + DB | 本机也要可搬走 |

### 6.2 事件

```json
{"event_id":"evt-...","asset_key":"vol:1A2B/fid:...","op":"tag.add",
 "value":"夜景","actor":"马宝全","created_at":"2026-10-31T14:02:11Z","schema":1}
```

- `op`: `tag.add|tag.remove|rating.set|collection.add|collection.remove|note.set|flag`
- 合并：同字段 `(created_at, event_id)` LWW；删除用 tombstone；日志每 1,000 条压成 `snapshot.json`。
- 打开库 = snapshot + 增量回放；DB 可全量重建。
- 本机来源可选“导出 sidecar”：写到来源根目录 `.himind-media/user-data.jsonl`，不逐文件生成。

### 6.3 边界

- SMB 不落 SQLite；每个用户只写自己的 JSONL（沿用 2.0.0 设计）。
- 事件写入前做字段白名单、长度上限、actor 清洗；读取时校验 JSON，坏行跳过并计数。
- 提供“导出快照 / 从快照恢复 / 重建物化视图”三个操作。

## 7. 检索 v3

### 7.1 查询编译

```text
用户输入
  ├─ 引号短语 → 原样短语
  ├─ -词 → 排除
  ├─ folder:/kind: → 结构化过滤
  ├─ 连续中文 → cjk 短语（"龙 舟"）
  └─ 英文/数字 → 前缀（"dji"*）
```

- 多字段 AND；结果做一次 `strings.Contains` 后置校验，消除跨字段误命中。
- 排序：BM25 字段权重 + 名称完全匹配/前缀/文件夹命中加权 + 时间衰减。
- 排序选项：相关度、拍摄时间、修改时间、大小、时长、名称。
- 分页：keyset 游标（rank/updated_at/id），不使用 offset。

### 7.2 Facets

- 文件夹、类型、分辨率档、时长档、拍摄年月、相机、采样率/声道。
- 10 万：实时 `GROUP BY` 可接受；50 万：预聚合 `facet_counts(field, value, count)`，写路径增量更新，读路径带 TTL 缓存。

### 7.3 目标

| 指标 | 目标 |
| --- | --- |
| 本机检索 P95 | ≤50ms @10 万（含 1–2 字中文） |
| 键入到本地首屏 | ≤150ms |
| 缩略图命中 | ≤100ms |
| 在线来源 | 不阻塞本地首屏 |
| 50 万压测 | P95 ≤150ms（允许预聚合/缓存） |

## 8. 精选与沉淀

### 8.1 概念

- **精选集（selection）**：一次选片工作。输入是一个范围（文件夹、搜索结果、手动勾选），输出是入选清单。
- **精选库（curated library）**：沉淀目标。可以是一个真实文件夹（推荐 `E:\精选`），也可以是一个本地/SMB 资源库。
- **三态**：`候选 → 入选 / 备选 / 排除`；入选可撤销，所有决定留痕。
- **与“收藏”的区别**：收藏是个人标记；精选是一次有目标、有进度、有沉淀去向的决策。

### 8.2 工作流

1. **选范围**：从来源/文件夹/搜索结果进入，设定目标数量（如 50 选 10）和沉淀目标。
2. **自动预选（可选）**：相似聚类 → 技术质量 → 多样性 → 可选 AI 评分，输出候选、分数和理由。
3. **人工复核**：精选工作台（网格 + 大图 + 对比 + 键盘）：
   - `P` 入选、`X` 排除、`1–5` 评分、`Space` 预览、`C` 对比、`J/K` 上下；
   - 显示进度 `7/10`；支持“锁定已选，只自动补齐剩余”；
   - 相似组折叠，避免在同一组里反复看近似镜头。
4. **沉淀**：一键写入精选库；幂等去重；保留相对路径或扁平化；写 `.himind-media/curation.jsonl`。
5. **继续沉淀**：下次选片沿用同一精选库，按项目/日期累计；精选库视图聚合所有精选集。

### 8.3 自动预选

| 步骤 | 方法 | 说明 |
| --- | --- | --- |
| 相似聚类 | 关键帧 pHash（图片直接 pHash）；汉明距离阈值 | 视频取 1–3 帧；同组只保留质量最高的代表 |
| 技术质量 | 清晰度（Laplacian 方差）、曝光直方图裁切、稳定度（光流/场景活动）、黑帧/冻结、分辨率/fps、音频削波 | 纯本机，零费用 |
| 多样性 | 聚类配额 + MMR（最大边际相关） | 保证 10 个入选覆盖不同镜头，而不是 10 个近似重复 |
| 意图匹配 | 本地 CLIP 文本-图像相似；或视觉模型打标后匹配 | 对应“夜景城市航拍、水面倒影”这类描述 |
| 审美/可用性 | 视觉模型 1–5 分 + 简短理由 | 可选、受预算闸门 |
| 解释 | cluster、分数组成、与某条相似度、技术告警 | 用户可调权重、可覆盖 |

- 记录 `selection_runs`（算法、模型、参数、结果），保证可复现。
- 目标数量大于可用聚类时，回退到质量排序并提示“无更多差异镜头”。

### 8.4 沉淀模式

| 模式 | 行为 | 适用 |
| --- | --- | --- |
| `reference` | 只在精选清单登记，不落文件 | 只想记录选择，不占空间 |
| `hardlink` | 同卷 NTFS 硬链接，零额外空间、瞬时完成 | `E:\航拍素材` → `E:\精选`（推荐） |
| `copy` | 复制文件，独立于原素材 | 跨盘、需要独立副本或后续会改原文件 |

规则：

- 硬链接仅同卷；同文件 ID；删除原路径不影响内容；任一链接被修改会同时改变；跨盘移动会退化为复制。
- 默认保留来源相对路径：`精选/<项目>/<子目录>/<文件名>`；冲突加后缀；可选扁平化 + 日期前缀。
- 幂等：按内容快速指纹（size+mtime+首尾 1MB）或 pHash 去重；重复沉淀只补血缘，不重复落盘。
- 目标目录写 `.himind-media/curation.jsonl`：`selection_id/asset_id/源路径/模式/时间/操作者`。
- 若精选目录也被注册为来源，扫描时通过 `asset_locations` 归并到同一资产，不产生重复条目。
- SMB 精选库只允许 `copy`，避免把本机硬链接/路径写进共享目录。

### 8.5 精选库与血缘

- 每个精选条目都能回溯：来源资产、精选集、算法/模型版本、操作者、沉淀时间。
- 检查器显示“来自 `E:\航拍素材\...`”，并提供“打开原素材 / 在资源管理器中显示”。
- 原素材离线时：`copy` 的精选副本仍可用；`hardlink` 同卷仍可用；`reference` 标“离线”。
- 精选库可以继续参与检索、打标、AI 能力；不会因为“精选”而失去原始元数据。

### 8.6 成本

- 聚类、质量、多样性、pHash：**零费用**，纯本机。
- 视觉评分：默认每资产 1 帧；50 个资产约 50 张图，按 gpt-6-luna/claude-haiku 约 **$0.01–0.02**；3 帧/条约 $0.03–0.06。
- 每次精选可设独立预算上限（建议默认 $0.5），超限必须确认。
- 10 万/50 万级：先做本机聚类与质量筛选，只对入围候选调用视觉评分。

### 8.7 验收

1. 构造 50 个片段、10 个相似组，目标 10：入选覆盖 10 组，且任意两条 pHash 距离不低于阈值。
2. 人工覆盖自动结果后，刷新/重启仍保留；进度与决定可恢复。
3. 沉淀幂等：重复执行不新增文件、不重复入库。
4. 同卷硬链接后文件 ID 不变、磁盘占用不增加；跨卷自动改复制；`reference` 不落文件。
5. 精选目录重新扫描不产生重复资产（`asset_locations` 归并）。
6. 视觉评分前显示估算，超预算被拒绝。
7. 真实界面：`E:\航拍素材` 某个拍摄目录 50 选 10，沉淀到测试精选目录，再检索验证。

### 8.8 非目标

- 不做自动剪辑/成片；精选结果交给后续工作流。
- 不做多人同时编辑同一精选集的实时协作（JSONL 合并按事件即可）。

## 9. 媒体预览：Agent 统一端点

> 实现状态（2026-10-31）：已落地 `src/app/media_stream.rs` + `/plugin-media` 路由，
> 插件用 `HIMIND_MEDIA_STREAM_BASE/SECRET` 签发票据；2.8GB 视频拖动验证通过。

### 9.1 为什么不放插件

- 插件各自开 loopback 会重复鉴权/白名单逻辑，端口数量随插件增长；
- 媒体预览是平台能力，媒体处理、动作编排等插件都要用；
- Agent 已有只监听 `127.0.0.1` 的本地服务和安全校验层，扩展它更可控。

### 9.2 契约

```text
插件进程启动 → Agent 注入 HIMIND_MEDIA_STREAM_SECRET（每插件独立）
插件签发 ticket = HMAC(secret, path + exp + plugin_id)
视图请求     GET /plugin-media?ticket=<...>
Agent 校验   签名、过期、插件身份、路径在已注册来源内
响应         200/206 + Content-Range + Accept-Ranges + ETag + Last-Modified
限制         只读、仅 127.0.0.1、5–10 分钟过期、无目录浏览、并发上限
```

- 视图 CSP 已允许 `media-src http:`，无需放 `file:`。
- `HEAD` 支持；`If-Range` 支持；`416` 正确处理。
- 失败返回结构化错误，不泄露绝对路径。

### 9.3 Spike 验收（0.5–1 天）

1. 用 2.9GB 的 `DJI_..._0045_D.MP4` 验证首帧、拖动、暂停恢复；
2. 验证 CSP 无报错、Origin/Referer 行为符合预期；
3. 验证插件重启、Agent 重启、多个标签页；
4. 验证越权路径、过期 ticket、错误签名均被拒绝；
5. 不通过则退回“插件 loopback（临时）/系统播放器打开”。

## 10. 智能能力

### 10.1 范围

| 编号 | 能力 | 实现 | 成本 |
| --- | --- | --- | --- |
| S1 | 语音转写 | `media-ai` 本地模型，VAD 先过滤静音 | 无按次费用；模型磁盘 + CPU |
| S2 | AI 打标/描述 | 关键帧 + 已接入视觉模型 | 按 token；预算闸门 |
| S3 | 语义检索 | 本机 CLIP/Chinese-CLIP 向量 + 关键词混合 | 无按次费用；CPU/GPU 时间 |
| S4 | 找相似 | 图片/关键帧 pHash；后续接向量 | 无按次费用 |
| S5 | OCR | Windows OCR 优先，视觉模型兜底 | 本机零费用；模型兜底按 token |

说明：当前 Agent 已接入服务只有对话类模型，**没有配置转写、图像生成、嵌入模型**；因此 S1 走本机 `media-ai`，S3 走本机 CLIP，S2/S5 需要一个“视觉对话”能力（Agent 侧新增或扩展 `media-ai`），复用已接入的视觉模型。

### 10.2 数据与作业

- `asset_ai` 记录标签/描述/OCR，带 `model`、`model_version`、`confidence`；
- `asset_transcript` 存分段文本与时间戳；
- `asset_embedding` 存向量与维度；
- 作业类型 `transcribe|tag|embed`，带预算字段与取消；
- 模型/版本变化 → 对应作业可重跑，旧结果保留可对比。

### 10.3 成本闸门

- 执行前估算：`资产数 × 单资产 token × 单价`；超过阈值必须确认；
- 预算表：`ai_budget(scope, period, limit_usd, used_usd)`；80% 警告、100% 硬停；
- 每条结果记录实际 token/费用（优先用 provider `usage`，缺失则估算并标注）；
- 默认建议：单任务 $2、单日 $5、单月 $20；首批 E 盘全量 <$1（按 gpt-6-luna/claude-haiku 单价估算）。
- 使用配额型服务（opencode-go）时同时显示配额消耗。

### 10.4 评测

- 30–50 条真实查询（示例：龙舟、夜景、水面、人群、4K 横屏、骆岗）；
- 指标：recall@10、MRR、ASR 分段命中率、语义查询 P95；
- 每次模型/索引变更跑一遍，结果落 `evidence/`。

## 11. 规模策略

| 维度 | 10 万 | 50 万 |
| --- | --- | --- |
| 索引 | 单库 0.3–0.5GB | 单库可行，按卷分区，1.5–3GB |
| FTS | 约 0.1GB，实时 facets | 0.5–1GB，预聚合 facets |
| 缩略图 | 约 2GB，单层 LRU | 10GB+，两级 + 配额 |
| 扫描 | 1–3 分钟 | 10–30 分钟（SSD） |
| 探测/抽帧 | 4 并发，1–3 小时 | 8–16 并发 + 限流，8–24 小时 |
| 监听 | 目录监听 | USN Journal + 定期对账 |
| 重建 | 分钟级 | 十分钟级，按卷重建 |
| 导出 | 百 MB | GB 级流式 |

现在就要落的结构：`volume_id`、作业表、预聚合 facets、缓存配额、虚拟列表、keyset 分页、监听接口。

## 12. 界面

- 三种浏览：网格、列表（可排序多列）、按文件夹/项目。
- 精选工作台：范围/目标数设置、相似组折叠、网格 + 大图、2–4 路对比、键盘 `P/X/1–5/Space/C`、进度 `n/N`、锁定已选后自动补齐。
- 检查器：EXIF/视频参数/音频参数/标签/集合/路径/转写文本/精选血缘。
- 批量：多选、打标、集合、采纳、沉淀到精选库、复制路径、资源管理器定位。
- 任务中心：扫描/探测/抽帧/转写/打标/精选评分进度与错误。
- 精选库视图：按精选集/项目/日期聚合，显示累计入选数、重复项、来源。
- 索引状态：按类型计数、待处理、失败、缓存占用、重建入口。
- 离线盘：条目保留，显示“离线”，不删除。

## 13. 安全与隐私

- 来源只读；不写源文件；标签只在 DB/库 JSONL。
- 媒体端点：签名 ticket、过期、路径白名单、仅回环、只读。
- AI：逐来源开关；默认本机；调用云端前明确告知并受预算限制；日志脱敏。
- 不读取/传递凭据；不把绝对路径暴露给非本机。

## 14. 迁移与回滚

- 升级前导出用户数据快照；派生数据重建。
- v3 失败可回滚到 2.0.0：保留旧表一个版本，回滚只丢新增的 AI/元数据。
- 任何 schema 变更走 `schema_migrations`，不手改 DB。

## 15. 验收

### 15.1 单元/集成

- 提取器：jpg/png/webp/tiff/psd、mp4/mov、wav/mp3 的最小样例；
- 身份：同卷改名/移动后标签仍命中；换卷/换盘提示；
- 双存储：两客户端并发打标、离线编辑、回放合并；
- 检索：中文 1/2/3 字、英文前缀、文件夹筛选、排序、游标；
- 精选：50 条/10 组覆盖与去近似、覆盖后持久化、沉淀幂等、硬链接/复制/引用三种模式；
- 50 万合成数据基准（P95 门限）。

### 15.2 页面验收（真实界面）

- development profile、端口 18082、真实数据；
- `E:\航拍素材`（视频）、`~\Pictures`（图片）、`C:\Windows\Media`（音频）；
- 路径：添加来源 → 扫描 → 搜索 → 筛选 → 预览 → 打标 → 精选 → 沉淀 → 任务中心；
- 精选：某个拍摄目录 50 选 10，覆盖自动结果，沉淀到测试精选目录，再检索验证；
- 检查 console CSP、媒体拖动、离线态、空态/错误态；
- 非破坏性：不复制入库、不删除用户素材、不动 AI 配置。

## 16. 里程碑

| 阶段 | 出口条件 |
| --- | --- |
| M0 | 本设计评审通过；成本预算确认；媒体端点 spike 通过 |
| M1 | 三类素材可索引/出图/出波形；作业可恢复；E 盘可搜 |
| M2 | 中文 1–2 字达标；批量标签；双存储可用；手动精选 + 本机自动预选 + 沉淀（hardlink/copy/reference） |
| M3 | Range 预览；任务中心；缓存配额；离线/重建；精选库视图与血缘 |
| M4 | S1–S5 可用；预算闸门；评测达标；视觉评分/文本意图接入精选 |
| M5 | 50 万压测报告；USN Journal；按卷重建 |

## 17. 开放问题

1. 视觉打标选哪个已接入模型（需实测图片输入支持与单价）。
2. 本机 CLIP 选型（Chinese-CLIP / SigLIP）与模型体积/CPU 时间。
3. `exiftool` 是否默认引导安装。
4. 首批 AI 预算上限（建议单任务 $2 / 单日 $5 / 单月 $20）。
5. 精选库默认位置与默认沉淀模式（建议 `E:\精选` + 同卷 hardlink）。
