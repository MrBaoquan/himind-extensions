# 工作日志插件界面审查报告

**审查对象**：`plugins/work-log`（`com.himind.work-log` 0.16.0）扩展视图
**审查日期**：2026-11-04
**审查人**：界面设计助手（以「软件界面规范」为审查基线）
**审查范围**：`ui/index.html`、`ui/app.css`、`ui/app.js` 渲染出的 6 个视图（工作台、计划、日历回看、报告、工时表、项目）与 5 个模态
**适用平台**：桌面 WebView（HiMind Agent 扩展视图窗，Chromium / WebView2）

## 结论先行

界面整体结构与层次是成立的：单一主视图轴 + 顶部工具栏、令牌化的圆角与语义色、空态/加载态/错误态齐备、模态焦点管理正确、复制到 Excel 的双格式剪贴板实现正确。

**本报告提出的问题已全部修复完毕（0.17.0）**，逐项复验结果见第八节。修复前的问题分级如下：

| 级别 | 数量 | 内容 | 状态 |
| --- | --- | --- | --- |
| 高 | 1 类（20 个控件） | 聚焦环被 `overflow:hidden` 容器裁掉，键盘用户看不到焦点在哪 | 已修复 |
| 中 | 4 项 | 4 处文本对比度低于 4.5:1；6 类控件高度 < 24px；工时表单元格不可键盘到达；报告视图标题跳级 | 已修复 |
| 低 | 3 项 | tablist 缺方向键契约；窄窗 ≤340px 计划工具栏越界；搜索框聚焦外环对比度 1.02:1 | 前两项已修复，末项判定不改 |

审查过程中还新发现 1 项报告未收录的缺陷：`.agg-jump:focus-visible` 把 `outline` 设为 `none` 却没有替代提示，导致报告页归档跳转按钮**完全没有**键盘焦点指示，已一并修复。

需要说明的是：审查过程中有 3 条「疑似缺陷」经复验后**不成立**，已在第六节「排除的误报」中列出，避免据此改动代码。

---

## 一、问题清单

### P1（高）聚焦环被裁切容器吃掉

**现象**：用键盘 Tab 到顶栏视图页签、工作台分段控件、报告周期、工时表口径、项目视图分段控件时，蓝色聚焦环只有下方一侧可见，上、左两侧被切除。

