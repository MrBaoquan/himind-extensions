/**
 * 开源热榜自带视图。
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
    periods: [],
    selected: '',
    selectedCollection: 'all',
    detail: null,
    periodDetails: [],
    periodErrors: [],
    dataRoot: '',
    tab: 'ranking',
    busy: false,
    themes: [],
    tags: [],
    revision: 0,
    selectedThemes: new Set(),
    maxQueries: 24,
    maxCategoryTopics: 12,
    intervalMs: 7000,
    customTopicPattern: '^[a-z0-9][a-z0-9-]{0,49}$',
    categoryDraft: null,
    categoryUsage: {},
    categoryUsageAvailable: true,
    categoryBusy: false,
  };
  const DEFAULT_THEMES = ['ai-short-drama', 'ai-video', 'code-animation', 'creator-tools'];

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

  function getCustomTopics() {
    return el('topics')
      .value.split(/[,，\s]+/)
      .map((item) => item.trim().toLowerCase())
      .filter(Boolean);
  }

  function themeById(id) {
    return state.themes.find((theme) => theme.id === id) || null;
  }

  function renderThemePicker() {
    const holder = el('theme-picker');
    holder.innerHTML = state.themes
      .map((theme) => {
        const active = state.selectedThemes.has(theme.id);
        return (
          '<button type="button" class="theme-chip' +
          (active ? ' active' : '') +
          '" data-theme-id="' +
          escapeHTML(theme.id) +
          '" aria-pressed="' +
          (active ? 'true' : 'false') +
          '"><strong>' +
          escapeHTML(theme.label || theme.id) +
          '<span class="theme-source">' + (theme.source === 'user' ? '我的' : '系统') + '</span></strong><small>' +
          escapeHTML(theme.query_count || (theme.topics || []).length * 2) +
          ' 次查询 · ' + escapeHTML((theme.topics || []).length) + ' 个标签</small></button>'
        );
      })
      .join('');
    renderBudget();
  }

  function renderBudget() {
    const custom = getCustomTopics();
    const pattern = new RegExp(state.customTopicPattern);
    const invalid = custom.find((topic) => !pattern.test(topic));
    const topics = new Set();
    let queryCount = 0;
    state.themes.forEach((theme) => {
      if (!state.selectedThemes.has(theme.id)) return;
      queryCount += Number(theme.query_count) || 0;
      (theme.topics || []).forEach((topic) => topics.add(topic));
    });
    custom.forEach((topic) => topics.add(topic));
    queryCount = topics.size * 2;
    const seconds = queryCount > 0 ? Math.ceil(((queryCount - 1) * state.intervalMs) / 1000) : 0;
    const node = el('budget');
    node.className = 'budget-preview' + (invalid ? ' error' : queryCount > state.maxQueries ? ' blocked' : '');
    if (invalid) {
      node.textContent = '额外 topic 不合法：' + invalid;
    } else if (!topics.size) {
      node.textContent = '请选择至少一个分类，或填写额外 topic';
    } else if (queryCount > state.maxQueries) {
      node.textContent = '预计 ' + queryCount + '/' + state.maxQueries + ' 次查询，已超过上限';
    } else {
      node.textContent = '预计 ' + queryCount + '/' + state.maxQueries + ' 次查询 · 未鉴权约 ' + seconds + ' 秒';
    }
  }

  function applyThemeCatalog(catalog, preserveSelection) {
    const previous = state.selectedThemes;
    state.themes = Array.isArray(catalog && catalog.themes) ? catalog.themes : [];
    state.tags = Array.isArray(catalog && catalog.tags) ? catalog.tags : [];
    state.revision = Number(catalog && catalog.revision) || 0;
    state.maxQueries = Number(catalog && catalog.max_queries) || 24;
    state.maxCategoryTopics = Number(catalog && catalog.max_category_topics) || 12;
    state.intervalMs = Number(catalog && catalog.query_interval_ms) || 7000;
    state.customTopicPattern = (catalog && catalog.custom_topic_pattern) || state.customTopicPattern;
    const available = new Set(state.themes.map((theme) => theme.id));
    if (preserveSelection) {
      state.selectedThemes = new Set(Array.from(previous).filter((id) => available.has(id)));
    }
    if (!state.selectedThemes.size) {
      const defaults = DEFAULT_THEMES.filter((id) => available.has(id));
      state.selectedThemes = new Set(defaults.length ? defaults : state.themes.slice(0, 1).map((theme) => theme.id));
    }
    renderThemePicker();
    if (state.categoryDraft) renderCategoryEditor();
  }

  async function loadThemes(preserveSelection) {
    const catalog = await bridgeInvoke('tech-radar.themes', {});
    applyThemeCatalog(catalog, Boolean(preserveSelection));
    return catalog;
  }
  function categoryById(id) {
    return state.themes.find((theme) => theme.id === id) || null;
  }

  function categoryUsageText(id) {
    const count = Number(state.categoryUsage[id] || 0);
    return count ? `被 ${count} 个计划引用` : '尚未被计划引用';
  }

  function renderCategoryList() {
    const list = el('category-list');
    const activeId = state.categoryDraft ? state.categoryDraft.originId : '';
    list.innerHTML = state.themes.map((theme) => {
      const source = theme.source === 'user' ? '我的' : '系统';
      const actions = theme.source === 'user'
        ? '<button type="button" class="btn mini" data-copy-id="' + escapeHTML(theme.id) + '">复制</button>'
        : '<button type="button" class="btn mini" data-copy-id="' + escapeHTML(theme.id) + '">复制</button>';
      return (
        '<div class="category-card' + (theme.id === activeId ? ' active' : '') + '" data-category-id="' + escapeHTML(theme.id) + '" tabindex="0">' +
        '<strong>' + escapeHTML(theme.label || theme.id) + '<span class="theme-source">' + source + '</span></strong>' +
        '<small>' + escapeHTML((theme.topics || []).length) + ' 个标签 · ' + escapeHTML((theme.topics || []).length * 2) + ' 次查询 · ' + escapeHTML(categoryUsageText(theme.id)) + '</small>' +
        '<div class="category-card-actions">' + actions + '</div>' +
        '</div>'
      );
    }).join('');
  }

  function beginCategoryDraft(category, copy) {
    const base = category || null;
    const isCopy = Boolean(copy || (base && base.source === 'system'));
    const customTags = {};
    if (base) {
      (base.topics || []).forEach((slug) => {
        const tag = state.tags.find((item) => item.slug === slug);
        if (tag && tag.source === 'custom') customTags[slug] = tag.label;
      });
    }
    state.categoryDraft = {
      id: base && base.source === 'user' && !isCopy ? base.id : '',
      originId: base ? base.id : '',
      source: isCopy ? 'copy' : (base ? base.source : 'user'),
      label: base ? (isCopy ? base.label + '（副本）' : base.label) : '',
      description: base ? (base.description || '') : '',
      topics: base ? Array.from(base.topics || []) : [],
      customTags,
    };
    renderCategoryList();
    renderCategoryEditor();
  }

  function renderCategoryEditor() {
    const draft = state.categoryDraft;
    const empty = el('category-editor-empty');
    const form = el('category-editor-form');
    if (!draft) {
      empty.hidden = false;
      form.hidden = true;
      return;
    }
    empty.hidden = true;
    form.hidden = false;
    const isEdit = Boolean(draft.id);
    el('category-editor-title').textContent = isEdit ? '编辑分类' : (draft.source === 'copy' ? '复制内置分类' : '新建分类');
    el('category-name').value = draft.label;
    el('category-description').value = draft.description;
    el('category-usage').textContent = draft.originId ? categoryUsageText(draft.originId) : '尚未被计划引用';
    el('category-delete').hidden = !isEdit;
    el('category-duplicate').hidden = !draft.originId;
    el('category-delete').disabled = state.categoryBusy || !state.categoryUsageAvailable || Number(state.categoryUsage[draft.originId] || 0) > 0;
    el('category-save').disabled = state.categoryBusy;

    const selectedHolder = el('category-selected-tags');
    if (!draft.topics.length) {
      selectedHolder.innerHTML = '<span class="tag-slug">还没有选择标签</span>';
    } else {
      selectedHolder.innerHTML = draft.topics.map((slug) => {
        const tag = state.tags.find((item) => item.slug === slug) || { slug, label: slug, source: 'custom' };
        return '<button type="button" class="tag-chip selected ' + escapeHTML(tag.source || 'custom') + '" data-remove-tag="' + escapeHTML(slug) + '">' +
          escapeHTML(tag.label || slug) + '<span class="tag-slug">' + escapeHTML(slug) + '</span>×</button>';
      }).join('');
    }
    el('category-tag-library').innerHTML = state.tags.map((tag) => {
      const selected = draft.topics.includes(tag.slug);
      return '<button type="button" class="tag-chip ' + (selected ? 'selected ' : '') + escapeHTML(tag.source || 'custom') + '" data-tag-slug="' + escapeHTML(tag.slug) + '">' +
        escapeHTML(tag.label || tag.slug) + '<span class="tag-slug">' + escapeHTML(tag.slug) + '</span></button>';
    }).join('');
    const queryCount = draft.topics.length * 2;
    el('category-budget').textContent = draft.topics.length + ' 个唯一标签 · ' + queryCount + ' 次查询' +
      (draft.topics.length > state.maxCategoryTopics ? ' · 超过单分类上限' : '');
    const error = el('category-error');
    if (draft.topics.length > state.maxCategoryTopics) {
      error.hidden = false;
      error.textContent = '单个分类最多 ' + state.maxCategoryTopics + ' 个唯一 topic';
    } else if (error.dataset.source === 'draft') {
      error.hidden = true;
      error.textContent = '';
    }
  }

  function setCategoryError(message) {
    const error = el('category-error');
    error.dataset.source = message ? 'draft' : '';
    error.textContent = message;
    error.hidden = !message;
  }

  async function loadCategoryUsage() {
    try {
      const internals = window.__TAURI_INTERNALS__ || {};
      if (typeof internals.invoke !== 'function') throw new Error('宿主桥接不可用');
      const usage = await internals.invoke('get_plugin_view_schedule_reference_counts');
      state.categoryUsage = usage && typeof usage === 'object' ? usage : {};
      state.categoryUsageAvailable = true;
      setCategoryError('');
    } catch (error) {
      state.categoryUsage = {};
      state.categoryUsageAvailable = false;
      setCategoryError('计划引用检查暂时不可用；为避免误删，用户分类删除已禁用。');
      return false;
    }
    return true;
  }

  async function openCategoryManager() {
    el('category-modal').hidden = false;
    state.categoryDraft = null;
    renderCategoryEditor();
    try {
      await loadThemes(true);
      await loadCategoryUsage();
      renderCategoryList();
    } catch (error) {
      setCategoryError('分类目录读取失败：' + String((error && error.message) || error));
    }
  }

  function closeCategoryManager() {
    el('category-modal').hidden = true;
    state.categoryDraft = null;
    setCategoryError('');
  }

  function toggleDraftTopic(slug) {
    if (!state.categoryDraft) return;
    const topics = state.categoryDraft.topics;
    const index = topics.indexOf(slug);
    if (index >= 0) topics.splice(index, 1);
    else topics.push(slug);
    renderCategoryEditor();
  }

  function addCustomTag() {
    if (!state.categoryDraft) return;
    const slugInput = el('custom-tag-slug');
    const slug = slugInput.value.trim().toLowerCase();
    const label = el('custom-tag-label').value.trim() || slug;
    const pattern = new RegExp(state.customTopicPattern);
    if (!pattern.test(slug)) {
      setCategoryError('自定义 topic 只允许小写字母、数字和连字符，长度 1–50');
      return;
    }
    const existing = state.tags.find((tag) => tag.slug === slug);
    if (existing && existing.source === 'system') {
      setCategoryError('“' + slug + '”已是系统标签，直接从标签库选择即可。');
      return;
    }
    if (existing) existing.label = label;
    else state.tags.push({ slug, label, source: 'custom' });
    state.categoryDraft.customTags[slug] = label;
    if (!state.categoryDraft.topics.includes(slug)) state.categoryDraft.topics.push(slug);
    slugInput.value = '';
    el('custom-tag-label').value = '';
    setCategoryError('');
    renderCategoryEditor();
  }

  async function saveCategoryDraft() {
    const draft = state.categoryDraft;
    if (!draft || state.categoryBusy) return;
    const label = el('category-name').value.trim();
    if (!label) {
      setCategoryError('分类名称不能为空');
      return;
    }
    if (!draft.topics.length) {
      setCategoryError('分类至少需要一个标签主题');
      return;
    }
    if (draft.topics.length > state.maxCategoryTopics) {
      setCategoryError('单个分类最多 ' + state.maxCategoryTopics + ' 个唯一 topic');
      return;
    }
    state.categoryBusy = true;
    renderCategoryEditor();
    try {
      const payload = {
        id: draft.id,
        label,
        description: el('category-description').value.trim(),
        topics: draft.topics,
        custom_tags: Object.keys(draft.customTags).filter((slug) => draft.topics.includes(slug)).map((slug) => ({
          slug,
          label: draft.customTags[slug] || slug,
          source: 'custom',
        })),
        expected_revision: state.revision,
      };
      const catalog = await bridgeInvoke('tech-radar.categories.save', payload);
      applyThemeCatalog(catalog, true);
      const savedId = catalog && catalog.saved_id ? String(catalog.saved_id) : '';
      const saved = categoryById(savedId);
      await loadCategoryUsage();
      state.categoryDraft = null;
      if (saved) beginCategoryDraft(saved, false);
      else renderCategoryList();
      notify('分类已保存。');
    } catch (error) {
      setCategoryError(String((error && error.message) || error));
    } finally {
      state.categoryBusy = false;
      renderCategoryEditor();
      renderCategoryList();
    }
  }

  async function deleteCategoryDraft() {
    const draft = state.categoryDraft;
    if (!draft || !draft.id || state.categoryBusy) return;
    const usage = Number(state.categoryUsage[draft.id] || 0);
    if (usage > 0) {
      setCategoryError('这个分类正被 ' + usage + ' 个计划引用，请先调整计划后再删除。');
      return;
    }
    state.categoryBusy = true;
    renderCategoryEditor();
    try {
      const catalog = await bridgeInvoke('tech-radar.categories.delete', {
        id: draft.id,
        expected_revision: state.revision,
      });
      applyThemeCatalog(catalog, true);
      state.categoryDraft = null;
      renderCategoryEditor();
      renderCategoryList();
      notify('分类已删除。');
    } catch (error) {
      setCategoryError(String((error && error.message) || error));
    } finally {
      state.categoryBusy = false;
      renderCategoryEditor();
    }
  }
  function groupKey(record) {
    return text(record.collection_id) || '__legacy__';
  }

  function groupLabel(record) {
    return text(record.collection_label, text(record.collection_id, '旧版报告'));
  }

  function buildPeriods(records) {
    const byDate = new Map();
    (records || []).forEach((record) => {
      const date = text(record.report_date);
      if (!date) return;
      if (!byDate.has(date)) byDate.set(date, []);
      byDate.get(date).push(record);
    });
    return Array.from(byDate, ([date, reports]) => {
      const ordered = reports.slice().sort((left, right) => {
        const leftLegacy = !text(left.collection_id);
        const rightLegacy = !text(right.collection_id);
        if (leftLegacy !== rightLegacy) return leftLegacy ? 1 : -1;
        return groupLabel(left).localeCompare(groupLabel(right), 'zh-CN');
      });
      return { date: date, records: ordered };
    }).sort((left, right) => right.date.localeCompare(left.date));
  }

  function currentPeriod(date) {
    const wanted = text(date, state.selected);
    return state.periods.find((period) => period.date === wanted) || null;
  }

  function aggregateDetails() {
    const named = state.periodDetails.filter((detail) => text(detail.record && detail.record.collection_id));
    return named.length ? named : state.periodDetails;
  }

  function activeDetail() {
    return state.periodDetails.find((detail) => detail.group_key === state.selectedCollection) || null;
  }

  function renderArchiveList() {
    const list = el('archive-list');
    el('archive-count').textContent = state.periods.length ? state.periods.length + ' 期' : '暂无';
    list.innerHTML = state.periods.map((period) => {
      const named = period.records.filter((record) => text(record.collection_id));
      const legacy = period.records.filter((record) => !text(record.collection_id));
      const visibleRecords = named.length ? named : period.records;
      const totalEntries = visibleRecords.reduce((sum, record) => sum + (Number(record.entry_count) || 0), 0);
      const single = period.records.length === 1;
      const newBadge = single && period.records[0].new_entry_count
        ? '<strong>+' + escapeHTML(period.records[0].new_entry_count) + ' 新上榜</strong>'
        : '';
      const groupText = single
        ? groupLabel(period.records[0])
        : (named.length ? named.length + ' 个采集组' : '旧版报告') + (legacy.length ? ' · 含旧版' : '');
      return (
        '<li><button type="button" class="archive-item' +
        (period.date === state.selected ? ' active' : '') +
        '" data-date="' +
        escapeHTML(period.date) +
        '">' +
        '<span class="row"><span class="date">' +
        escapeHTML(period.date) +
        '</span><span class="pill">' +
        escapeHTML(totalEntries) +
        ' 条</span></span>' +
        '<span class="meta">' +
        escapeHTML(groupText) +
        (newBadge ? ' · ' + newBadge : '') +
        '</span></button></li>'
      );
    }).join('');
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
    const ruleVersion = (report && report.rule_version) || (record && record.rule_version);
    if (ruleVersion) badges.push('<span class="badge">' + escapeHTML(ruleVersion) + '</span>');
    el('detail-badges').innerHTML = badges.join('');
  }

  function renderPeriodBadges() {
    if (state.selectedCollection !== 'all') {
      const detail = activeDetail();
      if (!detail) {
        el('detail-badges').innerHTML = '';
        return;
      }
      renderBadges(detail.record, detail.report || {});
      return;
    }
    const details = aggregateDetails();
    const period = currentPeriod();
    const namedCount = period
      ? period.records.filter((record) => text(record.collection_id)).length
      : details.length;
    const reportList = details.map((detail) => detail.report || {});
    const versions = Array.from(new Set(reportList.map((report) => text(report.rule_version)).filter(Boolean)));
    const badges = [
      '<span class="badge">' + escapeHTML(namedCount) + ' 个采集组</span>',
      '<span class="badge">' + escapeHTML(buildEntries(details, true).length) + ' 个唯一仓库</span>',
    ];
    if (reportList.some((report) => report.degraded)) badges.push('<span class="badge warn">含规则稿</span>');
    else badges.push('<span class="badge new">含智能解读</span>');
    if (versions.length === 1) badges.push('<span class="badge">' + escapeHTML(versions[0]) + '</span>');
    else if (versions.length > 1) badges.push('<span class="badge">' + escapeHTML(versions.length) + ' 个规则版本</span>');
    if (state.periodErrors.some((failure) => text(failure.record && failure.record.collection_id))) {
      badges.push('<span class="badge warn">部分采集组读取失败</span>');
    }
    el('detail-badges').innerHTML = badges.join('');
  }

  function insightFor(detail, entryId) {
    const insights = (detail && detail.insight && detail.insight.insights) || [];
    const matched = insights.find((item) => item && item.entry_id === entryId);
    return matched && matched.reason ? String(matched.reason) : '';
  }

  function buildEntries(details, sortByStars) {
    const merged = new Map();
    (details || []).forEach((detail) => {
      const label = text(detail.group_label, '采集组');
      const entries = (detail.snapshot && detail.snapshot.entries) || [];
      entries.forEach((entry) => {
        const key = text(entry.entry_id, entry.full_name);
        if (!key) return;
        let item = merged.get(key);
        if (!item) {
          item = Object.assign({}, entry, {
            __group_labels: new Set(),
            __reason: insightFor(detail, entry.entry_id),
          });
          merged.set(key, item);
        }
        item.__group_labels.add(label);
        if (!item.__reason) item.__reason = insightFor(detail, entry.entry_id);
        if (Number(entry.stars || 0) > Number(item.stars || 0)) item.stars = entry.stars;
        if (Number(entry.stars_gained || 0) > Number(item.stars_gained || 0)) item.stars_gained = entry.stars_gained;
      });
    });
    const result = Array.from(merged.values()).map((entry) => {
      const copy = Object.assign({}, entry, { __group_labels: Array.from(entry.__group_labels) });
      delete copy.__reason;
      copy.__reason = entry.__reason;
      return copy;
    });
    if (sortByStars) result.sort((left, right) => (Number(right.stars) || 0) - (Number(left.stars) || 0));
    return result;
  }

  function renderEntryList(entries) {
    if (!entries.length) {
      return emptyBlock('本期没有条目', ['采集范围可能过窄，或窗口时间太短。']);
    }
    const items = entries.map((entry, index) => {
      const badges = [];
      if (entry.is_new) badges.push('<span class="badge new">新上榜</span>');
      else if (entry.rank_delta > 0) badges.push('<span class="badge new">↑' + escapeHTML(entry.rank_delta) + '</span>');
      else if (entry.rank_delta < 0) badges.push('<span class="badge">↓' + escapeHTML(-entry.rank_delta) + '</span>');
      const groups = entry.__group_labels || [];
      return (
        '<li class="entry">' +
        '<span class="rank">' + (index + 1) + '</span>' +
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
        (entry.__reason ? '<p class="reason">' + escapeHTML(entry.__reason) + '</p>' : '') +
        '<p class="meta">' +
        escapeHTML(text(entry.language, '未标注语言')) +
        (entry.topics && entry.topics.length ? ' · ' + escapeHTML(entry.topics.slice(0, 4).join(', ')) : '') +
        '</p>' +
        (groups.length
          ? '<p class="entry-groups">' + groups.map((group) => '<span>' + escapeHTML(group) + '</span>').join('') + '</p>'
          : '') +
        '</div>' +
        '<span class="stars">⭐ ' +
        escapeHTML(entry.stars || 0) +
        (entry.stars_gained ? ' (+' + escapeHTML(entry.stars_gained) + ')' : '') +
        '</span>' +
        '</li>'
      );
    }).join('');
    return '<ul class="entry-list">' + items + '</ul>';
  }

  function renderRanking(detail) {
    return renderEntryList(buildEntries([detail], false));
  }

  function renderAggregateRanking() {
    return renderEntryList(buildEntries(aggregateDetails(), true));
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
      '<dt>采集组</dt><dd>' + escapeHTML(detail.group_label || '—') + '</dd>' +
      '<dt>报告日期</dt><dd>' + escapeHTML(detail.report_date) + '</dd>' +
      '<dt>条目 / 新上榜</dt><dd>' +
      escapeHTML(report.entry_count || 0) + ' / ' + escapeHTML(report.new_entry_count || 0) +
      '</dd>' +
      '<dt>规则版本</dt><dd>' + escapeHTML(text(report.rule_version, '—')) + '</dd>' +
      '<dt>解读版本</dt><dd>' + escapeHTML(text(report.prompt_version, '—')) + '</dd>' +
      '<dt>快照指纹</dt><dd>' + escapeHTML(text(report.snapshot_sha256, '—').slice(0, 16)) + '</dd>' +
      '<dt>报告文件</dt><dd>' + escapeHTML(text(detail.html_path, '—')) + '</dd>' +
      '</dl></div>' +
      '<div class="summary-card" style="margin-top:10px"><h2>Markdown 摘要</h2>' +
      '<pre class="source">' + escapeHTML(markdown) + '</pre></div>'
    );
  }

  function renderAggregateSummary() {
    const details = aggregateDetails();
    const failed = state.periodErrors.filter((failure) => text(failure.record && failure.record.collection_id));
    return (
      '<div class="group-summary-grid">' +
      details.map((detail) => {
        const report = detail.report || {};
        return (
          '<button type="button" class="group-summary-card" data-open-group="' +
          escapeHTML(detail.group_key) +
          '">' +
          '<span class="group-summary-head"><strong>' + escapeHTML(detail.group_label) + '</strong>' +
          '<span class="pill">' + escapeHTML(report.entry_count || 0) + ' 条</span></span>' +
          '<p>' + escapeHTML(text(report.summary, '本期没有生成一句话总结。')) + '</p>' +
          '<span class="group-summary-meta">' +
          escapeHTML(text(report.rule_version, '—')) +
          ' · ' +
          escapeHTML(report.degraded ? '规则稿' : '含智能解读') +
          '</span></button>'
        );
      }).join('') +
      failed.map((failure) =>
        '<div class="group-summary-card failed"><strong>' + escapeHTML(failure.group_label) +
        '</strong><p>读取失败：' + escapeHTML(failure.message) + '</p></div>',
      ).join('') +
      '</div>'
    );
  }

  function renderSource(detail) {
    const html = text(detail.html, '');
    return (
      '<div class="row-actions">' +
      '<button type="button" class="btn" id="copy-html">复制 HTML</button>' +
      '<button type="button" class="btn" id="copy-path">复制报告路径</button>' +
      '<span class="badge">' + escapeHTML(html.length) + ' 字符 · 自包含</span></div>' +
      '<pre class="source">' +
      escapeHTML(html.slice(0, 22000)) +
      (html.length > 22000 ? '\n… 已截断展示，完整内容见报告文件' : '') +
      '</pre>'
    );
  }

  function renderAggregateSource() {
    const details = aggregateDetails();
    return (
      '<div class="row-actions"><span class="badge">' +
      escapeHTML(details.length) +
      ' 份独立报告 · 合并阅读不改变原始文件</span></div>' +
      '<div class="period-report-list">' +
      details.map((detail) => {
        const report = detail.report || {};
        return (
          '<article class="report-card"><div><strong>' +
          escapeHTML(detail.group_label) +
          '</strong><p>' +
          escapeHTML(text(detail.html_path, '—')) +
          '</p></div><div class="report-card-meta"><span class="badge">' +
          escapeHTML(text(report.rule_version, '—')) +
          '</span><span class="badge">' +
          escapeHTML(text(detail.html, '').length) +
          ' 字符</span></div><button type="button" class="btn" data-open-group="' +
          escapeHTML(detail.group_key) +
          '">查看该组原稿</button></article>'
        );
      }).join('') +
      '</div>'
    );
  }

  function emptyBlock(title, lines) {
    return (
      '<div class="empty"><strong>' + escapeHTML(title) + '</strong><ul>' +
      (lines || []).map((line) => '<li>' + escapeHTML(line) + '</li>').join('') +
      '</ul></div>'
    );
  }

  function errorBlock(message) {
    return (
      '<div class="error"><strong>无法读取归档</strong><p>' +
      escapeHTML(message) +
      '</p><p class="meta">请检查“开源热榜日报”定时计划是否已运行，或点右上角“刷新”重试。</p></div>'
    );
  }

  function renderGroupSwitcher() {
    const holder = el('group-switcher');
    if (!holder) return;
    const period = currentPeriod();
    if (!period || period.records.length <= 1) {
      holder.hidden = true;
      holder.innerHTML = '';
      return;
    }
    const named = period.records.filter((record) => text(record.collection_id));
    const failedKeys = new Set(state.periodErrors.map((failure) => failure.group_key));
    const buttons = [];
    if (named.length > 1) {
      buttons.push(
        '<button type="button" class="group-tab' +
        (state.selectedCollection === 'all' ? ' active' : '') +
        '" data-group-key="all"><span>全部</span><small>' + escapeHTML(named.length) + ' 组</small></button>',
      );
    }
    period.records.forEach((record) => {
      const key = groupKey(record);
      const label = groupLabel(record);
      const failed = failedKeys.has(key);
      buttons.push(
        '<button type="button" class="group-tab' +
        (state.selectedCollection === key ? ' active' : '') +
        '" data-group-key="' + escapeHTML(key) + '"><span>' + escapeHTML(label) +
        '</span><small>' + (failed ? '读取失败' : escapeHTML(record.entry_count || 0) + ' 条') + '</small></button>',
      );
    });
    holder.innerHTML = buttons.join('');
    holder.hidden = false;
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
    const aggregate = state.selectedCollection === 'all';
    if (state.tab === 'ranking') {
      el('panel-ranking').innerHTML = aggregate
        ? renderAggregateRanking()
        : (state.detail ? renderRanking(state.detail) : skeleton());
    } else if (state.tab === 'summary') {
      el('panel-summary').innerHTML = aggregate
        ? renderAggregateSummary()
        : (state.detail ? renderSummary(state.detail) : skeleton());
    } else {
      el('panel-source').innerHTML = aggregate
        ? renderAggregateSource()
        : (state.detail ? renderSource(state.detail) : skeleton());
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

  function applyPeriod() {
    const period = currentPeriod();
    if (!period) {
      applyEmpty('还没有归档报告', '运行“开源热榜日报”定时计划后，这里会出现每天的归档。', []);
      return;
    }
    state.detail = state.selectedCollection === 'all' ? null : activeDetail();
    el('detail-title').textContent = period.date + ' · 开源热榜';
    if (state.selectedCollection === 'all') {
      const details = aggregateDetails();
      const labels = details.map((detail) => detail.group_label);
      const legacy = period.records.some((record) => !text(record.collection_id));
      const failedCount = state.periodErrors.filter((failure) => text(failure.record && failure.record.collection_id)).length;
      el('detail-subtitle').textContent =
        '本期包含 ' + (labels.length + failedCount) + ' 个采集组：' + labels.join(' / ') +
        (failedCount ? '；' + failedCount + ' 个读取失败' : '') +
        (legacy ? '；另含旧版报告' : '');
    } else if (state.detail) {
      const report = state.detail.report || {};
      el('detail-subtitle').textContent = text(report.summary, '本期为规则稿，未包含智能解读。');
    } else {
      const failure = state.periodErrors.find((item) => item.group_key === state.selectedCollection);
      el('detail-subtitle').textContent = failure ? '该采集组读取失败：' + failure.message : '该采集组暂时没有数据。';
    }
    renderGroupSwitcher();
    renderPeriodBadges();
    renderPanel();
  }

  function selectGroup(key) {
    state.selectedCollection = text(key, 'all');
    state.error = '';
    applyPeriod();
  }

  function applyEmpty(title, message, lines) {
    state.periods = [];
    state.periodDetails = [];
    state.periodErrors = [];
    state.selectedCollection = '';
    state.detail = null;
    const switcher = el('group-switcher');
    if (switcher) {
      switcher.hidden = true;
      switcher.innerHTML = '';
    }
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
    state.periods = buildPeriods(state.records);
    state.dataRoot = (payload && payload.data_root) || '';
    const root = el('data-root');
    root.textContent = state.dataRoot ? '数据目录 · ' + state.dataRoot : '';
    root.title = state.dataRoot;
    if (!state.periods.length) {
      state.selected = '';
      renderArchiveList();
      applyEmpty('还没有归档报告', '运行“开源热榜日报”定时计划后，这里会出现每天的归档。', [
        '工作流入口：采集 → 解读 → 渲染归档，报告落在这个扩展自己的数据目录。',
        '归档只由定时计划生成；可在计划页调整采集组，再等待下一次运行。',
      ]);
      return;
    }
    const targetPeriod =
      (preferredDate && state.periods.find((period) => period.date === preferredDate)) ||
      state.periods.find((period) => period.date === state.selected) ||
      state.periods[0];
    state.selected = targetPeriod.date;
    renderArchiveList();
    await selectDate(targetPeriod.date);
  }

  async function selectDate(date) {
    const period = currentPeriod(date);
    if (!period) return;
    state.selected = period.date;
    state.error = '';
    state.periodDetails = [];
    state.periodErrors = [];
    const named = period.records.filter((record) => text(record.collection_id));
    if (named.length > 1) {
      state.selectedCollection = 'all';
    } else {
      const primary = named[0] || period.records[0];
      state.selectedCollection = groupKey(primary);
    }
    el('detail-title').textContent = period.date + ' · 开源热榜';
    el('detail-subtitle').textContent = '正在读取本期采集组…';
    el('detail-badges').innerHTML = '';
    renderArchiveList();
    renderGroupSwitcher();
    renderPanel();
    const settled = await Promise.allSettled(period.records.map((record) =>
      bridgeInvoke('tech-radar.archive.read', {
        report_date: period.date,
        collection_id: text(record.collection_id),
      }),
    ));
    if (state.selected !== period.date) return;
    settled.forEach((result, index) => {
      const record = period.records[index];
      if (result.status === 'fulfilled') {
        state.periodDetails.push(Object.assign({}, result.value, {
          record: record,
          group_key: groupKey(record),
          group_label: groupLabel(record),
        }));
      } else {
        state.periodErrors.push({
          record: record,
          group_key: groupKey(record),
          group_label: groupLabel(record),
          message: String((result.reason && result.reason.message) || result.reason),
        });
      }
    });
    if (!state.periodDetails.length) {
      state.error = state.periodErrors.map((failure) => failure.group_label + '：' + failure.message).join('；');
      el('detail-title').textContent = period.date + ' · 读取失败';
      el('detail-subtitle').textContent = state.error;
      renderGroupSwitcher();
      renderPanel();
      return;
    }
    applyPeriod();
  }

  async function refresh(preferredDate) {
    try {
      notify('');
      await loadArchive(preferredDate);
    } catch (error) {
      if (error && error.code === 'bridge_unavailable') {
        applyEmpty('请从 HiMind Agent 打开本视图', '页面需要宿主桥接才能读取归档数据。', [
          '从 Agent 的扩展入口或快速入口打开“开源热榜 · 历史归档”。',
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
    const categoryIds = Array.from(state.selectedThemes);
    const extraTopics = getCustomTopics();
    const pattern = new RegExp(state.customTopicPattern);
    const invalid = extraTopics.find((topic) => !pattern.test(topic));
    if (invalid) {
      notify('额外 topic 不合法：' + invalid, 'warn');
      return;
    }
    if (!categoryIds.length && !extraTopics.length) {
      notify('请选择至少一个分类，或填写额外 topic。', 'warn');
      return;
    }
    const windowDays = Number(el('window').value) || 14;
    const topN = Number(el('topn').value) || 12;
    const labels = categoryIds.map((id) => (themeById(id) || {}).label || id);
    setBusy(true, '采集中…');
    try {
      notify('正在按关注分类采集公开开源仓库…');
      const snapshot = await bridgeInvoke('tech-radar.collect', {
        category_ids: categoryIds,
        extra_topics: extraTopics,
        window_days: windowDays,
        top_n: topN,
        max_queries: state.maxQueries,
      });
      const result = await bridgeInvoke('tech-radar.report', {
        snapshot: snapshot,
        summary: '手动采集 · ' + labels.concat(extraTopics).join('/'),
      });
      const date = (result && result.report && result.report.report_date) || '';
      notify('已生成 ' + date + ' 的规则档，等待工作流补充智能解读。');
      await refresh(date);
    } catch (error) {
      notify('采集失败：' + String((error && error.message) || error), 'error');
    } finally {
      setBusy(false);
    }
  }

  function bind() {
    const refreshButton = el('refresh');
    if (refreshButton) refreshButton.addEventListener('click', () => refresh());
    const archiveList = el('archive-list');
    if (archiveList) archiveList.addEventListener('click', (event) => {
      const button = event.target.closest('.archive-item');
      if (button) selectDate(button.getAttribute('data-date'));
    });
    const groupSwitcher = el('group-switcher');
    if (groupSwitcher) groupSwitcher.addEventListener('click', (event) => {
      const button = event.target.closest('.group-tab');
      if (button) selectGroup(button.getAttribute('data-group-key') || 'all');
    });
    const ranking = el('panel-ranking');
    if (ranking) ranking.addEventListener('click', (event) => {
      const repo = event.target.closest('.repo');
      if (!repo) return;
      const url = repo.getAttribute('data-url');
      openLink(url).then((opened) => {
        if (!opened) copyText(url, '宿主未能打开链接，已复制仓库地址');
      });
    });
    for (const panelId of ['panel-summary', 'panel-source']) {
      const panel = el(panelId);
      if (panel) panel.addEventListener('click', (event) => {
        const button = event.target.closest('[data-open-group]');
        if (button) selectGroup(button.getAttribute('data-open-group'));
      });
    }
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