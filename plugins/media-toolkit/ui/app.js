(function () {
  'use strict';

  const state = {
    workspaceRoot: '',
    media: null,        // 最近一次 probe 的结果
    runtime: null,
    job: null,          // 当前跟踪的作业
    timer: null,
    busy: false,
  };

  const el = (id) => document.getElementById(id);

  // ── 桥接 ────────────────────────────────────────────
  // 扩展视图只能通过能力调用与插件通信（没有直接读文件的通道），
  // 所以产物预览走 media.toolkit.preview.image 这个受约束的能力。

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

  function describe(error) {
    if (error && error.code === 'bridge_unavailable') return error.message;
    if (typeof error === 'string') return error;
    if (error && error.message) return error.message;
    return '未知错误';
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

  function escapeHtml(value) {
    return String(value == null ? '' : value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function setBusy(busy) {
    state.busy = busy;
    for (const id of ['runtime-button', 'reload-files', 'probe-button', 'plan-button',
                      'run-button', 'extract-button', 'refresh-jobs', 'cancel-button']) {
      const node = el(id);
      if (node) node.disabled = busy;
    }
  }

  function activeTab() {
    const tab = document.querySelector('.tab.is-active');
    return tab ? tab.dataset.tab : 'convert';
  }

  const isRecipeTab = (tab) => ['convert', 'audio', 'images'].includes(tab);

  // ── 路径与命名 ──────────────────────────────────────

  function currentPath() {
    return (el('media-path').value || '').trim();
  }

  function baseName(path) {
    const slash = path.lastIndexOf('/');
    return slash >= 0 ? path.slice(slash + 1) : path;
  }

  function dirName(path) {
    const slash = path.lastIndexOf('/');
    return slash >= 0 ? path.slice(0, slash + 1) : '';
  }

  function stripExt(name) {
    const dot = name.lastIndexOf('.');
    return dot > 0 ? name.slice(0, dot) : name;
  }

  // 输出默认落在源文件旁边、带后缀 —— 省掉手打路径。
  function suggestOutput(suffix, extension) {
    const path = currentPath();
    if (!path) return '';
    return dirName(path) + stripExt(baseName(path)) + suffix + extension;
  }

  function audioExtension() {
    const format = el('audio-format').value;
    return format === 'm4a' ? '.m4a' : (format === 'wav' ? '.wav' : '.mp3');
  }

  function prefillOutputs() {
    const path = currentPath();
    const suggestions = {
      'convert-out': suggestOutput(
        el('convert-mode').value === 'compress' ? '-compressed' : '-transcoded', '.mp4'),
      'audio-out': suggestOutput('', audioExtension()),
      'images-out': suggestOutput('-from-images', '.mp4'),
    };
    for (const id of Object.keys(suggestions)) {
      const node = el(id);
      // 只填空白项：用户手填过的路径不该被覆盖。
      if (node && !node.value.trim()) node.value = path ? suggestions[id] : '';
    }
  }

  // ── 素材列表 ────────────────────────────────────────

  async function loadWorkspace() {
    try {
      const context = await window.__TAURI_INTERNALS__.invoke('get_plugin_view_context', {});
      state.workspaceRoot = (context && context.workspace_root) || '';
    } catch (_) {
      state.workspaceRoot = '';
    }
    el('workspace-root').textContent = state.workspaceRoot || '（未获取到，无法列出素材）';
  }

  async function loadFiles() {
    const select = el('file-select');
    if (!state.workspaceRoot) {
      select.innerHTML = '<option value="">未获取到工作区</option>';
      return;
    }
    select.innerHTML = '<option value="">正在读取…</option>';
    try {
      const result = await bridgeInvoke('media.toolkit.files.list', {
        workspace_root: state.workspaceRoot,
        limit: 300,
      });
      const files = (result && result.files) || [];
      if (!files.length) {
        select.innerHTML = '<option value="">工作区里没有媒体文件</option>';
        return;
      }
      const parts = ['<option value="">从列表选择…</option>'];
      for (const file of files) {
        const size = file.size_bytes ? ' · ' + formatSize(file.size_bytes) : '';
        parts.push(
          '<option value="' + escapeHtml(file.path) + '">' +
          escapeHtml(file.path) + '（' + escapeHtml(file.kind) + size + '）</option>'
        );
      }
      select.innerHTML = parts.join('');
      if (result.truncated) {
        notify('素材较多，列表只显示了前 ' + files.length + ' 个。可直接填写相对路径。');
      }
    } catch (error) {
      select.innerHTML = '<option value="">读取失败</option>';
      notify('读取素材列表失败：' + describe(error), 'bad');
    }
  }

  function formatSize(bytes) {
    if (bytes >= 1024 * 1024 * 1024) return (bytes / 1024 / 1024 / 1024).toFixed(2) + ' GB';
    if (bytes >= 1024 * 1024) return (bytes / 1024 / 1024).toFixed(1) + ' MB';
    if (bytes >= 1024) return (bytes / 1024).toFixed(0) + ' KB';
    return bytes + ' B';
  }

  function formatDuration(ms) {
    if (!ms) return '—';
    const total = Math.round(ms / 1000);
    const h = Math.floor(total / 3600);
    const m = Math.floor((total % 3600) / 60);
    const s = total % 60;
    const mm = String(m).padStart(2, '0');
    const ss = String(s).padStart(2, '0');
    return h > 0 ? h + ':' + mm + ':' + ss : m + ':' + ss;
  }

  // ── 运行时 ──────────────────────────────────────────

  async function loadRuntime() {
    setBusy(true);
    try {
      const runtime = await bridgeInvoke('media.toolkit.runtime.status', {});
      state.runtime = runtime;
      renderRuntime(runtime);
    } catch (error) {
      state.runtime = null;
      el('runtime-badge').className = 'badge bad';
      el('runtime-badge').textContent = '检查失败';
      notify('检查运行时失败：' + describe(error), 'bad');
    } finally {
      setBusy(false);
    }
  }

  function renderRuntime(runtime) {
    const badge = el('runtime-badge');
    if (runtime && runtime.available) {
      badge.className = 'badge ok';
      badge.textContent = 'ffmpeg 就绪';
      const version = (runtime.version || '').split(' ').slice(0, 3).join(' ');
      notify('ffmpeg 已就绪：' + version, 'ok');
    } else {
      badge.className = 'badge bad';
      badge.textContent = '缺 ffmpeg';
      notify((runtime && runtime.message) || '未找到 ffmpeg。装好并加入 PATH 后重新检查。', 'bad');
    }
  }

  // ── 读取素材信息 ────────────────────────────────────

  async function probe() {
    const path = currentPath();
    if (!path) {
      notify('先选一个素材，或填写相对路径。', 'bad');
      return;
    }
    setBusy(true);
    try {
      const info = await bridgeInvoke('media.toolkit.probe', {
        workspace_root: state.workspaceRoot,
        path: path,
      });
      if (info && info.error) {
        state.media = null;
        el('media-info').hidden = true;
        notify('读取失败：' + info.error, 'bad');
        return;
      }
      state.media = info;
      renderMedia(info);
      prefillOutputs();
      notify('');
    } catch (error) {
      notify('读取失败：' + describe(error), 'bad');
    } finally {
      setBusy(false);
    }
  }

  function renderMedia(info) {
    const rows = [
      ['文件', info.path],
      ['时长', formatDuration(info.duration_ms) + '（' + info.duration_ms + ' ms）'],
      ['体积', formatSize(info.size_bytes || 0)],
      ['容器', info.format_name || '—'],
    ];
    if (info.video) {
      rows.push(['视频', (info.video.codec_name || '?') + ' · ' +
        info.video.width + '×' + info.video.height + ' · ' +
        (info.video.fps ? info.video.fps.toFixed(2) + ' fps' : '帧率未知')]);
    } else {
      rows.push(['视频', '无']);
    }
    if (info.audio) {
      rows.push(['音频', (info.audio.codec_name || '?') + ' · ' +
        (info.audio.sample_rate || '?') + ' Hz · ' + (info.audio.channels || '?') + ' 声道']);
    } else {
      rows.push(['音频', '无（不能抽音频，也无法做响度分析）']);
    }
    if (info.warnings && info.warnings.length) {
      rows.push(['提醒', info.warnings.join('；')]);
    }

    const html = ['<table>'];
    for (const [key, value] of rows) {
      html.push('<tr><th>' + escapeHtml(key) + '</th><td>' + escapeHtml(value) + '</td></tr>');
    }
    html.push('</table>');
    const node = el('media-info');
    node.innerHTML = html.join('');
    node.hidden = false;
  }

  // ── 构造 recipe ─────────────────────────────────────
  // 与插件的声明式模型对应：只描述"要做什么"，参数由插件编译成 ffmpeg 参数。

  function numberOrNull(id) {
    const raw = (el(id).value || '').trim();
    if (!raw) return null;
    const value = Number(raw);
    return Number.isFinite(value) && value > 0 ? value : null;
  }

  function buildRecipe(tab) {
    const path = currentPath();
    if (!path) throw new Error('先选一个素材，或填写相对路径。');

    if (tab === 'convert') {
      const mode = el('convert-mode').value;
      const width = numberOrNull('convert-width');
      const crf = numberOrNull('convert-crf');
      const fps = numberOrNull('convert-fps');
      const output = (el('convert-out').value || '').trim() ||
        suggestOutput(mode === 'compress' ? '-compressed' : '-transcoded', '.mp4');

      const video = {
        encode: {
          codec: 'libx264',
          crf: crf === null ? (mode === 'compress' ? 28 : 23) : crf,
          preset: mode === 'compress' ? 'slow' : 'veryfast',
          pixel_format: 'yuv420p',
        },
      };
      if (width) video.scale = { width: width };
      if (fps) video.fps = { value: fps };
      return {
        inputs: [{ path: path }],
        video: video,
        audio: { encode: { codec: 'aac', bitrate: mode === 'compress' ? '128k' : '160k' } },
        output: { path: output, fast_start: true },
      };
    }

    if (tab === 'audio') {
      const format = el('audio-format').value;
      const codec = format === 'm4a' ? 'aac' : (format === 'wav' ? 'pcm_s16le' : 'libmp3lame');
      const ext = format === 'm4a' ? '.m4a' : (format === 'wav' ? '.wav' : '.mp3');
      const output = (el('audio-out').value || '').trim() || suggestOutput('', ext);
      const audio = { encode: { codec: codec } };
      if (format !== 'wav') audio.encode.bitrate = el('audio-bitrate').value;
      const start = numberOrNull('audio-start');
      const end = numberOrNull('audio-end');
      if (start !== null || end !== null) {
        audio.trim = {};
        if (start !== null) audio.trim.start_ms = start;
        if (end !== null) audio.trim.end_ms = end;
      }
      return {
        inputs: [{ path: path }],
        video: { disable: true },
        audio: audio,
        output: { path: output },
      };
    }

    if (tab === 'images') {
      const output = (el('images-out').value || '').trim() || suggestOutput('-from-images', '.mp4');
      const fps = numberOrNull('images-fps') || 30;
      const video = {
        fps: { value: fps },
        encode: { codec: 'libx264', crf: 20, preset: 'medium', pixel_format: 'yuv420p' },
      };
      const width = numberOrNull('images-width');
      const height = numberOrNull('images-height');
      if (width || height) {
        video.scale = { width: width || 0, height: height || 0 };
      }
      // 通配符路径是序列帧，交给 ffmpeg 自己展开；单张图则循环给定时长。
      const isSequence = /%0?\d*d/.test(path);
      const input = { path: path, framerate: String(fps) };
      if (!isSequence) {
        input.loop = true;
        const duration = numberOrNull('images-duration');
        video.trim = { duration_ms: duration || 5000 };
      }
      return {
        inputs: [input],
        video: video,
        audio: { disable: true },
        output: { path: output, fast_start: true },
      };
    }

    throw new Error('这个标签页不出参数。');
  }

  // ── 预览参数 / 开始 ─────────────────────────────────

  async function planRecipe() {
    let recipe;
    try {
      recipe = buildRecipe(activeTab());
    } catch (error) {
      notify(describe(error), 'bad');
      return;
    }
    setBusy(true);
    try {
      const result = await bridgeInvoke('media.toolkit.recipe.plan', { recipe: recipe });
      if (!result || !result.ok) {
        notify('参数不合法：' + ((result && result.message) || '未知原因'), 'bad');
        return;
      }
      el('plan-output').hidden = false;
      el('plan-output').textContent =
        result.description + '\n\nffmpeg ' + (result.args || []).join(' ') +
        '\n\n输出：' + result.output;
      notify('');
    } catch (error) {
      notify('预览参数失败：' + describe(error), 'bad');
    } finally {
      setBusy(false);
    }
  }

  async function runCurrent() {
    const tab = activeTab();
    if (tab === 'analyze') return runAnalyze();
    if (tab === 'preview') return runPreview();

    let recipe;
    try {
      recipe = buildRecipe(tab);
    } catch (error) {
      notify(describe(error), 'bad');
      return;
    }
    setBusy(true);
    try {
      const result = await bridgeInvoke('media.toolkit.recipe.run', {
        workspace_root: state.workspaceRoot,
        recipe: recipe,
        kind: tab,
      });
      const job = result && result.job;
      if (!job) {
        notify('提交失败：未返回作业。', 'bad');
        return;
      }
      state.job = job;
      renderJob(job);
      notify('已开始处理。');
      startPolling();
    } catch (error) {
      notify('提交失败：' + describe(error), 'bad');
    } finally {
      setBusy(false);
    }
  }

  // ── 分析 ────────────────────────────────────────────

  async function runAnalyze() {
    const path = currentPath();
    if (!path) {
      notify('先选一个素材。', 'bad');
      return;
    }
    const kind = el('analyze-kind').value;
    const points = numberOrNull('analyze-points') || 2000;
    setBusy(true);
    notify('分析中，长片的逐帧分析可能需要一段时间…');
    try {
      const result = await bridgeInvoke(
        kind === 'scene' ? 'media.toolkit.scene.score' : 'media.toolkit.audio.loudness',
        { workspace_root: state.workspaceRoot, path: path, max_points: points }
      );
      if (result && result.error) {
        notify('分析失败：' + result.error, 'bad');
        return;
      }
      if (kind === 'scene') drawSceneChart(result);
      else drawLoudnessChart(result);
      notify('');
    } catch (error) {
      notify('分析失败：' + describe(error), 'bad');
    } finally {
      setBusy(false);
    }
  }

  function chartContext() {
    el('chart-wrap').hidden = false;
    const canvas = el('chart');
    const ctx = canvas.getContext('2d');
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    return { canvas: canvas, ctx: ctx };
  }

  // 画一条时间序列。坐标轴与中位线是重点：
  // 画面活动的绝对值没有普适阈值，只有相对自身分布看才有意义。
  function drawSeries(points, pick, options) {
    const { canvas, ctx } = chartContext();
    const pad = { left: 46, right: 12, top: 12, bottom: 24 };
    const width = canvas.width - pad.left - pad.right;
    const height = canvas.height - pad.top - pad.bottom;

    const values = points.map(pick).filter((v) => Number.isFinite(v));
    if (!values.length) {
      el('chart-summary').textContent = '没有采样点。';
      return null;
    }
    // 用 reduce 而不是展开：采样点可达 20 万个，展开会把调用栈撑爆。
    let minValue = options.min !== undefined ? options.min : Infinity;
    let maxValue = -Infinity;
    for (const value of values) {
      if (value < minValue) minValue = value;
      if (value > maxValue) maxValue = value;
    }
    if (!Number.isFinite(minValue)) minValue = 0;
    if (!Number.isFinite(maxValue)) maxValue = 1;
    const span = maxValue - minValue || 1;
    const maxTime = points[points.length - 1].time_ms || 1;

    const x = (t) => pad.left + (t / maxTime) * width;
    const y = (v) => pad.top + height - ((v - minValue) / span) * height;

    // 网格与刻度
    ctx.strokeStyle = '#e6ebf2';
    ctx.fillStyle = '#68758c';
    ctx.font = '11px "Segoe UI", sans-serif';
    ctx.lineWidth = 1;
    for (let i = 0; i <= 4; i++) {
      const value = minValue + (span * i) / 4;
      const yy = y(value);
      ctx.beginPath();
      ctx.moveTo(pad.left, yy);
      ctx.lineTo(pad.left + width, yy);
      ctx.stroke();
      ctx.fillText(value.toFixed(options.digits === undefined ? 1 : options.digits), 4, yy + 4);
    }
    const totalSeconds = maxTime / 1000;
    for (let i = 0; i <= 4; i++) {
      const t = (maxTime * i) / 4;
      ctx.fillText(formatDuration(t), x(t) - 14, canvas.height - 6);
    }

    ctx.strokeStyle = options.color || '#2563eb';
    ctx.lineWidth = 1.5;
    ctx.beginPath();
    points.forEach((point, index) => {
      const value = pick(point);
      if (!Number.isFinite(value)) return;
      const px = x(point.time_ms);
      const py = y(value);
      if (index === 0) ctx.moveTo(px, py);
      else ctx.lineTo(px, py);
    });
    ctx.stroke();

    // 中位线：判断"哪里异常"的基准
    const sorted = values.slice().sort((a, b) => a - b);
    const median = sorted[Math.floor(sorted.length / 2)];
    ctx.strokeStyle = '#a15c07';
    ctx.setLineDash([4, 4]);
    ctx.beginPath();
    ctx.moveTo(pad.left, y(median));
    ctx.lineTo(pad.left + width, y(median));
    ctx.stroke();
    ctx.setLineDash([]);
    ctx.fillStyle = '#a15c07';
    ctx.fillText('中位 ' + median.toFixed(2), pad.left + 6, y(median) - 4);

    return { median: median, min: minValue, max: maxValue, count: points.length, totalSeconds: totalSeconds };
  }

  function drawSceneChart(result) {
    const points = (result && result.points) || [];
    const stats = drawSeries(points, (p) => p.score, { color: '#2563eb', min: 0 });
    if (!stats) return;
    const notes = (result.notes || []).join('；');
    el('chart-summary').textContent =
      '采样 ' + stats.count + ' 点 / ' + stats.totalSeconds.toFixed(0) + ' 秒；' +
      '中位 ' + stats.median.toFixed(2) + '，峰值 ' + (result.peak_score || 0).toFixed(2) +
      '，按阈值 ' + (result.threshold || 0) + ' 命中切点 ' + (result.cut_count || 0) + ' 个。' +
      ' 看"远高于中位线"的尖峰，不要盯着绝对值。' + (notes ? ' ' + notes : '');
  }

  function drawLoudnessChart(result) {
    const points = (result && result.points) || [];
    const stats = drawSeries(points, (p) => p.momentary_lufs, {
      color: '#7c3aed', digits: 0,
    });
    if (!stats) return;
    el('chart-summary').textContent =
      '采样 ' + stats.count + ' 点 / ' + stats.totalSeconds.toFixed(0) + ' 秒；' +
      '整体响度 ' + (result.integrated_lufs || 0).toFixed(1) + ' LUFS，' +
      '响度范围 ' + (result.loudness_range_lu || 0).toFixed(1) + ' LU。' +
      ' 骤升处多为撞击/重音（事件的可靠依据），显著低于中位处为静场。';
  }

  // ── 预览 ────────────────────────────────────────────

  async function runPreview() {
    const path = currentPath();
    if (!path) {
      notify('先选一个素材。', 'bad');
      return;
    }
    const times = parseTimes(el('extract-times').value);
    setBusy(true);
    try {
      if (times.length) {
        await extractFrames(path, times);
      } else {
        await contactSheet(path);
      }
      notify('');
    } catch (error) {
      notify('预览失败：' + describe(error), 'bad');
    } finally {
      setBusy(false);
    }
  }

  // 注意：Number('') === 0，空串不做过滤会被当成"抽第 0 毫秒一帧"，
  // 于是"留空 = 生成缩略图网格"这条默认路径永远走不到。
  function parseTimes(raw) {
    return (raw || '')
      .split(/[,，\s]+/)
      .map((piece) => piece.trim())
      .filter((piece) => piece.length > 0)
      .map((piece) => Number(piece))
      .filter((value) => Number.isFinite(value) && value >= 0);
  }

  async function contactSheet(path) {
    const output = suggestOutput('-sheet', '.jpg') || 'sheet.jpg';
    const interval = numberOrNull('sheet-interval');
    const input = {
      workspace_root: state.workspaceRoot,
      path: path,
      output: output,
      columns: numberOrNull('sheet-columns') || 6,
      rows: numberOrNull('sheet-rows') || 4,
      tile_width: numberOrNull('sheet-tile') || 320,
    };
    if (interval) input.interval_ms = interval;

    notify('正在生成缩略图网格…');
    const result = await bridgeInvoke('media.toolkit.frames.contact-sheet', input);
    if (result && result.error) {
      notify('生成失败：' + result.error, 'bad');
      return;
    }
    const warnings = (result.warnings || []).join('；');
    await showImages([result.path], '缩略图网格 · 平均亮度 ' +
      (result.mean_luminance || 0).toFixed(1) + (warnings ? ' · ' + warnings : ''));
    if (warnings) notify('注意：' + warnings, 'bad');
  }

  async function extractFrames(path, times) {
    const outputDir = dirName(path) + stripExt(baseName(path)) + '-frames';
    notify('正在抽取 ' + times.length + ' 帧…');
    const result = await bridgeInvoke('media.toolkit.frames.extract', {
      workspace_root: state.workspaceRoot,
      path: path,
      output_dir: outputDir,
      times_ms: times,
      width: 480,
    });
    if (result && result.error) {
      notify('抽帧失败：' + result.error, 'bad');
      return;
    }
    await showImages((result && result.files) || [], '抽取 ' + times.length + ' 帧');
  }

  // 图片通过受约束的预览能力取回（视图没有直接读文件的通道）。
  async function showImages(paths, caption) {
    const grid = el('preview-grid');
    grid.innerHTML = '';
    grid.hidden = false;
    if (!paths.length) return;

    let shown = 0;
    for (const path of paths) {
      try {
        const preview = await bridgeInvoke('media.toolkit.preview.image', {
          workspace_root: state.workspaceRoot,
          path: path,
        });
        const figure = document.createElement('figure');
        if (paths.length === 1) figure.className = 'wide';
        const image = document.createElement('img');
        image.src = preview.data_url;
        image.alt = caption;
        const label = document.createElement('figcaption');
        label.textContent = preview.path + ' · ' + formatSize(preview.size_bytes);
        figure.appendChild(image);
        figure.appendChild(label);
        grid.appendChild(figure);
        shown++;
      } catch (error) {
        notify('有产物无法预览：' + describe(error), 'bad');
      }
    }
    if (!shown) grid.hidden = true;
  }

  // ── 作业 ────────────────────────────────────────────

  function renderJob(job) {
    const box = el('current-job');
    box.hidden = false;
    el('job-title').textContent = job.kind ? '处理 · ' + job.kind : '处理';
    const stateNode = el('job-state');
    stateNode.textContent = jobStateText(job.status);
    stateNode.className = 'badge ' + jobStateTone(job.status);
    const percent = Math.max(0, Math.min(100, job.progress || 0));
    const bar = el('job-bar');
    bar.style.width = percent + '%';
    bar.className = 'bar-fill' +
      (job.status === 'completed' ? ' done' : (job.status === 'failed' ? ' bad' : ''));
    el('job-percent').textContent = percent.toFixed(1) + '%';
    el('job-speed').textContent = job.speed ? job.speed : '';
    el('job-output').textContent = job.output || '';
    if (job.error) el('job-output').textContent = job.error;
  }

  function jobStateText(status) {
    return ({
      queued: '排队中', running: '处理中', completed: '已完成',
      failed: '失败', cancelled: '已取消',
    })[status] || (status || '—');
  }

  function jobStateTone(status) {
    if (status === 'completed') return 'ok';
    if (status === 'failed') return 'bad';
    if (status === 'cancelled') return 'warn';
    return 'muted';
  }

  const terminal = (status) => ['completed', 'failed', 'cancelled'].includes(status);

  function startPolling() {
    stopPolling();
    state.timer = window.setInterval(() => { void pollJob(); }, 1200);
  }

  function stopPolling() {
    if (state.timer) {
      window.clearInterval(state.timer);
      state.timer = null;
    }
  }

  async function pollJob() {
    const job = state.job;
    if (!job || !job.id) { stopPolling(); return; }
    try {
      const fresh = await bridgeInvoke('media.toolkit.job.status', { job_id: job.id });
      if (!fresh) return;
      state.job = fresh;
      renderJob(fresh);
      if (terminal(fresh.status)) {
        stopPolling();
        if (fresh.status === 'completed') {
          notify('处理完成：' + (fresh.output || ''), 'ok');
          // 产物是媒体文件，直接给个预览（不是图片就不显示）
          if (/\.(jpg|jpeg|png|webp|bmp|gif)$/i.test(fresh.output || '')) {
            await showImages([fresh.output], '产物');
          }
        } else if (fresh.status === 'failed') {
          notify('处理失败：' + (fresh.error || '未知原因'), 'bad');
        }
        void loadHistory();
      }
    } catch (_) {
      // 轮询失败不打断：下一拍再试。
    }
  }

  async function loadHistory() {
    try {
      const result = await bridgeInvoke('media.toolkit.job.list', { limit: 10 });
      const jobs = (result && result.jobs) || [];
      const box = el('job-history');
      if (!jobs.length) {
        box.innerHTML = '<p class="hint">还没有处理记录。</p>';
        return;
      }
      const parts = [];
      for (const job of jobs) {
        parts.push(
          '<div class="history-item">' +
          '<span class="badge ' + jobStateTone(job.status) + '">' + escapeHtml(jobStateText(job.status)) + '</span>' +
          '<span class="grow mono">' + escapeHtml(job.output || job.input || job.id) + '</span>' +
          '<span class="muted">' + escapeHtml(jobStateText(job.status)) + '</span>' +
          '</div>'
        );
      }
      box.innerHTML = parts.join('');
    } catch (_) {
      /* 历史不是关键路径 */
    }
  }

  async function cancelJob() {
    const job = state.job;
    if (!job || !job.id) return;
    setBusy(true);
    try {
      await bridgeInvoke('media.toolkit.job.cancel', { job_id: job.id });
      notify('已请求取消。');
    } catch (error) {
      notify('取消失败：' + describe(error), 'bad');
    } finally {
      setBusy(false);
    }
  }

  // ── 事件绑定 ────────────────────────────────────────

  function bindTabs() {
    for (const tab of document.querySelectorAll('.tab')) {
      tab.addEventListener('click', () => {
        for (const node of document.querySelectorAll('.tab')) node.classList.remove('is-active');
        for (const node of document.querySelectorAll('.panel')) node.classList.remove('is-active');
        tab.classList.add('is-active');
        const panel = document.querySelector('.panel[data-panel="' + tab.dataset.tab + '"]');
        if (panel) panel.classList.add('is-active');
        // recipe 之外的标签页没有"预览参数"这一步
        const recipeTab = isRecipeTab(tab.dataset.tab);
        el('plan-button').hidden = !recipeTab;
        el('plan-output').hidden = true;
        el('run-button').textContent = recipeTab ? '开始' : '运行';
        prefillOutputs();
      });
    }
  }

  async function boot() {
    bindTabs();
    el('runtime-button').addEventListener('click', () => { void loadRuntime(); });
    el('reload-files').addEventListener('click', () => { void loadFiles(); });
    el('probe-button').addEventListener('click', () => { void probe(); });
    el('plan-button').addEventListener('click', () => { void planRecipe(); });
    el('run-button').addEventListener('click', () => { void runCurrent(); });
    el('extract-button').addEventListener('click', () => { void runPreview(); });
    el('refresh-jobs').addEventListener('click', () => { void loadHistory(); });
    el('cancel-button').addEventListener('click', () => { void cancelJob(); });

    el('file-select').addEventListener('change', (event) => {
      if (!event.target.value) return;
      el('media-path').value = event.target.value;
      el('convert-out').value = '';
      el('audio-out').value = '';
      el('images-out').value = '';
      state.media = null;
      el('media-info').hidden = true;
      void probe();
    });
    el('media-path').addEventListener('change', () => { prefillOutputs(); });
    el('convert-mode').addEventListener('change', () => { prefillOutputs(); });
    el('audio-format').addEventListener('change', () => { prefillOutputs(); });
    el('run-button').textContent = '开始';

    await loadWorkspace();
    await loadFiles();
    await loadHistory();
    void loadRuntime();
  }

  window.addEventListener('DOMContentLoaded', () => { void boot(); });
})();