**证据**：
- `.view-switch`、`.seg` 都声明了 `overflow: hidden`（[app.css:1412](plugins/work-log/ui/app.css#L1412)、[app.css:1582](plugins/work-log/ui/app.css#L1582)），圆角靠它裁出。
- 全局聚焦环为 `outline: 2px solid var(--accent); outline-offset: 2px`（[app.css:1479-L1482](plugins/work-log/ui/app.css#L1479-L1482)），即环体在元素盒**外侧 2px 起、厚 2px**，共外扩 4px。
- 实测（1024×900，真实键盘 Tab）：`.seg-btn.active` 元素盒 `left:774 top:227 right:884 bottom:263`，裁切容器 `#wb-modes.seg` 为 `773/226/981/264`；环区域 `769/222/889/268` → 上、下、左三边均越界被裁。
- 全量扫描 6 个视图共命中 **20 个可聚焦控件**，覆盖 `.view-switch`（6 个页签）与 5 个 `.seg`（工作台模式、报告周期、工时表口径、项目视图，以及计划页复用）。

**方向归类**：无障碍与包容性（WCAG 2.4.11 / 2.4.13）

**根因**：圆角裁切（`overflow: hidden`）与外侧聚焦环（`outline-offset: 2px`）是两个互相冲突的意图，规范里没有为「在被裁容器内的可聚焦子元素」单独定义聚焦表达。

**修复方案**：改用内阴影表达聚焦，环体落在元素盒内，不参与裁切。

```css
/* 在被 overflow:hidden 裁切的容器内，外侧 outline 会被切掉。
   这些子控件改用内阴影表达聚焦，环体始终在盒内。 */
.view-switch .switch:focus-visible,
.seg .seg-btn:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 2px var(--accent);
}

/* 深色填充的激活项：inset 环与底色同色会消失，改用白色内环。 */
.view-switch .switch.active:focus-visible,
.seg .seg-btn.active:focus-visible {
  box-shadow: inset 0 0 0 2px #fff;
}
```

**验收标准**：
1. 键盘 Tab 到上述 20 个控件中的任意一个，四周均可见完整闭合的聚焦环（截图可见，不依赖 computed style）。
2. 聚焦环与按钮自身底色对比度 ≥ 3:1（激活项白环 vs `#2563eb` = 4.86:1；非激活项 `#2563eb` vs 白 = 5.17:1）。
3. `document.querySelectorAll('.seg-btn:focus-visible')` 命中时，`getComputedStyle(el).outlineStyle === 'none'` 且 `boxShadow` 含 `inset`。

---

### P2（中）4 处文本对比度低于 4.5:1

**证据**（均为 11px 常规字重，属正文级，要求 ≥ 4.5:1）：

| # | 元素 | 前景 / 背景 | 实测 | 位置 |
| --- | --- | --- | --- | --- |
| a | `#wb-recent-count`（激活态计数徽标） | `#fff` on 白 22% 叠 `#2563eb` = `#5585ef` | **3.49:1** | [app.css:3791-L3800](plugins/work-log/ui/app.css#L3791-L3800) |
| b | `#wb-planned-count`（非激活态计数徽标） | `#64748b` on `#eef2f7` | **4.23:1** | [app.css:3802](plugins/work-log/ui/app.css#L3802) |
| c | `.pi-meta`（选中项目行的元信息） | `#64748b` on `#eff6ff` | **4.37:1** | [app.css:2451](plugins/work-log/ui/app.css#L2451) |
| d | `.stage-pill.stage-unknown`（未判断） | `#94a3b8` on `#f8fafc` | **2.45:1** | [app.css:3596](plugins/work-log/ui/app.css#L3596) |

**方向归类**：无障碍与包容性（WCAG 1.4.3）

**根因**：a 是「半透明白色徽标底」叠在彩色填充上，实际底色被计算成中间色，白色文字掉到 3.49；d 用 `#94a3b8` 表达「未判断」的弱化语义，但弱化到 2.45 已低于可读下限。

**修复方案**：

```css
/* a：激活态徽标改用实色深底，避免半透明叠加后白字掉到 3.4:1 */
.wb-mode-head .seg-btn.active span {
  background: rgba(0, 0, 0, 0.24);
}

/* b/c：元信息灰在浅底上统一加深到 #475569（对 #eef2f7 为 6.9:1） */
.wb-mode-head .seg-btn:not(.active) span,
.pi-meta {
  color: #475569;
}

/* d：未判断状态本身需要弱化观感，但文字必须可读 */
.stage-pill.stage-unknown {
  background: #f1f5f9;
  color: #475569;
}
```

**验收标准**：上述 4 处实测对比度均 ≥ 4.5:1；状态区分不依赖颜色（每个 pill 都有文字「未判断」，已满足 WCAG 1.4.1）。

---

### P3（中）6 类控件命中高度小于 24×24 CSS px

**证据**（1024 宽实测；WCAG 2.5.8 AA 要求 ≥ 24×24）：

| 控件 | 实测 | 出现视图 | 位置 |
| --- | --- | --- | --- |
| `.link-btn`（需要关注跳转） | 105.2×**19.2** | 工作台 | [app.css:1445](plugins/work-log/ui/app.css#L1445) |
| `.link-btn` / `.lock` / `.danger`（条目操作） | 24×**19.2** | 项目 | 同上 |
| `.entry-toggle`（展开/收起） | 36×**19.2** | 项目 | [app.css:623](plugins/work-log/ui/app.css#L623) |
| `.mx-copy`（工时表复制） | 24×**20** | 工时表 | [app.css:2318](plugins/work-log/ui/app.css#L2318) |
| `.agg-jump`（归档跳转） | 91.3×**20.8** | 报告 | [app.css:1907](plugins/work-log/ui/app.css#L1907) |
| `#log-root`（日志目录路径） | 158×**21.6** | 日历回看 | [app.css:355](plugins/work-log/ui/app.css#L355) |
| `.plan-prev/.plan-next` | **20.8**×28 | 计划（360px 下被 flex 压缩） | [app.css:234](plugins/work-log/ui/app.css#L234) |

**方向归类**：无障碍与包容性（WCAG 2.5.8）+ 布局容纳性（计划页两处是收缩导致，非设计尺寸）

**根因**：`.link-btn`、`.mx-copy`、`.agg-jump`、`#log-root` 都用 `padding: 0` 或极小内边距做「文字型按钮」的紧凑外观，命中区等于文字行高（19.2/20px）。`.cal-nav` 本来是 28×28，但在 360px 下被 `flex-wrap: nowrap` 的父容器压缩到 20.8。

**修复方案**：保持视觉紧凑，只扩大命中区（内边距透明，不影响布局观感）：

```css
/* 文字型按钮：视觉不变，命中区撑到 24px 高。
   紧凑外观靠负 margin 抵消，不改变行内排版。 */
.link-btn,
.agg-jump,
.entry-toggle,
.log-root-path {
  padding-block: 2px 3px;
  min-height: 24px;
}

/* 复制按钮：24×20 → 24×24 */
.matrix .mx-copy {
  min-width: 24px;
  min-height: 24px;
}

/* 计划/日历翻页按钮不允许被 flex 压缩 */
.cal-nav {
  flex: 0 0 auto;
}
```

**验收标准**：上表 7 项在 320 / 768 / 1024 / 1440 四个宽度下实测 `width ≥ 24 && height ≥ 24`；同级元素间距视觉与改前一致（行高差由 `padding-block` 内的透明区域承担）。

---

### P4（中）工时表单元格可点击但键盘不可达

**证据**：
```
tag: td, tabIndex: -1, role: null, cursor: pointer, focusable: false
```
单元格绑定 `click` 跳到日历视图（[app.js:1959-L1964](plugins/work-log/ui/app.js#L1959-L1964)），但 `td` 无 `tabindex`、无 `role`、无键盘事件。鼠标用户能「点日期跳过去」，键盘用户完全无法触发。

**方向归类**：无障碍与包容性（WCAG 2.1.1 键盘 / 2.4.7）

**根因**：`td` 上直接绑 click 是表格交互的常见捷径，但它不是原生可聚焦元素。

**修复方案**：改为「单元格可聚焦 + Enter 触发」，并在行头/表头已有 `scope` 的基础上补表格名：

```js
// 单元格：补 tabindex、role 与 Enter/Space 处理，使键盘等价于鼠标点击。
'<td class="mx-cell' + (minutes > 0 ? ' has' : '') +
  '" tabindex="0" role="button"' +
  '" aria-label="' + escapeHTML(title) + '"' + ...
```
```js
body.querySelectorAll('.mx-cell[data-date]').forEach((cell) => {
  const open = () => {
    const date = cell.getAttribute('data-date');
    if (date) openDateFromMatrix(date);
  };
  cell.addEventListener('click', open);
  cell.addEventListener('keydown', (event) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      open();
    }
  });
});
```

**验收标准**：Tab 可进入第一个有数据单元格；Enter 与 Space 均能跳转到对应日期；单元格有可读名称（`aria-label` 含日期与项目）。

---

### P5（中）报告视图标题跳级 h1 → h3

**证据**：报告视图可见标题为 `h1 报告`、`h3 工作类型分布`、`h3 核心脉络`、`h3 按日期`、`h3 后续事项`、`h3 阻塞与风险`，缺 h2。

**方向归类**：无障碍与包容性（语义结构）+ 一致性与规范对齐（其余 5 个视图均为 h1→h2）

**根因**：报告正文的 Markdown 渲染复用了 h3 作为分节标题，而视图本身没有 h2 层。

**修复方案**：把报告分节标题降为 h2（或给每组加 h2 分组）。推荐直接改渲染层：

```js
// 报告分节标题统一用 h2，与其它视图的 h1→h2 层级一致。
'<h2>' + escapeHTML(section.title) + '</h2>'
```

**验收标准**：6 个视图各自可见标题序列无跳级（`level[i] - level[i-1] <= 1`）。

---

### P6（低）tablist 缺方向键与 roving tabindex

**证据**：顶栏 `role="tablist"` 下 6 个 `role="tab"` 按钮，全部 `tabindex=null`（即 6 个都是 Tab 停靠点）；按 `→` 后焦点与选中态均未移动（`active: view-workbench`，`selected: [view-workbench]`）。

**方向归类**：一致性与规范对齐（ARIA APG Tabs 模式）

**说明**：任务能完成（Enter 可切换），属模式偏差而非功能缺失。按 APG，tablist 应只有一个 Tab 停靠点，方向键在页签间移动。

**修复方案**：

```js
// ARIA APG Tabs：tablist 只保留一个 Tab 停靠点，←/→ 在页签间移动并切换。
function syncTabStops() {
  document.querySelectorAll('.view-switch .switch').forEach((tab) => {
    const on = tab.getAttribute('aria-selected') === 'true';
    tab.tabIndex = on ? 0 : -1;
  });
}

el('view-switch-container').addEventListener('keydown', (event) => {
  const keys = ['ArrowLeft', 'ArrowRight', 'Home', 'End'];
  if (keys.indexOf(event.key) < 0) return;
  event.preventDefault();
  const tabs = Array.from(document.querySelectorAll('.view-switch .switch'));
  const current = tabs.indexOf(document.activeElement);
  if (current < 0) return;
  let next = current;
  if (event.key === 'ArrowLeft') next = (current - 1 + tabs.length) % tabs.length;
  if (event.key === 'ArrowRight') next = (current + 1) % tabs.length;
  if (event.key === 'Home') next = 0;
  if (event.key === 'End') next = tabs.length - 1;
  tabs[next].focus();
  switchView(tabs[next].getAttribute('data-view'));
  syncTabStops();
}
```

**验收标准**：Tab 进入 tablist 时只落在当前选中项；←/→/Home/End 移动焦点并同步切换视图；`aria-selected` 与 `tabindex` 保持一致。

---

### P7（低）窄窗 ≤340px 计划工具栏越界

**证据**：计划页 `.plan-date-nav` 为 `flex-wrap: nowrap` 且内容宽度固定约 301px。窗口收窄时：

```
420px: 文档溢出 0，越界=[]
360px: 文档溢出 0，越界=[]
340px: 文档溢出 4，plan-today 越界 4px
320px: 文档溢出 24，plan-today 越界 24px
```

即 ≤340px 时「今天」按钮部分/全部移出视口，需横向滚动才能触达。

**方向归类**：布局与响应式容纳性（WCAG 1.4.10 重排）

**平台说明**：本插件运行在桌面 WebView 面板，实际可用宽度通常 ≥360px。按审查基线仍需记录；优先级定为低。

**修复方案**：

```css
/* 计划日期导航：窄窗允许换行，按钮不被压缩 */
@media (max-width: 380px) {
  .plan-date-nav {
    flex-wrap: wrap;
    row-gap: 6px;
  }
}
```

**验收标准**：320px 宽下 `documentElement.scrollWidth === clientWidth`，`plan-today` 完整可见。

---

### P8（低）搜索框聚焦外环对比度 1.02:1

**证据**：聚焦时容器 `.search-box` 得到 `border-color: #2563eb`（对页面底 4.86:1，**有效**）+ `box-shadow: 0 0 0 3px #eff6ff`（`--accent-soft`，对页面底 **1.02:1**，对白 **1.09:1**）。

**判定**：**不构成缺陷**。真正的焦点指示由 1px 实色边框提供，对比度 4.86:1，满足 WCAG 2.4.11 的 3:1。`--accent-soft` 光晕只是装饰层。此项仅记录，**不建议改动**（改成高对比外环会让搜索框在安静顶栏里显得突兀）。

---

## 二、方向归类汇总

问题集中在两个方向，说明是**同一批缺省值的系统性缺口**，不是零散手误：

1. **无障碍与包容性**（P1、P2、P3、P4、P5、P6 六项中的多数）
   根因是同一条：`app.css` 把「紧凑」和「裁剪圆角」当成视觉目标，而把「可聚焦」「可读」「可命中」当成默认成立。聚焦环被裁、命中区等于行高、弱化色压到 2.45:1，都是这一取向的产物。

2. **布局与响应式容纳性**（P3 中的 `.cal-nav`、P7）
   `.seg`、`.view-switch`、`.plan-date-nav` 都用不换行 + 收缩，在窄窗下会压缩或推出控件。已有 `min-width: 0` 的集中约束块（[app.css:1484-L1510](plugins/work-log/ui/app.css#L1484-L1510)）解决了长文本撑破，但没有覆盖「控件被压到不可点击」。

**结论**：建议按「集中式约束块」方式落地 P1/P3，一次覆盖全部视图；P4/P5/P6 是渲染层与语义层，逐点改。

---

## 三、修复方案汇总（精确到文件与选择器）

| 问题 | 文件 | 位置 | 改动 |
| --- | --- | --- | --- |
| P1 聚焦环被裁 | `ui/app.css` | 聚焦环定义之后（[L1479](plugins/work-log/ui/app.css#L1479)） | 为 `.view-switch .switch`、`.seg .seg-btn` 增加 `inset` 内环，激活项用白环 |
| P2 对比度 | `ui/app.css` | [L3791](plugins/work-log/ui/app.css#L3791)、[L3802](plugins/work-log/ui/app.css#L3802)、[L2451](plugins/work-log/ui/app.css#L2451)、[L3596](plugins/work-log/ui/app.css#L3596) | 4 处色值调整 |
| P3 命中区 | `ui/app.css` | [L1445](plugins/work-log/ui/app.css#L1445)、[L623](plugins/work-log/ui/app.css#L623)、[L2318](plugins/work-log/ui/app.css#L2318)、[L1907](plugins/work-log/ui/app.css#L1907)、[L355](plugins/work-log/ui/app.css#L355)、[L234](plugins/work-log/ui/app.css#L234) | 6 组 `min-height` / `min-width` / `flex: 0 0 auto` |
| P4 单元格键盘 | `ui/app.js` | [L1959-L1964](plugins/work-log/ui/app.js#L1959-L1964) 与单元格模板 [L1881](plugins/work-log/ui/app.js#L1881) | 加 `tabindex`、`role`、`aria-label`、`keydown` |
| P5 标题跳级 | `ui/app.js` | 报告分节渲染处 | `h3` → `h2` |
| P6 tablist | `ui/app.js` | `switchView` 与 `bind` | roving `tabindex` + 方向键 |
| P7 窄窗 | `ui/app.css` | `.plan-date-nav`（[L3246](plugins/work-log/ui/app.css#L3246)） | `@media (max-width: 380px)` 允许换行 |

> 前端资源带 `?v=` 版本后缀且由 `ui_contract_test.go` 与 `plugin.json` 的 `version` 强绑定（`TestUIAssetVersionsMatchPluginVersion`）。按仓库约定，改动前端资源需同步提升 `plugin.json` 的 `version` 与 `ui/index.html` 的两处 `?v=`。

---

## 四、验收标准（可复验）

以下断言均可在真实界面度量，不依赖主观判断：

1. **聚焦环闭合**：键盘 Tab 到 `.view-switch` / `.seg` 内全部 20 个控件，四周环体完整可见；激活项环与自身底色对比 ≥ 3:1。
2. **对比度**：`.wb-mode-head .seg-btn span`、`.pi-meta`、`.stage-pill.stage-unknown` 实测 ≥ 4.5:1（11px 正文级）。
3. **命中区**：`.link-btn`、`.entry-toggle`、`.mx-copy`、`.agg-jump`、`.log-root-path`、`.cal-nav` 在 320/768/1024/1440 下均 ≥ 24×24。
4. **键盘等价**：工时表首个有数据单元格可 Tab 进入，Enter 与 Space 均跳转日期。
5. **标题层级**：6 个视图可见标题序列无跳级。
6. **tablist**：Tab 只在选中页签停靠；←/→/Home/End 移动并切换。
7. **重排**：320px 宽下 `scrollWidth === clientWidth`，无横向滚动。
8. **无回归**：控制台无新增错误；工时表行复制（表头/行/合计/整表）粘到 Excel 仍为 4 行 33 列结构；模态 Esc 关闭且焦点回到触发元素（改前已通过，需保持）。

---

## 五、已验证通过的项（不要误改）

这些是审查中确认成立、不需要改动的部分：

- **模态焦点管理**：打开 `记录一条` 后焦点进入模态内（`activeTag: input`），Esc 关闭，焦点正确回到触发按钮 `#record`。符合 ARIA APG Dialog。
- **减少动效**：存在 `@media (prefers-reduced-motion: reduce)` 规则，且滚动/骨架动效有对应降级。
- **触屏常显**：`@media (hover: none)` 下 `.mx-copy` 的 `opacity` 为 1，复制入口在触屏上不会永远不可见。
- **工时表表格语义**：`th[scope]` 全覆盖（`rowHeaders: 3`，`colHeaders: 32`，无缺 `scope` 的 th）。
- **复制到 Excel**：剪贴板同时写入 `text/plain`（TSV）与 `text/html`（`<table>`），与 [ui_contract_test.go](plugins/work-log/ui/ui_contract_test.go) 的断言一致。
- **长路径容纳**：日志目录路径、项目名、条目标题均为「单行 + 省略号 + title 提示」，属有意截断，不构成溢出缺陷。
- **200% 文本放大**：1024 宽下放大 200% 后页面无横向滚动。

---

## 六、排除的误报（复验后不成立）

审查过程中有 3 条结论被复验推翻，记录在此以免据此改坏代码：

1. **「聚焦时复制按钮 opacity 仍为 0，键盘用户看不见」——不成立。**
   读到的 `opacity: 0` 是 `transition: opacity 0.12s` 尚未走完的瞬时值。分时采样：`t=0` 为 `0`，`t=80ms` 为 `0.85`，`t=480ms` 为 `1`。规则 `.matrix .mx-copy:focus-visible { opacity: 1 }` 正常生效。

2. **「键盘 Enter 触发复制但剪贴板为空」——不成立。**
   我最初的夹具没有授予 `clipboard-read` 权限，`readText()` 返回空是读取侧被拒。补权限并记录剪贴板 API 调用后：Enter 触发 `navigator.clipboard.write` → `result: ok`，读回 116 字符（`项目\t1日\t2日…`），鼠标点击结果一致。

3. **「`.matrix tr:hover .mx-copy` 规则被 `@media (hover:none)` 覆盖导致永不显示」——不成立。**
   两条规则作用于互斥的媒体条件，桌面与触屏各自成立，实测桌面 hover 后 `opacity` 为 1。

> 这三条的共同教训：**焦点可见性、过渡动画、剪贴板权限都不能只靠一次 `getComputedStyle` / 单次读取下结论**，必须在真实键盘/鼠标事件链上分时采样。

---

## 七、来源与假设

- **规范依据**：WCAG 2.2 AA（1.4.3、1.4.10、1.4.11、2.1.1、2.4.7、2.4.11、2.4.13、2.5.8）、ARIA Authoring Practices（Tabs、Dialog 模式）。
- **项目自有约束**：仓库无 `design-system/`、`DESIGN.md` 或令牌 JSON（`ui-ux.project.inspect` 返回 `has_design_system: false`）。因此以 `app.css` 顶部的 `:root` 令牌（`--accent`、`--meta`、`--radius-*` 等）作为事实上的设计系统，并将其「同一层级只用一档」的既有约定作为第一约束。
- **令牌校验**：`ui-ux.token.validate` 对 `ui/app.css` 报出大量 `hardcoded_colors` 警告（`#475569`、`#f1f5f9` 等）与 **空 `contrast_checks`**——该工具未能自动算出组合后的对比度，本报告的对比度数值全部来自浏览器内自建计算（含 alpha 混合与祖先背景回溯），未采信工具的告警计数。
- **平台假设**：桌面 WebView，宽度通常 ≥360px。P7 的 320px 结论按基线记录但降为低优先级。
- **未验证项**：
  - 未在 HiMind Agent 真实宿主窗内验证（本报告在独立 Chromium 中以桥接桩渲染同一份 `ui/` 资源，桩覆盖了 12 个只读能力）。
  - 未覆盖深色主题（该插件没有深色主题，`app.css` 无 `prefers-color-scheme` 分支）。
  - 未做真实 Excel 粘贴的二次确认（粘贴行为已在 0.16.0 的既有验证中确认，本次仅复核剪贴板内容与格式）。

---

## 八、修复落地与复验（0.17.0）

### 8.1 顺手改掉的复制行为

用户在同一轮中提出：工时表复制**不要项目列与合计列，只复制日期列**。这与 0.16.0 的行为不同，已改并同步了提示文案与 HTML 说明：

- 表头从 `['项目'] + 日期 + ['合计']` 改为纯 `日期`（[app.js](plugins/work-log/ui/app.js) 中 `const tableHead = dayCells((day) => day + '日');`）。
- 每行从 `[项目名] + 日期 + [合计]` 改为纯日期工时。
- 合计行从 `['当天合计'] + 日期 + [总计]` 改为纯日期合计。
- 项目名移出剪贴板，另存 `state.matrixTable.rowNames`，仅供「已复制「X」的日期工时」提示文案使用。
- 理由：项目列与合计列通常由使用方自己的工时模板提供，合计往往还是公式；随数据一起粘过去会与模板已有列错位，或把公式覆盖成死值。

实测（1200 宽，30 天月份）：表头 30 列、行 30 列、合计行 30 列；整表 5 行 × 30 列；每格均为数值或空；合计行等于各数据行同列之和；`text/html` 仍为 `<table>` 结构；空月份正确提示「本月还没有可复制的工时数据」且不写剪贴板。

### 8.2 逐项复验结果

| 项 | 改动 | 复验结果 |
| --- | --- | --- |
| P1 聚焦环 | `.view-switch .switch:focus-visible`、`.seg .seg-btn:focus-visible` 改为 `box-shadow: inset 0 0 0 2px`；激活项用白色内环 | 采样 16 个裁切容器内控件，全部 `outline-style: none` + `inset` 内环；激活项为 `rgb(255,255,255) 0 0 0 2px inset` |
| P2 对比度 | 4 处色值调整（徽标改深色叠加、元信息与 pill 加深到 `#475569`） | 自动审计「对比度不达标样本数：0」 |
| P3 命中区 | 6 组 `min-height`/`min-width`/`flex: 0 0 auto` | 6 个视图 × 1024 宽，「小于 24×24 的控件：0」 |
| P4 单元格键盘 | 补 `tabindex="0" role="button" aria-label` 与 `keydown` | Tab 可进入；Enter 与 Space 均跳转到对应日期（标题 `2026-11-01 · 工作日志`）；聚焦时有 inset 内环 |
| P5 标题跳级 | 5 处分节标题 `h3` → `h2`，同步 `.report-section > h2` | 报告视图标题序列 `h1 → h2 ×5`，无跳级 |
| P6 tablist | roving `tabindex` + `←/→/Home/End` | 仅 1 个 Tab 停靠点；方向键移动并同步切换视图与 `aria-selected`；`←` 从首个回绕到末个 |
| P7 窄窗 | `.plan-date-nav` 允许换行 | 320px 下 6 个视图均 `scrollWidth === clientWidth`，无越界控件 |
| `.agg-jump` 焦点 | 拆开 `:hover` / `:focus-visible`，恢复外环 | 报告页归档跳转按钮重新有可见焦点环 |

### 8.3 自动化守护

- `TestUIMatrixRowCopyForExcel` 新增 6 条断言，并新增 `requireNotContainsForTest` 辅助函数锁定「不得再拼项目列/合计列」。已验证该测试能捕获回退（临时还原旧代码后测试失败，恢复后通过）。
- 新增 `TestUIKeyboardAccessibilityContract`，锁定 inset 焦点环、页签 roving tabindex 与方向键、单元格可聚焦与键盘触发、报告分节 `h2`。
- 版本提升到 **0.17.0**，`ui/index.html` 两处 `?v=` 同步；`TestUIAssetVersionsMatchPluginVersion` 通过。
- 全仓库 `go test ./...` 通过；`himind-repo-check` 校验 38 个扩展全部 `valid`。
- 包 `dist/com.himind.work-log-0.17.0.hmpkg`，sha256 `abada62a9181c6a531481607e35f18f1a667079f8ad58268e7caa8ecadd4d831`；解包复核确认包含「只复制日期列」、单元格 `tabindex`、页签 roving、报告 `h2`、`?v=0.17.0` ×2。

### 8.4 仍然存在的已知项

- 搜索框 `input#search` 自身没有焦点环，指示由父容器 `.search-box:focus-within` 提供（边框 `#2563eb`，对页面底 4.86:1）。这是**有意设计且合规**，审计脚本按「元素自身 outline」判定会把它计入，属工具口径噪声，未改。
- 审计中残留的 `404` 是测试夹具服务器不提供 `favicon.ico`，与插件无关。
- 未在真实宿主窗、深色主题、真实 Excel 粘贴上复验（同第七节「未验证项」）。
