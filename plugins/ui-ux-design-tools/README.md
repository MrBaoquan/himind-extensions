# 界面设计工具

把 UI UX Pro Max 的设计知识快照封装为 HiMind 本地插件。插件在运行时从内置数据检索，不访问网络；除显式调用设计系统持久化能力外，不读写项目文件。

## 能力

| Capability | 风险 | 用途 |
| --- | --- | --- |
| `ui-ux.catalog.describe` | 只读 | 查看数据版本、许可、检索域、技术栈和运行时策略 |
| `ui-ux.guideline.search` | 只读 | 按设计域或技术栈检索规范、风格、配色、字体、图表和 UX 规则 |
| `ui-ux.design-system.generate` | 只读 | 生成风格、配色、字体、间距、动效、图表、UX 约束和检查清单 |
| `ui-ux.project.inspect` | 只读 | 识别项目技术栈、设计系统、主题/XAML 令牌文件与组件约束 |
| `ui-ux.design-system.persist` | 工作区写入 | 受控写入 `design-system/<project>/MASTER.md` 和页面覆盖文件 |
| `ui-ux.token.validate` | 只读 | 检查 CSS、JSON、XAML 令牌、硬编码颜色和语义颜色对对比度 |

## 检索域

`product`、`style`、`color`、`typography`、`landing`、`chart`、`ux`、`icons`、`gsap`、`react`、`web`、`google-fonts`、`reasoning`。

支持 22 个技术栈：Angular、Astro、Avalonia、Flutter、HTML + Tailwind、JavaFX、Jetpack Compose、Laravel、Next.js、Nuxt、Nuxt UI、React、React Native、shadcn/ui、Svelte、SwiftUI、Three.js、Uno、UWP、Vue、WinUI、WPF。

## 项目上下文检查

```json
{"workspace_root":"F:\\path\\to\\project","max_depth":4,"max_files":3000}
```

返回识别到的技术栈、设计系统文件、令牌文件和扫描证据。支持 Avalonia `.axaml` 与 WPF `.xaml` 工程，并识别 `App.axaml`、主题、资源和令牌类 XAML 文件。扫描不进入 `node_modules`、`.git`、构建目录或符号链接，并受深度和文件数限制。

## 生成设计系统

```json
{"query":"中文企业运维仪表盘","project_name":"运维台","variance":8,"motion":6,"density":8}
```

结果包含产品类型、风格、语义颜色、对比度粗检、字体、间距、动效、图表、UX 检查项、CJK 字体搭配和来源信息。

## 受控持久化

```json
{
  "workspace_root":"F:\\path\\to\\project",
  "project_slug":"ops-console",
  "page_slug":"overview",
  "content":"# Overview\n\n...",
  "overwrite":false
}
```

- 目标只能是 `design-system/<project-slug>/MASTER.md` 或 `pages/<page-slug>.md`。
- slug 使用 lowercase-hyphen-case。
- 默认拒绝覆盖；`overwrite=true` 必须在用户确认后由技能传入。
- 目标路径不能经过符号链接，content 上限 512 KiB。

## 令牌校验

```json
{"workspace_root":"F:\\path\\to\\project","paths":["src/styles/tokens.css"],"max_files":50}
```

支持 CSS 自定义属性、JSON 颜色令牌，以及 Avalonia/WPF XAML 中的 `SolidColorBrush` 颜色资源。返回发现的设计令牌、硬编码颜色、已知语义颜色对的 AA 对比度检查与结构化问题。XAML 注释和令牌声明不会被误报为硬编码颜色；对比度计算只适用于可解析的十六进制颜色，不替代真实主题、图片背景和透明度的完整审查。

## 数据来源

- 项目：UI UX Pro Max
- 版本：`2.15.0`
- 提交：`d14efff8ed1faa4187f004f79926f428c8074ec7`
- 许可：MIT，见 `LICENSES/ui-ux-pro-max-MIT.txt`
- 数据快照：`data/`
- 第三方来源：`data/data-provenance.json`、`data/google-font-licenses.json`
- 中文字体搭配：`data/cjk-guidance.json`，不随插件分发字体文件

运行时不会访问 GitHub、Google Fonts 或 npm。Google Fonts 链接只用于识别字体和项目侧决策。

## 构建与测试

```powershell
go test ./...
go vet ./...
go build -o bin/ui-ux-design-tools.exe .
```

JSON-RPC over stdio 使用每行一个请求：

```json
{"jsonrpc":"2.0","id":1,"method":"ui-ux.catalog.describe","params":{}}
```

## 边界

- 设计数据是启发式知识，不是品牌规范、WCAG 认证或真实界面验收。
- 持久化能力只写工作区内的设计系统文件，不执行任意脚本。
- 字体建议不包含字体文件；上线前必须确认授权、字重和离线可用性。
- 真实界面仍需截图、键盘、响应式、空数据与错误状态验证。