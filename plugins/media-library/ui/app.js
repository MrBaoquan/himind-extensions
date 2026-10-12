/**
 * 媒体资源库自带视图。
 *
 * 视图只消费本插件 Manifest 已声明的能力：来源列表、检索、取用、本机索引。
 * 它不读文件系统、不依赖宿主页面，因此单独打开或嵌入宿主时行为一致。
 * 四种降级状态（无来源、检索失败、桥接不可用、插件被停用）都要给出可读反馈。
 */
(function () {
  'use strict';

  const state = {
    workspaceRoot: '',
    providers: [],
    results: [],
    localIndexed: false,
    busy: false,
  };

  const el = (id) => document.getElementById(id);
  // 可在视图里配置密钥的来源。没有密钥也能用视图，只是这些来源会显示未配置。
  const CONFIGURABLE = [
    { id: 'pexels', label: 'Pexels', hint: 'pexels.com/api 生成 API Key' },
    { id: 'unsplash', label: 'Unsplash', hint: 'unsplash.com/developers 生成 Access Key' },
    { id: 'freesound', label: 'Freesound', hint: 'freesound.org/apiv2/apply 生成 API Token' }
  ];

  function bridgeInvoke(capabilityId, input) {
    const internals = window.__TAURI_INTERNALS__ || {};
    if (typeof internals.invoke !== 'function') {
      const error = new Error('当前不在 HiMind Agent 的扩展视图窗口中，无法读取素材来源。');
      error.code = 'bridge_unavailable';
      return Promise.reject(error);
    }
    return internals.invoke('invoke_plugin_view_capability', {
      capabilityId: capabilityId,
      input: input || {},
    });
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

  function setBusy(busy) {
    state.busy = busy;
    for (const id of ['search-button', 'index-button', 'local-search-button', 'reload-sources']) {
      el(id).disabled = busy;
    }
  }

  function selectedKinds(scope) {
    return Array.from(scope.querySelectorAll('.kind:checked')).map((node) => node.value);
  }

  function renderSettings() {
    const host = el('settings-rows');
    host.innerHTML = CONFIGURABLE.map((item) => {
      const known = state.providers.find((provider) => provider.id === item.id);
      const configured = Boolean(known && known.configured);
      return '<div class="settings-row">' +
        '<label for="key-' + item.id + '">' + escapeHtml(item.label) + '</label>' +
        '<input id="key-' + item.id + '" type="password" autocomplete="off" spellcheck="false" placeholder="' + escapeHtml(item.hint) + '" />' +
        '<button type="button" class="ghost" data-save="' + item.id + '">保存</button>' +
        '<span class="state' + (configured ? ' on' : '') + '" data-state="' + item.id + '">' + (configured ? '已配置' : '未配置') + '</span>' +
        '</div>';
    }).join('');
  }

  async function saveKey(providerId) {
    const input = el('key-' + providerId);
    const value = input ? input.value.trim() : '';
    if (!value) {
      notify('请先粘贴密钥，再点保存。', 'warn');
      return;
    }
    setBusy(true);
    try {
      const payload = await bridgeInvoke('media-library.provider.configure', { provider: providerId, key: value });
      if (input) input.value = '';
      notify(payload.message || '已保存');
      await loadProviders();
      renderSettings();
    } catch (error) {
      notify('保存密钥失败：' + describe(error), 'error');
    } finally {
      setBusy(false);
    }
  }
  function renderSources() {

    const host = el('sources');
    if (!state.providers.length) {
      host.innerHTML = '';
      return;
    }
    host.innerHTML = state.providers
      .map((item) => {
        const classes = 'source ' + (item.configured ? 'on' : 'off');
        const hint = item.configured ? '已配置' : '未配置：' + item.requirement;
        return '<span class="' + classes + '" title="' + escapeHtml(hint) + '">' +
          escapeHtml(item.name) + ' · ' + escapeHtml(item.kinds.join('/')) +
          (item.configured ? '' : ' · 未配置') + '</span>';
      })
      .join('');
  }

  function escapeHtml(value) {
    return String(value == null ? '' : value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function emptyState(html) {
    return '<div class="empty">' + html + '</div>';
  }

  function renderResults() {
    const host = el('results');
    if (!state.results.length) {
      host.innerHTML = emptyState('还没有结果。给出关键词后点「检索」，或用「刷新来源」确认哪些来源已经配置。');
      return;
    }
    host.innerHTML = state.results
      .map((item, index) => {
        const meta = [item.author, item.width ? item.width + '×' + item.height : '', item.duration_seconds ? item.duration_seconds.toFixed(1) + 's' : '', item.license]
          .filter(Boolean)
          .map(escapeHtml)
          .join(' · ');
        const thumb = item.preview_url
          ? '<img src="' + escapeHtml(item.preview_url) + '" alt="" loading="lazy" />'
          : (item.kind === 'audio' ? '音频' : item.kind === 'video' ? '视频' : '图片');
        return '<article class="card">' +
          '<div class="thumb">' + thumb + '</div>' +
          '<div class="body"><strong>' + escapeHtml(item.title || item.id) + '</strong>' +
          '<small>' + escapeHtml(item.provider) + ' · ' + escapeHtml(item.kind) + '</small>' +
          '<small>' + meta + '</small></div>' +
          '<div class="actions"><button type="button" class="primary" data-fetch="' + index + '">取到工作区</button></div>' +
          '</article>';
      })
      .join('');
  }

  function renderLocalResults(payload) {
    const host = el('local-results');
    if (!payload) {
      host.innerHTML = emptyState('先填入要索引的目录并「建立索引」，之后即可按文件名检索本机素材。');
      return;
    }
    if (!payload.items || !payload.items.length) {
      host.innerHTML = emptyState('索引里没有匹配的素材。');
      return;
    }
    host.innerHTML = payload.items
      .map((item) => '<div class="row"><span class="kind-tag">' + escapeHtml(item.kind) + '</span>' +
        '<code title="' + escapeHtml(item.path) + '">' + escapeHtml(item.path) + '</code>' +
        '<small>' + Math.max(1, Math.round(item.bytes / 1024)) + ' KB</small></div>')
      .join('');
  }

  async function loadProviders() {
    const payload = await bridgeInvoke('media-library.provider.list', {});
    state.providers = payload.providers || [];
    renderSources();

    renderSettings();
    if (!state.providers.some((item) => item.configured)) {
      notify('还没有可用的素材来源。按下方提示配置密钥后重新打开本视图即可检索。', 'warn');
    }
  }

  async function loadContext() {
    try {
      const internals = window.__TAURI_INTERNALS__ || {};
      const context = await internals.invoke('get_plugin_view_context', {});
      state.workspaceRoot = context && context.workspace_root ? context.workspace_root : '';
    } catch (error) {
      state.workspaceRoot = '';
    }
  }

  async function runSearch() {
    const query = el('query').value.trim();
    if (!query) {
      notify('请先输入要检索的关键词。', 'warn');
      return;
    }
    const kinds = selectedKinds(el('panel-online'));
    if (!kinds.length) {
      notify('至少要选择一种素材类型。', 'warn');
      return;
    }
    setBusy(true);
    notify('');
    try {
      const payload = await bridgeInvoke('media-library.search', { query: query, kinds: kinds, per_source: 12 });
      state.results = payload.items || [];
      renderResults();
      const warnings = payload.warnings || [];
      if (warnings.length) {
        notify('部分来源没有返回结果：' + warnings.join('；'), 'warn');
      } else if (!state.results.length) {
        notify('没有匹配的素材，换个关键词或减少类型限制再试。', 'warn');
      }
    } catch (error) {
      state.results = [];
      renderResults();
      notify('检索失败：' + describe(error), 'error');
    } finally {
      setBusy(false);
    }
  }

  async function fetchAsset(index) {
    const item = state.results[index];
    if (!item) return;
    if (!state.workspaceRoot) {
      notify('当前会话没有绑定工作区，无法落盘。请从项目工作区打开这个视图。', 'warn');
      return;
    }
    setBusy(true);
    try {
      const payload = await bridgeInvoke('media-library.asset.fetch', {
        workspace_root: state.workspaceRoot,
        dest_dir: 'media',
        items: [{
          provider: item.provider,
          id: item.id,
          kind: item.kind,
          title: item.title,
          author: item.author,
          license: item.license,
          source_url: item.source_url,
          download_url: item.download_url,
          filename: item.title
        }]
      });
      const saved = (payload.saved || [])[0];
      const failed = (payload.failed || [])[0];
      if (failed) {
        notify('取用失败：' + failed.error, 'error');
      } else if (saved && saved.skipped) {
        notify('这份素材已经在工作区里，未重复下载：' + saved.path);
      } else if (saved) {
        notify('已取到工作区：' + saved.path + '（来源与许可写在同名 .source.json）');
      }
    } catch (error) {
      notify('取用失败：' + describe(error), 'error');
    } finally {
      setBusy(false);
    }
  }

  async function buildIndex() {
    const raw = el('index-roots').value.trim();
    if (!raw) {
      notify('请先填写要索引的目录。', 'warn');
      return;
    }
    const roots = raw.split(';').map((item) => item.trim()).filter(Boolean);
    setBusy(true);
    try {
      const payload = await bridgeInvoke('media-library.index.create', { roots: roots });
      const counts = payload.counts || {};
      notify('已索引 ' + payload.total + ' 个媒体文件（图片 ' + (counts.image || 0) + ' / 视频 ' + (counts.video || 0) + ' / 音频 ' + (counts.audio || 0) + '）。');
      state.localIndexed = true;
      await runLocalSearch();
    } catch (error) {
      notify('建立索引失败：' + describe(error), 'error');
    } finally {
      setBusy(false);
    }
  }

  async function runLocalSearch() {
    try {
      const payload = await bridgeInvoke('media-library.index.search', { query: el('local-query').value.trim() });
      state.localIndexed = Boolean(payload.indexed);
      renderLocalResults(payload.indexed ? payload : null);
      if (!payload.indexed && payload.message) notify(payload.message, 'warn');
    } catch (error) {
      notify('检索本机素材失败：' + describe(error), 'error');
    }
  }

  function describe(error) {
    if (error && error.code === 'bridge_unavailable') return error.message;
    if (error && typeof error === 'string') return error;
    if (error && error.message) return error.message;
    return '未知错误';
  }

  function switchTab(target) {
    const online = target === 'online';
    el('tab-online').classList.toggle('active', online);
    el('tab-local').classList.toggle('active', !online);
    el('tab-online').setAttribute('aria-selected', String(online));
    el('tab-local').setAttribute('aria-selected', String(!online));
    el('panel-online').hidden = !online;
    el('panel-local').hidden = online;
  }

  function bind() {
    el('search-form').addEventListener('submit', (event) => { event.preventDefault(); void runSearch(); });
    el('reload-sources').addEventListener('click', () => { void loadProviders().catch((error) => notify('读取来源失败：' + describe(error), 'error')); });
    el('results').addEventListener('click', (event) => {
    el('settings-rows').addEventListener('click', (event) => {
      const trigger = event.target.closest('[data-save]');
      if (!trigger) return;
      void saveKey(trigger.getAttribute('data-save'));
    });

      const trigger = event.target.closest('[data-fetch]');
      if (!trigger) return;
      void fetchAsset(Number(trigger.getAttribute('data-fetch')));
    });
    el('index-button').addEventListener('click', () => { void buildIndex(); });
    el('local-search-button').addEventListener('click', () => { void runLocalSearch(); });
    el('tab-online').addEventListener('click', () => switchTab('online'));
    el('tab-local').addEventListener('click', () => switchTab('local'));
  }

  async function start() {
    bind();
    renderResults();
    renderLocalResults(null);
    try {
      await loadContext();
      await loadProviders();
      if (!state.workspaceRoot) {
        notify('当前视图没有绑定工作区，检索可用，但「取到工作区」需要从项目工作区打开本视图。', 'warn');
      }
    } catch (error) {
      notify(describe(error), 'error');
    }
  }

  document.addEventListener('DOMContentLoaded', () => { void start(); });
})();
