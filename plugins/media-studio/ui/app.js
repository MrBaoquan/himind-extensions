(function () {
  'use strict';

  const STORAGE_KEY = 'himind.media-studio.ui.v1';
  const ALL_SIZES = ['1024x1024', '1280x720', '1920x1080', '1024x1365', '1200x628'];
  const TERMINAL_STATES = ['succeeded', 'partial', 'failed', 'canceled', 'interrupted'];
  const STATE_LABEL = {
    queued: '排队中',
    running: '生成中',
    succeeded: '已完成',
    partial: '部分完成',
    failed: '失败',
    canceled: '已取消',
    interrupted: '已中断'
  };

  const state = {
    context: null,
    styles: [],
    selectedStyles: new Set(),
    selectedSizes: new Set(['1024x1024']),
    job: null,
    jobs: [],
    pollTimer: 0,
    busy: false,
    lineageOpen: false,
    styleModalOpen: false,
    lastFocus: null,
    modalFocus: null,
    bridgeBroken: false
  };

  const el = (id) => document.getElementById(id);

  function bridgeInvoke(capabilityId, input) {
    const internals = window.__TAURI_INTERNALS__ || {};
    if (typeof internals.invoke !== 'function') {
      const error = new Error('当前不在 HiMind Agent 的扩展视图窗口中');
      error.code = 'bridge_unavailable';
      return Promise.reject(error);
    }
    return internals.invoke('invoke_plugin_view_capability', { capabilityId, input: input || {} });
  }

  function hostInvoke(command, args) {
    const internals = window.__TAURI_INTERNALS__ || {};
    if (typeof internals.invoke !== 'function') {
      const error = new Error('当前不在 HiMind Agent 的扩展视图窗口中');
      error.code = 'bridge_unavailable';
      return Promise.reject(error);
    }
    return internals.invoke(command, args || {});
  }

  function describe(error) {
    if (!error) return '未知错误';
    if (error.code === 'bridge_unavailable') return error.message;
    if (typeof error === 'string') return error;
    if (error.message) return error.message;
    return String(error);
  }

  function escapeHtml(value) {
    return String(value == null ? '' : value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function toFileSrc(path) {
    const internals = window.__TAURI_INTERNALS__ || {};
    if (typeof internals.convertFileSrc === 'function') {
      try { return internals.convertFileSrc(path); } catch (_) {}
    }
    const normalized = String(path || '').replace(/\\/g, '/');
    return /^[a-zA-Z]:/.test(normalized) ? 'file:///' + normalized : 'file://' + normalized;
  }

  function setFeedback(nodeId, message, tone) {
    const node = el(nodeId);
    if (!message) {
      node.hidden = true;
      node.textContent = '';
      node.className = 'inline-feedback';
      return;
    }
    node.hidden = false;
    node.textContent = message;
    node.className = 'inline-feedback' + (tone ? ' ' + tone : '');
  }

  function readPrefs() {
    try {
      const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}');
      if (Array.isArray(stored.styles)) {
        stored.styles.forEach((id) => { if (typeof id === 'string') state.selectedStyles.add(id); });
      }
      if (Array.isArray(stored.sizes) && stored.sizes.length) {
        state.selectedSizes = new Set(stored.sizes.filter((size) => ALL_SIZES.includes(size)));
      }
    } catch (_) {}
  }

  function savePrefs() {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({
        styles: Array.from(state.selectedStyles),
        sizes: Array.from(state.selectedSizes)
      }));
    } catch (_) {}
  }

  function workspaceRoot() {
    const value = state.context && state.context.workspace_root;
    return typeof value === 'string' ? value.trim() : '';
  }

  function setReadyChip(text, tone) {
    const chip = el('ready-chip');
    chip.textContent = text;
    chip.className = 'chip' + (tone ? ' ' + tone : '');
  }

  async function refreshState() {
    setReadyChip('正在检查…');
    try {
      const status = await bridgeInvoke('media.studio.runtime.status', {});
      const providers = Array.isArray(status.providers) ? status.providers : [];
      if (status.ready) {
        const ready = providers.filter((item) => item.health === 'ready').length || providers.length;
        setReadyChip('生图服务就绪 · ' + ready + ' 个', 'ready');
      } else {
        setReadyChip('缺少生图服务', 'blocked');
        setFeedback('composer-feedback', status.message || '本机没有已绑定生图能力的服务，请先在 Agent 的 AI 服务里绑定 image.generate。', 'warn');
      }
    } catch (error) {
      if (error && error.code === 'bridge_unavailable') {
        state.bridgeBroken = true;
        setReadyChip('需从 Agent 打开', 'blocked');
        setFeedback('composer-feedback', '这个视图需要在 HiMind Agent 的扩展视图窗口里打开，浏览器直接访问拿不到能力代理。', 'warn');
        return;
      }
      setReadyChip('状态不可用', 'blocked');
      setFeedback('composer-feedback', '检查就绪状态失败：' + describe(error), 'warn');
    }
  }

  async function loadStyles() {
    try {
      const payload = await bridgeInvoke('media.studio.style.list', {});
      state.styles = Array.isArray(payload.styles) ? payload.styles : [];
    } catch (error) {
      state.styles = [];
      setFeedback('composer-feedback', '读取风格包失败：' + describe(error), 'warn');
    }
    const known = new Set(state.styles.map((style) => style.id));
    Array.from(state.selectedStyles).forEach((id) => { if (!known.has(id)) state.selectedStyles.delete(id); });
    renderStyles();
    updateMatrix();
  }

  function renderStyles() {
    const host = el('styles');
    if (!state.styles.length) {
      host.innerHTML = '<span class="hint">还没有可用风格包。</span>';
      return;
    }
    host.innerHTML = state.styles.map((style) => {
      const selected = state.selectedStyles.has(style.id);
      const tag = style.builtin ? '内置' : '自定义';
      return '<button type="button" class="style-chip" data-style="' + escapeHtml(style.id) +
        '" aria-pressed="' + (selected ? 'true' : 'false') +
        '" title="' + escapeHtml(style.description || style.prompt_suffix || '') + '">' +
        escapeHtml(style.label) + '<span class="tag">' + tag + '</span></button>';
    }).join('');
  }

  function renderSizes() {
    el('sizes').innerHTML = ALL_SIZES.map((size) => {
      const selected = state.selectedSizes.has(size);
      return '<button type="button" class="size-chip" data-size="' + size + '" aria-pressed="' +
        (selected ? 'true' : 'false') + '">' + size + '</button>';
    }).join('');
  }

  function selectedVariantCount() {
    const value = Number(el('variants').value);
    return Number.isFinite(value) && value > 0 ? Math.min(4, Math.floor(value)) : 1;
  }

  function updateMatrix() {
    const styles = state.selectedStyles.size || 1;
    const sizes = state.selectedSizes.size;
    const variants = selectedVariantCount();
    const cells = styles * sizes;
    const images = cells * variants;
    const node = el('matrix-preview');
    if (!sizes) {
      node.innerHTML = '至少选一个尺寸。';
    } else {
      node.innerHTML = '本次矩阵：<strong>' + cells + '</strong> 个单元格 × <strong>' + variants +
        '</strong> 张 = <strong>' + images + '</strong> 张图片';
    }
    const blocked = !workspaceRoot() || state.bridgeBroken || images > 36 || !sizes;
    el('start-batch').disabled = blocked || state.busy;
    if (!workspaceRoot() && !state.bridgeBroken) {
      setFeedback('composer-feedback', '当前视图没有绑定工作区：产物要落在工作区里，请先在 Agent 里给这个扩展视图绑定工作区。', 'warn');
    } else if (images > 36 && !state.bridgeBroken) {
      setFeedback('composer-feedback', '单次矩阵最多 36 张：请减少风格包、尺寸或每格张数。', 'warn');
    } else if (!state.bridgeBroken) {
      setFeedback('composer-feedback', '');
    }
  }

  async function startBatch() {
    if (state.busy) return;
    const prompt = el('prompt').value.trim();
    if (!prompt) {
      setFeedback('composer-feedback', '先写一句要生成什么。', 'warn');
      el('prompt').focus();
      return;
    }
    if (!state.selectedSizes.size) {
      setFeedback('composer-feedback', '至少选一个尺寸。', 'warn');
      return;
    }
    state.busy = true;
    el('start-batch').disabled = true;
    setFeedback('composer-feedback', '');
    try {
      const payload = await bridgeInvoke('media.studio.batch.start', {
        workspace_root: workspaceRoot(),
        prompt,
        styles: Array.from(state.selectedStyles),
        sizes: Array.from(state.selectedSizes),
        variants: selectedVariantCount(),
        filename_prefix: el('filename-prefix').value.trim()
      });
      setFeedback('composer-feedback', payload.message || '已开始生成。', 'ok');
      await showJob(payload.job_id);
      await loadHistory();
    } catch (error) {
      setFeedback('composer-feedback', '开始生成失败：' + describe(error), '');
    } finally {
      state.busy = false;
      updateMatrix();
    }
  }

  function stopPolling() {
    if (state.pollTimer) {
      window.clearTimeout(state.pollTimer);
      state.pollTimer = 0;
    }
  }

  function schedulePoll(jobId) {
    stopPolling();
    state.pollTimer = window.setTimeout(async () => {
      const job = await fetchJob(jobId, true);
      if (job && !TERMINAL_STATES.includes(job.state)) {
        schedulePoll(jobId);
      }
    }, 1500);
  }

  async function fetchJob(jobId, withItems) {
    try {
      const payload = await bridgeInvoke('media.studio.batch.status', { job_id: jobId, include_items: withItems !== false });
      return payload.job || null;
    } catch (error) {
      setFeedback('composer-feedback', '查询作业失败：' + describe(error), '');
      return null;
    }
  }

  async function showJob(jobId) {
    const job = await fetchJob(jobId, true);
    if (!job) return;
    state.job = job;
    renderJob();
    if (!TERMINAL_STATES.includes(job.state)) schedulePoll(jobId);
  }

  function renderJob() {
    const job = state.job;
    const progress = el('progress');
    const cancel = el('cancel-job');
    if (!job) {
      progress.hidden = true;
      cancel.hidden = true;
      el('stage-subtitle').textContent = '还没有作业。左侧选好风格包后开始生成。';
      el('results').innerHTML = '<div class="empty-state"><strong>还没有生成结果</strong><span>选好风格包与尺寸，写下要生成什么，然后开始矩阵生成。</span></div>';
      return;
    }
    const percent = Number(job.total) > 0 ? Math.round((Number(job.completed) * 100) / Number(job.total)) : 0;
    progress.hidden = false;
    el('progress-fill').style.width = Math.max(2, percent) + '%';
    el('progress-bar').setAttribute('aria-valuenow', String(percent));
    el('progress-text').textContent = (STATE_LABEL[job.state] || job.state) + ' · 已完成 ' +
      job.completed + '/' + job.total + (job.failed ? '，失败 ' + job.failed : '') +
      (job.error ? ' · ' + job.error : '');
    cancel.hidden = TERMINAL_STATES.includes(job.state);
    el('stage-subtitle').textContent = (job.base_prompt || '') +
      ' · ' + (job.style_ids || []).map((id) => id || '无风格').join('、') +
      ' · ' + (job.sizes || []).join('、') + ' · 输出到 ' + (job.output_dir || '');
    renderResults(job);
  }

  function renderResults(job) {
    const host = el('results');
    const items = Array.isArray(job.items) ? job.items : [];
    if (!items.length) {
      host.innerHTML = '<div class="empty-state"><strong>作业已提交</strong><span>单元格还没开始执行，稍等片刻会自动刷新。</span></div>';
      return;
    }
    const cards = [];
    items.forEach((item) => {
      const files = Array.isArray(item.files) ? item.files : [];
      if (files.length) {
        files.forEach((file) => {
          cards.push('<button type="button" class="result-card" data-media="' + escapeHtml(file.media_id || '') +
            '" data-path="' + escapeHtml(file.path || '') + '"><span class="result-thumb"><img src="' +
            escapeHtml(toFileSrc(file.path)) + '" alt="' + escapeHtml(item.style_id || '生成结果') + '" loading="lazy" /></span>' +
            '<span class="result-meta"><span>' + escapeHtml(item.style_id || '无风格') + '</span><span>' +
            escapeHtml(item.size || '') + '</span><span>' + escapeHtml(file.width + '×' + file.height) + '</span></span></button>');
          return;
        });
      }
      if (item.state === 'failed') {
        cards.push('<div class="result-card"><span class="result-thumb"><span class="placeholder">生成失败</span></span>' +
          '<span class="result-failed">' + escapeHtml(item.error || '未知原因') + '</span></div>');
        return;
      }
      cards.push('<div class="result-card"><span class="result-thumb"><span class="placeholder">' +
        escapeHtml(STATE_LABEL[item.state] || item.state || '等待中') + '</span></span>' +
        '<span class="result-meta"><span>' + escapeHtml(item.style_id || '无风格') + '</span><span>' +
        escapeHtml(item.size || '') + '</span></span></div>');
    });
    host.innerHTML = cards.join('');
  }

  async function loadHistory() {
    try {
      const payload = await bridgeInvoke('media.studio.batch.list', { limit: 8 });
      state.jobs = Array.isArray(payload.jobs) ? payload.jobs : [];
    } catch (_) {
      state.jobs = [];
    }
    renderHistory();
  }

  function renderHistory() {
    const host = el('history-list');
    if (!state.jobs.length) {
      host.innerHTML = '<p class="hint">还没有历史作业。</p>';
      return;
    }
    host.innerHTML = state.jobs.map((job) => {
      const active = state.job && state.job.id === job.id;
      return '<button type="button" class="history-item" data-job="' + escapeHtml(job.id) +
        '"' + (active ? ' aria-current="true"' : '') + '><span>' + escapeHtml(job.base_prompt || job.id) +
        '</span><span>' + escapeHtml(STATE_LABEL[job.state] || job.state) + ' · ' +
        job.completed + '/' + job.total + '</span></button>';
    }).join('');
  }

  async function cancelJob() {
    if (!state.job) return;
    try {
      await bridgeInvoke('media.studio.batch.cancel', { job_id: state.job.id });
      await showJob(state.job.id);
    } catch (error) {
      setFeedback('composer-feedback', '取消失败：' + describe(error), '');
    }
  }

  function openLineage(mediaId) {
    if (!mediaId) {
      setFeedback('composer-feedback', '这条结果没有媒体 ID，无法反查血缘。', 'warn');
      return;
    }
    state.lastFocus = document.activeElement;
    state.lineageOpen = true;
    el('lineage-scrim').hidden = false;
    el('lineage').hidden = false;
    el('lineage').setAttribute('aria-hidden', 'false');
    document.body.classList.add('has-overlay');
    el('lineage-content').innerHTML = '<p class="hint">正在读取血缘…</p>';
    el('close-lineage').focus();
    bridgeInvoke('media.studio.asset.lineage', { media_id: mediaId, include_chain: true })
      .then((payload) => {
        const records = Array.isArray(payload.records) ? payload.records : [];
        if (!records.length) {
          el('lineage-content').innerHTML = '<p class="hint">' + escapeHtml(payload.message || '没有这条素材的生成记录。') + '</p>';
          return;
        }
        const record = records[0];
        const rows = [
          ['风格包', record.style_id || '无'],
          ['能力', record.capability || ''],
          ['模型', record.model || ''],
          ['服务', record.service_id || ''],
          ['尺寸', record.width + '×' + record.height],
          ['摘要', record.sha256 || ''],
          ['父素材', record.parent_media_id || '无'],
          ['时间', record.created_at || '']
        ];
        el('lineage-content').innerHTML = '<dl>' + rows.map((row) =>
          '<dt>' + escapeHtml(row[0]) + '</dt><dd>' + escapeHtml(row[1]) + '</dd>').join('') +
          '</dl><div><p class="hint">当时使用的提示词</p><pre>' + escapeHtml(record.prompt || '') + '</pre></div>';
      })
      .catch((error) => {
        el('lineage-content').innerHTML = '<p class="hint">读取血缘失败：' + escapeHtml(describe(error)) + '</p>';
      });
  }

  function closeLineage() {
    if (!state.lineageOpen) return;
    state.lineageOpen = false;
    el('lineage').hidden = true;
    el('lineage').setAttribute('aria-hidden', 'true');
    el('lineage-scrim').hidden = true;
    document.body.classList.toggle('has-overlay', state.styleModalOpen);
    if (state.lastFocus && typeof state.lastFocus.focus === 'function') state.lastFocus.focus();
  }

  function openStyleModal() {
    state.modalFocus = document.activeElement;
    state.styleModalOpen = true;
    el('style-modal').hidden = false;
    el('style-modal').setAttribute('aria-hidden', 'false');
    document.body.classList.add('has-overlay');
    setFeedback('style-feedback', '');
    el('style-id').value = '';
    el('style-label').value = '';
    el('style-suffix').value = el('prompt').value.trim();
    el('style-avoid').value = '';
    el('style-sizes').value = Array.from(state.selectedSizes).join(', ') || '1024x1024';
    el('style-id').focus();
  }

  function closeStyleModal() {
    if (!state.styleModalOpen) return;
    state.styleModalOpen = false;
    el('style-modal').hidden = true;
    el('style-modal').setAttribute('aria-hidden', 'true');
    document.body.classList.toggle('has-overlay', state.lineageOpen);
    if (state.modalFocus && typeof state.modalFocus.focus === 'function') state.modalFocus.focus();
  }

  async function saveStyle() {
    const id = el('style-id').value.trim().toLowerCase();
    const label = el('style-label').value.trim();
    const suffix = el('style-suffix').value.trim();
    if (!id || !label || !suffix) {
      setFeedback('style-feedback', '标识、显示名与风格要求都需要填写。', 'warn');
      return;
    }
    const sizes = el('style-sizes').value.split(',').map((part) => part.trim()).filter(Boolean);
    try {
      await bridgeInvoke('media.studio.style.save', {
        style: { id, label, prompt_suffix: suffix, avoid: el('style-avoid').value.trim(), sizes }
      });
      closeStyleModal();
      await loadStyles();
      state.selectedStyles.add(id);
      renderStyles();
      updateMatrix();
      savePrefs();
      setFeedback('composer-feedback', '已保存风格包：' + label, 'ok');
    } catch (error) {
      setFeedback('style-feedback', '保存失败：' + describe(error), '');
    }
  }

  function trapFocus(event, container) {
    if (event.key !== 'Tab' || !container) return;
    const focusable = container.querySelectorAll('button, input, textarea, select, a[href]');
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

  function bindEvents() {
    el('styles').addEventListener('click', (event) => {
      const button = event.target.closest('[data-style]');
      if (!button) return;
      const id = button.getAttribute('data-style');
      if (state.selectedStyles.has(id)) state.selectedStyles.delete(id);
      else state.selectedStyles.add(id);
      renderStyles();
      updateMatrix();
      savePrefs();
    });

    el('sizes').addEventListener('click', (event) => {
      const button = event.target.closest('[data-size]');
      if (!button) return;
      const size = button.getAttribute('data-size');
      if (state.selectedSizes.has(size)) state.selectedSizes.delete(size);
      else state.selectedSizes.add(size);
      renderSizes();
      updateMatrix();
      savePrefs();
    });

    el('variants').addEventListener('change', updateMatrix);
    el('clear-styles').addEventListener('click', () => {
      state.selectedStyles.clear();
      renderStyles();
      updateMatrix();
      savePrefs();
    });

    el('start-batch').addEventListener('click', startBatch);
    el('cancel-job').addEventListener('click', cancelJob);
    el('refresh-state').addEventListener('click', refreshState);
    el('reload').addEventListener('click', async () => {
      if (state.job) await showJob(state.job.id);
      await loadHistory();
    });

    el('results').addEventListener('click', (event) => {
      const card = event.target.closest('[data-media]');
      if (!card) return;
      openLineage(card.getAttribute('data-media'));
    });

    el('history-list').addEventListener('click', (event) => {
      const item = event.target.closest('[data-job]');
      if (!item) return;
      void showJob(item.getAttribute('data-job'));
    });

    el('close-lineage').addEventListener('click', closeLineage);
    el('lineage-scrim').addEventListener('click', closeLineage);
    el('open-style-form').addEventListener('click', openStyleModal);
    el('close-style').addEventListener('click', closeStyleModal);
    el('cancel-style').addEventListener('click', closeStyleModal);
    el('save-style').addEventListener('click', saveStyle);
    el('style-modal').querySelectorAll('[data-close-style]').forEach((node) => node.addEventListener('click', closeStyleModal));

    document.addEventListener('keydown', (event) => {
      if (state.styleModalOpen) {
        if (event.key === 'Escape') closeStyleModal();
        trapFocus(event, el('style-modal'));
        return;
      }
      if (state.lineageOpen) {
        if (event.key === 'Escape') closeLineage();
        trapFocus(event, el('lineage'));
      }
    });

    document.addEventListener('contextmenu', (event) => {
      if (event.target.closest('input, textarea')) return;
      event.preventDefault();
    });
  }

  async function init() {
    readPrefs();
    renderSizes();
    bindEvents();
    try {
      state.context = await hostInvoke('get_plugin_view_context', {});
    } catch (error) {
      state.context = null;
      if (error && error.code === 'bridge_unavailable') state.bridgeBroken = true;
    }
    updateMatrix();
    await loadStyles();
    await refreshState();
    await loadHistory();
    updateMatrix();
  }

  void init();
})();
