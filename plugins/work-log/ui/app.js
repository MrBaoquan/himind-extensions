/**
 * 工作日志自带视图。
 *
 * 导航分四块，按「看什么」组织而不是按数据表：
 *   日历回看：按天看当天脉络（项目 · 功能 · 工时）与明细。
 *   报告：按周期（日/周/月/季度/年）回看，核心仍是项目 → 功能 → 工时。
 *   工时表：按月生成「项目 × 日期」矩阵，可切换到 8 小时对齐口径。
 *   项目：按项目回看推进记录，并管理项目登记表与路径映射。
 *
 * 视图只消费本插件 Manifest 已声明的能力：月历统计、条目录入/读取/删除、区间查询、
 * 报告渲染/读取、项目登记、打开链接/目录。它不直接读文件系统，也不依赖宿主页面。
 */
(function () {
  'use strict';

  const PLUGIN_ID = 'com.himind.work-log';
  // 受控工作类型统一存中文枚举；保留旧英文键到中文的兜底映射，让历史条目也能正确显示。
  const LEGACY_CATEGORY_LABELS = {
    development: '开发',
    meeting: '会议',
    review: '评审',
    docs: '文档',
    research: '调研',
    ops: '运维',
    other: '其他',
  };
  const CATEGORY_OPTIONS = ['开发', '修复', '设计', '调研', '文档', '评审', '会议', '运维', '其他'];
  const WORK_STATE_LABELS = {
    planned: '待开始',
    in_progress: '进行中',
    blocked: '受阻',
    review: '待验收',
    done: '已完成',
    paused: '已暂停',
    dormant: '已搁置',
  };
  const WORK_STATE_ORDER = ['in_progress', 'blocked', 'review', 'planned', 'paused', 'dormant', 'done'];
  const PRIORITY_LABELS = { P0: 'P0 · 最高', P1: 'P1 · 高', P2: 'P2 · 中', P3: 'P3 · 低' };
  const WORK_ITEM_STATUS_LABELS = {
    todo: '待办',
    in_progress: '进行中',
    blocked: '受阻',
    done: '已完成',
    cancelled: '取消',
  };
  const PLAN_BLOCK_KIND_LABELS = {
    focus: '专注',
    meeting: '会议',
    admin: '行政',
    break: '休息',
    buffer: '缓冲',
    personal: '个人事务',
  };
  const CONFIDENCE_LABELS = { high: '高', medium: '中', low: '低' };

  function confidenceLabel(value) {
    return CONFIDENCE_LABELS[text(value)] || '';
  }
  const PLAN_BLOCK_STATUS_LABELS = {
    planned: '计划中',
    active: '进行中',
    done: '已完成',
    skipped: '已跳过',
    moved: '已顺延',
  };

  function workStateLabel(value) {
    return WORK_STATE_LABELS[text(value)] || text(value) || '未设置';
  }

  function priorityLabel(value) {
    return PRIORITY_LABELS[text(value)] || text(value) || 'P2';
  }

  function catLabel(value) {
    const raw = text(value);
    if (!raw) return '其他';
    return LEGACY_CATEGORY_LABELS[raw] || raw;
  }

  const PERIOD_ORDER = ['daily', 'weekly', 'monthly', 'quarterly', 'yearly'];
  const PERIOD_LABELS = {
    daily: '日报',
    weekly: '周报',
    monthly: '月报',
    quarterly: '季度总结',
    yearly: '年度总结',
  };

  // 季度 / 年度的起算月（公司财年口径）：1 表示自然季度，2 表示 Q1 = 2/3/4 月。
  // 由后端 work-log.preferences.get 读回并缓存在 state 里，前端所有周期计算都以它为准，
  // 避免界面显示一个区间、落盘归档又是另一个区间。
  const DEFAULT_FISCAL_START_MONTH = 1;
  const WEEKDAYS_MONDAY = ['一', '二', '三', '四', '五', '六', '日'];
  const WEEKDAYS_SUNDAY = ['日', '一', '二', '三', '四', '五', '六'];

  // 8 小时对齐的目标工时（分钟）。
  const ALIGN_TARGET_MINUTES = 8 * 60;
  const ALIGN_STEP_MINUTES = 30;
  const BOARD_REFRESH_MS = 60000;

  const VIEWS = ['workbench', 'plan', 'calendar', 'report', 'matrix', 'projects'];

  const state = {
    view: 'workbench',
    calendar: null,
    year: 0,
    month: 0,
    selected: '',
    entries: [],
    logRoot: '',
    projectFilter: '',
    projects: [],
    projectsData: null,
    editingEntry: null,
    query: '',
    searchResults: null,
    expanded: {},
    // 报告
    reportPeriod: 'monthly',
    reportAnchor: '',
    reportEntries: null,
    reportLoading: false,
    reportArchive: null,
    aggStatus: null,
    // 财年口径：季度与年度的起算月，来自 work-log.preferences.get
    fiscalStartMonth: DEFAULT_FISCAL_START_MONTH,
    // 工时表
    matrixYear: 0,
    matrixMonth: 0,
    matrixEntries: null,
    matrixLoading: false,
    matrixMode: 'actual',
    // matrixTable 是当前矩阵的纯文本副本（表头 / 各行 / 合计），供复制到 Excel。
    matrixTable: null,
    // 项目
    projectsMode: 'progress',
    activeProject: '',
    projectEntries: null,
    projectLoading: false,
    projectExhibit: '',
    projectExhibitsExpanded: false,
    projectGroupBy: 'time',
    // 登记管理（本地 UI 状态）
    registryQuery: '',
    registryFilter: 'all',
    registrySort: 'count',
    registrySelected: {},
    registryIOOpen: false,
    registryIOText: '',
    registryNewOpen: false,
    registryEditingName: '',
    registryPage: 1,
    registryPageSize: 12,
    registryExpanded: {},
    // 当前工作台
    board: null,
    boardLoading: false,
    boardRefreshing: false,
    showDone: false,
    showOther: false,
    workbenchMode: 'recent',
    workbenchModeResolved: false,
    workEditing: null,
    workEditingSteps: [],
    // 计划
    planDate: '',
    plan: null,
    planReview: null,
    planLoading: false,
    workItems: [],
    workItemCache: [],
    workItemEditing: null,
    planEditingBlock: null,
  };

  const el = (id) => document.getElementById(id);

  const MINUTES_SOURCE_LABELS = {
    measured: '实测',
    span: '区间',
    estimated: '估算',
    manual: '人工',
  };

  // SOURCE_LABELS 把条目来源（AI 客户端 ID）翻译成显示名，与 Go 侧 sourceClientLabel 对齐。
  const SOURCE_LABELS = {
    codex: 'Codex',
    claude: 'Claude Code',
    'claude-code': 'Claude Code',
    'claude-desktop': 'Claude Desktop',
    opencode: 'OpenCode',
    copilot: 'GitHub Copilot',
    'github-copilot': 'GitHub Copilot',
    'vscode-copilot': 'VS Code GitHub Copilot',
    vscode: 'VS Code GitHub Copilot',
    cursor: 'Cursor',
    cline: 'Cline',
    windsurf: 'Windsurf',
    manual: '手工',
    workbench: '看板',
  };

  function sourceLabel(source) {
    const id = text(source);
    if (!id) return '手工';
    return SOURCE_LABELS[id] || id;
  }

  // ---------- 通用工具 ----------

  function text(value, fallback) {
    const trimmed = value === 0 || value ? String(value) : '';
    return trimmed.trim() || fallback || '';
  }

  function escapeHTML(value) {
    return text(value).replace(/[&<>"']/g, (char) => {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char];
    });
  }

  function pad2(value) {
    return String(value).padStart(2, '0');
  }

  function today() {
    const now = new Date();
    return now.getFullYear() + '-' + pad2(now.getMonth() + 1) + '-' + pad2(now.getDate());
  }

  function formatDate(date) {
    return date.getFullYear() + '-' + pad2(date.getMonth() + 1) + '-' + pad2(date.getDate());
  }

  function parseDate(value) {
    const parts = text(value).split('-').map(Number);
    if (parts.length !== 3 || parts.some((n) => isNaN(n))) return null;
    return new Date(parts[0], parts[1] - 1, parts[2]);
  }

  function monthKeyOf(date) {
    return text(date).slice(0, 7);
  }

  // stampToLocal 把时间戳换算成「本机时区」的 Date；不是时间戳时返回 null。
  //
  // 采集器按 RFC3339 记录真实时刻，但不同客户端给出的时区并不一致：
  // Codex / Claude Desktop / GitHub Copilot 的会话里是 UTC（后缀 Z），
  // OpenCode / VS Code 是本地偏移（+08:00），手工录入还可能只有 HH:MM。
  // 因此展示前必须按时间戳自带的偏移换算成本机时间，绝不能直接截取字符串里的时分，
  // 否则 UTC 记录会整体偏掉一个时区（本机 +08:00 时就是少显示 8 小时）。
  function stampToLocal(value) {
    const raw = text(value);
    if (!raw) return null;
    // 只有时分（手工录入）时没有时区可换算，交给调用方原样展示。
    if (/^\d{1,2}:\d{2}$/.test(raw)) return null;
    // 纯日期按本机零点处理，避免被 Date 当成 UTC 零点而跨到前一天。
    const dateOnly = raw.match(/^(\d{4})-(\d{2})-(\d{2})$/);
    if (dateOnly) return new Date(Number(dateOnly[1]), Number(dateOnly[2]) - 1, Number(dateOnly[3]));
    // 不带偏移的日期时间按本机时间理解（服务端/手工写入的本地写法）。
    const naive = raw.match(/^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})(?::(\d{2}))?(?:\.(\d+))?$/);
    if (naive) {
      return new Date(
        Number(naive[1]),
        Number(naive[2]) - 1,
        Number(naive[3]),
        Number(naive[4]),
        Number(naive[5]),
        Number(naive[6] || 0),
        Number('0.' + (naive[7] || '0')) * 1000,
      );
    }
    const parsed = new Date(raw);
    return isNaN(parsed.getTime()) ? null : parsed;
  }

  // formatTime 把时间戳渲染成本机时区的 HH:MM。
  function formatTime(value) {
    const raw = text(value);
    if (!raw) return '';
    const local = stampToLocal(raw);
    if (local) return pad2(local.getHours()) + ':' + pad2(local.getMinutes());
    const match = raw.match(/^(\d{1,2}):(\d{2})/);
    if (match) return pad2(match[1]) + ':' + match[2];
    return raw;
  }

  // dayOfStamp 取时间戳在「本机时区」下的日期部分（用于判断会话是否跨天）。
  function dayOfStamp(value) {
    const local = stampToLocal(value);
    if (local) return formatDate(local);
    const match = text(value).match(/^(\d{4}-\d{2}-\d{2})/);
    return match ? match[1] : '';
  }

  // crossDayLabel 返回跨天标记：跨 1 天为「（次日）」，跨多天为「（跨 N 日）」；同日返回空。
  function crossDayLabel(startRaw, endRaw) {
    const startDay = dayOfStamp(startRaw);
    const endDay = dayOfStamp(endRaw);
    if (startDay && endDay) {
      const start = parseDate(startDay);
      const end = parseDate(endDay);
      if (start && end) {
        const days = Math.round((end - start) / 86400000);
        if (days === 1) return '（次日）';
        if (days > 1) return '（跨 ' + days + ' 日）';
        return '';
      }
    }
    const start = formatTime(startRaw);
    const end = formatTime(endRaw);
    if (start && end && /^\d{2}:\d{2}$/.test(start) && /^\d{2}:\d{2}$/.test(end) && end < start) return '（次日）';
    return '';
  }

  function formatSpan(item) {
    const start = formatTime(item.started_at);
    const end = formatTime(item.ended_at);
    if (!start && !end) return '';
    if (start && end) {
      if (start === end) return start;
      return start + '–' + end + crossDayLabel(item.started_at, item.ended_at);
    }
    return start || end;
  }

  function minutesLabel(minutes) {
    const value = Number(minutes) || 0;
    if (value <= 0) return '';
    if (value < 60) return value + ' 分钟';
    const hours = Math.floor(value / 60);
    const rest = value % 60;
    return rest ? hours + ' 小时 ' + rest + ' 分' : hours + ' 小时';
  }

  function hoursLabel(minutes) {
    const value = Number(minutes) || 0;
    if (value <= 0) return '';
    return (value / 60).toFixed(1) + ' 小时';
  }

  // hoursShort 把分钟渲染成紧凑小时数：整数不带小数点（3），否则一位小数（3.5）。
  function hoursShort(minutes) {
    const value = Number(minutes) || 0;
    if (value <= 0) return '';
    const hours = value / 60;
    return Number.isInteger(hours) ? String(hours) : hours.toFixed(1);
  }

  function toFileUri(path) {
    const normalized = text(path).replace(/\\/g, '/');
    if (!normalized) return '';
    if (/^[a-zA-Z]:/.test(normalized)) return 'file:///' + normalized;
    return 'file://' + normalized;
  }

  function copyText(value, message) {
    if (!value) return;
    const done = () => notify(message);
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(value).then(done, done);
      return;
    }
    const area = document.createElement('textarea');
    area.value = value;
    document.body.appendChild(area);
    area.select();
    try {
      document.execCommand('copy');
    } catch (error) {
      /* 剪贴板不可用时静默忽略。 */
    }
    document.body.removeChild(area);
    done();
  }

  // copyTableToClipboard 把二维文本数组写进剪贴板，保证粘到 Excel 后还是表格。
  //
  // 同时写两种格式：text/plain 用制表符分隔（Excel 据此拆列），text/html 用 <table>
  // 保留单元格边界。只写 text/plain 在部分 Excel 版本里会把整段并进一格，所以优先
  // 用 ClipboardItem 写多格式；浏览器不支持或被拒绝时退回纯文本（仍能拆列）。
  function copyTableToClipboard(rows, message) {
    if (!rows || !rows.length) return;
    const tsv = rows.map((cells) => cells.join('\t')).join('\n');
    const htmlTable =
      '<table>' +
      rows
        .map(
          (cells) =>
            '<tr>' + cells.map((value) => '<td>' + escapeHTML(value) + '</td>').join('') + '</tr>',
        )
        .join('') +
      '</table>';
    const done = () => notify(message || '已复制，可直接粘贴到 Excel');
    if (navigator.clipboard && navigator.clipboard.write && window.ClipboardItem) {
      try {
        const item = new ClipboardItem({
          'text/plain': new Blob([tsv], { type: 'text/plain' }),
          'text/html': new Blob([htmlTable], { type: 'text/html' }),
        });
        navigator.clipboard.write([item]).then(done, () => copyText(tsv, message));
        return;
      } catch (error) {
        /* 结构化剪贴板不可用时走纯文本兜底。 */
      }
    }
    copyText(tsv, message);
  }

  function emptyBlock(title, lines) {
    return (
      '<div class="empty"><strong>' +
      escapeHTML(title) +
      '</strong><ul>' +
      (lines || []).map((line) => '<li>' + escapeHTML(line) + '</li>').join('') +
      '</ul></div>'
    );
  }

  function skeleton() {
    return '<div class="skeleton"><div></div><div></div><div></div></div>';
  }

  function bridgeInvoke(capabilityId, input) {
    const internals = window.__TAURI_INTERNALS__ || {};
    if (typeof internals.invoke !== 'function') {
      const error = new Error('当前不在 HiMind Agent 的扩展视图窗口中');
      error.code = 'bridge_unavailable';
      return Promise.reject(error);
    }
    return internals.invoke('invoke_plugin_view_capability', {
      capabilityId: capabilityId,
      input: input || {},
    });
  }

  function openLink(url) {
    return bridgeInvoke('work-log.link.open', { url: url }).then(
      () => true,
      () => false,
    );
  }

  let noticeTimer = null;

  function notify(message, tone) {
    const node = el('notice');
    if (noticeTimer) {
      clearTimeout(noticeTimer);
      noticeTimer = null;
    }
    if (!message) {
      node.hidden = true;
      node.textContent = '';
      node.className = 'notice';
      return;
    }
    node.hidden = false;
    node.className = 'notice' + (tone ? ' ' + tone : '');
    node.textContent = message;
    const ttl = tone === 'error' ? 9000 : tone === 'warn' ? 7000 : 4500;
    noticeTimer = setTimeout(() => {
      node.hidden = true;
      node.textContent = '';
      node.className = 'notice';
    }, ttl);
  }

  function splitList(value) {
    return text(value)
      .split(/[,，、;；]+/)
      .map((item) => item.trim())
      .filter(Boolean);
  }

  // ---------- 聚合：项目 → 功能 → 工时 ----------

  // aggregateFlow 是「核心脉络」的唯一数据源：把条目折叠成 项目 → 功能 → 工时。
  function aggregateFlow(entries) {
    const byProject = {};
    const order = [];
    const categories = {};
    let totalMinutes = 0;
    (entries || []).forEach((item) => {
      const minutes = Number(item.minutes) || 0;
      totalMinutes += minutes;
      const category = catLabel(item.category);
      categories[category] = (categories[category] || 0) + minutes;

      const projectName = text(item.project, '未落项目');
      if (!byProject[projectName]) {
        byProject[projectName] = {
          name: projectName,
          minutes: 0,
          count: 0,
          features: {},
          featureOrder: [],
          categories: {},
          entries: [],
        };
        order.push(projectName);
      }
      const group = byProject[projectName];
      group.minutes += minutes;
      group.count += 1;
      group.entries.push(item);
      group.categories[category] = (group.categories[category] || 0) + minutes;

      const featureName = text(item.feature, '未分类功能');
      if (!group.features[featureName]) {
        group.features[featureName] = { name: featureName, minutes: 0, count: 0, entries: [] };
        group.featureOrder.push(featureName);
      }
      group.features[featureName].minutes += minutes;
      group.features[featureName].count += 1;
      group.features[featureName].entries.push(item);
    });

    const projects = order.map((name) => byProject[name]);
    projects.forEach((project) => {
      project.featureList = project.featureOrder
        .map((name) => project.features[name])
        .sort((a, b) => b.minutes - a.minutes || (a.name < b.name ? -1 : 1));
    });
    projects.sort((a, b) => b.minutes - a.minutes || b.count - a.count || (a.name < b.name ? -1 : 1));

    const categoryList = Object.keys(categories)
      .map((name) => ({ name: name, minutes: categories[name] }))
      .sort((a, b) => b.minutes - a.minutes || (a.name < b.name ? -1 : 1));

    return { projects: projects, categories: categoryList, totalMinutes: totalMinutes, count: (entries || []).length };
  }

  // groupByExhibit 把一组条目按展项分组（未归类展项置末），用于「按展项」视图与当天明细。
  function groupByExhibit(entries) {
    const map = {};
    const order = [];
    (entries || []).forEach((item) => {
      const name = text(item.exhibit, '未归类展项');
      if (!map[name]) {
        map[name] = { name: name, count: 0, minutes: 0, entries: [] };
        order.push(name);
      }
      const g = map[name];
      g.minutes += Number(item.minutes) || 0;
      g.count += 1;
      g.entries.push(item);
    });
    const named = order
      .filter((name) => name !== '未归类展项')
      .sort((a, b) => map[b].minutes - map[a].minutes || (a < b ? -1 : 1));
    const final = map['未归类展项'] ? named.concat(['未归类展项']) : named;
    return final.map((name) => map[name]);
  }

  function categoryChips(categories) {
    if (!categories || !categories.length) return '';
    return (
      '<div class="flow-cats">' +
      categories
        .filter((item) => item.minutes > 0)
        .map((item) => '<span class="cat-chip">' + escapeHTML(item.name) + ' <b>' + escapeHTML(hoursShort(item.minutes)) + 'h</b></span>')
        .join('') +
      '</div>'
    );
  }

  // renderDayFlow 渲染当天「核心脉络」：项目 · 功能 · 工时，一行一组。
  function renderDayFlow(entries) {
    const node = el('day-flow');
    if (!node) return;
    const filtered = filterEntries(entries, state.query);
    if (!filtered.length) {
      node.innerHTML = '';
      return;
    }
    const flow = aggregateFlow(filtered);
    const items = flow.projects
      .map((project) => {
        // 有展项时优先展示展项；否则退回功能。
        const byExhibit = project.entries.some((item) => text(item.exhibit));
        const features = byExhibit
          ? groupByExhibit(project.entries)
              .map((g) => {
                const label = g.name === '未归类展项' ? '未归类展项' : g.name;
                return (
                  '<span class="flow-feat exhibit">' +
                  escapeHTML(label) +
                  (g.minutes > 0 ? '<b>' + escapeHTML(hoursShort(g.minutes)) + 'h</b>' : '') +
                  '</span>'
                );
              })
              .join('')
          : project.featureList
              .filter((feature) => feature.name !== '未分类功能' || project.featureList.length === 1)
              .map((feature) => {
                const label = feature.name === '未分类功能' ? '未归类' : feature.name;
                return (
                  '<span class="flow-feat">' +
                  escapeHTML(label) +
                  (feature.minutes > 0 ? '<b>' + escapeHTML(hoursShort(feature.minutes)) + 'h</b>' : '') +
                  '</span>'
                );
              })
              .join('');
        return (
          '<div class="flow-item' +
          (project.name === '未落项目' ? ' miss' : '') +
          '">' +
          '<div class="flow-line"><span class="flow-proj">' +
          escapeHTML(project.name) +
          '</span><span class="flow-hours">' +
          (project.minutes > 0 ? escapeHTML(hoursShort(project.minutes)) + 'h' : project.count + ' 条') +
          '</span></div>' +
          '<div class="flow-feats">' +
          (features || '<span class="flow-feat muted">未记录功能</span>') +
          '</div></div>'
        );
      })
      .join('');
    node.innerHTML =
      '<div class="flow-head"><span class="flow-title">当天脉络</span><span class="flow-total">' +
      escapeHTML(flow.count) +
      ' 条' +
      (flow.totalMinutes > 0 ? ' · 约 ' + escapeHTML(hoursShort(flow.totalMinutes)) + ' 小时' : '') +
      '</span></div><div class="flow-grid">' +
      items +
      '</div>' +
      categoryChips(flow.categories);
  }

  // ---------- 日历回看 ----------

  function renderCalendar() {
    const calendar = state.calendar;
    const title = el('cal-title');
    const grid = el('cal-grid');
    const stats = el('cal-stats');
    if (!calendar) {
      title.textContent = '—';
      grid.innerHTML = '';
      el('cal-weekdays').innerHTML = '';
      stats.innerHTML = '';
      return;
    }
    title.textContent = calendar.year + ' 年 ' + calendar.month + ' 月';
    const labels = calendar.week_start === 'sunday' ? WEEKDAYS_SUNDAY : WEEKDAYS_MONDAY;
    el('cal-weekdays').innerHTML = labels.map((label) => '<span>' + label + '</span>').join('');
    grid.innerHTML = (calendar.weeks || [])
      .map((week) =>
        week
          .map((cell) => {
            const classes = ['cal-cell'];
            if (!cell.in_month) classes.push('out');
            if (cell.is_today) classes.push('today');
            if (cell.date === state.selected) classes.push('active');
            if (cell.entry_count > 0) classes.push('has');
            const projects = (cell.projects || []).join('、');
            const hours = hoursLabel(cell.total_minutes);
            const dowIndex = new Date(cell.date + 'T00:00:00').getDay();
            const dow =
              calendar.week_start === 'sunday'
                ? WEEKDAYS_SUNDAY[dowIndex]
                : WEEKDAYS_MONDAY[(dowIndex + 6) % 7];
            const ariaLabel =
              text(cell.date) +
              (dow ? ' ' + dow : '') +
              (cell.entry_count > 0 ? '，' + cell.entry_count + ' 条' : '，无记录') +
              (projects ? '，' + projects : '');
            const titleAttr =
              text(cell.date) + (projects ? ' · ' + projects : '') + (hours ? ' · 约 ' + hours : '');
            const marker =
              cell.entry_count > 0 ? '<span class="cal-count">' + escapeHTML(cell.entry_count) + '</span>' : '';
            return (
              '<button type="button" class="' +
              classes.join(' ') +
              '" data-date="' +
              escapeHTML(cell.date) +
              '" aria-label="' +
              escapeHTML(ariaLabel) +
              '" title="' +
              escapeHTML(titleAttr) +
              '"><span class="cal-day">' +
              escapeHTML(cell.day) +
              '</span>' +
              marker +
              '</button>'
            );
          })
          .join(''),
      )
      .join('');
    const projects = [];
    (calendar.days || []).forEach((day) => {
      (day.projects || []).forEach((project) => {
        if (projects.indexOf(project) < 0) projects.push(project);
      });
    });
    const pills = [
      '<span class="pill">' + escapeHTML(calendar.active_days || 0) + ' 天有记录</span>',
      '<span class="pill">' + escapeHTML(calendar.total_entries || 0) + ' 条日志</span>',
      calendar.total_minutes
        ? '<span class="pill">约 ' + escapeHTML(hoursLabel(calendar.total_minutes)) + '</span>'
        : '',
    ].filter(Boolean);
    if (projects.length) {
      pills.push(
        '<span class="pill">' + escapeHTML(projects.slice(0, 3).join('、')) + (projects.length > 3 ? ' 等' : '') + '</span>',
      );
    }
    stats.innerHTML = pills.join('');
  }

  function updateHead() {
    if (!state.selected) {
      el('detail-title').textContent = '选择一个日期';
      el('detail-subtitle').textContent = '月历按天统计；点一天查看当天脉络。';
      el('detail-badges').innerHTML = '';
      return;
    }
    el('detail-title').textContent = state.selected + ' · 工作日志';
    const flow = aggregateFlow(state.entries);
    const projectNames = flow.projects.filter((p) => p.name !== '未落项目').map((p) => p.name);
    el('detail-subtitle').textContent = state.entries.length
      ? '共 ' + state.entries.length + ' 条' + (projectNames.length ? '，涉及 ' + projectNames.join('、') : '')
      : '这一天还没有记录。';
    const badges = ['<span class="badge">' + escapeHTML(state.entries.length) + ' 条</span>'];
    if (projectNames.length) badges.push('<span class="badge new">' + escapeHTML(projectNames.length) + ' 个项目</span>');
    if (flow.totalMinutes > 0) badges.push('<span class="badge">约 ' + escapeHTML(hoursLabel(flow.totalMinutes)) + '</span>');
    el('detail-badges').innerHTML = badges.join('');
  }

  // entryCard 渲染一条日志：以「项目 · 功能 · 工时」为首要信息，标题随后。
  // opts.hideProject 在按项目分组的上下文里省略项目标签。
  function entryCard(item, opts) {
    const entryID = text(item.entry_id);
    const expanded = !!state.expanded[entryID];
    const hideProject = !!(opts && opts.hideProject);

    const head = [
      '<div class="entry ' + (expanded ? 'expanded' : 'compact') + '" data-entry-id="' + escapeHTML(entryID) + '">',
      '<div class="entry-head">',
    ];
    const entryDate = text(item.date, state.selected);
    if (!hideProject) {
      if (text(item.project)) {
        head.push(
          '<button type="button" class="proj edit-field" data-action="edit-field" data-field="project" data-entry-id="' +
            escapeHTML(entryID) +
            '" data-date="' +
            escapeHTML(entryDate) +
            '" data-value="' +
            escapeHTML(item.project) +
            '" title="点击修改项目">' +
            escapeHTML(item.project) +
            '</button>',
        );
      } else if (entryID) {
        head.push(
          '<button type="button" class="proj miss edit-field" data-action="edit-field" data-field="project" data-entry-id="' +
            escapeHTML(entryID) +
            '" data-date="' +
            escapeHTML(entryDate) +
            '" data-value="" title="还没落到项目，点击补全">未落项目</button>',
        );
      }
    }
    if (text(item.exhibit)) {
      head.push('<span class="exhibit" title="展项（项目下的互动装置）">' + escapeHTML(item.exhibit) + '</span>');
    }
    if (entryID) {
      head.push(
        '<button type="button" class="feat edit-field' +
          (text(item.feature) ? '' : ' empty') +
          '" data-action="edit-field" data-field="feature" data-entry-id="' +
          escapeHTML(entryID) +
          '" data-date="' +
          escapeHTML(entryDate) +
          '" data-value="' +
          escapeHTML(text(item.feature)) +
          '" title="功能/工作内容（条目细节，点击修改）">' +
          escapeHTML(text(item.feature, '+ 功能')) +
          '</button>',
      );
    } else if (text(item.feature)) {
      head.push('<span class="feat">' + escapeHTML(item.feature) + '</span>');
    }
    if (Number(item.minutes) > 0) {
      head.push('<span class="mins">' + escapeHTML(hoursShort(item.minutes)) + 'h</span>');
    }
    head.push('<span class="cat">' + escapeHTML(catLabel(item.category)) + '</span>');
    if (text(item.source) && text(item.source) !== 'manual') {
      head.push('<span class="src" title="记录来源的 AI 客户端">' + escapeHTML(sourceLabel(item.source)) + '</span>');
    }
    const span = formatSpan(item);
    if (span) head.push('<span class="time">' + escapeHTML(span) + '</span>');
    if (entryID) {
      head.push(
        '<span class="entry-actions">' +
          '<button type="button" class="link-btn" data-action="edit" data-entry-id="' +
          escapeHTML(entryID) +
          '" data-date="' +
          escapeHTML(entryDate) +
          '">编辑</button>' +
          '<button type="button" class="link-btn lock' +
          (item.locked ? ' on' : '') +
          '" data-action="lock" data-entry-id="' +
          escapeHTML(entryID) +
          '" data-date="' +
          escapeHTML(entryDate) +
          '" data-locked="' +
          (item.locked ? '1' : '0') +
          '" title="' +
          (item.locked ? '已锁定：自动重扫与合并都会跳过此条，点击解锁' : '锁定此条：自动重扫与合并不会改写它') +
          '">' +
          (item.locked ? '已锁定' : '锁定') +
          '</button>' +
          '<button type="button" class="link-btn danger" data-action="delete" data-entry-id="' +
          escapeHTML(entryID) +
          '" data-date="' +
          escapeHTML(entryDate) +
          '">删除</button>' +
          '</span>',
      );
    }
    head.push(
      '<button type="button" class="entry-toggle" data-action="toggle" data-entry-id="' +
        escapeHTML(entryID) +
        '">' +
        (expanded ? '收起' : '展开') +
        '</button>',
    );
    head.push('</div>');
    head.push('<strong class="entry-title">' + escapeHTML(text(item.title, '(未命名)')) + '</strong>');

    const preview = text(item.summary) || firstBullet(item);
    if (preview) head.push('<p class="summary" title="' + escapeHTML(preview) + '">' + escapeHTML(preview) + '</p>');

    const detail = [];
    detail.push(listBlock('交付', item.outcomes));
    detail.push(listBlock('决策', item.decisions));
    detail.push(listBlock('经验', item.learnings));
    detail.push(listBlock('阻塞', item.blockers, true));
    detail.push(listBlock('后续', item.next));
    const refs = (item.refs || []).filter((ref) => text(ref && ref.value));
    if (refs.length) {
      detail.push(
        '<div class="refs">' +
          refs
            .map((ref) => {
              const value = text(ref.value);
              return (
                '<button type="button" class="ref" data-url="' +
                escapeHTML(value) +
                '">' +
                escapeHTML(text(ref.type, '链接')) +
                ' ↗</button>'
              );
            })
            .join('') +
          '</div>',
      );
    }
    if (Number(item.minutes) > 0) {
      const source = MINUTES_SOURCE_LABELS[item.minutes_source] || text(item.minutes_source, '工时');
      detail.push('<p class="entry-meta">工时 ' + escapeHTML(minutesLabel(item.minutes)) + ' · 来源 ' + escapeHTML(source) + '</p>');
    }
    if (text(item.source)) {
      detail.push('<p class="entry-meta">记录来源：' + escapeHTML(sourceLabel(item.source)) + (text(item.session_id) ? ' · 会话 ' + escapeHTML(text(item.session_id)) : '') + '</p>');
    }
    head.push('<div class="entry-detail">' + detail.join('') + '</div>');
    head.push('</div>');
    return head.join('');
  }

  function firstBullet(item) {
    for (const key of ['outcomes', 'decisions', 'blockers', 'next']) {
      const values = item[key] || [];
      for (const value of values) {
        if (text(value)) return text(value);
      }
    }
    return '';
  }

  function listBlock(label, values, warn) {
    const items = (values || []).map((value) => text(value)).filter(Boolean);
    if (!items.length) return '';
    return (
      '<ul' +
      (warn ? ' class="warn"' : '') +
      '>' +
      items.map((value) => '<li><b>' + escapeHTML(label) + '：</b>' + escapeHTML(value) + '</li>').join('') +
      '</ul>'
    );
  }

  function filterEntries(entries, query) {
    const needle = text(query).toLowerCase();
    if (!needle) return entries || [];
    return (entries || []).filter((item) => {
      const hay = [item.title, item.summary, item.feature, item.project, item.exhibit, catLabel(item.category), sourceLabel(item.source)].concat(item.tags || []);
      return hay.some((value) => text(value).toLowerCase().indexOf(needle) >= 0);
    });
  }

  function renderTimeline() {
    if (state.searchResults) {
      const results = state.searchResults.entries || [];
      if (!results.length) {
        return emptyBlock('没有匹配的日志', ['换个关键词，或清空搜索框回到日历回看。']);
      }
      return (
        '<div class="search-summary">在当月匹配到 ' +
        escapeHTML(results.length) +
        ' 条 · 关键词「' +
        escapeHTML(state.query) +
        '」</div>' +
        results.map((item) => entryCard(item, {})).join('')
      );
    }
    if (!state.entries.length) {
      return emptyBlock('这一天还没有记录', [
        '可以用右上角“记录一条”手工补记。',
        '也可以让“工作日志助手”技能把当天的 AI 会话整理成条目后写入。',
        '需要批量补记时，运行“每日工作日志”工作流。',
      ]);
    }
    const entries = filterEntries(state.entries, state.query);
    if (!entries.length) {
      return emptyBlock('这一天没有匹配的日志', [
        '关键词「' + text(state.query) + '」在本日无命中。',
        '按回车可在当月日志里全局搜索。',
        '清空搜索框可回到完整明细。',
      ]);
    }
    return renderProjectGroups(entries);
  }

  // renderProjectGroups 按「项目 → 功能」两级分组渲染明细，逐组给确定性小计。
  function renderProjectGroups(entries) {
    const flow = aggregateFlow(entries);
    let html = '';
    const unassigned = entries.filter((item) => !text(item.project));
    if (unassigned.length) {
      html +=
        '<div class="unassigned-banner" role="note">有 ' +
        escapeHTML(unassigned.length) +
        ' 条还没落到项目：点卡片上的「未落项目」即可补全。</div>';
    }
    html += flow.projects
      .map((project) => {
        // 有展项的条目按「展项」分组；否则退回「功能」（展项为主，功能为辅）。
        const byExhibit = project.entries.some((item) => text(item.exhibit));
        const groups = byExhibit
          ? groupByExhibit(project.entries).map((g) => ({
              label: g.name === '未归类展项' ? '未归类展项' : '展项 · ' + g.name,
              count: g.count,
              minutes: g.minutes,
              entries: g.entries,
              exhibit: true,
            }))
          : project.featureList.map((feature) => ({
              label: feature.name === '未分类功能' ? '未分类功能' : '功能 · ' + feature.name,
              count: feature.count,
              minutes: feature.minutes,
              entries: feature.entries,
              exhibit: false,
            }));
        const features = groups
          .map((g) => {
            return (
              '<div class="feature-group' +
              (g.exhibit ? ' exhibit-group' : '') +
              '">' +
              '<div class="feature-group-head"><span class="fg-tag">' +
              escapeHTML(g.label) +
              '</span><span class="fg-meta">' +
              escapeHTML(g.count) +
              ' 条' +
              (g.minutes > 0 ? ' · 约 ' + escapeHTML(hoursLabel(g.minutes)) : '') +
              '</span></div>' +
              g.entries.map((item) => entryCard(item, { hideProject: true })).join('') +
              '</div>'
            );
          })
          .join('');
        return (
          '<section class="project-group">' +
          '<div class="project-group-head"><span class="pg-name">' +
          escapeHTML(project.name) +
          '</span><span class="pg-meta">' +
          escapeHTML(project.count) +
          ' 条' +
          (project.minutes > 0 ? ' · 约 ' + escapeHTML(hoursLabel(project.minutes)) : '') +
          '</span></div>' +
          features +
          '</section>'
        );
      })
      .join('');
    return html;
  }

  function renderPanel() {
    const panel = el('panel');
    panel.innerHTML = renderTimeline();
    renderDayFlow(state.entries);
    panel.querySelectorAll('.ref').forEach((node) => {
      node.addEventListener('click', () => {
        const url = node.getAttribute('data-url');
        openLink(url).then((opened) => {
          if (!opened) copyText(url, '宿主未能打开链接，已复制引用地址');
        });
      });
    });
    bindEntryCards(panel);
  }

  // bindEntryCards 绑定一批条目卡片上的通用动作（编辑 / 就地改字段 / 展开 / 删除）。
  // 日历、搜索结果与项目推进三处共用，避免行为漂移。
  function bindEntryCards(scope) {
    scope.querySelectorAll('[data-action="edit"]').forEach((node) => {
      node.addEventListener('click', () => startEdit(node.getAttribute('data-entry-id'), node.getAttribute('data-date')));
    });
    scope.querySelectorAll('[data-action="edit-field"]').forEach((node) => {
      node.addEventListener('click', () => editEntryField(node));
    });
    scope.querySelectorAll('[data-action="toggle"]').forEach((node) => {
      node.addEventListener('click', () => {
        const id = node.getAttribute('data-entry-id');
        state.expanded[id] = !state.expanded[id];
        const card = node.closest('.entry');
        if (card) {
          card.classList.toggle('expanded', !!state.expanded[id]);
          card.classList.toggle('compact', !state.expanded[id]);
          node.textContent = state.expanded[id] ? '收起' : '展开';
        }
      });
    });
    scope.querySelectorAll('[data-action="delete"]').forEach((node) => {
      node.addEventListener('click', () => armDelete(node));
    });
    scope.querySelectorAll('[data-action="lock"]').forEach((node) => {
      node.addEventListener('click', () => toggleEntryLock(node));
    });
  }

  // editEntryField 就地编辑条目的某个字段（项目 / 功能）：点击标签 → 变成输入框 →
  // 回车或失焦保存（只改这一个字段，走 work-log.entry.patch，不会注入默认值）。
  function editEntryField(button) {
    const entryID = button.getAttribute('data-entry-id');
    const field = button.getAttribute('data-field');
    const date = text(button.getAttribute('data-date'), state.selected);
    const current = text(button.getAttribute('data-value'));
    const host = button.parentElement;
    if (!entryID || !date || !host || host.querySelector('.entry-inline-input')) return;

    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'entry-inline-input';
    input.value = current;
    input.placeholder = field === 'project' ? '项目名' : '功能，如：日历视图';
    input.setAttribute('aria-label', field === 'project' ? '修改项目' : '修改功能');
    host.replaceChild(input, button);
    input.focus();
    input.select();

    let settled = false;
    const restore = () => {
      if (input.parentElement) input.parentElement.replaceChild(button, input);
    };
    const commit = async () => {
      if (settled) return;
      settled = true;
      const value = text(input.value);
      if (value === current) {
        restore();
        return;
      }
      const patch = {};
      patch[field] = value;
      try {
        await bridgeInvoke('work-log.entry.patch', { date, entry_id: entryID, patch });
        notify((field === 'project' ? '项目' : '功能') + '已更新：' + (value || '（已清空）'));
        await refreshAfterEntryEdit(date);
      } catch (error) {
        notify('更新失败：' + String((error && error.message) || error), 'error');
        restore();
      }
    };
    input.addEventListener('keydown', (event) => {
      if (event.key === 'Enter') {
        event.preventDefault();
        commit();
      } else if (event.key === 'Escape') {
        event.preventDefault();
        settled = true;
        restore();
      }
    });
    input.addEventListener('blur', () => {
      commit();
    });
  }

  // refreshAfterEntryEdit 改动单条后按当前视图局部刷新，避免整页跳回。
  async function refreshAfterEntryEdit(date) {
    try {
      await loadProjects();
      if (state.view === 'projects') {
        if (state.projectsMode === 'registry') {
          renderProjectsPanel();
        } else if (state.activeProject) {
          await selectProject(state.activeProject);
        }
        return;
      }
      if (state.view === 'calendar') {
        if (state.searchResults) {
          await runSearch();
          return;
        }
        if (date) await selectDate(date);
      }
    } catch (error) {
      notify('刷新失败：' + String((error && error.message) || error), 'error');
    }
  }

  // toggleEntryLock 锁定/解锁一条日志：锁定后自动重扫与合并（upsert/import）默认跳过它，
  // 只有显式 overwrite 才会整条替换，用于保护手工订正过的条目不被定时采集覆盖。
  async function toggleEntryLock(button) {
    const entryID = text(button.getAttribute('data-entry-id'));
    const date = text(button.getAttribute('data-date'), state.selected || today());
    if (!entryID || !date) return;
    const next = button.getAttribute('data-locked') !== '1';
    try {
      await bridgeInvoke('work-log.entry.lock', { date, entry_id: entryID, locked: next });
      notify(next ? '已锁定该条，自动重扫不会覆盖。' : '已解锁该条。');
      await refreshAfterEntryEdit(date);
    } catch (error) {
      notify('锁定操作失败：' + String((error && error.message) || error), 'error');
    }
  }

  // ---------- 报告 ----------

  // isoWeekKey 计算 ISO 8601 周键（如 2026-W41），与后端报告归档的键保持一致。
  function isoWeekKey(date) {
    const target = new Date(Date.UTC(date.getFullYear(), date.getMonth(), date.getDate()));
    const dayNum = (target.getUTCDay() + 6) % 7; // 周一为 0
    target.setUTCDate(target.getUTCDate() - dayNum + 3); // 该周的周四
    const firstThursday = new Date(Date.UTC(target.getUTCFullYear(), 0, 4));
    const firstDayNum = (firstThursday.getUTCDay() + 6) % 7;
    firstThursday.setUTCDate(firstThursday.getUTCDate() - firstDayNum + 3);
    const week = 1 + Math.round((target - firstThursday) / (7 * 24 * 3600 * 1000));
    return target.getUTCFullYear() + '-W' + pad2(week);
  }

  // fiscalStartMonth 归一化起算月：越界值一律当自然季度处理，
  // 与后端 normalizeFiscalYearStartMonth 保持同一容错行为。
  function fiscalStartMonth() {
    const value = Number(state.fiscalStartMonth);
    if (!Number.isInteger(value) || value < 1 || value > 12) return DEFAULT_FISCAL_START_MONTH;
    return value;
  }

  // fiscalYearStart 返回 date 所属财年的首日（用月序做绝对比较，避免跨年特判）。
  function fiscalYearStart(date) {
    const startMonth = fiscalStartMonth();
    const abs = date.getFullYear() * 12 + date.getMonth();
    let startAbs = date.getFullYear() * 12 + (startMonth - 1);
    if (startAbs > abs) startAbs -= 12;
    const year = Math.floor(startAbs / 12);
    return { year: year, month: startMonth - 1, date: new Date(year, startMonth - 1, 1) };
  }

  // fiscalQuarterStart 返回 date 所属季度的起始日与索引，季度边界是起算月 + 3k 个月。
  // month 是起始月的 0 基序号（JS Date 口径），调用方据此算季度末，避免重复推导。
  function fiscalQuarterStart(date) {
    const yearStart = fiscalYearStart(date);
    const offset = (date.getFullYear() - yearStart.year) * 12 + date.getMonth() - yearStart.month;
    const index = Math.floor(offset / 3);
    const month = yearStart.month + index * 3;
    return { year: yearStart.year, index: index, month: month, date: new Date(yearStart.year, month, 1) };
  }

  function periodRangeJS(period, anchor) {
    const date = parseDate(anchor) || new Date();
    const year = date.getFullYear();
    const month = date.getMonth();
    if (period === 'daily') {
      const key = formatDate(date);
      return { from: key, to: key, key: key };
    }
    if (period === 'weekly') {
      const weekday = (date.getDay() + 6) % 7; // 周一为 0
      const monday = new Date(year, month, date.getDate() - weekday);
      const sunday = new Date(year, month, date.getDate() - weekday + 6);
      return { from: formatDate(monday), to: formatDate(sunday), key: isoWeekKey(monday) };
    }
    if (period === 'monthly') {
      const start = new Date(year, month, 1);
      const end = new Date(year, month + 1, 0);
      return { from: formatDate(start), to: formatDate(end), key: year + '-' + pad2(month + 1) };
    }
    if (period === 'quarterly') {
      const quarter = fiscalQuarterStart(date);
      // 结束日 = 起点 + 3 个月 - 1 天；用 new Date(y, m+3, 0) 拿到该月最后一天。
      const end = new Date(quarter.year, quarter.month, 1);
      end.setMonth(end.getMonth() + 3);
      end.setDate(0);
      return { from: formatDate(quarter.date), to: formatDate(end), key: quarter.year + '-Q' + (quarter.index + 1) };
    }
    const yearStart = fiscalYearStart(date);
    const end = new Date(yearStart.year, yearStart.month, 1);
    end.setFullYear(end.getFullYear() + 1);
    end.setDate(0);
    return { from: formatDate(yearStart.date), to: formatDate(end), key: String(yearStart.year) };
  }

  // shiftAnchor 按周期移动锚点。月 / 季 / 年以「周期起点 + 周期内偏移」为基准再夹住天数，
  // 直接对锚点 setMonth 会让 31 日溢出到下一个周期（如 5-31 减一月变 5-01）。
  function shiftAnchor(period, anchor, delta) {
    const date = parseDate(anchor) || new Date();
    if (period === 'daily') {
      date.setDate(date.getDate() + delta);
      return formatDate(date);
    }
    if (period === 'weekly') {
      date.setDate(date.getDate() + delta * 7);
      return formatDate(date);
    }
    const base = period === 'monthly' ? new Date(date.getFullYear(), date.getMonth(), 1) : period === 'quarterly' ? fiscalQuarterStart(date).date : fiscalYearStart(date).date;
    const offset = Math.round((new Date(date.getFullYear(), date.getMonth(), date.getDate()) - base) / 86400000);
    const months = period === 'monthly' ? delta : period === 'quarterly' ? delta * 3 : delta * 12;
    const shifted = new Date(base.getFullYear(), base.getMonth() + months, 1);
    const shiftedEnd = new Date(shifted.getFullYear(), shifted.getMonth() + (period === 'monthly' ? 1 : period === 'quarterly' ? 3 : 12), 0);
    const length = Math.round((shiftedEnd - shifted) / 86400000) + 1;
    const clamped = Math.min(Math.max(offset, 0), length - 1);
    return formatDate(new Date(shifted.getFullYear(), shifted.getMonth(), shifted.getDate() + clamped));
  }

  // quarterRuleText 用一句话说明当前起算月下的季度划分，与后端 fiscalQuarterRuleText 对齐。
  // 跨年的季度（如 11-1 月）写成「11 月-次年 1 月」，避免被读成 11 月到 1 月之外的区间。
  function quarterRuleText() {
    const start = fiscalStartMonth();
    return [0, 1, 2, 3]
      .map((index) => {
        const from = ((start + index * 3 - 1) % 12) + 1;
        const to = ((start + index * 3 + 1) % 12) + 1;
        if (to < from) return 'Q' + (index + 1) + ' ' + from + ' 月-次年 ' + to + ' 月';
        return 'Q' + (index + 1) + ' ' + from + '-' + to + ' 月';
      })
      .join(' · ');
  }

  function renderReportPeriods() {
    const node = el('report-periods');
    if (!node) return;
    node.innerHTML = PERIOD_ORDER.map(
      (period) =>
        '<button type="button" class="seg-btn' +
        (period === state.reportPeriod ? ' active' : '') +
        '" data-period="' +
        period +
        '" aria-pressed="' +
        (period === state.reportPeriod ? 'true' : 'false') +
        '">' +
        escapeHTML(PERIOD_LABELS[period]) +
        '</button>',
    ).join('');
  }

  // renderFiscalSetting 渲染起算月选择与规则说明。
  // 12 个选项用月份序号生成，标签写「从 N 月起算」而不是「第 N 季度」，避免歧义。
  function renderFiscalSetting() {
    const select = el('fiscal-start-month');
    const rule = el('fiscal-rule');
    if (!select) return;
    const current = fiscalStartMonth();
    if (!select.options.length) {
      select.innerHTML = Array.from({ length: 12 }, (_, index) => index + 1)
        .map((month) => '<option value="' + month + '">' + month + ' 月</option>')
        .join('');
    }
    select.value = String(current);
    if (rule) {
      rule.textContent =
        '当前：' +
        quarterRuleText() +
        (current === DEFAULT_FISCAL_START_MONTH ? '（自然季度）' : '（财年从 ' + current + ' 月起算）');
    }
  }

  // loadPreferences 读取日志库级偏好（起算月）。失败时保留缺省值并提示，
  // 不阻塞报告：口径读不到就用自然季度，界面仍可继续看报告。
  async function loadPreferences() {
    try {
      const data = await bridgeInvoke('work-log.preferences.get', {});
      state.fiscalStartMonth = Number(data && data.fiscal_year_start_month) || DEFAULT_FISCAL_START_MONTH;
    } catch (error) {
      state.fiscalStartMonth = DEFAULT_FISCAL_START_MONTH;
    }
    renderFiscalSetting();
  }

  async function saveFiscalStartMonth(month) {
    const previous = state.fiscalStartMonth;
    try {
      const data = await bridgeInvoke('work-log.preferences.save', { fiscal_year_start_month: month });
      state.fiscalStartMonth = Number(data && data.fiscal_year_start_month) || month;
      renderFiscalSetting();
      notify('已把季度起算月设为 ' + state.fiscalStartMonth + ' 月：' + quarterRuleText());
      // 口径变了，同一锚点对应的区间与键都可能变，必须重算当前报告。
      if (state.view === 'report') await loadReport();
    } catch (error) {
      state.fiscalStartMonth = previous;
      renderFiscalSetting();
      notify('设置起算月失败：' + String((error && error.message) || error), 'error');
    }
  }

  async function loadReport() {
    const anchor = state.reportAnchor || state.selected || today();
    state.reportAnchor = anchor;
    const range = periodRangeJS(state.reportPeriod, anchor);
    state.reportLoading = true;
    state.reportArchive = null;
    renderReport();
    try {
      const data = await bridgeInvoke('work-log.entry.list', {
        from: range.from,
        to: range.to,
        limit: 5000,
      });
      state.reportEntries = (data && data.entries) || [];
    } catch (error) {
      state.reportEntries = [];
      notify('读取报告数据失败：' + String((error && error.message) || error), 'error');
    } finally {
      state.reportLoading = false;
    }
    el('report-range').innerHTML =
      '<span class="badge">' + escapeHTML(range.key) + '</span>' +
      (range.from !== range.to ? '<span class="badge">' + escapeHTML(range.from) + ' ~ ' + escapeHTML(range.to) + '</span>' : '') +
      // 季度 / 年度是按起算月切的，把口径直接标在范围旁边，免得看到 2 月起点以为算错。
      (state.reportPeriod === 'quarterly' || state.reportPeriod === 'yearly'
        ? '<span class="badge">' + escapeHTML(quarterRuleText()) + '</span>'
        : '');
    renderReport();
    renderAggregateStatus();
    loadAggregateStatus();
  }

  function renderReport() {
    renderReportPeriods();
    const body = el('report-body');
    if (state.reportLoading) {
      body.innerHTML = skeleton();
      return;
    }
    const range = periodRangeJS(state.reportPeriod, state.reportAnchor || today());
    const all = state.reportEntries || [];
    const entries = filterEntries(all, state.query);
    if (!entries.length) {
      body.innerHTML = emptyBlock(
        all.length ? '没有匹配的记录' : '本期还没有记录',
        all.length
          ? ['换个关键词，或清空搜索框。']
          : ['写日志或补录后，这里会按 项目 → 功能 → 工时 汇总。', '也可以点右上角“生成归档报告”按确定性规则落盘一份。'],
      );
      return;
    }
    const flow = aggregateFlow(entries);
    const totalMinutes = flow.totalMinutes;
    const measured = entries.filter((i) => i.minutes_source !== 'estimated').reduce((s, i) => s + (Number(i.minutes) || 0), 0);
    const estimated = totalMinutes - measured;
    const activeDays = {};
    entries.forEach((item) => {
      activeDays[item.date] = true;
    });

    const stats =
      '<div class="stat-row">' +
      statCard(totalMinutes > 0 ? hoursShort(totalMinutes) + 'h' : '—', '记录工时') +
      statCard(String(flow.count), '条目') +
      statCard(String(flow.projects.filter((p) => p.name !== '未落项目').length), '项目') +
      statCard(String(Object.keys(activeDays).length), '活跃天数') +
      '</div>' +
      (totalMinutes > 0 && estimated > 0
        ? '<p class="meta">其中实测 ' + escapeHTML(hoursShort(measured)) + 'h · 估算 ' + escapeHTML(hoursShort(estimated)) + 'h</p>'
        : '');

    const catBars = flow.categories.length
      ? '<section class="report-section"><h3>工作类型分布</h3><div class="cat-bars">' +
        flow.categories
          .map((item) => {
            const percent = totalMinutes > 0 ? Math.round((item.minutes / totalMinutes) * 100) : 0;
            return (
              '<div class="cat-bar"><span class="cb-name">' +
              escapeHTML(item.name) +
              '</span><span class="cb-track"><i style="width:' +
              percent +
              '%"></i></span><span class="cb-val">' +
              escapeHTML(hoursShort(item.minutes)) +
              'h</span></div>'
            );
          })
          .join('') +
        '</div></section>'
      : '';

    const flowBlocks = flow.projects
      .map((project) => {
        const features = project.featureList
          .map((feature) => {
            const label = feature.name === '未分类功能' ? '未分类功能' : feature.name;
            const lines = feature.entries
              .map((item) => {
                const parts = [text(item.title, '(未命名)')];
                if (item.summary) parts.push(item.summary);
                return (
                  '<li><span class="rf-title">' +
                  escapeHTML(parts[0]) +
                  '</span>' +
                  (item.summary ? '<span class="rf-sum">' + escapeHTML(item.summary) + '</span>' : '') +
                  (Number(item.minutes) > 0 ? '<span class="rf-hours">' + escapeHTML(hoursShort(item.minutes)) + 'h</span>' : '') +
                  '</li>'
                );
              })
              .join('');
            return (
              '<div class="rf"><div class="rf-head"><span class="rf-name">' +
              escapeHTML(label) +
              '</span><span class="rf-meta">' +
              (feature.minutes > 0 ? escapeHTML(hoursShort(feature.minutes)) + 'h · ' : '') +
              escapeHTML(feature.count) +
              ' 条</span></div><ul class="rf-list">' +
              lines +
              '</ul></div>'
            );
          })
          .join('');
        return (
          '<div class="flow-block"><div class="fb-head"><span class="fb-name">' +
          escapeHTML(project.name) +
          '</span><span class="fb-hours">' +
          (project.minutes > 0 ? escapeHTML(hoursShort(project.minutes)) + 'h' : project.count + ' 条') +
          '</span></div>' +
          categoryChips(
            Object.keys(project.categories).map((name) => ({ name: name, minutes: project.categories[name] })),
          ) +
          features +
          '</div>'
        );
      })
      .join('');

    const byDate = {};
    entries.forEach((item) => {
      if (!byDate[item.date]) byDate[item.date] = [];
      byDate[item.date].push(item);
    });
    const dateKeys = Object.keys(byDate).sort().reverse();
    const dateBlock =
      '<section class="report-section"><h3>按日期</h3>' +
      dateKeys
        .map((date) => {
          const dayEntries = byDate[date];
          const minutes = dayEntries.reduce((s, i) => s + (Number(i.minutes) || 0), 0);
          return (
            '<details class="date-fold"><summary><span class="df-date">' +
            escapeHTML(date) +
            '</span><span class="df-meta">' +
            escapeHTML(dayEntries.length) +
            ' 条' +
            (minutes > 0 ? ' · ' + escapeHTML(hoursShort(minutes)) + 'h' : '') +
            '</span></summary><ul class="date-list">' +
            dayEntries
              .map(
                (item) =>
                  '<li><span class="dl-flow">' +
                  escapeHTML(text(item.project, '未落项目') + (item.feature ? ' · ' + item.feature : '')) +
                  '</span><span class="dl-title">' +
                  escapeHTML(text(item.title, '(未命名)')) +
                  '</span>' +
                  (Number(item.minutes) > 0 ? '<span class="dl-hours">' + escapeHTML(hoursShort(item.minutes)) + 'h</span>' : '') +
                  '</li>',
              )
              .join('') +
            '</ul></details>'
          );
        })
        .join('') +
      '</section>';

    const next = collectValues(entries, 'next');
    const blockers = collectValues(entries, 'blockers');
    const tail =
      (next.length || blockers.length
        ? '<div class="report-columns">' +
          (next.length
            ? '<section class="report-section"><h3>后续事项</h3><ul class="plain">' +
              next.map((v) => '<li>' + escapeHTML(v) + '</li>').join('') +
              '</ul></section>'
            : '') +
          (blockers.length
            ? '<section class="report-section"><h3>阻塞与风险</h3><ul class="plain warn">' +
              blockers.map((v) => '<li>' + escapeHTML(v) + '</li>').join('') +
              '</ul></section>'
            : '') +
          '</div>'
        : '');

    body.innerHTML =
      stats +
      catBars +
      '<section class="report-section"><h3>核心脉络</h3>' +
      (flowBlocks || emptyBlock('本期没有可归类的条目', [])) +
      '</section>' +
      dateBlock +
      tail;
  }

  function statCard(value, label) {
    return '<div class="stat"><span class="stat-num">' + escapeHTML(value) + '</span><span class="stat-label">' + escapeHTML(label) + '</span></div>';
  }

  function collectValues(entries, key) {
    const out = [];
    (entries || []).forEach((item) => {
      (item[key] || []).forEach((value) => {
        const line = text(value);
        if (line && out.indexOf(line) < 0) out.push(line);
      });
    });
    return out;
  }

  async function generateArchive() {
    const anchor = state.reportAnchor || state.selected || today();
    const label = PERIOD_LABELS[state.reportPeriod] || '报告';
    notify('正在按确定性规则生成' + label + '…');
    try {
      const result = await bridgeInvoke('work-log.report.render', { period: state.reportPeriod, date: anchor });
      const report = result && result.report;
      if (report && report.rendered === false) {
        notify('本期没有日志条目，未生成归档报告。', 'warn');
        return;
      }
      const count = report ? report.entry_count : '';
      notify('已生成 ' + label + '（覆盖 ' + count + ' 条），落盘到全局日志目录。');
    } catch (error) {
      notify('生成失败：' + String((error && error.message) || error), 'error');
    }
  }

  async function openArchive() {
    const anchor = state.reportAnchor || state.selected || today();
    try {
      const data = await bridgeInvoke('work-log.report.read', { period: state.reportPeriod, date: anchor });
      state.reportArchive = data;
      const uri = toFileUri(data && (data.html_path || data.md_path));
      if (!uri) {
        notify('报告尚未落盘。', 'warn');
        return;
      }
      notify('提示：分享或导出前请先脱敏。', 'warn');
      openLink(uri).then((opened) => {
        if (!opened) copyText(uri, '宿主未能打开，已复制报告路径');
      });
    } catch (error) {
      notify('还没有归档报告：' + String((error && error.message) || error), 'warn');
    }
  }

  async function copyArchive() {
    const anchor = state.reportAnchor || state.selected || today();
    try {
      const data = await bridgeInvoke('work-log.report.read', { period: state.reportPeriod, date: anchor });
      copyText(data && data.markdown, '已复制报告 Markdown（分享前请脱敏）');
    } catch (error) {
      notify('还没有归档报告：' + String((error && error.message) || error), 'warn');
    }
  }

  // ---------- 周期汇总 ----------

  // lastCompletedAnchor 返回最近一个「已结束」自然周期内的锚点日期。
  // 锚点所在周期尚未结束时（end >= anchor），退回上一期。
  function lastCompletedAnchor(period, anchor) {
    const range = periodRangeJS(period, anchor);
    if (range.to >= anchor) return shiftAnchor(period, anchor, -1);
    return anchor;
  }

  function aggStatusText(row) {
    if (!row.count) return { text: '无记录', cls: 'agg-chip muted' };
    if (row.archived) return { text: '已归档', cls: 'agg-chip ok' };
    return { text: '待补算', cls: 'agg-chip todo' };
  }

  function renderAggregateStatus() {
    const node = el('report-agg');
    if (!node) return;
    const rows = state.aggStatus || [];
    const head =
      '<div class="agg-head"><strong>周期汇总</strong>' +
      '<span class="meta">跟随每日报告自动跨期补算；已有记录不重复</span></div>';
    if (!rows.length) {
      node.innerHTML = head;
      return;
    }
    const list =
      '<div class="agg-rows">' +
      rows
        .map((row) => {
          const label = PERIOD_LABELS[row.period] || row.period;
          const status = aggStatusText(row);
          return (
            '<div class="agg-row">' +
            '<button type="button" class="agg-jump" data-period="' +
            row.period +
            '" data-anchor="' +
            row.anchor +
            '" title="在报告中查看该周期">' +
            escapeHTML(label + ' ' + row.key) +
            '</button>' +
            '<span class="agg-range">' +
            escapeHTML(row.from + ' ~ ' + row.to) +
            '</span>' +
            '<span class="agg-count">' +
            (row.count ? escapeHTML(row.count + ' 条') : '—') +
            '</span>' +
            '<span class="' +
            status.cls +
            '">' +
            status.text +
            '</span>' +
            '</div>'
          );
        })
        .join('') +
      '</div>';
    node.innerHTML = head + list;
    node.querySelectorAll('.agg-jump').forEach((button) => {
      button.addEventListener('click', () => {
        state.reportPeriod = button.getAttribute('data-period');
        state.reportAnchor = button.getAttribute('data-anchor');
        loadReport();
      });
    });
  }

  async function loadAggregateStatus() {
    const anchor = state.reportAnchor || state.selected || today();
    const targets = ['weekly', 'monthly'];
    const rows = [];
    for (const period of targets) {
      const target = lastCompletedAnchor(period, anchor);
      const range = periodRangeJS(period, target);
      let archived = false;
      try {
        await bridgeInvoke('work-log.report.read', { period: period, date: target });
        archived = true;
      } catch (error) {
        archived = false;
      }
      let count = 0;
      try {
        const data = await bridgeInvoke('work-log.entry.list', { from: range.from, to: range.to, limit: 5000 });
        count = ((data && data.entries) || []).length;
      } catch (error) {
        count = 0;
      }
      rows.push({ period: period, anchor: target, key: range.key, from: range.from, to: range.to, archived: archived, count: count });
    }
    state.aggStatus = rows;
    renderAggregateStatus();
  }

  async function aggregateNow() {
    notify('正在跨期补算周报/月报…');
    try {
      const data = await bridgeInvoke('work-log.report.aggregate', {
        periods: ['weekly', 'monthly'],
        completed_only: true,
        force: false,
      });
      const rendered = (data && data.rendered) || 0;
      const skipped = (data && data.skipped) || 0;
      if (rendered > 0) {
        notify('已补算 ' + rendered + ' 份周期汇总' + (skipped ? '（跳过 ' + skipped + ' 份已存在）' : '') + '。');
      } else {
        notify(skipped ? '没有需要补算的周期，已存在 ' + skipped + ' 份记录。' : '没有需要补算的周期。', 'warn');
      }
      await loadAggregateStatus();
      if (state.view === 'report') loadReport();
    } catch (error) {
      notify('汇总失败：' + String((error && error.message) || error), 'error');
    }
  }

  // ---------- 工时表 ----------

  function renderMatrixMode() {
    const node = el('matrix-mode');
    if (!node) return;
    node.querySelectorAll('.seg-btn').forEach((button) => {
      const on = button.getAttribute('data-mode') === state.matrixMode;
      button.classList.toggle('active', on);
      button.setAttribute('aria-pressed', on ? 'true' : 'false');
    });
  }

  async function loadMatrix(year, month) {
    if (year) {
      state.matrixYear = year;
      state.matrixMonth = month;
    }
    if (!state.matrixYear) {
      const anchor = parseDate(state.selected || today());
      state.matrixYear = anchor.getFullYear();
      state.matrixMonth = anchor.getMonth() + 1;
    }
    const from = state.matrixYear + '-' + pad2(state.matrixMonth) + '-01';
    const lastDay = new Date(state.matrixYear, state.matrixMonth, 0).getDate();
    const to = state.matrixYear + '-' + pad2(state.matrixMonth) + '-' + pad2(lastDay);
    el('mx-title').textContent = state.matrixYear + ' 年 ' + state.matrixMonth + ' 月';
    state.matrixLoading = true;
    renderMatrix();
    try {
      const data = await bridgeInvoke('work-log.entry.list', { from: from, to: to, limit: 5000 });
      state.matrixEntries = (data && data.entries) || [];
    } catch (error) {
      state.matrixEntries = [];
      notify('读取工时失败：' + String((error && error.message) || error), 'error');
    } finally {
      state.matrixLoading = false;
    }
    renderMatrix();
  }

  // buildMatrix 把条目折成 项目 × 日期 的分钟矩阵；aligned 时对每个有记录的日子按比例归到 8 小时。
  function buildMatrix(entries, year, month, aligned) {
    const daysInMonth = new Date(year, month, 0).getDate();
    const rows = {};
    const order = [];
    const dayMinutes = {};
    for (let d = 1; d <= daysInMonth; d += 1) dayMinutes[d] = 0;

    (entries || []).forEach((item) => {
      const day = Number(text(item.date).slice(8, 10));
      if (!day) return;
      const name = text(item.project, '未落项目');
      if (!rows[name]) {
        rows[name] = { name: name, cells: {}, byCategory: {} };
        order.push(name);
      }
      const minutes = Number(item.minutes) || 0;
      rows[name].cells[day] = (rows[name].cells[day] || 0) + minutes;
      const category = catLabel(item.category);
      rows[name].byCategory[day] = rows[name].byCategory[day] || {};
      rows[name].byCategory[day][category] = (rows[name].byCategory[day][category] || 0) + minutes;
      dayMinutes[day] += minutes;
    });

    if (aligned) {
      for (let d = 1; d <= daysInMonth; d += 1) {
        const contributors = order
          .map((name) => ({ name: name, minutes: rows[name].cells[d] || 0, present: rows[name].cells[d] !== undefined }))
          .filter((item) => item.present);
        if (!contributors.length) continue;
        const alignedCells = alignDay(contributors);
        const map = {};
        alignedCells.forEach((item) => {
          map[item.name] = item.minutes;
        });
        order.forEach((name) => {
          if (rows[name].cells[d] !== undefined) rows[name].cells[d] = map[name] || 0;
        });
      }
    }

    const dayTotals = {};
    for (let d = 1; d <= daysInMonth; d += 1) dayTotals[d] = 0;
    order.forEach((name) => {
      Object.keys(rows[name].cells).forEach((d) => {
        dayTotals[Number(d)] += rows[name].cells[d];
      });
    });
    const rowList = order.map((name) => rows[name]);
    rowList.sort((a, b) => rowTotal(b) - rowTotal(a) || (a.name < b.name ? -1 : 1));
    return { daysInMonth: daysInMonth, rows: rowList, dayTotals: dayTotals, aligned: aligned };
  }

  function rowTotal(row) {
    return Object.keys(row.cells).reduce((sum, day) => sum + (Number(row.cells[day]) || 0), 0);
  }

  // alignDay 把某天各项目分钟数按占比归到 8 小时，半小时取整，余数补给最大项。
  function alignDay(contributors) {
    const total = contributors.reduce((sum, item) => sum + item.minutes, 0);
    const result = contributors.map((item) => ({ name: item.name, minutes: 0 }));
    if (!contributors.length) return result;
    if (total <= 0) {
      const each = Math.round(ALIGN_TARGET_MINUTES / contributors.length / ALIGN_STEP_MINUTES) * ALIGN_STEP_MINUTES;
      result.forEach((item) => (item.minutes = each));
    } else {
      contributors.forEach((item, index) => {
        result[index].minutes = Math.round((item.minutes / total) * ALIGN_TARGET_MINUTES / ALIGN_STEP_MINUTES) * ALIGN_STEP_MINUTES;
      });
    }
    let sum = result.reduce((acc, item) => acc + item.minutes, 0);
    let largest = 0;
    result.forEach((item, index) => {
      if (item.minutes > result[largest].minutes) largest = index;
    });
    result[largest].minutes += ALIGN_TARGET_MINUTES - sum;
    if (result[largest].minutes < 0) result[largest].minutes = 0;
    return result;
  }

  function renderMatrix() {
    renderMatrixMode();
    const body = el('matrix-body');
    const badges = el('matrix-badges');
    const note = el('matrix-note');
    state.matrixTable = null;
    if (state.matrixLoading || !state.matrixEntries) {
      body.innerHTML = skeleton();
      return;
    }
    const entries = filterEntries(state.matrixEntries, state.query);
    const matrix = buildMatrix(entries, state.matrixYear, state.matrixMonth, state.matrixMode === 'aligned');
    const grand = matrix.rows.reduce((sum, row) => sum + rowTotal(row), 0);
    const recordedDays = Object.keys(matrix.dayTotals).filter((d) => matrix.dayTotals[d] > 0).length;

    badges.innerHTML =
      '<span class="badge">' + escapeHTML(hoursShort(grand) || '0') + ' 小时</span>' +
      '<span class="badge">' + escapeHTML(recordedDays) + ' 个有记录的日子</span>' +
      '<span class="badge new">' + escapeHTML(matrix.rows.length) + ' 个项目</span>';

    if (state.matrixMode === 'aligned') {
      note.textContent = '对齐版：把每个有记录的日子按当天各项目占比折算为合计 8 小时（半小时取整），只用于对照，不改动已记录数据。';
    } else {
      note.textContent = '实际工时：显示已记录时长；当天合计不足或超过 8 小时的日子会标出差额。';
    }

    if (!matrix.rows.length) {
      body.innerHTML = emptyBlock('本月还没有记录', ['写日志或补录后，这里会按项目和日期汇总工时。']);
      return;
    }

    const dayCells = (valueFor) =>
      Array.from({ length: matrix.daysInMonth }, (_, i) => valueFor(i + 1));

    const head =
      '<tr><th class="mx-corner" scope="col"><span class="mx-head"><span class="mx-head-name">项目</span>' +
      '<button type="button" class="mx-copy" data-mx-copy="head" title="复制表头，可粘贴到 Excel" aria-label="复制表头">⧉</button>' +
      '</span></th>' +
      dayCells((day) => '<th scope="col" class="mx-day">' + day + '</th>').join('') +
      '<th scope="col" class="mx-total-col">合计</th></tr>';

    // 复制用的纯文本副本：表头用「1日」，单元格用小时数值（Excel 能直接求和）。
    const tableHead = ['项目'].concat(dayCells((day) => day + '日')).concat(['合计']);

    const rows = matrix.rows
      .map((row, rowIndex) => {
        const cells = dayCells((day) => {
          const minutes = Number(row.cells[day]) || 0;
          const cats = row.byCategory[day] || {};
          const catText = Object.keys(cats)
            .map((name) => name + ' ' + hoursShort(cats[name]) + 'h')
            .join('、');
          const title = state.matrixYear + '-' + pad2(state.matrixMonth) + '-' + pad2(day) + ' · ' + row.name + (catText ? ' · ' + catText : '');
          return (
            '<td class="mx-cell' +
            (minutes > 0 ? ' has' : '') +
            '" data-date="' +
            escapeHTML(state.matrixYear + '-' + pad2(state.matrixMonth) + '-' + pad2(day)) +
            '" title="' +
            escapeHTML(title) +
            '">' +
            (minutes > 0 ? escapeHTML(hoursShort(minutes)) : '') +
            '</td>'
          );
        }).join('');
        const total = rowTotal(row);
        return (
          '<tr><th scope="row" class="mx-rowhead"><span class="mx-head">' +
          '<span class="mx-head-name">' +
          escapeHTML(row.name) +
          '</span>' +
          '<button type="button" class="mx-copy" data-mx-copy="row" data-mx-row="' +
          rowIndex +
          '" title="复制「' +
          escapeHTML(row.name) +
          '」这一行，可粘贴到 Excel" aria-label="复制 ' +
          escapeHTML(row.name) +
          ' 这一行">⧉</button>' +
          '</span></th>' +
          cells +
          '<td class="mx-total">' +
          (total > 0 ? escapeHTML(hoursShort(total)) : '—') +
          '</td></tr>'
        );
      })
      .join('');

    const foot =
      '<tr class="mx-foot"><th scope="row" class="mx-rowhead"><span class="mx-head">' +
      '<span class="mx-head-name">当天合计</span>' +
      '<button type="button" class="mx-copy" data-mx-copy="total" title="复制当天合计行，可粘贴到 Excel" aria-label="复制当天合计行">⧉</button>' +
      '</span></th>' +
      dayCells((day) => {
        const minutes = matrix.dayTotals[day] || 0;
        const off = minutes > 0 && minutes !== ALIGN_TARGET_MINUTES;
        return (
          '<td class="mx-cell' +
          (minutes > 0 ? ' has' : '') +
          (off ? ' off' : '') +
          '" title="' +
          escapeHTML(state.matrixYear + '-' + pad2(state.matrixMonth) + '-' + pad2(day) + ' · 合计 ' + (hoursShort(minutes) || '0') + 'h' + (off ? '（差 ' + hoursShort(Math.abs(minutes - ALIGN_TARGET_MINUTES)) + 'h）' : '')) +
          '">' +
          (minutes > 0 ? escapeHTML(hoursShort(minutes)) : '') +
          '</td>'
        );
      }).join('') +
      '<td class="mx-total">' +
      escapeHTML(hoursShort(grand) || '0') +
      '</td></tr>';

    state.matrixTable = {
      head: tableHead,
      rows: matrix.rows.map((row) => {
        const total = rowTotal(row);
        return [row.name]
          .concat(dayCells((day) => hoursShort(row.cells[day]) || ''))
          .concat([total > 0 ? hoursShort(total) : '']);
      }),
      total: ['当天合计']
        .concat(dayCells((day) => hoursShort(matrix.dayTotals[day]) || ''))
        .concat([hoursShort(grand) || '']),
    };

    body.innerHTML =
      '<table class="matrix"><thead>' +
      head +
      '</thead><tbody>' +
      rows +
      '</tbody><tfoot>' +
      foot +
      '</tfoot></table>';

    body.querySelectorAll('.mx-cell[data-date]').forEach((cell) => {
      cell.addEventListener('click', () => {
        const date = cell.getAttribute('data-date');
        if (date) openDateFromMatrix(date);
      });
    });
    body.querySelectorAll('[data-mx-copy]').forEach((button) => {
      button.addEventListener('click', (event) => {
        // 行头按钮在 sticky 列里，点它不应该连带跳到日历视图。
        event.stopPropagation();
        copyMatrixPart(button.getAttribute('data-mx-copy'), button.getAttribute('data-mx-row'));
      });
    });
  }

  // copyMatrixPart 复制工时表的一部分：表头行、某个项目行，或当天合计行。
  function copyMatrixPart(kind, rowIndex) {
    const table = state.matrixTable;
    if (!table) return;
    if (kind === 'head') {
      copyTableToClipboard([table.head], '已复制表头，可直接粘贴到 Excel');
      return;
    }
    if (kind === 'total') {
      copyTableToClipboard([table.total], '已复制当天合计行，可直接粘贴到 Excel');
      return;
    }
    const index = Number(rowIndex);
    const row = Number.isInteger(index) ? table.rows[index] : null;
    if (!row) return;
    copyTableToClipboard([row], '已复制「' + row[0] + '」这一行，可直接粘贴到 Excel');
  }

  async function openDateFromMatrix(date) {
    switchView('calendar');
    const parts = date.split('-').map(Number);
    try {
      await loadCalendar(parts[0], parts[1]);
      await selectDate(date);
    } catch (error) {
      notify('读取日志失败：' + String((error && error.message) || error), 'error');
    }
  }

  // ---------- 当前工作台 ----------

  async function loadBoard(options) {
    const silent = !!(options && options.silent) && !!state.board;
    if (silent && state.boardRefreshing) return;
    state.boardRefreshing = true;
    if (!silent) {
      state.boardLoading = true;
      if (state.view === 'workbench') renderWorkbench();
    }
    try {
      const data = await bridgeInvoke('work-log.project.board', {});
      state.board = data || { projects: [], summary: {}, attention: [], focus: [] };
      if (!state.workbenchModeResolved) {
        const currentCount = (state.board.current || []).length;
        const plannedCount = (state.board.planned || []).length;
        if (currentCount === 0 && plannedCount > 0) state.workbenchMode = 'planned';
        state.workbenchModeResolved = true;
      }
    } catch (error) {
      if (silent && error && error.code === 'bridge_unavailable') {
        // 后台静默刷新失败时保留现有内容，不打断用户。
      } else if (error && error.code === 'bridge_unavailable') {
        state.board = { projects: [], summary: {}, attention: [], focus: [], error: 'bridge_unavailable' };
      } else if (!silent) {
        notify('读取当前工作台失败：' + String((error && error.message) || error), 'error');
        state.board = { projects: [], summary: {}, attention: [], focus: [], error: String((error && error.message) || error) };
      }
    } finally {
      state.boardRefreshing = false;
      state.boardLoading = false;
      if (state.view === 'workbench') renderWorkbench();
    }
  }

  let boardAutoRefreshTimer = null;

  function startBoardAutoRefresh() {
    if (boardAutoRefreshTimer) {
      clearInterval(boardAutoRefreshTimer);
      boardAutoRefreshTimer = null;
    }
    boardAutoRefreshTimer = setInterval(() => {
      if (document.hidden || state.view !== 'workbench') return;
      loadBoard({ silent: true });
    }, BOARD_REFRESH_MS);
  }

  function boardProjects() {
    return (state.board && state.board.projects) || [];
  }

  function nextStepsHtml(item, limit) {
    const steps = Array.isArray(item.next_steps) ? item.next_steps : [];
    if (!steps.length) return '<div class="wb-empty-line">还没有下一步</div>';
    const open = steps.filter((step) => !step.done);
    const visible = (open.length ? open : steps).slice(0, limit || 2);
    const remaining = Math.max(0, open.length - visible.length);
    return (
      '<div class="wb-steps">' +
      visible
        .map(
          (step) =>
            '<label class="wb-step' + (step.done ? ' done' : '') + '"><input type="checkbox" data-wb-step="' +
            escapeHTML(step.id) +
            '" data-project-id="' +
            escapeHTML(item.id) +
            '"' +
            (step.done ? ' checked' : '') +
            ' /><span>' +
            escapeHTML(step.text) +
            (step.due ? '<em>@' + escapeHTML(step.due) + '</em>' : '') +
            '</span></label>',
        )
        .join('') +
      (remaining > 0 ? '<span class="wb-more">还有 ' + escapeHTML(remaining) + ' 项</span>' : '') +
      '</div>'
    );
  }

  function relativeActivityLabel(item) {
    const days = Number(item.days_since_activity);
    if (!isFinite(days) || days < 0) return '暂无日志';
    if (days === 0) return '今天';
    if (days === 1) return '昨天';
    if (days <= 7) return days + ' 天前';
    return text(item.last_date);
  }

  function projectRow(item, compact) {
    const stateValue = text(item.state, 'planned');
    const priority = text(item.priority, 'P2');
    const stage = text(item.stage, 'unknown');
    const stageLabel = text(item.stage_label, '未判断');
    const progress = Number(item.progress) || 0;
    const inferred = text(item.progress_source) !== 'manual';
    const done = stateValue === 'done';
    const planned = stateValue === 'planned';
    const running = stateValue === 'in_progress' && item.is_current === true;
    const displayStage = done
      ? '已完成'
      : stateValue === 'blocked'
        ? '受阻'
        : stateValue === 'paused' || stateValue === 'dormant'
          ? '搁置'
          : stageLabel;
    const stageClass = done
      ? 'done'
      : stateValue === 'blocked'
        ? 'blocked'
        : stateValue === 'paused' || stateValue === 'dormant'
          ? 'dormant'
          : stage;
    const steps = item.next_steps || [];
    const next = steps.find((step) => !step.done);
    const nextText = next ? text(next.text) : '暂无下一步';
    const stepDone = steps.filter((step) => step.done).length;
    const stageConfidence = confidenceLabel(item.stage_confidence);
    const progressConfidence = confidenceLabel(item.progress_confidence);
    const stageTitle =
      '阶段：' +
      (done ? '已完成' : stateValue === 'blocked' ? '受阻' : stateValue === 'paused' || stateValue === 'dormant' ? '搁置' : stageLabel) +
      (stateValue === 'blocked' && stageLabel !== '受阻' ? '（推断阶段：' + stageLabel + '）' : '') +
      (stageConfidence ? ' · 置信度 ' + stageConfidence : '');
    let progressTitle = '手工设置';
    if (text(item.progress_source) === 'next_steps') {
      progressTitle = '依据：下一步完成 ' + stepDone + '/' + steps.length;
    } else if (text(item.progress_source) === 'stage') {
      progressTitle = '依据：阶段判断';
    } else if (text(item.progress_source) === 'state') {
      progressTitle = '依据：项目已完成';
    }
    if (progressConfidence) progressTitle += ' · 置信度 ' + progressConfidence;
    const warning = [];
    if (stateValue === 'blocked') warning.push('受阻');
    if (item.overdue) warning.push('逾期');
    if (item.stale) warning.push(text(item.days_since_activity) + ' 天无日志');
    const meta = [relativeActivityLabel(item)];
    if (Number(item.minutes_7d) > 0) meta.push('本周 ' + hoursShort(item.minutes_7d) + 'h');
    if (text(item.target_date)) meta.push('目标 ' + text(item.target_date));
    return (
      '<article class="wb-row' +
      (compact ? ' compact' : '') +
      ' state-' +
      escapeHTML(stateValue) +
      '"><span class="priority-bar p-' +
      escapeHTML(priority) +
      '"></span><div class="wb-row-main"><div class="wb-row-title">' +
      (running ? '<span class="wb-spinner" role="status" aria-label="正在推进" title="正在推进"></span>' : '') +
      '<button class="link-btn wb-row-name" type="button" data-wb-open="' +
      escapeHTML(item.name) +
      '" title="打开项目详情">' +
      escapeHTML(item.name) +
      '</button><span class="stage-pill stage-' +
      escapeHTML(stageClass) +
      '" title="' +
      escapeHTML(stageTitle) +
      '">' +
      escapeHTML(displayStage) +
      '</span><span class="priority-pill p-' +
      escapeHTML(priority) +
      '">' +
      escapeHTML(priority) +
      '</span>' +
      (warning.length ? '<span class="wb-warn">' + escapeHTML(warning.join(' · ')) + '</span>' : '') +
      '</div><div class="wb-row-progress"><div class="wb-progress"><i style="width:' +
      escapeHTML(String(planned ? 0 : progress)) +
      '%"></i></div><span class="wb-progress-label" title="' +
      escapeHTML(progressTitle) +
      '">' +
      (planned ? '待开始' : done ? '100%' : (inferred ? '推断 ' : '') + escapeHTML(String(progress)) + '%') +
      '</span></div><div class="wb-row-next"><span>下一步</span>' +
      escapeHTML(nextText) +
      '</div></div><div class="wb-row-side"><div class="wb-row-meta">' +
      meta.map((value) => '<span>' + escapeHTML(value) + '</span>').join('') +
      '</div><div class="wb-row-actions"><button class="btn subtle" type="button" data-wb-edit="' +
      escapeHTML(item.id) +
      '">编辑</button>' +
      (planned
        ? '<button class="btn subtle" type="button" data-wb-start="' + escapeHTML(item.id) + '">开始</button>'
        : done
          ? ''
          : '<button class="btn subtle" type="button" data-wb-complete="' + escapeHTML(item.id) + '">完成</button>') +
      '</div></div></article>'
    );
  }

  function renderWorkbench() {
    const current = el('wb-current');
    const done = el('wb-done');
    const doneWrap = el('wb-done-wrap');
    const summary = el('wb-summary');
    const attention = el('wb-attention');
    if (!current || !done || !doneWrap || !summary || !attention) return;
    if (state.boardLoading && !state.board) {
      current.innerHTML = skeleton();
      done.innerHTML = '';
      return;
    }
    const board = state.board || { projects: [], summary: {}, attention: [] };
    if (board.error === 'bridge_unavailable') {
      current.innerHTML = emptyBlock('请从 HiMind Agent 打开本视图', ['当前工作台需要宿主桥接才能读取项目和日志。']);
      done.innerHTML = '';
      summary.innerHTML = '';
      attention.innerHTML = '';
      return;
    }
    const recentDays = Number(board.recent_days) || 14;
    const subtitle = el('wb-subtitle');
    if (subtitle) subtitle.textContent = '最近 ' + recentDays + ' 天有推进的项目 · 每 ' + (BOARD_REFRESH_MS / 1000) + ' 秒自动刷新';
    const query = text(state.query).toLowerCase();
    const all = boardProjects().filter(
      (item) =>
        !query ||
        text(item.name).toLowerCase().indexOf(query) >= 0 ||
        text(item.description).toLowerCase().indexOf(query) >= 0,
    );
    const matchesQuery = (item) =>
      !query ||
      text(item.name).toLowerCase().indexOf(query) >= 0 ||
      text(item.description).toLowerCase().indexOf(query) >= 0;
    const currentItems = (board.current || all.filter((item) => item.is_current === true)).filter(matchesQuery);
    const plannedItems = (board.planned || all.filter((item) => item.activity_bucket === 'planned')).filter(matchesQuery);
    const doneItems = (board.done || all.filter((item) => item.is_done_recent === true)).filter(matchesQuery);
    const mode = state.workbenchMode === 'planned' ? 'planned' : 'recent';
    const listItems = mode === 'planned' ? plannedItems : currentItems;
    const sm = board.summary || {};
    summary.innerHTML =
      '<strong>' + escapeHTML(sm.current || currentItems.length) + ' 个正在推进</strong>' +
      (plannedItems.length > 0 ? '<span>' + escapeHTML(plannedItems.length) + ' 个待开始</span>' : '') +
      '<span>本周 ' + escapeHTML(hoursShort(sm.minutes_this_week || 0) || '0') + 'h</span>' +
      (Number(sm.blocked_current || 0) > 0 ? '<span class="wb-warn">' + escapeHTML(sm.blocked_current) + ' 个受阻</span>' : '');
    const rows = Array.isArray(board.attention) ? board.attention : [];
    attention.innerHTML = rows.length
      ? '<div class="wb-attention-line"><strong>需要关注</strong>' +
        rows
          .map((row) => {
            const reasons = (row.reasons || []).map((reason) => {
              if (reason === 'blocked') return '受阻';
              if (reason === 'stale') return text(row.days) + ' 天无日志';
              if (reason === 'overdue') return '已逾期';
              return reason;
            });
            return (
              '<button class="link-btn" type="button" data-wb-open="' +
              escapeHTML(row.name) +
              '">' +
              escapeHTML(row.name) +
              ' · ' +
              escapeHTML(reasons.join(' · ')) +
              '</button>'
            );
          })
          .join('') +
        '</div>'
      : '';
    current.innerHTML = listItems.length
      ? listItems.map((item) => projectRow(item, false)).join('')
      : mode === 'planned'
        ? emptyBlock('还没有待开始的项目', [
            '在项目编辑里把状态设为「待开始」，或从历史项目中选择一个重新开始。',
          ])
        : emptyBlock('最近 ' + recentDays + ' 天没有推进中的项目', [
            '记录一条工作日志后，对应项目会自动进入这里。',
            '可以切换到「待开始」查看计划中的项目。',
          ]);
    done.innerHTML = doneItems.length ? doneItems.map((item) => projectRow(item, true)).join('') : '';
    doneWrap.hidden = doneItems.length === 0;
    const doneCount = el('wb-done-count');
    if (doneCount) doneCount.textContent = doneItems.length ? '（' + doneItems.length + '）' : '';
    if (el('wb-list-title')) el('wb-list-title').textContent = mode === 'planned' ? '待开始' : '最近活动';
    if (el('wb-current-note')) {
      el('wb-current-note').textContent =
        mode === 'planned' ? '已计划但尚未开始' : '最近 ' + recentDays + ' 天有推进';
    }
    if (el('wb-recent-count')) el('wb-recent-count').textContent = String(currentItems.length);
    if (el('wb-planned-count')) el('wb-planned-count').textContent = String(plannedItems.length);
    document.querySelectorAll('#wb-modes [data-wb-mode]').forEach((button) => {
      const on = button.getAttribute('data-wb-mode') === mode;
      button.classList.toggle('active', on);
      button.setAttribute('aria-pressed', on ? 'true' : 'false');
    });
    if (el('wb-all-projects')) {
      const total = (board.projects || []).length;
      el('wb-all-projects').textContent = total > 0 ? '查看全部项目（' + total + '）' : '查看全部项目';
    }
    bindWorkbench();
  }
  function bindWorkbench() {
    const node = el('workbench-view');
    if (!node) return;

    node.querySelectorAll('#wb-modes [data-wb-mode]').forEach((button) => {
      button.addEventListener('click', () => {
        state.workbenchMode = button.getAttribute('data-wb-mode') === 'planned' ? 'planned' : 'recent';
        state.workbenchModeResolved = true;
        renderWorkbench();
      });
    });
    node.querySelectorAll('[data-wb-start]').forEach((button) => {
      button.addEventListener('click', () =>
        updateProjectWork(button.getAttribute('data-wb-start'), { state: 'in_progress' }),
      );
    });
    node.querySelectorAll('[data-wb-step]').forEach((checkbox) => {
      checkbox.addEventListener('change', () => {
        const projectID = checkbox.getAttribute('data-project-id');
        const stepID = checkbox.getAttribute('data-wb-step');
        const item = boardProjects().find((p) => text(p.id) === projectID);
        if (!item) return;
        const steps = (item.next_steps || []).map((step) => {
          if (text(step.id) !== stepID) return step;
          return Object.assign({}, step, { done: checkbox.checked });
        });
        updateProjectWork(projectID, { next_steps: steps });
      });
    });
    node.querySelectorAll('[data-wb-edit]').forEach((button) => {
      button.addEventListener('click', () => {
        const item = boardProjects().find((p) => text(p.id) === button.getAttribute('data-wb-edit'));
        if (item) openWorkModal(item, false);
      });
    });
    node.querySelectorAll('[data-wb-complete]').forEach((button) => {
      button.addEventListener('click', () => {
        const item = boardProjects().find((p) => text(p.id) === button.getAttribute('data-wb-complete'));
        if (item) openWorkModal(item, true);
      });
    });
    node.querySelectorAll('[data-wb-open]').forEach((button) => {
      button.addEventListener('click', () => openProjectFromBoard(button.getAttribute('data-wb-open')));
    });
  }

  async function updateProjectWork(projectID, patch) {
    try {
      await bridgeInvoke('work-log.project.work.update', { project_id: projectID, patch: patch });
      if (patch.state === 'in_progress' && state.workbenchMode === 'planned') {
        state.workbenchMode = 'recent';
        state.workbenchModeResolved = true;
      }
      notify('项目状态已更新。');
      await loadBoard();
      loadProjects().catch(() => {});
      if (state.view === 'projects' && state.activeProject) {
        loadProjects()
          .then(() => selectProject(state.activeProject))
          .catch(() => {});
      }
    } catch (error) {
      notify('更新项目状态失败：' + String((error && error.message) || error), 'error');
      await loadBoard();
    }
  }

  function openProjectFromBoard(name) {
    state.activeProject = text(name);
    switchView('projects');
    loadProjects()
      .then(() => selectProject(state.activeProject))
      .catch((error) => notify('读取项目失败：' + String((error && error.message) || error), 'error'));
  }

  function formatNextStepsText(steps) {
    return (steps || [])
      .map((step) => (step.done ? '[x] ' : '[ ] ') + text(step.text) + (step.due ? ' @' + step.due : ''))
      .join('\n');
  }

  function parseNextStepsText(raw, existing) {
    const byText = {};
    (existing || []).forEach((step) => {
      byText[text(step.text)] = step;
    });
    return text(raw)
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter(Boolean)
      .map((line) => {
        let done = false;
        let body = line;
        const checked = line.match(/^\[(x|X| )?\]\s*(.*)$/);
        if (checked) {
          done = text(checked[1]).toLowerCase() === 'x';
          body = checked[2];
        }
        let due = '';
        const dueMatch = body.match(/\s*@(\d{4}-\d{2}-\d{2})\s*$/);
        if (dueMatch) {
          due = dueMatch[1];
          body = body.slice(0, dueMatch.index).trim();
        }
        const old = byText[body];
        return {
          id: old ? text(old.id) : '',
          text: body,
          done: done,
          due: due || (old ? text(old.due) : ''),
          source: old ? text(old.source, 'manual') : 'manual',
          created_at: old ? text(old.created_at) : '',
          completed_at: done ? (old ? text(old.completed_at) : '') : '',
        };
      })
      .filter((step) => step.text);
  }

  function normalizeStepDraft(step) {
    return {
      id: text(step.id),
      text: text(step.text),
      done: !!step.done,
      due: text(step.due),
      source: text(step.source, 'manual'),
      created_at: text(step.created_at),
      completed_at: text(step.completed_at),
    };
  }

  function parseStepInput(raw) {
    let value = text(raw);
    if (!value) return null;
    let due = '';
    const match = value.match(/\s*@(\d{4}-\d{2}-\d{2})\s*$/);
    if (match) {
      due = match[1];
      value = value.slice(0, match.index).trim();
    }
    if (!value) return null;
    return { id: '', text: value, done: false, due: due, source: 'manual', created_at: '', completed_at: '' };
  }

  function renderWorkSteps() {
    const list = el('w-next-list');
    if (!list) return;
    const steps = state.workEditingSteps || [];
    if (!steps.length) {
      list.innerHTML = '<div class="wb-empty-line">还没有下一步</div>';
      return;
    }
    list.innerHTML = steps
      .map(
        (step, index) =>
          '<div class="wb-next-row"><label class="wb-step' +
          (step.done ? ' done' : '') +
          '"><input type="checkbox" data-w-next-toggle="' +
          index +
          '"' +
          (step.done ? ' checked' : '') +
          ' /><span>' +
          escapeHTML(step.text) +
          (step.due ? '<em>@' + escapeHTML(step.due) + '</em>' : '') +
          '</span></label><button class="link-btn wb-next-remove" type="button" data-w-next-remove="' +
          index +
          '" aria-label="删除下一步">×</button></div>',
      )
      .join('');
  }

  function updateCompleteBox() {
    const box = el('w-complete-box');
    if (!box) return;
    box.hidden = el('w-state').value !== 'done';
  }

  function addWorkStep() {
    const input = el('w-next-input');
    if (!input) return;
    const step = parseStepInput(input.value);
    if (!step) return;
    state.workEditingSteps = (state.workEditingSteps || []).concat([step]);
    input.value = '';
    renderWorkSteps();
    input.focus();
  }

  function openWorkModal(item, complete) {
    if (!item) return;
    state.workEditing = { item: item, complete: !!complete };
    state.workEditingSteps = (item.next_steps || []).map(normalizeStepDraft);
    el('w-project-id').value = text(item.id);
    el('w-project-name').value = text(item.name);
    el('w-state').value = complete ? 'done' : text(item.state, 'planned');
    el('w-priority').value = text(item.priority, 'P2');
    el('w-progress').value = Number(item.manual_progress) || 0;
    el('w-target').value = text(item.target_date);
    el('w-goal').value = text(item.goal);
    el('w-blockers').value = (item.blockers || []).join('\n');
    el('w-completion').value = text(item.completion_summary);
    el('w-write-log').checked = true;
    el('w-next-input').value = '';
    renderWorkSteps();
    updateCompleteBox();
    el('w-more').open = false;
    el('work-title').textContent = complete ? '标记项目完成' : '更新项目';
    el('work-hint').textContent = complete
      ? '完成时间记为今天；可同时写一条完成日志。'
      : '先改状态、优先级或下一步；其他设置收在下面。';
    el('work-backdrop').hidden = false;
    setTimeout(() => (complete ? el('w-completion').focus() : el('w-next-input').focus()), 30);
  }

  function closeWorkModal() {
    el('work-backdrop').hidden = true;
    state.workEditing = null;
    state.workEditingSteps = [];
  }

  async function submitWorkForm(event) {
    event.preventDefault();
    if (!state.workEditing) return;
    const item = state.workEditing.item;
    const patch = {
      state: el('w-state').value,
      priority: el('w-priority').value,
      progress: Number(el('w-progress').value) || 0,
      goal: el('w-goal').value,
      target_date: el('w-target').value,
      next_steps: (state.workEditingSteps || [])
        .filter((step) => text(step.text))
        .map((step) => ({
          id: text(step.id),
          text: text(step.text),
          done: !!step.done,
          due: text(step.due),
          source: text(step.source, 'manual'),
          created_at: text(step.created_at),
          completed_at: step.done ? text(step.completed_at) : '',
        })),
      blockers: text(el('w-blockers').value)
        .split(/\r?\n/)
        .map((line) => line.trim())
        .filter(Boolean),
      completion_summary: el('w-completion').value,
    };
    try {
      await bridgeInvoke('work-log.project.work.update', { project_id: item.id, patch: patch });
      if (patch.state === 'done' && el('w-write-log').checked) {
        const summary = text(patch.completion_summary, '项目已完成');
        await bridgeInvoke('work-log.entry.upsert', {
          date: today(),
          entry: {
            source: 'manual',
            project: text(item.name),
            category: '开发',
            title: '完成项目：' + text(item.name),
            summary: summary,
            outcomes: patch.goal ? [patch.goal] : [],
            next: [],
            minutes: 0,
            minutes_source: 'manual',
          },
        });
      }
      closeWorkModal();
      notify(patch.state === 'done' ? '项目已标记完成。' : '项目工作状态已保存。');
      await loadBoard();
      await loadProjects();
      if (state.view === 'projects' && state.activeProject) {
        await selectProject(state.activeProject);
      }
    } catch (error) {
      notify('保存项目状态失败：' + String((error && error.message) || error), 'error');
    }
  }

  // ---------- 计划 ----------

  function planDateLabel(date) {
    const d = parseDate(date);
    if (!d) return text(date);
    const weekdays = ['日', '一', '二', '三', '四', '五', '六'];
    return date + ' 周' + weekdays[d.getDay()];
  }

  function ensurePlanDate() {
    if (!state.planDate) state.planDate = today();
    return state.planDate;
  }

  async function loadPlan(date) {
    state.planDate = date || ensurePlanDate();
    state.planLoading = true;
    if (state.view === 'plan') renderPlan();
    try {
      const [planResult, itemResult, reviewResult] = await Promise.all([
        bridgeInvoke('work-log.plan.get', { date: state.planDate }),
        bridgeInvoke('work-log.item.list', { include_done: true }),
        bridgeInvoke('work-log.plan.review', { date: state.planDate }),
      ]);
      state.plan = (planResult && planResult.plan) || null;
      state.workItemCache = (itemResult && itemResult.items) || [];
      state.planReview = (reviewResult && reviewResult.review) || null;
    } catch (error) {
      if (error && error.code === 'bridge_unavailable') {
        state.plan = null;
        state.planReview = null;
        state.workItemCache = [];
      } else {
        notify('读取计划失败：' + String((error && error.message) || error), 'error');
        state.plan = null;
        state.planReview = null;
        state.workItemCache = [];
      }
    } finally {
      state.planLoading = false;
      if (state.view === 'plan') renderPlan();
    }
  }

  function planCapacityHtml(plan) {
    const cap = (plan && plan.capacity) || {};
    const work = Number(cap.work_minutes) || 0;
    const meeting = Number(cap.meeting_minutes) || 0;
    const brk = Number(cap.break_minutes) || 0;
    const buffer = Number(cap.buffer_minutes) || 0;
    const committed = Number(cap.committed_minutes) || 0;
    const available = Number(cap.available_minutes) || 0;
    const remaining = Number(cap.remaining_minutes) || 0;
    const pct = available > 0 ? Math.min(100, Math.round((committed / available) * 100)) : 0;
    return (
      '<div class="plan-capacity-main"><div class="plan-capacity-labels"><strong>已排 ' +
      escapeHTML(hoursShort(committed) || '0') +
      'h / 可用 ' +
      escapeHTML(hoursShort(available) || '0') +
      'h</strong><span class="' +
      (remaining < 0 ? 'wb-warn' : 'meta') +
      '">' +
      (remaining < 0 ? '超出 ' + escapeHTML(hoursShort(-remaining)) + 'h' : '剩余 ' + escapeHTML(hoursShort(remaining) || '0') + 'h') +
      '</span></div><div class="plan-capacity-bar"><i style="width:' +
      pct +
      '%"></i></div><div class="plan-capacity-chips"><span>工作 ' +
      escapeHTML(hoursShort(work) || '0') +
      'h</span><span>会议 ' +
      escapeHTML(hoursShort(meeting) || '0') +
      'h</span><span>休息 ' +
      escapeHTML(hoursShort(brk) || '0') +
      'h</span><span>缓冲 ' +
      escapeHTML(hoursShort(buffer) || '0') +
      'h</span></div></div>'
    );
  }

  function renderPlan() {
    const node = el('plan-blocks');
    const backlog = el('plan-backlog-list');
    const capacity = el('plan-capacity');
    if (!node || !backlog || !capacity) return;
    const date = ensurePlanDate();
    if (el('plan-date')) el('plan-date').value = date;
    if (el('plan-title')) el('plan-title').textContent = planDateLabel(date);
    const isToday = date === today();
    if (el('plan-auto')) {
      el('plan-auto').hidden = !isToday;
      el('plan-auto').title = isToday ? '按当前项目和待开始工作项生成今日计划' : '一键智能排期仅支持今天';
    }
    if (state.planLoading) {
      node.innerHTML = skeleton();
      backlog.innerHTML = '';
      capacity.innerHTML = '';
      return;
    }
    const plan = state.plan;
    if (!plan) {
      node.innerHTML = emptyBlock('暂时读不到计划', ['请确认从 HiMind Agent 打开本视图，或先保存一次计划。']);
      backlog.innerHTML = '';
      capacity.innerHTML = '';
      return;
    }
    const cap = plan.capacity || {};
    const review = state.planReview || null;
    const reviewDone = !!(review && review.finalized);
    if (el('plan-badges')) {
      el('plan-badges').innerHTML =
        '<span class="badge">' + (plan.revision > 0 ? '第 ' + escapeHTML(plan.revision) + ' 版' : '草稿') + '</span>' +
        '<span class="badge">' + escapeHTML((plan.blocks || []).length) + ' 个时间块</span>' +
        (reviewDone
          ? '<span class="badge" title="实际 ' + escapeHTML(review.actual_minutes) + ' / 计划 ' + escapeHTML(review.planned_minutes) + ' 分钟">实际完成 ' + escapeHTML(review.completion_percent) + '%</span>'
          : '<span class="badge">待到当日结束</span>');
    }
    capacity.innerHTML =
      planCapacityHtml(plan) +
      '<p class="meta plan-review-note">' +
      (reviewDone
        ? '实际完成 ' + escapeHTML(review.completion_percent) + '% · 依据当日日志'
        : '实际完成以当日结束后的日志为准') +
      '</p>';
    const query = text(state.query).toLowerCase();
    const items = (state.workItemCache || []).filter(
      (item) =>
        text(item.status) !== 'done' &&
        text(item.status) !== 'cancelled' &&
        (!query || text(item.title).toLowerCase().indexOf(query) >= 0 || text(item.project).toLowerCase().indexOf(query) >= 0),
    );
    if (el('plan-backlog-count')) el('plan-backlog-count').textContent = items.length + ' 项未完成';
    backlog.innerHTML = items.length
      ? items
          .map(
            (item) =>
              '<article class="plan-item"><div class="plan-item-head"><strong>' +
              escapeHTML(item.title) +
              '</strong><span class="priority-pill p-' +
              escapeHTML(item.priority || 'P2') +
              '">' +
              escapeHTML(item.priority || 'P2') +
              '</span></div><div class="plan-item-meta"><span>' +
              escapeHTML(item.project || '') +
              (item.feature ? ' · ' + escapeHTML(item.feature) : '') +
              '</span><span>预计 ' +
              escapeHTML(hoursShort(item.estimate_minutes) || '0') +
              'h · 剩余 ' +
              escapeHTML(hoursShort(item.remaining_minutes) || '0') +
              'h</span>' +
              (item.due_date ? '<span>截止 ' + escapeHTML(item.due_date) + '</span>' : '') +
              '</div><div class="plan-item-actions"><button class="btn subtle" type="button" data-plan-schedule="' +
              escapeHTML(item.id) +
              '">排入</button><button class="btn subtle" type="button" data-plan-item-edit="' +
              escapeHTML(item.id) +
              '">编辑</button></div></article>',
          )
          .join('')
      : '<p class="meta pad">没有待排工作项。先新增一个，或把已完成的工作项重新打开。</p>';

    const blocks = (plan.blocks || []).slice().sort((a, b) => text(a.start).localeCompare(text(b.start)));
    const reviewByID = {};
    ((review && review.blocks) || []).forEach((item) => {
      reviewByID[text(item.id)] = item;
    });
    const actualLabel = (status) => {
      if (status === 'done') return '实际完成';
      if (status === 'partial') return '部分完成';
      if (status === 'skipped') return '未完成';
      return '待结算';
    };
    node.innerHTML = blocks.length
      ? blocks
          .map((block) => {
            const title = text(block.title) || PLAN_BLOCK_KIND_LABELS[block.kind] || block.kind;
            const duration = blockMinutes(block);
            const workItem = (state.workItemCache || []).find((item) => text(item.id) === text(block.work_item_id));
            const reviewBlock = reviewByID[text(block.id)];
            const actualHtml =
              reviewDone && reviewBlock && (block.kind === 'focus' || block.kind === 'admin')
                ? '<span class="plan-actual status-' +
                  escapeHTML(reviewBlock.status) +
                  '" title="' +
                  escapeHTML(reviewBlock.evidence || '') +
                  '">' +
                  escapeHTML(actualLabel(reviewBlock.status)) +
                  '</span>'
                : '';
            return (
              '<article class="plan-block kind-' +
              escapeHTML(block.kind) +
              ' status-' +
              escapeHTML(block.status) +
              '" data-block-id="' +
              escapeHTML(block.id) +
              '"><div class="plan-block-time"><strong>' +
              escapeHTML(block.start) +
              '</strong><span>' +
              escapeHTML(block.end) +
              '</span></div><div class="plan-block-body"><div class="plan-block-title">' +
              escapeHTML(title) +
              (block.locked ? '<span class="lock-badge">锁定</span>' : '') +
              '</div><div class="plan-block-meta">' +
              escapeHTML(PLAN_BLOCK_KIND_LABELS[block.kind] || block.kind) +
              (duration ? ' · ' + escapeHTML(hoursShort(duration)) + 'h' : '') +
              (workItem ? ' · ' + escapeHTML(workItem.project) : '') +
              ' · ' +
              escapeHTML(PLAN_BLOCK_STATUS_LABELS[block.status] || block.status) +
              actualHtml +
              '</div></div><div class="plan-block-actions"><button class="btn subtle" type="button" data-plan-block-edit="' +
              escapeHTML(block.id) +
              '">编辑</button><button class="btn subtle" type="button" data-plan-block-delete="' +
              escapeHTML(block.id) +
              '">删除</button></div></article>'
            );
          })
          .join('')
      : '<div class="empty"><strong>今天还没有时间块</strong><ul><li>从左侧工作项点「排入」，或点右上角「添加时间块」。</li></ul></div>';
    bindPlan();
  }

  function blockMinutes(block) {
    const start = text(block.start).split(':').map(Number);
    const end = text(block.end).split(':').map(Number);
    if (start.length !== 2 || end.length !== 2 || start.some(isNaN) || end.some(isNaN)) return 0;
    return end[0] * 60 + end[1] - (start[0] * 60 + start[1]);
  }

  function bindPlan() {
    const node = el('plan-view');
    if (!node) return;
    node.querySelectorAll('[data-plan-schedule]').forEach((button) => {
      button.addEventListener('click', () => {
        const item = (state.workItemCache || []).find((x) => text(x.id) === button.getAttribute('data-plan-schedule'));
        if (item) openBlockModal(null, item);
      });
    });
    node.querySelectorAll('[data-plan-item-edit]').forEach((button) => {
      button.addEventListener('click', () => {
        const item = (state.workItemCache || []).find((x) => text(x.id) === button.getAttribute('data-plan-item-edit'));
        if (item) openItemModal(item);
      });
    });
    node.querySelectorAll('[data-plan-block-done]').forEach((button) => {
      button.addEventListener('click', () => togglePlanBlockDone(button.getAttribute('data-plan-block-done')));
    });
    node.querySelectorAll('[data-plan-block-edit]').forEach((button) => {
      button.addEventListener('click', () => {
        const id = button.getAttribute('data-plan-block-edit');
        const block = (state.plan.blocks || []).find((x) => text(x.id) === id);
        if (block) openBlockModal(block, null);
      });
    });
    node.querySelectorAll('[data-plan-block-delete]').forEach((button) => {
      button.addEventListener('click', () => deletePlanBlock(button.getAttribute('data-plan-block-delete')));
    });
  }

  async function autoPlanNow() {
    const button = el('plan-auto');
    if (button) button.disabled = true;
    try {
      const result = await bridgeInvoke('work-log.plan.auto', { date: ensurePlanDate() });
      const scheduled = Number(result && result.scheduled_minutes) || 0;
      const unscheduled = Number(result && result.unscheduled_minutes) || 0;
      await loadPlan(state.planDate);
      notify(
        '智能排期完成：已排 ' +
          (hoursShort(scheduled) || '0') +
          'h' +
          (unscheduled > 0 ? '，还有 ' + (hoursShort(unscheduled) || '0') + 'h 未排入' : '') +
          '。',
      );
    } catch (error) {
      notify('智能排期失败：' + String((error && error.message) || error), 'error');
    } finally {
      if (button) button.disabled = false;
    }
  }
  async function savePlan() {
    if (!state.plan) return;
    try {
      const result = await bridgeInvoke('work-log.plan.save', { plan: state.plan });
      state.plan = (result && result.plan) || state.plan;
      notify('计划已保存。');
      renderPlan();
    } catch (error) {
      notify('保存计划失败：' + String((error && error.message) || error), 'error');
    }
  }

  function togglePlanBlockDone(id) {
    if (!state.plan) return;
    state.plan.blocks = (state.plan.blocks || []).map((block) =>
      text(block.id) === id ? Object.assign({}, block, { status: block.status === 'done' ? 'planned' : 'done' }) : block,
    );
    savePlan();
  }

  function deletePlanBlock(id) {
    if (!state.plan) return;
    state.plan.blocks = (state.plan.blocks || []).filter((block) => text(block.id) !== id);
    savePlan();
  }

  function planDateShift(days) {
    const d = parseDate(ensurePlanDate()) || new Date();
    d.setDate(d.getDate() + days);
    loadPlan(formatDate(d));
  }

  function openItemModal(item) {
    const list = state.projects && state.projects.length ? state.projects : boardProjects();
    const select = el('i-project');
    if (select) {
      select.innerHTML = list
        .map((p) => '<option value="' + escapeHTML(p.name) + '">' + escapeHTML(p.name) + '</option>')
        .join('');
    }
    state.workItemEditing = item || null;
    el('i-id').value = item ? text(item.id) : '';
    el('i-title').value = item ? text(item.title) : '';
    el('i-feature').value = item ? text(item.feature) : '';
    el('i-priority').value = item ? text(item.priority, 'P2') : 'P2';
    el('i-status').value = item ? text(item.status, 'todo') : 'todo';
    el('i-estimate').value = item ? Number(item.estimate_minutes) || 0 : 60;
    el('i-remaining').value = item ? Number(item.remaining_minutes) || 0 : 60;
    el('i-due').value = item ? text(item.due_date) : '';
    el('i-notes').value = item ? text(item.notes) : '';
    if (select && item && text(item.project)) select.value = text(item.project);
    el('item-title').textContent = item ? '编辑工作项' : '新增工作项';
    el('item-backdrop').hidden = false;
    setTimeout(() => el('i-title').focus(), 30);
  }

  function closeItemModal() {
    el('item-backdrop').hidden = true;
    state.workItemEditing = null;
  }

  async function submitItemForm(event) {
    event.preventDefault();
    const project = el('i-project').value;
    if (!project) {
      notify('请先选择项目。', 'warn');
      return;
    }
    const payload = {
      project: project,
      title: el('i-title').value,
      feature: el('i-feature').value,
      priority: el('i-priority').value,
      status: el('i-status').value,
      estimate_minutes: Number(el('i-estimate').value) || 0,
      remaining_minutes: Number(el('i-remaining').value) || 0,
      due_date: el('i-due').value,
      notes: el('i-notes').value,
    };
    try {
      if (state.workItemEditing) {
        await bridgeInvoke('work-log.item.update', {
          work_item_id: state.workItemEditing.id,
          patch: payload,
        });
      } else {
        await bridgeInvoke('work-log.item.upsert', { work_item: payload });
      }
      closeItemModal();
      notify('工作项已保存。');
      await loadPlan(state.planDate);
    } catch (error) {
      notify('保存工作项失败：' + String((error && error.message) || error), 'error');
    }
  }

  function openBlockModal(block, item) {
    state.planEditingBlock = block || null;
    const select = el('b-item');
    if (select) {
      select.innerHTML =
        '<option value="">不关联</option>' +
        (state.workItemCache || [])
          .map((x) => '<option value="' + escapeHTML(x.id) + '">' + escapeHTML(x.title) + ' · ' + escapeHTML(x.project) + '</option>')
          .join('');
    }
    const blocks = (state.plan && state.plan.blocks) || [];
    const last = blocks.length ? blocks[blocks.length - 1] : null;
    el('b-id').value = block ? text(block.id) : '';
    el('b-kind').value = block ? text(block.kind, 'focus') : 'focus';
    el('b-title').value = block ? text(block.title) : '';
    el('b-start').value = block ? text(block.start, '09:30') : text(last && last.end, '09:30');
    el('b-end').value = block ? text(block.end, '11:00') : '11:00';
    el('b-locked').checked = block ? !!block.locked : false;
    if (select) select.value = block ? text(block.work_item_id) : item ? text(item.id) : '';
    if (item && !block) {
      el('b-title').value = text(item.title);
      const start = el('b-start').value || '09:30';
      const startParts = start.split(':').map(Number);
      if (startParts.length === 2) {
        const total = Math.min(23 * 60 + 59, startParts[0] * 60 + startParts[1] + Math.min(90, Number(item.remaining_minutes) || 90));
        el('b-end').value = String(Math.floor(total / 60)).padStart(2, '0') + ':' + String(total % 60).padStart(2, '0');
      }
    }
    el('block-title').textContent = block ? '编辑时间块' : '添加时间块';
    el('block-backdrop').hidden = false;
    setTimeout(() => el('b-start').focus(), 30);
  }

  function closeBlockModal() {
    el('block-backdrop').hidden = true;
    state.planEditingBlock = null;
  }

  async function submitBlockForm(event) {
    event.preventDefault();
    if (!state.plan) return;
    const block = Object.assign({}, state.planEditingBlock || {}, {
      id: el('b-id').value || '',
      kind: el('b-kind').value,
      work_item_id: el('b-item').value,
      title: el('b-title').value,
      start: el('b-start').value,
      end: el('b-end').value,
      status: state.planEditingBlock ? text(state.planEditingBlock.status, 'planned') : 'planned',
      locked: el('b-locked').checked,
      source: 'manual',
      rationale: state.planEditingBlock ? text(state.planEditingBlock.rationale) : '',
    });
    if (state.planEditingBlock) {
      state.plan.blocks = (state.plan.blocks || []).map((item) => (text(item.id) === text(block.id) ? block : item));
    } else {
      state.plan.blocks = (state.plan.blocks || []).concat([block]);
    }
    closeBlockModal();
    await savePlan();
  }  // ---------- 项目 ----------

  function renderProjectsMode() {
    const node = el('projects-mode');
    if (!node) return;
    node.querySelectorAll('.seg-btn').forEach((button) => {
      const on = button.getAttribute('data-mode') === state.projectsMode;
      button.classList.toggle('active', on);
      button.setAttribute('aria-pressed', on ? 'true' : 'false');
    });
    el('projects-progress').hidden = state.projectsMode !== 'progress';
    el('projects-panel').hidden = state.projectsMode !== 'registry';
  }

  function renderProjectList() {
    const node = el('proj-list');
    if (!node) return;
    const query = text(state.query).toLowerCase();
    const list = (state.projects || []).filter((item) => !query || text(item.name).toLowerCase().indexOf(query) >= 0);
    if (!list.length) {
      node.innerHTML = '<p class="meta pad">还没有项目。写入日志时会按项目名或仓库目录自动建项目。</p>';
      return;
    }
    node.innerHTML = list
      .map((item) => {
        const active = text(item.name) === state.activeProject;
        const range = text(item.first_date) ? escapeHTML(item.last_date) + ' 止' : '暂无记录';
        return (
          '<button type="button" class="proj-item' +
          (active ? ' active' : '') +
          '" data-name="' +
          escapeHTML(item.name) +
          '"><span class="pi-name">' +
          escapeHTML(item.name) +
          '</span><span class="pi-meta">' +
          escapeHTML(item.entry_count || 0) +
          ' 条 · ' +
          escapeHTML(item.active_days || 0) +
          ' 天 · ' +
          range +
          '</span></button>'
        );
      })
      .join('');
    node.querySelectorAll('.proj-item').forEach((button) => {
      button.addEventListener('click', () => selectProject(button.getAttribute('data-name')));
    });
  }

  async function selectProject(name) {
    if (state.activeProject !== name) {
      state.projectExhibit = '';
      state.projectExhibitsExpanded = false;
    }
    state.activeProject = name;
    renderProjectList();
    const node = el('proj-progress');
    if (!name) {
      node.innerHTML = emptyBlock('选择一个项目', ['左侧点一个项目，查看它的推进记录。']);
      return;
    }
    state.projectLoading = true;
    node.innerHTML = skeleton();
    try {
      const data = await bridgeInvoke('work-log.entry.list', { project: name, limit: 5000 });
      state.projectEntries = (data && data.entries) || [];
    } catch (error) {
      state.projectEntries = [];
      notify('读取项目记录失败：' + String((error && error.message) || error), 'error');
    } finally {
      state.projectLoading = false;
    }
    renderProjectProgress();
  }

  function projectWorkPanel(proj) {
    if (!proj) return '';
    const stateValue = text(proj.state || (proj.work && proj.work.state), 'planned');
    const priority = text(proj.priority || (proj.work && proj.work.priority), 'P2');
    const goal = text(proj.goal || (proj.work && proj.work.goal));
    const target = text(proj.target_date || (proj.work && proj.work.target_date));
    const stage = text(proj.stage, 'unknown');
    const stageLabel = text(proj.stage_label, '未判断');
    const progress = Number(proj.progress) || 0;
    const progressSource = text(proj.progress_source, 'stage');
    const steps = (proj.next_steps || (proj.work && proj.work.next_steps) || []).slice(0, 6);
    const blockers = proj.blockers || (proj.work && proj.work.blockers) || [];
    const done = stateValue === 'done';
    const dormant = proj.is_other === true;
    const running = stateValue === 'in_progress' && proj.is_current === true;
    const stepHtml = steps.length
      ? steps
          .map(
            (step) =>
              '<label class="wb-step' + (step.done ? ' done' : '') + '"><input type="checkbox" data-pp-step="' +
              escapeHTML(step.id) +
              '"' +
              (step.done ? ' checked' : '') +
              ' /><span>' +
              escapeHTML(step.text) +
              (step.due ? '<em>@' + escapeHTML(step.due) + '</em>' : '') +
              '</span></label>',
          )
          .join('')
      : '<span class="wb-empty-line">还没有下一步</span>';
    return (
      '<section class="pp-work"><div class="pp-work-head">' +
      (running ? '<span class="wb-spinner" role="status" aria-label="正在推进" title="正在推进"></span>' : '') +
      '<span class="state-pill state-' +
      escapeHTML(dormant ? 'dormant' : stateValue) +
      '">' +
      escapeHTML(dormant ? '未活跃' : workStateLabel(stateValue)) +
      '</span>' +
      (done
        ? ''
        : '<span class="stage-pill stage-' +
          escapeHTML(stage) +
          '">' +
          escapeHTML(stageLabel) +
          '</span>') +
      '<span class="priority-pill p-' +
      escapeHTML(priority) +
      '">' +
      escapeHTML(priorityLabel(priority)) +
      '</span><span class="meta">' +
      (progressSource === 'manual' ? '手工进度 ' : done ? '' : '约 ') +
      escapeHTML(progress) +
      '%' +
      (progressSource === 'manual' ? '' : ' · 智能推断') +
      '</span>' +
      (target ? '<span class="meta">目标 ' + escapeHTML(target) + '</span>' : '') +
      (dormant ? '<span class="meta">超过 14 天未活跃</span>' : '') +
      (proj.stale ? '<span class="wb-warn">' + escapeHTML(proj.days_since_activity) + ' 天无日志</span>' : '') +
      '</div>' +
      (goal ? '<p class="pp-work-goal">' + escapeHTML(goal) + '</p>' : '') +
      '<div class="pp-work-next"><strong>下一步</strong>' +
      stepHtml +
      '</div>' +
      (blockers.length ? '<div class="pp-work-blockers"><strong>阻塞</strong>' + blockers.map((item) => '<span>' + escapeHTML(item) + '</span>').join('') + '</div>' : '') +
      (done && text(proj.completion_summary) ? '<p class="pp-work-summary">完成摘要：' + escapeHTML(proj.completion_summary) + '</p>' : '') +
      '<div class="wb-actions"><button class="btn subtle" type="button" data-pp-work-edit>编辑工作状态</button>' +
      (done ? '' : '<button class="btn subtle" type="button" data-pp-work-complete>标记完成</button>') +
      '</div></section>'
    );
  }
  function renderProjectProgress() {
    const node = el('proj-progress');
    if (!node) return;
    if (state.projectLoading) {
      node.innerHTML = skeleton();
      return;
    }
    const allEntries = state.projectEntries || [];
    if (!state.activeProject) {
      node.innerHTML = emptyBlock('选择一个项目', ['左侧点一个项目，查看它的推进记录。']);
      return;
    }
    if (!allEntries.length) {
      node.innerHTML = emptyBlock('这个项目还没有记录', ['写入日志时带上该项目名即可出现在这里。']);
      return;
    }

    const proj = (state.projects || []).find((p) => text(p.name) === state.activeProject) || null;
    const kind = proj ? text(proj.kind) : '';
    const exhibits = proj && Array.isArray(proj.exhibits) ? proj.exhibits : [];
    const exhibitIDByName = {};
    exhibits.forEach((ex) => {
      exhibitIDByName[text(ex.name)] = text(ex.external_id);
    });

    // 展项分布来自条目；未归类 = 条目没有展项。
    const tally = {};
    let unclassified = 0;
    allEntries.forEach((item) => {
      const name = text(item.exhibit);
      if (!name) {
        unclassified += 1;
        return;
      }
      if (!tally[name]) tally[name] = { count: 0, minutes: 0 };
      tally[name].count += 1;
      tally[name].minutes += Number(item.minutes) || 0;
    });
    const registered = new Set(exhibits.map((ex) => text(ex.name)));
    const withRecords = exhibits.filter((ex) => tally[text(ex.name)]);
    const unregistered = Object.keys(tally).filter((name) => !registered.has(name));
    const totalMinutes = allEntries.reduce((s, i) => s + (Number(i.minutes) || 0), 0);

    const NONE = '__none__'; // 「未归类」哨兵值
    const selRaw = text(state.projectExhibit);
    const entries =
      selRaw === NONE
        ? allEntries.filter((item) => !text(item.exhibit))
        : selRaw
          ? allEntries.filter((item) => text(item.exhibit) === selRaw)
          : allEntries;
    const flow = aggregateFlow(entries);

    const dateKeys = (list) =>
      Object.keys(
        list.reduce((acc, item) => {
          acc[item.date] = 1;
          return acc;
        }, {}),
      )
        .sort()
        .reverse();
    const statLine = (list) => {
      const ds = dateKeys(list);
      return {
        count: list.length,
        days: ds.length,
        mins: list.reduce((s, i) => s + (Number(i.minutes) || 0), 0),
        range: ds.length ? text(ds[ds.length - 1]) + ' ~ ' + text(ds[0]) : '暂无记录',
      };
    };
    const dates = dateKeys(entries);
    const activeDays = dates.length;

    // daySections 把一组条目按日期分节渲染（时间线内共用）。
    const daySections = (list) => {
      const grouped = {};
      list.forEach((item) => {
        if (!grouped[item.date]) grouped[item.date] = [];
        grouped[item.date].push(item);
      });
      return Object.keys(grouped)
        .sort()
        .reverse()
        .map((date) => {
          const dayEntries = grouped[date];
          const minutes = dayEntries.reduce((s, i) => s + (Number(i.minutes) || 0), 0);
          const cards = dayEntries.map((item) => entryCard(item, { hideProject: true })).join('');
          return (
            '<section class="pp-day"><div class="pp-day-head"><span class="pd-date">' +
            escapeHTML(date) +
            '</span><span class="pd-meta">' +
            escapeHTML(dayEntries.length) +
            ' 条' +
            (minutes > 0 ? ' · ' + escapeHTML(hoursShort(minutes)) + 'h' : '') +
            '</span></div>' +
            cards +
            '</section>'
          );
        })
        .join('');
    };

    const kindClass =
      kind === '交付项目' ? 'delivery' : kind === '平台产品' ? 'platform' : kind === '通用沉淀' ? 'tooling' : 'other';
    const selLabel = selRaw === NONE ? '未归类' : selRaw;
    const selID = selRaw && selRaw !== NONE ? exhibitIDByName[selRaw] : '';

    const head =
      '<div class="pp-head"><h2>' +
      escapeHTML(state.activeProject) +
      '</h2>' +
      '<div class="pp-stats">' +
      (kind ? '<span class="badge kind kind-' + kindClass + '">' + escapeHTML(kind) + '</span>' : '') +
      (selRaw
        ? '<span class="badge accent">展项 · ' +
          escapeHTML(selLabel) +
          (selID ? ' <span class="exhibit-id">' + escapeHTML(selID) + '</span>' : '') +
          '</span>'
        : '') +
      '<span class="badge">' +
      escapeHTML(flow.count) +
      ' 条</span><span class="badge">' +
      escapeHTML(activeDays) +
      ' 天</span>' +
      (flow.totalMinutes > 0 ? '<span class="badge">约 ' + escapeHTML(hoursShort(flow.totalMinutes)) + ' 小时</span>' : '') +
      '<span class="badge">' +
      escapeHTML(text(dates[dates.length - 1]) + ' ~ ' + text(dates[0])) +
      '</span>' +
      (selRaw ? '<button class="link-btn" type="button" data-exhibit-clear>返回全部</button>' : '') +
      '</div>' +
      categoryChips(flow.categories) +
      '</div>';

    // 展项过滤条 + 视图切换（仅交付项目有展项维度）。
    let toolbar = '';
    if (exhibits.length > 0) {
      const CAP = 14;
      const expanded = !!state.projectExhibitsExpanded;
      const named = withRecords
        .map((ex) => ({ name: text(ex.name), ext: text(ex.external_id), count: tally[text(ex.name)].count, minutes: tally[text(ex.name)].minutes }))
        .sort((a, b) => b.minutes - a.minutes || b.count - a.count || a.name.localeCompare(b.name, 'zh'));
      let visible = expanded ? named.slice() : named.slice(0, CAP);
      if (selRaw && selRaw !== NONE && !visible.some((x) => x.name === selRaw)) {
        const hit = named.find((x) => x.name === selRaw);
        visible = [
          hit || { name: selRaw, ext: exhibitIDByName[selRaw] || '', count: (tally[selRaw] || {}).count || 0, minutes: (tally[selRaw] || {}).minutes || 0 },
        ].concat(visible);
      }
      const chip = (value, label, count, minutes, active, ext) =>
        '<button class="pp-feat-chip' +
        (active ? ' active' : '') +
        '" type="button" data-exhibit="' +
        escapeHTML(value) +
        '" title="' +
        escapeHTML(label + (ext ? ' · ' + ext : '')) +
        '"><span class="pfc-name">' +
        escapeHTML(label) +
        '</span><b>' +
        (minutes > 0 ? escapeHTML(hoursShort(minutes)) + 'h' : escapeHTML(count) + ' 条') +
        '</b></button>';
      const chips =
        chip('', '全部', allEntries.length, totalMinutes, selRaw === '', '') +
        visible.map((x) => chip(x.name, x.name, x.count, x.minutes, selRaw === x.name, x.ext)).join('') +
        unregistered.map((name) => chip(name, name, tally[name].count, tally[name].minutes, selRaw === name, '')).join('') +
        (unclassified > 0 ? chip(NONE, '未归类', unclassified, 0, selRaw === NONE, '') : '') +
        (named.length > CAP
          ? '<button class="pp-feat-more" type="button" data-exhibits-toggle>' +
            (expanded ? '收起' : '展开全部 ' + named.length + ' 项') +
            '</button>'
          : '');
      const grp = (key, label) =>
        '<button class="pp-grp-btn' +
        (state.projectGroupBy === key ? ' active' : '') +
        '" type="button" data-group="' +
        key +
        '">' +
        label +
        '</button>';
      toolbar =
        '<div class="pp-toolbar">' +
        '<div class="pp-feats"><span class="pp-feats-label">展项</span><div class="pp-feat-chips' +
        (expanded ? ' expanded' : '') +
        '">' +
        chips +
        '</div></div>' +
        '<div class="pp-group"><span class="pp-group-label">视图</span>' +
        grp('time', '按时间') +
        grp('exhibit', '按展项') +
        '</div></div>';
    } else if (kind === '平台产品' || kind === '通用沉淀') {
      toolbar =
        '<div class="pp-toolbar"><div class="pp-feats"><span class="pp-feats-label">维度</span>' +
        '<span class="pp-note">' +
        escapeHTML(kind) +
        '：单一归属，无展项，按工作内容（功能）与时间推进。</span></div></div>';
    }

    let body;
    if (exhibits.length > 0 && state.projectGroupBy === 'exhibit') {
      const groups = {};
      const order = [];
      entries.forEach((item) => {
        const name = text(item.exhibit, '未归类');
        if (!groups[name]) {
          groups[name] = { name: name, entries: [], count: 0, minutes: 0 };
          order.push(name);
        }
        const g = groups[name];
        g.entries.push(item);
        g.count += 1;
        g.minutes += Number(item.minutes) || 0;
      });
      const namedOrder = order
        .filter((name) => name !== '未归类')
        .sort((a, b) => groups[b].minutes - groups[a].minutes || groups[b].count - groups[a].count);
      const finalOrder = groups['未归类'] ? namedOrder.concat(['未归类']) : namedOrder;
      body =
        '<div class="pp-exhibits">' +
        finalOrder
          .map((name) => {
            const g = groups[name];
            const st = statLine(g.entries);
            const ext = exhibitIDByName[name];
            return (
              '<section class="pp-exhibit"><div class="pp-exhibit-head"><span class="pp-exhibit-name">' +
              escapeHTML(name) +
              (ext ? ' <span class="exhibit-id">' + escapeHTML(ext) + '</span>' : '') +
              '</span><span class="pp-exhibit-meta">' +
              escapeHTML(st.count) +
              ' 条 · ' +
              escapeHTML(st.days) +
              ' 天' +
              (st.mins > 0 ? ' · 约 ' + escapeHTML(hoursShort(st.mins)) + ' 小时' : '') +
              ' · ' +
              escapeHTML(st.range) +
              '</span></div><div class="pp-timeline">' +
              daySections(g.entries) +
              '</div></section>'
            );
          })
          .join('') +
        '</div>';
    } else {
      body = '<div class="pp-timeline">' + daySections(entries) + '</div>';
    }

    node.innerHTML = head + projectWorkPanel(proj) + toolbar + body;
    bindEntryCards(node);
    node.querySelectorAll("[data-pp-work-edit]").forEach((button) => {
      button.addEventListener("click", () => openWorkModal(proj, false));
    });
    node.querySelectorAll("[data-pp-work-complete]").forEach((button) => {
      button.addEventListener("click", () => openWorkModal(proj, true));
    });
    node.querySelectorAll("[data-pp-step]").forEach((checkbox) => {
      checkbox.addEventListener("change", () => {
        const stepID = checkbox.getAttribute("data-pp-step");
        const steps = (proj.next_steps || []).map((step) =>
          text(step.id) === stepID ? Object.assign({}, step, { done: checkbox.checked }) : step,
        );
        updateProjectWork(proj.id, { next_steps: steps });
      });
    });
    node.querySelectorAll('[data-exhibit]').forEach((btn) => {
      btn.addEventListener('click', () => {
        state.projectExhibit = btn.getAttribute('data-exhibit') || '';
        renderProjectProgress();
      });
    });
    node.querySelectorAll('[data-exhibit-clear]').forEach((btn) => {
      btn.addEventListener('click', () => {
        state.projectExhibit = '';
        renderProjectProgress();
      });
    });
    node.querySelectorAll('[data-exhibits-toggle]').forEach((btn) => {
      btn.addEventListener('click', () => {
        state.projectExhibitsExpanded = !state.projectExhibitsExpanded;
        renderProjectProgress();
      });
    });
    node.querySelectorAll('[data-group]').forEach((btn) => {
      btn.addEventListener('click', () => {
        state.projectGroupBy = btn.getAttribute('data-group') || 'time';
        renderProjectProgress();
      });
    });
    node.querySelectorAll('.ref').forEach((ref) => {
      ref.addEventListener('click', () => {
        const url = ref.getAttribute('data-url');
        openLink(url).then((opened) => {
          if (!opened) copyText(url, '宿主未能打开链接，已复制引用地址');
        });
      });
    });
  }

  // pathTags 渲染一组路径映射为可移除的标签。
  function pathTags(paths, projectId, exhibitName) {
    const items = Array.isArray(paths) ? paths : [];
    if (!items.length) return '<span class="path-empty">未绑定</span>';
    return items
      .map((p) => {
        const kind = text(p && p.kind);
        const value = text(p && p.path);
        return (
          '<span class="path-tag" title="' + escapeHTML(value) + '">' +
          (kind ? '<em>' + escapeHTML(kind) + '</em>' : '') +
          '<span class="path-text">' + escapeHTML(value) + '</span>' +
          '<button class="path-remove" type="button" data-action="rm-path" data-id="' + escapeHTML(projectId) +
          '" data-exhibit="' + escapeHTML(text(exhibitName)) + '" data-path="' + escapeHTML(value) +
          '" aria-label="移除路径">×</button>' +
          '</span>'
        );
      })
      .join('');
  }

  function renderProjectsPanel() {
    const panel = el('projects-panel');
    if (!panel) return;
    const data = state.projectsData || {};
    const projects = data.projects || [];
    const unmatched = data.unmatched || [];

    const form = state.registryNewOpen
      ? '<div class="proj-form">' +
        '<label class="field"><span>项目名</span><input id="p-name" type="text" placeholder="如：深圳科技馆" /></label>' +
        '<label class="field"><span>仓库 / 目录路径（可选）</span><input id="p-paths" type="text" placeholder="如：F:/work/szkjg" /></label>' +
        '<button id="p-save" class="btn primary" type="button">创建项目</button>' +
        '<button id="p-cancel" class="btn" type="button">取消</button>' +
        '</div>'
      : '';

    const filters = [
      { key: 'all', label: '全部' },
      { key: 'used', label: '有记录' },
      { key: 'empty', label: '无记录' },
      { key: 'nopath', label: '未绑路径' },
      { key: 'workbench', label: '看板' },
      { key: 'archived', label: '已归档' },
    ];
    const sorts = [
      { key: 'count', label: '条目多→少' },
      { key: 'name', label: '名称' },
      { key: 'recent', label: '最近更新' },
      { key: 'paths', label: '路径多→少' },
    ];
    const q = text(state.registryQuery).toLowerCase();

    function projectMatches(item) {
      const status = text(item.status, 'active');
      switch (state.registryFilter) {
        case 'used':
          return Number(item.entry_count || 0) > 0;
        case 'empty':
          return Number(item.entry_count || 0) === 0;
        case 'nopath':
          return !(item.paths || []).length && !(item.exhibits || []).some((ex) => ((ex && ex.paths) || []).length);
        case 'workbench':
          return text(item.source) === 'workbench';
        case 'archived':
          return status === 'archived';
        default:
          return status !== 'archived';
      }
    }
    function matchesQuery(item) {
      if (!q) return true;
      const hay = [
        item.name,
        (item.paths || []).map((p) => text(p && p.path)).join(' '),
        item.external_id,
      ]
        .join(' ')
        .toLowerCase();
      return hay.indexOf(q) >= 0;
    }

    const visible = projects.filter((item) => matchesQuery(item) && projectMatches(item));
    const sortKey = state.registrySort;
    visible.sort((a, b) => {
      if (sortKey === 'name') return String(a.name).localeCompare(String(b.name), 'zh-Hans-CN');
      if (sortKey === 'recent') return String(b.last_date || '').localeCompare(String(a.last_date || ''));
      if (sortKey === 'paths') {
        return (b.paths || []).length + (b.exhibits || []).length - ((a.paths || []).length + (a.exhibits || []).length);
      }
      return Number(b.entry_count || 0) - Number(a.entry_count || 0);
    });

    const chips = filters
      .map((f) => {
        const on = state.registryFilter === f.key ? ' active' : '';
        return '<button class="chip' + on + '" type="button" data-filter="' + f.key + '">' + escapeHTML(f.label) + '</button>';
      })
      .join('');
    const sortOptions = sorts
      .map((s) => '<option value="' + s.key + '"' + (state.registrySort === s.key ? ' selected' : '') + '>' + escapeHTML(s.label) + '</option>')
      .join('');
    const selectedIds = Object.keys(state.registrySelected).filter((k) => state.registrySelected[k]);
    const zeroCount = projects.filter((item) => Number(item.entry_count || 0) === 0).length;
    const toolbar =
      '<div class="proj-tools">' +
      '<input id="p-search" class="proj-search" type="search" placeholder="搜索项目名 / 路径" value="' +
      escapeHTML(state.registryQuery) +
      '" />' +
      '<div class="proj-chips">' +
      chips +
      '</div>' +
      '<label class="proj-sort"><span>排序</span><select id="p-sort">' +
      sortOptions +
      '</select></label>' +
      '</div>' +
      '<div class="proj-bulk">' +
      '<span class="meta">共 ' +
      escapeHTML(projects.length) +
      ' 个 · 当前 ' +
      escapeHTML(visible.length) +
      ' 个' +
      (zeroCount ? ' · 无记录 ' + escapeHTML(zeroCount) + ' 个' : '') +
      '</span>' +
      '<button class="btn tiny" type="button" data-action="autofill">自动填充路径</button>' +
      '<button class="btn tiny" type="button" data-action="clean-empty"' +
      (zeroCount ? '' : ' disabled') +
      '>清理无记录</button>' +
      '<button class="btn tiny" type="button" data-action="io-toggle">导出 / 导入</button>' +
      '<button class="btn tiny primary" type="button" data-action="new-toggle">' +
      (state.registryNewOpen ? '收起新建' : '+ 新建项目') +
      '</button>' +
      '</div>' +
      (selectedIds.length
        ? '<div class="proj-selbar"><span>已选 ' +
          escapeHTML(selectedIds.length) +
          ' 个</span>' +
          '<button class="btn tiny" type="button" data-action="bulk-archive">批量归档</button>' +
          '<button class="btn tiny" type="button" data-action="bulk-restore">恢复</button>' +
          '<button class="btn tiny danger" type="button" data-action="bulk-delete">批量删除</button>' +
          '<button class="btn tiny" type="button" data-action="clear-sel">取消选择</button></div>'
        : '') +
      (state.registryIOOpen
        ? '<div class="proj-io"><textarea id="p-io-text" placeholder="导出的登记表 JSON 会出现在这里；也可粘贴 JSON 后点「合并导入 / 替换导入」">' +
          escapeHTML(state.registryIOText) +
          '</textarea>' +
          '<div class="proj-io-actions"><button class="btn tiny" type="button" data-action="io-export">导出到此框</button>' +
          '<button class="btn tiny" type="button" data-action="io-copy">复制</button>' +
          '<button class="btn tiny" type="button" data-action="io-merge">合并导入</button>' +
          '<button class="btn tiny danger" type="button" data-action="io-replace">替换导入</button></div></div>'
        : '');

    const targets = projects
      .filter((item) => text(item.status, 'active') !== 'archived')
      .map((item) => '<option value="' + escapeHTML(item.id) + '">' + escapeHTML(item.name) + '</option>')
      .join('');

    // —— 分页 ——
    const pageSize = Number(state.registryPageSize) || 12;
    const totalPages = Math.max(1, Math.ceil(visible.length / pageSize));
    const page = Math.max(1, Math.min(Number(state.registryPage) || 1, totalPages));
    state.registryPage = page;
    const pageItems = visible.slice((page - 1) * pageSize, page * pageSize);

    function pathSummary(item) {
      const paths = item.paths || [];
      if (!paths.length) return '<span class="reg-muted">—</span>';
      const first = text(paths[0] && paths[0].path);
      const extra = paths.length > 1 ? '<span class="reg-plus">+' + (paths.length - 1) + '</span>' : '';
      return '<span class="reg-path" title="' + escapeHTML(first) + '">' + escapeHTML(first) + '</span>' + extra;
    }

    const rows = pageItems
      .map((item) => {
        const exhibits = item.exhibits || [];
        const archived = text(item.status, 'active') === 'archived';
        const src = text(item.source) === 'workbench' ? '<span class="badge new">看板</span>' : '';
        const statusBadge = archived ? '<span class="badge arch">已归档</span>' : '';
        const projectKind = text(item.kind);
        const kindBadge = projectKind
          ? '<span class="badge kind kind-' +
            (projectKind === '交付项目' ? 'delivery' : projectKind === '平台产品' ? 'platform' : projectKind === '通用沉淀' ? 'tooling' : 'other') +
            '">' +
            escapeHTML(projectKind) +
            '</span>'
          : '';
        const hasPath = (item.paths || []).length > 0;
        const pathHint = hasPath ? '' : '<span class="badge warn">未绑路径</span>';
        const checked = state.registrySelected[item.id] ? ' checked' : '';
        const expanded = !!state.registryExpanded[item.id];
        const editing = state.registryEditingName === item.id;
        const nameNode = editing
          ? '<input id="p-rename" class="proj-name-input" type="text" data-id="' +
            escapeHTML(item.id) +
            '" value="' +
            escapeHTML(item.name) +
            '" aria-label="项目名" />'
          : '<button class="proj-name" type="button" data-action="rename" data-id="' +
            escapeHTML(item.id) +
            '" title="点击修改项目名">' +
            escapeHTML(item.name) +
            '</button>';
        const attrs =
          ' data-id="' + escapeHTML(item.id) + '" data-name="' + escapeHTML(item.name) +
          '" data-status="' + (archived ? 'archived' : 'active') + '"';

        const mainRow =
          '<tr class="proj-row' + (archived ? ' archived' : '') + '"' + attrs + '>' +
          '<td class="reg-pick"><input type="checkbox" class="proj-check" data-id="' + escapeHTML(item.id) + '"' + checked + ' /></td>' +
          '<td class="reg-name"><div class="reg-name-row">' + nameNode + kindBadge + src + statusBadge + pathHint + '</div></td>' +
          '<td class="reg-num">' + escapeHTML(item.entry_count || 0) + '</td>' +
          '<td class="reg-num">' + escapeHTML(item.active_days || 0) + '</td>' +
          '<td class="reg-cell">' + pathSummary(item) + '</td>' +
          '<td class="reg-num">' + escapeHTML(exhibits.length || 0) + '</td>' +
          '<td class="reg-date">' + escapeHTML(text(item.last_date) || '—') + '</td>' +
          '<td class="reg-exp"><button class="reg-toggle" type="button" data-action="toggle-row" data-id="' +
          escapeHTML(item.id) + '" aria-expanded="' + (expanded ? 'true' : 'false') + '" title="展开/收起">' +
          (expanded ? '▾' : '▸') + '</button></td>' +
          '</tr>';

        if (!expanded) return mainRow;

        const range = text(item.first_date)
          ? escapeHTML(item.first_date) + ' ~ ' + escapeHTML(text(item.last_date))
          : '暂无记录';
        const exhibitRows = exhibits
          .map((ex) => {
            const exName = text(ex && ex.name);
            return (
              '<div class="proj-exhibit">' +
              '<span class="ex-name">' + escapeHTML(exName) + '</span>' +
              '<span class="ex-paths">' + pathTags(ex && ex.paths, item.id, exName) + '</span>' +
              '<span class="ex-add">' +
              '<input class="proj-ex-path-input" type="text" placeholder="展项目录路径" data-id="' + escapeHTML(item.id) +
              '" data-exhibit="' + escapeHTML(exName) + '" />' +
              '<button class="btn tiny" type="button" data-action="add-ex-path">加</button>' +
              '</span></div>'
            );
          })
          .join('');

        const detailRow =
          '<tr class="proj-row reg-detail' + (archived ? ' archived' : '') + '"' + attrs + '>' +
          '<td colspan="8"><div class="reg-detail-inner">' +
          '<div class="reg-detail-row"><span class="paths-label">路径</span>' +
          pathTags(item.paths, item.id, '') +
          '<span class="path-add">' +
          '<input class="proj-path-input" type="text" placeholder="加目录路径" data-id="' + escapeHTML(item.id) + '" />' +
          '<button class="btn tiny" type="button" data-action="add-path">加路径</button>' +
          '</span></div>' +
          '<div class="reg-detail-row"><span class="paths-label">展项</span>' +
          (exhibits.length
            ? '<div class="reg-exhibits">' + exhibitRows + '</div>'
            : '<span class="reg-muted">暂无展项（写入日志时带「展项」会自动登记）</span>') +
          '</div>' +
          '<div class="reg-detail-row reg-detail-actions">' +
          '<span class="proj-meta">' + escapeHTML(item.entry_count || 0) + ' 条 · ' + escapeHTML(item.active_days || 0) + ' 天 · ' + range + '</span>' +
          '<span class="reg-spacer"></span>' +
          '<select class="proj-merge-target"><option value="">并入到…</option>' + (targets || '') + '</select>' +
          '<button class="btn tiny" type="button" data-action="merge">并入</button>' +
          '<button class="btn tiny" type="button" data-action="archive">' + (archived ? '恢复' : '归档') + '</button>' +
          '<button class="btn tiny danger" type="button" data-action="delete">删除</button>' +
          '</div></div></td></tr>';

        return mainRow + detailRow;
      })
      .join('');

    // —— 分页控件 ——
    const pageNums = [];
    for (let n = 1; n <= totalPages; n++) {
      if (n === 1 || n === totalPages || Math.abs(n - page) <= 2) {
        if (pageNums.length && n - pageNums[pageNums.length - 1] > 1) pageNums.push('…');
        pageNums.push(n);
      }
    }
    const pagerBtns = pageNums
      .map((n) =>
        typeof n === 'number'
          ? '<button class="reg-page' + (n === page ? ' active' : '') + '" type="button" data-page="' + n + '">' + n + '</button>'
          : '<span class="reg-page-gap">…</span>',
      )
      .join('');
    const pageSizeOpts = [12, 24, 48, 0]
      .map((n) => '<option value="' + n + '"' + (pageSize === n ? ' selected' : '') + '>' + (n ? n : '全部') + '</option>')
      .join('');

    const list = visible.length
      ? '<div class="reg-table-wrap"><table class="reg-table"><thead><tr>' +
        '<th class="reg-pick"></th><th>项目</th><th class="reg-num">条目</th><th class="reg-num">天数</th><th>路径</th><th class="reg-num">展项</th><th class="reg-date">最近</th><th class="reg-exp"></th>' +
        '</tr></thead><tbody>' + rows + '</tbody></table></div>' +
        '<div class="reg-pager"><span class="meta">共 ' + escapeHTML(visible.length) + ' 个 · 第 ' + escapeHTML(page) + ' / ' + escapeHTML(totalPages) + ' 页</span>' +
        '<div class="reg-pager-btns"><button class="reg-page" type="button" data-page="' + (page - 1) + '"' + (page <= 1 ? ' disabled' : '') + '>‹</button>' +
        pagerBtns +
        '<button class="reg-page" type="button" data-page="' + (page + 1) + '"' + (page >= totalPages ? ' disabled' : '') + '>›</button></div>' +
        '<label class="reg-pagesize">每页 <select id="p-pagesize">' + pageSizeOpts + '</select> 个</label></div>'
      : projects.length
        ? emptyBlock('当前筛选没有项目', ['换个筛选条件，或点「全部」查看所有登记项目。'])
        : emptyBlock('还没有项目', ['写入日志时会按项目名或仓库目录自动建项目；也可以在上方手动添加。']);

    const unmatchedBlock = unmatched.length
      ? '<div class="proj-unmatched"><h3>未归类（登记表里没有）</h3><ul>' +
        unmatched
          .map((item) => '<li>' + escapeHTML(item.name) + ' · ' + escapeHTML(item.entry_count) + ' 条</li>')
          .join('') +
        '</ul><p class="meta">在上方登记同名项目，或用行内「并入」把历史条目归并到规范项目。</p></div>'
      : '';

    panel.innerHTML = toolbar + form + '<div class="proj-list">' + list + '</div>' + unmatchedBlock;
    bindProjectsPanel(panel);
  }

  async function refreshProjects() {
    await loadProjects();
    renderProjectsPanel();
    renderProjectList();
  }

  function bindProjectsPanel(panel) {
    const rerender = (focusId) => {
      renderProjectsPanel();
      if (focusId) {
        const node = el(focusId);
        if (node) {
          node.focus();
          if (node.setSelectionRange && typeof node.value === 'string') {
            const end = node.value.length;
            node.setSelectionRange(end, end);
          }
        }
      }
    };

    const searchInput = panel.querySelector('#p-search');
    if (searchInput) {
      searchInput.addEventListener('input', () => {
        state.registryQuery = searchInput.value || '';
        state.registryPage = 1;
        rerender('p-search');
      });
    }
    panel.querySelectorAll('[data-filter]').forEach((chip) => {
      chip.addEventListener('click', () => {
        state.registryFilter = chip.getAttribute('data-filter') || 'all';
        state.registryPage = 1;
        renderProjectsPanel();
      });
    });
    const sortSelect = panel.querySelector('#p-sort');
    if (sortSelect) {
      sortSelect.addEventListener('change', () => {
        state.registrySort = sortSelect.value || 'count';
        state.registryPage = 1;
        renderProjectsPanel();
      });
    }
    panel.querySelectorAll('.proj-check').forEach((box) => {
      box.addEventListener('change', () => {
        state.registrySelected[box.getAttribute('data-id')] = box.checked;
        if (!box.checked) delete state.registrySelected[box.getAttribute('data-id')];
        renderProjectsPanel();
      });
    });
    panel.querySelector('[data-action="clear-sel"]')?.addEventListener('click', () => {
      state.registrySelected = {};
      renderProjectsPanel();
    });
    panel.querySelector('[data-action="bulk-archive"]')?.addEventListener('click', async () => {
      await bulkSetStatus(panel, 'archived');
    });
    panel.querySelector('[data-action="bulk-restore"]')?.addEventListener('click', async () => {
      await bulkSetStatus(panel, 'active');
    });
    panel.querySelector('[data-action="bulk-delete"]')?.addEventListener('click', async (event) => {
      const btn = event.currentTarget;
      if (btn.getAttribute('data-armed') !== '1') {
        btn.setAttribute('data-armed', '1');
        btn.textContent = '确认删除';
        return;
      }
      const ids = selectedProjectIds();
      if (!ids.length) return;
      try {
        const result = await bridgeInvoke('work-log.project.delete', { project_ids: ids });
        notify('已删除 ' + text((result && result.count) || ids.length) + ' 个项目登记');
        state.registrySelected = {};
        await refreshProjects();
      } catch (error) {
        notify('批量删除失败：' + String((error && error.message) || error), 'error');
      }
    });
    panel.querySelector('[data-action="clean-empty"]')?.addEventListener('click', async (event) => {
      const btn = event.currentTarget;
      const empties = (state.projectsData && state.projectsData.projects ? state.projectsData.projects : []).filter(
        (item) => Number(item.entry_count || 0) === 0,
      );
      if (!empties.length) return;
      if (btn.getAttribute('data-armed') !== '1') {
        btn.setAttribute('data-armed', '1');
        btn.textContent = '确认清理 ' + empties.length + ' 个';
        setTimeout(() => {
          btn.setAttribute('data-armed', '0');
          btn.textContent = '清理无记录';
        }, 3000);
        return;
      }
      try {
        const result = await bridgeInvoke('work-log.project.delete', { project_ids: empties.map((item) => item.id) });
        notify('已清理 ' + text((result && result.count) || empties.length) + ' 个无记录项目');
        state.registrySelected = {};
        await refreshProjects();
      } catch (error) {
        notify('清理失败：' + String((error && error.message) || error), 'error');
      }
    });

    // 导出 / 导入。
    panel.querySelector('[data-action="io-toggle"]')?.addEventListener('click', () => {
      const box = panel.querySelector('#p-io-text');
      if (box) state.registryIOText = box.value || '';
      state.registryIOOpen = !state.registryIOOpen;
      renderProjectsPanel();
    });
    panel.querySelector('[data-action="io-export"]')?.addEventListener('click', async () => {
      try {
        const result = await bridgeInvoke('work-log.project.export', {});
        state.registryIOText = text(result && result.json);
        state.registryIOOpen = true;
        renderProjectsPanel();
        notify('已导出 ' + text((result && result.count) || 0) + ' 个项目到文本框，可复制保存');
      } catch (error) {
        notify('导出失败：' + String((error && error.message) || error), 'error');
      }
    });
    panel.querySelector('[data-action="io-copy"]')?.addEventListener('click', async () => {
      const box = panel.querySelector('#p-io-text');
      const value = (box && box.value) || state.registryIOText;
      if (!value) {
        notify('文本框为空，请先点「导出到此框」', 'warn');
        return;
      }
      state.registryIOText = value;
      try {
        await navigator.clipboard.writeText(value);
        notify('已复制到剪贴板');
      } catch (error) {
        if (box) {
          box.focus();
          box.select();
        }
        notify('请按 Ctrl+C 复制（浏览器未授权剪贴板）', 'warn');
      }
    });
    panel.querySelector('[data-action="io-merge"]')?.addEventListener('click', () => importRegistry(panel, 'merge'));
    panel.querySelector('[data-action="io-replace"]')?.addEventListener('click', async (event) => {
      const btn = event.currentTarget;
      if (btn.getAttribute('data-armed') !== '1') {
        btn.setAttribute('data-armed', '1');
        btn.textContent = '确认替换';
        setTimeout(() => {
          btn.setAttribute('data-armed', '0');
          btn.textContent = '替换导入';
        }, 3000);
        return;
      }
      await importRegistry(panel, 'replace');
    });

    function selectedProjectIds() {
      return Object.keys(state.registrySelected).filter((k) => state.registrySelected[k]);
    }

    async function bulkSetStatus(currentPanel, status) {
      const ids = selectedProjectIds();
      if (!ids.length) return;
      const byId = {};
      (state.projectsData && state.projectsData.projects ? state.projectsData.projects : []).forEach((item) => {
        byId[item.id] = item;
      });
      try {
        for (const id of ids) {
          const item = byId[id];
          if (!item) continue;
          await bridgeInvoke('work-log.project.upsert', { project_id: id, project_name: item.name, status });
        }
        notify((status === 'archived' ? '已归档 ' : '已恢复 ') + ids.length + ' 个项目');
        state.registrySelected = {};
        await refreshProjects();
      } catch (error) {
        notify('批量操作失败：' + String((error && error.message) || error), 'error');
      }
    }

    async function importRegistry(currentPanel, mode) {
      const box = currentPanel.querySelector('#p-io-text');
      const value = (box && box.value) || state.registryIOText;
      if (!text(value)) {
        notify('请先在文本框里粘贴登记表 JSON', 'warn');
        return;
      }
      try {
        const result = await bridgeInvoke('work-log.project.import', { projects_json: value, mode });
        if (mode === 'replace') {
          notify('已替换导入 ' + text((result && result.imported) || 0) + ' 个项目');
        } else {
          notify('已合并导入：新增 ' + text((result && result.created) || 0) + ' · 更新 ' + text((result && result.updated) || 0));
        }
        await refreshProjects();
      } catch (error) {
        notify('导入失败：' + String((error && error.message) || error), 'error');
      }
    }

    panel.querySelector('[data-action="new-toggle"]')?.addEventListener('click', () => {
      state.registryNewOpen = !state.registryNewOpen;
      renderProjectsPanel();
      if (state.registryNewOpen) {
        const input = el('p-name');
        if (input) input.focus();
      }
    });
    panel.querySelector('[data-action="autofill"]')?.addEventListener('click', async (event) => {
      const btn = event.currentTarget;
      btn.disabled = true;
      try {
        const result = await bridgeInvoke('work-log.project.autofill', {});
        const count = Number((result && result.filled_count) || 0);
        if (!count) {
          notify('没有可自动补全的路径：会话记录里没找到与登记项目匹配的目录', 'warn');
        } else {
          const sample = (result.filled || [])
            .map((f) => text(f && f.project) + ' → ' + text(f && f.path))
            .slice(0, 3)
            .join('；');
          notify('已自动补全 ' + count + ' 个项目路径：' + sample + (count > 3 ? ' 等' : ''));
        }
        await refreshProjects();
      } catch (error) {
        notify('自动填充失败：' + String((error && error.message) || error), 'error');
        btn.disabled = false;
      }
    });

    const save = panel.querySelector('#p-save');
    if (save) {
      save.addEventListener('click', async () => {
        const name = text(el('p-name').value);
        if (!name) {
          notify('请先填写项目名。', 'warn');
          el('p-name').focus();
          return;
        }
        const paths = splitList(el('p-paths').value).map((p) => ({ path: p, kind: 'repo' }));
        try {
          const payload = { project_name: name };
          if (paths.length) payload.paths = paths;
          await bridgeInvoke('work-log.project.upsert', payload);
          notify('已创建项目：' + name);
          state.registryNewOpen = false;
          await refreshProjects();
        } catch (error) {
          notify('创建项目失败：' + String((error && error.message) || error), 'error');
        }
      });
    }
    panel.querySelector('#p-cancel')?.addEventListener('click', () => {
      state.registryNewOpen = false;
      renderProjectsPanel();
    });

    // 名称就地编辑：点击名称 → 输入框，回车 / 失焦保存，Esc 取消。
    panel.querySelectorAll('[data-action="rename"]').forEach((btn) => {
      btn.addEventListener('click', () => {
        state.registryEditingName = btn.getAttribute('data-id') || '';
        renderProjectsPanel();
        const input = el('p-rename');
        if (input) {
          input.focus();
          if (input.select) input.select();
        }
      });
    });
    const renameInput = panel.querySelector('.proj-name-input');
    if (renameInput) {
      const editId = renameInput.getAttribute('data-id') || '';
      const source = (state.projectsData && state.projectsData.projects ? state.projectsData.projects : []).find(
        (item) => item.id === editId,
      );
      const oldName = text(source && source.name);
      let finished = false;
      const finish = async (commitRename) => {
        if (finished) return;
        finished = true;
        const value = text(renameInput.value);
        state.registryEditingName = '';
        if (!commitRename || !value || value === oldName) {
          renderProjectsPanel();
          return;
        }
        try {
          const result = await bridgeInvoke('work-log.project.rename', { project_id: editId, new_name: value });
          const rebound = Number((result && result.updated) || 0);
          notify('已改名为「' + value + '」' + (rebound ? '，回绑 ' + rebound + ' 条记录' : ''));
          await refreshProjects();
        } catch (error) {
          notify('改名失败：' + String((error && error.message) || error), 'error');
          await refreshProjects();
        }
      };
      renameInput.addEventListener('keydown', (event) => {
        if (event.key === 'Enter') {
          event.preventDefault();
          finish(true);
        } else if (event.key === 'Escape') {
          event.preventDefault();
          finish(false);
        }
      });
      renameInput.addEventListener('blur', () => finish(true));
    }

    panel.querySelectorAll('.proj-row').forEach((row) => {
      const id = row.getAttribute('data-id');
      const name = row.getAttribute('data-name');

      const addPathBtn = row.querySelector('[data-action="add-path"]');
      if (addPathBtn) {
        addPathBtn.addEventListener('click', async () => {
          const input = row.querySelector('.proj-path-input');
          const path = text(input ? input.value : '');
          if (!path) {
            notify('请先填写目录路径。', 'warn');
            return;
          }
          try {
            await bridgeInvoke('work-log.project.path.set', {
              project_id: id,
              add_paths: [{ path: path, kind: 'repo' }],
            });
            notify('已为「' + name + '」添加路径');
            await refreshProjects();
          } catch (error) {
            notify('添加路径失败：' + String((error && error.message) || error), 'error');
          }
        });
      }

      row.querySelectorAll('[data-action="add-ex-path"]').forEach((btn) => {
        btn.addEventListener('click', async () => {
          const input = btn.parentElement.querySelector('.proj-ex-path-input');
          const path = text(input ? input.value : '');
          const exhibit = input ? text(input.getAttribute('data-exhibit')) : '';
          if (!path) {
            notify('请先填写展项目录路径。', 'warn');
            return;
          }
          try {
            await bridgeInvoke('work-log.project.path.set', {
              project_id: id,
              exhibit_name: exhibit,
              add_paths: [{ path: path, kind: 'repo' }],
            });
            notify('已为展项「' + exhibit + '」添加路径');
            await refreshProjects();
          } catch (error) {
            notify('添加展项路径失败：' + String((error && error.message) || error), 'error');
          }
        });
      });

      const delBtn = row.querySelector('[data-action="delete"]');
      if (delBtn) {
        delBtn.addEventListener('click', async () => {
          if (delBtn.getAttribute('data-armed') !== '1') {
            delBtn.setAttribute('data-armed', '1');
            delBtn.textContent = '确认删除';
            setTimeout(() => {
              delBtn.setAttribute('data-armed', '0');
              delBtn.textContent = '删除';
            }, 3000);
            return;
          }
          try {
            await bridgeInvoke('work-log.project.delete', { project_id: id });
            notify('已删除项目登记：' + name + '（既有条目不受影响）');
            await refreshProjects();
          } catch (error) {
            notify('删除失败：' + String((error && error.message) || error), 'error');
          }
        });
      }

      const archiveBtn = row.querySelector('[data-action="archive"]');
      if (archiveBtn) {
        archiveBtn.addEventListener('click', async () => {
          const nextStatus = text(row.getAttribute('data-status')) === 'archived' ? 'active' : 'archived';
          try {
            await bridgeInvoke('work-log.project.upsert', { project_id: id, project_name: name, status: nextStatus });
            notify((nextStatus === 'archived' ? '已归档：' : '已恢复：') + name);
            await refreshProjects();
          } catch (error) {
            notify('操作失败：' + String((error && error.message) || error), 'error');
          }
        });
      }

      const mergeBtn = row.querySelector('[data-action="merge"]');
      if (mergeBtn) {
        mergeBtn.addEventListener('click', async () => {
          const select = row.querySelector('.proj-merge-target');
          const targetId = text(select ? select.value : '');
          if (!targetId) {
            notify('请先选择要并入的目标项目。', 'warn');
            return;
          }
          const targetName = text(select.options[select.selectedIndex].textContent);
          if (mergeBtn.getAttribute('data-armed') !== '1') {
            mergeBtn.setAttribute('data-armed', '1');
            mergeBtn.textContent = '确认并入';
            setTimeout(() => {
              mergeBtn.setAttribute('data-armed', '0');
              mergeBtn.textContent = '并入';
            }, 3000);
            return;
          }
          try {
            const result = await bridgeInvoke('work-log.project.merge', {
              from_project: name,
              to_project: targetName,
              project_id: targetId,
            });
            notify('已把「' + name + '」并入「' + targetName + '」：改写 ' + text((result && result.updated) || 0) + ' 条');
            await refreshProjects();
          } catch (error) {
            notify('合并失败：' + String((error && error.message) || error), 'error');
          }
        });
      }
    });

    // 展开 / 收起行明细。
    panel.querySelectorAll('[data-action="toggle-row"]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const id = btn.getAttribute('data-id') || '';
        if (state.registryExpanded[id]) delete state.registryExpanded[id];
        else state.registryExpanded[id] = true;
        renderProjectsPanel();
      });
    });

    // 分页：页码按钮 + 每页条数。
    panel.querySelectorAll('[data-page]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const n = Number(btn.getAttribute('data-page'));
        if (!n || n < 1) return;
        state.registryPage = n;
        renderProjectsPanel();
      });
    });
    const pageSizeSel = panel.querySelector('#p-pagesize');
    if (pageSizeSel) {
      pageSizeSel.addEventListener('change', () => {
        state.registryPageSize = Number(pageSizeSel.value) || 12;
        state.registryPage = 1;
        renderProjectsPanel();
      });
    }

    // 移除路径：项目级与展项级共用。
    panel.querySelectorAll('[data-action="rm-path"]').forEach((btn) => {
      btn.addEventListener('click', async () => {
        const id = btn.getAttribute('data-id');
        const exhibit = text(btn.getAttribute('data-exhibit'));
        const path = btn.getAttribute('data-path');
        try {
          const payload = { project_id: id, remove_paths: [path] };
          if (exhibit) payload.exhibit_name = exhibit;
          await bridgeInvoke('work-log.project.path.set', payload);
          notify('已移除路径');
          await refreshProjects();
        } catch (error) {
          notify('移除路径失败：' + String((error && error.message) || error), 'error');
        }
      });
    });
  }

  function renderProjectsView() {
    renderProjectsMode();
    renderProjectList();
    if (state.projectsMode === 'registry') {
      renderProjectsPanel();
    } else if (!state.activeProject && state.projects.length) {
      selectProject(text(state.projects[0].name));
    } else if (state.activeProject) {
      renderProjectProgress();
    }
  }

  // ---------- 数据加载 ----------

  async function loadCalendar(year, month) {
    const payload = { year: year, month: month };
    if (state.projectFilter) payload.project = state.projectFilter;
    const data = await bridgeInvoke('work-log.archive.calendar', payload);
    state.calendar = data;
    state.year = data.year;
    state.month = data.month;
    state.logRoot = text(data.log_root, state.logRoot);
    el('log-root').textContent = state.logRoot ? '日志目录 · ' + state.logRoot : '';
    el('log-root').title = state.logRoot;
    renderCalendar();
  }

  async function loadProjects() {
    try {
      const data = await bridgeInvoke('work-log.project.list', {});
      state.projectsData = data || {};
      state.projects = (data && data.projects) || [];
    } catch (error) {
      state.projectsData = { projects: [], unmatched: [], error: String((error && error.message) || error) };
      state.projects = [];
    }
    populateProjectOptions();
  }

  function populateProjectOptions() {
    const names = state.projects.map((item) => text(item.name)).filter(Boolean);

    const select = el('cal-project');
    if (select) {
      select.innerHTML =
        '<option value="">全部项目</option>' +
        names.map((name) => '<option value="' + escapeHTML(name) + '">' + escapeHTML(name) + '</option>').join('');
      select.value = names.indexOf(state.projectFilter) >= 0 ? state.projectFilter : '';
      if (select.value !== state.projectFilter) state.projectFilter = select.value;
    }

    const datalist = el('project-options');
    if (datalist) {
      datalist.innerHTML = names.map((name) => '<option value="' + escapeHTML(name) + '"></option>').join('');
    }

    const exhibits = [];
    state.projects.forEach((item) => {
      (item.exhibits || []).forEach((ex) => {
        const name = text(ex && ex.name);
        if (name && exhibits.indexOf(name) < 0) exhibits.push(name);
      });
    });
    const exList = el('exhibit-options');
    if (exList) {
      exList.innerHTML = exhibits.map((name) => '<option value="' + escapeHTML(name) + '"></option>').join('');
    }
  }

  function pickDefaultDate(calendar) {
    const inMonth = (date) => monthKeyOf(date) === calendar.month_key;
    if (inMonth(state.selected)) return state.selected;
    if (inMonth(calendar.today)) return calendar.today;
    const active = (calendar.days || []).filter((day) => day.entry_count > 0);
    if (active.length) return active[active.length - 1].date;
    return calendar.month_key + '-01';
  }

  async function selectDate(date) {
    state.selected = date || '';
    state.searchResults = null;
    state.entries = [];
    renderCalendar();
    if (state.selected) {
      try {
        const data = await bridgeInvoke('work-log.entry.read', { date: state.selected });
        state.entries = (data && data.entries) || [];
      } catch (error) {
        state.entries = [];
        notify('读取日志失败：' + String((error && error.message) || error), 'error');
      }
    }
    updateHead();
    renderPanel();
  }

  async function gotoMonth(year, month) {
    await loadCalendar(year, month);
    await selectDate(pickDefaultDate(state.calendar));
  }

  async function shiftMonth(delta) {
    let year = state.year;
    let month = state.month + delta;
    if (month < 1) {
      month = 12;
      year -= 1;
    } else if (month > 12) {
      month = 1;
      year += 1;
    }
    try {
      await gotoMonth(year, month);
    } catch (error) {
      notify('读取月历失败：' + String((error && error.message) || error), 'error');
    }
  }

  async function onCalendarClick(event) {
    const button = event.target.closest('.cal-cell');
    if (!button) return;
    const date = button.getAttribute('data-date');
    if (!date) return;
    try {
      if (monthKeyOf(date) !== (state.calendar && state.calendar.month_key)) {
        const parts = date.split('-').map(Number);
        await loadCalendar(parts[0], parts[1]);
      }
      await selectDate(date);
    } catch (error) {
      notify('读取日志失败：' + String((error && error.message) || error), 'error');
    }
  }

  async function applyProjectFilter() {
    const anchor = state.selected || today();
    const parts = anchor.split('-').map(Number);
    await loadCalendar(parts[0], parts[1]);
    await selectDate(pickDefaultDate(state.calendar));
  }

  // ---------- 录入弹窗 ----------

  let lastFocused = null;

  function openManualForm(entry) {
    state.editingEntry = entry || null;
    el('m-entry-id').value = entry ? text(entry.entry_id) : '';
    el('m-date').value = (entry && text(entry.date)) || state.selected || today();
    el('m-title').value = entry ? text(entry.title) : '';
    el('m-project').value = entry ? text(entry.project) : '';
    el('m-feature').value = entry ? text(entry.feature) : '';
    el('m-exhibit').value = entry ? text(entry.exhibit) : '';
    el('m-hours').value = entry && Number(entry.minutes) > 0 ? String((Number(entry.minutes) / 60).toFixed(2).replace(/\.?0+$/, '')) : '';
    el('m-category').value = (() => {
      const label = entry ? catLabel(entry.category) : '';
      return CATEGORY_OPTIONS.indexOf(label) >= 0 ? label : '开发';
    })();
    el('m-summary').value = entry ? text(entry.summary) : '';
    el('m-outcomes').value = entry ? (entry.outcomes || []).join('，') : '';
    el('m-next').value = entry ? (entry.next || []).join('，') : '';
    el('manual-title').textContent = entry ? '编辑一条工作日志' : '记录一条工作日志';
    el('manual-hint').textContent = entry
      ? '保存后按 entry_id 整条替换，可清空字段；日期不可改。'
      : '手工录入；AI 会话整理走“工作日志助手”技能或工作流。';
    el('m-save').textContent = saveLabel();
    // 编辑时锁定日期：条目按天分片存储，改日期会变成「在新的一天新增」，需要另做移动语义。
    el('m-date').disabled = !!entry;
    lastFocused = document.activeElement;
    el('manual-backdrop').hidden = false;
    document.body.classList.add('modal-open');
    el('m-title').focus();
  }

  function saveLabel() {
    return state.editingEntry ? '保存修改' : '保存到日志';
  }

  function closeManualForm() {
    if (el('manual-backdrop').hidden) return;
    state.editingEntry = null;
    el('m-date').disabled = false;
    el('m-save').textContent = saveLabel();
    el('manual-backdrop').hidden = true;
    document.body.classList.remove('modal-open');
    if (lastFocused && typeof lastFocused.focus === 'function') lastFocused.focus();
    lastFocused = null;
  }

  function trapFocus(event) {
    if (event.key !== 'Tab') return;
    const backdrop = ['maint-backdrop', 'manual-backdrop', 'work-backdrop', 'item-backdrop', 'block-backdrop'].map((id) => el(id)).find((node) => node && !node.hidden);
    if (!backdrop) return;
    const focusable = backdrop.querySelectorAll(
      'button, [href], input:not([disabled]), select, textarea, [tabindex]:not([tabindex="-1"])',
    );
    if (!focusable.length) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  // ---------- 维护弹窗：整库备份/恢复、条目导出/导入、重扫对账 ----------

  let maintRestoreArmed = false;

  function openMaintenance() {
    setMaintStatus('');
    el('maint-io').value = '';
    disarmRestore();
    lastFocused = document.activeElement;
    el('maint-backdrop').hidden = false;
    document.body.classList.add('modal-open');
    el('maint-backup').focus();
  }

  function closeMaintenance() {
    if (el('maint-backdrop').hidden) return;
    el('maint-backdrop').hidden = true;
    document.body.classList.remove('modal-open');
    disarmRestore();
    if (lastFocused && typeof lastFocused.focus === 'function') lastFocused.focus();
    lastFocused = null;
  }

  function setMaintStatus(message, tone) {
    const node = el('maint-status');
    if (!node) return;
    node.textContent = message || '';
    node.className = 'meta' + (tone ? ' ' + tone : '');
  }

  function disarmRestore() {
    maintRestoreArmed = false;
    const btn = el('maint-restore');
    if (!btn) return;
    btn.classList.remove('danger-armed');
    btn.textContent = '恢复最近备份';
  }

  async function backupArchive() {
    setMaintStatus('正在备份整库…');
    try {
      const result = await bridgeInvoke('work-log.archive.backup', {});
      setMaintStatus('已备份 ' + text(result && result.files) + ' 个文件：' + text(result && result.path));
      notify('整库备份完成。');
    } catch (error) {
      setMaintStatus('备份失败：' + String((error && error.message) || error), 'error');
    }
  }

  async function restoreArchive() {
    if (!maintRestoreArmed) {
      maintRestoreArmed = true;
      const btn = el('maint-restore');
      btn.classList.add('danger-armed');
      btn.textContent = '再次点击确认恢复';
      setMaintStatus('恢复会用最新备份覆盖当前日志库（恢复前会自动再留一份安全备份）。3 秒内再次点击确认。', 'warn');
      setTimeout(disarmRestore, 3000);
      return;
    }
    disarmRestore();
    setMaintStatus('正在恢复…');
    try {
      const result = await bridgeInvoke('work-log.archive.restore', {});
      setMaintStatus('已恢复 ' + text(result && result.files) + ' 个文件（来源：' + text(result && result.restored_from) + '）。');
      notify('已从备份恢复。');
      await refresh(state.selected);
    } catch (error) {
      setMaintStatus('恢复失败：' + String((error && error.message) || error), 'error');
    }
  }

  async function exportEntries() {
    const range = monthRange(state.selected || today());
    setMaintStatus('正在导出 ' + range.from + ' ~ ' + range.to + ' …');
    try {
      const result = await bridgeInvoke('work-log.entry.export', { from: range.from, to: range.to });
      el('maint-io').value = text(result && result.json);
      setMaintStatus('已导出 ' + text(result && result.count) + ' 条（' + range.from + ' ~ ' + range.to + '），可复制保存。');
    } catch (error) {
      setMaintStatus('导出失败：' + String((error && error.message) || error), 'error');
    }
  }

  async function importEntries() {
    const json = text(el('maint-io').value);
    if (!json) {
      setMaintStatus('请先把条目 JSON 粘贴到下方文本框。', 'warn');
      return;
    }
    setMaintStatus('正在导入（合并）…');
    try {
      const result = await bridgeInvoke('work-log.entry.import', { entries_json: json, mode: 'merge' });
      setMaintStatus(
        '已导入：新增 ' + text(result && result.added) + ' 条，更新 ' + text(result && result.updated) +
          ' 条，跳过锁定 ' + text(result && result.locked_skipped) + ' 条。',
      );
      notify('条目导入完成。');
      await refresh(state.selected);
    } catch (error) {
      setMaintStatus('导入失败：' + String((error && error.message) || error), 'error');
    }
  }

  async function reconcileEntries() {
    setMaintStatus('正在重扫对账…');
    try {
      const result = await bridgeInvoke('work-log.entry.reconcile', {});
      const lines = [
        '语料目录：' + text(result && result.material_dir),
        '语料会话 ' + text(result && result.material_sessions) + ' · 日志会话 ' + text(result && result.log_sessions) + ' · 命中 ' + text(result && result.matched),
        '未入账会话 ' + text(result && result.missing_count) + ' 条 · 过时条目 ' + text(result && result.stale_count) + ' 条',
      ];
      (result && result.missing ? result.missing.slice(0, 12) : []).forEach((item) => {
        lines.push('· 未入账 ' + text(item.date) + ' ' + text(item.source) + '/' + text(item.session_id));
      });
      el('maint-io').value = JSON.stringify(result, null, 2);
      setMaintStatus(lines.join('\n'));
    } catch (error) {
      setMaintStatus('对账失败：' + String((error && error.message) || error), 'error');
    }
  }

  function findEntryByID(entryID, date) {
    const pools = [state.entries, state.projectEntries, state.searchResults && state.searchResults.entries];
    for (const pool of pools) {
      if (!pool) continue;
      const found = pool.find((item) => text(item.entry_id) === entryID);
      if (found) return found;
    }
    return null;
  }

  function startEdit(entryID, date) {
    const entry = findEntryByID(entryID, date);
    if (!entry) {
      notify('未找到要编辑的条目，请刷新后重试。', 'warn');
      return;
    }
    if (date && !text(entry.date)) entry.date = date;
    openManualForm(entry);
  }

  // armDelete 用「二次点击确认」避免误删：首次点击把按钮变成确认态，3 秒内再点才真正删除。
  function armDelete(node) {
    if (node.getAttribute('data-armed') !== '1') {
      node.setAttribute('data-armed', '1');
      node.textContent = '确认删除';
      setTimeout(() => {
        node.setAttribute('data-armed', '0');
        node.textContent = '删除';
      }, 3000);
      return;
    }
    const entryID = node.getAttribute('data-entry-id');
    const entry = findEntryByID(entryID);
    const date = text(node.getAttribute('data-date'), (entry && text(entry.date)) || state.selected || today());
    removeEntry(entryID, date);
  }

  async function removeEntry(entryID, date) {
    const target = date || state.selected || today();
    notify('正在删除…');
    try {
      await bridgeInvoke('work-log.entry.delete', { date: target, entry_id: entryID });
      notify('已删除该条日志。');
      await refresh(target);
    } catch (error) {
      notify('删除失败：' + String((error && error.message) || error), 'error');
    }
  }

  // ENTRY_FIELDS 只列出 capability schema 允许透传的条目字段，避免把 created_at/updated_at
  // 原样回传被 schema 拒绝（entry 声明了 additionalProperties:false）。
  const ENTRY_FIELDS = [
    'entry_id', 'date', 'source', 'session_id', 'project', 'feature', 'exhibit',
    'project_id', 'exhibit_id', 'category',
    'minutes', 'minutes_source', 'started_at', 'ended_at', 'title', 'summary',
    'outcomes', 'decisions', 'learnings', 'blockers', 'next', 'tags', 'refs',
  ];

  function pickEntryFields(source) {
    const out = {};
    ENTRY_FIELDS.forEach((key) => {
      if (source[key] !== undefined) out[key] = source[key];
    });
    return out;
  }

  async function submitManual() {
    const title = text(el('m-title').value);
    if (!title) {
      notify('请先填写标题。', 'warn');
      el('m-title').focus();
      return;
    }
    const date = text(el('m-date').value, today());
    const editing = state.editingEntry;
    const entry = editing ? pickEntryFields(editing) : { source: 'manual' };
    entry.title = title;
    entry.project = text(el('m-project').value);
    entry.feature = text(el('m-feature').value);
    entry.exhibit = text(el('m-exhibit').value);
    delete entry.project_id;
    delete entry.exhibit_id;
    entry.category = text(el('m-category').value, '其他');
    entry.summary = text(el('m-summary').value);
    entry.outcomes = splitList(el('m-outcomes').value);
    entry.next = splitList(el('m-next').value);
    const hours = Number(el('m-hours').value);
    if (hours > 0) {
      entry.minutes = Math.round(hours * 60);
      entry.minutes_source = 'manual';
    } else {
      delete entry.minutes;
      delete entry.minutes_source;
    }
    const payload = { date: date, entry: entry };
    if (editing) {
      payload.overwrite = true;
      payload.entry.entry_id = text(editing.entry_id);
    }
    setBusy(true, '保存中…');
    try {
      await bridgeInvoke('work-log.entry.upsert', payload);
      notify(editing ? '已更新 ' + date + ' 的日志。' : '已保存 ' + date + ' 的工作日志。');
      closeManualForm();
      await refresh(date);
    } catch (error) {
      notify('保存失败：' + String((error && error.message) || error), 'error');
    } finally {
      setBusy(false);
    }
  }

  function setBusy(busy, label) {
    state.busy = busy;
    el('record').disabled = busy;
    el('refresh').disabled = busy;
    el('m-save').disabled = busy;
    el('m-save').textContent = busy && label ? label : saveLabel();
  }

  // ---------- 搜索 ----------

  function monthRange(date) {
    const key = monthKeyOf(date || today());
    const parts = key.split('-').map(Number);
    const year = parts[0];
    const month = parts[1];
    const lastDay = new Date(year, month, 0).getDate();
    return { from: year + '-' + pad2(month) + '-01', to: year + '-' + pad2(month) + '-' + pad2(lastDay) };
  }

  async function runSearch() {
    const query = text(state.query);
    if (!query) {
      state.searchResults = null;
      renderPanel();
      return;
    }
    const range = monthRange(state.selected || today());
    try {
      const data = await bridgeInvoke('work-log.entry.list', {
        from: range.from,
        to: range.to,
        query: query,
        limit: 500,
      });
      state.searchResults = { entries: (data && data.entries) || [], from: range.from, to: range.to };
    } catch (error) {
      state.searchResults = null;
      notify('搜索失败：' + String((error && error.message) || error), 'error');
    }
    renderPanel();
  }

  // ---------- 视图切换 ----------

  function switchView(view) {
    state.view = VIEWS.indexOf(view) >= 0 ? view : 'workbench';
    el('workbench-view').hidden = state.view !== 'workbench';
    el('plan-view').hidden = state.view !== 'plan';
    el('calendar-view').hidden = state.view !== 'calendar';
    el('report-view').hidden = state.view !== 'report';
    el('matrix-view').hidden = state.view !== 'matrix';
    el('projects-view').hidden = state.view !== 'projects';
    document.querySelectorAll('.view-switch .switch').forEach((node) => {
      const on = node.getAttribute('data-view') === state.view;
      node.classList.toggle('active', on);
      node.setAttribute('aria-selected', on ? 'true' : 'false');
    });
    const search = el('search');
    if (search) {
      const placeholders = {
        workbench: '筛选当前项目…',
        plan: '筛选工作项…',
        calendar: '搜索日志…',
        report: '筛选本期记录…',
        matrix: '筛选项目…',
        projects: '筛选项目…',
      };
      search.placeholder = placeholders[state.view] || '搜索…';
    }
    if (state.view === 'calendar') {
      if (state.selected) renderCalendar();
    } else {
      closeManualForm();
    }
    if (state.view === 'workbench') {
      if (!state.board) {
        loadBoard();
      } else {
        renderWorkbench();
        loadBoard({ silent: true });
      }
    } else if (state.view === 'plan') {
      if (!state.plan || state.plan.date !== ensurePlanDate()) loadPlan(state.planDate);
      else renderPlan();
    } else if (state.view === 'report') {
      if (!state.reportAnchor) state.reportAnchor = state.selected || today();
      renderFiscalSetting();
      loadReport();
    } else if (state.view === 'matrix') {
      loadMatrix(state.matrixYear || 0, state.matrixMonth || 0);
    } else if (state.view === 'projects') {
      loadProjects()
        .then(renderProjectsView)
        .catch((error) => notify('读取项目失败：' + String((error && error.message) || error), 'error'));
    }
  }

  function bind() {
    el('record').addEventListener('click', () => openManualForm(null));
    el('refresh').addEventListener('click', () => refresh());
    el('m-close').addEventListener('click', closeManualForm);
    el('m-cancel').addEventListener('click', closeManualForm);
    el('manual-backdrop').addEventListener('mousedown', (event) => {
      if (event.target === el('manual-backdrop')) closeManualForm();
    });
    el('maintain').addEventListener('click', openMaintenance);
    el('maint-close').addEventListener('click', closeMaintenance);
    el('maint-close2').addEventListener('click', closeMaintenance);
    el('maint-backdrop').addEventListener('mousedown', (event) => {
      if (event.target === el('maint-backdrop')) closeMaintenance();
    });
    el('maint-backup').addEventListener('click', backupArchive);
    el('maint-restore').addEventListener('click', restoreArchive);
    el('maint-export').addEventListener('click', exportEntries);
    el('maint-import').addEventListener('click', importEntries);
    el('maint-reconcile').addEventListener('click', reconcileEntries);
    el('maint-copy').addEventListener('click', () => copyText(el('maint-io').value, '已复制到剪贴板'));
    el('manual-fields').addEventListener('submit', (event) => {
      event.preventDefault();
      submitManual();
    });

    // 工作台 / 计划
    el('wb-open-plan').addEventListener('click', () => switchView('plan'));
    el('wb-all-projects').addEventListener('click', () => switchView('projects'));
    el('plan-prev').addEventListener('click', () => planDateShift(-1));
    el('plan-next').addEventListener('click', () => planDateShift(1));
    el('plan-today').addEventListener('click', () => loadPlan(today()));
    el('plan-date').addEventListener('change', () => {
      const value = el('plan-date').value;
      if (value) loadPlan(value);
    });
    el('plan-auto').addEventListener('click', autoPlanNow);
    el('plan-add-item').addEventListener('click', () => openItemModal(null));
    el('plan-add-block').addEventListener('click', () => openBlockModal(null, null));

    el('w-close').addEventListener('click', closeWorkModal);
    el('w-cancel').addEventListener('click', closeWorkModal);
    el('work-backdrop').addEventListener('mousedown', (event) => {
      if (event.target === el('work-backdrop')) closeWorkModal();
    });
    el('work-form').addEventListener('submit', submitWorkForm);
    el('w-state').addEventListener('change', updateCompleteBox);
    el('w-next-add').addEventListener('click', addWorkStep);
    el('w-next-input').addEventListener('keydown', (event) => {
      if (event.key === 'Enter') {
        event.preventDefault();
        addWorkStep();
      }
    });
    el('w-next-list').addEventListener('change', (event) => {
      const input = event.target.closest('[data-w-next-toggle]');
      if (!input) return;
      const index = Number(input.getAttribute('data-w-next-toggle'));
      if (state.workEditingSteps[index]) {
        state.workEditingSteps[index].done = input.checked;
        state.workEditingSteps[index].completed_at = input.checked ? new Date().toISOString() : '';
        renderWorkSteps();
      }
    });
    el('w-next-list').addEventListener('click', (event) => {
      const button = event.target.closest('[data-w-next-remove]');
      if (!button) return;
      const index = Number(button.getAttribute('data-w-next-remove'));
      state.workEditingSteps.splice(index, 1);
      renderWorkSteps();
    });

    el('i-close').addEventListener('click', closeItemModal);
    el('i-cancel').addEventListener('click', closeItemModal);
    el('item-backdrop').addEventListener('mousedown', (event) => {
      if (event.target === el('item-backdrop')) closeItemModal();
    });
    el('item-form').addEventListener('submit', submitItemForm);

    el('b-close').addEventListener('click', closeBlockModal);
    el('b-cancel').addEventListener('click', closeBlockModal);
    el('block-backdrop').addEventListener('mousedown', (event) => {
      if (event.target === el('block-backdrop')) closeBlockModal();
    });
    el('block-form').addEventListener('submit', submitBlockForm);

    const search = el('search');
    search.addEventListener('input', () => {
      state.query = search.value || '';
      state.searchResults = null;
      if (state.view === 'workbench') renderWorkbench();
      else if (state.view === 'plan') renderPlan();
      else if (state.view === 'calendar') renderPanel();
      else if (state.view === 'report') renderReport();
      else if (state.view === 'matrix') renderMatrix();
      else if (state.view === 'projects') {
        renderProjectList();
        if (state.projectsMode === 'progress') renderProjectProgress();
      }
    });
    search.addEventListener('keydown', (event) => {
      if (event.key === 'Enter') {
        event.preventDefault();
        if (state.view === 'calendar') runSearch();
      }
    });

    const logRootBtn = el('log-root');
    if (logRootBtn) {
      logRootBtn.addEventListener('click', () => copyText(state.logRoot, '已复制日志目录路径'));
    }
    const revealBtn = el('reveal-root');
    if (revealBtn) {
      revealBtn.addEventListener('click', () => {
        bridgeInvoke('work-log.link.reveal', {}).then(
          () => notify('已在文件管理器中打开日志目录。'),
          (error) => notify('打开目录失败：' + String((error && error.message) || error), 'error'),
        );
      });
    }

    document.querySelectorAll('.view-switch .switch').forEach((button) => {
      button.addEventListener('click', () => switchView(button.getAttribute('data-view')));
    });

    el('cal-prev').addEventListener('click', () => shiftMonth(-1));
    el('cal-next').addEventListener('click', () => shiftMonth(1));
    el('cal-grid').addEventListener('click', onCalendarClick);
    el('cal-project').addEventListener('change', () => {
      state.projectFilter = el('cal-project').value || '';
      applyProjectFilter().catch((error) =>
        notify('读取月历失败：' + String((error && error.message) || error), 'error'),
      );
    });

    // 报告
    el('report-periods').addEventListener('click', (event) => {
      const button = event.target.closest('.seg-btn');
      if (!button) return;
      state.reportPeriod = button.getAttribute('data-period');
      renderReportPeriods();
      loadReport();
    });
    el('report-prev').addEventListener('click', () => {
      state.reportAnchor = shiftAnchor(state.reportPeriod, state.reportAnchor || today(), -1);
      loadReport();
    });
    el('report-next').addEventListener('click', () => {
      state.reportAnchor = shiftAnchor(state.reportPeriod, state.reportAnchor || today(), 1);
      loadReport();
    });
    el('report-anchor').addEventListener('click', () => {
      state.reportAnchor = state.selected || today();
      loadReport();
    });
    el('report-generate').addEventListener('click', generateArchive);
    el('report-aggregate').addEventListener('click', aggregateNow);
    el('report-open').addEventListener('click', openArchive);
    el('report-copy').addEventListener('click', copyArchive);
    const fiscalSelect = el('fiscal-start-month');
    if (fiscalSelect) {
      fiscalSelect.addEventListener('change', () => {
        const month = Number(fiscalSelect.value);
        if (!month) return;
        saveFiscalStartMonth(month);
      });
    }

    // 工时表
    el('mx-prev').addEventListener('click', () => {
      let year = state.matrixYear;
      let month = state.matrixMonth - 1;
      if (month < 1) {
        month = 12;
        year -= 1;
      }
      loadMatrix(year, month);
    });
    el('mx-next').addEventListener('click', () => {
      let year = state.matrixYear;
      let month = state.matrixMonth + 1;
      if (month > 12) {
        month = 1;
        year += 1;
      }
      loadMatrix(year, month);
    });
    el('matrix-mode').addEventListener('click', (event) => {
      const button = event.target.closest('.seg-btn');
      if (!button) return;
      state.matrixMode = button.getAttribute('data-mode');
      renderMatrix();
    });
    el('mx-copy-all').addEventListener('click', () => {
      const table = state.matrixTable;
      if (!table) {
        notify('本月还没有可复制的工时数据', 'warn');
        return;
      }
      copyTableToClipboard([table.head].concat(table.rows).concat([table.total]), '已复制整张工时表，可直接粘贴到 Excel');
    });

    // 项目
    el('projects-mode').addEventListener('click', (event) => {
      const button = event.target.closest('.seg-btn');
      if (!button) return;
      state.projectsMode = button.getAttribute('data-mode');
      renderProjectsView();
    });

    // 工作台只在页面可见时每 60 秒静默刷新一次；spinner 本身由 CSS 持续动画驱动。
    startBoardAutoRefresh();
    document.addEventListener('visibilitychange', () => {
      if (!document.hidden && state.view === 'workbench') {
        loadBoard({ silent: true });
      }
    });

    document.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') {
        closeManualForm();
        closeMaintenance();
        closeWorkModal();
        closeItemModal();
        closeBlockModal();
      }
      trapFocus(event);
    });
  }

  async function refresh(preferredDate) {
    try {
      if (preferredDate) state.selected = preferredDate;
      const anchor = preferredDate || state.selected || today();
      const parts = anchor.split('-').map(Number);
      await loadProjects();
      await loadCalendar(parts[0], parts[1]);
      await selectDate(pickDefaultDate(state.calendar));
      await loadBoard();
      if (state.view === 'plan') await loadPlan(state.planDate);
      if (state.view === 'report') loadReport();
      if (state.view === 'matrix') loadMatrix(state.matrixYear || parts[0], state.matrixMonth || parts[1]);
    } catch (error) {
      if (error && error.code === 'bridge_unavailable') {
        el('detail-title').textContent = '请从 HiMind Agent 打开本视图';
        el('detail-subtitle').textContent = '页面需要宿主桥接才能读写日志数据。';
        el('panel').innerHTML = emptyBlock('请从 HiMind Agent 打开本视图', [
          '从 Agent 的扩展入口或快速入口打开“工作日志 · 日历回看”。',
          '日志保存在全局目录，页面本身不直接读文件系统。',
        ]);
        return;
      }
      notify('读取月历失败：' + String((error && error.message) || error), 'error');
    }
  }

  bind();
  switchView('workbench');
  // 先读回日志库级口径再做首次渲染：报告区间依赖起算月，
  // 晚读会让首屏先按自然季度画一遍再跳变。
  loadPreferences();
  refresh();
  window.__WORK_LOG_VIEW__ = { PLUGIN_ID: PLUGIN_ID, refresh: refresh };
})();
