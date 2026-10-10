/**
 * 工作日志自带视图。
 *
 * 导航分四块，按「看什么」组织而不是按数据表：
 *   日历回看：按天看当天脉络（项目 · 功能 · 工时）与明细。
 *   报告：按周期（日/周/月/季度/年）回看，核心仍是项目 → 功能 → 工时。
 *   工时表：按月生成「项目 × 日期」矩阵，可切换到 8 小时对齐口径。
 *   项目：按项目回看推进记录，并管理项目登记表与别名。
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
  const WEEKDAYS_MONDAY = ['一', '二', '三', '四', '五', '六', '日'];
  const WEEKDAYS_SUNDAY = ['日', '一', '二', '三', '四', '五', '六'];

  // 8 小时对齐的目标工时（分钟）。
  const ALIGN_TARGET_MINUTES = 8 * 60;
  const ALIGN_STEP_MINUTES = 30;

  const VIEWS = ['calendar', 'report', 'matrix', 'projects'];

  const state = {
    view: 'calendar',
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
    // 工时表
    matrixYear: 0,
    matrixMonth: 0,
    matrixEntries: null,
    matrixLoading: false,
    matrixMode: 'actual',
    // 项目
    projectsMode: 'progress',
    activeProject: '',
    projectEntries: null,
    projectLoading: false,
  };

  const el = (id) => document.getElementById(id);

  const MINUTES_SOURCE_LABELS = {
    measured: '实测',
    span: '区间',
    estimated: '估算',
    manual: '人工',
  };

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

  function formatTime(value) {
    const raw = text(value);
    if (!raw) return '';
    const match = raw.match(/^\d{4}-\d{2}-\d{2}[T ](\d{2}):(\d{2})/);
    if (match) return match[1] + ':' + match[2];
    return raw;
  }

  function formatSpan(item) {
    const start = formatTime(item.started_at);
    const end = formatTime(item.ended_at);
    if (!start && !end) return '';
    if (start && end) return start === end ? start : start + '–' + end;
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
        const features = project.featureList
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
    if (!hideProject) {
      if (text(item.project)) {
        head.push('<span class="proj">' + escapeHTML(item.project) + '</span>');
      } else if (entryID) {
        head.push(
          '<button type="button" class="proj miss" data-action="assign" data-entry-id="' +
            escapeHTML(entryID) +
            '" title="还没落到项目，点击补全">未落项目</button>',
        );
      }
    }
    if (text(item.feature)) head.push('<span class="feat">' + escapeHTML(item.feature) + '</span>');
    if (text(item.exhibit)) head.push('<span class="exhibit" title="展项">' + escapeHTML(item.exhibit) + '</span>');
    if (Number(item.minutes) > 0) {
      head.push('<span class="mins">' + escapeHTML(hoursShort(item.minutes)) + 'h</span>');
    }
    head.push('<span class="cat">' + escapeHTML(catLabel(item.category)) + '</span>');
    const span = formatSpan(item);
    if (span) head.push('<span class="time">' + escapeHTML(span) + '</span>');
    if (entryID) {
      head.push(
        '<span class="entry-actions">' +
          '<button type="button" class="link-btn" data-action="edit" data-entry-id="' +
          escapeHTML(entryID) +
          '">编辑</button>' +
          '<button type="button" class="link-btn danger" data-action="delete" data-entry-id="' +
          escapeHTML(entryID) +
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
      const hay = [item.title, item.summary, item.feature, item.project, item.exhibit, catLabel(item.category)].concat(item.tags || []);
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
        const features = project.featureList
          .map((feature) => {
            const label = feature.name === '未分类功能' ? '未分类功能' : '功能 · ' + feature.name;
            return (
              '<div class="feature-group">' +
              '<div class="feature-group-head"><span class="fg-tag">' +
              escapeHTML(label) +
              '</span><span class="fg-meta">' +
              escapeHTML(feature.count) +
              ' 条' +
              (feature.minutes > 0 ? ' · 约 ' + escapeHTML(hoursLabel(feature.minutes)) : '') +
              '</span></div>' +
              feature.entries.map((item) => entryCard(item, { hideProject: true })).join('') +
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
    panel.querySelectorAll('[data-action="edit"]').forEach((node) => {
      node.addEventListener('click', () => startEdit(node.getAttribute('data-entry-id')));
    });
    panel.querySelectorAll('[data-action="assign"]').forEach((node) => {
      node.addEventListener('click', () => startEdit(node.getAttribute('data-entry-id')));
    });
    panel.querySelectorAll('[data-action="toggle"]').forEach((node) => {
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
    panel.querySelectorAll('[data-action="delete"]').forEach((node) => {
      node.addEventListener('click', () => armDelete(node));
    });
  }

  // ---------- 报告 ----------

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
      return { from: formatDate(monday), to: formatDate(sunday), key: formatDate(monday) };
    }
    if (period === 'monthly') {
      const start = new Date(year, month, 1);
      const end = new Date(year, month + 1, 0);
      return { from: formatDate(start), to: formatDate(end), key: year + '-' + pad2(month + 1) };
    }
    if (period === 'quarterly') {
      const quarter = Math.floor(month / 3);
      const start = new Date(year, quarter * 3, 1);
      const end = new Date(year, quarter * 3 + 3, 0);
      return { from: formatDate(start), to: formatDate(end), key: year + '-Q' + (quarter + 1) };
    }
    return { from: year + '-01-01', to: year + '-12-31', key: String(year) };
  }

  function shiftAnchor(period, anchor, delta) {
    const date = parseDate(anchor) || new Date();
    if (period === 'daily') date.setDate(date.getDate() + delta);
    else if (period === 'weekly') date.setDate(date.getDate() + delta * 7);
    else if (period === 'monthly') date.setMonth(date.getMonth() + delta, 1);
    else if (period === 'quarterly') date.setMonth(date.getMonth() + delta * 3, 1);
    else date.setFullYear(date.getFullYear() + delta, 0, 1);
    return formatDate(date);
  }

  function renderReportPeriods() {
    const node = el('report-periods');
    if (!node) return;
    node.innerHTML = PERIOD_ORDER.map(
      (period) =>
        '<button type="button" class="seg-btn" data-period="' +
        period +
        '" aria-pressed="' +
        (period === state.reportPeriod ? 'true' : 'false') +
        '">' +
        escapeHTML(PERIOD_LABELS[period]) +
        '</button>',
    ).join('');
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
      (range.from !== range.to ? '<span class="badge">' + escapeHTML(range.from) + ' ~ ' + escapeHTML(range.to) + '</span>' : '');
    renderReport();
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

    const head =
      '<tr><th class="mx-corner" scope="col">项目</th>' +
      Array.from({ length: matrix.daysInMonth }, (_, i) => i + 1)
        .map((day) => '<th scope="col" class="mx-day">' + day + '</th>')
        .join('') +
      '<th scope="col" class="mx-total-col">合计</th></tr>';

    const rows = matrix.rows
      .map((row) => {
        const cells = Array.from({ length: matrix.daysInMonth }, (_, i) => {
          const day = i + 1;
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
        return (
          '<tr><th scope="row" class="mx-rowhead">' +
          escapeHTML(row.name) +
          '</th>' +
          cells +
          '<td class="mx-total">' +
          (rowTotal(row) > 0 ? escapeHTML(hoursShort(rowTotal(row))) : '—') +
          '</td></tr>'
        );
      })
      .join('');

    const foot =
      '<tr class="mx-foot"><th scope="row" class="mx-rowhead">当天合计</th>' +
      Array.from({ length: matrix.daysInMonth }, (_, i) => {
        const day = i + 1;
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

  // ---------- 项目 ----------

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
      node.innerHTML = '<p class="meta pad">还没有项目。写入日志时带项目名会自动登记。</p>';
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

  function renderProjectProgress() {
    const node = el('proj-progress');
    if (!node) return;
    if (state.projectLoading) {
      node.innerHTML = skeleton();
      return;
    }
    const entries = state.projectEntries || [];
    if (!state.activeProject) {
      node.innerHTML = emptyBlock('选择一个项目', ['左侧点一个项目，查看它的推进记录。']);
      return;
    }
    if (!entries.length) {
      node.innerHTML = emptyBlock('这个项目还没有记录', ['写入日志时带上该项目名即可出现在这里。']);
      return;
    }
    const flow = aggregateFlow(entries);
    const byDate = {};
    entries.forEach((item) => {
      if (!byDate[item.date]) byDate[item.date] = [];
      byDate[item.date].push(item);
    });
    const dates = Object.keys(byDate).sort().reverse();
    const activeDays = dates.length;

    const head =
      '<div class="pp-head"><h2>' +
      escapeHTML(state.activeProject) +
      '</h2>' +
      '<div class="pp-stats"><span class="badge">' +
      escapeHTML(flow.count) +
      ' 条</span><span class="badge">' +
      escapeHTML(activeDays) +
      ' 天</span>' +
      (flow.totalMinutes > 0 ? '<span class="badge">约 ' + escapeHTML(hoursShort(flow.totalMinutes)) + ' 小时</span>' : '') +
      '<span class="badge">' +
      escapeHTML(text(dates[dates.length - 1]) + ' ~ ' + text(dates[0])) +
      '</span></div>' +
      categoryChips(flow.categories) +
      '</div>';

    const features = flow.projects[0] ? flow.projects[0].featureList : [];
    const featureBlock =
      features.length > 1
        ? '<div class="pp-features">' +
          features
            .map(
              (feature) =>
                '<span class="flow-feat">' +
                escapeHTML(feature.name) +
                (feature.minutes > 0 ? '<b>' + escapeHTML(hoursShort(feature.minutes)) + 'h</b>' : '') +
                '</span>',
            )
            .join('') +
          '</div>'
        : '';

    const timeline = dates
      .map((date) => {
        const dayEntries = byDate[date];
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

    node.innerHTML = head + featureBlock + '<div class="pp-timeline">' + timeline + '</div>';
    node.querySelectorAll('[data-action="toggle"]').forEach((button) => {
      button.addEventListener('click', () => {
        const id = button.getAttribute('data-entry-id');
        state.expanded[id] = !state.expanded[id];
        const card = button.closest('.entry');
        if (card) {
          card.classList.toggle('expanded', !!state.expanded[id]);
          card.classList.toggle('compact', !state.expanded[id]);
          button.textContent = state.expanded[id] ? '收起' : '展开';
        }
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

  function renderProjectsPanel() {
    const panel = el('projects-panel');
    if (!panel) return;
    const data = state.projectsData || {};
    const projects = data.projects || [];
    const unmatched = data.unmatched || [];

    const form =
      '<div class="proj-form">' +
      '<label class="field"><span>项目名</span><input id="p-name" type="text" placeholder="如：项目看板" /></label>' +
      '<label class="field"><span>别名（逗号分隔）</span><input id="p-aliases" type="text" placeholder="如：kanban, 看板项目" /></label>' +
      '<button id="p-save" class="btn primary" type="button">添加 / 更新</button>' +
      '</div>';

    const rows = projects
      .map((item) => {
        const aliases = (item.aliases || []).join(', ');
        const range = text(item.first_date)
          ? escapeHTML(item.first_date) + ' ~ ' + escapeHTML(text(item.last_date))
          : '暂无记录';
        const exhibits = (item.exhibits || []).map((ex) => text(ex && ex.name)).filter(Boolean);
        const src = text(item.source) === 'workbench' ? '<span class="badge new">看板</span>' : '';
        const exhibitTag = exhibits.length ? ' · 展项 ' + escapeHTML(exhibits.length) : '';
        return (
          '<div class="proj-row" data-id="' +
          escapeHTML(item.id) +
          '" data-name="' +
          escapeHTML(item.name) +
          '">' +
          '<div class="proj-main"><strong>' +
          src +
          escapeHTML(item.name) +
          '</strong><span class="proj-meta">' +
          escapeHTML(item.entry_count || 0) +
          ' 条 · ' +
          escapeHTML(item.active_days || 0) +
          ' 天 · ' +
          range +
          exhibitTag +
          (exhibits.length ? '（' + escapeHTML(exhibits.slice(0, 4).join('、')) + (exhibits.length > 4 ? ' 等' : '') + '）' : '') +
          '</span></div>' +
          '<div class="proj-edit">' +
          '<input class="proj-alias-input" type="text" value="' +
          escapeHTML(aliases) +
          '" placeholder="别名，逗号分隔" />' +
          '<button class="btn" type="button" data-action="alias">保存</button>' +
          '<button class="btn" type="button" data-action="delete">删除</button>' +
          '</div></div>'
        );
      })
      .join('');

    const list = projects.length
      ? rows
      : emptyBlock('还没有项目', ['写入日志时若带项目名会自动登记；也可以在上方手动添加。']);

    const unmatchedBlock = unmatched.length
      ? '<div class="proj-unmatched"><h3>未归类（登记表里没有）</h3><ul>' +
        unmatched
          .map((item) => '<li>' + escapeHTML(item.name) + ' · ' + escapeHTML(item.entry_count) + ' 条</li>')
          .join('') +
        '</ul><p class="meta">在上方登记同名项目并设置别名，即可把历史条目归并到规范项目。</p></div>'
      : '';

    panel.innerHTML = form + '<div class="proj-list">' + list + '</div>' + unmatchedBlock;
    bindProjectsPanel(panel);
  }

  function bindProjectsPanel(panel) {
    const save = panel.querySelector('#p-save');
    if (save) {
      save.addEventListener('click', async () => {
        const name = text(el('p-name').value);
        if (!name) {
          notify('请先填写项目名。', 'warn');
          el('p-name').focus();
          return;
        }
        try {
          await bridgeInvoke('work-log.project.upsert', {
            project_name: name,
            aliases: splitList(el('p-aliases').value),
          });
          notify('已保存项目：' + name);
          el('p-name').value = '';
          el('p-aliases').value = '';
          await loadProjects();
          renderProjectsPanel();
          renderProjectList();
        } catch (error) {
          notify('保存项目失败：' + String((error && error.message) || error), 'error');
        }
      });
    }

    panel.querySelectorAll('.proj-row').forEach((row) => {
      const id = row.getAttribute('data-id');
      const name = row.getAttribute('data-name');

      const aliasBtn = row.querySelector('[data-action="alias"]');
      if (aliasBtn) {
        aliasBtn.addEventListener('click', async () => {
          const input = row.querySelector('.proj-alias-input');
          try {
            await bridgeInvoke('work-log.project.upsert', {
              project_id: id,
              project_name: name,
              aliases: splitList(input ? input.value : ''),
            });
            notify('已更新别名：' + name);
            await loadProjects();
            renderProjectsPanel();
            renderProjectList();
          } catch (error) {
            notify('更新别名失败：' + String((error && error.message) || error), 'error');
          }
        });
      }

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
            await loadProjects();
            renderProjectsPanel();
            renderProjectList();
          } catch (error) {
            notify('删除失败：' + String((error && error.message) || error), 'error');
          }
        });
      }
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
    if (event.key !== 'Tab' || el('manual-backdrop').hidden) return;
    const focusable = el('manual-form').querySelectorAll(
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

  function startEdit(entryID) {
    const entry = state.entries.find((item) => text(item.entry_id) === entryID);
    if (!entry) {
      notify('未找到要编辑的条目，请刷新后重试。', 'warn');
      return;
    }
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
    removeEntry(node.getAttribute('data-entry-id'));
  }

  async function removeEntry(entryID) {
    const date = state.selected || today();
    notify('正在删除…');
    try {
      await bridgeInvoke('work-log.entry.delete', { date: date, entry_id: entryID });
      notify('已删除该条日志。');
      await refresh(date);
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
    state.view = VIEWS.indexOf(view) >= 0 ? view : 'calendar';
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
    if (state.view === 'report') {
      if (!state.reportAnchor) state.reportAnchor = state.selected || today();
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
    el('manual-fields').addEventListener('submit', (event) => {
      event.preventDefault();
      submitManual();
    });

    const search = el('search');
    search.addEventListener('input', () => {
      state.query = search.value || '';
      state.searchResults = null;
      if (state.view === 'calendar') renderPanel();
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
    el('report-open').addEventListener('click', openArchive);
    el('report-copy').addEventListener('click', copyArchive);

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

    // 项目
    el('projects-mode').addEventListener('click', (event) => {
      const button = event.target.closest('.seg-btn');
      if (!button) return;
      state.projectsMode = button.getAttribute('data-mode');
      renderProjectsView();
    });

    document.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') closeManualForm();
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
  switchView('calendar');
  refresh();
  window.__WORK_LOG_VIEW__ = { PLUGIN_ID: PLUGIN_ID, refresh: refresh };
})();
