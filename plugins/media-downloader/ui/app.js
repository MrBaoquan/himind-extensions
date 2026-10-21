(function () {
  'use strict';

  const STORAGE_KEY = 'himind.media-downloader.settings.v1';
  const VERSION = '0.2.2';

  const DEFAULT_SETTINGS = {
    defaultDest: '',
    defaultQuality: 'best',
    defaultMaxBytes: 2147483648,
    defaultMaxItems: 50,
    pageSize: 50,
    pollMs: 2500,
    theme: 'system',
    density: 'comfortable',
  };

  const VIEW_META = {
    overview: { title: '概览', subtitle: '下载队列、运行状态与最近完成的集中视图。' },
    download: { title: '新建下载', subtitle: '粘贴链接，按步骤解析并保存到系统下载文件夹或指定目录。' },
    tasks: { title: '下载管理', subtitle: '查看正在下载、已完成与失败的记录，快速定位下载文件。' },
    runtime: { title: '运行时与诊断', subtitle: '检查 yt-dlp、ffmpeg 与本地运行环境。' },
    settings: { title: '设置', subtitle: '配置下载默认值、界面密度与任务刷新行为。' },
  };

  const state = {
    workspaceRoot: '',
    runtime: null,
    media: null,
    mediaInput: '',
    jobs: [],
    activeJobs: [],
    recentJobs: [],
    summary: null,
    total: 0,
    page: 1,
    pageSize: 50,
    filters: { status: 'all', query: '', range: 'all', sort: 'created_desc' },
    selected: new Set(),
    lastSelectedJobId: '',
    currentView: 'overview',
    jobsLoaded: false,
    loadingJobs: false,
    timer: null,
    probeToken: 0,
    jobsRequestSeq: 0,
    lastJobsLoadedAt: 0,
    searchTimer: null,
    drawerJobId: null,
    drawerJob: null,
    drawerLog: null,
    drawerLogError: '',
    confirmResolve: null,
    focusBeforeDialog: null,
    settings: loadSettings(),
  };

  state.pageSize = state.settings.pageSize;

  const el = (id) => document.getElementById(id);
  const qs = (selector, root) => (root || document).querySelector(selector);
  const qsa = (selector, root) => Array.from((root || document).querySelectorAll(selector));

  function icon(name, className) {
    return '<svg class="icon' + (className ? ' ' + className : '') + '" aria-hidden="true"><use href="#i-' + name + '"></use></svg>';
  }

  function escapeHtml(value) {
    return String(value == null ? '' : value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  function describe(error) {
    if (error && error.code === 'bridge_unavailable') return error.message;
    if (typeof error === 'string') return error;
    if (error && error.message) return error.message;
    try {
      return JSON.stringify(error);
    } catch (_) {
      return '未知错误';
    }
  }

  function clamp(value, min, max) {
    return Math.max(min, Math.min(max, value));
  }

  function formatBytes(value) {
    const size = Number(value || 0);
    if (!Number.isFinite(size) || size <= 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let index = 0;
    let current = size;
    while (current >= 1024 && index < units.length - 1) {
      current /= 1024;
      index += 1;
    }
    return (index === 0 ? current.toFixed(0) : current.toFixed(current >= 100 ? 0 : current >= 10 ? 1 : 2)) + ' ' + units[index];
  }

  function formatDuration(seconds) {
    const total = Math.round(Number(seconds || 0));
    if (!total) return '';
    const hours = Math.floor(total / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    const secs = total % 60;
    if (hours) return hours + ':' + String(minutes).padStart(2, '0') + ':' + String(secs).padStart(2, '0');
    return minutes + ':' + String(secs).padStart(2, '0');
  }

  function parseDate(value) {
    if (!value) return null;
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? null : date;
  }

  function formatDate(value, withTime) {
    const date = parseDate(value);
    if (!date) return '—';
    const pad = (number) => String(number).padStart(2, '0');
    const base = date.getFullYear() + '-' + pad(date.getMonth() + 1) + '-' + pad(date.getDate());
    if (!withTime) return base;
    return base + ' ' + pad(date.getHours()) + ':' + pad(date.getMinutes());
  }

  function formatRelative(value) {
    const date = parseDate(value);
    if (!date) return '—';
    const diff = Math.round((Date.now() - date.getTime()) / 1000);
    if (diff < 30) return '刚刚';
    if (diff < 3600) return Math.floor(diff / 60) + ' 分钟前';
    if (diff < 86400) return Math.floor(diff / 3600) + ' 小时前';
    if (diff < 86400 * 7) return Math.floor(diff / 86400) + ' 天前';
    return formatDate(value, false);
  }

  function statusLabel(status) {
    switch (status) {
      case 'queued': return '排队中';
      case 'running': return '下载中';
      case 'canceling': return '取消中';
      case 'completed': return '已完成';
      case 'failed': return '失败';
      case 'canceled': return '已取消';
      default: return status || '未知';
    }
  }

  function statusBadge(status) {
    return '<span class="status-badge ' + escapeHtml(status || 'neutral') + '">' + escapeHtml(statusLabel(status)) + '</span>';
  }

  function jobTitle(job) {
    return job.title || job.url || job.job_id || '未命名任务';
  }

  function displayDest(job) {
    const root = job.workspace_root || state.workspaceRoot;
    const dest = job.dest_dir || '';
    if (!dest) return '—';
    if (root) {
      const normalizedRoot = root.replace(/[\\/]+$/, '');
      if (dest === normalizedRoot) return '.';
      if (dest.indexOf(normalizedRoot + '\\') === 0 || dest.indexOf(normalizedRoot + '/') === 0) {
        return dest.slice(normalizedRoot.length + 1) || '.';
      }
    }
    return dest;
  }

  function workspaceName() {
    const value = String(state.workspaceRoot || '').replace(/[\\/]+$/, '');
    if (!value) return '未绑定';
    const parts = value.split(/[\\/]/);
    return parts[parts.length - 1] || value;
  }

  function systemDownloadsDir() {
    const value = state.runtime && state.runtime.downloads_dir ? String(state.runtime.downloads_dir) : '';
    return value || 'Downloads';
  }

  function defaultDestination() {
    return state.settings.defaultDest || systemDownloadsDir();
  }

  function syncDestinationDefaults() {
    const resolved = systemDownloadsDir();
    if (el('setting-dest')) {
      if (document.activeElement !== el('setting-dest')) el('setting-dest').value = state.settings.defaultDest || '';
      el('setting-dest').placeholder = resolved;
    }
    if (el('setting-dest-hint')) {
      el('setting-dest-hint').textContent = state.settings.defaultDest
        ? '当前默认目录：' + state.settings.defaultDest
        : '留空时使用系统下载文件夹：' + resolved;
    }
    if (!state.media && el('dest-dir')) {
      const current = el('dest-dir').value.trim();
      if (document.activeElement !== el('dest-dir') && (!current || current === 'media' || current === 'Downloads')) {
        el('dest-dir').value = defaultDestination();
      }
      el('dest-dir').placeholder = resolved;
    }
    updateDownloadSummary();
  }

  function setComposerStep(step) {
    const value = clamp(Number(step) || 1, 1, 3);
    qsa('#composer-steps [data-step]').forEach((node) => {
      const current = Number(node.getAttribute('data-step'));
      node.classList.toggle('active', current === value);
      node.classList.toggle('done', current < value);
    });
    const labels = {
      1: '第 1 步 · 粘贴链接',
      2: '第 2 步 · 解析媒体',
      3: '第 3 步 · 确认下载',
    };
    if (el('composer-stage')) el('composer-stage').textContent = labels[value];
  }

  function setComposerBadge(label, tone) {
    const badge = el('composer-badge');
    if (!badge) return;
    if (!label) {
      badge.hidden = true;
      badge.textContent = '';
      badge.className = 'status-badge neutral';
      return;
    }
    badge.hidden = false;
    badge.textContent = label;
    badge.className = 'status-badge ' + (tone || 'neutral');
  }

  function normalizePath(value) {
    return String(value || '').trim().replace(/\//g, '\\').replace(/\\+$/g, '').toLowerCase();
  }

  function showComposerOptions(show) {
    if (el('download-step-options')) el('download-step-options').hidden = !show;
  }

  function pathDirectory(path) {
    const value = String(path || '').replace(/[\\/]+$/, '');
    if (!value) return '';
    const index = Math.max(value.lastIndexOf('\\'), value.lastIndexOf('/'));
    return index > 2 ? value.slice(0, index) : value;
  }
  function jobFolder(job) {
    if (!job) return '';
    const outputs = Array.isArray(job.outputs) ? job.outputs.filter(Boolean) : [];
    if (outputs.length) return pathDirectory(outputs[0]);
    return job.dest_dir || '';
  }

  async function openFolder(path) {
    const value = String(path || '').trim();
    if (!value) {
      toast('没有可定位的下载目录。', 'warn');
      return;
    }
    try {
      await window.__TAURI_INTERNALS__.invoke('open_folder', { path: value });
    } catch (error) {
      toast('打开下载目录失败：' + describe(error), 'error');
    }
  }

  async function chooseDirectory(targetId, isSetting) {
    try {
      const result = await window.__TAURI_INTERNALS__.invoke('pick_workspace_directory', {});
      const path = result && result.path ? String(result.path) : '';
      if (!path) return;
      if (el(targetId)) el(targetId).value = path;
      if (isSetting) {
        if (el('setting-dest')) el('setting-dest').value = path;
      }
      updateDownloadSummary();
    } catch (error) {
      toast('选择目录失败：' + describe(error), 'error');
    }
  }

  function loadSettings() {
    try {
      const raw = window.localStorage.getItem(STORAGE_KEY);
      if (!raw) return Object.assign({}, DEFAULT_SETTINGS);
      const parsed = JSON.parse(raw);
      const merged = Object.assign({}, DEFAULT_SETTINGS, parsed || {});
      if ([25, 50, 100, 200].indexOf(Number(merged.pageSize)) < 0) merged.pageSize = DEFAULT_SETTINGS.pageSize;
      if ([1500, 2500, 5000, 10000].indexOf(Number(merged.pollMs)) < 0) merged.pollMs = DEFAULT_SETTINGS.pollMs;
      if (['system', 'light', 'dark'].indexOf(merged.theme) < 0) merged.theme = DEFAULT_SETTINGS.theme;
      if (['comfortable', 'compact'].indexOf(merged.density) < 0) merged.density = DEFAULT_SETTINGS.density;
      if (['best', '1080p', '720p', '480p'].indexOf(merged.defaultQuality) < 0) merged.defaultQuality = DEFAULT_SETTINGS.defaultQuality;
      merged.defaultMaxItems = clamp(Number(merged.defaultMaxItems) || DEFAULT_SETTINGS.defaultMaxItems, 1, 200);
      merged.defaultMaxBytes = Number(merged.defaultMaxBytes) || DEFAULT_SETTINGS.defaultMaxBytes;
      merged.defaultDest = String(merged.defaultDest || DEFAULT_SETTINGS.defaultDest);
      if (merged.defaultDest === 'media') merged.defaultDest = '';
      merged.pageSize = Number(merged.pageSize);
      merged.pollMs = Number(merged.pollMs);
      return merged;
    } catch (_) {
      return Object.assign({}, DEFAULT_SETTINGS);
    }
  }

  function saveSettings() {
    try {
      window.localStorage.setItem(STORAGE_KEY, JSON.stringify(state.settings));
    } catch (_) {
      // 本地存储不可用时，设置只在当前会话生效。
    }
  }

  function applySettings() {
    const settings = state.settings;
    document.documentElement.dataset.theme = settings.theme || 'system';
    const table = qs('.data-table');
    if (table) table.classList.toggle('dense', settings.density === 'compact');
    state.pageSize = Number(settings.pageSize) || 50;
    if (el('setting-dest')) el('setting-dest').value = settings.defaultDest;
    if (el('setting-quality')) el('setting-quality').value = settings.defaultQuality;
    if (el('setting-max-bytes')) el('setting-max-bytes').value = String(settings.defaultMaxBytes);
    if (el('setting-max-items')) el('setting-max-items').value = String(settings.defaultMaxItems);
    if (el('setting-theme')) el('setting-theme').value = settings.theme;
    if (el('setting-page-size')) el('setting-page-size').value = String(settings.pageSize);
    if (el('setting-poll')) el('setting-poll').value = String(settings.pollMs);
    if (el('setting-density')) el('setting-density').value = settings.density;
    if (el('page-size')) el('page-size').value = String(settings.pageSize);
    if (!state.media) {
      if (el('quality')) el('quality').value = settings.defaultQuality;
      if (el('max-bytes')) el('max-bytes').value = String(settings.defaultMaxBytes);
      if (el('max-items')) el('max-items').value = String(settings.defaultMaxItems);
      syncDestinationDefaults();
    }
  }

  function toast(message, tone, timeout) {
    const stack = el('toast-stack');
    if (!stack) return;
    const node = document.createElement('div');
    const resolvedTone = tone || 'info';
    const iconName = resolvedTone === 'success' ? 'check' : resolvedTone === 'error' ? 'alert' : resolvedTone === 'warn' ? 'info' : 'info';
    node.className = 'toast ' + resolvedTone;
    node.innerHTML = icon(iconName) + '<span>' + escapeHtml(message) + '</span>';
    stack.appendChild(node);
    const remove = () => {
      node.classList.add('leaving');
      window.setTimeout(() => node.remove(), 220);
    };
    window.setTimeout(remove, timeout || 4200);
  }

  function copyText(text) {
    const value = String(text == null ? '' : text);
    if (!value) return Promise.resolve(false);
    if (navigator.clipboard && navigator.clipboard.writeText) {
      return navigator.clipboard.writeText(value).then(() => true).catch(() => fallbackCopy(value));
    }
    return Promise.resolve(fallbackCopy(value));
  }

  function fallbackCopy(value) {
    try {
      const area = document.createElement('textarea');
      area.value = value;
      area.setAttribute('readonly', 'readonly');
      area.style.position = 'fixed';
      area.style.opacity = '0';
      document.body.appendChild(area);
      area.select();
      const ok = document.execCommand('copy');
      area.remove();
      return ok;
    } catch (_) {
      return false;
    }
  }

  function bridgeInvoke(capabilityId, input) {
    const internals = window.__TAURI_INTERNALS__ || {};
    if (typeof internals.invoke !== 'function') {
      const error = new Error('当前不在 HiMind Agent 的扩展视图窗口中。');
      error.code = 'bridge_unavailable';
      return Promise.reject(error);
    }
    return internals.invoke('invoke_plugin_view_capability', {
      capabilityId: capabilityId,
      input: input || {},
    });
  }

  async function loadWorkspace() {
    const label = el('workspace-label');
    const overview = el('overview-workspace');
    try {
      const context = await window.__TAURI_INTERNALS__.invoke('get_plugin_view_context', {});
      state.workspaceRoot = context && context.workspace_root ? String(context.workspace_root) : '';
    } catch (_) {
      state.workspaceRoot = '';
    }
    if (label) {
      label.textContent = workspaceName();
      label.title = state.workspaceRoot || '未绑定工作区';
    }
    if (overview) {
      overview.textContent = state.workspaceRoot || '未绑定';
      overview.title = state.workspaceRoot || '未绑定工作区';
    }
    if (!state.workspaceRoot) {
      toast('当前视图未绑定工作区，下载记录仍可保存到系统下载文件夹。', 'info', 5000);
    }
    updateStartState();
  }

  function openSidebar() {
    if (el('sidebar')) el('sidebar').classList.add('open');
    if (el('sidebar-scrim')) el('sidebar-scrim').hidden = false;
    if (el('sidebar-toggle')) el('sidebar-toggle').setAttribute('aria-expanded', 'true');
  }

  function closeSidebar() {
    if (el('sidebar')) el('sidebar').classList.remove('open');
    if (el('sidebar-scrim')) el('sidebar-scrim').hidden = true;
    if (el('sidebar-toggle')) el('sidebar-toggle').setAttribute('aria-expanded', 'false');
  }

  function prepareOverlay() {
    const open = !!(
      (el('drawer-layer') && !el('drawer-layer').hidden) ||
      (el('confirm-layer') && !el('confirm-layer').hidden)
    );
    document.body.classList.toggle('has-overlay', open);
  }

  function trapFocus(layer, event) {
    if (!layer || event.key !== 'Tab') return;
    const focusables = qsa(
      'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
      layer
    ).filter((node) => node.offsetParent !== null || node === document.activeElement);
    if (!focusables.length) return;
    const first = focusables[0];
    const last = focusables[focusables.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  function resetSelection() {
    state.selected.clear();
    if (el('select-all')) {
      el('select-all').checked = false;
      el('select-all').indeterminate = false;
    }
    renderBulkBar();
  }

  function navigate(view, options) {
    const meta = VIEW_META[view];
    if (!meta) return;
    state.currentView = view;
    qsa('.view').forEach((node) => {
      const active = node.id === 'view-' + view;
      node.hidden = !active;
      node.classList.toggle('active', active);
    });
    qsa('.nav-item').forEach((node) => {
      const active = node.getAttribute('data-view') === view;
      node.classList.toggle('active', active);
      if (active) node.setAttribute('aria-current', 'page');
      else node.removeAttribute('aria-current');
    });
    el('page-title').textContent = meta.title;
    el('page-subtitle').textContent = meta.subtitle;
    closeSidebar();
    window.scrollTo({ top: 0, behavior: 'smooth' });
    if (view === 'tasks' && (!state.jobsLoaded || (options && options.refresh))) {
      void loadJobs({ silent: false });
    }
    if (view === 'overview') {
      updateOverview();
      void loadActiveJobs();
      void loadRecentJobs();
      if (!state.jobsLoaded) void loadJobs({ silent: true });
    }
    if (view === 'runtime' && !state.runtime) void loadRuntime();
    if (view === 'download') {
      window.setTimeout(() => { if (el('url')) el('url').focus(); }, 60);
    }
  }

  function runtimeStateTone(runtime) {
    if (!runtime) return 'muted';
    switch (runtime.state) {
      case 'ready': return 'ok';
      case 'outdated': return 'warn';
      case 'missing':
      case 'broken':
      case 'error': return 'error';
      default: return 'muted';
    }
  }

  function runtimeStateLabel(runtime) {
    if (!runtime) return '未检查';
    switch (runtime.state) {
      case 'ready': return '可用';
      case 'outdated': return '版本不一致';
      case 'missing': return '未安装';
      case 'broken': return '不可用';
      case 'error': return '检查失败';
      default: return runtime.state || '未知';
    }
  }

  async function loadRuntime(options) {
    const silent = !!(options && options.silent);
    const button = options && options.button;
    if (button) button.disabled = true;
    try {
      const runtime = await bridgeInvoke('media.downloader.runtime.status', {});
      state.runtime = runtime || null;
      renderRuntime();
      if (!silent) toast('运行时状态已刷新。', 'success');
      return runtime;
    } catch (error) {
      state.runtime = { state: 'error', message: describe(error), capabilities: [] };
      renderRuntime();
      if (!silent) toast('检查运行时失败：' + describe(error), 'error');
      return null;
    } finally {
      if (button) button.disabled = false;
    }
  }

  function renderRuntime() {
    const runtime = state.runtime;
    const tone = runtimeStateTone(runtime);
    const ready = !!runtime && runtime.state === 'ready';
    const ytdlpReady = !!runtime && (runtime.state === 'ready' || runtime.state === 'outdated');
    const ffmpegReady = !!(runtime && runtime.ffmpeg_path);

    const dot = el('side-runtime-dot');
    if (dot) dot.className = 'dot ' + (tone === 'muted' ? 'muted' : tone);
    if (el('side-runtime-label')) el('side-runtime-label').textContent = runtime ? (ready ? '运行时可用' : '运行时' + runtimeStateLabel(runtime)) : '运行时未检查';
    if (el('side-runtime-version')) {
      const parts = [];
      if (runtime && runtime.version) parts.push('yt-dlp ' + runtime.version);
      else parts.push('yt-dlp');
      parts.push(ffmpegReady ? 'ffmpeg 可用' : 'ffmpeg 未检测');
      el('side-runtime-version').textContent = parts.join(' · ');
    }

    const overviewBody = el('overview-runtime-body');
    if (overviewBody) {
      const ytDetail = ytdlpReady
        ? (runtime.version || '已安装') + (runtime.source ? ' · ' + runtime.source : '')
        : (runtime && runtime.message ? runtime.message : '尚未安装或无法启动');
      const ffDetail = ffmpegReady
        ? (runtime.ffmpeg_version || '已安装') + ' · 合并与音频提取可用'
        : '未检测到，部分格式与仅音频不可用';
      overviewBody.innerHTML =
        '<div class="health-row"><span class="dot ' + (ytdlpReady ? 'ok' : 'error') + '"></span><div><strong>yt-dlp ' + escapeHtml(ytdlpReady ? '可用' : '不可用') + '</strong><span class="truncate" title="' + escapeHtml(ytDetail) + '">' + escapeHtml(ytDetail) + '</span></div></div>' +
        '<div class="health-row"><span class="dot ' + (ffmpegReady ? 'ok' : 'warn') + '"></span><div><strong>ffmpeg ' + escapeHtml(ffmpegReady ? '可用' : '未检测到') + '</strong><span class="truncate" title="' + escapeHtml(ffDetail) + '">' + escapeHtml(ffDetail) + '</span></div></div>';
    }
    if (el('overview-install')) el('overview-install').hidden = ready;

    const orb = el('runtime-orb');
    if (orb) orb.className = 'runtime-orb ' + (tone === 'ok' ? 'ok' : tone === 'warn' ? 'warn' : tone === 'error' ? 'error' : 'muted');
    if (el('runtime-title')) el('runtime-title').textContent = runtime ? (ready ? '运行时已就绪' : '运行时' + runtimeStateLabel(runtime)) : '尚未检查';
    if (el('runtime-message')) {
      const message = runtime && runtime.message
        ? runtime.message
        : ready
          ? 'yt-dlp 与 ffmpeg 均已就绪，可以解析链接并执行下载。'
          : '检查 yt-dlp 与 ffmpeg 是否可用。安装器只下载固定版本并校验 SHA-256。';
      el('runtime-message').textContent = message;
    }
    if (el('runtime-install-btn')) el('runtime-install-btn').hidden = ready;

    const ytdlpBadge = el('ytdlp-status');
    if (ytdlpBadge) {
      ytdlpBadge.textContent = ytdlpReady ? (runtime.state === 'outdated' ? '版本不一致' : '可用') : '不可用';
      ytdlpBadge.className = 'status-badge ' + (ytdlpReady ? (runtime.state === 'outdated' ? 'queued' : 'completed') : 'failed');
    }
    const ffmpegBadge = el('ffmpeg-status');
    if (ffmpegBadge) {
      ffmpegBadge.textContent = ffmpegReady ? '可用' : '未检测到';
      ffmpegBadge.className = 'status-badge ' + (ffmpegReady ? 'completed' : 'queued');
    }
    if (el('ytdlp-version')) el('ytdlp-version').textContent = runtime && runtime.version ? runtime.version : '—';
    if (el('ytdlp-expected')) el('ytdlp-expected').textContent = runtime && runtime.expected_version ? runtime.expected_version : '—';
    if (el('ytdlp-source')) el('ytdlp-source').textContent = runtime && runtime.source ? runtime.source : '—';
    if (el('ytdlp-path')) {
      el('ytdlp-path').textContent = runtime && runtime.path ? runtime.path : '—';
      el('ytdlp-path').title = runtime && runtime.path ? runtime.path : '';
    }
    if (el('ffmpeg-version')) el('ffmpeg-version').textContent = runtime && runtime.ffmpeg_version ? runtime.ffmpeg_version : '—';
    if (el('ffmpeg-path')) {
      el('ffmpeg-path').textContent = runtime && runtime.ffmpeg_path ? runtime.ffmpeg_path : '—';
      el('ffmpeg-path').title = runtime && runtime.ffmpeg_path ? runtime.ffmpeg_path : '';
    }
    if (el('ffmpeg-capability')) {
      el('ffmpeg-capability').textContent = ffmpegReady ? '音频提取、格式合并与转码' : '未安装时不可用';
    }
    if (el('diagnostics-output')) {
      const report = {
        tool: 'himind-media-downloader',
        version: VERSION,
        checked_at: new Date().toISOString(),
        workspace_root: state.workspaceRoot || '',
        runtime: runtime || null,
      };
      el('diagnostics-output').textContent = JSON.stringify(report, null, 2);
    }
    if (el('update-banner')) {
      const blocked = !!runtime && runtime.downloads_writable === false;
      el('update-banner').hidden = !blocked;
      el('update-banner').textContent = blocked
        ? '系统下载目录当前不可写，下载会自动回退到插件下载目录；可从下载管理直接打开实际保存位置。'
        : '';
    }
    syncDestinationDefaults();
  }

  async function installRuntime(button) {
    if (button) button.disabled = true;
    try {
      const result = await bridgeInvoke('media.downloader.runtime.install', {});
      toast(result && result.message ? result.message : '运行时已安装。', 'success', 6000);
      await loadRuntime({ silent: true });
    } catch (error) {
      toast('安装运行时失败：' + describe(error), 'error', 7000);
    } finally {
      if (button) button.disabled = false;
    }
  }

  function setProbeState(kind, message, busy) {
    const node = el('probe-state');
    if (!message) {
      node.hidden = true;
      node.textContent = '';
      node.className = 'inline-state';
      return;
    }
    node.hidden = false;
    node.className = 'inline-state' + (kind ? ' ' + kind : '');
    node.innerHTML = (busy ? '<span class="spinner"></span>' : '') + '<span>' + escapeHtml(message) + '</span>';
  }

  function validateMediaUrl(value) {
    let parsed;
    try {
      parsed = new URL(value);
    } catch (_) {
      throw new Error('请输入完整的 http 或 https 媒体链接。');
    }
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      throw new Error('只接受 http 或 https 链接。');
    }
    return parsed.toString();
  }

  async function probe() {
    const button = el('probe-button');
    const raw = el('url').value.trim();
    if (!raw) {
      setProbeState('error', '请先输入媒体链接。');
      el('url').focus();
      return;
    }
    let url;
    try {
      url = validateMediaUrl(raw);
    } catch (error) {
      setProbeState('error', error.message);
      return;
    }
    const token = ++state.probeToken;
    button.disabled = true;
    setComposerStep(2);
    setProbeState('', '正在解析媒体信息，播放列表可能需要更长时间…', true);
    try {
      const result = await bridgeInvoke('media.downloader.info.get', {
        url: url,
        playlist: el('playlist').checked,
        max_items: 200,
      });
      if (token !== state.probeToken) return;
      state.media = result || null;
      state.mediaInput = raw;
      renderMedia(result || {});
      showComposerOptions(true);
      setComposerStep(3);
      setComposerBadge('已解析', 'completed');
      setProbeState('success', '解析完成，确认保存位置后即可开始下载。');
      window.setTimeout(() => {
        if (el('download-step-options')) el('download-step-options').scrollIntoView({ behavior: 'smooth', block: 'nearest' });
      }, 60);
    } catch (error) {
      if (token !== state.probeToken) return;
      state.media = null;
      state.mediaInput = '';
      el('info-card').hidden = true;
      el('media-entries-wrap').hidden = true;
      showComposerOptions(false);
      setComposerStep(1);
      setComposerBadge('解析失败', 'failed');
      setProbeState('error', '解析失败：' + describe(error));
      updateDownloadSummary();
      updateStartState();
    } finally {
      if (token === state.probeToken) button.disabled = false;
    }
  }

  function renderMedia(media) {
    el('info-card').hidden = false;
    showComposerOptions(true);
    el('media-title').textContent = media.title || '未命名媒体';
    el('media-title').title = media.title || '';
    el('media-kind').textContent = media.is_playlist ? '播放列表' : (media.formats && media.formats.length ? '视频' : '媒体');

    const cover = el('media-cover');
    const coverUrl = safeThumbnail(media.thumbnail);
    if (coverUrl) {
      cover.innerHTML = '<img alt="" referrerpolicy="no-referrer" src="' + escapeHtml(coverUrl) + '">';
      const image = cover.querySelector('img');
      image.addEventListener('error', () => { cover.innerHTML = icon('play'); }, { once: true });
    } else {
      cover.innerHTML = icon('play');
    }

    const meta = [];
    if (media.uploader) meta.push(media.uploader);
    if (media.extractor) meta.push(media.extractor);
    if (media.duration_seconds) meta.push(formatDuration(media.duration_seconds));
    if (media.entry_count) meta.push(media.entry_count + ' 个条目');
    el('media-meta').textContent = meta.join(' · ') || '未提供更多元信息';

    const tags = [];
    if (media.is_playlist) tags.push('<span class="tag">播放列表</span>');
    if (media.formats && media.formats.length) tags.push('<span class="tag">' + media.formats.length + ' 种格式</span>');
    if (media.subtitles && media.subtitles.length) tags.push('<span class="tag">' + media.subtitles.length + ' 种字幕</span>');
    if (media.webpage_url) {
      try {
        const host = new URL(media.webpage_url).hostname;
        if (host) tags.push('<span class="tag">' + escapeHtml(host) + '</span>');
      } catch (_) {}
    }
    el('media-tags').innerHTML = tags.join('');

    const format = el('format');
    format.innerHTML = '<option value="">自动选择</option>';
    const formats = (media.formats || []).slice().sort((left, right) => (right.height || 0) - (left.height || 0));
    formats.slice(0, 80).forEach((item) => {
      const option = document.createElement('option');
      option.value = item.format_id;
      const resolution = item.resolution || (item.height ? item.height + 'p' : '');
      const codec = item.vcodec && item.vcodec !== 'none' ? item.vcodec : (item.acodec && item.acodec !== 'none' ? item.acodec : '');
      const size = item.filesize || item.filesize_approx;
      const label = [item.format_id, resolution, item.ext, codec, size ? formatBytes(size) : ''].filter(Boolean).join(' · ');
      option.textContent = label || item.format_id;
      format.appendChild(option);
    });

    const subtitles = el('subtitles');
    subtitles.innerHTML = '';
    if (media.subtitles && media.subtitles.length) {
      media.subtitles.forEach((lang) => {
        const label = document.createElement('label');
        label.className = 'chip';
        const input = document.createElement('input');
        input.type = 'checkbox';
        input.value = lang;
        input.addEventListener('change', updateDownloadSummary);
        label.appendChild(input);
        label.appendChild(document.createTextNode(lang));
        subtitles.appendChild(label);
      });
    } else {
      subtitles.innerHTML = '<span class="chip muted-chip">暂无字幕</span>';
    }

    const entriesWrap = el('media-entries-wrap');
    if (media.is_playlist && media.entries && media.entries.length) {
      entriesWrap.hidden = false;
      el('media-entries-summary').textContent = '播放列表条目 · ' + (media.entry_count || media.entries.length) + ' 个';
      el('media-entries').innerHTML = media.entries.slice(0, 50).map((entry, index) =>
        '<div class="entry-item"><span class="entry-index">' + String(index + 1).padStart(2, '0') + '</span><span class="entry-title" title="' + escapeHtml(entry.title || entry.url || '') + '">' + escapeHtml(entry.title || entry.url || '未命名条目') + '</span><span class="entry-duration">' + escapeHtml(formatDuration(entry.duration)) + '</span></div>'
      ).join('');
    } else {
      entriesWrap.hidden = true;
      el('media-entries').innerHTML = '';
    }
    updateDownloadSummary();
    updateStartState();
  }

  function selectedSubtitles() {
    return qsa('#subtitles input:checked').map((node) => node.value);
  }

  function updateDownloadSummary() {
    const media = state.media;
    const qualityValue = el('quality') ? el('quality').value : state.settings.defaultQuality;
    const qualityLabel = el('quality') && el('quality').selectedOptions[0] ? el('quality').selectedOptions[0].textContent : qualityValue;
    const formatSelect = el('format');
    const formatLabel = formatSelect && formatSelect.selectedOptions[0] ? formatSelect.selectedOptions[0].textContent : '自动选择';
    const dest = el('dest-dir') ? el('dest-dir').value.trim() || defaultDestination() : defaultDestination();
    const subs = selectedSubtitles();
    if (el('summary-quality')) el('summary-quality').textContent = qualityLabel;
    if (el('summary-format')) el('summary-format').textContent = formatLabel;
    if (el('summary-dest')) {
      el('summary-dest').textContent = dest;
      el('summary-dest').title = dest;
    }
    if (el('summary-items')) el('summary-items').textContent = media && media.is_playlist ? (el('max-items').value || '50') + ' 个以内' : '当前条目';
    if (el('summary-subs')) el('summary-subs').textContent = subs.length ? subs.join('、') : '无';
    const summary = el('summary-media');
    if (summary) {
      if (media && (media.title || media.webpage_url)) {
        summary.innerHTML = '<strong title="' + escapeHtml(media.title || '') + '">' + escapeHtml(media.title || '未命名媒体') + '</strong><span title="' + escapeHtml(media.webpage_url || el('url').value || '') + '">' + escapeHtml(media.uploader || media.webpage_url || '已解析') + '</span>';
      } else {
        summary.textContent = '尚未解析媒体链接';
      }
    }
    const hint = el('start-hint');
    if (hint) {
      const runtimeReady = !!state.runtime && (state.runtime.state === 'ready' || state.runtime.state === 'outdated');
      if (!runtimeReady) hint.textContent = '下载运行时尚未就绪，请先在“运行时与诊断”中检查或安装。';
      else if (!media) hint.textContent = '先粘贴链接并解析，确认保存位置后即可开始下载。';
      else if (state.runtime && state.runtime.downloads_writable === false) hint.textContent = '系统下载目录当前不可写，任务会自动回退到插件下载目录。';
      else hint.textContent = media.is_playlist ? '将按播放列表创建受控任务，超出条目上限的部分不会下载。' : '确认保存位置后即可开始下载。';
    }
    if (el('overview-dest')) {
      el('overview-dest').textContent = dest;
      el('overview-dest').title = dest;
    }
    updateStartState();
  }

  function updateStartState() {
    const runtimeReady = !!state.runtime && (state.runtime.state === 'ready' || state.runtime.state === 'outdated');
    const ready = !!state.media && runtimeReady;
    if (el('start-button')) el('start-button').disabled = !ready;
  }

  async function pasteUrl() {
    try {
      if (!navigator.clipboard || !navigator.clipboard.readText) throw new Error('剪贴板不可用');
      const text = await navigator.clipboard.readText();
      if (!text.trim()) {
        toast('剪贴板中没有文本。', 'warn');
        return;
      }
      el('url').value = text.trim();
      el('url').focus();
      toast('已粘贴链接，点击“解析”继续。', 'success');
    } catch (error) {
      toast('读取剪贴板失败，请手动粘贴链接。', 'warn');
    }
  }

  function resetComposer() {
    state.probeToken += 1;
    state.media = null;
    state.mediaInput = '';
    el('url').value = '';
    el('playlist').checked = false;
    el('audio-only').checked = false;
    el('overwrite').checked = false;
    el('quality').value = state.settings.defaultQuality;
    el('dest-dir').value = defaultDestination();
    el('max-items').value = String(state.settings.defaultMaxItems);
    el('max-bytes').value = String(state.settings.defaultMaxBytes);
    el('format').innerHTML = '<option value="">自动选择</option>';
    el('subtitles').innerHTML = '<span class="chip muted-chip">暂无字幕</span>';
    el('info-card').hidden = true;
    el('media-entries-wrap').hidden = true;
    el('media-entries').innerHTML = '';
    el('probe-state').hidden = true;
    showComposerOptions(false);
    setComposerStep(1);
    setComposerBadge('', 'neutral');
    updateDownloadSummary();
    updateStartState();
  }

  function collectDownloadInput() {
    return {
      workspace_root: state.workspaceRoot,
      url: el('url').value.trim(),
      dest_dir: el('dest-dir').value.trim() || defaultDestination(),
      format_id: el('format').value,
      quality: el('quality').value,
      audio_only: el('audio-only').checked,
      subtitles: selectedSubtitles(),
      playlist: el('playlist').checked,
      max_items: Number(el('max-items').value) || state.settings.defaultMaxItems,
      max_bytes: Number(el('max-bytes').value) || state.settings.defaultMaxBytes,
      overwrite: el('overwrite').checked,
      title: state.media && state.media.title ? state.media.title : '',
      thumbnail: state.media && state.media.thumbnail ? state.media.thumbnail : '',
      extractor: state.media && state.media.extractor ? state.media.extractor : '',
      duration_seconds: state.media && state.media.duration_seconds ? state.media.duration_seconds : 0,
    };
  }

  async function startDownload() {
    if (!state.media) {
      toast('请先解析链接，确认媒体信息。', 'warn');
      return;
    }
    const button = el('start-button');
    button.disabled = true;
    try {
      const input = collectDownloadInput();
      const result = await bridgeInvoke('media.downloader.job.start', input);
      const actualDest = result && result.dest_dir ? String(result.dest_dir) : input.dest_dir;
      const fallback = normalizePath(actualDest) !== normalizePath(input.dest_dir);
      state.filters.status = 'all';
      state.page = 1;
      resetSelection();
      resetComposer();
      setComposerBadge(fallback ? '已切换保存位置' : '已加入下载队列', fallback ? 'queued' : 'completed');
      if (fallback) {
        toast('系统下载目录不可写，已切换到：' + actualDest, 'warn', 9000);
      } else {
        toast('已创建下载任务：' + (result && result.job_id ? String(result.job_id).slice(0, 8) : ''), 'success');
      }
      syncStatusFilterUI();
      navigate('tasks', { refresh: true });
      await loadActiveJobs();
      await loadRecentJobs();
    } catch (error) {
      setComposerBadge('下载失败', 'failed');
      setProbeState('error', '创建下载任务失败：' + describe(error));
      toast('创建下载任务失败：' + describe(error), 'error', 7000);
    } finally {
      updateStartState();
    }
  }

  function statusesFor(filter) {
    if (filter === 'all') return [];
    if (filter === 'active') return ['queued', 'running', 'canceling'];
    return [filter];
  }

  function sinceForRange(range) {
    if (!range || range === 'all') return '';
    const now = new Date();
    if (range === 'today') {
      now.setHours(0, 0, 0, 0);
      return now.toISOString();
    }
    const days = range === '7d' ? 7 : range === '30d' ? 30 : 0;
    if (!days) return '';
    return new Date(Date.now() - days * 86400 * 1000).toISOString();
  }

  function buildListInput(page) {
    return {
      limit: state.pageSize,
      offset: Math.max(0, (page - 1) * state.pageSize),
      query: state.filters.query,
      statuses: statusesFor(state.filters.status),
      since: sinceForRange(state.filters.range),
      sort: state.filters.sort,
    };
  }

  async function loadJobs(options) {
    const silent = !!(options && options.silent);
    const seq = ++state.jobsRequestSeq;
    state.loadingJobs = true;
    const table = qs('.data-table');
    if (table) table.setAttribute('aria-busy', 'true');
    if (!state.jobsLoaded) renderTaskSkeleton();
    try {
      const result = await bridgeInvoke('media.downloader.job.list', buildListInput(state.page));
      if (seq !== state.jobsRequestSeq) return;
      const items = Array.isArray(result.items) ? result.items : [];
      state.jobs = items;
      state.total = Number(result.total || 0);
      state.summary = result.summary || state.summary;
      state.jobsLoaded = true;
      state.lastJobsLoadedAt = Date.now();
      const visible = new Set(items.map((job) => job.job_id));
      Array.from(state.selected).forEach((id) => {
        if (!visible.has(id)) state.selected.delete(id);
      });
      const maxPage = Math.max(1, Math.ceil(state.total / state.pageSize));
      if (state.page > maxPage) {
        state.page = maxPage;
        state.loadingJobs = false;
        return loadJobs(options);
      }
      renderSummary();
      renderTasks();
      updateOverview();
      syncDrawerJob();
      ensurePolling();
    } catch (error) {
      if (seq !== state.jobsRequestSeq) return;
      if (!silent) toast('读取任务失败：' + describe(error), 'error');
      if (!state.jobsLoaded) {
        state.jobsLoaded = true;
        renderTasks();
      }
    } finally {
      if (seq === state.jobsRequestSeq) {
        state.loadingJobs = false;
        if (table) table.setAttribute('aria-busy', 'false');
      }
    }
  }

  async function loadActiveJobs() {
    if (state.loadingJobs && !state.jobsLoaded) return;
    try {
      const result = await bridgeInvoke('media.downloader.job.list', {
        limit: 6,
        offset: 0,
        statuses: ['queued', 'running', 'canceling'],
        sort: 'created_desc',
      });
      state.activeJobs = Array.isArray(result.items) ? result.items : [];
      renderOverviewQueue();
    } catch (_) {
      // 概览数据失败不覆盖主表错误提示。
    }
  }

  async function loadRecentJobs() {
    try {
      const result = await bridgeInvoke('media.downloader.job.list', {
        limit: 4,
        offset: 0,
        statuses: ['completed'],
        sort: 'created_desc',
      });
      state.recentJobs = Array.isArray(result.items) ? result.items : [];
      renderOverviewRecent();
    } catch (_) {
      // 概览数据失败不覆盖主表错误提示。
    }
  }

  function renderSummary() {
    const summary = state.summary || { total: 0, active: 0, completed: 0, failed: 0, canceled: 0, output_bytes: 0 };
    if (el('stat-active')) el('stat-active').textContent = String(summary.active || 0);
    if (el('stat-completed')) el('stat-completed').textContent = String(summary.completed || 0);
    if (el('stat-failed')) el('stat-failed').textContent = String(summary.failed || 0);
    if (el('stat-output')) el('stat-output').textContent = formatBytes(summary.output_bytes || 0);
    if (el('count-all')) el('count-all').textContent = String(summary.total || 0);
    if (el('count-active')) el('count-active').textContent = String(summary.active || 0);
    if (el('count-completed')) el('count-completed').textContent = String(summary.completed || 0);
    if (el('count-failed')) el('count-failed').textContent = String(summary.failed || 0);
    if (el('count-canceled')) el('count-canceled').textContent = String(summary.canceled || 0);
    if (el('nav-active-count')) {
      el('nav-active-count').hidden = !(summary.active > 0);
      el('nav-active-count').textContent = String(summary.active || 0);
    }
    const healthBadge = el('tasks-health-badge');
    if (healthBadge) {
      if (summary.active > 0) {
        healthBadge.hidden = false;
        healthBadge.className = 'status-badge running';
        healthBadge.textContent = '下载中 ' + summary.active;
      } else if (summary.failed > 0) {
        healthBadge.hidden = false;
        healthBadge.className = 'status-badge failed';
        healthBadge.textContent = '失败 ' + summary.failed;
      } else if (summary.completed > 0) {
        healthBadge.hidden = false;
        healthBadge.className = 'status-badge completed';
        healthBadge.textContent = '已完成 ' + summary.completed;
      } else {
        healthBadge.hidden = true;
        healthBadge.className = 'status-badge neutral';
        healthBadge.textContent = '';
      }
    }
    if (el('overview-records')) el('overview-records').textContent = String(summary.total || 0) + ' 条';
    if (el('stat-completed-hint')) el('stat-completed-hint').textContent = '累计完成记录';
    if (el('tasks-subtitle')) {
      const filterText = state.filters.query ? ' · 搜索“' + state.filters.query + '”' : '';
      el('tasks-subtitle').textContent = '共 ' + (state.total || 0) + ' 条下载记录' + filterText + ' · ' + (summary.active || 0) + ' 个下载中';
    }
  }

  function syncStatusFilterUI() {
    qsa('#status-filters [data-status]').forEach((button) => {
      const active = button.getAttribute('data-status') === state.filters.status;
      button.classList.toggle('active', active);
      button.setAttribute('aria-selected', active ? 'true' : 'false');
    });
  }

  function safeThumbnail(value) {
    if (!value) return '';
    try {
      const parsed = new URL(value);
      if (parsed.protocol === 'http:' || parsed.protocol === 'https:') return value;
    } catch (_) {}
    return '';
  }

  function renderTaskSkeleton() {
    const body = el('tasks-body');
    if (!body) return;
    const rows = [];
    for (let index = 0; index < 6; index += 1) {
      rows.push('<tr class="skeleton-row"><td class="col-check"><span class="skeleton-box"></span></td><td><div class="skeleton-line wide"></div><div class="skeleton-line short"></div></td><td><span class="skeleton-line" style="width:56px"></span></td><td><span class="skeleton-line" style="width:100px"></span></td><td><span class="skeleton-line" style="width:70px"></span></td><td><span class="skeleton-line" style="width:80px"></span></td><td><span class="skeleton-line" style="width:70px"></span></td><td></td></tr>');
    }
    body.innerHTML = rows.join('');
    if (el('tasks-empty')) el('tasks-empty').hidden = true;
  }

  function renderTasks() {
    const body = el('tasks-body');
    if (!body) return;
    const active = document.activeElement;
    const focusSelector = active && active.closest && active.closest('#tasks-body')
      ? (active.getAttribute('data-select')
        ? '[data-select="' + active.getAttribute('data-select') + '"]'
        : active.getAttribute('data-act') && active.getAttribute('data-id')
          ? '[data-act="' + active.getAttribute('data-act') + '"][data-id="' + active.getAttribute('data-id') + '"]'
          : '')
      : '';
    if (!state.jobsLoaded) {
      renderTaskSkeleton();
      return;
    }
    if (!state.jobs.length) {
      body.innerHTML = '';
      if (el('tasks-empty')) el('tasks-empty').hidden = false;
      renderBulkBar();
      renderPager();
      updateSelectAllState();
      return;
    }
    if (el('tasks-empty')) el('tasks-empty').hidden = true;
    body.innerHTML = state.jobs.map((job) => {
      const id = job.job_id;
      const progressValue = job.status === 'completed' ? 100 : clamp(Number(job.progress || 0), 0, 100);
      const size = Number(job.output_bytes || job.total_bytes || job.downloaded_bytes || 0);
      const thumb = safeThumbnail(job.thumbnail);
      const progressClass = job.status === 'completed' ? 'completed' : job.status === 'failed' ? 'failed' : job.status === 'canceled' ? 'canceled' : '';
      const sub = [job.extractor, formatRelative(job.created_at)].filter(Boolean).join(' · ') || formatRelative(job.created_at);
      let detail = '已保存';
      let detailClass = 'progress-meta';
      if (job.status === 'running') {
        detail = [job.speed || '', job.eta ? '剩余 ' + job.eta : ''].filter(Boolean).join(' · ') || '正在下载';
      } else if (job.status === 'queued') {
        detail = '等待开始';
      } else if (job.status === 'canceling') {
        detail = '正在停止';
      } else if (job.status === 'failed') {
        detail = job.error || '下载失败';
        detailClass = 'progress-meta is-error';
      } else if (job.status === 'canceled') {
        detail = '已取消';
      }
      const actions = [];
      actions.push('<button class="icon-btn small" type="button" data-act="detail" data-id="' + escapeHtml(id) + '" title="查看详情" aria-label="查看详情">' + icon('info') + '</button>');
      if (job.dest_dir) {
        actions.push('<button class="icon-btn small" type="button" data-act="reveal" data-id="' + escapeHtml(id) + '" title="打开下载文件夹：' + escapeHtml(job.dest_dir) + '" aria-label="打开下载文件夹">' + icon('folder') + '</button>');
      }
      if (job.status === 'queued' || job.status === 'running' || job.status === 'canceling') {
        actions.push('<button class="icon-btn small" type="button" data-act="cancel" data-id="' + escapeHtml(id) + '" title="取消任务" aria-label="取消任务">' + icon('x') + '</button>');
      } else {
        actions.push('<button class="icon-btn small" type="button" data-act="retry" data-id="' + escapeHtml(id) + '" title="重试任务" aria-label="重试任务">' + icon('retry') + '</button>');
        actions.push('<button class="icon-btn small danger" type="button" data-act="delete" data-id="' + escapeHtml(id) + '" title="删除记录" aria-label="删除记录">' + icon('trash') + '</button>');
      }
      return '<tr data-row-id="' + escapeHtml(id) + '" class="' + (state.selected.has(id) ? 'selected' : '') + '">' +
        '<td class="col-check"><input type="checkbox" data-select="' + escapeHtml(id) + '" ' + (state.selected.has(id) ? 'checked' : '') + ' aria-label="选择任务"></td>' +
        '<td class="col-task"><div class="task-cell"><span class="task-thumb">' + (thumb ? '<img alt="" src="' + escapeHtml(thumb) + '" referrerpolicy="no-referrer" data-thumb>' : icon('file')) + '</span><span class="task-copy"><button type="button" class="task-title" data-act="detail" data-id="' + escapeHtml(id) + '" title="' + escapeHtml(jobTitle(job)) + '">' + escapeHtml(jobTitle(job)) + '</button><span class="task-sub" title="' + escapeHtml(job.url || '') + '">' + escapeHtml(sub) + '</span></span></div></td>' +
        '<td class="col-progress"><div class="status-line">' + statusBadge(job.status) + '<span class="progress-value">' + progressValue.toFixed(progressValue < 10 ? 1 : 0) + '%</span><span class="progress-size">' + escapeHtml(formatBytes(size)) + '</span></div><div class="progress-track"><span class="' + progressClass + '" style="width:' + progressValue + '%"></span></div><div class="' + detailClass + '" title="' + escapeHtml(detail) + '">' + escapeHtml(detail) + '</div></td>' +
        '<td class="col-actions"><div class="row-actions">' + actions.join('') + '</div></td>' +
        '</tr>';
    }).join('');
    qsa('img[data-thumb]', body).forEach((image) => {
      image.addEventListener('error', () => {
        const host = image.closest('.task-thumb');
        if (host) host.innerHTML = icon('file');
      }, { once: true });
    });
    renderBulkBar();
    renderPager();
    updateSelectAllState();
    if (focusSelector) {
      const next = qs(focusSelector, body);
      if (next && typeof next.focus === 'function') next.focus({ preventScroll: true });
    }
  }

  function renderBulkBar() {
    const bar = el('bulk-bar');
    if (!bar) return;
    const selectedJobs = state.jobs.filter((job) => state.selected.has(job.job_id));
    if (!selectedJobs.length) {
      bar.hidden = true;
      return;
    }
    bar.hidden = false;
    if (el('bulk-count')) el('bulk-count').textContent = '已选 ' + selectedJobs.length + ' 项';
    const active = selectedJobs.some((job) => ['queued', 'running', 'canceling'].indexOf(job.status) >= 0);
    const terminal = selectedJobs.some((job) => ['completed', 'failed', 'canceled'].indexOf(job.status) >= 0);
    const cancelButton = qs('[data-bulk="cancel"]', bar);
    const retryButton = qs('[data-bulk="retry"]', bar);
    const removeButton = qs('[data-bulk="remove"]', bar);
    if (cancelButton) cancelButton.disabled = !active;
    if (retryButton) retryButton.disabled = !terminal;
    if (removeButton) removeButton.disabled = !terminal;
  }

  function renderPager() {
    const maxPage = Math.max(1, Math.ceil(state.total / state.pageSize));
    if (el('page-number')) el('page-number').textContent = state.page + ' / ' + maxPage;
    if (el('prev-page')) el('prev-page').disabled = state.page <= 1;
    if (el('next-page')) el('next-page').disabled = state.page >= maxPage;
    if (el('page-info')) {
      if (!state.total) {
        el('page-info').textContent = '没有匹配的记录';
      } else {
        const start = (state.page - 1) * state.pageSize + 1;
        const end = Math.min(state.total, state.page * state.pageSize);
        el('page-info').textContent = '显示 ' + start + '–' + end + ' / 共 ' + state.total + ' 条';
      }
    }
    if (el('page-size')) el('page-size').value = String(state.pageSize);
  }

  function renderOverviewQueue() {
    const host = el('overview-active');
    const empty = el('overview-active-empty');
    if (!host) return;
    if (!state.activeJobs.length) {
      host.innerHTML = '';
      if (empty) empty.hidden = false;
      return;
    }
    if (empty) empty.hidden = true;
    host.innerHTML = state.activeJobs.map((job) => {
      const progress = job.status === 'completed' ? 100 : clamp(Number(job.progress || 0), 0, 100);
      const detail = [job.speed || '', job.eta ? '剩余 ' + job.eta : '', displayDest(job)].filter(Boolean).join(' · ');
      return '<article class="queue-item" data-id="' + escapeHtml(job.job_id) + '">' +
        '<div class="queue-top"><div class="queue-title"><strong title="' + escapeHtml(jobTitle(job)) + '">' + escapeHtml(jobTitle(job)) + '</strong><span title="' + escapeHtml(job.url || '') + '">' + escapeHtml(job.url || '') + '</span></div>' + statusBadge(job.status) + '</div>' +
        '<div class="progress-track" style="margin-top:10px"><span style="width:' + progress + '%"></span></div>' +
        '<div class="queue-metrics"><span><b>' + progress.toFixed(progress < 10 ? 1 : 0) + '%</b> · ' + escapeHtml(formatBytes(job.downloaded_bytes || 0)) + (job.total_bytes ? ' / ' + escapeHtml(formatBytes(job.total_bytes)) : '') + '</span><span>' + escapeHtml(detail || '等待下载器更新') + '</span></div>' +
        '<div class="queue-actions"><button class="link-btn" type="button" data-overview-act="detail" data-id="' + escapeHtml(job.job_id) + '">详情</button><button class="link-btn" type="button" data-overview-act="cancel" data-id="' + escapeHtml(job.job_id) + '">取消</button></div>' +
        '</article>';
    }).join('');
  }

  function renderOverviewRecent() {
    const host = el('overview-recent');
    if (!host) return;
    if (!state.recentJobs.length) {
      host.innerHTML = '<div class="empty compact" style="grid-column:1/-1"><span class="empty-icon">' + icon('file') + '</span><strong>还没有已完成的任务</strong><p>完成第一个下载后，这里会展示最近落盘的媒体。</p></div>';
      return;
    }
    host.innerHTML = state.recentJobs.map((job) => {
      const size = Number(job.output_bytes || job.total_bytes || 0);
      return '<button class="recent-item" type="button" data-recent-id="' + escapeHtml(job.job_id) + '" style="text-align:left;cursor:pointer">' +
        '<span class="recent-icon">' + icon('check') + '</span>' +
        '<span class="recent-copy"><strong title="' + escapeHtml(jobTitle(job)) + '">' + escapeHtml(jobTitle(job)) + '</strong><span>' + escapeHtml(formatBytes(size) + ' · ' + formatRelative(job.finished_at || job.created_at) + ' · ' + displayDest(job)) + '</span></span>' +
        '</button>';
    }).join('');
  }

  function updateOverview() {
    const summary = state.summary || { total: 0, active: 0, completed: 0, failed: 0, output_bytes: 0 };
    if (el('stat-active')) el('stat-active').textContent = String(summary.active || 0);
    if (el('stat-completed')) el('stat-completed').textContent = String(summary.completed || 0);
    if (el('stat-failed')) el('stat-failed').textContent = String(summary.failed || 0);
    if (el('stat-output')) el('stat-output').textContent = formatBytes(summary.output_bytes || 0);
    if (el('overview-records')) el('overview-records').textContent = String(summary.total || 0) + ' 条';
    if (el('overview-workspace')) {
      el('overview-workspace').textContent = state.workspaceRoot || '未绑定';
      el('overview-workspace').title = state.workspaceRoot || '未绑定工作区';
    }
    if (el('overview-dest')) el('overview-dest').textContent = el('dest-dir') ? el('dest-dir').value.trim() || 'media' : state.settings.defaultDest;
    if (el('nav-active-count')) {
      el('nav-active-count').hidden = !(summary.active > 0);
      el('nav-active-count').textContent = String(summary.active || 0);
    }
  }

  function ensurePolling() {
    const active = !!(state.summary && state.summary.active > 0);
    if (!active) {
      stopPolling();
      return;
    }
    if (state.timer) return;
    const interval = Number(state.settings.pollMs) || 2500;
    state.timer = window.setInterval(() => {
      if (!document.hidden) void refreshLive();
    }, interval);
  }

  function stopPolling() {
    if (state.timer) {
      window.clearInterval(state.timer);
      state.timer = null;
    }
  }

  async function refreshLive() {
    if (state.loadingJobs) return;
    await loadJobs({ silent: true });
    if (state.currentView === 'overview') {
      await Promise.all([loadActiveJobs(), loadRecentJobs()]);
    }
    syncDrawerJob();
  }

  async function refreshAll(button) {
    if (button) button.disabled = true;
    try {
      await Promise.all([
        loadRuntime({ silent: true }),
        loadJobs({ silent: false }),
        loadActiveJobs(),
        loadRecentJobs(),
      ]);
      toast('数据已刷新。', 'success');
    } finally {
      if (button) button.disabled = false;
    }
  }

  function findJob(jobId) {
    const pools = [state.jobs, state.activeJobs, state.recentJobs];
    for (const pool of pools) {
      const found = pool.find((job) => job.job_id === jobId);
      if (found) return found;
    }
    if (state.drawerJob && state.drawerJob.job_id === jobId) return state.drawerJob;
    return null;
  }

  function revealJobFolder(jobId) {
    const job = findJob(jobId);
    if (!job) {
      toast('任务记录已更新，请刷新后重试。', 'warn');
      return;
    }
    void openFolder(jobFolder(job));
  }

  function syncDrawerJob() {
    if (!state.drawerJobId) return;
    const job = findJob(state.drawerJobId);
    if (!job) return;
    const previous = state.drawerJob;
    state.drawerJob = job;
    if (!previous || previous.status !== job.status) {
      renderDrawer(job);
      return;
    }
    const progressValue = job.status === 'completed' ? 100 : clamp(Number(job.progress || 0), 0, 100);
    const size = Number(job.output_bytes || job.total_bytes || job.downloaded_bytes || 0);
    const valueNode = el('drawer-progress-value');
    const sizeNode = el('drawer-progress-size');
    const bar = el('drawer-progress-bar');
    const meta = el('drawer-progress-meta');
    if (valueNode) valueNode.textContent = progressValue.toFixed(progressValue < 10 ? 1 : 0) + '%';
    if (sizeNode) sizeNode.textContent = formatBytes(size);
    if (bar) bar.style.width = progressValue + '%';
    if (meta) meta.textContent = [job.speed || '', job.eta ? '剩余 ' + job.eta : ''].filter(Boolean).join(' · ');
  }

  async function refreshAfterMutation() {
    await loadJobs({ silent: true });
    await Promise.all([loadActiveJobs(), loadRecentJobs()]);
  }

  async function cancelJobById(jobId, options) {
    try {
      await bridgeInvoke('media.downloader.job.cancel', { job_id: jobId });
      toast('已请求取消任务。', 'success');
      await refreshAfterMutation();
      if (state.drawerJobId === jobId) await openDrawer(jobId, { force: true });
    } catch (error) {
      toast('取消失败：' + describe(error), 'error');
    }
  }

  async function retryJobById(jobId) {
    const job = findJob(jobId);
    const ok = await askConfirm(
      '重试下载任务',
      '将沿用原任务参数创建新任务，并覆盖同名产物。原任务记录会保留。',
      '重试',
      'primary'
    );
    if (!ok) return;
    try {
      const result = await bridgeInvoke('media.downloader.job.retry', { job_id: jobId });
      toast('已创建重试任务：' + (result && result.job_id ? String(result.job_id).slice(0, 8) : ''), 'success');
      if (job) state.selected.delete(job.job_id);
      await refreshAfterMutation();
      if (state.drawerJobId === jobId) closeDrawer();
    } catch (error) {
      toast('重试失败：' + describe(error), 'error');
    }
  }

  async function deleteJobById(jobId) {
    const ok = await askConfirm(
      '删除任务记录',
      '只删除任务元数据与日志，已下载的媒体文件不会被删除。该操作不可撤销。',
      '删除记录',
      'danger'
    );
    if (!ok) return;
    try {
      await bridgeInvoke('media.downloader.job.remove', { job_id: jobId });
      toast('任务记录已删除。', 'success');
      state.selected.delete(jobId);
      if (state.drawerJobId === jobId) closeDrawer();
      await refreshAfterMutation();
    } catch (error) {
      toast('删除失败：' + describe(error), 'error');
    }
  }

  async function runWithConcurrency(items, worker, concurrency) {
    const limit = Math.max(1, Number(concurrency) || 4);
    const results = new Array(items.length);
    let cursor = 0;
    async function next() {
      while (cursor < items.length) {
        const index = cursor++;
        try {
          results[index] = { status: 'fulfilled', value: await worker(items[index], index) };
        } catch (error) {
          results[index] = { status: 'rejected', reason: error };
        }
      }
    }
    const lanes = [];
    for (let index = 0; index < Math.min(limit, items.length); index += 1) lanes.push(next());
    await Promise.all(lanes);
    return results;
  }

  async function runBulkAction(action) {
    const selectedJobs = state.jobs.filter((job) => state.selected.has(job.job_id));
    if (!selectedJobs.length) return;
    const activeJobs = selectedJobs.filter((job) => ['queued', 'running', 'canceling'].indexOf(job.status) >= 0);
    const terminalJobs = selectedJobs.filter((job) => ['completed', 'failed', 'canceled'].indexOf(job.status) >= 0);
    if (action === 'cancel') {
      if (!activeJobs.length) return;
      const ok = await askConfirm('取消所选任务', '将向 ' + activeJobs.length + ' 个进行中的任务发送取消请求。', '取消任务', 'danger');
      if (!ok) return;
      const results = await runWithConcurrency(activeJobs, (job) => bridgeInvoke('media.downloader.job.cancel', { job_id: job.job_id }), 4);
      const failed = results.filter((item) => item.status === 'rejected').length;
      toast(failed ? '部分任务取消失败：' + failed + ' 个' : '已请求取消 ' + activeJobs.length + ' 个任务。', failed ? 'warn' : 'success');
    } else if (action === 'retry') {
      if (!terminalJobs.length) return;
      const ok = await askConfirm('重试所选任务', '将为 ' + terminalJobs.length + ' 个已结束任务创建新的下载任务，并覆盖同名产物。', '重试', 'primary');
      if (!ok) return;
      const results = await runWithConcurrency(terminalJobs, (job) => bridgeInvoke('media.downloader.job.retry', { job_id: job.job_id }), 4);
      const failed = results.filter((item) => item.status === 'rejected').length;
      toast(failed ? '部分任务重试失败：' + failed + ' 个' : '已创建 ' + terminalJobs.length + ' 个重试任务。', failed ? 'warn' : 'success');
    } else if (action === 'remove') {
      if (!terminalJobs.length) return;
      const ok = await askConfirm('删除所选记录', '将删除 ' + terminalJobs.length + ' 个已结束任务的记录与日志，不会删除媒体文件。', '删除记录', 'danger');
      if (!ok) return;
      const results = await runWithConcurrency(terminalJobs, (job) => bridgeInvoke('media.downloader.job.remove', { job_id: job.job_id }), 4);
      const failed = results.filter((item) => item.status === 'rejected').length;
      toast(failed ? '部分记录删除失败：' + failed + ' 个' : '已删除 ' + terminalJobs.length + ' 条任务记录。', failed ? 'warn' : 'success');
    }
    resetSelection();
    await refreshAfterMutation();
  }

  function askConfirm(title, message, confirmLabel, tone) {
    if (state.confirmResolve) {
      state.confirmResolve(false);
      state.confirmResolve = null;
    }
    state.focusBeforeDialog = document.activeElement;
    el('confirm-title').textContent = title;
    el('confirm-message').textContent = message;
    const okButton = el('confirm-ok');
    okButton.textContent = confirmLabel || '确认';
    okButton.className = 'btn ' + (tone === 'primary' ? 'primary' : 'danger');
    el('confirm-layer').hidden = false;
    prepareOverlay();
    okButton.focus();
    return new Promise((resolve) => {
      state.confirmResolve = resolve;
    });
  }

  function resolveConfirm(value) {
    const resolve = state.confirmResolve;
    state.confirmResolve = null;
    el('confirm-layer').hidden = true;
    prepareOverlay();
    if (state.focusBeforeDialog && typeof state.focusBeforeDialog.focus === 'function') {
      try { state.focusBeforeDialog.focus(); } catch (_) {}
    }
    state.focusBeforeDialog = null;
    if (resolve) resolve(value);
  }

  async function openDrawer(jobId, options) {
    if (!jobId) return;
    if (!options || !options.force) {
      if (state.drawerJobId === jobId && state.drawerJob) return;
    }
    if (!state.drawerJobId) state.focusBeforeDialog = document.activeElement;
    state.drawerJobId = jobId;
    state.drawerLog = null;
    state.drawerLogError = '';
    el('drawer-layer').hidden = false;
    prepareOverlay();
    el('drawer-body').innerHTML = '<div class="drawer-loading"><span class="spinner" style="display:inline-block;vertical-align:middle;margin-right:7px"></span>正在读取任务详情…</div>';
    el('drawer-title').textContent = '任务详情';
    el('drawer-subtitle').textContent = jobId;
    const status = el('drawer-status');
    status.className = 'status-badge neutral';
    status.textContent = '加载中';
    let job = findJob(jobId);
    try {
      if (!job) {
        job = await bridgeInvoke('media.downloader.job.status', { job_id: jobId });
      }
      if (state.drawerJobId !== jobId) return;
      state.drawerJob = job;
      renderDrawer(job);
      el('drawer-close').focus();
      void loadDrawerLog(jobId);
    } catch (error) {
      if (state.drawerJobId !== jobId) return;
      el('drawer-body').innerHTML = '<div class="detail-error">读取任务详情失败：' + escapeHtml(describe(error)) + '</div>';
      status.className = 'status-badge failed';
      status.textContent = '读取失败';
    }
  }

  async function loadDrawerLog(jobId) {
    try {
      const result = await bridgeInvoke('media.downloader.job.log', { job_id: jobId, max_bytes: 65536 });
      if (state.drawerJobId !== jobId) return;
      state.drawerLog = result || null;
      state.drawerLogError = '';
    } catch (error) {
      if (state.drawerJobId !== jobId) return;
      state.drawerLog = null;
      state.drawerLogError = describe(error);
    }
    if (state.drawerJob && state.drawerJob.job_id === jobId) renderDrawer(state.drawerJob);
  }

  function renderDrawer(job) {
    const id = job.job_id;
    el('drawer-title').textContent = jobTitle(job);
    el('drawer-subtitle').textContent = id + (job.extractor ? ' · ' + job.extractor : '');
    const status = el('drawer-status');
    status.className = 'status-badge ' + escapeHtml(job.status || 'neutral');
    status.textContent = statusLabel(job.status);

    const progressValue = job.status === 'completed' ? 100 : clamp(Number(job.progress || 0), 0, 100);
    const size = Number(job.output_bytes || job.total_bytes || job.downloaded_bytes || 0);
    const actions = [];
    if (job.status === 'queued' || job.status === 'running' || job.status === 'canceling') {
      actions.push('<button class="btn small" type="button" data-drawer-act="cancel">' + icon('x') + '取消任务</button>');
    } else {
      actions.push('<button class="btn small primary" type="button" data-drawer-act="retry">' + icon('retry') + '重试</button>');
      actions.push('<button class="btn small danger" type="button" data-drawer-act="delete">' + icon('trash') + '删除记录</button>');
    }
    if (job.dest_dir) actions.push('<button class="btn small" type="button" data-drawer-act="reveal">' + icon('folder') + '打开下载目录</button>');
    actions.push('<button class="btn small" type="button" data-drawer-act="refresh">' + icon('refresh') + '刷新</button>');
    actions.push('<button class="btn small" type="button" data-copy="' + escapeHtml(id) + '">' + icon('copy') + '复制任务 ID</button>');
    if (job.url) actions.push('<button class="btn small" type="button" data-copy="' + escapeHtml(job.url) + '">' + icon('link') + '复制链接</button>');

    const outputs = Array.isArray(job.outputs) ? job.outputs : [];
    const outputHtml = outputs.length
      ? outputs.map((output) => '<div class="output-item">' + icon('file') + '<span class="output-path">' + escapeHtml(output) + '</span><button class="icon-btn tiny" type="button" data-folder="' + escapeHtml(pathDirectory(output)) + '" title="打开所在文件夹" aria-label="打开所在文件夹">' + icon('folder') + '</button><button class="icon-btn tiny" type="button" data-copy="' + escapeHtml(output) + '" title="复制路径" aria-label="复制路径">' + icon('copy') + '</button></div>').join('')
      : '<div class="meta">暂无产物记录。</div>';

    const logText = state.drawerLogError
      ? '读取日志失败：' + state.drawerLogError
      : state.drawerLog && state.drawerLog.tail
        ? state.drawerLog.tail
        : (job.log_path ? '日志为空或尚未生成。' : '尚未生成日志。');
    const logMeta = state.drawerLog && state.drawerLog.truncated ? '仅显示日志尾部 · 原始 ' + formatBytes(state.drawerLog.size) : (state.drawerLog && state.drawerLog.path ? state.drawerLog.path : '');
    const logHtml = '<div class="meta" style="margin-bottom:6px;overflow-wrap:anywhere">' + escapeHtml(logMeta) + '</div><pre class="log-box">' + escapeHtml(logText) + '</pre>';

    el('drawer-body').innerHTML =
      '<section class="detail-section"><div class="detail-actions">' + actions.join('') + '</div></section>' +
      '<section class="detail-section"><h3>进度</h3><div class="progress-line"><b id="drawer-progress-value">' + progressValue.toFixed(progressValue < 10 ? 1 : 0) + '%</b><span id="drawer-progress-size">' + escapeHtml(formatBytes(size)) + '</span></div><div class="progress-track detail-progress"><span id="drawer-progress-bar" class="' + (job.status === 'completed' ? 'completed' : job.status === 'failed' ? 'failed' : job.status === 'canceled' ? 'canceled' : '') + '" style="width:' + progressValue + '%"></span></div><div id="drawer-progress-meta" class="meta" style="margin-top:6px">' + escapeHtml([job.speed || '', job.eta ? '剩余 ' + job.eta : ''].filter(Boolean).join(' · ')) + '</div></section>' +
      (job.error ? '<section class="detail-section"><h3>错误</h3><div class="detail-error">' + escapeHtml(job.error) + '</div></section>' : '') +
      '<section class="detail-section"><h3>任务信息</h3><dl class="detail-grid">' +
        detailRow('链接', job.url || '—', true) +
        detailRow('工作区', job.workspace_root || '—', true) +
        detailRow('目标目录', displayDest(job), true) +
        detailRow('清晰度', job.quality || '—') +
        detailRow('格式', job.format_id || '自动') +
        detailRow('仅音频', job.audio_only ? '是' : '否') +
        detailRow('覆盖同名', job.overwrite ? '是' : '否') +
        detailRow('播放列表', job.playlist ? '是，最多 ' + (job.max_items || '—') + ' 项' : '否') +
        detailRow('字幕', Array.isArray(job.subtitles) && job.subtitles.length ? job.subtitles.join('、') : '无') +
        detailRow('大小上限', job.max_bytes ? formatBytes(job.max_bytes) : '默认') +
        detailRow('已下载', formatBytes(job.downloaded_bytes || 0)) +
        detailRow('创建时间', formatDate(job.created_at, true)) +
        detailRow('开始时间', job.started_at ? formatDate(job.started_at, true) : '—') +
        detailRow('结束时间', job.finished_at ? formatDate(job.finished_at, true) : '—') +
        detailRow('yt-dlp', job.ytdlp_version || '—') +
        detailRow('ffmpeg', job.ffmpeg_path || '未检测到') +
      '</dl></section>' +
      '<section class="detail-section"><h3>产物 · ' + outputs.length + '</h3><div class="output-list">' + outputHtml + '</div></section>' +
      '<section class="detail-section"><h3>日志</h3>' + logHtml + '</section>';
  }

  function detailRow(label, value, mono) {
    return '<div class="detail-row"><dt>' + escapeHtml(label) + '</dt><dd class="' + (mono ? 'mono' : '') + '" title="' + escapeHtml(value) + '">' + escapeHtml(value) + '</dd></div>';
  }

  function closeDrawer() {
    if (el('drawer-layer')) el('drawer-layer').hidden = true;
    prepareOverlay();
    state.drawerJobId = null;
    state.drawerJob = null;
    state.drawerLog = null;
    state.drawerLogError = '';
    if (state.focusBeforeDialog && typeof state.focusBeforeDialog.focus === 'function') {
      try { state.focusBeforeDialog.focus(); } catch (_) {}
    }
    state.focusBeforeDialog = null;
  }

  function csvCell(value) {
    let text = String(value == null ? '' : value);
    if (/^[=+\-@]/.test(text)) text = "'" + text;
    return '"' + text.replace(/"/g, '""') + '"';
  }

  function recordsToCsv(records) {
    const headers = ['job_id', 'status', 'title', 'url', 'quality', 'format_id', 'audio_only', 'playlist', 'dest_dir', 'created_at', 'started_at', 'finished_at', 'progress', 'output_bytes', 'speed', 'error', 'outputs', 'extractor', 'duration_seconds', 'ytdlp_version'];
    const lines = [headers.map(csvCell).join(',')];
    records.forEach((job) => {
      lines.push([
        job.job_id,
        job.status,
        job.title,
        job.url,
        job.quality,
        job.format_id,
        job.audio_only ? 'true' : 'false',
        job.playlist ? 'true' : 'false',
        job.dest_dir,
        job.created_at,
        job.started_at,
        job.finished_at,
        job.progress,
        job.output_bytes || job.total_bytes || 0,
        job.speed,
        job.error,
        (job.outputs || []).join(' | '),
        job.extractor,
        job.duration_seconds,
        job.ytdlp_version,
      ].map(csvCell).join(','));
    });
    return '\ufeff' + lines.join('\r\n');
  }

  function downloadText(filename, content, mime) {
    const blob = new Blob([content], { type: mime || 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = filename;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 2000);
  }

  async function fetchAllRecords(input, maximum) {
    const all = [];
    let offset = 0;
    let total = 0;
    const cap = maximum || 10000;
    while (all.length < cap) {
      const page = await bridgeInvoke('media.downloader.job.list', Object.assign({}, input, { limit: 200, offset: offset }));
      const items = Array.isArray(page.items) ? page.items : [];
      total = Number(page.total || total || 0);
      all.push.apply(all, items);
      if (!items.length) break;
      if (all.length >= total) break;
      offset += items.length;
    }
    return { records: all.slice(0, cap), total };
  }

  async function exportRecords(allRecords) {
    try {
      let result;
      if (allRecords) {
        result = await fetchAllRecords({ sort: state.filters.sort }, 10000);
      } else {
        result = await fetchAllRecords(buildListInput(1), 10000);
      }
      const records = result.records;
      if (!records.length) {
        toast('没有可导出的任务记录。', 'warn');
        return;
      }
      const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-');
      downloadText('media-download-records-' + stamp + '.csv', recordsToCsv(records), 'text/csv;charset=utf-8');
      if (result.total > records.length) {
        toast('已导出前 ' + records.length + ' 条，仍有 ' + (result.total - records.length) + ' 条记录；请缩小筛选范围后分批导出。', 'warn', 7000);
      } else {
        toast('已导出 ' + records.length + ' 条任务记录。', 'success');
      }
    } catch (error) {
      toast('导出失败：' + describe(error), 'error');
    }
  }

  async function cleanFinishedRecords(button) {
    const statuses = ['completed', 'failed', 'canceled'];
    const label = button ? button.querySelector('span') : null;
    let dryRun;
    try {
      dryRun = await bridgeInvoke('media.downloader.job.cleanup', {
        statuses: statuses,
        dry_run: true,
      });
    } catch (error) {
      toast('读取可清理记录失败：' + describe(error), 'error');
      return;
    }
    const matched = Number(dryRun && dryRun.matched ? dryRun.matched : 0);
    if (!matched) {
      toast('没有需要清理的已结束记录。', 'success');
      return;
    }
    const ok = await askConfirm(
      '清理已结束记录',
      '将删除 ' + matched + ' 条已完成、失败和已取消任务的记录与日志，但不会删除工作区内的媒体文件。该操作不可撤销。',
      '清理 ' + matched + ' 条',
      'danger'
    );
    if (!ok) return;
    if (button) button.disabled = true;
    let removed = 0;
    let failed = 0;
    try {
      for (;;) {
        const result = await bridgeInvoke('media.downloader.job.cleanup', {
          statuses: statuses,
          limit: 200,
        });
        const batchRemoved = Number(result && result.removed ? result.removed : 0);
        removed += batchRemoved;
        failed += Number(result && result.failed ? result.failed : 0);
        if (label) label.textContent = '正在清理 ' + removed + ' / ' + matched;
        if (!batchRemoved || Number(result && result.remaining ? result.remaining : 0) <= 0) break;
      }
      toast(failed ? '已清理 ' + removed + ' 条记录，' + failed + ' 条失败。' : '已清理 ' + removed + ' 条已结束记录。', failed ? 'warn' : 'success');
      resetSelection();
      await refreshAfterMutation();
    } catch (error) {
      toast('清理失败：' + describe(error), 'error');
    } finally {
      if (button) {
        button.disabled = false;
        if (label) label.textContent = '清理已结束记录';
      }
    }
  }

  function saveSettingsFromForm() {
    state.settings = {
      defaultDest: el('setting-dest').value.trim(),
      defaultQuality: el('setting-quality').value,
      defaultMaxBytes: Number(el('setting-max-bytes').value) || DEFAULT_SETTINGS.defaultMaxBytes,
      defaultMaxItems: clamp(Number(el('setting-max-items').value) || DEFAULT_SETTINGS.defaultMaxItems, 1, 200),
      pageSize: Number(el('setting-page-size').value) || DEFAULT_SETTINGS.pageSize,
      pollMs: Number(el('setting-poll').value) || DEFAULT_SETTINGS.pollMs,
      theme: el('setting-theme').value,
      density: el('setting-density').value,
    };
    saveSettings();
    applySettings();
    stopPolling();
    state.page = 1;
    void loadJobs({ silent: true });
    toast('设置已保存，并应用到当前视图。', 'success');
  }

  async function resetSettings(button) {
    const ok = await askConfirm('恢复默认设置', '界面偏好与下载默认值将恢复为初始状态，不会影响任务记录。', '恢复默认', 'primary');
    if (!ok) return;
    state.settings = Object.assign({}, DEFAULT_SETTINGS);
    saveSettings();
    applySettings();
    stopPolling();
    state.page = 1;
    await loadJobs({ silent: true });
    toast('已恢复默认设置。', 'success');
  }

  function debounce(fn, delay) {
    let timer = null;
    return function debounced() {
      const args = arguments;
      if (timer) window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        timer = null;
        fn.apply(null, args);
      }, delay);
    };
  }

  function updateSelectAllState() {
    const boxes = qsa('[data-select]', el('tasks-body') || document);
    const checked = boxes.filter((box) => state.selected.has(box.getAttribute('data-select'))).length;
    if (!el('select-all')) return;
    el('select-all').checked = boxes.length > 0 && checked === boxes.length;
    el('select-all').indeterminate = checked > 0 && checked < boxes.length;
  }

  function applySelectionState() {
    qsa('[data-select]', el('tasks-body') || document).forEach((box) => {
      const id = box.getAttribute('data-select');
      const checked = state.selected.has(id);
      box.checked = checked;
      const row = box.closest('tr');
      if (row) row.classList.toggle('selected', checked);
    });
    updateSelectAllState();
    renderBulkBar();
  }

  function syncSearchInputs(value) {
    const inputs = [el('global-search'), el('task-search')];
    inputs.forEach((input) => {
      if (input && document.activeElement !== input && input.value !== value) input.value = value;
    });
  }

  function applyQuery(value) {
    state.filters.query = String(value || '').trim();
    state.page = 1;
    resetSelection();
    syncSearchInputs(value);
    if (state.currentView !== 'tasks') {
      navigate('tasks', { refresh: true });
    } else {
      void loadJobs({ silent: false });
    }
  }

  function applyTaskFilters() {
    state.page = 1;
    resetSelection();
    syncStatusFilterUI();
    renderPager();
    void loadJobs({ silent: false });
  }

  function handleTaskSelection(event) {
    const box = event.target.closest('[data-select]');
    if (!box) return;
    const id = box.getAttribute('data-select');
    const boxes = qsa('[data-select]', el('tasks-body') || document);
    const ids = boxes.map((node) => node.getAttribute('data-select'));
    const currentIndex = ids.indexOf(id);
    const anchorIndex = ids.indexOf(state.lastSelectedJobId);
    if (event.shiftKey && anchorIndex >= 0 && currentIndex >= 0 && anchorIndex !== currentIndex) {
      const start = Math.min(anchorIndex, currentIndex);
      const end = Math.max(anchorIndex, currentIndex);
      for (let index = start; index <= end; index += 1) {
        if (box.checked) state.selected.add(ids[index]);
        else state.selected.delete(ids[index]);
      }
    } else if (box.checked) {
      state.selected.add(id);
    } else {
      state.selected.delete(id);
    }
    state.lastSelectedJobId = id;
    applySelectionState();
  }

  function invalidateProbe(message) {
    if (!state.media && !message) return;
    state.probeToken += 1;
    state.media = null;
    state.mediaInput = '';
    if (el('info-card')) el('info-card').hidden = true;
    if (el('media-entries-wrap')) el('media-entries-wrap').hidden = true;
    if (el('media-entries')) el('media-entries').innerHTML = '';
    showComposerOptions(false);
    setComposerStep(1);
    setComposerBadge('', 'neutral');
    setProbeState(message ? 'info' : '', message || '', false);
    updateDownloadSummary();
    updateStartState();
  }

  async function copyDiagnostics() {
    const text = el('diagnostics-output') ? el('diagnostics-output').textContent : '';
    if (!text || text === '尚未获取运行时状态。') {
      toast('当前没有可复制的诊断信息，请先检查运行时。', 'warn');
      return;
    }
    const ok = await copyText(text);
    toast(ok ? '诊断信息已复制。' : '复制失败，请手动选择文本。', ok ? 'success' : 'warn');
  }

  function handleGlobalClick(event) {
    const goto = event.target.closest('[data-goto]');
    if (goto) {
      event.preventDefault();
      navigate(goto.getAttribute('data-goto'));
      return;
    }
    const folder = event.target.closest('[data-folder]');
    if (folder) {
      event.preventDefault();
      void openFolder(folder.getAttribute('data-folder'));
      return;
    }
    const copy = event.target.closest('[data-copy]');
    if (copy) {
      event.preventDefault();
      const value = copy.getAttribute('data-copy');
      copyText(value).then((ok) => toast(ok ? '已复制到剪贴板。' : '复制失败。', ok ? 'success' : 'warn'));
      return;
    }
    if (event.target.closest('[data-close-drawer]')) closeDrawer();
  }

  function handleTaskRowClick(event) {
    const button = event.target.closest('[data-act]');
    if (!button) return;
    const id = button.getAttribute('data-id');
    const action = button.getAttribute('data-act');
    if (action === 'detail') void openDrawer(id);
    else if (action === 'reveal') revealJobFolder(id);
    else if (action === 'cancel') void cancelJobById(id);
    else if (action === 'retry') void retryJobById(id);
    else if (action === 'delete') void deleteJobById(id);
  }

  function handleBulkClick(event) {
    const button = event.target.closest('[data-bulk]');
    if (!button) return;
    const action = button.getAttribute('data-bulk');
    if (action === 'clear') resetSelection();
    else void runBulkAction(action);
  }

  function handleOverviewAction(event) {
    const button = event.target.closest('[data-overview-act]');
    if (!button) return;
    const id = button.getAttribute('data-id');
    if (button.getAttribute('data-overview-act') === 'detail') void openDrawer(id);
    else void cancelJobById(id);
  }

  function handleRecentClick(event) {
    const button = event.target.closest('[data-recent-id]');
    if (button) void openDrawer(button.getAttribute('data-recent-id'));
  }

  function handleDrawerAction(event) {
    const button = event.target.closest('[data-drawer-act]');
    if (!button) return;
    const id = state.drawerJobId;
    if (!id) return;
    const action = button.getAttribute('data-drawer-act');
    if (action === 'cancel') void cancelJobById(id);
    else if (action === 'retry') void retryJobById(id);
    else if (action === 'delete') void deleteJobById(id);
    else if (action === 'reveal') revealJobFolder(id);
    else if (action === 'refresh') void openDrawer(id, { force: true });
  }

  function focusSearch() {
    if (state.currentView !== 'tasks') navigate('tasks');
    window.setTimeout(() => {
      const target = el('task-search');
      if (target && target.offsetParent !== null) {
        target.focus();
        target.select();
      } else if (el('global-search')) {
        el('global-search').focus();
      }
    }, 40);
  }

  function handleKeydown(event) {
    const modifier = event.ctrlKey || event.metaKey;
    if (modifier && String(event.key).toLowerCase() === 'k') {
      event.preventDefault();
      focusSearch();
      return;
    }
    if (event.key === 'Escape') {
      if (el('confirm-layer') && !el('confirm-layer').hidden) {
        event.preventDefault();
        resolveConfirm(false);
      } else if (el('drawer-layer') && !el('drawer-layer').hidden) {
        event.preventDefault();
        closeDrawer();
      } else if (el('sidebar') && el('sidebar').classList.contains('open')) {
        event.preventDefault();
        closeSidebar();
      }
      return;
    }
    if (el('confirm-layer') && !el('confirm-layer').hidden) {
      trapFocus(qs('.dialog', el('confirm-layer')), event);
    } else if (el('drawer-layer') && !el('drawer-layer').hidden) {
      trapFocus(el('detail-drawer'), event);
    }
  }

  function bindEvents() {
    qsa('.nav-item').forEach((button) => {
      button.addEventListener('click', () => navigate(button.getAttribute('data-view')));
    });
    document.addEventListener('click', handleGlobalClick);

    const on = (id, type, handler) => {
      const node = el(id);
      if (node) node.addEventListener(type, handler);
    };

    on('sidebar-toggle', 'click', openSidebar);
    on('sidebar-scrim', 'click', closeSidebar);
    on('refresh-all', 'click', () => refreshAll(el('refresh-all')));
    on('new-download', 'click', () => navigate('download'));

    on('side-runtime-action', 'click', (event) => loadRuntime({ button: event.currentTarget }));
    on('overview-recheck', 'click', (event) => loadRuntime({ button: event.currentTarget }));
    on('overview-install', 'click', (event) => installRuntime(event.currentTarget));
    on('runtime-recheck', 'click', (event) => loadRuntime({ button: event.currentTarget }));
    on('runtime-install-btn', 'click', (event) => installRuntime(event.currentTarget));
    on('copy-diagnostics', 'click', copyDiagnostics);

    on('paste-url', 'click', pasteUrl);
    on('probe-button', 'click', probe);
    on('start-button', 'click', startDownload);
    on('reset-composer', 'click', resetComposer);
    on('url', 'keydown', (event) => {
      if (event.key === 'Enter') {
        event.preventDefault();
        void probe();
      }
    });
    on('url', 'input', () => {
      if (state.media && state.mediaInput && el('url').value.trim() !== state.mediaInput) {
        invalidateProbe('链接已更改，请重新解析。');
      } else if (!state.media) {
        setComposerStep(1);
      }
    });
    on('playlist', 'change', () => invalidateProbe('播放列表选项已更改，请重新解析链接。'));
    on('pick-dest', 'click', () => chooseDirectory('dest-dir', false));
    on('setting-pick-dest', 'click', () => chooseDirectory('setting-dest', true));
    ['quality', 'format', 'max-items', 'max-bytes', 'dest-dir', 'audio-only'].forEach((id) => {
      on(id, 'input', updateDownloadSummary);
      on(id, 'change', updateDownloadSummary);
    });

    const searchDebounced = debounce(() => applyQuery(el('global-search') ? el('global-search').value : ''), 240);
    const taskSearchDebounced = debounce(() => applyQuery(el('task-search') ? el('task-search').value : ''), 240);
    on('global-search', 'input', searchDebounced);
    on('task-search', 'input', taskSearchDebounced);

    on('task-range', 'change', () => {
      state.filters.range = el('task-range').value;
      applyTaskFilters();
    });
    on('task-sort', 'change', () => {
      state.filters.sort = el('task-sort').value;
      applyTaskFilters();
    });
    on('status-filters', 'click', (event) => {
      const button = event.target.closest('[data-status]');
      if (!button) return;
      state.filters.status = button.getAttribute('data-status');
      applyTaskFilters();
    });
    on('tasks-refresh', 'click', (event) => {
      event.currentTarget.disabled = true;
      Promise.all([loadJobs({ silent: false }), loadActiveJobs(), loadRecentJobs()])
        .finally(() => { event.currentTarget.disabled = false; });
    });
    on('export-tasks', 'click', () => exportRecords(false));
    on('page-size', 'change', () => {
      const size = Number(el('page-size').value) || DEFAULT_SETTINGS.pageSize;
      state.pageSize = size;
      state.settings.pageSize = size;
      if (el('setting-page-size')) el('setting-page-size').value = String(size);
      saveSettings();
      state.page = 1;
      resetSelection();
      void loadJobs({ silent: false });
    });
    on('prev-page', 'click', () => {
      if (state.page <= 1) return;
      state.page -= 1;
      resetSelection();
      void loadJobs({ silent: false });
    });
    on('next-page', 'click', () => {
      const maxPage = Math.max(1, Math.ceil(state.total / state.pageSize));
      if (state.page >= maxPage) return;
      state.page += 1;
      resetSelection();
      void loadJobs({ silent: false });
    });
    on('select-all', 'change', () => {
      const checked = !!el('select-all').checked;
      state.jobs.forEach((job) => {
        if (checked) state.selected.add(job.job_id);
        else state.selected.delete(job.job_id);
      });
      state.lastSelectedJobId = '';
      applySelectionState();
    });
    on('tasks-body', 'change', handleTaskSelection);
    on('tasks-body', 'click', handleTaskRowClick);
    on('bulk-bar', 'click', handleBulkClick);
    on('overview-active', 'click', handleOverviewAction);
    on('overview-recent', 'click', handleRecentClick);

    on('drawer-close', 'click', closeDrawer);
    on('drawer-body', 'click', handleDrawerAction);
    on('confirm-cancel', 'click', () => resolveConfirm(false));
    on('confirm-ok', 'click', () => resolveConfirm(true));

    on('settings-save', 'click', saveSettingsFromForm);
    on('settings-export', 'click', () => exportRecords(true));
    on('settings-clear-finished', 'click', (event) => cleanFinishedRecords(event.currentTarget));
    on('settings-reset', 'click', (event) => resetSettings(event.currentTarget));

    document.addEventListener('keydown', handleKeydown);
    document.addEventListener('visibilitychange', () => {
      if (!document.hidden && state.jobsLoaded && !state.loadingJobs) void refreshLive();
    });
    window.addEventListener('beforeunload', stopPolling);
  }

  async function boot() {
    applySettings();
    bindEvents();
    setComposerStep(1);
    showComposerOptions(false);
    updateDownloadSummary();
    updateSelectAllState();
    if (!window.__TAURI_INTERNALS__ || typeof window.__TAURI_INTERNALS__.invoke !== 'function') {
      toast('当前不在 HiMind Agent 的扩展视图窗口中，任务与运行时数据不可用。', 'warn', 8000);
    }
    await loadWorkspace();
    await Promise.all([
      loadRuntime({ silent: true }),
      loadJobs({ silent: true }),
    ]);
    await Promise.all([loadActiveJobs(), loadRecentJobs()]);
  }

  window.addEventListener('DOMContentLoaded', () => { void boot(); });
})();
