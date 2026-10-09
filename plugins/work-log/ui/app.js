/**
 * 工作日志自带视图（按日为核心）。
 *
 * 导航以自然月日历为主：左侧月历每天标注日志数量，点一天看当天时间线。
 * 日报是一等视图；周 / 季度 / 年度汇总收进「辅助视图」，不占主干。
 * 「项目管理」是全局视图（项目登记表），与按日的「日历回看」并列，不混进按日页签。
 *
 * 视图只消费本插件 Manifest 已声明的能力：月历统计、条目录入/读取/删除、报告渲染/读取、
 * 打开链接。它不直接读文件系统，也不依赖宿主页面，因此单独打开或嵌入宿主时行为一致。
 */
(function () {
  'use strict';

  const PLUGIN_ID = 'com.himind.work-log';
  // 受控工作类型现在统一存中文枚举；这里仅保留旧英文键到中文的兜底映射，
  // 让历史条目也能正确显示。展示一律走 catLabel()。
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
  const PERIOD_LABELS = {
    daily: '日报',
    weekly: '周报',
    quarterly: '季度总结',
    yearly: '年度总结',
  };
  const WEEKDAYS_MONDAY = ['一', '二', '三', '四', '五', '六', '日'];
  const WEEKDAYS_SUNDAY = ['日', '一', '二', '三', '四', '五', '六'];

  const state = {
    calendar: null,
    year: 0,
    month: 0,
    selected: '',
    entries: [],
    tab: 'timeline',
    view: 'calendar',
    logRoot: '',
    report: null,
    loadingReport: false,
    busy: false,
    projectFilter: '',
    projects: [],
    projectsData: null,
    editingEntry: null,
    query: '',
    searchResults: null,
  };

  const REPORT_TABS = ['daily', 'weekly', 'quarterly', 'yearly'];

  const el = (id) => document.getElementById(id);

  const MINUTES_SOURCE_LABELS = {
    measured: '实测',
    span: '区间',
    estimated: '估算',
    manual: '人工',
  };

  // formatTime 把 RFC3339 时间戳渲染成 HH:MM；无法解析时原样返回。
  function formatTime(value) {
    const raw = text(value);
    if (!raw) return '';
    const match = raw.match(/^\d{4}-\d{2}-\d{2}[T ](\d{2}):(\d{2})/);
    if (match) return match[1] + ':' + match[2];
    return raw;
  }

  // formatSpan 把起止时间渲染成区间。
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

  // notify 显示一条提示并自动消失：成功/信息短，警告与错误留久一点。
  // 传空消息表示立即清除。refresh() 不再清提示，否则「已保存/已删除」会被刷新冲掉。
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

  function text(value, fallback) {
    const trimmed = value === 0 || value ? String(value) : '';
    return trimmed.trim() || fallback || '';
  }

  function escapeHTML(value) {
    return text(value).replace(/[&<>"']/g, (char) => {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char];
    });
  }

  function today() {
    const now = new Date();
    const pad = (value) => String(value).padStart(2, '0');
    return now.getFullYear() + '-' + pad(now.getMonth() + 1) + '-' + pad(now.getDate());
  }

  function monthKeyOf(date) {
    return text(date).slice(0, 7);
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
            const titleAttr =
              text(cell.date) +
              (projects ? ' · ' + projects : '') +
              (hours ? ' · 约 ' + hours : '');
            const marker =
              cell.entry_count > 0 ? '<span class="cal-count">' + escapeHTML(cell.entry_count) + '</span>' : '';
            const hoursMark = hours ? '<span class="cal-hours">' + escapeHTML(hours) + '</span>' : '';
            return (
              '<button type="button" role="gridcell" class="' +
              classes.join(' ') +
              '" data-date="' +
              escapeHTML(cell.date) +
              '" title="' +
              escapeHTML(titleAttr) +
              '"><span class="cal-day">' +
              escapeHTML(cell.day) +
              '</span>' +
              marker +
              hoursMark +
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
      el('detail-subtitle').textContent = '左侧月历按天统计；点一天查看当天时间线。';
      el('detail-badges').innerHTML = '';
      return;
    }
    el('detail-title').textContent = state.selected + ' · 工作日志';
    const projects = [];
    state.entries.forEach((item) => {
      const name = text(item.project);
      if (name && projects.indexOf(name) < 0) projects.push(name);
    });
    el('detail-subtitle').textContent = state.entries.length
      ? '共 ' + state.entries.length + ' 条记录' + (projects.length ? '，涉及 ' + projects.join('、') : '')
      : '这一天还没有记录。';
    const badges = [];
    badges.push('<span class="badge">' + escapeHTML(state.entries.length) + ' 条</span>');
    if (projects.length) badges.push('<span class="badge new">' + escapeHTML(projects.length) + ' 个项目</span>');
    const totalMinutes = state.entries.reduce((sum, item) => sum + (Number(item.minutes) || 0), 0);
    if (totalMinutes > 0) badges.push('<span class="badge">约 ' + escapeHTML(hoursLabel(totalMinutes)) + '</span>');
    el('detail-badges').innerHTML = badges.join('');
  }

  function entryCard(item) {
    const entryID = text(item.entry_id);
    const head = ['<div class="entry" data-entry-id="' + escapeHTML(entryID) + '"><div class="entry-head">'];
    const span = formatSpan(item);
    if (span) {
      head.push('<span class="time">' + escapeHTML(span) + '</span>');
    }
    head.push('<strong>' + escapeHTML(text(item.title, '(未命名)')) + '</strong>');
    head.push('<span class="cat">' + escapeHTML(catLabel(item.category)) + '</span>');
    if (text(item.project)) head.push('<span class="proj">' + escapeHTML(item.project) + '</span>');
    if (text(item.feature)) head.push('<span class="feat">' + escapeHTML(item.feature) + '</span>');
    if (Number(item.minutes) > 0) {
      const source = MINUTES_SOURCE_LABELS[item.minutes_source] || text(item.minutes_source, '工时');
      head.push('<span class="mins">' + escapeHTML(minutesLabel(item.minutes)) + ' · ' + escapeHTML(source) + '</span>');
    }
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
    head.push('</div>');
    if (text(item.summary)) head.push('<p class="summary">' + escapeHTML(item.summary) + '</p>');
    head.push(listBlock('交付', item.outcomes));
    head.push(listBlock('决策', item.decisions));
    head.push(listBlock('经验', item.learnings));
    head.push(listBlock('阻塞', item.blockers, true));
    head.push(listBlock('后续', item.next));
    const refs = (item.refs || []).filter((ref) => text(ref && ref.value));
    if (refs.length) {
      head.push(
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
    head.push('</div>');
    return head.join('');
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
    if (!needle) return entries;
    return entries.filter((item) => {
      const hay = [item.title, item.summary, item.feature, item.project, catLabel(item.category)].concat(item.tags || []);
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
        results.map(entryCard).join('')
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
        '清空搜索框可回到完整时间线。',
      ]);
    }
    return entries.map(entryCard).join('');
  }

  function markdownToHTML(markdown) {
    const lines = String(markdown || '').split(/\r?\n/);
    const out = [];
    let inList = false;
    const closeList = () => {
      if (inList) {
        out.push('</ul>');
        inList = false;
      }
    };
    for (const raw of lines) {
      const line = raw.replace(/\s+$/, '');
      if (!line.trim()) {
        closeList();
        continue;
      }
      if (line.startsWith('#### ')) {
        closeList();
        out.push('<h4>' + escapeHTML(line.slice(5)) + '</h4>');
      } else if (line.startsWith('### ')) {
        closeList();
        out.push('<h3>' + escapeHTML(line.slice(4)) + '</h3>');
      } else if (line.startsWith('## ')) {
        closeList();
        out.push('<h2>' + escapeHTML(line.slice(3)) + '</h2>');
      } else if (line.startsWith('# ')) {
        closeList();
        out.push('<h1>' + escapeHTML(line.slice(2)) + '</h1>');
      } else if (/^\s*-\s+/.test(line)) {
        if (!inList) {
          out.push('<ul>');
          inList = true;
        }
        out.push('<li>' + escapeHTML(line.replace(/^\s*-\s+/, '')) + '</li>');
      } else {
        closeList();
        out.push('<p>' + escapeHTML(line) + '</p>');
      }
    }
    closeList();
    return '<div class="md-view">' + out.join('') + '</div>';
  }

  function renderReportPanel() {
    const panel = el('panel');
    const period = state.tab;
    if (state.loadingReport || !state.report) {
      panel.innerHTML = skeleton();
      return;
    }
    if (state.report.error) {
      panel.innerHTML =
        '<div class="empty"><strong>还没有 ' +
        escapeHTML(PERIOD_LABELS[period]) +
        '</strong><p>' +
        escapeHTML(state.report.error) +
        '</p><div class="row-actions"><button type="button" class="btn primary" data-action="generate">按当前日期生成</button></div></div>';
      bindGenerate(panel, period);
      return;
    }
    const data = state.report.data || {};
    const markdown = text(data.markdown);
    const range = data.date_from && data.date_to && data.date_from !== data.date_to
      ? '<span class="badge">' + escapeHTML(data.date_from) + ' ~ ' + escapeHTML(data.date_to) + '</span>'
      : '';
    panel.innerHTML =
      '<div class="row-actions">' +
      '<button type="button" class="btn" data-action="regen">重新生成</button>' +
      '<button type="button" class="btn" data-action="open">在浏览器打开</button>' +
      '<button type="button" class="btn" data-action="copy">复制 Markdown</button>' +
      '<span class="badge">' +
      escapeHTML(text(data.key, period)) +
      '</span>' +
      range +
      '</div>' +
      '<p class="redact-hint" role="note">导出 / 分享前请先脱敏：报告可能包含项目名、内网地址、密钥或客户信息。</p>' +
      markdownToHTML(markdown);
    panel.querySelector('[data-action="regen"]').addEventListener('click', () => generateReport(period));
    panel.querySelector('[data-action="open"]').addEventListener('click', () => {
      const uri = toFileUri(data.html_path || data.md_path);
      if (!uri) {
        notify('报告尚未落盘。', 'warn');
        return;
      }
      notify('提示：分享或导出前请先脱敏。', 'warn');
      openLink(uri).then((opened) => {
        if (!opened) copyText(uri, '宿主未能打开，已复制报告路径');
      });
    });
    panel.querySelector('[data-action="copy"]').addEventListener('click', () =>
      copyText(markdown, '已复制报告 Markdown（分享前请脱敏）'),
    );
  }

  function bindGenerate(panel, period) {
    const button = panel.querySelector('[data-action="generate"]');
    if (button) button.addEventListener('click', () => generateReport(period));
  }

  function renderPanel() {
    const panel = el('panel');
    if (state.tab === 'timeline') {
      panel.innerHTML = renderTimeline();
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
      panel.querySelectorAll('[data-action="delete"]').forEach((node) => {
        node.addEventListener('click', () => armDelete(node));
      });
      return;
    }
    renderReportPanel();
  }

  function applyEmpty(title, message, lines) {
    el('detail-title').textContent = title;
    el('detail-subtitle').textContent = message;
    el('detail-badges').innerHTML = '';
    el('panel').innerHTML = emptyBlock(title, lines);
  }

  async function loadReport(period) {
    const anchor = state.selected || today();
    state.loadingReport = true;
    state.report = null;
    renderPanel();
    try {
      const data = await bridgeInvoke('work-log.report.read', { period: period, date: anchor });
      state.report = { data: data };
    } catch (error) {
      state.report = { error: String((error && error.message) || error) };
    } finally {
      state.loadingReport = false;
    }
    renderPanel();
  }

  async function generateReport(period) {
    const anchor = state.selected || today();
    const label = period === 'daily' ? '日报' : PERIOD_LABELS[period] || period;
    notify('正在按确定性规则生成' + label + '…');
    try {
      const result = await bridgeInvoke('work-log.report.render', { period: period, date: anchor });
      const count = result && result.report ? result.report.entry_count : '';
      notify('已生成 ' + label + '（覆盖 ' + count + ' 条记录），落盘到全局日志目录。');
      await loadReport(period);
    } catch (error) {
      notify('生成失败：' + String((error && error.message) || error), 'error');
    }
  }

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
      datalist.innerHTML = names
        .map((name) => '<option value="' + escapeHTML(name) + '"></option>')
        .join('');
    }
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
        return (
          '<div class="proj-row" data-id="' +
          escapeHTML(item.id) +
          '" data-name="' +
          escapeHTML(item.name) +
          '">' +
          '<div class="proj-main"><strong>' +
          escapeHTML(item.name) +
          '</strong><span class="proj-meta">' +
          escapeHTML(item.entry_count || 0) +
          ' 条 · ' +
          escapeHTML(item.active_days || 0) +
          ' 天 · ' +
          range +
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

    panel.innerHTML =
      form + '<div class="proj-list">' + list + '</div>' + unmatchedBlock;
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
          } catch (error) {
            notify('删除失败：' + String((error && error.message) || error), 'error');
          }
        });
      }
    });
  }

  async function applyProjectFilter() {
    const anchor = state.selected || today();
    const parts = anchor.split('-').map(Number);
    await loadCalendar(parts[0], parts[1]);
    await selectDate(pickDefaultDate(state.calendar));
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
    state.report = null;
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
    if (state.tab === 'timeline') {
      renderPanel();
    } else if (REPORT_TABS.indexOf(state.tab) >= 0) {
      loadReport(state.tab);
    } else {
      renderPanel();
    }
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

  function setBusy(busy, label) {
    state.busy = busy;
    el('record').disabled = busy;
    el('refresh').disabled = busy;
    el('m-save').disabled = busy;
    el('m-save').textContent = busy && label ? label : saveLabel();
  }

  function openManualForm(entry) {
    state.editingEntry = entry || null;
    el('m-entry-id').value = entry ? text(entry.entry_id) : '';
    el('m-date').value = (entry && text(entry.date)) || state.selected || today();
    el('m-title').value = entry ? text(entry.title) : '';
    el('m-project').value = entry ? text(entry.project) : '';
    el('m-feature').value = entry ? text(entry.feature) : '';
    el('m-minutes').value = entry && Number(entry.minutes) > 0 ? String(entry.minutes) : '';
    el('m-category').value = (() => {
      const label = entry ? catLabel(entry.category) : '';
      return CATEGORY_OPTIONS.indexOf(label) >= 0 ? label : '开发';
    })();
    el('m-summary').value = entry ? text(entry.summary) : '';
    el('m-outcomes').value = entry ? (entry.outcomes || []).join('，') : '';
    el('m-next').value = entry ? (entry.next || []).join('，') : '';
    el('manual-title').textContent = entry ? '编辑一条工作日志' : '手动记录一条工作日志';
    el('manual-hint').textContent = entry
      ? '保存后按 entry_id 整条替换，可清空字段；日期不可改。'
      : '纯手工入口，不依赖任何会话采集。';
    el('m-save').textContent = saveLabel();
    // 编辑时锁定日期：条目按天分片存储，改日期会变成「在新的一天新增」，需要另做移动语义。
    el('m-date').disabled = !!entry;
    el('manual-form').hidden = false;
    el('m-title').focus();
  }

  function saveLabel() {
    return state.editingEntry ? '保存修改' : '保存到日志';
  }

  function closeManualForm() {
    state.editingEntry = null;
    el('m-date').disabled = false;
    el('manual-form').hidden = true;
    el('m-save').textContent = saveLabel();
  }

  function startEdit(entryID) {
    const entry = state.entries.find((item) => text(item.entry_id) === entryID);
    if (!entry) {
      notify('未找到要编辑的条目，请刷新后重试。', 'warn');
      return;
    }
    if (!el('manual-form').hidden && state.editingEntry && text(state.editingEntry.entry_id) === entryID) {
      closeManualForm();
      return;
    }
    openManualForm(entry);
    el('manual-form').scrollIntoView({ behavior: 'smooth', block: 'nearest' });
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

  function splitList(value) {
    return text(value)
      .split(/[,，、;；]+/)
      .map((item) => item.trim())
      .filter(Boolean);
  }

  // ENTRY_FIELDS 只列出 capability schema 允许透传的条目字段。created_at/updated_at
  // 由后端维护，且 schema 对 entry 声明了 additionalProperties:false；若把整条 entry
  // 原样回传（含 created_at），保存会被直接拒绝。这正是「编辑保存失败：unknown
  // property: created_at」的根因，所以编辑时按白名单挑选字段而不是整条克隆。
  const ENTRY_FIELDS = [
    'entry_id', 'date', 'source', 'session_id', 'project', 'feature', 'category',
    'minutes', 'minutes_source', 'started_at', 'ended_at', 'title', 'summary',
    'outcomes', 'decisions', 'learnings', 'blockers', 'next', 'tags', 'refs',
  ];

  function pickEntryFields(source) {
    const out = {};
    ENTRY_FIELDS.forEach((key) => {
      if (source[key] !== undefined) {
        out[key] = source[key];
      }
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
    // 编辑时以原条目为底，保证表单未覆盖的字段（source/session_id/refs/decisions 等）不丢；
    // 只透传 schema 允许的字段（见 pickEntryFields），其余由后端接管。
    // 传 overwrite=true 让后端整条替换，用户清空的字段才真正被清空。
    const entry = editing ? pickEntryFields(editing) : { source: 'manual' };
    entry.title = title;
    entry.project = text(el('m-project').value);
    entry.feature = text(el('m-feature').value);
    entry.category = text(el('m-category').value, '其他');
    entry.summary = text(el('m-summary').value);
    entry.outcomes = splitList(el('m-outcomes').value);
    entry.next = splitList(el('m-next').value);
    const minutes = Number(el('m-minutes').value);
    if (minutes > 0) {
      entry.minutes = Math.round(minutes);
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

  function monthRange(date) {
    const key = monthKeyOf(date || today());
    const parts = key.split('-').map(Number);
    const year = parts[0];
    const month = parts[1];
    const pad = (value) => String(value).padStart(2, '0');
    const lastDay = new Date(year, month, 0).getDate();
    return {
      from: year + '-' + pad(month) + '-01',
      to: year + '-' + pad(month) + '-' + pad(lastDay),
    };
  }

  // runSearch 在「当前选中日所在月」范围内做全文检索，命中结果替换时间线显示。
  async function runSearch() {
    const query = text(state.query);
    if (!query) {
      state.searchResults = null;
      if (state.tab === 'timeline') renderPanel();
      return;
    }
    const range = monthRange(state.selected || today());
    try {
      const data = await bridgeInvoke('work-log.entry.list', {
        from: range.from,
        to: range.to,
        query: query,
        limit: 200,
      });
      state.searchResults = { entries: (data && data.entries) || [], from: range.from, to: range.to };
    } catch (error) {
      state.searchResults = null;
      notify('搜索失败：' + String((error && error.message) || error), 'error');
    }
    setActiveTab('timeline');
    renderPanel();
  }

  function setActiveTab(tab) {
    state.tab = tab;
    document.querySelectorAll('.tabs .tab').forEach((node) => {
      const on = node.getAttribute('data-tab') === tab;
      node.classList.toggle('active', on);
      node.setAttribute('aria-selected', on ? 'true' : 'false');
      node.setAttribute('tabindex', on ? '0' : '-1');
    });
    const active = document.getElementById('tab-' + tab);
    if (active) el('panel').setAttribute('aria-labelledby', active.id);
  }

  function switchView(view) {
    state.view = view === 'projects' ? 'projects' : 'calendar';
    el('calendar-view').hidden = state.view !== 'calendar';
    el('projects-view').hidden = state.view !== 'projects';
    document.querySelectorAll('.view-switch .switch').forEach((node) => {
      const on = node.getAttribute('data-view') === state.view;
      node.classList.toggle('active', on);
      node.setAttribute('aria-selected', on ? 'true' : 'false');
    });
    if (state.view === 'projects') {
      closeManualForm();
      loadProjects()
        .then(renderProjectsPanel)
        .catch((error) => notify('读取项目失败：' + String((error && error.message) || error), 'error'));
    }
  }

  function bind() {
    el('record').addEventListener('click', () => {
      if (el('manual-form').hidden) {
        openManualForm(null);
      } else {
        closeManualForm();
      }
    });
    el('refresh').addEventListener('click', () => refresh());
    const search = el('search');
    if (search) {
      search.addEventListener('input', () => {
        state.query = search.value || '';
        if (state.searchResults) state.searchResults = null;
        if (state.tab === 'timeline') renderPanel();
      });
      search.addEventListener('keydown', (event) => {
        if (event.key === 'Enter') {
          event.preventDefault();
          runSearch();
        }
      });
    }
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
    el('m-cancel').addEventListener('click', closeManualForm);
    el('manual-fields').addEventListener('submit', (event) => {
      event.preventDefault();
      submitManual();
    });

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
    document.querySelectorAll('.tabs .tab').forEach((button) => {
      button.addEventListener('click', () => {
        setActiveTab(button.getAttribute('data-tab'));
        if (REPORT_TABS.indexOf(state.tab) >= 0) {
          loadReport(state.tab);
        } else {
          renderPanel();
        }
      });
      // 符合 WAI-ARIA tab 模式：方向键在同一组页签间移动并激活。
      button.addEventListener('keydown', (event) => {
        if (event.key !== 'ArrowRight' && event.key !== 'ArrowLeft') return;
        const tabs = Array.prototype.slice.call(button.parentElement.querySelectorAll('.tab'));
        const index = tabs.indexOf(button);
        if (index < 0) return;
        const next = event.key === 'ArrowRight' ? (index + 1) % tabs.length : (index - 1 + tabs.length) % tabs.length;
        event.preventDefault();
        tabs[next].focus();
        tabs[next].click();
      });
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
    } catch (error) {
      if (error && error.code === 'bridge_unavailable') {
        applyEmpty('请从 HiMind Agent 打开本视图', '页面需要宿主桥接才能读写日志数据。', [
          '从 Agent 的扩展入口或快速入口打开“工作日志 · 日历回看”。',
          '日志保存在全局目录，页面本身不直接读文件系统。',
        ]);
        return;
      }
      notify('读取月历失败：' + String((error && error.message) || error), 'error');
    }
  }

  bind();
  setActiveTab(state.tab);
  switchView('calendar');
  refresh();
  window.__WORK_LOG_VIEW__ = { PLUGIN_ID: PLUGIN_ID, refresh: refresh };
})();
