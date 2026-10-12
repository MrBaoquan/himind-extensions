(function () {
  'use strict';

  const state = {
    workspaceRoot: '',
    runtime: null,
    media: null,
    jobs: [],
    timer: null,
    busy: false,
  };

  const el = (id) => document.getElementById(id);

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

  function notify(message, tone) {
    const node = el('notice');
    if (!message) {
      node.hidden = true;
      node.textContent = '';
      node.className = 'notice';
      return;
    }
    node.hidden = false;
    node.className = 'notice' + (tone ? ' ' + tone : '');
    node.textContent = message;
  }

  function describe(error) {
    if (error && error.code === 'bridge_unavailable') return error.message;
    if (typeof error === 'string') return error;
    if (error && error.message) return error.message;
    return '未知错误';
  }

  function setBusy(busy) {
    state.busy = busy;
    for (const id of ['runtime-button', 'install-button', 'probe-button', 'start-button', 'refresh-jobs']) {
      const node = el(id);
      if (node) node.disabled = busy;
    }
  }

  function escapeHtml(value) {
    return String(value == null ? '' : value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  async function loadWorkspace() {
    try {
      const context = await window.__TAURI_INTERNALS__.invoke('get_plugin_view_context', {});
      state.workspaceRoot = context && context.workspace_root ? context.workspace_root : '';
    } catch (_) {
      state.workspaceRoot = '';
    }
  }

  async function loadRuntime() {
    setBusy(true);
    try {
      const runtime = await bridgeInvoke('media.downloader.runtime.status', {});
      state.runtime = runtime;
      renderRuntime(runtime);
    } catch (error) {
      state.runtime = null;
      el('runtime-state').textContent = '不可用';
      el('runtime-state').className = 'badge error';
      el('runtime-detail').textContent = describe(error);
      el('runtime-actions').hidden = false;
    } finally {
      setBusy(false);
    }
  }

  function renderRuntime(runtime) {
    const ready = runtime && runtime.state === 'ready';
    const stateNode = el('runtime-state');
    stateNode.textContent = ready ? '可用' : (runtime && runtime.state ? runtime.state : '不可用');
    stateNode.className = 'badge ' + (ready ? 'ok' : 'error');
    const details = [];
    if (runtime && runtime.version) details.push('yt-dlp ' + runtime.version);
    if (runtime && runtime.source) details.push('来源：' + runtime.source);
    if (runtime && runtime.ffmpeg_path) details.push('ffmpeg 可用');
    else details.push('ffmpeg 未检测到，部分格式不可用');
    if (runtime && runtime.message) details.push(runtime.message);
    el('runtime-detail').textContent = details.join('；');
    el('runtime-actions').hidden = ready;
  }

  async function installRuntime() {
    setBusy(true);
    try {
      const result = await bridgeInvoke('media.downloader.runtime.install', {});
      notify(result.message || '运行时已安装。');
      await loadRuntime();
    } catch (error) {
      notify('安装运行时失败：' + describe(error), 'error');
    } finally {
      setBusy(false);
    }
  }

  async function probe() {
    const url = el('url').value.trim();
    if (!url) {
      notify('请先输入媒体链接。', 'warn');
      return;
    }
    setBusy(true);
    notify('');
    try {
      const result = await bridgeInvoke('media.downloader.info.get', {
        url: url,
        playlist: el('playlist').checked,
      });
      state.media = result;
      renderMedia(result);
      notify('解析完成，可以开始下载。');
    } catch (error) {
      state.media = null;
      el('info-card').hidden = true;
      notify('解析失败：' + describe(error), 'error');
    } finally {
      setBusy(false);
    }
  }

  function renderMedia(media) {
    el('info-card').hidden = false;
    el('media-title').textContent = media.title || '媒体信息';
    const meta = [];
    if (media.uploader) meta.push(media.uploader);
    if (media.duration_seconds) meta.push(Math.round(media.duration_seconds) + ' 秒');
    if (media.entry_count) meta.push(media.entry_count + ' 个条目');
    el('media-meta').textContent = meta.join(' · ');

    const format = el('format');
    format.innerHTML = '<option value="">自动选择</option>';
    (media.formats || []).slice(0, 40).forEach((item) => {
      const option = document.createElement('option');
      option.value = item.format_id;
      const label = [item.format_id, item.resolution || item.note || item.ext, item.vcodec && item.vcodec !== 'none' ? item.vcodec : '']
        .filter(Boolean).join(' · ');
      option.textContent = label;
      format.appendChild(option);
    });

    const subtitles = el('subtitles');
    subtitles.innerHTML = '';
    (media.subtitles || []).forEach((lang) => {
      const label = document.createElement('label');
      const input = document.createElement('input');
      input.type = 'checkbox';
      input.value = lang;
      label.appendChild(input);
      label.appendChild(document.createTextNode(lang));
      subtitles.appendChild(label);
    });
    el('subtitle-details').hidden = !(media.subtitles || []).length;
  }

  async function startDownload() {
    if (!state.workspaceRoot) {
      notify('当前视图没有绑定工作区，无法确定落盘位置。', 'warn');
      return;
    }
    const url = el('url').value.trim();
    if (!url) {
      notify('请先输入媒体链接。', 'warn');
      return;
    }
    const subtitles = Array.from(el('subtitles').querySelectorAll('input:checked')).map((node) => node.value);
    setBusy(true);
    try {
      const result = await bridgeInvoke('media.downloader.job.start', {
        workspace_root: state.workspaceRoot,
        url: url,
        dest_dir: el('dest-dir').value.trim() || 'media',
        format_id: el('format').value,
        quality: el('quality').value,
        audio_only: el('audio-only').checked,
        subtitles: subtitles,
        playlist: el('playlist').checked,
        max_items: 50,
        max_bytes: 2 * 1024 * 1024 * 1024,
      });
      notify('已创建下载任务：' + result.job_id);
      await loadJobs();
      startPolling();
    } catch (error) {
      notify('创建下载任务失败：' + describe(error), 'error');
    } finally {
      setBusy(false);
    }
  }

  async function loadJobs() {
    try {
      const result = await bridgeInvoke('media.downloader.job.list', { limit: 30 });
      state.jobs = result.items || [];
      renderJobs();
      const active = state.jobs.some((item) => item.status === 'queued' || item.status === 'running' || item.status === 'canceling');
      if (active) startPolling();
      else stopPolling();
    } catch (error) {
      notify('读取任务失败：' + describe(error), 'error');
    }
  }

  function renderJobs() {
    const host = el('jobs');
    if (!state.jobs.length) {
      host.innerHTML = '<p class="muted">还没有下载任务。</p>';
      return;
    }
    host.innerHTML = state.jobs.map((job) => {
      const progress = Math.max(0, Math.min(100, Number(job.progress || 0)));
      const details = [job.status, job.quality || '', job.dest_dir || ''].filter(Boolean).join(' · ');
      const error = job.error ? '<div class="job-meta">' + escapeHtml(job.error) + '</div>' : '';
      const actions = (job.status === 'queued' || job.status === 'running' || job.status === 'canceling')
        ? '<div class="job-actions"><button type="button" data-cancel="' + escapeHtml(job.job_id) + '">取消</button></div>' : '';
      return '<article class="job">' +
        '<div class="job-head"><div class="job-title">' + escapeHtml(job.title || job.url) + '</div><span class="muted">' + escapeHtml(job.job_id.slice(0, 8)) + '</span></div>' +
        '<div class="job-meta">' + escapeHtml(details) + (job.speed ? ' · ' + escapeHtml(job.speed) : '') + (job.eta ? ' · ETA ' + escapeHtml(job.eta) : '') + '</div>' +
        '<div class="progress"><span style="width:' + progress + '%"></span></div>' +
        '<div class="job-meta">' + progress.toFixed(1) + '%</div>' + error + actions + '</article>';
    }).join('');
  }

  async function cancelJob(jobId) {
    try {
      await bridgeInvoke('media.downloader.job.cancel', { job_id: jobId });
      notify('已请求取消任务。');
      await loadJobs();
    } catch (error) {
      notify('取消失败：' + describe(error), 'error');
    }
  }

  function startPolling() {
    if (state.timer) return;
    state.timer = window.setInterval(() => { void loadJobs(); }, 2500);
  }

  function stopPolling() {
    if (state.timer) {
      window.clearInterval(state.timer);
      state.timer = null;
    }
  }

  function bind() {
    el('runtime-button').addEventListener('click', () => { void loadRuntime(); });
    el('install-button').addEventListener('click', () => { void installRuntime(); });
    el('probe-button').addEventListener('click', () => { void probe(); });
    el('start-button').addEventListener('click', () => { void startDownload(); });
    el('refresh-jobs').addEventListener('click', () => { void loadJobs(); });
    el('jobs').addEventListener('click', (event) => {
      const button = event.target.closest('[data-cancel]');
      if (button) void cancelJob(button.getAttribute('data-cancel'));
    });
  }

  async function boot() {
    bind();
    await loadWorkspace();
    await loadRuntime();
    await loadJobs();
  }

  window.addEventListener('DOMContentLoaded', () => { void boot(); });
})();
