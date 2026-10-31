(function () {
  'use strict';

  const STORAGE_KEY = 'himind.media-library.ui.v2';
  const ALL_KINDS = ['image', 'video', 'audio'];
  const state = {
    providers: [], libraries: [], sources: [], results: [], localResults: [], onlineResults: [], nextCursor: '', loadingMore: false, query: '',
    scope: 'all', kinds: new Set(ALL_KINDS), providerFilters: new Set(),
    filters: { orientation: '', minWidth: '', minDuration: '' },
    defaultLibraryId: '', previewLibraryId: '', cardAudio: null, cardAudioLoadingIndex: -1, previewItem: null, previewIndex: -1, previewRequestId: 0,
    settingsOpen: false, previewOpen: false, filterOpen: false, busy: false,
    scanned: false, scanningSourceId: '', scanProgress: '', libraryDraftDirty: false,
    lastFocus: null, settingsLastFocus: null, editingLibraryId: '', editingSourceId: '', confirmOpen: false, confirmResolver: null, confirmLastFocus: null,
  };
  const el = (id) => document.getElementById(id);

  function readPrefs() {
    try {
      const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}');
      if (['all', 'online', 'library', 'sources'].includes(stored.scope)) state.scope = stored.scope;
      if (Array.isArray(stored.kinds) && stored.kinds.length) state.kinds = new Set(stored.kinds.filter((kind) => ALL_KINDS.includes(kind)));
      if (typeof stored.defaultLibraryId === 'string') state.defaultLibraryId = stored.defaultLibraryId;
    } catch (_) {}
  }

  function savePrefs() {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({ scope: state.scope, kinds: Array.from(state.kinds), defaultLibraryId: state.defaultLibraryId }));
    } catch (_) {}
  }

  function bridgeInvoke(capabilityId, input) {
    const internals = window.__TAURI_INTERNALS__ || {};
    if (typeof internals.invoke !== 'function') {
      const error = new Error('当前不在 HiMind Agent 的扩展视图窗口中');
      error.code = 'bridge_unavailable';
      return Promise.reject(error);
    }
    return internals.invoke('invoke_plugin_view_capability', { capabilityId, input: input || {} });
  }

  function describe(error) {
    if (error && error.code === 'bridge_unavailable') return error.message;
    if (error && typeof error === 'string') return error;
    if (error && error.message) return error.message;
    return '未知错误';
  }

  function escapeHtml(value) {
    return String(value == null ? '' : value).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }

  function formatBytes(value) {
    if (!value) return '';
    if (value >= 1024 * 1024 * 1024) return (value / (1024 * 1024 * 1024)).toFixed(1) + ' GB';
    if (value >= 1024 * 1024) return (value / (1024 * 1024)).toFixed(1) + ' MB';
    if (value >= 1024) return Math.round(value / 1024) + ' KB';
    return value + ' B';
  }

  function formatTime(value) {
    if (!value) return '';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
  }

  function setFeedback(message, tone, actionTab) {
    const node = el('search-feedback');
    if (!message) { node.hidden = true; node.textContent = ''; return; }
    node.hidden = false;
    node.className = 'inline-feedback' + (tone ? ' ' + tone : '');
    node.textContent = message;
    if (actionTab) {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'inline-link';
      button.textContent = '打开设置';
      button.addEventListener('click', () => openSettings(actionTab));
      node.appendChild(button);
    }
  }

  function showConfirm(options) {
    if (state.confirmResolver) {
      state.confirmResolver(false);
      state.confirmResolver = null;
    }
    state.confirmOpen = true;
    state.confirmLastFocus = document.activeElement;
    el('confirm-title').textContent = options.title || '确认操作';
    el('confirm-message').textContent = options.message || '';
    el('cancel-confirm').textContent = options.cancelText || '取消';
    el('accept-confirm').textContent = options.confirmText || '确认';
    el('accept-confirm').classList.toggle('danger', Boolean(options.danger));
    el('confirm-modal').hidden = false;
    el('confirm-modal').setAttribute('aria-hidden', 'false');
    document.body.classList.add('has-overlay');
    el('cancel-confirm').focus();
    return new Promise((resolve) => { state.confirmResolver = resolve; });
  }

  function resolveConfirm(value) {
    if (!state.confirmOpen) return;
    const resolver = state.confirmResolver;
    const previousFocus = state.confirmLastFocus;
    state.confirmOpen = false;
    state.confirmResolver = null;
    state.confirmLastFocus = null;
    el('confirm-modal').hidden = true;
    el('confirm-modal').setAttribute('aria-hidden', 'true');
    document.body.classList.toggle('has-overlay', state.settingsOpen || state.previewOpen);
    if (previousFocus && typeof previousFocus.focus === 'function') previousFocus.focus();
    if (resolver) resolver(Boolean(value));
  }

  function setSettingsFeedback(message, tone) {
    const node = el('settings-feedback');
    if (!message) { node.hidden = true; node.textContent = ''; node.className = 'inline-feedback settings-feedback'; return; }
    node.hidden = false;
    node.className = 'inline-feedback settings-feedback' + (tone ? ' ' + tone : '');
    node.textContent = message;
  }

  function setPreviewFeedback(message, tone) {
    const node = el('preview-feedback');
    if (!message) { node.hidden = true; node.textContent = ''; node.className = 'inline-feedback'; return; }
    node.hidden = false;
    node.className = 'inline-feedback' + (tone ? ' ' + tone : '');
    node.textContent = message;
  }

  function setBusy(busy, label) {
    state.busy = busy;
    el('busy-state').hidden = !busy;
    el('busy-state').textContent = label || '正在处理…';
    el('search-button').disabled = busy;
    el('refresh').disabled = busy;
    el('results').setAttribute('aria-busy', String(busy));
    if (state.previewItem) updateAdoptionControls(state.previewItem);
    updateLoadMore();
    renderSources();
  }

  function selectedKinds() { return ALL_KINDS.filter((kind) => state.kinds.has(kind)); }
  function providerName(providerId) { const p = state.providers.find((item) => item.id === providerId); return p ? p.name : providerId || '在线'; }
  function selectedAdoptionLibrary() { const id = el('adopt-library') && el('adopt-library').value ? el('adopt-library').value : state.previewLibraryId || state.defaultLibraryId; return state.libraries.find((item) => item.id === id) || null; }

  function updateScopeUI() {
    document.querySelectorAll('[data-scope]').forEach((button) => {
      const active = button.getAttribute('data-scope') === state.scope;
      button.classList.toggle('active', active);
      button.setAttribute('aria-pressed', String(active));
    });
    document.querySelectorAll('[data-kind]').forEach((button) => {
      const active = state.kinds.has(button.getAttribute('data-kind'));
      button.classList.toggle('active', active);
      button.setAttribute('aria-pressed', String(active));
    });
    const onlineOnly = state.scope === 'online' || state.scope === 'all';
    el('filter-toggle').disabled = !onlineOnly;
    el('filter-toggle').title = onlineOnly ? '筛选在线素材' : '高级筛选仅作用于在线来源';
    if (!onlineOnly) closeFilters();
  }

  function renderTargetLibrary() {
    const select = el('target-library');
    if (!state.libraries.length) {
      select.innerHTML = '<option value="">未配置资源库</option>';
      select.disabled = true;
      state.defaultLibraryId = '';
      return;
    }
    select.disabled = false;
    select.innerHTML = state.libraries.map((library) => '<option value="' + escapeHtml(library.id) + '">' + escapeHtml(library.name) + '</option>').join('');
    if (state.libraries.some((library) => library.id === state.defaultLibraryId)) select.value = state.defaultLibraryId;
    else { state.defaultLibraryId = state.libraries[0].id; select.value = state.defaultLibraryId; }
    savePrefs();
  }

  async function loadProviders() {
    const payload = await bridgeInvoke('media-library.provider.list', {});
    state.providers = payload.providers || [];
    renderProviders();
    renderProviderFilters();
  }

  function renderProviders() {
    const host = el('providers');
    if (!state.providers.length) { host.innerHTML = '<div class="empty-state"><strong>没有在线来源</strong><span class="meta">请检查插件配置。</span></div>'; return; }
    host.innerHTML = state.providers.map((provider) => {
      const statusClass = provider.configured ? 'ok' : 'warn';
      const statusText = provider.configured ? '已配置' : '未配置';
      const requirement = provider.configured ? '可用于在线检索' : (provider.error || provider.requirement || '请在账号与凭据中配置');
      return '<div class="config-row"><div class="config-row-main"><strong>' + escapeHtml(provider.name) + '</strong><small>' + escapeHtml((provider.kinds || []).join(' / ')) + ' · ' + escapeHtml(requirement) + '</small></div><span class="status ' + statusClass + '">' + statusText + '</span></div>';
    }).join('');
  }

  function renderProviderFilters() {
    const host = el('provider-filters');
    if (!state.providers.length) { host.innerHTML = '<span class="meta">暂无在线来源</span>'; return; }
    const allSelected = state.providerFilters.size === 0;
    host.innerHTML = state.providers.map((provider) => {
      const checked = allSelected || state.providerFilters.has(provider.id);
      return '<label><input type="checkbox" class="provider-filter" value="' + escapeHtml(provider.id) + '"' + (checked ? ' checked' : '') + ' />' + escapeHtml(provider.name) + '</label>';
    }).join('');
    host.querySelectorAll('.provider-filter').forEach((input) => input.addEventListener('change', handleProviderFilterChange));
    updateFilterCount();
  }

  async function loadLibraries() {
    const payload = await bridgeInvoke('media-library.repository.list', {});
    state.libraries = payload.libraries || [];
    if (state.defaultLibraryId && !state.libraries.some((library) => library.id === state.defaultLibraryId)) state.defaultLibraryId = '';
    renderTargetLibrary();
    renderLibraries();
    renderAdoptionLibraryOptions();
    renderIndexOverview();
  }

  function renderAdoptionLibraryOptions() {
    const select = el('adopt-library');
    if (!select) return;
    if (!state.libraries.length) {
      select.innerHTML = '<option value="">未配置资源库</option>';
      select.disabled = true;
      state.previewLibraryId = '';
      return;
    }
    select.disabled = false;
    select.innerHTML = state.libraries.map((library) => '<option value="' + escapeHtml(library.id) + '">' + escapeHtml(library.name) + (library.access_mode === 'read_only' ? ' · 只读' : '') + '</option>').join('');
    const preferred = state.libraries.some((library) => library.id === state.previewLibraryId) ? state.previewLibraryId : state.defaultLibraryId;
    select.value = state.libraries.some((library) => library.id === preferred) ? preferred : state.libraries[0].id;
    state.previewLibraryId = select.value;
  }

  function renderLibraries() {
    const host = el('libraries');
    if (!state.libraries.length) {
      host.innerHTML = '<div class="empty-state"><strong>还没有资源库</strong><span class="meta">创建本地库或关联 SMB 目录后即可采纳素材。</span><button type="button" class="primary" data-open-library-form>新建或关联</button></div>';
      return;
    }
    host.innerHTML = state.libraries.map((library) => {
      const active = library.id === state.defaultLibraryId;
      return '<div class="config-row"><div class="config-row-main"><strong>' + escapeHtml(library.name) + (active ? ' · 默认' : '') + '</strong><small>' + escapeHtml(library.backend_type === 'smb' ? 'SMB' : '本地') + ' · ' + escapeHtml(library.state === 'online' ? '在线' : '离线') + ' · ' + library.asset_count + ' 项</small></div><div class="config-actions">' + (active ? '<span class="status ok">默认入库</span>' : '') + '<button type="button" class="ghost" data-edit-library="' + escapeHtml(library.id) + '" aria-label="编辑资源库 ' + escapeHtml(library.name) + '" title="编辑资源库">编辑</button><button type="button" class="ghost danger-text" data-remove-library="' + escapeHtml(library.id) + '" aria-label="移除资源库 ' + escapeHtml(library.name) + '" title="移除资源库">移除</button></div></div>';
    }).join('');
  }

  async function loadSources() {
    const payload = await bridgeInvoke('media-library.source.list', {});
    state.sources = payload.sources || [];
    renderSources();
    renderIndexOverview();
  }

  function sourceStatusText(source) {
    if (state.scanningSourceId === source.id) return state.scanProgress || '正在扫描…';
    if (source.scan_status === 'complete') return '索引完成' + (source.last_scan_at ? ' · ' + formatTime(source.last_scan_at) : '');
    return '待扫描';
  }

  function renderSources() {
    const host = el('sources');
    if (!host) return;
    if (!state.sources.length) { host.innerHTML = '<div class="empty-state"><strong>还没有本机来源</strong><span class="meta">添加文件夹后会建立候选索引，不会自动进入资源库。</span></div>'; return; }
    host.innerHTML = state.sources.map((source) => '<div class="config-row"><div class="config-row-main"><strong>' + escapeHtml(source.display_name) + '</strong><small title="' + escapeHtml(source.path) + '">' + escapeHtml(source.path) + '</small><small>' + escapeHtml(sourceStatusText(source)) + '</small></div><div class="config-actions"><button type="button" class="ghost" data-scan-source="' + escapeHtml(source.id) + '"' + (state.busy ? ' disabled' : '') + '>扫描</button><button type="button" class="ghost" data-edit-source="' + escapeHtml(source.id) + '" aria-label="编辑本机来源 ' + escapeHtml(source.display_name) + '" title="编辑本机来源">编辑</button><button type="button" class="ghost danger-text" data-remove-source="' + escapeHtml(source.id) + '" aria-label="移除本机来源 ' + escapeHtml(source.display_name) + '" title="移除本机来源">移除</button></div></div>').join('');
  }

  function renderIndexOverview() {
    const overview = el('index-overview');
    if (overview) {
      const assets = state.libraries.reduce((sum, library) => sum + Number(library.asset_count || 0), 0);
      const online = state.libraries.filter((library) => library.state === 'online').length;
      overview.innerHTML = [['资源库', state.libraries.length], ['资源条目', assets], ['本机来源', state.sources.length], ['在线来源', state.providers.filter((provider) => provider.configured).length]].map(([label, value]) => '<div class="metric"><span>' + escapeHtml(label) + '</span><strong>' + value + '</strong></div>').join('') + (online !== state.libraries.length ? '<div class="inline-feedback warn">有 ' + (state.libraries.length - online) + ' 个资源库当前离线</div>' : '');
    }
    const host = el('index-sources');
    if (host) host.innerHTML = state.sources.length ? state.sources.map((source) => '<div class="config-row"><div class="config-row-main"><strong>' + escapeHtml(source.display_name) + '</strong><small>' + escapeHtml(sourceStatusText(source)) + '</small></div><span class="status ' + (source.scan_status === 'complete' ? 'ok' : 'warn') + '">' + (source.scan_status === 'complete' ? '已索引' : '待处理') + '</span></div>').join('') : '<span class="meta">暂无本机候选来源。</span>';
  }

  function updateFilterCount() {
    let count = 0;
    if (state.providerFilters.size && state.providerFilters.size < state.providers.length) count += 1;
    if (state.filters.orientation) count += 1;
    if (state.filters.minWidth) count += 1;
    if (state.filters.minDuration) count += 1;
    const badge = el('filter-count');
    badge.hidden = count === 0;
    badge.textContent = String(count);
    renderActiveFilters();
  }

  function filterLabel(key, value) { return key === 'orientation' ? ({ landscape: '横屏', portrait: '竖屏', square: '方形' }[value] || value) : key === 'minWidth' ? '≥ ' + value + 'px' : key === 'minDuration' ? '≥ ' + value + 's' : value; }

  function renderActiveFilters() {
    const host = el('active-filters');
    const chips = [];
    if (state.providerFilters.size && state.providerFilters.size < state.providers.length) chips.push({ key: 'providers', text: state.providerFilters.size + ' 个来源' });
    ['orientation', 'minWidth', 'minDuration'].forEach((key) => { if (state.filters[key]) chips.push({ key, text: filterLabel(key, state.filters[key]) }); });
    host.hidden = chips.length === 0;
    host.innerHTML = chips.map((chip) => '<span class="filter-chip">' + escapeHtml(chip.text) + '<button type="button" data-clear-filter="' + chip.key + '" aria-label="移除筛选">×</button></span>').join('');
  }

  function handleProviderFilterChange() {
    const selected = Array.from(document.querySelectorAll('.provider-filter:checked')).map((input) => input.value);
    state.providerFilters = selected.length === state.providers.length ? new Set() : new Set(selected);
    updateFilterCount();
  }

  function openFilters() { if (el('filter-toggle').disabled) return; state.filterOpen = true; el('filter-popover').hidden = false; el('filter-toggle').setAttribute('aria-expanded', 'true'); }
  function closeFilters() { state.filterOpen = false; if (el('filter-popover')) el('filter-popover').hidden = true; el('filter-toggle').setAttribute('aria-expanded', 'false'); }
  function clearFilters() { state.providerFilters = new Set(); state.filters = { orientation: '', minWidth: '', minDuration: '' }; el('filter-orientation').value = ''; el('filter-min-width').value = ''; el('filter-min-duration').value = ''; renderProviderFilters(); updateFilterCount(); }
  function applyFilters() { state.filters.orientation = el('filter-orientation').value; state.filters.minWidth = el('filter-min-width').value.trim(); state.filters.minDuration = el('filter-min-duration').value.trim(); handleProviderFilterChange(); closeFilters(); }
  function skeletonResults() { el('results').innerHTML = Array.from({ length: 6 }, () => '<div class="skeleton" aria-hidden="true"></div>').join(''); }

  function combineSearchResults() { return [...state.localResults, ...state.onlineResults]; }
  function updateLoadMore() {
    const button = el('load-more');
    if (!button) return;
    const available = Boolean(state.nextCursor) && state.scope !== 'online';
    button.hidden = !available;
    button.disabled = state.busy;
    button.textContent = state.loadingMore ? '正在加载…' : '加载更多';
  }
  function updateResultSummary() {
    const results = state.results;
    const onlineCount = results.filter((item) => item.origin === 'online').length; const localCount = results.length - onlineCount;
    el('results-title').textContent = results.length ? '找到 ' + results.length + ' 项' : '没有匹配结果';
    el('result-detail').textContent = (onlineCount ? '在线 ' + onlineCount : '') + (onlineCount && localCount ? ' · ' : '') + (localCount ? '资源库/本机 ' + localCount : '');
  }

  async function loadMoreResults() {
    if (!state.nextCursor || state.loadingMore || state.busy) return;
    state.loadingMore = true; setBusy(true, '正在加载更多…');
    try {
      const localScope = state.scope === 'online' ? 'all' : state.scope;
      const payload = await bridgeInvoke('media-library.asset.search', { query: state.query, kinds: selectedKinds(), scope: localScope, limit: 60, cursor: state.nextCursor });
      const existing = new Set(state.localResults.map((item) => item.id));
      for (const item of (payload.items || [])) {
        if (!existing.has(item.id)) state.localResults.push(Object.assign({}, item, { origin: item.entry_type || 'candidate' }));
      }
      state.nextCursor = payload.next_cursor || '';
      state.results = combineSearchResults();
      renderResults(); updateResultSummary(); setFeedback('');
    } catch (error) { setFeedback('加载更多失败：' + describe(error), 'error'); }
    finally { state.loadingMore = false; setBusy(false); }
  }

  async function runSearch() {
    const query = el('query').value.trim();
    if (!query) { setFeedback('请输入搜索关键词。', 'warn'); el('query').focus(); return; }
    const kinds = selectedKinds();
    if (!kinds.length) { setFeedback('至少选择一种媒体类型。', 'warn'); return; }
    state.query = query; state.scanned = true; state.nextCursor = ''; state.localResults = []; state.onlineResults = []; state.results = [];
    setFeedback(''); setBusy(true, '正在搜索…'); skeletonResults();
    el('results-title').textContent = '正在搜索…'; el('result-detail').textContent = query;
    try {
      const localResults = []; const onlineResults = []; const warnings = [];
      if (state.scope === 'all' || state.scope === 'library' || state.scope === 'sources') {
        const localScope = state.scope === 'online' ? 'all' : state.scope;
        const local = await bridgeInvoke('media-library.asset.search', { query, kinds, scope: localScope, limit: 60 });
        localResults.push(...(local.items || []).map((item) => Object.assign({}, item, { origin: item.entry_type || 'candidate' })));
        state.nextCursor = local.next_cursor || '';
      }
      if (state.scope === 'all' || state.scope === 'online') {
        try {
          const inputs = { query, kinds, per_source: 12, page: 1, providers: state.providerFilters.size ? Array.from(state.providerFilters) : undefined, orientation: state.filters.orientation || undefined, min_width: state.filters.minWidth ? Number(state.filters.minWidth) : undefined, min_duration_seconds: state.filters.minDuration ? Number(state.filters.minDuration) : undefined };
          Object.keys(inputs).forEach((key) => { if (inputs[key] === undefined) delete inputs[key]; });
          const online = await bridgeInvoke('media-library.search', inputs);
          onlineResults.push(...(online.items || []).map((item) => Object.assign({}, item, { origin: 'online' })));
          warnings.push(...(online.warnings || []));
        } catch (error) { warnings.push('在线来源：' + describe(error)); }
      }
      state.localResults = localResults; state.onlineResults = onlineResults; state.results = combineSearchResults(); renderResults(); updateResultSummary();
      if (warnings.length) { const configured = state.providers.some((provider) => provider.configured); setFeedback(configured ? '部分来源不可用：' + warnings.join('；') : '在线来源尚未配置。打开设置查看来源状态。', 'warn', 'online'); }
      else if (!state.results.length) setFeedback('没有匹配结果，尝试更换关键词或减少筛选条件。', 'warn');
    } catch (error) { state.results = []; state.localResults = []; state.onlineResults = []; state.nextCursor = ''; renderResults(); el('results-title').textContent = '搜索失败'; el('result-detail').textContent = ''; setFeedback(describe(error), 'error'); }
    finally { setBusy(false); }
  }

  function resultMeta(item) { return [item.author, item.license || item.license_name, item.bytes ? formatBytes(item.bytes) : '', item.kind].filter(Boolean); }
  function sourceLabel(item) { if (item.origin === 'online') return providerName(item.provider); if (item.entry_type === 'library') return '资源库'; return '本机'; }
  function kindLabel(kind) { return ({ image: '图片', video: '视频', audio: '音频' })[kind] || kind || '素材'; }
  function stripMediaExtension(value) { return String(value || '').replace(/\.(png|jpe?g|gif|webp|svg|mp4|mov|webm|mkv|avi|mp3|wav|flac|ogg|m4a|aac)$/i, ''); }
  function parseTagList(value) { return Array.from(new Set(String(value || '').split(/[,，、;；]/).map((tag) => tag.trim()).filter(Boolean))).slice(0, 20); }
  function normalizeCollection(value) { return String(value || '').trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, ''); }
  function populateAdoptionForm(item) {
    const adoptable = item.entry_type !== 'library';
    renderAdoptionLibraryOptions();
    el('adopt-form').hidden = !adoptable;
    el('adopt-name').value = stripMediaExtension(item.title || item.name || item.id || '');
    el('adopt-collection').value = item.collection || '';
    el('adopt-tags').value = Array.isArray(item.tags) ? item.tags.join(', ') : '';
    const original = item.path ? String(item.path).split(/[\\/]/).pop() : (item.title || item.name || '');
    el('adopt-source-name').textContent = original ? '原文件：' + original : '';
  }
  function formatDuration(value) { const seconds = Number(value); if (!Number.isFinite(seconds) || seconds < 0) return '0:00'; const minutes = Math.floor(seconds / 60); return minutes + ':' + String(Math.floor(seconds % 60)).padStart(2, '0'); }

  function resetCardAudioButton(button) {
    if (!button) return;
    button.disabled = false;
    button.classList.remove('playing', 'loading');
    button.setAttribute('aria-pressed', 'false');
    button.setAttribute('aria-busy', 'false');
    button.setAttribute('aria-label', '播放 ' + (button.getAttribute('data-audio-title') || '音频'));
    button.title = '播放音频';
    const card = button.closest('.result-card'); if (card) card.classList.remove('audio-playing');
    const progress = card && card.querySelector('.card-audio-progress span'); if (progress) progress.style.width = '0%';
    const time = card && card.querySelector('.card-audio-time'); if (time) time.textContent = '试听';
  }

  function stopCardAudio() {
    if (state.cardAudio) {
      const { audio } = state.cardAudio;
      audio.pause();
      audio.removeAttribute('src');
      audio.load();
    }
    state.cardAudio = null;
    state.cardAudioLoadingIndex = -1;
    document.querySelectorAll('[data-play-index]').forEach(resetCardAudioButton);
  }

  function pauseCardAudio() {
    if (state.cardAudio) state.cardAudio.audio.pause();
  }

  function syncCardAudio(current) {
    if (state.cardAudio !== current) return;
    const { audio, button, card, progress, time } = current;
    const playing = !audio.paused && !audio.ended;
    button.disabled = false;
    button.classList.remove('loading');
    button.classList.toggle('playing', playing);
    button.setAttribute('aria-pressed', String(playing));
    button.setAttribute('aria-busy', 'false');
    button.setAttribute('aria-label', (playing ? '暂停 ' : '播放 ') + (button.getAttribute('data-audio-title') || '音频'));
    button.title = playing ? '暂停音频' : '播放音频';
    card.classList.toggle('audio-playing', playing);
    const duration = Number.isFinite(audio.duration) && audio.duration > 0 ? audio.duration : 0;
    if (duration) progress.style.width = Math.min(100, audio.currentTime / duration * 100) + '%';
    time.textContent = audio.ended ? '重播' : formatDuration(audio.currentTime);
  }

  async function toggleCardAudio(index) {
    const item = state.results[index];
    if (!item || item.kind !== 'audio') return;
    const existing = state.cardAudio;
    if (existing && existing.index === index) {
      if (existing.audio.paused) {
        try { await existing.audio.play(); } catch (error) { setFeedback('音频播放失败：' + describe(error), 'error'); }
      } else {
        existing.audio.pause();
      }
      return;
    }
    stopCardAudio();
    const button = document.querySelector('[data-play-index="' + index + '"]');
    const card = button && button.closest('.result-card');
    if (!button || !card) return;
    state.cardAudioLoadingIndex = index;
    button.disabled = true;
    button.classList.add('loading');
    button.setAttribute('aria-busy', 'true');
    try {
      const src = item.origin === 'online' ? (item.kind === 'audio' ? (item.download_url || item.preview_url) : (item.preview_url || item.download_url)) : await (async () => { const local = await previewLocal(item); return local.data_url || local.file_url || toFileUrl(item.path); })();
      if (!src) throw new Error('该音频没有可播放地址');
      const audio = new Audio();
      audio.preload = 'metadata';
      audio.src = src;
      const current = { index, audio, button, card, progress: card.querySelector('.card-audio-progress span'), time: card.querySelector('.card-audio-time') };
      state.cardAudio = current;
      audio.addEventListener('play', () => syncCardAudio(current));
      audio.addEventListener('pause', () => syncCardAudio(current));
      audio.addEventListener('ended', () => syncCardAudio(current));
      audio.addEventListener('timeupdate', () => syncCardAudio(current));
      audio.addEventListener('loadedmetadata', () => syncCardAudio(current));
      audio.addEventListener('error', () => {
        if (state.cardAudio !== current) return;
        stopCardAudio();
        setFeedback('音频播放失败，请打开详情检查来源。', 'error');
      });
      await audio.play();
      syncCardAudio(current);
    } catch (error) {
      resetCardAudioButton(button);
      setFeedback('音频播放失败：' + describe(error), 'error');
    } finally {
      state.cardAudioLoadingIndex = -1;
    }
  }

  function renderResults() {
    stopCardAudio();
    updateLoadMore();
    const host = el('results');
    if (!state.scanned) { host.innerHTML = '<div class="empty-state"><strong>搜索图片、视频和音频</strong><span class="meta">按名称、标签、分类路径和来源统一检索。</span><div class="empty-actions"><button type="button" class="ghost" data-open-settings="local">添加本机文件夹</button><button type="button" class="ghost" data-open-settings="online">配置在线来源</button></div></div>'; return; }
    if (!state.results.length) { host.innerHTML = '<div class="empty-state"><strong>没有匹配结果</strong><span class="meta">更换关键词，或清除来源和类型筛选后再试。</span><div class="empty-actions"><button type="button" class="ghost" data-clear-all-filters>清空筛选</button><button type="button" class="ghost" data-open-settings="libraries">检查资源库</button></div></div>'; return; }
    host.innerHTML = state.results.map((item, index) => {
      const title = item.title || item.name || item.id || '素材';
      const thumb = item.origin === 'online' && item.preview_url && item.kind === 'audio' ? '<img class="result-waveform" src="' + escapeHtml(item.preview_url) + '" alt="" loading="lazy" />' : item.origin === 'online' && item.preview_url && item.kind !== 'audio' ? '<img src="' + escapeHtml(item.preview_url) + '" alt="" loading="lazy" />' : '<span class="result-icon" aria-hidden="true">' + (item.kind === 'audio' ? '♪' : item.kind === 'video' ? '▶' : '▧') + '</span>';
      const sourceClass = item.origin === 'online' ? 'online' : item.entry_type === 'library' ? 'library' : 'candidate';
      const playback = item.kind === 'audio' ? '<div class="card-audio-playback"><button type="button" class="result-play" data-play-index="' + index + '" data-audio-title="' + escapeHtml(title) + '" aria-label="播放 ' + escapeHtml(title) + '" aria-pressed="false"><svg class="play-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="m9 6 9 6-9 6Z"/></svg><svg class="pause-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M8 6v12M16 6v12"/></svg></button><span class="card-audio-progress" aria-hidden="true"><span></span></span><span class="card-audio-time">试听</span></div>' : '';
      const tags = Array.isArray(item.tags) ? item.tags.slice(0, 2) : [];
      const tagMarkup = tags.length ? '<span class="result-tags">' + tags.map((tag) => '<span class="result-tag">' + escapeHtml(tag) + '</span>').join('') + '</span>' : '';
      return '<article class="result-card" data-result-index="' + index + '" data-kind="' + escapeHtml(item.kind || '') + '"><span class="result-thumb">' + thumb + '<span class="source-tag ' + sourceClass + '">' + escapeHtml(sourceLabel(item)) + '</span>' + playback + '</span><span class="result-body"><span class="result-title">' + escapeHtml(title) + '</span><span class="result-meta">' + resultMeta(item).map((value) => '<span>' + escapeHtml(value) + '</span>').join('') + '</span>' + tagMarkup + '</span><button type="button" class="result-open" data-open-result="' + index + '" aria-label="预览 ' + escapeHtml(title) + '"></button></article>';
    }).join('');
  }

  function toFileUrl(path) { const internals = window.__TAURI_INTERNALS__ || {}; if (typeof internals.convertFileSrc === 'function') { try { return internals.convertFileSrc(path); } catch (_) {} } const normalized = String(path || '').replace(/\\/g, '/'); return /^[a-zA-Z]:/.test(normalized) ? 'file:///' + normalized : 'file://' + normalized; }
  async function previewLocal(item) { return bridgeInvoke('media-library.asset.preview', { path: item.path, kind: item.kind, max_bytes: 32 * 1024 * 1024 }); }

  function previewMediaMarkup(item, src) {
    const title = escapeHtml(item.title || item.name || '素材');
    if (item.kind === 'image') return '<img src="' + escapeHtml(src) + '" alt="' + title + '" />';
    if (item.kind === 'video') return '<video controls preload="metadata" poster="' + escapeHtml(item.preview_url || '') + '" src="' + escapeHtml(src) + '" aria-label="播放 ' + title + '"></video>';
    return '<div class="audio-preview"><span class="audio-art" aria-hidden="true">♪</span><audio controls preload="metadata" src="' + escapeHtml(src) + '" aria-label="试听 ' + title + '"></audio></div>';
  }

  function previewLinkMarkup(value, href) {
    const text = escapeHtml(value);
    if (!href) return text;
    try {
      const target = new URL(String(href));
      if (target.protocol !== 'http:' && target.protocol !== 'https:') return text;
      return '<a href="' + escapeHtml(target.href) + '" target="_blank" rel="noopener noreferrer">' + text + '</a>';
    } catch (_) {
      return text;
    }
  }

  function updatePreviewNavigation() {
    const count = state.results.length;
    el('preview-position').textContent = state.previewIndex >= 0 ? (state.previewIndex + 1) + ' / ' + count : '';
    el('previous-preview').disabled = state.previewIndex <= 0;
    el('next-preview').disabled = state.previewIndex < 0 || state.previewIndex >= count - 1;
  }

  function navigatePreview(delta) {
    const next = state.previewIndex + delta;
    if (next < 0 || next >= state.results.length) return;
    void openPreview(next);
  }

  async function openPreview(index) {
    const item = state.results[index];
    if (!item) return;
    const switching = state.previewOpen;
    pauseCardAudio();
    state.previewItem = item;
    state.previewIndex = index;
    if (!switching) {
      state.previewLibraryId = state.defaultLibraryId;
      state.previewOpen = true;
      state.lastFocus = document.activeElement;
      el('preview-scrim').hidden = false;
      el('preview').hidden = false;
      el('preview').setAttribute('aria-hidden', 'false');
      document.body.classList.add('has-overlay');
    }
    const requestId = ++state.previewRequestId;
    const title = item.title || item.name || item.id || '素材预览';
    const source = sourceLabel(item);
    el('preview-badges').innerHTML = '<span class="detail-badge kind">' + escapeHtml(kindLabel(item.kind)) + '</span><span class="detail-badge">' + escapeHtml(source) + '</span>';
    el('preview-title').textContent = title;
    el('preview-subtitle').textContent = item.author ? source + ' · ' + item.author : source + ' · ' + kindLabel(item.kind);
    el('preview-kind-label').textContent = kindLabel(item.kind) + '素材';
    populateAdoptionForm(item);
    updatePreviewNavigation();
    el('preview-media').innerHTML = '<div class="empty-state"><span class="meta">正在加载预览…</span></div>';
    el('preview-meta').innerHTML = '';
    setPreviewFeedback('');
    el('adopt-button').hidden = item.entry_type === 'library';
    el('redistribution-notice').hidden = true;
    try {
      let src;
      if (item.origin === 'online') {
        src = item.kind === 'audio' ? (item.download_url || item.preview_url) : item.kind === 'video' ? (item.download_url || item.preview_url) : (item.preview_url || item.download_url);
      } else {
        const local = await previewLocal(item);
        if (requestId !== state.previewRequestId) return;
        src = local.data_url || local.file_url || toFileUrl(item.path);
      }
      if (requestId !== state.previewRequestId) return;
      el('preview-media').innerHTML = previewMediaMarkup(item, src);
      const license = item.license || item.license_name;
      const rows = [
        ['来源', source],
        ['作者', item.author || ''],
        ['许可证', previewLinkMarkup(license || '', item.license_url)],
        ['分类路径', item.collection || ''],
        ['标签', Array.isArray(item.tags) && item.tags.length ? item.tags.join(' · ') : ''],
        ['时长', item.duration_seconds || item.duration ? formatDuration(item.duration_seconds || item.duration) : ''],
        ['路径', item.relative_path || item.path || ''],
        ['大小', formatBytes(item.bytes)],
        ['原始页面', previewLinkMarkup(item.source_url ? '打开来源' : '', item.source_url)],
      ].filter((row) => row[1]);
      el('preview-meta').innerHTML = rows.map((row) => '<dt>' + escapeHtml(row[0]) + '</dt><dd>' + row[1] + '</dd>').join('');
      updateAdoptionControls(item);
      if (!switching) el('close-preview').focus();
    } catch (error) {
      if (requestId !== state.previewRequestId) return;
      el('preview-media').innerHTML = '<div class="empty-state"><strong>预览失败</strong><span class="meta">' + escapeHtml(describe(error)) + '</span></div>';
      setPreviewFeedback('预览失败：' + describe(error), 'error');
    }
  }

  function closePreview() {
    if (!state.previewOpen) return;
    state.previewRequestId += 1;
    state.previewOpen = false;
    state.previewItem = null;
    state.previewIndex = -1;
    el('preview').hidden = true;
    el('preview').setAttribute('aria-hidden', 'true');
    el('preview-scrim').hidden = true;
    el('preview-media').querySelectorAll('audio, video').forEach((media) => media.pause());
    el('preview-media').innerHTML = '';
    document.body.classList.toggle('has-overlay', state.settingsOpen);
    if (state.lastFocus && typeof state.lastFocus.focus === 'function') state.lastFocus.focus();
  }

  function updateAdoptionControls(item) {
    const library = selectedAdoptionLibrary();
    const smbOnline = Boolean(library && library.backend_type === 'smb' && item.origin === 'online' && item.entry_type !== 'library');
    el('adopt-form').hidden = item.entry_type === 'library';
    el('redistribution-notice').hidden = !smbOnline;
    el('redistribution-notice').textContent = smbOnline ? '点击下方按钮即确认该素材许可允许在团队 SMB 共享库中再分发；许可证不明确时请勿入库。' : '';
    const button = el('adopt-button'); button.disabled = state.busy || !library || library.access_mode === 'read_only';
    button.textContent = !library ? '请先在设置中创建资源库' : library.access_mode === 'read_only' ? '资源库为只读' : smbOnline ? '确认并添加到 SMB 共享库' : '添加到资源库';
  }

  async function adoptCurrent() {
    const item = state.previewItem; const library = selectedAdoptionLibrary();
    if (!item || !library) { openSettings('libraries'); return; }
    if (library.access_mode === 'read_only') { setFeedback('当前资源库为只读，无法入库。', 'warn', 'libraries'); return; }
    const name = el('adopt-name').value.trim();
    if (!name) { setPreviewFeedback('请填写入库名称。', 'warn'); el('adopt-name').focus(); return; }
    if (/[\\/:*?"<>|]/.test(name)) { setPreviewFeedback('入库名称不能包含 \ / : * ? " < > | 等字符。', 'warn'); el('adopt-name').focus(); return; }
    const tags = parseTagList(el('adopt-tags').value);
    const collection = normalizeCollection(el('adopt-collection').value);
    const smbOnline = library.backend_type === 'smb' && item.origin === 'online' && item.entry_type !== 'library';
    const license = String(item.license || item.license_name || '').trim();
    if (smbOnline && !license) { setPreviewFeedback('该素材缺少许可证信息，不能进入 SMB 共享库。', 'error'); return; }
    const payloadItem = item.origin === 'online'
      ? { provider: item.provider, provider_id: item.id, kind: item.kind, title: name, author: item.author, license_name: license, license_url: item.license_url, source_url: item.source_url, download_url: item.download_url, filename: name, tags, collection, redistribution_allowed: smbOnline }
      : { candidate_id: item.entry_type === 'candidate' ? item.id : '', local_path: item.path, kind: item.kind, title: name, filename: name, tags, collection, redistribution_allowed: false };
    setPreviewFeedback('');
    setBusy(true, '正在添加到资源库…');
    try {
      const result = await bridgeInvoke('media-library.asset.adopt', { library_id: library.id, mode: 'copy', items: [payloadItem] });
      const failed = (result.failed || [])[0]; if (failed) throw new Error(failed.error);
      const saved = (result.saved || [])[0];
      const message = saved && saved.deduplicated ? '该素材已在资源库中，未重复复制或改名。' : '已添加到资源库：' + name + '。';
      closePreview();
      await loadLibraries();
      if (state.query) await runSearch();
      setFeedback(message);
    } catch (error) { setPreviewFeedback('添加到资源库失败：' + describe(error), 'error'); }
    finally { setBusy(false); }
  }

  function switchSettingsTab(tab) {
    document.querySelectorAll('[data-settings-tab]').forEach((button) => { const active = button.getAttribute('data-settings-tab') === tab; button.classList.toggle('active', active); button.setAttribute('aria-selected', String(active)); button.tabIndex = active ? 0 : -1; });
    document.querySelectorAll('[data-settings-panel]').forEach((panel) => { panel.hidden = panel.getAttribute('data-settings-panel') !== tab; });
  }

  function openSettings(tab) {
    pauseCardAudio();
    el('preview-media').querySelectorAll('audio, video').forEach((media) => media.pause());
    state.settingsLastFocus = document.activeElement;
    state.settingsOpen = true;
    closeFilters();
    el('settings-modal').hidden = false;
    el('settings-modal').setAttribute('aria-hidden', 'false');
    setSettingsFeedback('');
    if (state.previewOpen) el('preview').setAttribute('aria-hidden', 'true');
    document.body.classList.add('has-overlay');
    switchSettingsTab(tab || 'libraries');
    el('settings-modal').querySelector('.settings-tab.active').focus();
  }
  async function closeSettings() {
    if (!state.settingsOpen) return;
    if (state.libraryDraftDirty && !(await showConfirm({ title: '放弃未保存的修改？', message: '资源库信息尚未保存，关闭设置后这些修改会丢失。', confirmText: '放弃修改', danger: true }))) return;
    state.settingsOpen = false;
    state.libraryDraftDirty = false;
    el('library-form').hidden = true;
    el('settings-modal').hidden = true;
    el('settings-modal').setAttribute('aria-hidden', 'true');
    setSettingsFeedback('');
    if (state.previewOpen) el('preview').setAttribute('aria-hidden', 'false');
    document.body.classList.toggle('has-overlay', state.previewOpen);
    if (state.settingsLastFocus && typeof state.settingsLastFocus.focus === 'function') state.settingsLastFocus.focus();
  }
  function showLibraryForm(libraryId) {
    const library = libraryId ? state.libraries.find((item) => item.id === libraryId) : null;
    state.editingLibraryId = library ? library.id : '';
    state.libraryDraftDirty = false;
    el('library-form-title').textContent = library ? '编辑资源库' : '新建资源库';
    el('library-form').querySelector('button[type="submit"]').textContent = library ? '保存修改' : '创建或关联';
    el('library-name').value = library ? library.name : '';
    el('library-backend').value = library ? library.backend_type : 'local';
    el('library-root').value = library ? library.root_uri : '';
    el('library-access').value = library ? library.access_mode : 'contribute';
    el('library-backend').disabled = Boolean(library);
    el('library-root').readOnly = Boolean(library);
    el('library-form').hidden = false;
    el('library-name').focus();
  }
  async function hideLibraryForm(force) {
    if (!force && state.libraryDraftDirty && !(await showConfirm({ title: '放弃未保存的修改？', message: '资源库信息尚未保存，关闭表单后这些修改会丢失。', confirmText: '放弃修改', danger: true }))) return false;
    el('library-form').hidden = true;
    state.libraryDraftDirty = false;
    state.editingLibraryId = '';
    el('library-name').value = '';
    el('library-root').value = '';
    el('library-backend').disabled = false;
    el('library-root').readOnly = false;
  }

  async function createLibrary(event) {
    event.preventDefault(); const name = el('library-name').value.trim(); const root = el('library-root').value.trim();
    if (!name || (!state.editingLibraryId && !root)) { setSettingsFeedback('请填写资源库名称和根目录。', 'warn'); return; }
    const editing = state.editingLibraryId;
    setBusy(true, editing ? '正在保存资源库…' : '正在创建资源库…');
    try {
      if (editing) {
        await bridgeInvoke('media-library.repository.update', { id: editing, name, access_mode: el('library-access').value });
        await hideLibraryForm(true); await loadLibraries(); setSettingsFeedback('资源库已更新。');
      } else {
        const created = await bridgeInvoke('media-library.repository.create', { name, backend_type: el('library-backend').value, root_path: root, access_mode: el('library-access').value });
        if (created && created.id) state.defaultLibraryId = created.id;
        await hideLibraryForm(true); await loadLibraries(); setSettingsFeedback('资源库已创建或关联。');
      }
      switchSettingsTab('libraries');
    } catch (error) { setSettingsFeedback((editing ? '保存资源库失败：' : '创建资源库失败：') + describe(error), 'error'); }
    finally { setBusy(false); }
  }

  async function removeLibrary(libraryId) {
    const library = state.libraries.find((item) => item.id === libraryId);
    if (!library) return;
    if (!(await showConfirm({ title: '移除资源库？', message: '将从 HiMind 移除资源库“' + library.name + '”及其检索索引。磁盘中的文件不会被删除。', confirmText: '移除资源库', danger: true }))) return;
    setBusy(true, '正在移除资源库…');
    try {
      await bridgeInvoke('media-library.repository.remove', { id: libraryId });
      if (state.defaultLibraryId === libraryId) { state.defaultLibraryId = ''; savePrefs(); }
      if (state.editingLibraryId === libraryId) await hideLibraryForm(true);
      await loadLibraries(); if (state.query) await runSearch();
      setSettingsFeedback('资源库已从 HiMind 移除，磁盘文件未删除。');
    } catch (error) { setSettingsFeedback('移除资源库失败：' + describe(error), 'error'); }
    finally { setBusy(false); }
  }

  function showSourceEditForm(sourceId) {
    const source = state.sources.find((item) => item.id === sourceId);
    if (!source) return;
    state.editingSourceId = source.id;
    el('source-display-name').value = source.display_name;
    el('source-edit-path').value = source.path;
    el('source-edit-form').hidden = false;
    el('source-display-name').focus();
  }
  function hideSourceEditForm() {
    state.editingSourceId = '';
    el('source-edit-form').hidden = true;
    el('source-display-name').value = '';
    el('source-edit-path').value = '';
  }
  async function updateSource(event) {
    event.preventDefault();
    const name = el('source-display-name').value.trim();
    if (!name) { setSettingsFeedback('请填写来源名称。', 'warn'); return; }
    setBusy(true, '正在保存来源…');
    try {
      await bridgeInvoke('media-library.source.update', { id: state.editingSourceId, display_name: name });
      hideSourceEditForm(); await loadSources(); setSettingsFeedback('本机来源已更新。');
    } catch (error) { setSettingsFeedback('保存来源失败：' + describe(error), 'error'); }
    finally { setBusy(false); }
  }
  async function removeSource(sourceId) {
    const source = state.sources.find((item) => item.id === sourceId);
    if (!source) return;
    if (!(await showConfirm({ title: '移除本机来源？', message: '将移除“' + source.display_name + '”及其候选索引。磁盘文件不会被删除。', confirmText: '移除来源', danger: true }))) return;
    setBusy(true, '正在移除本机来源…');
    try {
      await bridgeInvoke('media-library.source.remove', { id: sourceId });
      if (state.editingSourceId === sourceId) hideSourceEditForm();
      await loadSources(); if (state.query) await runSearch();
      setSettingsFeedback('本机来源已移除，磁盘文件未删除。');
    } catch (error) { setSettingsFeedback('移除本机来源失败：' + describe(error), 'error'); }
    finally { setBusy(false); }
  }

  async function pickFolder() { try { const internals = window.__TAURI_INTERNALS__ || {}; const result = await internals.invoke('pick_workspace_directory', {}); if (result && result.path) el('source-path').value = result.path; } catch (_) { setSettingsFeedback('无法打开文件夹选择器，请直接填写路径。', 'warn'); } }

  async function scanSource(sourceId) {
    state.scanningSourceId = sourceId; state.scanProgress = '正在扫描…'; renderSources(); renderIndexOverview();
    let restart = true; let total = 0;
    try { for (let round = 0; round < 2000; round += 1) { const payload = await bridgeInvoke('media-library.source.scan.batch', { source_id: sourceId, restart, max_entries: 200 }); restart = false; total += Number(payload.indexed_files || 0); state.scanProgress = '扫描中 · 已索引 ' + total + ' 个文件'; renderSources(); if (payload.done) break; } await loadSources(); setSettingsFeedback('扫描完成，本次索引 ' + total + ' 个文件。'); }
    finally { state.scanningSourceId = ''; state.scanProgress = ''; renderSources(); renderIndexOverview(); }
  }

  async function addAndScanSource() {
    const path = el('source-path').value.trim(); if (!path) { setFeedback('请选择或填写本地文件夹。', 'warn'); return; }
    setBusy(true, '正在添加文件夹…');
    try { const source = await bridgeInvoke('media-library.source.add', { path }); el('source-path').value = ''; await loadSources(); await scanSource(source.id); }
    catch (error) { setSettingsFeedback('添加文件夹失败：' + describe(error), 'error'); }
    finally { setBusy(false); }
  }

  async function scanAllSources() { if (!state.sources.length) { setSettingsFeedback('还没有可扫描的本机来源。', 'warn'); return; } setBusy(true, '正在扫描全部来源…'); try { for (const source of state.sources) await scanSource(source.id); setSettingsFeedback('全部来源扫描完成。'); } catch (error) { setSettingsFeedback('扫描失败：' + describe(error), 'error'); } finally { setBusy(false); } }
  async function refreshAll() { setBusy(true, '正在刷新…'); try { await loadProviders(); await loadLibraries(); await loadSources(); if (state.query) await runSearch(); else setFeedback(''); } catch (error) { setFeedback('刷新失败：' + describe(error), 'error'); } finally { setBusy(false); } }

  function trapFocus(event, container) {
    if (event.key !== 'Tab') return;
    const focusable = Array.from(container.querySelectorAll('button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex="0"]')).filter((node) => !node.hidden && node.offsetParent !== null);
    if (!focusable.length) return;
    const first = focusable[0]; const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }

  function bind() {
    readPrefs(); updateScopeUI();
    el('search-form').addEventListener('submit', (event) => { event.preventDefault(); void runSearch(); });
    el('refresh').addEventListener('click', () => { void refreshAll(); });
    el('load-more').addEventListener('click', () => { void loadMoreResults(); });
    el('scope-group').addEventListener('click', (event) => { const button = event.target.closest('[data-scope]'); if (!button) return; state.scope = button.getAttribute('data-scope'); savePrefs(); updateScopeUI(); });
    el('kind-group').addEventListener('click', (event) => { const button = event.target.closest('[data-kind]'); if (!button) return; const kind = button.getAttribute('data-kind'); if (state.kinds.has(kind)) state.kinds.delete(kind); else state.kinds.add(kind); savePrefs(); updateScopeUI(); });
    el('filter-toggle').addEventListener('click', () => { if (state.filterOpen) closeFilters(); else openFilters(); });
    el('apply-filters').addEventListener('click', applyFilters); el('clear-filters').addEventListener('click', clearFilters);
    el('active-filters').addEventListener('click', (event) => { const button = event.target.closest('[data-clear-filter]'); if (!button) return; const key = button.getAttribute('data-clear-filter'); if (key === 'providers') state.providerFilters = new Set(); else state.filters[key] = ''; updateFilterCount(); renderProviderFilters(); });
    el('results').addEventListener('click', (event) => {
      const settingsButton = event.target.closest('[data-open-settings]');
      if (settingsButton) { openSettings(settingsButton.getAttribute('data-open-settings')); return; }
      if (event.target.closest('[data-clear-all-filters]')) { clearFilters(); renderResults(); return; }
      const playButton = event.target.closest('[data-play-index]');
      if (playButton) { event.preventDefault(); void toggleCardAudio(Number(playButton.getAttribute('data-play-index'))); return; }
      const openButton = event.target.closest('[data-open-result]');
      if (openButton) void openPreview(Number(openButton.getAttribute('data-open-result')));
    });
    el('close-preview').addEventListener('click', closePreview); el('preview-scrim').addEventListener('click', closePreview); el('previous-preview').addEventListener('click', () => navigatePreview(-1)); el('next-preview').addEventListener('click', () => navigatePreview(1)); el('adopt-button').addEventListener('click', () => { void adoptCurrent(); });
    el('target-library').addEventListener('change', () => { state.defaultLibraryId = el('target-library').value; savePrefs(); renderLibraries(); if (state.previewItem) updateAdoptionControls(state.previewItem); });
    el('adopt-library').addEventListener('change', () => { state.previewLibraryId = el('adopt-library').value; setPreviewFeedback(''); if (state.previewItem) updateAdoptionControls(state.previewItem); });
    el('open-settings').addEventListener('click', () => openSettings('libraries')); el('close-settings').addEventListener('click', closeSettings);
    el('settings-modal').addEventListener('click', (event) => { if (event.target.closest('[data-close-settings]')) void closeSettings(); });
    el('settings-modal').querySelectorAll('[data-settings-tab]').forEach((button) => button.addEventListener('click', () => switchSettingsTab(button.getAttribute('data-settings-tab'))));
    el('settings-modal').querySelector('.settings-nav').addEventListener('keydown', (event) => {
      const tabs = Array.from(el('settings-modal').querySelectorAll('[data-settings-tab]'));
      const current = tabs.indexOf(document.activeElement);
      if (current < 0) return;
      let next = current;
      if (event.key === 'ArrowRight' || event.key === 'ArrowDown') next = (current + 1) % tabs.length;
      else if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') next = (current - 1 + tabs.length) % tabs.length;
      else if (event.key === 'Home') next = 0;
      else if (event.key === 'End') next = tabs.length - 1;
      else return;
      event.preventDefault();
      switchSettingsTab(tabs[next].getAttribute('data-settings-tab'));
      tabs[next].focus();
    });
    el('show-library-form').addEventListener('click', () => showLibraryForm()); el('cancel-library-form').addEventListener('click', () => { void hideLibraryForm(false); }); el('library-form').addEventListener('submit', (event) => { void createLibrary(event); });
    ['library-name', 'library-root', 'library-backend', 'library-access'].forEach((id) => el(id).addEventListener('input', () => { state.libraryDraftDirty = true; }));
    el('libraries').addEventListener('click', (event) => {
      if (event.target.closest('[data-open-library-form]')) { showLibraryForm(); return; }
      const edit = event.target.closest('[data-edit-library]'); if (edit) { showLibraryForm(edit.getAttribute('data-edit-library')); return; }
      const remove = event.target.closest('[data-remove-library]'); if (remove) void removeLibrary(remove.getAttribute('data-remove-library'));
    });
    el('pick-folder').addEventListener('click', () => { void pickFolder(); }); el('add-source').addEventListener('click', () => { void addAndScanSource(); });
    el('source-edit-form').addEventListener('submit', (event) => { void updateSource(event); }); el('cancel-source-edit').addEventListener('click', hideSourceEditForm);
    el('cancel-confirm').addEventListener('click', () => resolveConfirm(false)); el('accept-confirm').addEventListener('click', () => resolveConfirm(true));
    el('confirm-modal').addEventListener('click', (event) => { if (event.target.closest('[data-cancel-confirm]')) resolveConfirm(false); });
    el('sources').addEventListener('click', (event) => {
      const scan = event.target.closest('[data-scan-source]'); if (scan) { setBusy(true, '正在扫描…'); scanSource(scan.getAttribute('data-scan-source')).catch((error) => setSettingsFeedback('扫描失败：' + describe(error), 'error')).finally(() => setBusy(false)); return; }
      const edit = event.target.closest('[data-edit-source]'); if (edit) { showSourceEditForm(edit.getAttribute('data-edit-source')); return; }
      const remove = event.target.closest('[data-remove-source]'); if (remove) void removeSource(remove.getAttribute('data-remove-source'));
    });
    el('refresh-providers').addEventListener('click', () => { void loadProviders().catch((error) => setSettingsFeedback('刷新来源失败：' + describe(error), 'error')); }); el('scan-all-sources').addEventListener('click', () => { void scanAllSources(); });
    document.addEventListener('contextmenu', (event) => {
      const editable = event.target.closest('input, textarea, [contenteditable="true"]');
      const selection = window.getSelection ? String(window.getSelection()) : '';
      if (!editable && !selection) event.preventDefault();
    });

    document.addEventListener('keydown', (event) => {
      if (state.confirmOpen) {
        if (event.key === 'Escape') { event.preventDefault(); resolveConfirm(false); }
        else trapFocus(event, el('confirm-modal').querySelector('.confirm-dialog'));
        return;
      }
      if (event.key === 'Escape') {
        if (state.settingsOpen) void closeSettings();
        else if (state.previewOpen) closePreview();
        else if (state.filterOpen) closeFilters();
      }
      if (state.previewOpen && !state.settingsOpen && (event.key === 'ArrowLeft' || event.key === 'ArrowRight')) {
        const target = event.target;
        if (!target.closest('audio, video, input, select, textarea')) {
          event.preventDefault();
          navigatePreview(event.key === 'ArrowLeft' ? -1 : 1);
        }
      }
      if (state.settingsOpen) trapFocus(event, el('settings-modal').querySelector('.settings-dialog'));
      else if (state.previewOpen) trapFocus(event, el('preview'));
    });
  }

  async function start() { bind(); renderResults(); try { await loadProviders(); await loadLibraries(); await loadSources(); renderIndexOverview(); } catch (error) { setFeedback(describe(error), 'error'); } }
  document.addEventListener('DOMContentLoaded', () => { void start(); });
})();
