/**
 * 科技雷达自带视图。
 *
 * 视图只消费本插件 Manifest 已声明的能力：归档列表、归档读取、采集和报告渲染。
 * 它不读文件系统，也不依赖宿主页面，因此插件被单独打开或嵌入宿主时行为一致。
 */
(function () {
  'use strict';

  const PLUGIN_ID = 'com.himind.tech-radar';
  const tabs = ['ranking', 'summary', 'source'];
  const state = {
    records: [],
    selected: '',
    detail: null,
    dataRoot: '',
    tab: 'ranking',
    busy: false,
  };

  const el = (id) => document.getElementById(id);

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

  // 打开链接由扩展自己的 tech-radar.link.open 能力完成：宿主没有为插件视图
  // 开放任何通用打开能力，视图能用什么在 Manifest 里写得很清楚。
  function openLink(url) {
    return bridgeInvoke('tech-radar.link.open', { url: url }).then(
      () => true,
      () => false,
    );
  }

  function notify(message, tone) {
    const node = el('notice');
    if (!message) {
      node.hidden = true;
      node.textContent = '';
      return;
    }
    node.hidden = false;
    node.className = 'notice' + (tone ? ' ' + tone : '');
    node.textContent = message;
  }

  function text(value, fallback) {
    const trimmed = (value === 0 || value ? String(value) : '').trim();
    return trimmed || fallback || '';
  }

  function escapeHTML(value) {
    return text(value).replace(/[&<>"']/g, (char) => {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char];
    });
  }

  function setBusy(busy, label) {
    state.busy = busy;
    el('collect').disabled = busy;
    el('refresh').disabled = busy;
    el('collect').textContent = busy && label ? label : '采集一期';
  }

  function renderArchiveList() {
    const list = el('archive-list');
    el('archive-count').textContent = state.records.length
      ? state.records.length + ' 期'
      : '暂无';
    list.innerHTML = state.records
      .map((record) => {
        const active = record.report_date === state.selected ? ' active' : '';
        const newBadge = record.new_entry_count
          ? '<strong>+' + escapeHTML(record.new_entry_count) + ' 新上榜</strong>'
          : '';
        const degraded = record.degraded ? ' · 规则稿' : '';
        return (
          '<li><button type="button" class="archive-item' +
          active +
          '" data-date="' +
          escapeHTML(record.report_date) +
          '">' +
          '<span class="row"><span class="date">' +
          escapeHTML(record.report_date) +
          '</span><span class="pill">' +
          escapeHTML(record.entry_count) +
          ' 条</span></span>' +
          '<span class="meta">' +
          newBadge +
          escapeHTML(degraded) +
          '</span></button></li>'
        );
      })
      .join('');
  }

  function renderBadges(record, report) {
    const badges = [];
    if (report && report.degraded) badges.push('<span class="badge warn">规则稿 · 未含智能解读</span>');
    else badges.push('<span class="badge new">含智能解读</span>');
    if (report && report.entry_count !== undefined) {
      badges.push('<span class="badge">' + escapeHTML(report.entry_count) + ' 条上榜</span>');
    }
    if (report && report.new_entry_count) {
      badges.push('<span class="badge new">' + escapeHTML(report.new_entry_count) + ' 条新上榜</span>');
    }
    if (record && record.rule_version) badges.push('<span class="badge">' + escapeHTML(record.rule_version) + '</span>');
    el('detail-badges').innerHTML = badges.join('');
  }

  function insightFor(detail, entryId) {
    const insights = (detail && detail.insight && detail.insight.insights) || [];
    const matched = insights.find((item) => item && item.entry_id === entryId);
    return matched && matched.reason ? String(matched.reason) : '';
  }

  function renderRanking(detail) {
    const entries = (detail.snapshot && detail.snapshot.entries) || [];
    if (!entries.length) {
      return emptyBlock('本期没有条目', ['采集返回为空，通常是主题过窄或窗口太短。']);
    }
    const items = entries
      .map((entry, index) => {
        const badges = [];
        if (entry.is_new) badges.push('<span class="badge new">新上榜</span>');
        else if (entry.rank_delta > 0) badges.push('<span class="badge new">↑' + escapeHTML(entry.rank_delta) + '</span>');
        else if (entry.rank_delta < 0) badges.push('<span class="badge">↓' + escapeHTML(-entry.rank_delta) + '</span>');
        const reason = insightFor(detail, entry.entry_id);
        return (
          '<li class="entry">' +
          '<span class="rank">' +
          (index + 1) +
          '</span>' +
          '<div class="entry-main">' +
          '<div class="entry-head">' +
          '<button type="button" class="repo" data-url="' +
          escapeHTML(entry.html_url) +
          '" title="在浏览器打开 ' +
          escapeHTML(entry.html_url) +
          '">' +
          escapeHTML(entry.full_name || entry.entry_id) +
          '</button>' +
          badges.join('') +
          '</div>' +
          (entry.description ? '<p class="desc">' + escapeHTML(entry.description) + '</p>' : '') +
          (reason ? '<p class="reason">' + escapeHTML(reason) + '</p>' : '') +
          '<p class="meta">' +
          escapeHTML(text(entry.language, '未标注语言')) +
          (entry.topics && entry.topics.length ? ' · ' + escapeHTML(entry.topics.slice(0, 4).join(', ')) : '') +
          '</p>' +
          '</div>' +
          '<span class="stars">⭐ ' +
          escapeHTML(entry.stars || 0) +
          (entry.stars_gained ? ' (+' + escapeHTML(entry.stars_gained) + ')' : '') +
          '</span>' +
          '</li>'
        );
      })
      .join('');
    return '<ul class="entry-list">' + items + '</ul>';
  }

  function renderSummary(detail) {
    const report = detail.report || {};
    const summary = text(report.summary, '本期没有生成一句话总结。');
    const markdown = text(report.dingtalk_markdown, '（没有可用的 Markdown 摘要）');
    return (
      '<div class="summary-card"><h2>一句话总结</h2><p>' +
      escapeHTML(summary) +
      '</p></div>' +
      '<div class="summary-card" style="margin-top:10px"><h2>报告元数据</h2><dl class="kv">' +
      '<dt>报告日期</dt><dd>' +
      escapeHTML(detail.report_date) +
      '</dd>' +
      '<dt>条目 / 新上榜</dt><dd>' +
      escapeHTML(report.entry_count || 0) +
      ' / ' +
      escapeHTML(report.new_entry_count || 0) +
      '</dd>' +
      '<dt>规则版本</dt><dd>' +
      escapeHTML(text(report.rule_version, '—')) +
      '</dd>' +
      '<dt>解读版本</dt><dd>' +
      escapeHTML(text(report.prompt_version, '—')) +
      '</dd>' +
      '<dt>快照指纹</dt><dd>' +
      escapeHTML(text(report.snapshot_sha256, '—').slice(0, 16)) +
      '</dd>' +
      '<dt>报告文件</dt><dd>' +
      escapeHTML(text(detail.html_path, '—')) +
      '</dd>' +
      '</dl></div>' +
      '<div class="summary-card" style="margin-top:10px"><h2>Markdown 摘要</h2>' +
      '<pre class="source">' +
      escapeHTML(markdown) +
      '</pre></div>'
    );
  }

  function renderSource(detail) {
    const html = text(detail.html, '');
    return (
      '<div class="row-actions">' +
      '<button type="button" class="btn" id="copy-html">复制 HTML</button>' +
      '<button type="button" class="btn" id="copy-path">复制报告路径</button>' +
      '<span class="badge">' +
      escapeHTML(html.length) +
      ' 字符 · 自包含</span></div>' +
      '<pre class="source">' +
      escapeHTML(html.slice(0, 22000)) +
      (html.length > 22000 ? '\n… 已截断展示，完整内容见报告文件' : '') +
      '</pre>'
    );
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

  function errorBlock(message) {
    return (
      '<div class="error"><strong>无法读取归档</strong><p>' +
      escapeHTML(message) +
      '</p><p class="meta">可先点右上角“采集一期”生成第一期，或检查工作流是否已运行。</p></div>'
    );
  }

  function renderPanel() {
    for (const tab of tabs) {
      const button = document.querySelector('.tab[data-tab="' + tab + '"]');
      if (button) button.classList.toggle('active', tab === state.tab);
      el('panel-' + tab).hidden = tab !== state.tab;
    }
    if (state.error) {
      el('panel-' + state.tab).innerHTML = errorBlock(state.error);
      return;
    }
    if ('ranking' === state.tab) {
      el('panel-ranking').innerHTML = state.detail ? renderRanking(state.detail) : skeleton();
    } else if ('summary' === state.tab) {
      el('panel-summary').innerHTML = state.detail ? renderSummary(state.detail) : skeleton();
    } else {
      el('panel-source').innerHTML = state.detail ? renderSource(state.detail) : skeleton();
    }
    const copyHTML = el('copy-html');
    if (copyHTML) {
      copyHTML.addEventListener('click', () => copyText(text(state.detail && state.detail.html), '已复制报告 HTML'));
    }
    const copyPath = el('copy-path');
    if (copyPath) {
      copyPath.addEventListener('click', () =>
        copyText(text(state.detail && state.detail.html_path), '已复制报告路径'),
      );
    }
  }

  function skeleton() {
    return '<div class="skeleton"><div></div><div></div><div></div></div>';
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
      /* 剪贴板不可用时静默忽略，用户仍可手动选择文本。 */
    }
    document.body.removeChild(area);
    done();
  }

  function applyDetail(detail) {
    state.detail = detail;
    const report = detail.report || {};
    el('detail-title').textContent = detail.report_date + ' · 科技雷达';
    el('detail-subtitle').textContent = text(
      report.summary,
      '本期为规则稿，未包含智能解读。',
    );
    const record = state.records.find((item) => item.report_date === detail.report_date);
    renderBadges(record, report);
    renderPanel();
  }

  function applyEmpty(title, message, lines) {
    state.detail = null;
    el('detail-title').textContent = title;
    el('detail-subtitle').textContent = message;
    el('detail-badges').innerHTML = '';
    for (const tab of tabs) {
      el('panel-' + tab).innerHTML = emptyBlock(title, lines);
    }
  }

  async function loadArchive(preferredDate) {
    const payload = await bridgeInvoke('tech-radar.archive.list', { top_n: 60 });
    state.records = (payload && payload.records) || [];
    state.dataRoot = (payload && payload.data_root) || '';
    const root = el('data-root');
    root.textContent = state.dataRoot ? '数据目录 · ' + state.dataRoot : '';
    root.title = state.dataRoot;
    renderArchiveList();
    if (!state.records.length) {
      state.selected = '';
      applyEmpty('还没有归档报告', '运行“科技雷达日报”工作流后，这里会出现每天的归档。', [
        '工作流入口：采集 → 解读 → 渲染归档，报告落在这个扩展自己的数据目录。',
        '也可以用右上角“采集一期”手动生成一期规则稿，先验证链路。',
      ]);
      return;
    }
    const target =
      (preferredDate && state.records.some((item) => item.report_date === preferredDate) && preferredDate) ||
      (state.records.some((item) => item.report_date === state.selected) && state.selected) ||
      state.records[0].report_date;
    state.selected = target;
    renderArchiveList();
    await selectDate(target);
  }

  async function selectDate(date) {
    state.selected = date;
    state.error = '';
    renderArchiveList();
    renderPanel();
    try {
      const detail = await bridgeInvoke('tech-radar.archive.read', { report_date: date });
      applyDetail(detail);
    } catch (error) {
      state.error = String((error && error.message) || error);
      el('detail-title').textContent = date + ' · 读取失败';
      el('detail-subtitle').textContent = state.error;
      renderPanel();
    }
  }

  async function refresh(preferredDate) {
    try {
      notify('');
      await loadArchive(preferredDate);
    } catch (error) {
      if (error && error.code === 'bridge_unavailable') {
        applyEmpty('请从 HiMind Agent 打开本视图', '页面需要宿主桥接才能读取归档数据。', [
          '从 Agent 的扩展入口或快速入口打开“科技雷达 · 历史归档”。',
          '归档数据保存在扩展自己的数据目录，页面不直接读文件系统。',
        ]);
        return;
      }
      state.error = String((error && error.message) || error);
      el('detail-title').textContent = '读取失败';
      el('detail-subtitle').textContent = state.error;
      renderPanel();
    }
  }

  async function collectNow() {
    const topics = el('topics')
      .value.split(/[,，\s]+/)
      .map((item) => item.trim())
      .filter(Boolean);
    if (!topics.length) {
      notify('请先填写至少一个主题，例如 ai-agent。', 'warn');
      return;
    }
    const windowDays = Number(el('window').value) || 14;
    const topN = Number(el('topn').value) || 12;
    setBusy(true, '采集中…');
    try {
      notify('正在采集公开热点仓库…');
      const snapshot = await bridgeInvoke('tech-radar.collect', {
        topics: topics,
        window_days: windowDays,
        top_n: topN,
      });
      const result = await bridgeInvoke('tech-radar.report', {
        snapshot: snapshot,
        summary: '手动采集 · ' + topics.join('/'),
      });
      const date = (result && result.report && result.report.report_date) || '';
      notify('已生成 ' + date + ' 的规则稿，等待工作流补充智能解读。');
      await refresh(date);
    } catch (error) {
      notify('采集失败：' + String((error && error.message) || error), 'error');
    } finally {
      setBusy(false);
    }
  }

  function bind() {
    el('refresh').addEventListener('click', () => refresh());
    el('collect').addEventListener('click', collectNow);
    el('archive-list').addEventListener('click', (event) => {
      const button = event.target.closest('.archive-item');
      if (button) selectDate(button.getAttribute('data-date'));
    });
    el('panel-ranking').addEventListener('click', (event) => {
      const repo = event.target.closest('.repo');
      if (!repo) return;
      const url = repo.getAttribute('data-url');
      openLink(url).then((opened) => {
        if (!opened) copyText(url, '宿主未能打开链接，已复制仓库地址');
      });
    });
    for (const tab of tabs) {
      const button = document.querySelector('.tab[data-tab="' + tab + '"]');
      if (button) {
        button.addEventListener('click', () => {
          state.tab = tab;
          renderPanel();
        });
      }
    }
  }

  bind();
  renderPanel();
  refresh();
  window.__TECH_RADAR_VIEW__ = { PLUGIN_ID: PLUGIN_ID, refresh: refresh };
})();
