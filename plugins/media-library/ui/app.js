(function () {
  'use strict';

  const STORAGE_KEY = 'himind.media-library.ui.v2';
  const ALL_KINDS = ['image', 'video', 'audio'];
  const state = {
    providers: [], libraries: [], sources: [], results: [], localResults: [], onlineResults: [], nextCursor: '', loadingMore: false, query: '',
    scope: 'all', kinds: new Set(ALL_KINDS), providerFilters: new Set(),
    filters: { orientation: '', minWidth: '', minDuration: '', folder: '', resolution: '', duration: '' },
    defaultLibraryId: '', previewLibraryId: '', cardAudio: null, cardAudioLoadingIndex: -1, previewItem: null, previewIndex: -1, previewRequestId: 0,
    settingsOpen: false, previewOpen: false, filterOpen: false, busy: false,
    scanned: false, scanningSourceId: '', scanProgress: '', libraryDraftDirty: false,
    thumbs: {}, thumbPaths: {}, hydrationToken: 0, hydrating: false, aiBatchToken: 0,
    aiJob: null, aiJobMapping: null, aiApplied: {}, aiPoll: null,
    view: 'assets', selectionList: [], selectionLibrary: null, selectionCreateOpen: false,
    pipelineFilter: '', selectionSort: 'rank', groupSimilar: false, selectionDirty: false,
    mediaWarmed: false, previewList: null, seenContent: new Set(),
    libraryView: null,
    selection: null, selectionItems: [], selectionBusy: false, selectionLastFocus: null,
    lastFocus: null, settingsLastFocus: null, editingLibraryId: '', editingSourceId: '', confirmOpen: false, confirmResolver: null, confirmLastFocus: null,
  };
  const el = (id) => document.getElementById(id);
	let intake = null;
  let audio = null;
  let previewUI = null;
  let generationUI = null;
  let imageEditorUI = null;

  function readPrefs() {
    try {
      const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}');
      if (['all', 'online', 'library', 'sources'].includes(stored.scope)) state.scope = stored.scope;
      if (Array.isArray(stored.kinds) && stored.kinds.length) state.kinds = new Set(stored.kinds.filter((kind) => ALL_KINDS.includes(kind)));
      if (typeof stored.defaultLibraryId === 'string') state.defaultLibraryId = stored.defaultLibraryId;
      if (stored.filters && typeof stored.filters === 'object') Object.assign(state.filters, stored.filters);
    } catch (_) {}
  }

  function savePrefs() {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({ scope: state.scope, kinds: Array.from(state.kinds), defaultLibraryId: state.defaultLibraryId, filters: state.filters }));
    } catch (_) {}
  }

  function syncFilterControls() {
    const mapping = {
      orientation: 'filter-orientation', minWidth: 'filter-min-width', minDuration: 'filter-min-duration',
      folder: 'filter-folder', resolution: 'filter-resolution', duration: 'filter-duration',
    };
    Object.entries(mapping).forEach(([key, id]) => {
      const control = el(id);
      if (control) control.value = state.filters[key] || '';
    });
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
    let message = typeof error === 'string' ? error : error && error.message;
    if (typeof message !== 'string') return '操作未完成，请重试。';
    const rpc = message.match(/^plugin capability [\w.-]+ failed: (\{[\s\S]*\})$/);
    if (rpc) {
      try { message = JSON.parse(rpc[1]).message; } catch (_) { return '操作未完成，请重试。'; }
    }
    if (typeof message !== 'string') return '操作未完成，请重试。';
    return message.replace(/^site_connection_(required|expired|changed):\s*/, '');
  }

  function escapeHtml(value) {
    return String(value == null ? '' : value).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }

  function icon(name, cls) {
    return '<svg class="icon' + (cls ? ' ' + cls : '') + '" aria-hidden="true"><use href="#icon-' + name + '"/></svg>';
  }

  async function copyText(value) {
    const text = String(value || '');
    if (!text) return false;
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        await navigator.clipboard.writeText(text);
        return true;
      }
    } catch (_) {}
    try {
      const area = document.createElement('textarea');
      area.value = text;
      area.style.cssText = 'position:fixed;left:-9999px;top:0';
      document.body.appendChild(area);
      area.select();
      const ok = document.execCommand('copy');
      area.remove();
      return ok;
    } catch (_) {
      return false;
    }
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
    document.body.classList.toggle('has-overlay', state.settingsOpen || state.previewOpen || !el('intake-modal').hidden || !el('acquisitions-modal').hidden || !el('image-editor-modal').hidden);
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
    if (state.scope !== 'library' || state.kinds.size !== 1 || !state.kinds.has('audio')) state.bgm = false;
    el('browse-bgm').classList.toggle('active', Boolean(state.bgm)); el('browse-bgm').setAttribute('aria-pressed', String(Boolean(state.bgm)));
    const onlineOnly = state.scope === 'online' || state.scope === 'all';
    el('provider-filter-group').hidden = !onlineOnly;
    el('filter-toggle').disabled = false;
    el('filter-toggle').title = '筛选';
  }

  function renderTargetLibrary() {
    const select = el('target-library');
    const materials = (state.libraries || []).filter((library) => (library.kind || 'material') !== 'curated');
    if (!materials.length) {
      select.innerHTML = '<option value="">' + (state.libraries.length ? '没有可用素材库' : '未配置资源库') + '</option>';
      select.disabled = true;
      state.defaultLibraryId = '';
      return;
    }
    select.disabled = false;
    select.innerHTML = materials.map((library) => '<option value="' + escapeHtml(library.id) + '">' + escapeHtml(library.name) + '</option>').join('');
    if (materials.some((library) => library.id === state.defaultLibraryId)) select.value = state.defaultLibraryId;
    else { state.defaultLibraryId = materials[0].id; select.value = state.defaultLibraryId; }
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
      return '<button type="button" class="filter-chip provider-filter" data-provider="' + escapeHtml(provider.id) +
        '" aria-pressed="' + (checked ? 'true' : 'false') + '">' + icon('check') + escapeHtml(provider.name) + '</button>';
    }).join('');
    host.querySelectorAll('.provider-filter').forEach((button) => button.addEventListener('click', () => {
      if (state.providerFilters.size === 0) {
        // 首次取消某个来源时，把其余来源显式选中，避免“空集合=全选”的歧义。
        for (const provider of state.providers) {
          if (provider.id !== button.getAttribute('data-provider')) state.providerFilters.add(provider.id);
        }
      } else {
        const id = button.getAttribute('data-provider');
        if (state.providerFilters.has(id)) state.providerFilters.delete(id);
        else state.providerFilters.add(id);
        if (state.providerFilters.size >= state.providers.length) state.providerFilters.clear();
      }
      renderProviderFilters();
      handleProviderFilterChange();
    }));
    updateFilterCount();
  }

  async function loadFacets() {
    try {
      const payload = await bridgeInvoke('media-library.asset.facets', {});
      const select = el('filter-folder');
      if (!select) return;
      const current = state.filters.folder || select.value;
      const folders = payload.folders || [];
      select.innerHTML = '<option value="">全部</option>' + folders.map((folder) => {
        const label = String(folder.label || folder.value).split('/').filter(Boolean).pop() || folder.value;
        return '<option value="' + escapeHtml(folder.value) + '" title="' + escapeHtml(folder.value) + '">' + escapeHtml(label) + ' (' + folder.count + ')</option>';
      }).join('');
      select.value = folders.some((folder) => folder.value === current) ? current : '';
      if (select.value !== current) state.filters.folder = select.value;
      updateFilterCount();
    } catch (_) {
      // facet 是增强项，失败时保留“全部”。
    }
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
    const adoptable = state.libraries.filter((library) => (library.kind || 'material') !== 'curated');
    if (!adoptable.length) {
      select.innerHTML = '<option value="">仅配置了精选库</option>';
      select.disabled = true;
      state.previewLibraryId = '';
      return;
    }
    select.innerHTML = adoptable.map((library) => '<option value="' + escapeHtml(library.id) + '">' + escapeHtml(library.name) + (library.access_mode === 'read_only' ? ' · 只读' : '') + '</option>').join('');
    const preferred = adoptable.some((library) => library.id === state.previewLibraryId) ? state.previewLibraryId : state.defaultLibraryId;
    select.value = adoptable.some((library) => library.id === preferred) ? preferred : adoptable[0].id;
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
      const curated = (library.kind || 'material') === 'curated';
      const kindLabel = curated ? '精选库' : '素材库';
      const mediumLabel = library.backend_type === 'smb' ? 'SMB 目录' : '本地目录';
      const stateLabel = library.state === 'online' ? '可访问' : '离线';
      return '<div class="config-row"><div class="config-row-main"><strong>' + escapeHtml(library.name) + (active ? ' · 默认' : '') + '</strong><small>' + escapeHtml(kindLabel) + ' · ' + escapeHtml(mediumLabel) + ' · ' + escapeHtml(stateLabel) + ' · ' + library.asset_count + ' 项</small><small title="' + escapeHtml(library.root_uri || '') + '">' + escapeHtml(library.root_uri || '') + '</small></div><div class="config-actions">' + (active ? '<span class="status ok">默认入库</span>' : '') + '<button type="button" class="ghost" data-view-library="' + escapeHtml(library.id) + '" aria-label="查看资源库 ' + escapeHtml(library.name) + ' 的内容" title="查看库内容">' + icon('list') + '查看</button><button type="button" class="ghost" data-edit-library="' + escapeHtml(library.id) + '" aria-label="编辑资源库 ' + escapeHtml(library.name) + '" title="编辑资源库">编辑</button><button type="button" class="ghost danger-text" data-remove-library="' + escapeHtml(library.id) + '" aria-label="移除资源库 ' + escapeHtml(library.name) + '" title="移除资源库">移除</button></div></div>';
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

  function sourceModeText(source) {
    if (source.metadata_mode === 'jsonl') return '标记写来源';
    if (source.metadata_mode === 'both') return '标记双写';
    return '标记存本机';
  }

  function renderSources() {
    const host = el('sources');
    if (!host) return;
    if (!state.sources.length) { host.innerHTML = '<div class="empty-state"><strong>还没有本机来源</strong><span class="meta">添加文件夹后会建立候选索引，不会自动进入资源库。</span></div>'; return; }
    host.innerHTML = state.sources.map((source) => '<div class="config-row"><div class="config-row-main"><strong>' + escapeHtml(source.display_name) + '</strong><small title="' + escapeHtml(source.path) + '">' + escapeHtml(source.path) + '</small><small>' + escapeHtml(sourceStatusText(source)) + ' · ' + escapeHtml(sourceModeText(source)) + '</small></div><div class="config-actions">' + '<button type="button" class="ghost" data-scan-source="' + escapeHtml(source.id) + '"' + (state.busy ? ' disabled' : '') + '>扫描</button><button type="button" class="ghost" data-edit-source="' + escapeHtml(source.id) + '" aria-label="编辑本机来源 ' + escapeHtml(source.display_name) + '" title="编辑本机来源">编辑</button><button type="button" class="ghost danger-text" data-remove-source="' + escapeHtml(source.id) + '" aria-label="移除本机来源 ' + escapeHtml(source.display_name) + '" title="移除本机来源">移除</button></div></div>').join('');
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
    if (state.filters.folder) count += 1;
    if (state.filters.resolution) count += 1;
    if (state.filters.duration) count += 1;
    const badge = el('filter-count');
    badge.hidden = count === 0;
    badge.textContent = String(count);
    renderActiveFilters();
  }

  function filterLabel(key, value) {
    if (key === 'orientation') return ({ landscape: '横屏', portrait: '竖屏', square: '方形' }[value] || value);
    if (key === 'minWidth') return '≥ ' + value + 'px';
    if (key === 'minDuration') return '≥ ' + value + 's';
    if (key === 'folder') return String(value).split('/').filter(Boolean).pop() || value;
    if (key === 'resolution') return ({ 3800: '4K', 2500: '2K', 1900: '1080p', 1200: '720p' }[value] || value);
    if (key === 'duration') return ({ short: '5 秒内', medium: '5–30 秒', long: '30–120 秒', longer: '2 分钟以上' }[value] || value);
    return value;
  }

  function renderActiveFilters() {
    const host = el('active-filters');
    const chips = [];
    if (state.providerFilters.size && state.providerFilters.size < state.providers.length) chips.push({ key: 'providers', text: state.providerFilters.size + ' 个来源' });
    ['orientation', 'minWidth', 'minDuration', 'folder', 'resolution', 'duration'].forEach((key) => { if (state.filters[key]) chips.push({ key, text: filterLabel(key, state.filters[key]) }); });
    host.hidden = chips.length === 0;
    host.innerHTML = chips.map((chip) => '<span class="filter-chip">' + escapeHtml(chip.text) + '<button type="button" data-clear-filter="' + chip.key + '" aria-label="移除筛选">×</button></span>').join('');
  }

  function handleProviderFilterChange() {
    updateFilterCount();
    renderActiveFilters();
  }

  function openFilters() { if (el('filter-toggle').disabled) return; state.filterOpen = true; el('filter-popover').hidden = false; el('filter-toggle').setAttribute('aria-expanded', 'true'); }
  function closeFilters() { state.filterOpen = false; if (el('filter-popover')) el('filter-popover').hidden = true; el('filter-toggle').setAttribute('aria-expanded', 'false'); }
  function clearFilters() {
    state.providerFilters = new Set();
    state.filters = { orientation: '', minWidth: '', minDuration: '', folder: '', resolution: '', duration: '' };
    el('filter-orientation').value = '';
    el('filter-min-width').value = '';
    el('filter-min-duration').value = '';
    el('filter-folder').value = '';
    el('filter-resolution').value = '';
    el('filter-duration').value = '';
    renderProviderFilters();
    updateFilterCount();
    savePrefs();
    if (state.query) void runSearch();
  }

  function applyFilters() {
    state.filters.orientation = el('filter-orientation').value;
    state.filters.minWidth = el('filter-min-width').value.trim();
    state.filters.minDuration = el('filter-min-duration').value.trim();
    state.filters.folder = el('filter-folder').value;
    state.filters.resolution = el('filter-resolution').value;
    state.filters.duration = el('filter-duration').value;
    handleProviderFilterChange();
    savePrefs();
    closeFilters();
    if (state.query) void runSearch();
  }
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
    const onlineCount = results.filter((item) => item.origin === 'online').length;
    const curatedCount = results.filter((item) => item.entry_type === 'library' && item.library_kind === 'curated').length;
    const libraryCount = results.filter((item) => item.entry_type === 'library' && item.library_kind !== 'curated').length;
    const candidateCount = results.length - onlineCount - curatedCount - libraryCount;
    el('results-title').textContent = results.length ? '找到 ' + results.length + ' 项' : '没有匹配结果';
    const parts = [];
    if (onlineCount) parts.push('在线 ' + onlineCount);
    if (libraryCount) parts.push('资源库 ' + libraryCount);
    if (curatedCount) parts.push('精选库 ' + curatedCount);
    if (candidateCount) parts.push('本机 ' + candidateCount);
    el('result-detail').textContent = parts.join(' · ');
  }

  function localSearchFilters() {
    const filters = {};
	if (state.bgm) filters.audio_purpose = 'bgm';
    if (state.filters.folder) filters.folder = state.filters.folder;
    if (state.filters.resolution) filters.min_dimension = Number(state.filters.resolution);
    const duration = state.filters.duration;
    if (duration === 'short') filters.max_duration_ms = 5000;
    else if (duration === 'medium') { filters.min_duration_ms = 5000; filters.max_duration_ms = 30000; }
    else if (duration === 'long') { filters.min_duration_ms = 30000; filters.max_duration_ms = 120000; }
    else if (duration === 'longer') filters.min_duration_ms = 120000;
    return filters;
  }

  function onlineDurationSeconds() {
    if (state.filters.minDuration) return Number(state.filters.minDuration);
    if (state.filters.duration === 'medium') return 5;
    if (state.filters.duration === 'long') return 30;
    if (state.filters.duration === 'longer') return 120;
    return undefined;
  }

  async function loadMoreResults() {
    if (!state.nextCursor || state.loadingMore || state.busy) return;
    state.loadingMore = true; setBusy(true, '正在加载更多…');
    try {
      const localScope = state.scope === 'online' ? 'all' : state.scope;
      const payload = await bridgeInvoke('media-library.asset.search', Object.assign({ query: state.query, kinds: selectedKinds(), scope: localScope, limit: 60, cursor: state.nextCursor }, localSearchFilters()));
      const existing = new Set(state.localResults.map((item) => item.id));
      for (const item of (payload.items || [])) {
        if (existing.has(item.id)) continue;
        // 跨页去重：同一内容在上一页已经出现过（库条目 + 本机副本）。
        if (item.content_key && state.seenContent.has(item.content_key)) continue;
        if (item.content_key) state.seenContent.add(item.content_key);
        state.localResults.push(Object.assign({}, item, { origin: item.entry_type || 'candidate' }));
      }
      state.nextCursor = payload.next_cursor || '';
      state.results = combineSearchResults();
      renderResults(); updateResultSummary(); setFeedback('');
    } catch (error) { setFeedback('加载更多失败：' + describe(error), 'error'); }
    finally { state.loadingMore = false; setBusy(false); }
  }

  async function runSearch() {
    const query = el('query').value.trim();
    if (!query && !state.bgm) {
      state.query = '';
      state.scanned = false;
      state.results = [];
      state.localResults = [];
      state.onlineResults = [];
      state.nextCursor = '';
      state.hydrationToken += 1;
      updateResultSummary();
      renderResults();
      setFeedback('请输入搜索关键词。', 'warn');
      el('query').focus();
      return;
    }
    const kinds = selectedKinds();
    if (!kinds.length) { setFeedback('至少选择一种媒体类型。', 'warn'); return; }
    state.query = query; state.scanned = true; state.nextCursor = ''; state.localResults = []; state.onlineResults = []; state.results = [];
    state.seenContent = new Set();
    state.hydrationToken += 1;
    setFeedback(''); setBusy(true, '正在搜索…'); skeletonResults();
    el('results-title').textContent = '正在搜索…'; el('result-detail').textContent = query;
    try {
      const localResults = []; const onlineResults = []; const warnings = [];
      if (state.scope === 'all' || state.scope === 'library' || state.scope === 'sources') {
        const localScope = state.scope === 'online' ? 'all' : state.scope;
        const local = await bridgeInvoke('media-library.asset.search', Object.assign({ query, kinds, scope: localScope, limit: 60 }, localSearchFilters()));
        localResults.push(...(local.items || []).map((item) => Object.assign({}, item, { origin: item.entry_type || 'candidate' })));
        state.nextCursor = local.next_cursor || '';
      }
      if (state.scope === 'all' || state.scope === 'online') {
        try {
          const inputs = { query, kinds, per_source: 12, page: 1, providers: state.providerFilters.size ? Array.from(state.providerFilters) : undefined, orientation: state.filters.orientation || undefined, min_width: state.filters.minWidth ? Number(state.filters.minWidth) : (state.filters.resolution ? Number(state.filters.resolution) : undefined), min_duration_seconds: onlineDurationSeconds() };
          Object.keys(inputs).forEach((key) => { if (inputs[key] === undefined) delete inputs[key]; });
          const online = await bridgeInvoke('media-library.search', inputs);
          onlineResults.push(...(online.items || []).map((item) => Object.assign({}, item, { origin: 'online' })));
          warnings.push(...(online.warnings || []));
        } catch (error) { warnings.push('在线来源：' + describe(error)); }
      }
      state.localResults = localResults; state.onlineResults = onlineResults; state.results = combineSearchResults();
      state.seenContent = new Set();
      for (const item of state.results) {
        if (item.content_key) state.seenContent.add(item.content_key);
      }
      renderResults(); updateResultSummary();
      if (warnings.length) { const configured = state.providers.some((provider) => provider.configured); setFeedback(configured ? '部分来源不可用：' + warnings.join('；') : '在线来源尚未配置。打开设置查看来源状态。', 'warn', 'online'); }
      else if (!state.results.length) setFeedback('没有匹配结果，尝试更换关键词或减少筛选条件。', 'warn');
    } catch (error) { state.results = []; state.localResults = []; state.onlineResults = []; state.nextCursor = ''; renderResults(); el('results-title').textContent = '搜索失败'; el('result-detail').textContent = ''; setFeedback(describe(error), 'error'); }
    finally { setBusy(false); }
  }

  function resolutionLabel(width, height) {
    const w = Number(width) || 0;
    const h = Number(height) || 0;
    if (!w || !h) return '';
    const longSide = Math.max(w, h);
    if (longSide >= 5000) return '5K+';
    if (longSide >= 3800) return '4K';
    if (longSide >= 2500) return '2K';
    if (longSide >= 1900) return '1080p';
    if (longSide >= 1200) return '720p';
    return w + '×' + h;
  }

  function resultMeta(item) {
    if (item.origin !== 'online') {
      return [
        resolutionLabel(item.width, item.height),
        item.duration_ms ? formatDuration(Number(item.duration_ms) / 1000) : '',
        item.bytes ? formatBytes(item.bytes) : '',
        item.folder,
      ].filter(Boolean);
    }
    return [item.author, item.license || item.license_name, item.bytes ? formatBytes(item.bytes) : ''].filter(Boolean);
  }
  function sourceLabel(item) { if (item.origin === 'generation') return 'AI 生成候选'; if (item.origin === 'generation-reference') return '生成参考图'; if (item.origin === 'platform') return ({ douyin: '抖音', bilibili: '哔哩哔哩', youtube: 'YouTube' })[item.provider] || '在线作品'; if (item.origin === 'online') return providerName(item.provider); if (item.entry_type === 'library') return item.library_kind === 'curated' ? '精选库' : '资源库'; return '本机'; }
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
      const src = item.origin === 'online' ? (item.kind === 'audio' ? (item.download_url || item.preview_url) : (item.preview_url || item.download_url)) : await (async () => { const local = await previewLocal(item); return local.stream_url || local.data_url || local.file_url || toFileUrl(item.path); })();
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
    if (!state.results.length && state.bgm) { host.innerHTML = '<div class="empty-state"><strong>没有匹配的 BGM</strong><span class="meta">在视频或音频预览中提取音轨入库，也可以添加视频链接提取音频。</span><div class="empty-actions"><button type="button" class="ghost" data-add-bgm-link>添加视频链接</button><button type="button" class="ghost" data-clear-all-filters>清空筛选</button></div></div>'; return; }
    if (!state.results.length) { host.innerHTML = '<div class="empty-state"><strong>没有匹配结果</strong><span class="meta">更换关键词，或清除来源和类型筛选后再试。</span><div class="empty-actions"><button type="button" class="ghost" data-clear-all-filters>清空筛选</button><button type="button" class="ghost" data-open-settings="libraries">检查资源库</button></div></div>'; return; }
    host.innerHTML = state.results.map((item, index) => {
      const title = item.title || item.name || item.id || '素材';
      const localThumb = item.origin !== 'online' && item.id && state.thumbs[item.id] ? '<img class="result-thumb-img' + (item.kind === 'audio' ? ' result-waveform' : '') + '" src="' + escapeHtml(state.thumbs[item.id]) + '" alt="" loading="lazy" />' : '';
      const thumb = item.origin === 'online' && item.preview_url && item.kind === 'audio' ? '<img class="result-waveform" src="' + escapeHtml(item.preview_url) + '" alt="" loading="lazy" />' : item.origin === 'online' && item.preview_url && item.kind !== 'audio' ? '<img src="' + escapeHtml(item.preview_url) + '" alt="" loading="lazy" />' : localThumb || '<span class="result-icon" aria-hidden="true">' + (item.kind === 'audio' ? '♪' : item.kind === 'video' ? '▶' : '▧') + '</span>';
      const sourceClass = item.origin === 'online' ? 'online' : item.entry_type === 'library' ? (item.library_kind === 'curated' ? 'library curated' : 'library') : 'candidate';
      const sourceIcon = item.origin === 'online' ? 'cloud' : item.entry_type === 'library' ? 'library' : 'monitor';
      const duplicateMark = item.also_local ? '<span class="tag-dup" title="本机也有同一文件">' + icon('link') + '</span>' : '';
      const playback = item.kind === 'audio' ? '<div class="card-audio-playback"><button type="button" class="result-play" data-play-index="' + index + '" data-audio-title="' + escapeHtml(title) + '" aria-label="播放 ' + escapeHtml(title) + '" aria-pressed="false"><svg class="play-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="m9 6 9 6-9 6Z"/></svg><svg class="pause-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M8 6v12M16 6v12"/></svg></button><span class="card-audio-progress" aria-hidden="true"><span></span></span><span class="card-audio-time">试听</span></div>' : '';
      const tags = Array.isArray(item.tags) ? item.tags.slice(0, 2) : [];
      const tagMarkup = tags.length ? '<span class="result-tags">' + tags.map((tag) => '<span class="result-tag">' + escapeHtml(tag) + '</span>').join('') + '</span>' : '';
      return '<article class="result-card" data-result-index="' + index + '" data-asset-id="' + escapeHtml(item.id || '') + '" data-kind="' + escapeHtml(item.kind || '') + '"><span class="result-thumb">' + thumb + '<span class="source-tag ' + sourceClass + '">' + icon(sourceIcon) + escapeHtml(sourceLabel(item)) + duplicateMark + '</span>' + playback + '</span><span class="result-body"><span class="result-title">' + escapeHtml(title) + '</span><span class="result-meta">' + resultMeta(item).map((value) => '<span>' + escapeHtml(value) + '</span>').join('') + '</span>' + tagMarkup + '</span><button type="button" class="result-open" data-open-result="' + index + '" aria-label="预览 ' + escapeHtml(title) + '"></button></article>';
    }).join('');
    void hydrateLocalResults();
  }

  function resultCardFor(id) {
    if (!id) return null;
    return document.querySelector('.result-card[data-asset-id="' + String(id).replace(/"/g, '') + '"]');
  }

  function applyThumbnail(id, dataURL) {
    if (!id || !dataURL) return;
    state.thumbs[id] = dataURL;
    const card = resultCardFor(id);
    if (!card) return;
    const holder = card.querySelector('.result-thumb');
    if (!holder) return;
    let img = holder.querySelector('img.result-thumb-img');
    if (!img) {
      img = document.createElement('img');
      img.className = 'result-thumb-img';
      img.loading = 'lazy';
      img.alt = '';
      const item = state.results.find((entry) => entry.id === id);
      if (item && item.kind === 'audio') img.classList.add('result-waveform');
      holder.insertBefore(img, holder.firstChild);
      const icon = holder.querySelector('.result-icon');
      if (icon) icon.remove();
    }
    img.src = dataURL;
  }

  function patchResultMetadata(ids) {
    for (const id of ids) {
      const item = state.results.find((entry) => entry.id === id);
      const card = resultCardFor(id);
      if (!item || !card) continue;
      const meta = card.querySelector('.result-meta');
      if (meta) meta.innerHTML = resultMeta(item).map((value) => '<span>' + escapeHtml(value) + '</span>').join('');
    }
  }

  async function hydrateLocalResults() {
    if (state.hydrating) return;
    state.hydrating = true;
    const token = ++state.hydrationToken;
    try {
      // 资源库条目本身没有缩略图：用它的本机路径找回同一素材，复用已生成的缩略图。
      const libraryItems = state.results
        .filter((item) => item.entry_type === 'library' && item.candidate_id && !state.thumbs[item.id])
        .slice(0, 24);
      if (libraryItems.length) {
        const byCandidate = new Map(libraryItems.map((item) => [item.candidate_id, item]));
        const payload = await bridgeInvoke('media-library.asset.thumbnail', {
          asset_ids: libraryItems.map((item) => item.candidate_id), size: 'grid',
        });
        if (token !== state.hydrationToken) return;
        for (const thumb of (payload.items || [])) {
          const item = byCandidate.get(thumb.id);
          if (!item || !thumb.data_url) continue;
          state.thumbs[item.id] = thumb.data_url;
          applyThumbnail(item.id, thumb.data_url);
        }
      }
      for (let round = 0; round < 3; round += 1) {
        const pendingItems = state.results.filter((item) => item.entry_type === 'candidate' && item.id && !state.thumbs[item.id]).slice(0, 24);
        if (!pendingItems.length) break;
        const needsEnrich = pendingItems.filter((item) => item.probe_state !== 'ok' || item.thumb_state !== 'ok');
        let progress = 0;
        if (needsEnrich.length) {
          const payload = await bridgeInvoke('media-library.asset.enrich', {
            asset_ids: needsEnrich.map((item) => item.id),
            limit: needsEnrich.length,
            time_budget_ms: 8000,
            thumbnails: true,
            thumbnail_size: 'grid',
          });
          if (token !== state.hydrationToken) return;
          for (const summary of (payload.items || [])) {
            const target = state.results.find((item) => item.id === summary.id);
            if (target) Object.assign(target, summary);
            const local = state.localResults.find((item) => item.id === summary.id);
            if (local) Object.assign(local, summary);
            progress += 1;
          }
        }
        const thumbIDs = pendingItems.filter((item) => {
          const current = state.results.find((entry) => entry.id === item.id);
          return current && current.thumb_state === 'ok' && !state.thumbs[item.id];
        }).map((item) => item.id);
        for (let offset = 0; offset < thumbIDs.length; offset += 30) {
          const chunk = thumbIDs.slice(offset, offset + 30);
          const payload = await bridgeInvoke('media-library.asset.thumbnail', { asset_ids: chunk, size: 'grid' });
          if (token !== state.hydrationToken) return;
          for (const thumb of (payload.items || [])) {
            if (thumb.data_url) {
              applyThumbnail(thumb.id, thumb.data_url);
              progress += 1;
            }
          }
        }
        patchResultMetadata(pendingItems.map((item) => item.id));
        if (progress === 0) break;
      }
    } catch (_) {
      // 增强失败不打断检索；卡片保留类型图标，预览仍可用。
    } finally {
      state.hydrating = false;
    }
    void warmupMediaPipeline();
  }

  function toFileUrl(path) { const internals = window.__TAURI_INTERNALS__ || {}; if (typeof internals.convertFileSrc === 'function') { try { return internals.convertFileSrc(path); } catch (_) {} } const normalized = String(path || '').replace(/\\/g, '/'); return /^[a-zA-Z]:/.test(normalized) ? 'file:///' + normalized : 'file://' + normalized; }
  async function previewLocal(item) { return bridgeInvoke('media-library.asset.preview', { asset_id: item.id || '', path: item.path, kind: item.kind, max_bytes: 32 * 1024 * 1024 }); }

  // 第一次调用插件能力会冷启动进程（实测 1s 左右，之后 0.27s）。结果出来后预取一次
  // 元数据，把这次冷启动提前到用户点开之前；只拉 moov，不播放、不读完整个文件。
  async function warmupMediaPipeline() {
    if (state.mediaWarmed || state.previewOpen) return;
    const candidate = state.results.find((item) => item.origin !== 'online' && item.kind === 'video' && item.path);
    if (!candidate) return;
    state.mediaWarmed = true;
    try {
      const local = await previewLocal(candidate);
      if (!local || !local.stream_url) return;
      const video = document.createElement('video');
      video.muted = true;
      video.preload = 'metadata';
      video.playsInline = true;
      video.style.cssText = 'position:fixed;left:-9999px;top:0;width:2px;height:2px;opacity:0;pointer-events:none';
      let cleaned = false;
      const cleanup = () => {
        if (cleaned) return;
        cleaned = true;
        try {
          video.pause();
          video.removeAttribute('src');
          video.load();
        } catch (_) {}
        video.remove();
      };
      video.addEventListener('loadedmetadata', () => setTimeout(cleanup, 80), { once: true });
      video.addEventListener('error', cleanup, { once: true });
      setTimeout(cleanup, 4000);
      document.body.appendChild(video);
      video.src = local.stream_url;
    } catch (_) {
      // 预热失败不影响正常播放，下次搜索还会再试一次。
      state.mediaWarmed = false;
    }
  }

  function previewMediaMarkup(item, src) {
    const title = escapeHtml(item.title || item.name || '素材');
    if (item.kind === 'image') return '<img src="' + escapeHtml(src) + '" alt="' + title + '" />';
    if (item.kind === 'video') {
      const poster = item.preview_url || state.thumbs[item.id] || '';
      return '<video id="preview-player" tabindex="0" controls preload="metadata" poster="' + escapeHtml(poster) + '" src="' + escapeHtml(src) + '" aria-label="播放 ' + title + '"></video>';
    }
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

  function previewSource() {
    return state.previewList || state.results;
  }

  function updatePreviewNavigation() {
    const count = previewSource().length;
    el('preview-position').textContent = state.previewIndex >= 0 ? (state.previewIndex + 1) + ' / ' + count : '';
    el('previous-preview').disabled = state.previewIndex <= 0;
    el('next-preview').disabled = state.previewIndex < 0 || state.previewIndex >= count - 1;
  }

  function navigatePreview(delta) {
    const next = state.previewIndex + delta;
    if (next < 0 || next >= previewSource().length) return;
    void openPreview(next, state.previewList);
  }

  function stopPreviewMedia() {
    if (state.remotePreviewRequest) {
      void bridgeInvoke('media-library.remote.preview.cancel', { request_id: state.remotePreviewRequest }).catch(() => {});
      state.remotePreviewRequest = '';
    }
    el('preview-media').querySelectorAll('video,audio').forEach((media) => { media.pause(); media.removeAttribute('src'); media.load(); });
    el('preview-media').classList.remove('media-loading');
  }

  function previewError(item, text) {
    el('preview-media').classList.remove('media-loading');
    el('preview-tools').hidden = true;
    el('preview-media').innerHTML = '<div class="empty-state"><strong>预览暂时不可用</strong><span class="meta">' + escapeHtml(text) + '</span><button type="button" id="retry-preview" class="ghost">重试播放</button></div>';
    el('retry-preview').addEventListener('click', () => { void openPreview(state.previewIndex, state.previewList); });
  }

  function renderPreviewDetails(item, platformVideo) {
      const source = sourceLabel(item);
      const durationSeconds = item.duration_ms ? Number(item.duration_ms) / 1000 : Number(item.duration_seconds || item.duration || 0);
      const groups = [
        ['文件', [
          ['来源', item.entry_type === 'library' ? '' : escapeHtml(source)],
          ['所属库', item.entry_type === 'library' ? escapeHtml(item.library_kind === 'curated' ? '精选库' : '资源库') : ''],
          ['大小', escapeHtml(formatBytes(item.bytes))],
          ['状态', item.entry_type === 'library' ? '已入库' : item.missing ? '文件不可用' : item.origin === 'platform' ? '在线作品 · 尚未入库' : '尚未入库'],
        ]],
        [item.kind === 'audio' ? '音频' : '画面', [
          ['分辨率', item.width && item.height ? escapeHtml(Array.from(new Set([resolutionLabel(item.width, item.height), item.width + '×' + item.height])).join(' · ')) : ''],
          ['时长', durationSeconds > 0 ? escapeHtml(formatDuration(durationSeconds)) : ''],
          ['编码', escapeHtml([item.codec, item.fps ? Number(item.fps).toFixed(2) + 'fps' : '', item.hdr].filter(Boolean).join(' · '))],
          ['拍摄时间', item.captured_at ? escapeHtml(formatTime(item.captured_at)) : ''],
        ]],
        ['归类', [
          ['分类路径', escapeHtml(item.collection || '')],
          ['文件夹', escapeHtml(item.folder || '')],
          ['标签', Array.isArray(item.tags) && item.tags.length ? escapeHtml(item.tags.join(' · ')) : ''],
          ['作者', escapeHtml(item.author || '')],
        ]],
      ];
      const markup = groups.map(([title, rows]) => {
        const filled = rows.filter((row) => row[1]);
        if (!filled.length) return '';
        return '<dt class="group">' + escapeHtml(title) + '</dt>'
          + filled.map((row) => '<dt>' + escapeHtml(row[0]) + '</dt><dd>' + row[1] + '</dd>').join('');
      }).join('');
      el('preview-meta').innerHTML = markup;
  }

  async function openPreview(index, list) {
    const listSource = list || state.results;
    const item = listSource[index];
    if (!item) return;
    const switching = state.previewOpen;
    if (intake) intake.closeEmbedded();
    if (previewUI) previewUI.begin(item, index, listSource, switching);
    stopPreviewMedia();
    pauseCardAudio();
    state.previewList = list || null;
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
    el('preview-kind-label').textContent = kindLabel(item.kind);
    populateAdoptionForm(item);
    updatePreviewNavigation();
    el('preview-media').innerHTML = '<div class="empty-state"><span class="meta">正在加载预览…</span></div>';
    const platformVideo = item.origin === 'platform';
    el('remote-preview-actions').hidden = !platformVideo;
    el('reveal-asset').hidden = platformVideo || ['generation','generation-reference'].includes(item.origin);
    el('remote-preview-source').href = platformVideo ? item.source_url : '#';
    el('remote-preview-source').hidden = !platformVideo;
    if (!switching) el('close-preview').focus();
    renderPreviewDetails(item, platformVideo);
    if (!platformVideo) updateAdoptionControls(item);
    void loadUserState(item);
	if (intake) intake.preview(item);
    if (audio) void audio.preview(['generation','generation-reference'].includes(item.origin) ? null : item);
    setPreviewFeedback('');
    el('adopt-button').hidden = platformVideo || item.entry_type === 'library' || ['generation','generation-reference'].includes(item.origin);
    el('adopt-form').hidden = platformVideo || item.entry_type === 'library' || ['generation','generation-reference'].includes(item.origin);
    if (generationUI) generationUI.previewContext(item);
    el('redistribution-notice').hidden = true;
    try {
      let src;
      if (platformVideo) {
        const id = Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) => b.toString(16).padStart(2, '0')).join('');
        state.remotePreviewRequest = id;
        el('preview-media').innerHTML = '<div class="empty-state"><strong>正在加载视频…</strong><span class="meta">首次播放需要准备预览，关闭可停止加载。</span></div>';
        const payload = await bridgeInvoke('media-library.remote.preview', { url: item.source_url, request_id: id });
        if (requestId !== state.previewRequestId) return;
        state.remotePreviewRequest = '';
        src = payload.stream_url;
      } else if (['generation','generation-reference'].includes(item.origin)) {
        const generated = await bridgeInvoke(item.origin === 'generation' ? 'media-library.generation.preview' : 'media-library.generation.reference', { session_id: item.generation_session, index: item.generation_index });
        if (requestId !== state.previewRequestId) return;
        src = generated.stream_url;
      } else if (item.origin === 'online') {
        src = item.kind === 'audio' ? (item.download_url || item.preview_url) : item.kind === 'video' ? (item.download_url || item.preview_url) : (item.preview_url || item.download_url);
      } else {
        const local = await previewLocal(item);
        if (requestId !== state.previewRequestId) return;
        src = local.stream_url || local.data_url || local.file_url || toFileUrl(item.path);
      }
      if (requestId !== state.previewRequestId) return;
      el('preview-media').innerHTML = previewMediaMarkup(item, src);
      if (audio) audio.ready();
      if (previewUI) previewUI.ready(item);
      markPreviewMediaLoading();
      const media = el('preview-media').querySelector('video,audio');
      const image = el('preview-media').querySelector('img');
      if (image) image.addEventListener('error', () => { if (requestId === state.previewRequestId) previewError(item, '图片无法解码或文件已不可用，请重试或打开所在位置。'); }, { once: true });
      if (media) media.addEventListener('error', () => { if (requestId === state.previewRequestId) previewError(item, '当前格式无法播放或文件暂不可用，请重试或打开来源。'); }, { once: true });
      if (platformVideo && media) {
        media.addEventListener('error', () => { if (requestId === state.previewRequestId) previewError(item, '检查平台连接后重试，或打开原作品。'); }, { once: true });
        void media.play().catch(() => { if (requestId === state.previewRequestId && !media.error) setPreviewFeedback('视频已就绪，点击播放。'); });
      }
      if (!switching) el('close-preview').focus();
    } catch (error) {
      if (requestId !== state.previewRequestId) return;
      state.remotePreviewRequest = '';
      previewError(item, describe(error));
      if (!platformVideo) setPreviewFeedback('预览失败：' + describe(error), 'error');
    }
  }

  async function loadUserState(item) {
    const section = el('user-state');
    if (!section) return;
    if (!item || item.entry_type !== 'candidate' || !item.id) {
      section.hidden = true;
      return;
    }
    section.hidden = false;
    try {
      el('asset-ai-profile').value = localStorage.getItem('himind.media-library.ai-profile') || 'full';
    } catch (_) {}
    const aiNote = el('asset-ai-service-note');
    if (aiNote) aiNote.textContent = 'AI 服务由 Agent 决定';
    if (previewUI) previewUI.userLoaded(item, item);
    el('save-user-state').disabled = false;
    el('user-state-mode').textContent = '';
    el('user-state-feedback').textContent = '';
    el('user-note').hidden = true;
    el('user-note').textContent = '';
    try {
      const payload = await bridgeInvoke('media-library.asset.user-state', { asset_id: item.id });
      if (state.previewItem !== item) return;
      if (previewUI) previewUI.userLoaded(item, payload);
      el('user-state-mode').textContent = payload.mode === 'db' ? '标记保存在本机' : '标记会同步到来源 .himind-media';
      el('user-note').hidden = !payload.note;
      el('user-note').textContent = payload.note || '';
      item.tags = payload.tags || [];
      item.rating = payload.rating || 0;
    } catch (_) {
      if (state.previewItem === item) el('user-state-mode').textContent = '暂时无法读取标记；编辑内容将保留为草稿';
    }
  }

  async function saveUserState() {
    const item = state.previewItem;
    if (!item || item.entry_type !== 'candidate' || !item.id) return;
    const submitted = { tags: el('asset-tags').value, rating: el('asset-rating').value, note: el('asset-note').value };
    const button = el('save-user-state');
    button.disabled = true;
    el('user-state-feedback').textContent = '正在保存…';
    try {
      const tags = parseTagList(submitted.tags);
      const rating = Number(submitted.rating) || 0;
      const saved = await bridgeInvoke('media-library.asset.tags.set', { asset_id: item.id, tags });
      await bridgeInvoke('media-library.asset.rating.set', { asset_id: item.id, rating });
      await bridgeInvoke('media-library.asset.note.set', { asset_id: item.id, note: submitted.note });
      item.tags = saved.tags || tags;
      item.rating = rating;
      item.note = submitted.note;
      if (previewUI) previewUI.saved(item, submitted);
      const card = resultCardFor(item.id);
      if (card) patchResultMetadata([item.id]);
    } catch (error) {
      if (state.previewItem === item) el('user-state-feedback').textContent = '保存未完成：' + describe(error) + '；可重试，草稿仍保留。';
    } finally {
      if (state.previewItem === item) button.disabled = false;
    }
  }

  function parseAIResult(text) {
    let raw = String(text || '').trim();
    raw = raw.replace(/^```(?:json)?/i, '').replace(/```$/, '').trim();
    const start = raw.indexOf('{');
    const end = raw.lastIndexOf('}');
    if (start >= 0 && end > start) raw = raw.slice(start, end + 1);
    const parsed = JSON.parse(raw);
    const tags = Array.isArray(parsed.tags) ? parsed.tags.map((tag) => String(tag).trim()).filter(Boolean).slice(0, 20) : [];
    return { tags, description: String(parsed.description || '').trim(), score: Number(parsed.score) || 0 };
  }

  async function describeAsset(item, profile) {
    const assetID = item.asset_id || item.id;
    if (!assetID) throw new Error('素材缺少 ID');
    const promptVersion = promptVersionFor(profile);
    // 服务由 Agent 决定；缓存键只跟任务（模式 + 提示版本）走，服务切换不影响复用。
    const modelKey = 'agent';
    try {
      const cached = await bridgeInvoke('media-library.asset.ai-cache.get', {
        asset_id: assetID, profile, model: modelKey, prompt_version: promptVersion,
      });
      const cachedResult = cached && cached.result;
      const valid = cachedResult && (profile === 'ocr' ? typeof cachedResult.text === 'string' : Array.isArray(cachedResult.tags));
      if (cached && cached.hit && valid) {
        return { result: cachedResult, tokens: cached.tokens || 0, cached: true };
      }
    } catch (_) {}
    let image = state.thumbs[assetID] || '';
    if (!image) {
      const thumb = await bridgeInvoke('media-library.asset.thumbnail', { asset_ids: [assetID], size: 'grid' });
      image = ((thumb.items || [])[0] || {}).data_url || '';
    }
    if (!image) throw new Error('没有可用的缩略图，请先补全素材信息');
    const internals = window.__TAURI_INTERNALS__ || {};
    if (typeof internals.invoke !== 'function') throw new Error('当前不在 HiMind Agent 扩展视图窗口中');
    const payload = await internals.invoke('invoke_plugin_view_capability', {
      capabilityId: 'media.ai.describe',
      input: {
        image,
        profile,
        max_edge: profile === 'ocr' ? 1024 : (profile === 'fast' ? 512 : 768),
        max_output_tokens: profile === 'ocr' ? 500 : (profile === 'fast' ? 200 : 400),
      },
    });
    const result = profile === 'ocr'
      ? { text: String(payload.text || '').trim(), tags: [] }
      : parseAIResult(payload.text);
    const tokens = Number((payload.usage || {}).total_tokens || 0);
    try {
      await bridgeInvoke('media-library.asset.ai-cache.set', {
        asset_id: assetID, profile, model: modelKey, prompt_version: promptVersion,
        result, tokens,
      });
    } catch (_) {}
    return {
      result, tokens, cached: false,
      service: payload.service || '', model: payload.model || '', displayName: payload.display_name || '',
    };
  }

  async function applyAIResult(item, result, note) {
    const assetID = item.asset_id || item.id;
    if (!assetID) return;
    if (Array.isArray(result.tags) && result.tags.length) {
      const saved = await bridgeInvoke('media-library.asset.tags.set', { asset_id: assetID, tags: result.tags });
      item.tags = saved.tags || result.tags;
    }
    if (state.previewItem === item) {
      el('asset-tags').value = Array.isArray(item.tags) ? item.tags.join(', ') : '';
      el('user-state-feedback').textContent = note;
    }
  }

  async function aiTagAsset() {
    const item = state.previewItem;
    if (!item || item.entry_type !== 'candidate' || !item.id) return;
    const button = el('ai-tag-asset');
    button.disabled = true;
    el('user-state-feedback').textContent = '正在调用多模态模型…';
    try {
      const profile = el('asset-ai-profile').value || 'full';
      try {
        localStorage.setItem('himind.media-library.ai-profile', profile);
      } catch (_) {}
      const outcome = await withRetry(() => describeAsset(item, profile), 2);
      const result = outcome.result;
      const tokenText = outcome.cached ? ' · 缓存' : (outcome.tokens ? ' · ' + outcome.tokens + ' tokens' : '');
      if (outcome.service) {
        const note = el('asset-ai-service-note');
        if (note) {
          note.textContent = 'AI 服务：' + (outcome.displayName || outcome.service) + (outcome.model ? ' · ' + outcome.model : '');
          note.title = '由 Agent 统一配置（' + outcome.service + '）';
        }
      }
      if (profile === 'ocr') {
        const note = String(result.text || '').trim();
        const saved = await bridgeInvoke('media-library.asset.note.set', { asset_id: item.asset_id || item.id, note });
        item.note = saved.note || note;
        el('user-note').hidden = !item.note;
        el('user-note').textContent = item.note || '';
        el('user-state-feedback').textContent = (note ? '已保存 OCR 文本' : '画面中没有识别到文字') + tokenText;
        return;
      }
      const note = (result.description || 'AI 已返回结果')
        + (result.score ? ' · 质量 ' + result.score : '')
        + tokenText;
      await applyAIResult(item, result, note);
      const card = resultCardFor(item.id);
      if (card) patchResultMetadata([item.id]);
    } catch (error) {
      el('user-state-feedback').textContent = 'AI 打标失败：' + describe(error);
    } finally {
      button.disabled = false;
    }
  }

  async function withRetry(action, attempts) {
    let lastError = null;
    for (let attempt = 0; attempt < (attempts || 2); attempt += 1) {
      try {
        return await action();
      } catch (error) {
        lastError = error;
        if (attempt + 1 < (attempts || 2)) await new Promise((resolve) => setTimeout(resolve, 2000));
      }
    }
    throw lastError;
  }

  async function runPool(items, limit, worker) {
    let cursor = 0;
    const runners = Array.from({ length: Math.min(limit, items.length) }, async () => {
      while (cursor < items.length) {
        const index = cursor;
        cursor += 1;
        await worker(items[index], index);
      }
    });
    await Promise.all(runners);
  }

  function promptVersionFor(profile) {
    return profile === 'ocr' ? 'ocr-v1' : (profile === 'fast' ? 'tags-v3' : 'full-v2');
  }

  function invokeAgentCapability(capabilityId, input) {
    const internals = window.__TAURI_INTERNALS__ || {};
    if (typeof internals.invoke !== 'function') {
      return Promise.reject(new Error('当前不在 HiMind Agent 扩展视图窗口中'));
    }
    return internals.invoke('invoke_plugin_view_capability', { capabilityId, input: input || {} });
  }

  async function loadAIJobRef(selectionID) {
    try {
      const payload = await bridgeInvoke('media-library.ai.job.get', { selection_id: selectionID });
      return payload && payload.found ? payload : null;
    } catch (_) {
      return null;
    }
  }

  async function saveAIJobRef(selectionID, job) {
    if (!selectionID) return;
    try {
      if (job) {
        await bridgeInvoke('media-library.ai.job.set', {
          selection_id: selectionID, job_id: job.job_id,
          profile: job.profile || 'fast', service: job.service || '',
        });
      } else {
        await bridgeInvoke('media-library.ai.job.clear', { selection_id: selectionID });
      }
    } catch (_) {
      // 作业引用只是恢复用的线索，写不进去不影响本次运行。
    }
  }

  function groupSelectionItems(items) {
    const groups = new Map();
    for (const item of items) {
      const match = /相似组\s*(\d+)/.exec(item.reason || '');
      const key = match ? 'cluster-' + match[1] : 'asset-' + item.asset_id;
      if (!groups.has(key)) groups.set(key, []);
      groups.get(key).push(item);
    }
    return groups;
  }

  async function ensureThumbPaths(assetIDs, size) {
    const paths = {};
    const key = (id) => size + ':' + id;
    for (const id of assetIDs) {
      if (state.thumbPaths[key(id)]) paths[id] = state.thumbPaths[key(id)];
    }
    const missing = assetIDs.filter((id) => !paths[id]);
    for (let index = 0; index < missing.length; index += 40) {
      const chunk = missing.slice(index, index + 40);
      try {
        const payload = await bridgeInvoke('media-library.asset.thumbnail', { asset_ids: chunk, size });
        for (const item of payload.items || []) {
          if (item.data_url) state.thumbs[item.id] = item.data_url;
          if (item.path) {
            state.thumbPaths[key(item.id)] = item.path;
            paths[item.id] = item.path;
          }
        }
      } catch (_) {
        // 缺缩略图的素材会跳过，其他继续。
      }
    }
    return paths;
  }

  async function applyBatchResult(repID, result, profile, tokens, service, memberIDs, persistCache) {
    const ids = (memberIDs && memberIDs.length ? memberIDs : [repID]).filter(Boolean);
    if (!ids.length) return;
    if (profile === 'ocr') {
      const note = String(result.text || '').trim();
      if (!note) return;
      for (const assetID of ids) {
        await bridgeInvoke('media-library.asset.note.set', { asset_id: assetID, note });
      }
      return;
    }
    const tags = Array.isArray(result.tags) ? result.tags : [];
    for (const assetID of ids) {
      if (tags.length) {
        await bridgeInvoke('media-library.asset.tags.set', { asset_id: assetID, tags });
      }
      const item = state.selectionItems.find((entry) => entry.asset_id === assetID);
      if (item) item.tags = tags;
      if (state.previewItem && (state.previewItem.asset_id === assetID || state.previewItem.id === assetID)) {
        state.previewItem.tags = tags;
        if (tags.length) el('asset-tags').value = tags.join(', ');
      }
    }
    // 只有真正调用过模型的结果才写缓存；缓存命中和同组跟随的成员不再写，
    // 否则会把「调用次数 / token 花费」算重复。
    if (persistCache && tags.length && Number(tokens) > 0) {
      try {
        await bridgeInvoke('media-library.asset.ai-cache.set', {
          asset_id: ids[0], profile, model: service || 'active',
          prompt_version: promptVersionFor(profile), result, tokens: Number(tokens),
        });
      } catch (_) {}
    }
  }

  function stopAIPoll() {
    if (state.aiPoll) {
      clearInterval(state.aiPoll);
      state.aiPoll = null;
    }
  }

  function applyAIControls() {
    const button = el('selection-ai-cancel');
    if (!button) return;
    button.hidden = !state.aiJob;
    button.disabled = false;
  }

  function startAIPoll(job) {
    stopAIPoll();
    applyAIControls();
    const tick = () => { void pollAIJob(job).catch(() => {}); };
    tick();
    state.aiPoll = setInterval(tick, 2500);
  }

  async function pollAIJob(job) {
    if (!state.aiJob || state.aiJob.job_id !== job.job_id) return;
    const payload = await invokeAgentCapability('media.ai.tag.batch.status', { job_id: job.job_id });
    const mapping = state.aiJobMapping || {};
    let applied = 0;
    for (const item of payload.items || []) {
      if (item.status !== 'done' || state.aiApplied[item.id]) continue;
      state.aiApplied[item.id] = true;
      try {
        const result = job.profile === 'ocr' ? { text: item.text, tags: [] } : parseAIResult(item.text);
        await applyBatchResult(item.id, result, job.profile, item.tokens, job.service, mapping[item.id], true);
        applied += 1;
      } catch (error) {
        state.aiApplied[item.id] = false;
        el('selection-progress').textContent = '结果写入失败：' + describe(error);
      }
    }
    const total = Number(payload.total || 0);
    const aiNote = el('selection-ai-service-note');
    if (aiNote && payload.service) {
      aiNote.textContent = 'AI 服务：' + payload.service;
      aiNote.title = '由 Agent 统一配置';
    }
    el('selection-progress').textContent = 'AI 后台作业 ' + Number(payload.done || 0) + '/' + total
      + ' · 失败 ' + Number(payload.failed || 0)
      + (payload.used_tokens ? ' · ' + payload.used_tokens + ' tokens' : '')
      + (applied ? ' · 刚写入 ' + applied : '');
    if (payload.status === 'running') return;
    stopAIPoll();
    const selectionID = job.selection_id;
    await saveAIJobRef(selectionID, null);
    state.aiJob = null;
    state.aiJobMapping = null;
    applyAIControls();
    const statusLabel = {
      done: '完成', canceled: '已取消', budget_exceeded: '到达预算上限',
      interrupted: '中断（Agent 重启过）',
    }[payload.status] || payload.status;
    const hint = payload.status === 'interrupted' ? '；再点「AI 打标入选」会跳过已完成的组继续' : '';
    setSelectionFeedback('AI 打标' + statusLabel + '：处理 ' + Number(payload.done || 0) + '/' + total
      + ' 组，失败 ' + Number(payload.failed || 0)
      + '，共 ' + Number(payload.used_tokens || 0) + ' tokens' + hint + '。',
      payload.status === 'done' ? '' : 'warn');
    renderSelection();
  }

  async function reconnectAIJob(selectionID) {
    if (state.aiJob) return true;
    const ref = await loadAIJobRef(selectionID);
    if (!ref || !ref.job_id) return false;
    const groups = groupSelectionItems(state.selectionItems.filter((item) => item.state === 'included'));
    const members = {};
    for (const items of groups.values()) {
      members[items[0].asset_id] = items.map((item) => item.asset_id);
    }
    const job = {
      job_id: ref.job_id, selection_id: selectionID,
      profile: ref.profile || 'fast', service: ref.service || '', members,
    };
    const status = await invokeAgentCapability('media.ai.tag.batch.status', { job_id: job.job_id }).catch(() => null);
    if (!status) {
      await saveAIJobRef(selectionID, null);
      return false;
    }
    state.aiJob = job;
    state.aiJobMapping = members;
    state.aiApplied = {};
    setSelectionFeedback('接着上次的 AI 后台作业：已处理 ' + Number(status.done || 0)
      + '/' + Number(status.total || 0) + ' 组。');
    startAIPoll(job);
    return true;
  }

  async function cancelAIJob() {
    if (!state.aiJob) return;
    el('selection-ai-cancel').disabled = true;
    try {
      const payload = await invokeAgentCapability('media.ai.tag.batch.cancel', { job_id: state.aiJob.job_id });
      setSelectionFeedback('已请求取消 AI 作业（' + payload.status + '）；已写入的标签会保留。');
    } catch (error) {
      setSelectionFeedback('取消失败：' + describe(error), 'error');
    } finally {
      el('selection-ai-cancel').disabled = false;
    }
  }

  async function aiTagSelection() {
    if (!state.selection) return;
    if (state.aiJob) {
      setSelectionFeedback('AI 作业还在后台跑，等它结束或先取消。', 'warn');
      return;
    }
    const included = state.selectionItems.filter((item) => item.state === 'included');
    if (!included.length) {
      setSelectionFeedback('先入选至少一条素材。', 'warn');
      return;
    }
    const profile = el('selection-ai-profile').value || 'fast';
    try {
      localStorage.setItem('himind.media-library.ai-profile', profile);
    } catch (_) {}
    const groups = groupSelectionItems(included);
    const reps = Array.from(groups.values()).map((items) => items[0]);
    const members = {};
    for (const items of groups.values()) {
      members[items[0].asset_id] = items.map((item) => item.asset_id);
    }
    setSelectionBusy(true, 'AI 打标 检查缓存…');
    setSelectionFeedback('');
    let cached = 0;
    const pending = [];
    try {
      for (const item of reps) {
        let hit = null;
        try {
          const payload = await bridgeInvoke('media-library.asset.ai-cache.get', {
            asset_id: item.asset_id, profile, model: service || 'active', prompt_version: promptVersionFor(profile),
          });
          if (payload && payload.hit) hit = payload.result;
        } catch (_) {}
        if (!hit) {
          pending.push(item);
          continue;
        }
        cached += 1;
        await applyBatchResult(item.asset_id, hit, profile, 0, service, members[item.asset_id], false);
      }
      if (!pending.length) {
        setSelectionFeedback('AI 打标完成：' + cached + ' 组全部命中缓存，本次没有产生调用。');
        renderSelection();
        return;
      }
      const size = profile === 'ocr' ? 'preview' : 'grid';
      const paths = await ensureThumbPaths(pending.map((item) => item.asset_id), size);
      const items = pending
        .filter((item) => paths[item.asset_id])
        .map((item) => ({ id: item.asset_id, path: paths[item.asset_id] }));
      if (!items.length) {
        setSelectionFeedback('入选素材还没有缩略图，请先补全素材信息再打标。', 'warn');
        return;
      }
      const budget = await bridgeInvoke('media-library.ai.budget', { planned_calls: items.length });
      if (budget && budget.allowed === false) {
        setSelectionFeedback(String(budget.blocked_reason || 'AI 预算不足，已停止。'), 'error');
        return;
      }
      const plannedUSD = Number((budget && budget.planned && budget.planned.usd) || 0);
      if (plannedUSD > 0.5) {
        const ok = await showConfirm({
          title: '确认 AI 花费',
          message: '本次 AI 打标 ' + items.length + ' 组，预计 $' + plannedUSD.toFixed(2)
            + '（上限：单次 $2 / 每日 $5 / 每月 $20）。继续吗？',
          confirmText: '开始打标',
        });
        if (!ok) {
          setSelectionFeedback('已取消本次 AI 打标，没有产生调用。', 'warn');
          return;
        }
      }
      const started = await invokeAgentCapability('media.ai.tag.batch.start', {
        items,
        profile,
        concurrency: 2,
        attempts: 2,
        max_edge: profile === 'ocr' ? 1024 : 512,
        max_output_tokens: profile === 'ocr' ? 500 : (profile === 'fast' ? 200 : 400),
        // 打标不需要长推理：不限制时会单张烧掉上万 token。
        reasoning_effort: 'none',
        budget_tokens: Number((budget && budget.next_job_budget_tokens) || 0),
      });
      const job = {
        job_id: started.job_id,
        selection_id: state.selection.id,
        profile,
        service,
        members,
        created_at: Date.now(),
      };
      await saveAIJobRef(state.selection.id, job);
      state.aiJob = job;
      state.aiJobMapping = members;
      state.aiApplied = {};
      setSelectionFeedback('AI 作业已在 Agent 后台运行：' + items.length + ' 组，关掉这个窗口也会继续。'
        + (cached ? '（缓存跳过 ' + cached + ' 组）' : ''));
      startAIPoll(job);
    } catch (error) {
      setSelectionFeedback('AI 批量打标失败：' + describe(error), 'error');
    } finally {
      setSelectionBusy(false);
    }
  }
  async function revealPreviewAsset() {
    const item = state.previewItem;
    if (!item) return;
    const payload = item.entry_type === 'library' || !item.path
      ? { asset_id: item.id }
      : { path: item.path };
    try {
      await bridgeInvoke('media-library.asset.reveal', payload);
    } catch (error) {
      setPreviewFeedback('打开位置失败：' + describe(error), 'error');
    }
  }

  function markPreviewMediaLoading() {
    const host = el('preview-media');
    const video = host.querySelector('video');
    if (!video) return;
    host.classList.add('media-loading');
    const done = () => host.classList.remove('media-loading');
    for (const name of ['loadeddata', 'playing', 'error']) {
      video.addEventListener(name, done, { once: true });
    }
    // 大文件首次解码可能久一点，最多提示 12 秒，避免一直挂着。
    setTimeout(done, 12000);
  }

  function closePreview() {
    if (!state.previewOpen) return;
    if (document.fullscreenElement && el('preview').contains(document.fullscreenElement)) void document.exitFullscreen().catch(() => {});
    if (previewUI) previewUI.close();
    if (audio) void audio.preview(null);
    if (intake) intake.closeEmbedded();
    stopPreviewMedia();
    state.previewRequestId += 1;
    state.previewOpen = false;
    state.previewItem = null;
    state.previewIndex = -1;
    state.previewList = null;
    el('preview').hidden = true;
    el('preview').setAttribute('aria-hidden', 'true');
    el('preview-scrim').hidden = true;
    el('preview-media').querySelectorAll('audio, video').forEach((media) => media.pause());
    el('preview-media').innerHTML = '';
    el('user-state').hidden = true;
    el('user-state-feedback').textContent = '';
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
      setPreviewFeedback(message);
      el('adopt-button').hidden = true; el('adopt-form').hidden = true; el('preview-adopt-action').hidden = true;
      await loadLibraries();
    } catch (error) { setPreviewFeedback('添加到资源库失败：' + describe(error), 'error'); }
    finally { setBusy(false); }
  }

  function switchSettingsTab(tab) {
    document.querySelectorAll('[data-settings-tab]').forEach((button) => { const active = button.getAttribute('data-settings-tab') === tab; button.classList.toggle('active', active); button.setAttribute('aria-selected', String(active)); button.tabIndex = active ? 0 : -1; });
    document.querySelectorAll('[data-settings-panel]').forEach((panel) => { panel.hidden = panel.getAttribute('data-settings-panel') !== tab; });
	if (intake) intake.settings(tab);
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
	if (intake) intake.settings('');
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
    try { for (let round = 0; round < 2000; round += 1) { const payload = await bridgeInvoke('media-library.source.scan.batch', { source_id: sourceId, restart, max_entries: 200 }); restart = false; total += Number(payload.indexed_files || 0); state.scanProgress = '扫描中 · 已索引 ' + total + ' 个文件'; renderSources(); if (payload.done) break; } await loadSources(); await loadFacets(); setSettingsFeedback('扫描完成，本次索引 ' + total + ' 个文件。'); }
    finally { state.scanningSourceId = ''; state.scanProgress = ''; renderSources(); renderIndexOverview(); }
  }

  async function addAndScanSource() {
    const path = el('source-path').value.trim(); if (!path) { setFeedback('请选择或填写本地文件夹。', 'warn'); return; }
    setBusy(true, '正在添加文件夹…');
    try { const source = await bridgeInvoke('media-library.source.add', { path, metadata_mode: el('source-metadata-mode').value }); el('source-path').value = ''; await loadSources(); await scanSource(source.id); }
    catch (error) { setSettingsFeedback('添加文件夹失败：' + describe(error), 'error'); }
    finally { setBusy(false); }
  }

  async function scanAllSources() { if (!state.sources.length) { setSettingsFeedback('还没有可扫描的本机来源。', 'warn'); return; } setBusy(true, '正在扫描全部来源…'); try { for (const source of state.sources) await scanSource(source.id); setSettingsFeedback('全部来源扫描完成。'); } catch (error) { setSettingsFeedback('扫描失败：' + describe(error), 'error'); } finally { setBusy(false); } }
  async function refreshAll() { setBusy(true, '正在刷新…'); try { await loadProviders(); await loadLibraries(); await loadSources(); await loadFacets(); if (state.query) await runSearch(); else setFeedback(''); } catch (error) { setFeedback('刷新失败：' + describe(error), 'error'); } finally { setBusy(false); } }

  function trapFocus(event, container) {
    if (event.key !== 'Tab') return;
    const focusable = Array.from(container.querySelectorAll('button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex="0"]')).filter((node) => !node.hidden && node.offsetParent !== null);
    if (!focusable.length) return;
    const first = focusable[0]; const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }

  function selectionCandidates() {
    const seen = new Set();
    return state.results.filter((item) => {
      if (!item || item.entry_type !== 'candidate' || !item.id || seen.has(item.id)) return false;
      seen.add(item.id);
      return true;
    }).slice(0, 200);
  }

  function readSelectionPrefs() {
    try {
      return JSON.parse(localStorage.getItem('himind.media-library.selection.v1') || '{}');
    } catch (_) {
      return {};
    }
  }

  function saveSelectionPrefs(patch) {
    try {
      localStorage.setItem('himind.media-library.selection.v1', JSON.stringify(Object.assign(readSelectionPrefs(), patch)));
    } catch (_) {}
  }

  function setSelectionFeedback(message, tone) {
    const node = el('selection-feedback');
    if (!message) { node.hidden = true; node.textContent = ''; return; }
    node.hidden = false;
    node.className = 'inline-feedback' + (tone ? ' ' + tone : '');
    node.textContent = message;
  }

  const SELECTION_STATUS_LABEL = { draft: '草稿', reviewing: '进行中', done: '已加入' };

  function selectionStatusLabel(status) {
    return SELECTION_STATUS_LABEL[status] || '草稿';
  }

  function setSelectionBusy(busy, message) {
    state.selectionBusy = busy;
    const hasIncluded = state.selectionItems.some((item) => item.state === 'included');
    const unchanged = state.selection && state.selection.status === 'done' && !state.selectionDirty;
    if (el('selection-create')) el('selection-create').disabled = busy;
    el('selection-auto').disabled = busy || !state.selection;
    el('selection-promote').disabled = busy || !state.selection || !hasIncluded || unchanged;
    el('selection-promote').title = unchanged ? '没有改动，无需更新' : '';
    el('selection-ai-tag').disabled = busy || Boolean(state.aiJob) || !state.selection || !hasIncluded;
    if (message) el('selection-progress').textContent = message;
    applyAIControls();
  }

  function selectionGroupKey(item) {
    const match = /相似组\s*(\d+)/.exec(item.reason || '');
    return match ? 'cluster-' + match[1] : 'asset-' + item.asset_id;
  }

  function selectionStats(items) {
    const stats = { total: items.length, included: 0, reserve: 0, excluded: 0, candidate: 0 };
    for (const item of items) {
      if (stats[item.state] === undefined) stats.candidate += 1;
      else stats[item.state] += 1;
    }
    return stats;
  }

  function pipelineChips(stats) {
    const chips = [
      ['', 'layers', '全部', stats.total],
      ['included', 'check', '入选', stats.included],
      ['reserve', 'bookmark', '备选', stats.reserve],
      ['excluded', 'ban', '排除', stats.excluded],
    ];
    return chips.map(([value, iconName, label, count]) =>
      '<button type="button" class="pipeline-chip" data-pipeline="' + value + '" aria-pressed="' +
      String((state.pipelineFilter || '') === value) + '">' + icon(iconName) + '<strong>' + count + '</strong><span>' + label + '</span></button>').join('');
  }

  function renderSelectionList() {
    const host = el('selection-list');
    if (!host) return;
    const items = state.selectionList || [];
    if (!items.length) {
      host.innerHTML = '<div class="selection-list-empty">还没有精选。先在「素材」里搜索，再回来从当前结果新建。</div>';
      return;
    }
    host.innerHTML = items.map((item) => {
      const active = state.selection && state.selection.id === item.id;
      const secondary = item.status === 'done'
        ? '库内 ' + (item.library_items || 0) + ' 条'
        : '入选 ' + item.included + ' / 目标 ' + item.target_count;
      const root = item.library_root || '';
      return '<button type="button" class="selection-list-item' + (active ? ' active' : '') +
        '" data-selection-id="' + escapeHtml(item.id) + '" aria-pressed="' + (active ? 'true' : 'false') + '">' +
        '<strong title="' + escapeHtml(item.name) + '">' + escapeHtml(item.name) + '</strong>' +
        '<small>' + escapeHtml(selectionStatusLabel(item.status)) + ' · ' + escapeHtml(secondary) + '</small>' +
        (root ? '<small title="' + escapeHtml(root) + '">' + escapeHtml(root) + '</small>' : '') +
        '</button>';
    }).join('');
    const count = el('selection-count');
    if (count) count.textContent = items.length ? '· ' + items.length + ' 个' : '';
  }

  function renderSelectionItems(items) {
    const host = el('selection-items');
    const stats = selectionStats(items);
    if (!items.length) {
      host.innerHTML = '<div class="empty-state"><strong>没有候选素材</strong><span class="meta">回到「素材」换一个搜索结果，再新建精选。</span></div>';
      return [];
    }
    const order = { included: 0, reserve: 1, candidate: 2, excluded: 3 };
    let visible = items.filter((item) => {
      const filter = state.pipelineFilter;
      if (!filter || filter === 'promoted') return true;
      return item.state === filter;
    });
    const sort = state.selectionSort || 'rank';
    visible = visible.slice().sort((left, right) => {
      if (sort === 'quality') return (right.score || 0) - (left.score || 0);
      if (sort === 'state') {
        const diff = (order[left.state] ?? 9) - (order[right.state] ?? 9);
        if (diff !== 0) return diff;
        return (left.rank || 999) - (right.rank || 999);
      }
      return (left.rank || 999) - (right.rank || 999);
    });
    const card = (item) => {
      const title = item.title || item.asset_id;
      const thumb = state.thumbs[item.asset_id]
        ? '<img src="' + escapeHtml(state.thumbs[item.asset_id]) + '" alt="" loading="lazy" />'
        : '<span aria-hidden="true">' + (item.kind === 'video' ? '▶' : item.kind === 'audio' ? '♪' : '▧') + '</span>';
      const score = item.score ? '质量 ' + Math.round(item.score) : '';
      const reasonText = item.reason && item.reason.includes('质量')
        ? item.reason
        : [score, item.reason].filter(Boolean).join(' · ');
      const stateLabel = item.state === 'included' ? '入选' : item.state === 'excluded' ? '排除' : item.state === 'reserve' ? '备选' : '候选';
      const flag = item.state === 'included' ? '<span class="selection-item-flag">入选</span>' : '';
      return '<article class="selection-item" role="listitem" tabindex="0" data-asset-id="' + escapeHtml(item.asset_id) +
        '" data-group="' + escapeHtml(selectionGroupKey(item)) + '" data-state="' + escapeHtml(item.state) + '">' +
        '<span class="selection-item-thumb">' + thumb + flag + '</span>' +
        '<span class="selection-item-body"><strong title="' + escapeHtml(title) + '">' + escapeHtml(title) + '</strong>' +
        '<small title="' + escapeHtml(reasonText) + '">' + escapeHtml(reasonText || stateLabel) + '</small></span>' +
        '<span class="selection-item-actions">' +
        '<button type="button" data-decision="included" aria-pressed="' + (item.state === 'included') + '" title="快捷键 1">' + icon('check') + '入选</button>' +
        '<button type="button" data-decision="reserve" aria-pressed="' + (item.state === 'reserve') + '" title="快捷键 2">' + icon('bookmark') + '备选</button>' +
        '<button type="button" data-decision="excluded" aria-pressed="' + (item.state === 'excluded') + '" title="快捷键 3">' + icon('ban') + '排除</button>' +
        '</span></article>';
    };
    if (!state.groupSimilar) {
      host.innerHTML = visible.map(card).join('');
      return visible;
    }
    const groups = new Map();
    for (const item of visible) {
      const key = selectionGroupKey(item);
      if (!groups.has(key)) groups.set(key, []);
      groups.get(key).push(item);
    }
    const grouped = Array.from(groups.entries()).sort((left, right) => {
      const leftRank = Math.min(...left[1].map((item) => item.rank || 999));
      const rightRank = Math.min(...right[1].map((item) => item.rank || 999));
      return leftRank - rightRank;
    });
    host.innerHTML = grouped.map(([key, groupItems]) => {
      const label = key.startsWith('cluster-') ? '相似组 ' + key.slice(8) : '单独素材';
      const best = Math.max(...groupItems.map((item) => item.score || 0));
      const pickButton = groupItems.length > 1
        ? '<button type="button" class="ghost" data-pick-best="' + escapeHtml(key) + '">只留最佳</button>'
        : '';
      return '<section class="selection-group"><header class="selection-group-head"><strong>' + escapeHtml(label) +
        '</strong><span class="meta">' + groupItems.length + ' 条 · 最高质量 ' + Math.round(best) + '</span>' +
        pickButton + '</header>' +
        '<div class="selection-group-items">' + groupItems.map(card).join('') + '</div></section>';
    }).join('');
    return visible;
  }

  function syncSelectionSummary() {
    if (!state.selection) return;
    const entry = state.selectionList.find((item) => item.id === state.selection.id);
    if (!entry) return;
    entry.status = state.selection.status;
    entry.target_count = state.selection.target_count;
    if (typeof state.selection.total === 'number') entry.total = state.selection.total;
    else entry.total = state.selectionItems.length;
    if (typeof state.selection.included === 'number') entry.included = state.selection.included;
    else entry.included = state.selectionItems.filter((item) => item.state === 'included').length;
    if (state.selectionLibrary) entry.library_items = state.selectionLibrary.asset_count || entry.library_items;
    renderSelectionList();
  }

  function renderSelection() {
    const selection = state.selection;
    const items = state.selectionItems || [];
    const stats = selectionStats(items);
    el('selection-title').textContent = selection ? selection.name : '精选';
    const badge = el('selection-status');
    if (selection) {
      badge.hidden = false;
      badge.textContent = selectionStatusLabel(selection.status);
      badge.setAttribute('data-status', selection.status || 'draft');
    } else {
      badge.hidden = true;
    }
    if (!selection) {
      el('selection-subtitle').textContent = '当前结果里有 ' + selectionCandidates().length + ' 条本机素材，可据此新建。';
    } else {
      const entry = state.selectionList.find((item) => item.id === selection.id);
      const run = entry && entry.last_run && entry.last_run.created_at ? entry.last_run : null;
      const runText = run ? ' · 上次预选 ' + Number(run.clusters || 0) + ' 组' : '';
      if (selection.status === 'done') {
        const libraryCount = state.selectionLibrary ? Number(state.selectionLibrary.asset_count || 0) : 0;
        el('selection-subtitle').textContent = '已加入精选库 · 库内 ' + libraryCount + ' 条 · 候选 ' + stats.total + ' 条' + runText;
      } else {
        el('selection-subtitle').textContent = '候选 ' + stats.total + ' 条 · 入选 ' + stats.included + ' 条 · 目标 ' + selection.target_count + ' 条' + runText;
      }
    }
    const pipeline = el('selection-pipeline');
    pipeline.hidden = !selection;
    if (selection) pipeline.innerHTML = pipelineChips(stats);
    const groupToggle = el('selection-group-similar');
    if (groupToggle) groupToggle.setAttribute('aria-pressed', String(Boolean(state.groupSimilar)));
    el('selection-create-form').hidden = Boolean(selection) && !state.selectionCreateOpen;
    if (!selection) {
      el('selection-progress').textContent = '';
      el('selection-items').innerHTML = '';
      el('selection-destination-label').textContent = '从当前结果新建精选后，再选择目标精选库';
      el('selection-open-library').hidden = true;
      setSelectionBusy(false);
      return;
    }
    el('selection-progress').textContent = '';
    const destination = state.selectionLibrary;
    const libraryItems = destination && Number(destination.asset_count || 0);
    const tempLibrary = Boolean(destination && /[\\/](Temp|tmp)[\\/]/i.test(destination.root_path || ''));
    el('selection-destination-label').innerHTML = destination && destination.root_path
      ? icon('folder') + ' ' + escapeHtml(destination.name + (libraryItems ? ' · 库内 ' + libraryItems + ' 条' : '') + ' · ' + destination.root_path)
        + (tempLibrary ? ' ' + icon('alert') + ' ' + '<span title="临时目录，重开后可能被清理">临时目录</span>' : '')
      : icon('folder') + ' 还没有目标精选库';
    const hasLibrary = Boolean(destination && destination.root_path);
    el('selection-open-library').hidden = !hasLibrary;
    const visible = renderSelectionItems(items);
    el('selection-promote').textContent = selection.status === 'done' ? '更新精选库' : '加入精选库';
    syncSelectionSummary();
    setSelectionBusy(state.selectionBusy);
    void hydrateSelectionThumbnails(visible);
  }

  async function hydrateSelectionThumbnails(items) {
    const ids = items.map((item) => item.asset_id).filter((id) => id && !state.thumbs[id]).slice(0, 30);
    if (!ids.length) return;
    try {
      const payload = await bridgeInvoke('media-library.asset.thumbnail', { asset_ids: ids, size: 'grid' });
      for (const thumb of (payload.items || [])) {
        if (!thumb.data_url) continue;
        state.thumbs[thumb.id] = thumb.data_url;
        const card = document.querySelector('.selection-item[data-asset-id="' + String(thumb.id).replace(/"/g, '') + '"] .selection-item-thumb');
        if (!card) continue;
        const image = document.createElement('img');
        image.src = thumb.data_url;
        image.alt = '';
        image.loading = 'lazy';
        card.innerHTML = '';
        card.appendChild(image);
      }
    } catch (_) {
      // 缩略图是增强项，失败时保留类型图标。
    }
  }

  async function loadSelectionList() {
    try {
      const payload = await bridgeInvoke('media-library.selection.summary', {});
      state.selectionList = payload.items || [];
    } catch (_) {
      state.selectionList = [];
    }
    renderSelectionList();
  }

  function renderSelectionLibraries() {
    const select = el('selection-library');
    if (!select) return;
    const curated = (state.libraries || []).filter((item) => (item.kind || 'material') === 'curated');
    const current = (state.selectionLibrary && state.selectionLibrary.id) || select.value;
    select.innerHTML = curated.map((item) =>
      '<option value="' + escapeHtml(item.id) + '">' + escapeHtml(item.name) + ' · ' + (item.asset_count || 0) + ' 条</option>').join('')
      + '<option value="__new">新建精选库…</option>';
    if (current && curated.some((item) => item.id === current)) select.value = current;
  }

  async function openExistingSelection(id) {
    if (!id) {
      state.selection = null;
      state.selectionItems = [];
      state.selectionLibrary = null;
      renderSelection();
      return;
    }
    setSelectionBusy(true, '正在读取精选…');
    setSelectionFeedback('');
    try {
      const payload = await bridgeInvoke('media-library.selection.detail', { selection_id: id });
      state.selection = payload.selection;
      state.selectionItems = payload.items || [];
      state.selectionLibrary = payload.library && payload.library.id ? payload.library : null;
      state.selectionCreateOpen = false;
      el('selection-name').value = payload.selection.name;
      el('selection-target').value = payload.selection.target_count;
      el('selection-mode').value = payload.selection.promote_mode;
      state.pipelineFilter = '';
      state.selectionDirty = false;
      renderSelectionLibraries();
      renderSelectionList();
      renderSelection();
      const resumed = await reconnectAIJob(id);
      if (!resumed) setSelectionFeedback('');
    } catch (error) {
      setSelectionFeedback('读取精选失败：' + describe(error), 'error');
    } finally {
      setSelectionBusy(false);
    }
  }

  function prepareSelectionDraft() {
    const candidates = selectionCandidates();
    const prefs = readSelectionPrefs();
    const folder = candidates.length ? String(candidates[0].folder || '').split('/').filter(Boolean).pop() : '';
    el('selection-name').value = prefs.name || folder || '精选';
    el('selection-target').value = Math.min(10, Math.max(1, candidates.length));
    el('selection-mode').value = prefs.mode || 'hardlink';
    el('selection-library-root').value = prefs.libraryRoot || 'E:\\精选';
    try {
      const savedProfile = localStorage.getItem('himind.media-library.ai-profile') || 'fast';
      el('selection-ai-profile').value = ['fast', 'full', 'ocr'].includes(savedProfile) ? savedProfile : 'fast';
    } catch (_) {}
    state.selectionCreateOpen = true;
  }

  function showSelectionCreateForm() {
    prepareSelectionDraft();
    renderSelection();
    el('selection-name').focus();
  }

  async function enterSelectionView() {
    await loadLibraries();
    renderSelectionLibraries();
    await loadSelectionList();
    if (!state.selection) prepareSelectionDraft();
    renderSelection();
  }

  function switchView(view, preferredLibraryID) {
    const views = ['assets', 'discovery', 'bookmarks', 'selection', 'library', 'generation'];
    state.view = views.includes(view) ? view : 'assets';
    for (const name of views) {
      const active = state.view === name;
      el('view-' + name).hidden = !active;
      const tab = el('view-tab-' + name);
      if (!tab) continue;
      tab.classList.toggle('active', active);
      tab.setAttribute('aria-selected', String(active));
      tab.tabIndex = active ? 0 : -1;
    }
    if (state.view === 'selection') void enterSelectionView();
    if (state.view === 'library') void enterLibraryView(preferredLibraryID);
	if (intake) intake.enterView(state.view);
    if (generationUI) generationUI.enter(state.view);
    el('open-generation').setAttribute('aria-pressed', String(state.view === 'generation'));
  }

  async function createSelection() {
    const candidates = selectionCandidates();
    if (!candidates.length) {
      setSelectionFeedback('当前结果里没有本机素材；先去「素材」搜索。', 'warn');
      return;
    }
    const libraryID = el('selection-library').value;
    if (!libraryID || libraryID === '__new') {
      setSelectionFeedback('先选择或新建一个精选库。', 'warn');
      return;
    }
    const name = el('selection-name').value.trim() || '精选';
    const targetCount = Math.max(1, Number(el('selection-target').value) || 10);
    const mode = el('selection-mode').value;
    setSelectionBusy(true, '正在创建精选…');
    setSelectionFeedback('');
    try {
      const payload = await bridgeInvoke('media-library.selection.create', {
        name,
        asset_ids: candidates.map((item) => item.id),
        target_count: targetCount,
        library_id: libraryID,
        promote_mode: mode,
      });
      state.selection = payload.selection;
      state.selectionItems = payload.items || [];
      state.selectionLibrary = payload.library && payload.library.id ? payload.library : null;
      state.selectionCreateOpen = false;
      state.pipelineFilter = '';
      state.selectionDirty = false;
      saveSelectionPrefs({ name, mode });
      await loadSelectionList();
      renderSelectionLibraries();
      renderSelection();
      setSelectionFeedback('精选已创建：候选 ' + state.selectionItems.length + ' 条，可自动预选或逐条调整。');
      el('selection-auto').focus();
    } catch (error) {
      setSelectionFeedback('创建精选失败：' + describe(error), 'error');
    } finally {
      setSelectionBusy(false);
    }
  }

  async function createSelectionLibrary() {
    const name = (el('selection-name').value.trim() || '精选') + ' 精选库';
    const root = el('selection-library-root').value.trim();
    if (!root) {
      setSelectionFeedback('先填写精选库目录。', 'warn');
      el('selection-library-root').focus();
      return;
    }
    setSelectionBusy(true, '正在创建精选库…');
    try {
      const payload = await bridgeInvoke('media-library.repository.create', {
        name, backend_type: 'local', root_path: root, access_mode: 'contribute', kind: 'curated',
      });
      await loadLibraries();
      renderSelectionLibraries();
      if (payload && payload.id) el('selection-library').value = payload.id;
      el('selection-library-new-row').hidden = true;
      saveSelectionPrefs({ libraryRoot: root });
      setSelectionFeedback('精选库已创建：' + name);
    } catch (error) {
      setSelectionFeedback('创建精选库失败：' + describe(error), 'error');
    } finally {
      setSelectionBusy(false);
    }
  }

  async function promoteSelection() {
    if (!state.selection) return;
    const mode = el('selection-mode').value;
    setSelectionBusy(true, '正在加入精选库…');
    setSelectionFeedback('');
    el('selection-result').hidden = true;
    try {
      const payload = await bridgeInvoke('media-library.selection.promote', { selection_id: state.selection.id, mode });
      const detail = await bridgeInvoke('media-library.selection.detail', { selection_id: state.selection.id });
      state.selection = detail.selection;
      state.selectionItems = detail.items || [];
      if (detail.library && detail.library.id) state.selectionLibrary = detail.library;
      state.selectionDirty = false;
      renderPromoteResult(payload);
      await loadLibraries();
      await loadSelectionList();
      renderSelectionLibraries();
      renderSelection();
    } catch (error) {
      setSelectionFeedback('加入精选库失败：' + describe(error), 'error');
    } finally {
      setSelectionBusy(false);
    }
  }

  function renderPromoteResult(payload) {
    const node = el('selection-result');
    const saved = (payload.saved || []).length;
    const skipped = (payload.skipped || []).length;
    const failed = (payload.failed || []).length;
    const summary = '新增 ' + saved + ' 条 · 已存在 ' + skipped + ' 条 · 失败 ' + failed
      + ' 条 · 库内共 ' + Number(payload.library_items || 0) + ' 条';
    node.hidden = false;
    node.innerHTML = '<strong>' + escapeHtml(payload.library_name || '精选库') + '</strong>'
      + '<span class="meta">' + escapeHtml(summary) + '</span>'
      + '<button type="button" class="ghost" id="selection-result-open">打开精选库</button>'
      + (failed ? '<span class="meta">失败原因：' + escapeHtml((payload.failed || []).map((item) => item.error).join('；')) + '</span>' : '');
    const openButton = el('selection-result-open');
    if (openButton) openButton.addEventListener('click', () => { void revealSelectionLibrary(); });
  }

  async function revealSelectionLibrary() {    const path = state.selectionLibrary && state.selectionLibrary.root_path;
    if (!path) {
      setSelectionFeedback('还没有可打开的精选库。', 'warn');
      return;
    }
    try {
      await bridgeInvoke('media-library.asset.reveal', { path });
    } catch (error) {
      setSelectionFeedback('打开精选库失败：' + describe(error), 'error');
    }
  }

  function libraryKindLabel(library) {
    return library && library.kind === 'curated' ? '精选库' : '素材库';
  }

  function selectedLibrary() {
    return state.libraryView && state.libraryView.library ? state.libraryView.library : null;
  }

  function setLibraryFeedback(message, tone, undoIDs) {
    const node = el('library-feedback');
    if (!node) return;
    node.className = 'meta';
    node.textContent = '';
    if (!message) return;
    node.textContent = message;
    if (tone) node.classList.add(tone);
    if (undoIDs && undoIDs.length) {
      const undo = document.createElement('button');
      undo.type = 'button';
      undo.className = 'ghost';
      undo.textContent = '撤销';
      undo.addEventListener('click', () => { void restoreLibraryItemsByIDs(undoIDs); }, { once: true });
      node.appendChild(document.createTextNode(' '));
      node.appendChild(undo);
    }
  }

  async function enterLibraryView(preferredLibraryID) {
    await loadLibraries();
    await loadSelectionList();
    renderLibraryList();
    if (preferredLibraryID && state.libraries.some(item => item.id === preferredLibraryID)) {
      await openLibraryView(preferredLibraryID);
      return;
    }
    if (!state.libraryView) {
      const ranked = (state.libraries || []).slice().sort((left, right) => (right.asset_count || 0) - (left.asset_count || 0));
      const first = ranked[0];
      if (first) await openLibraryView(first.id);
      else renderLibraryEmpty();
      return;
    }
    const current = state.libraries.find((item) => item.id === state.libraryView.id);
    if (!current) {
      state.libraryView = null;
      renderLibraryList();
      renderLibraryEmpty();
      return;
    }
    state.libraryView.library = Object.assign({}, state.libraryView.library, current);
    await loadLibraryPage(true);
  }

  function renderLibraryEmpty() {
    el('library-view-name').textContent = '库';
    el('library-kind').hidden = true;
    el('library-meta').textContent = '还没有资源库。到设置里新建，或从精选加入素材时创建精选库。';
    el('library-content').innerHTML = '<div class="empty-state"><strong>没有可管理的库</strong><span class="meta">精选 →「加入精选库」会创建精选库。</span></div>';
    el('library-content-count').textContent = '';
    el('library-rescan').disabled = true;
    el('library-sync').disabled = true;
    el('library-trash-toggle').disabled = true;
    el('library-remove').disabled = true;
    el('library-select-all').disabled = true;
  }

  function renderLibraryList() {
    const host = el('library-list');
    if (!host) return;
    const libraries = state.libraries || [];
    const count = el('library-list-count');
    if (count) count.textContent = libraries.length ? '· ' + libraries.length + ' 个' : '';
    if (!libraries.length) {
      host.innerHTML = '<div class="selection-list-empty">还没有库。到设置里新建，或从精选加入素材。</div>';
      return;
    }
    host.innerHTML = libraries.map((library) => {
      const active = state.libraryView && state.libraryView.id === library.id;
      const kind = libraryKindLabel(library);
      const status = library.status === 'archived' ? '已归档' : (library.state === 'online' ? '可访问' : '离线');
      return '<button type="button" class="selection-list-item' + (active ? ' active' : '') +
        '" data-library-id="' + escapeHtml(library.id) + '" aria-pressed="' + (active ? 'true' : 'false') + '">' +
        '<strong title="' + escapeHtml(library.name) + '">' + escapeHtml(library.name) + '</strong>' +
        '<small>' + escapeHtml(kind) + ' · ' + library.asset_count + ' 项 · ' + escapeHtml(status) + '</small>' +
        '<small title="' + escapeHtml(library.root_uri || '') + '">' + escapeHtml(library.root_uri || '') + '</small>' +
        '</button>';
    }).join('');
  }

  function renderLibraryHeader() {
    const view = state.libraryView;
    if (!view || !view.library) return;
    const library = view.library;
    const kind = libraryKindLabel(library);
    const status = library.state === 'offline'
      ? '目录不可访问'
      : (view.trash ? '回收站 · ' + Number(view.total || 0) + ' 项' : '共 ' + Number(view.total || 0) + ' 项');
    el('library-meta').textContent = kind + ' · ' + status + ' · ' + (library.root_path || '');
    el('library-meta').title = library.root_path || '';
  }

  function organizeWait(ms) {
    return new Promise((resolve) => setTimeout(resolve, ms));
  }

  function setOrganizeStatus(text, tone) {
    const node = el('organize-status');
    if (!node) return;
    node.className = 'meta';
    node.textContent = text || '';
    if (tone) node.classList.add(tone);
  }

  function organizePanelVisible(visible) {
    const panel = el('organize-panel');
    if (!panel) return;
    panel.hidden = !visible;
  }

  function renderOrganizeSummary(counts, extra) {
    const view = state.libraryView;
    if (!view) return;
    const body = el('organize-body');
    const c = counts || {};
    const rows = [
      ['自动入库', Number(c.ingested || c.auto || 0)],
      ['待复核', Number(c.review || 0)],
      ['跳过', Number(c.skip || 0)],
      ['清理', Number(c.removed || 0)],
    ];
    body.innerHTML = '<div class="organize-counts">' + rows.map(([label, value]) =>
      '<span class="organize-count"><strong>' + value + '</strong>' + label + '</span>').join('') + '</div>' +
      (extra ? '<p class="meta">' + escapeHtml(extra) + '</p>' : '');
  }

  async function organizeCurrentLibrary() {
    const view = state.libraryView;
    if (!view || view.organize.busy) return;
    view.organize.busy = true;
    el('library-organize').disabled = true;
    organizePanelVisible(true);
    el('organize-title').textContent = '自动整理 · ' + view.library.name;
    el('organize-review').hidden = true;
    el('organize-back').hidden = true;
    el('organize-accept-all').hidden = true;
    el('organize-reject-all').hidden = true;
    setOrganizeStatus('正在分析素材（画质 / 相似组 / 已有标签）…');
    try {
      const plan = await bridgeInvoke('media-library.organize.plan', {
        library_id: view.id,
        policy: { auto_ingest: true, cleanup: true, per_cluster: 1, max_items: 60, max_review: 100, mode: 'hardlink' },
      });
      const counts = plan.counts || {};
      if (!plan.run_id) {
        setOrganizeStatus(String(plan.message || '没有需要整理的素材'));
        renderOrganizeSummary(counts);
        return;
      }
      const autoCount = Number(counts.auto || 0);
      const reviewCount = Number(counts.review || 0);
      const skipCount = Number(counts.skip || 0);
      if (!autoCount && !reviewCount) {
        setOrganizeStatus('这次没有值得入库的素材（跳过 ' + skipCount + ' 条）');
        renderOrganizeSummary(counts);
        return;
      }
      const confirmed = await showConfirm({
        title: '自动整理到「' + view.library.name + '」',
        message: '自动入库 ' + autoCount + ' 条（高画质 + 相似组只留最佳），待复核 ' + reviewCount
          + ' 条，跳过 ' + skipCount + ' 条。文件只落到库目录（硬链接，跨盘自动复制），源文件不动；自动入库的条目可随时移出。',
        confirmText: '开始整理',
      });
      if (!confirmed) {
        setOrganizeStatus('已取消，没有改动库内容');
        return;
      }
      setOrganizeStatus('正在入库 ' + autoCount + ' 条…');
      const applied = await bridgeInvoke('media-library.organize.apply', { run_id: plan.run_id });
      view.organize.runID = plan.run_id;
      await loadLibraryPage(true);
      await loadLibraries();
      renderLibraryList();
      const ingested = Number(applied.ingested || 0);
      const removed = Number(applied.removed || 0);
      const failed = Array.isArray(applied.failed) ? applied.failed.length : 0;
      renderOrganizeSummary(Object.assign({}, counts, { ingested: ingested, removed: removed }));
      setOrganizeStatus('整理完成：入库 ' + ingested + ' 条' + (removed ? ' · 清理 ' + removed + ' 条' : '')
        + (failed ? ' · 失败 ' + failed + ' 条' : '') + '（文件保留，可撤销）', failed ? 'warn' : '');
      if (reviewCount > 0) {
        el('organize-review').hidden = false;
        el('organize-review').textContent = '待复核 ' + reviewCount + ' 条';
      }
      const aiIDs = (plan.items || []).filter((item) => item.bucket === 'auto').map((item) => item.asset_id);
      if (aiIDs.length) void runOrganizeAI(view, aiIDs);
    } catch (error) {
      setOrganizeStatus('自动整理失败：' + describe(error), 'error');
    } finally {
      view.organize.busy = false;
      el('library-organize').disabled = false;
    }
  }

  async function runOrganizeAI(view, assetIDs) {
    if (!assetIDs.length) return;
    // 服务由 Agent 按「vision.describe」能力绑定决定，插件不指定服务。
    // 每次整理只自动打标一小批，避免一次点击就长时间占着后台与预算；剩下的下次整理继续。
    const cap = 24;
    const pendingIDs = assetIDs.slice(0, cap);
    const restCount = assetIDs.length - pendingIDs.length;
    try {
      const budget = await bridgeInvoke('media-library.ai.budget', { planned_calls: pendingIDs.length });
      if (budget && budget.allowed === false) {
        setOrganizeStatus('AI 打标已跳过：' + String(budget.blocked_reason || '预算不足'));
        return;
      }
      const paths = await ensureThumbPaths(pendingIDs, 'grid');
      const items = pendingIDs.filter((id) => paths[id]).map((id) => ({ id: id, path: paths[id] }));
      if (!items.length) return;
      setOrganizeStatus('AI 打标准备中：' + items.length + ' 条（服务由 Agent 决定）');
      const started = await invokeAgentCapability('media.ai.tag.batch.start', {
        items: items, profile: 'fast', concurrency: 2, attempts: 2,
        max_edge: 512, max_output_tokens: 200, reasoning_effort: 'none',
        budget_tokens: Number((budget && budget.next_job_budget_tokens) || 0),
      });
      const applied = {};
      for (let round = 0; round < 240; round += 1) {
        await organizeWait(2500);
        const status = await invokeAgentCapability('media.ai.tag.batch.status', { job_id: started.job_id }).catch(() => null);
        if (!status) {
          setOrganizeStatus('AI 打标状态读取失败，已停止跟随', 'warn');
          return;
        }
        for (const item of status.items || []) {
          if (item.status !== 'done' || applied[item.id]) continue;
          applied[item.id] = true;
          try {
            await applyBatchResult(item.id, parseAIResult(item.text), 'fast', item.tokens, '', [item.id], true);
          } catch (_) {
            applied[item.id] = false;
          }
        }
        const done = Number(status.done || 0);
        setOrganizeStatus('AI 自动打标 ' + done + '/' + Number(status.total || 0) + ' · 后台运行');
        if (status.status !== 'running') {
          const finished = status.status === 'done';
          const failedCount = Number(status.failed || 0);
          let hint = '';
          if (failedCount) {
            const firstFailure = (status.items || []).find((item) => item.status === 'failed' && item.error) || {};
            if (/402|Insufficient Balance|余额/i.test(String(firstFailure.error || ''))) {
              hint = '；AI 服务余额不足，充值后重跑即可补齐标签';
            } else {
              hint = '；失败 ' + failedCount + ' 组，可重跑补齐';
            }
          }
          setOrganizeStatus('AI 自动打标' + (finished ? '完成' : '结束（' + status.status + '）')
            + '：' + done + ' 组，共 ' + Number(status.used_tokens || 0) + ' tokens'
            + (restCount ? '；其余 ' + restCount + ' 条下次整理继续打标' : '') + hint + '。', finished && !failedCount ? '' : 'warn');
          if (view.id === state.libraryView.id && !view.review) await loadLibraryPage(true);
          return;
        }
      }
    } catch (error) {
      setOrganizeStatus('AI 打标失败：' + describe(error), 'warn');
    }
  }

  async function openOrganizeReview() {
    const view = state.libraryView;
    if (!view || !view.organize.runID) return;
    try {
      const payload = await bridgeInvoke('media-library.organize.review.list', {
        run_id: view.organize.runID, limit: 200,
      });
      view.review = { runID: view.organize.runID, items: payload.items || [] };
      el('organize-review').hidden = true;
      el('organize-back').hidden = false;
      el('organize-accept-all').hidden = view.review.items.length === 0;
      el('organize-reject-all').hidden = view.review.items.length === 0;
      el('organize-title').textContent = '待复核 · ' + view.review.items.length + ' 条';
      setOrganizeStatus('逐条看一眼即可：通过会入库，排除只跳过这一次。');
      renderLibraryContent();
      void hydrateReviewThumbs();
    } catch (error) {
      setOrganizeStatus('读取复核队列失败：' + describe(error), 'error');
    }
  }

  function closeOrganizeReview() {
    const view = state.libraryView;
    if (!view) return;
    view.review = null;
    el('organize-review').hidden = false;
    el('organize-back').hidden = true;
    el('organize-accept-all').hidden = true;
    el('organize-reject-all').hidden = true;
    el('organize-title').textContent = '自动整理 · ' + view.library.name;
    setOrganizeStatus('');
    renderLibraryContent();
  }

  async function hydrateReviewThumbs() {
    const view = state.libraryView;
    if (!view || !view.review) return;
    const items = view.review.items.filter((item) => item.candidate_id && !state.thumbs['review:' + item.asset_id]);
    if (!items.length) return;
    try {
      const payload = await bridgeInvoke('media-library.asset.thumbnail', {
        asset_ids: items.slice(0, 40).map((item) => item.candidate_id), size: 'grid',
      });
      for (const thumb of (payload.items || [])) {
        if (!thumb.data_url) continue;
        state.thumbs['review:' + thumb.id] = thumb.data_url;
        document.querySelectorAll('[data-review-thumb="' + thumb.id + '"]').forEach((node) => {
          const image = document.createElement('img');
          image.src = thumb.data_url;
          image.alt = '';
          image.loading = 'lazy';
          node.innerHTML = '';
          node.appendChild(image);
        });
      }
    } catch (_) {
      // 缩略图是增强项。
    }
  }

  async function decideOrganizeItems(assetIDs, accept) {
    const view = state.libraryView;
    if (!view || !view.review || !assetIDs.length) return;
    try {
      const payload = await bridgeInvoke('media-library.organize.review.decide', {
        run_id: view.review.runID,
        items: assetIDs.map((id) => ({ asset_id: id, accept: accept })),
      });
      const decided = new Set(assetIDs);
      view.review.items = view.review.items.filter((item) => !decided.has(item.asset_id));
      if (accept) {
        await loadLibraryPage(true);
        await loadLibraries();
        renderLibraryList();
      } else {
        renderLibraryContent();
      }
      setOrganizeStatus((accept ? '已通过 ' : '已排除 ') + assetIDs.length + ' 条'
        + (payload.failed && payload.failed.length ? '，失败 ' + payload.failed.length : ''));
      el('organize-title').textContent = '待复核 · ' + view.review.items.length + ' 条';
      el('organize-accept-all').hidden = view.review.items.length === 0;
      el('organize-reject-all').hidden = view.review.items.length === 0;
      if (!view.review.items.length) {
        setOrganizeStatus('复核完成，队列已清空。');
      }
    } catch (error) {
      setOrganizeStatus('处理复核失败：' + describe(error), 'error');
    }
  }

  async function openLibraryView(libraryID) {
    const library = (state.libraries || []).find((item) => item.id === libraryID);
    if (!library) return;
    state.libraryView = {
      id: libraryID, library, items: [], total: 0, selected: new Set(),
      trash: false, grid: false, report: null, addCount: 0, removePlan: null,
      review: null,
      organize: { runID: '', busy: false },
    };
    el('library-kind').hidden = false;
    el('library-kind').textContent = libraryKindLabel(library);
    el('library-kind').setAttribute('data-status', library.kind === 'curated' ? 'done' : 'draft');
    el('library-view-name').textContent = library.name;
    el('library-trash-toggle').setAttribute('aria-pressed', 'false');
    el('library-rescan').disabled = false;
    el('library-trash-toggle').disabled = false;
    el('library-sync').disabled = library.kind !== 'curated';
    el('library-select-all').disabled = false;
    el('library-organize').disabled = library.access_mode === 'read_only';
    organizePanelVisible(false);
    setLibraryFeedback('');
    el('library-report').hidden = true;
    renderLibraryList();
    await loadLibraryPage(true);
  }

  async function reloadLibraryView() {
    if (!state.libraryView) return;
    await loadLibraryPage(true);
  }

  async function loadLibraryPage(reset) {
    const view = state.libraryView;
    if (!view) return;
    if (reset) {
      view.items = [];
      view.total = 0;
      view.selected = new Set();
      setLibraryFeedback('');
    }
    try {
      if (view.trash) {
        const payload = await bridgeInvoke('media-library.holdings.trash.list', { library_id: view.id, limit: 200 });
        view.items = payload.items || [];
        view.total = Number(payload.total || view.items.length);
      } else {
        const payload = await bridgeInvoke('media-library.holdings.items', {
          library_id: view.id, limit: 60, offset: view.items.length,
        });
        view.library = payload.library || view.library;
        view.total = Number(payload.total || 0);
        view.items = view.items.concat(payload.items || []);
      }
      renderLibraryContent();
      if (!view.trash) void hydrateLibraryContentThumbs();
    } catch (error) {
      el('library-content').innerHTML = '<div class="empty-state"><strong>读取失败</strong><span class="meta">' + escapeHtml(describe(error)) + '</span></div>';
    }
  }

  async function hydrateLibraryContentThumbs() {
    const view = state.libraryView;
    if (!view) return;
    const pending = view.items.filter((item) => item.candidate_id && !state.thumbs[item.id]).slice(0, 40);
    if (!pending.length) return;
    try {
      const payload = await bridgeInvoke('media-library.asset.thumbnail', {
        asset_ids: pending.map((item) => item.candidate_id), size: 'grid',
      });
      const byCandidate = new Map(pending.map((item) => [item.candidate_id, item]));
      for (const thumb of (payload.items || [])) {
        const item = byCandidate.get(thumb.id);
        if (!item || !thumb.data_url) continue;
        state.thumbs[item.id] = thumb.data_url;
        const node = document.querySelector('.library-row[data-item-id="' + item.id + '"] .library-row-thumb, .library-card[data-item-id="' + item.id + '"] .library-item-thumb');
        if (!node) continue;
        const image = document.createElement('img');
        image.src = thumb.data_url;
        image.alt = '';
        image.loading = 'lazy';
        node.innerHTML = '';
        node.appendChild(image);
      }
    } catch (_) {
      // 缩略图是增强项。
    }
  }

  function libraryItemMeta(item) {
    return [
      item.width && item.height ? resolutionLabel(item.width, item.height) : '',
      item.duration_ms ? formatDuration(Number(item.duration_ms) / 1000) : '',
      formatBytes(item.bytes),
    ].filter(Boolean).join(' · ');
  }

  function libraryToolbarState() {
    const view = state.libraryView;
    if (!view) return;
    const reviewing = Boolean(view.review);
    const selected = view.selected.size;
    el('library-selected').textContent = reviewing
      ? (view.review.items.length + ' 条待复核')
      : (selected ? '已选 ' + selected + ' 项' : '未选择');
    el('library-remove').disabled = reviewing || selected === 0 || view.trash;
    el('library-remove').hidden = view.trash || reviewing;
    el('library-restore').hidden = !view.trash;
    el('library-restore').disabled = selected === 0;
    el('library-select-all').parentElement.hidden = reviewing;
    const all = view.items.length > 0 && selected === view.items.length;
    el('library-select-all').checked = all;
    el('library-select-all').indeterminate = selected > 0 && !all;
    el('library-select-all').disabled = view.items.length === 0;
  }

  function renderLibraryContent() {
    const view = state.libraryView;
    if (!view) return;
    renderLibraryHeader();
    const host = el('library-content');
    const scrollTop = host.scrollTop;
    if (view.review) {
      host.className = 'library-content';
      el('library-content-count').textContent = view.review.items.length
        ? '待复核 ' + view.review.items.length + ' 条' : '';
      el('library-content-more').hidden = true;
      if (!view.review.items.length) {
        host.innerHTML = '<div class="empty-state"><strong>复核完成</strong><span class="meta">这一轮需要人工确认的条目都处理完了。</span></div>';
        libraryToolbarState();
        return;
      }
      host.innerHTML = '<div class="review-list" role="list">' + view.review.items.map((item) => {
        const thumb = state.thumbs['review:' + item.asset_id];
        const meta = [
          item.quality ? '画质 ' + Math.round(item.quality) : '',
          item.width && item.height ? resolutionLabel(item.width, item.height) : '',
          item.duration_ms ? formatDuration(Number(item.duration_ms) / 1000) : '',
          item.bytes ? formatBytes(item.bytes) : '',
        ].filter(Boolean).join(' · ');
        return '<div class="review-row" role="listitem" data-asset-id="' + escapeHtml(item.asset_id) + '">' +
          '<span class="review-thumb" data-review-thumb="' + escapeHtml(item.candidate_id || item.asset_id) + '">' +
          (thumb ? '<img src="' + escapeHtml(thumb) + '" alt="" loading="lazy" />' : '▧') + '</span>' +
          '<span class="review-main"><strong>' + escapeHtml(item.title || item.relative_path || item.asset_id) + '</strong>' +
          '<small>' + escapeHtml(item.reason || '') + ' · ' + escapeHtml(meta) + '</small></span>' +
          '<span class="review-actions">' +
          '<button type="button" class="ghost" data-review-accept="' + escapeHtml(item.asset_id) + '"><svg class="icon" aria-hidden="true"><use href="#icon-check"/></svg>通过</button>' +
          '<button type="button" class="ghost danger-text" data-review-reject="' + escapeHtml(item.asset_id) + '"><svg class="icon" aria-hidden="true"><use href="#icon-ban"/></svg>排除</button>' +
          '</span></div>';
      }).join('') + '</div>';
      libraryToolbarState();
      host.scrollTop = scrollTop;
      return;
    }
    el('library-content-count').textContent = view.total
      ? '共 ' + view.total + ' 项，已显示 ' + view.items.length + ' 项'
      : '';
    el('library-content-more').hidden = view.trash || view.items.length >= view.total;
    if (!view.items.length) {
      host.innerHTML = view.trash
        ? '<div class="empty-state"><strong>回收站是空的</strong><span class="meta">移出的条目会在这里保留，可随时恢复。</span></div>'
        : '<div class="empty-state"><strong>库是空的</strong><span class="meta">从精选加入素材后，这里会列出条目。</span></div>';
      libraryToolbarState();
      return;
    }
    if (view.grid) {
      host.className = 'library-content library-content-grid';
      host.innerHTML = view.items.map((item, index) => {
        const thumb = state.thumbs[item.id]
          ? '<img src="' + escapeHtml(state.thumbs[item.id]) + '" alt="" loading="lazy" />'
          : (item.kind === 'video' ? '▶' : item.kind === 'audio' ? '♪' : '▧');
        const checked = view.selected.has(item.id);
        return '<article class="library-card' + (checked ? ' selected' : '') + '" data-item-id="' + escapeHtml(item.id) + '" data-index="' + index + '" role="listitem">' +
          '<label class="library-card-check"><input type="checkbox" data-select-item="' + escapeHtml(item.id) + '"' + (checked ? ' checked' : '') + ' aria-label="选择 ' + escapeHtml(item.title || item.id) + '" /></label>' +
          '<button type="button" class="library-card-open" data-open-item="' + index + '">' +
          '<span class="library-item-thumb">' + thumb + '</span>' +
          '<span class="library-item-body"><strong>' + escapeHtml(item.title || item.id) + '</strong><small>' + escapeHtml(libraryItemMeta(item)) + '</small></span>' +
          '</button></article>';
      }).join('');
    } else {
      host.className = 'library-content';
      host.innerHTML = '<div class="library-table" role="table">' +
        '<div class="library-row library-row-head" role="row"><span></span><span>名称</span><span>画面</span><span>时长</span><span>大小</span><span>标签</span><span>评分</span><span></span></div>' +
        view.items.map((item, index) => {
          const checked = view.selected.has(item.id);
          const thumb = state.thumbs[item.id]
            ? '<img src="' + escapeHtml(state.thumbs[item.id]) + '" alt="" loading="lazy" />'
            : (item.kind === 'video' ? '▶' : item.kind === 'audio' ? '♪' : '▧');
          const tags = Array.isArray(item.tags) ? item.tags : [];
          return '<div class="library-row' + (checked ? ' selected' : '') + '" data-item-id="' + escapeHtml(item.id) + '" data-index="' + index + '" role="row">' +
            '<span class="library-row-check"><input type="checkbox" data-select-item="' + escapeHtml(item.id) + '"' + (checked ? ' checked' : '') + ' aria-label="选择 ' + escapeHtml(item.title || item.id) + '" /></span>' +
            '<span class="library-row-name"><span class="library-row-thumb">' + thumb + '</span><button type="button" class="library-row-open" data-open-item="' + index + '" title="' + escapeHtml(item.title || item.id) + '">' + escapeHtml(item.title || item.id) + '</button></span>' +
            '<span>' + escapeHtml(item.width && item.height ? resolutionLabel(item.width, item.height) : '') + '</span>' +
            '<span>' + escapeHtml(item.duration_ms ? formatDuration(Number(item.duration_ms) / 1000) : '') + '</span>' +
            '<span>' + escapeHtml(formatBytes(item.bytes)) + '</span>' +
            '<span class="library-row-tags">' + (item.candidate_id
              ? '<button type="button" class="cell-edit" data-edit-tags="' + escapeHtml(item.id) + '">' + escapeHtml(tags.length ? tags.join(' · ') : '添加标签') + '</button>'
              : escapeHtml(tags.join(' · '))) + '</span>' +
            '<span>' + (item.candidate_id
              ? '<select class="cell-rating" data-edit-rating="' + escapeHtml(item.id) + '" aria-label="评分">' +
                [0, 1, 2, 3, 4, 5].map((value) => '<option value="' + value + '"' + (Number(item.rating || 0) === value ? ' selected' : '') + '>' + (value || '—') + '</option>').join('') +
                '</select>'
              : '') + '</span>' +
            '<span class="library-row-actions">' + (item.path
              ? '<button type="button" class="icon-button compact" data-reveal-item="' + escapeHtml(item.path) + '" aria-label="打开位置" title="打开位置"><svg class="icon" aria-hidden="true"><use href="#icon-folder"/></svg></button>'
              : '') + '</span>' +
            '</div>';
        }).join('') + '</div>';
    }
    libraryToolbarState();
    host.scrollTop = scrollTop;
  }

  function toggleLibraryItem(id) {
    const view = state.libraryView;
    if (!view) return;
    const selected = !view.selected.has(id);
    if (selected) view.selected.add(id);
    else view.selected.delete(id);
    // 局部更新，避免整表重绘导致滚动位置跳回顶部。
    const node = document.querySelector('#library-content [data-item-id="' + id + '"]');
    if (node) {
      node.classList.toggle('selected', selected);
      const input = node.querySelector('input[data-select-item]');
      if (input) input.checked = selected;
    }
    libraryToolbarState();
  }

  function toggleLibrarySelectAll() {
    const view = state.libraryView;
    if (!view) return;
    const all = view.selected.size === view.items.length;
    view.items.forEach((item) => {
      if (all) view.selected.delete(item.id);
      else view.selected.add(item.id);
    });
    document.querySelectorAll('#library-content [data-item-id]').forEach((node) => {
      node.classList.toggle('selected', !all);
    });
    document.querySelectorAll('#library-content input[data-select-item]').forEach((input) => {
      input.checked = !all;
    });
    libraryToolbarState();
  }

  async function removeSelectedLibraryItems() {
    const view = state.libraryView;
    if (!view || view.selected.size === 0) return;
    const ids = Array.from(view.selected);
    const confirmed = await showConfirm({
      title: '移出库条目',
      message: '将把 ' + ids.length + ' 条移出「' + view.library.name + '」。默认只删索引，文件保留，可在回收站恢复。',
      confirmText: '移出',
      danger: true,
    });
    if (!confirmed) return;
    try {
      const payload = await bridgeInvoke('media-library.holdings.item.remove', { ids, delete_files: false });
      const removed = Number(payload.removed || 0);
      await loadLibraryPage(true);
      await loadLibraries();
      renderLibraryList();
      setLibraryFeedback('已移出 ' + removed + ' 条（文件保留）', '', ids);
    } catch (error) {
      setLibraryFeedback('移出失败：' + describe(error), 'error');
    }
  }

  async function restoreLibraryItemsByIDs(ids) {
    if (!ids || !ids.length) return;
    try {
      const payload = await bridgeInvoke('media-library.holdings.item.restore', { ids });
      await loadLibraryPage(true);
      await loadLibraries();
      renderLibraryList();
      setLibraryFeedback('已恢复 ' + Number(payload.restored || 0) + ' 条');
    } catch (error) {
      setLibraryFeedback('恢复失败：' + describe(error), 'error');
    }
  }

  async function saveLibraryItemTags(itemID, value) {
    const view = state.libraryView;
    const item = view && view.items.find((entry) => entry.id === itemID);
    if (!item || !item.candidate_id) return;
    const tags = String(value || '').split(/[,，、;；]/).map((tag) => tag.trim()).filter(Boolean).slice(0, 20);
    try {
      const saved = await bridgeInvoke('media-library.asset.tags.set', { asset_id: item.candidate_id, tags });
      item.tags = saved.tags || tags;
      renderLibraryContent();
    } catch (error) {
      setLibraryFeedback('保存标签失败：' + describe(error), 'error');
    }
  }

  async function saveLibraryItemRating(itemID, value) {
    const view = state.libraryView;
    const item = view && view.items.find((entry) => entry.id === itemID);
    if (!item || !item.candidate_id) return;
    try {
      await bridgeInvoke('media-library.asset.rating.set', { asset_id: item.candidate_id, rating: Number(value) || 0 });
      item.rating = Number(value) || 0;
      setLibraryFeedback('评分已保存');
    } catch (error) {
      setLibraryFeedback('保存评分失败：' + describe(error), 'error');
    }
  }

  function beginLibraryTagEdit(button) {
    const itemID = button.getAttribute('data-edit-tags');
    const view = state.libraryView;
    const item = view && view.items.find((entry) => entry.id === itemID);
    if (!item) return;
    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'cell-input';
    input.value = (item.tags || []).join(', ');
    input.maxLength = 500;
    input.setAttribute('aria-label', '编辑标签');
    button.replaceWith(input);
    input.focus();
    input.select();
    let done = false;
    const commit = (save) => {
      if (done) return;
      done = true;
      if (save) void saveLibraryItemTags(itemID, input.value);
      else renderLibraryContent();
    };
    input.addEventListener('keydown', (event) => {
      if (event.key === 'Enter') { event.preventDefault(); commit(true); }
      else if (event.key === 'Escape') { event.preventDefault(); commit(false); }
    });
    input.addEventListener('blur', () => commit(true), { once: true });
  }

  async function rescanCurrentLibrary() {
    const view = state.libraryView;
    if (!view) return;
    el('library-rescan').disabled = true;
    setLibraryFeedback('正在扫描…');
    try {
      const payload = await bridgeInvoke('media-library.holdings.rescan', { library_id: view.id });
      view.report = payload;
      renderLibraryReport(payload);
      setLibraryFeedback('');
    } catch (error) {
      setLibraryFeedback('扫描失败：' + describe(error), 'error');
    } finally {
      el('library-rescan').disabled = false;
    }
  }

  function renderLibraryReport(report) {
    const node = el('library-report');
    const counts = report.counts || {};
    const section = (title, rows, render) => rows && rows.length
      ? '<details><summary>' + title + '（' + rows.length + '）</summary><ul>' + rows.slice(0, 30).map(render).join('') + '</ul></details>'
      : '';
    node.hidden = false;
    node.innerHTML = '<div class="library-report-head"><strong>扫描结果</strong><span class="meta">索引 ' +
      Number(counts.items || 0) + ' · 缺失 ' + Number(counts.missing || 0) + ' · 变化 ' + Number(counts.changed || 0) +
      ' · 孤儿 ' + Number(counts.orphan || 0) + '</span><button type="button" class="ghost" data-close-report>关闭</button></div>' +
      section('缺失文件', report.missing, (row) => '<li>' + escapeHtml(row.relative_path || row.path) + '</li>') +
      section('大小变化', report.changed, (row) => '<li>' + escapeHtml(row.relative_path) + '（索引 ' + formatBytes(row.db_bytes) + ' / 磁盘 ' + formatBytes(row.disk_bytes) + '）</li>') +
      section('未登记文件', report.orphan, (row) => '<li>' + escapeHtml(row) + '</li>');
  }

  async function syncCurrentLibrary() {
    const view = state.libraryView;
    if (!view) return;
    const origin = view.library.origin_selection_id || '';
    if (!origin) {
      setLibraryFeedback('这个库没有关联的精选，无法按精选同步', 'warn');
      return;
    }
    try {
      const preview = await bridgeInvoke('media-library.holdings.sync', { selection_id: origin, apply: false });
      const counts = preview.counts || {};
      const removeCount = Number(counts.remove || 0);
      const addCount = Number(counts.add || 0);
      if (!removeCount && !addCount) {
        setLibraryFeedback('已与精选一致');
        return;
      }
      const sample = (preview.remove || []).slice(0, 3).map((row) => row.relative_path).join('、');
      const message = [
        removeCount ? '将移出 ' + removeCount + ' 条（不在精选中的）' + (sample ? '：' + sample : '') : '',
        addCount ? '精选里还有 ' + addCount + ' 条没进库，可回到精选执行「加入精选库」' : '',
      ].filter(Boolean).join('；') + '。移出只删索引，文件保留。';
      if (!removeCount) {
        setLibraryFeedback(message, 'warn');
        return;
      }
      const confirmed = await showConfirm({ title: '同步精选', message, confirmText: '移出 ' + removeCount + ' 条', danger: true });
      if (!confirmed) return;
      const applied = await bridgeInvoke('media-library.holdings.sync', {
        selection_id: origin, apply: true, token: preview.token,
      });
      await loadLibraryPage(true);
      await loadLibraries();
      renderLibraryList();
      setLibraryFeedback('同步完成：移出 ' + Number(applied.removed || 0) + ' 条（文件保留）');
    } catch (error) {
      setLibraryFeedback('同步失败：' + describe(error), 'error');
    }
  }

  function toggleLibraryTrash() {
    const view = state.libraryView;
    if (!view) return;
    view.trash = !view.trash;
    el('library-trash-toggle').setAttribute('aria-pressed', String(view.trash));
    setLibraryFeedback('');
    el('library-report').hidden = true;
    void loadLibraryPage(true);
  }

  function toggleLibraryLayout() {
    const view = state.libraryView;
    if (!view) return;
    view.grid = !view.grid;
    renderLibraryContent();
  }

  async function pickBestInGroup(groupKey) {
    if (!state.selection) return;
    const members = state.selectionItems.filter((item) => selectionGroupKey(item) === groupKey);
    if (!members.length) return;
    const best = members.slice().sort((left, right) => (right.score || 0) - (left.score || 0))[0];
    const decisions = members.map((item) => ({
      asset_id: item.asset_id,
      state: item.asset_id === best.asset_id ? 'included' : 'excluded',
    }));
    setSelectionBusy(true, '正在按组调整…');
    try {
      const payload = await bridgeInvoke('media-library.selection.decide.batch', {
        selection_id: state.selection.id, decisions,
      });
      state.selection = payload.selection;
      state.selectionItems = payload.items || [];
      state.selectionDirty = true;
      renderSelection();
      setSelectionFeedback('该组已保留最佳 1 条（质量 ' + Math.round(best.score || 0) + '）。');
    } catch (error) {
      setSelectionFeedback('按组调整失败：' + describe(error), 'error');
    } finally {
      setSelectionBusy(false);
    }
  }

  async function autoSelect() {
    if (!state.selection) return;
    setSelectionBusy(true, '正在分析相似组与质量…');
    setSelectionFeedback('');
    try {
      const payload = await bridgeInvoke('media-library.selection.auto', { selection_id: state.selection.id, similarity: 12 });
      state.selection = payload.selection;
      state.selectionItems = payload.items || [];
      state.selectionDirty = true;
      renderSelection();
      const included = state.selectionItems.filter((item) => item.state === 'included').length;
      setSelectionFeedback('自动预选完成：入选 ' + included + ' 条，其余进入备选。');
    } catch (error) {
      setSelectionFeedback('自动预选失败：' + describe(error), 'error');
    } finally {
      setSelectionBusy(false);
    }
  }

  async function decideSelection(assetID, nextState) {
    if (!state.selection || state.selectionBusy) return;
    try {
      const payload = await bridgeInvoke('media-library.selection.decide', {
        selection_id: state.selection.id,
        asset_id: assetID,
        state: nextState,
      });
      state.selection = payload.selection;
      state.selectionItems = payload.items || [];
      state.selectionDirty = true;
      renderSelection();
    } catch (error) {
      setSelectionFeedback('调整失败：' + describe(error), 'error');
    }
  }

  function bind() {
    readPrefs(); syncFilterControls(); updateScopeUI();
    el('search-form').addEventListener('submit', (event) => { event.preventDefault(); void runSearch(); });
    el('refresh').addEventListener('click', () => { void refreshAll(); });
    el('view-tab-assets').addEventListener('click', () => switchView('assets'));
    el('view-tab-selection').addEventListener('click', () => switchView('selection'));
    el('view-tab-discovery').addEventListener('click', () => switchView('discovery'));
    el('view-tab-bookmarks').addEventListener('click', () => switchView('bookmarks'));
    el('view-tab-assets').parentElement.addEventListener('keydown', (event) => {
      if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
      event.preventDefault();
      const order = ['assets', 'discovery', 'bookmarks', 'selection', 'library'];
      const index = order.indexOf(state.view);
      const next = order[(index + (event.key === 'ArrowRight' ? 1 : order.length - 1)) % order.length];
      switchView(next);
      el('view-tab-' + next).focus();
    });
    el('toggle-selection-list').addEventListener('click', () => {
      const layout = document.querySelector('.selection-layout');
      if (!layout) return;
      const collapsed = layout.classList.toggle('sidebar-collapsed');
      el('toggle-selection-list').setAttribute('aria-expanded', String(!collapsed));
    });
    el('view-tab-library').addEventListener('click', () => switchView('library'));
    el('library-list').addEventListener('click', (event) => {
      const item = event.target.closest('[data-library-id]');
      if (item) void openLibraryView(item.getAttribute('data-library-id'));
    });
    el('library-rescan').addEventListener('click', () => { void rescanCurrentLibrary(); });
    el('library-sync').addEventListener('click', () => { void syncCurrentLibrary(); });
    el('library-organize').addEventListener('click', () => { void organizeCurrentLibrary(); });
    el('organize-review').addEventListener('click', () => { void openOrganizeReview(); });
    el('organize-back').addEventListener('click', closeOrganizeReview);
    el('organize-close').addEventListener('click', () => { organizePanelVisible(false); });
    el('organize-accept-all').addEventListener('click', () => {
      const view = state.libraryView;
      if (view && view.review) void decideOrganizeItems(view.review.items.map((item) => item.asset_id), true);
    });
    el('organize-reject-all').addEventListener('click', () => {
      const view = state.libraryView;
      if (view && view.review) void decideOrganizeItems(view.review.items.map((item) => item.asset_id), false);
    });
    el('library-trash-toggle').addEventListener('click', toggleLibraryTrash);
    el('library-view-toggle').addEventListener('click', toggleLibraryLayout);
    el('library-select-all').addEventListener('change', toggleLibrarySelectAll);
    el('library-remove').addEventListener('click', () => { void removeSelectedLibraryItems(); });
    el('library-restore').addEventListener('click', () => {
      const view = state.libraryView;
      if (view) void restoreLibraryItemsByIDs(Array.from(view.selected));
    });
    el('library-content-more').addEventListener('click', () => { void loadLibraryPage(false); });
    el('library-report').addEventListener('click', (event) => {
      if (event.target.closest('[data-close-report]')) el('library-report').hidden = true;
    });
    el('library-content').addEventListener('click', (event) => {
      const accept = event.target.closest('[data-review-accept]');
      if (accept) {
        event.stopPropagation();
        void decideOrganizeItems([accept.getAttribute('data-review-accept')], true);
        return;
      }
      const reject = event.target.closest('[data-review-reject]');
      if (reject) {
        event.stopPropagation();
        void decideOrganizeItems([reject.getAttribute('data-review-reject')], false);
        return;
      }
      const reveal = event.target.closest('[data-reveal-item]');
      if (reveal) {
        event.stopPropagation();
        void bridgeInvoke('media-library.asset.reveal', { path: reveal.getAttribute('data-reveal-item') })
          .catch((error) => setLibraryFeedback('打开位置失败：' + describe(error), 'error'));
        return;
      }
      const tagButton = event.target.closest('[data-edit-tags]');
      if (tagButton) { event.stopPropagation(); beginLibraryTagEdit(tagButton); return; }
      const open = event.target.closest('[data-open-item]');
      if (open && state.libraryView) {
        const index = Number(open.getAttribute('data-open-item'));
        void openPreview(index, state.libraryView.items);
        return;
      }
      const row = event.target.closest('[data-item-id]');
      if (row && state.libraryView) {
        const id = row.getAttribute('data-item-id');
        const index = Number(row.getAttribute('data-index'));
        if (event.target.closest('input[type="checkbox"], select, button, label')) return;
        if (event.detail === 2) void openPreview(index, state.libraryView.items);
        else toggleLibraryItem(id);
      }
    });
    el('library-content').addEventListener('change', (event) => {
      const select = event.target.closest('[data-select-item]');
      if (select) { toggleLibraryItem(select.getAttribute('data-select-item')); return; }
      const rating = event.target.closest('[data-edit-rating]');
      if (rating) void saveLibraryItemRating(rating.getAttribute('data-edit-rating'), rating.value);
    });
    el('selection-new').addEventListener('click', showSelectionCreateForm);
    el('selection-create').addEventListener('click', () => { void createSelection(); });
    el('selection-new-library').addEventListener('click', () => {
      const row = el('selection-library-new-row');
      row.hidden = !row.hidden;
      if (!row.hidden) el('selection-library-root').focus();
    });
    el('selection-library-create').addEventListener('click', () => { void createSelectionLibrary(); });
    el('selection-library').addEventListener('change', () => {
      if (el('selection-library').value === '__new') {
        el('selection-library-new-row').hidden = false;
        el('selection-library-root').focus();
      }
    });
    el('selection-list').addEventListener('click', (event) => {
      const item = event.target.closest('[data-selection-id]');
      if (item) void openExistingSelection(item.getAttribute('data-selection-id'));
    });
    el('selection-pipeline').addEventListener('click', (event) => {
      const chip = event.target.closest('[data-pipeline]');
      if (!chip) return;
      state.pipelineFilter = chip.getAttribute('data-pipeline') || '';
      renderSelection();
    });
    el('selection-sort').addEventListener('change', () => { state.selectionSort = el('selection-sort').value; renderSelection(); });
    el('selection-group-similar').addEventListener('click', () => {
      state.groupSimilar = !state.groupSimilar;
      el('selection-group-similar').setAttribute('aria-pressed', String(state.groupSimilar));
      renderSelection();
    });
    el('selection-open-library').addEventListener('click', () => { void revealSelectionLibrary(); });
    el('selection-auto').addEventListener('click', () => { void autoSelect(); });
    el('selection-ai-tag').addEventListener('click', () => { void aiTagSelection(); });
    el('selection-ai-cancel').addEventListener('click', () => { void cancelAIJob(); });
    el('selection-ai-profile').addEventListener('change', () => { try { localStorage.setItem('himind.media-library.ai-profile', el('selection-ai-profile').value); } catch (_) {} });
    el('selection-promote').addEventListener('click', () => { void promoteSelection(); });
    el('selection-items').addEventListener('click', (event) => {
      const pick = event.target.closest('[data-pick-best]');
      if (pick) { void pickBestInGroup(pick.getAttribute('data-pick-best')); return; }
      const button = event.target.closest('[data-decision]');
      if (!button) return;
      const card = button.closest('[data-asset-id]');
      if (card) void decideSelection(card.getAttribute('data-asset-id'), button.getAttribute('data-decision'));
    });
    el('selection-items').addEventListener('keydown', (event) => {
      const card = event.target.closest('[data-asset-id]');
      if (!card) return;
      const cards = Array.from(el('selection-items').querySelectorAll('[data-asset-id]'));
      const index = cards.indexOf(card);
      let nextState = '';
      if (event.key === '1' || event.key === 'Enter') nextState = 'included';
      else if (event.key === '2') nextState = 'reserve';
      else if (event.key === '3') nextState = 'excluded';
      else if (event.key === '0') nextState = 'candidate';
      if (nextState) {
        event.preventDefault();
        void decideSelection(card.getAttribute('data-asset-id'), nextState);
        return;
      }
      if (event.key === 'j' || event.key === 'ArrowDown') {
        event.preventDefault();
        if (cards[index + 1]) cards[index + 1].focus();
      } else if (event.key === 'k' || event.key === 'ArrowUp') {
        event.preventDefault();
        if (cards[index - 1]) cards[index - 1].focus();
      }
    });
    el('load-more').addEventListener('click', () => { void loadMoreResults(); });
    el('scope-group').addEventListener('click', (event) => { const button = event.target.closest('[data-scope]'); if (!button) return; state.scope = button.getAttribute('data-scope'); savePrefs(); updateScopeUI(); });
    el('browse-bgm').addEventListener('click', () => { state.bgm = !state.bgm; state.scope = 'library'; state.kinds = new Set(['audio']); el('query').value = ''; updateScopeUI(); void runSearch(); });
    el('kind-group').addEventListener('click', (event) => { const button = event.target.closest('[data-kind]'); if (!button) return; const kind = button.getAttribute('data-kind'); if (state.kinds.has(kind)) state.kinds.delete(kind); else state.kinds.add(kind); savePrefs(); updateScopeUI(); });
    el('filter-toggle').addEventListener('click', () => { if (state.filterOpen) closeFilters(); else openFilters(); });
    el('apply-filters').addEventListener('click', applyFilters); el('clear-filters').addEventListener('click', clearFilters);
    el('active-filters').addEventListener('click', (event) => { const button = event.target.closest('[data-clear-filter]'); if (!button) return; const key = button.getAttribute('data-clear-filter'); if (key === 'providers') state.providerFilters = new Set(); else { state.filters[key] = ''; const control = ({ orientation: 'filter-orientation', minWidth: 'filter-min-width', minDuration: 'filter-min-duration', folder: 'filter-folder', resolution: 'filter-resolution', duration: 'filter-duration' })[key]; if (control) el(control).value = ''; } updateFilterCount(); renderProviderFilters(); });
    el('results').addEventListener('click', (event) => {
      if (event.target.closest('[data-add-bgm-link]')) { void intake.open('', true); return; }
      const settingsButton = event.target.closest('[data-open-settings]');
      if (settingsButton) { openSettings(settingsButton.getAttribute('data-open-settings')); return; }
      if (event.target.closest('[data-clear-all-filters]')) { clearFilters(); renderResults(); return; }
      const playButton = event.target.closest('[data-play-index]');
      if (playButton) { event.preventDefault(); void toggleCardAudio(Number(playButton.getAttribute('data-play-index'))); return; }
      const openButton = event.target.closest('[data-open-result]');
      if (openButton) void openPreview(Number(openButton.getAttribute('data-open-result')));
    });
    el('close-preview').addEventListener('click', closePreview); el('preview-scrim').addEventListener('click', closePreview); el('previous-preview').addEventListener('click', () => navigatePreview(-1)); el('next-preview').addEventListener('click', () => navigatePreview(1)); el('adopt-button').addEventListener('click', () => { void adoptCurrent(); });
    el('preview-source-meta').addEventListener('click', (event) => {
      const button = event.target.closest('[data-copy-path]');
      if (!button) return;
      const value = button.getAttribute('data-copy-path');
      void copyText(value).then((ok) => {
        if (ok) {
          setPreviewFeedback('路径已复制');
          return;
        }
        // 剪贴板被策略拦住时，退化为选中文本，用户按 Ctrl+C 即可。
        const span = button.parentElement.querySelector('.path-text');
        if (span && window.getSelection) {
          const range = document.createRange();
          range.selectNodeContents(span);
          const selection = window.getSelection();
          selection.removeAllRanges();
          selection.addRange(range);
        }
        setPreviewFeedback('已选中路径，按 Ctrl+C 复制', 'warn');
      });
    });
    el('reveal-asset').addEventListener('click', () => { void revealPreviewAsset(); });
    el('save-user-state').addEventListener('click', () => { void saveUserState(); });
    el('ai-tag-asset').addEventListener('click', () => { void aiTagAsset(); });
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
      const view = event.target.closest('[data-view-library]');
      if (view) {
        const id = view.getAttribute('data-view-library');
        if (!document.getElementById('settings-modal').hidden) void closeSettings();
        switchView('library');
        void openLibraryView(id);
        return;
      }
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

    // Capture Escape before the native video controls consume it. Fullscreen
    // keeps its normal first-Escape behavior; the next Escape closes preview.
    document.addEventListener('keydown', (event) => {
      if (event.key === 'Escape' && state.previewOpen && !state.settingsOpen && !state.confirmOpen && el('intake-modal').hidden && el('acquisitions-modal').hidden && !document.fullscreenElement) {
        event.preventDefault(); event.stopImmediatePropagation(); closePreview();
      }
    }, true);
    document.addEventListener('keydown', (event) => {
      if (state.confirmOpen) {
        if (event.key === 'Escape') { event.preventDefault(); resolveConfirm(false); }
        else trapFocus(event, el('confirm-modal').querySelector('.confirm-dialog'));
        return;
      }
      if (!el('intake-modal').hidden || !el('acquisitions-modal').hidden) return;
      if (state.libraryView && state.view === 'library' && !state.previewOpen) {
        if (event.key === 'Escape') { event.preventDefault(); switchView('assets'); }
        return;
      }
      if (event.key === 'Escape') {
        if (document.fullscreenElement) { event.preventDefault(); void document.exitFullscreen().catch(() => {}); return; }
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

  async function start() {
    bind();
    generationUI = window.MediaLibraryGeneration.create({invoke:bridgeInvoke,describe,libraries:()=>state.libraries,defaultLibrary:()=>state.defaultLibraryId,back:()=>switchView('assets'),open:()=>{closePreview();switchView('generation');},openLibrary:id=>switchView('library',id),confirm:showConfirm,thumbnail:item=>state.thumbs[item.id]||item.thumbnail||item.preview_url||'',preview:(items,index)=>void openPreview(index,items),previewFeedback:setPreviewFeedback});
    imageEditorUI = window.MediaLibraryImageEditor.create({invoke:bridgeInvoke,describe,libraries:()=>state.libraries,defaultLibrary:()=>state.defaultLibraryId,confirm:(title,message,confirmText)=>showConfirm({title,message,confirmText}),confirming:()=>state.confirmOpen,opened:()=>{closePreview();document.body.classList.add('has-overlay');},closed:()=>document.body.classList.remove('has-overlay'),openLibrary:id=>{switchView('library',id);}});
    el('open-generation').addEventListener('click',()=>switchView('generation'));
    el('open-generation-tasks').addEventListener('click',()=>{el('close-acquisitions').click();switchView('generation');void generationUI.records();});
    intake = window.MediaLibraryIntake.create({ invoke: bridgeInvoke, describe, openSettings, confirm: showConfirm, acquisitionStarted: (job, source) => previewUI.started(job, source), jobsChanged: jobs => { if (previewUI) previewUI.jobsChanged(jobs); }, openLibrary: (id) => { closePreview(); switchView('library', id); }, previewRemote: (items, index) => { void openPreview(index, items.map((item) => Object.assign({}, item, { origin: 'platform', kind: 'video' }))); }, previewOnline: (item) => { void openPreview(0, [Object.assign({}, item, { origin: 'online' })]); }, defaultLibrary: () => state.defaultLibraryId });
    ['collect','audio'].forEach((action) => el('remote-preview-' + action).addEventListener('click', () => { const item = state.previewItem; if (!item || item.origin !== 'platform') return; void intake.open(item.source_url, action === 'audio', { embedded: true, source: item }); }));
    el('remote-preview-bookmark').addEventListener('click', () => { if (state.previewItem) void intake.bookmark(state.previewItem, el('remote-preview-bookmark')); });
    previewUI = window.MediaLibraryPreview.create({ invoke: bridgeInvoke, describe, feedback: setPreviewFeedback, engagement: window.MediaLibraryIntake.engagementMarkup, thumbnail: item => state.thumbs[item.id] || item.thumbnail || item.preview_url || '', navigate: index => { void openPreview(index, state.previewList); }, jobs: () => { void intake.jobs(); }, openLibrary: id => { closePreview(); switchView('library', id); }, openGeneration:(id,reuse)=>void generationUI.openSession(id,reuse), previewReference:(record,index)=>void openPreview(0,[{origin:'generation-reference',kind:'image',id:'reference:'+record.session_id+':'+index,title:'生成参考图 '+(index+1),generation_session:record.session_id,generation_index:index}]), taskStarted: () => { void intake.refreshJobs().catch(() => setPreviewFeedback('任务状态暂不可用，请打开任务面板重试。', 'warn')); } });
    el('preview-edit-image').addEventListener('click',()=>{if(state.previewItem)void imageEditorUI.open(state.previewItem);});
    el('preview-image-video').addEventListener('click',()=>{if(state.previewItem)generationUI.fromAsset(state.previewItem,'video.generate');});
    audio = window.MediaLibraryAudio.create({ invoke: bridgeInvoke, describe, defaultLibrary: () => state.defaultLibraryId, waveform: item => previewUI.waveform(item), drawWave: (canvas, peaks) => previewUI.drawWave(canvas, peaks), started: (job, source) => previewUI.started(job, source) });
    void intake.start();
    renderResults();
    renderSelectionList();
    renderSelection();
    switchView('assets');
    try {
      await loadProviders(); await loadLibraries(); await loadSources(); await loadFacets(); renderIndexOverview();
      renderSelectionLibraries();
    } catch (error) {
      setFeedback(describe(error), 'error');
    }
  }
  document.addEventListener('DOMContentLoaded', () => { void start(); });
})();
