# IOStudio 工具

驱动 **IOStudio 设备调试工作台**：查设备与动作通道、构建与预检动作文件、执行协议测试场景。

## 为什么按产品命名，而不是按功能

IOStudio 是**设备调试工作台**，动作编排只是它四个域之一（另有设备表、协议测试、输出测试）。
最初的版本叫 `motion-authoring`、能力前缀也是 `motion-authoring.*` —— 那等于让一个"工作台包装"
顶着"某功能包装"的名字，把其它域挡在门外。

改成按产品命名之后，能力按**域**分组，新域只是新增一个能力组：

| 能力 | 域 |
| --- | --- |
| `iostudio.runtime.status` | 工作台自身（找到没、版本、生效设备表） |
| `iostudio.devices` | 设备表 |
| `iostudio.motion.plan` / `.build` / `.validate` / `.tracks` | 动作编排 |
| `iostudio.protocol.test` | 协议测试台 |

**代价对比**：按功能命名时，每来一个新域就得新建一个插件，各自复制一遍"定位 CLI、跑子进程、
解析结果、守工作区边界"的管道。按产品命名后，这些只有一份。

## 一条硬边界：不重写工作台的逻辑

动作文件的构建与预检、协议测试的执行，在 IOStudio 里都已是一等公民，且规则会随产品演进。
**在 Go 里复刻会产生第二真相源**——两份实现迟早不一致，而这类不一致是静默的：
插件说"通过"，真机却丢掉输出。所以这里只做两件事：**受控地调用** CLI，**解析它的结构化结果**。

## 依赖

需要本机有 IOStudio。定位顺序：

1. 调用参数 `cli_path`
2. 环境变量 `IOSTUDIO_CLI`
3. `PATH` 里的 `iostudio` / `IOStudio.exe`
4. `%LOCALAPPDATA%\Programs\IOStudio\IOStudio.exe`

所有能力都接受 `cli_path` 覆盖，因为**它同时决定用哪份设备表**（见下）。

## 必须知道的一件事：用的是哪份设备表

**设备表决定了"哪些设备与动作算已声明"**，它按 IOStudio 安装目录解析。
发布包自带的 `IODevice.xml` 往往是空的（刻意如此，避免覆盖现场配置），
这时预检会对任何编排都报"设备未声明"。

所以每次调用都会回显 `cli` / `config_dir` / `device_table` —— 让不匹配**可见**，
而不是让人对着一堆假问题排查。

## 能力

### 工作台
```
iostudio.runtime.status   { cli_path? }
```
返回是否就绪、版本、配置目录、生效设备表、可用能力清单。`device_table` 为空说明装的是"出厂态"，
任何编排预检都会报设备未声明。

### 设备表
```
iostudio.devices          { cli_path?, device? }
```
设备 → 动作 → 输出通道。**写任何编排前先查这个**：通道名靠猜会导致输出被**静默丢弃**
（不报错、只是设备不动）。返回里带 `warnings`（例如某个动作没有 Key）时一并留意。

### 动作编排
```
iostudio.motion.plan      { workspace_root, spec_path, cli_path?, skip_validate? }
iostudio.motion.build     { workspace_root, spec_path, out_path?, cli_path?, skip_validate? }
iostudio.motion.validate  { workspace_root, motion_path, cli_path? }
iostudio.motion.tracks    { workspace_root, motion_path, cli_path? }
```

- `plan`：构建 + 预检，**不保留产物**（先看方案）。构建前复核用。
- `build`：构建动作文件并预检，写回工作区。
- `validate`：预检已有动作文件，返回问题清单。
- `tracks`：轨道 → 通道映射（不建设备、不开窗口，最快）。

演出规格（spec）的字段见技能 `video-motion-authoring` 的 `references/show-spec.md`。

### 协议测试
```
iostudio.protocol.test    { workspace_root, scenario_path, report_path?, keep_report? }
```
执行协议测试场景，返回逐步结果。退出码 0 = 全部通过、2 = 有失败步骤。
**报告默认不落盘**（内容已结构化回传）；需要留存时传 `keep_report: true`。

返回里 `failed_steps` 只列失败步骤 —— 报告可能几十步，全列出来没人看。

## 安全与边界

- 所有路径（规格、动作文件、场景、报告）都必须落在 `workspace_root` 内，越界拒绝。
- 不提供"执行任意 IOStudio 参数"的能力 —— 那是变相的任意命令执行。参数由各能力固定。
- 本插件是**只读 + 受控写**（写动作文件与报告），不改设备表、不改现场配置。

## 开发

```bash
go test ./...                    # 单元测试
IOSTUDIO_CLI=<路径> go test ./...  # 连带跑集成测试（需要真实 IOStudio）
```

集成测试会真的调用 IOStudio：先读设备表、再按真实声明生成规格（不猜通道名），
然后构建与预检。本机没有 IOStudio 时自动跳过。

集成测试曾经抓到两个真问题，都值得记住：状态文件带 **UTF-8 BOM**（.NET 的 `Encoding.UTF8`
会写 BOM，Go 的 `json.Unmarshal` 直接报错），以及干跑早退时泄漏产物路径。
