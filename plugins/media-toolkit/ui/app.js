(function () {
  'use strict';

  const state = {
    workspaceRoot: '',
    selections: { image: [], video: [], images: [], analyze: [] },
    runtime: null,
    activeTab: 'image',
    job: null,
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
    for (const id of ['runtime-button', 'pick-media', 'pick-media-more', 'pick-audio', 'run-button',
                      'refresh-jobs', 'cancel-button']) {
      const node = el(id);
      if (node) node.disabled = busy;
    }
  }

  function formatSize(bytes) {
    const value = Number(bytes || 0);
    if (!Number.isFinite(value) || value <= 0) return '0 B';
    if (value >= 1024 * 1024 * 1024) return (value / 1024 / 1024 / 1024).toFixed(2) + ' GB';
    if (value >= 1024 * 1024) return (value / 1024 / 1024).toFixed(1) + ' MB';
    if (value >= 1024) return (value / 1024).toFixed(0) + ' KB';
    return value + ' B';
  }

  function formatBitrate(bits) {
    const value = Number(bits || 0);
    if (!Number.isFinite(value) || value <= 0) return '—';
    if (value >= 1000000) return (value / 1000000).toFixed(2) + ' Mbps';
    return Math.round(value / 1000) + ' kbps';
  }

  function formatDuration(ms) {
    const value = Number(ms || 0);
    if (!Number.isFinite(value) || value <= 0) return '—';
    const total = Math.round(value / 1000);
    const h = Math.floor(total / 3600);
    const m = Math.floor((total % 3600) / 60);
    const s = total % 60;
    const mm = String(m).padStart(2, '0');
    const ss = String(s).padStart(2, '0');
    return h > 0 ? h + ':' + mm + ':' + ss : m + ':' + ss;
  }

  function baseName(path) {
    const slash = Math.max(path.lastIndexOf('/'), path.lastIndexOf('\\'));
    return slash >= 0 ? path.slice(slash + 1) : path;
  }

  function dirName(path) {
    const slash = Math.max(path.lastIndexOf('/'), path.lastIndexOf('\\'));
    return slash >= 0 ? path.slice(0, slash + 1) : '';
  }

  function stripExt(name) {
    const dot = name.lastIndexOf('.');
    return dot > 0 ? name.slice(0, dot) : name;
  }

  function currentSelection() {
    return state.selections[state.activeTab] || [];
  }

  function currentPath() {
    const selected = currentSelection();
    return selected.length ? selected[0].path : '';
  }

  function positiveNumber(id) {
    const raw = (el(id).value || '').trim();
    if (!raw) return 0;
    const value = Number(raw);
    return Number.isFinite(value) && value > 0 ? value : 0;
  }

  function integerOrZero(id) {
    return Math.max(0, Math.round(positiveNumber(id)));
  }

  function resolveSizePreset(presetId, widthId, heightId, fixedWidths) {
    const preset = el(presetId).value;
    if (preset === 'source') return { width: 0, height: 0 };
    if (preset === 'custom') {
      return { width: integerOrZero(widthId), height: integerOrZero(heightId) };
    }
    return { width: fixedWidths[preset] || 0, height: 0 };
  }

  function imageDimensions() {
    return resolveSizePreset('image-size-preset', 'image-width', 'image-height', {
      '1920': 1920, '1280': 1280, '640': 640,
    });
  }

  function heightPreset(presetId, widthId, heightId) {
    const preset = el(presetId).value;
    if (preset === 'source') return { width: 0, height: 0 };
    if (preset === 'custom') {
      return { width: integerOrZero(widthId), height: integerOrZero(heightId) };
    }
    const height = Number(preset);
    return { width: 0, height: Number.isFinite(height) && height > 0 ? Math.round(height) : 0 };
  }

  function videoDimensions() {
    return heightPreset('video-size-preset', 'video-width', 'video-height');
  }

  function imagesDimensions() {
    return heightPreset('images-size-preset', 'images-width', 'images-height');
  }

  function openAdvanced(id) {
    const node = el(id);
    if (node) node.open = true;
  }

  function chooseCustom(presetId, advancedId) {
    el(presetId).value = 'custom';
    openAdvanced(advancedId);
  }

  function requireCustomDimensions(presetId, dimensions) {
    if (el(presetId).value === 'custom' && !dimensions.width && !dimensions.height) {
      throw new Error('自定义尺寸至少填写宽度或高度。');
    }
    return dimensions;
  }

  function syncImageSizePreset() {
    if (el('image-size-preset').value === 'custom') {
      openAdvanced('image-advanced');
      return;
    }
    const dimensions = imageDimensions();
    el('image-width').value = dimensions.width || '';
    el('image-height').value = dimensions.height || '';
  }

  function syncVideoSizePreset() {
    if (el('video-size-preset').value === 'custom') {
      openAdvanced('video-advanced');
      return;
    }
    const dimensions = videoDimensions();
    el('video-width').value = dimensions.width || '';
    el('video-height').value = dimensions.height || '';
  }

  function syncImagesSizePreset() {
    if (el('images-size-preset').value === 'custom') {
      openAdvanced('images-advanced');
      return;
    }
    const dimensions = imagesDimensions();
    el('images-width').value = dimensions.width || '';
    el('images-height').value = dimensions.height || '';
  }

  function imageQualityValue() {
    const preset = el('image-quality-preset').value;
    if (preset === 'custom') return Math.max(1, integerOrZero('image-quality'));
    return ({ high: 92, balanced: 82, small: 65 })[preset] || 82;
  }

  function syncImageQualityPreset() {
    if (el('image-quality-preset').value === 'custom') {
      openAdvanced('image-advanced');
      return;
    }
    el('image-quality').value = imageQualityValue();
    el('image-quality-label').textContent = el('image-quality').value;
  }

  function videoQualitySettings() {
    const preset = el('video-quality-preset').value;
    if (preset === 'custom') {
      return { crf: integerOrZero('video-crf'), speed: el('video-preset').value };
    }
    return ({
      high: { crf: 18, speed: 'slow' },
      balanced: { crf: 23, speed: 'medium' },
      small: { crf: 28, speed: 'slow' },
    })[preset] || { crf: 23, speed: 'medium' };
  }

  function syncVideoQualityPreset() {
    if (el('video-quality-preset').value === 'custom') {
      openAdvanced('video-advanced');
      return;
    }
    const quality = videoQualitySettings();
    el('video-crf').value = quality.crf;
    el('video-bitrate').value = '';
    el('video-preset').value = quality.speed;
  }

  function updateVideoPresetAvailability() {
    const locked = el('audio-only').checked || el('video-codec').value === 'copy';
    for (const id of ['video-size-preset', 'video-quality-preset']) {
      el(id).disabled = locked;
      el(id).title = locked ? '流复制或提取音轨时不改变尺寸与质量。' : '';
    }
  }

  function setAutoValue(id, value) {
    const node = el(id);
    if (!node) return;
    if (!node.value || node.dataset.auto === 'true') {
      node.value = value || '';
      node.dataset.auto = 'true';
    }
  }

  function markManual(id) {
    const node = el(id);
    if (node) node.dataset.auto = 'false';
  }

  function suggestOutput(suffix, extension, sourcePath) {
    const path = sourcePath || currentPath();
    if (!path) return '';
    return dirName(path) + stripExt(baseName(path)) + suffix + extension;
  }

  function outputExtension(format) {
    return format === 'jpeg' ? '.jpg' : '.' + format;
  }

  function videoExtension(format) {
    return '.' + format;
  }

  function prefillOutputs() {
    const path = currentPath();
    if (!path) return;
    if (state.activeTab === 'image') {
      setAutoValue('image-output-dir', dirName(path).replace(/[\\/]$/, ''));
    }
    if (state.activeTab === 'video') {
      if (el('audio-only').checked) {
        const format = el('audio-format').value;
        setAutoValue('video-out', suggestOutput('', format === 'm4a' ? '.m4a' : '.' + format, path));
      } else {
        setAutoValue('video-out', suggestOutput('-converted', videoExtension(el('video-format').value), path));
      }
    }
    if (state.activeTab === 'images') {
      setAutoValue('images-out', suggestOutput('-slideshow', '.mp4', path));
    }
  }

  async function loadWorkspace() {
    try {
      const context = await window.__TAURI_INTERNALS__.invoke('get_plugin_view_context', {});
      state.workspaceRoot = (context && context.workspace_root) || '';
    } catch (_) {
      state.workspaceRoot = '';
    }
  }

  function kindText(kind) {
    return ({ image: '图片', video: '视频', audio: '音频' })[kind] || '文件';
  }

  function pickerConfig(tab) {
    return ({
      image: { mode: 'image', multiple: true },
      video: { mode: 'video', multiple: false },
      images: { mode: 'image', multiple: false },
      analyze: { mode: 'media', multiple: false },
    })[tab] || { mode: 'media', multiple: false };
  }

  function emptyHint(tab) {
    return ({
      image: '可一次选择多张图片',
      video: '选择一个视频文件',
      images: '选择一张图片',
      analyze: '选择图片、视频或音频',
    })[tab] || '点击选择文件';
  }

  function renderSelection() {
    const items = currentSelection();
    const list = el('selection-list');
    const empty = el('pick-media');
    const more = el('pick-media-more');
    const count = el('selection-count');
    const moreLabels = {
      image: '+ 添加更多图片',
      video: '更换视频',
      images: '更换图片',
      analyze: '更换文件',
    };
    count.textContent = items.length ? items.length + ' 个文件' : '';
    el('selection-empty-hint').textContent = emptyHint(state.activeTab);
    empty.hidden = items.length > 0;
    more.hidden = items.length === 0;
    more.textContent = moreLabels[state.activeTab] || '添加更多';
    list.hidden = items.length === 0;
    if (!items.length) {
      list.innerHTML = '';
      return;
    }
    list.innerHTML = items.map((file) => {
      const detail = file.imported ? '已导入到项目' : file.path;
      return '<div class="selection-item">' +
        '<span class="file-kind">' + escapeHtml(kindText(file.kind)) + '</span>' +
        '<span class="selection-copy">' +
        '<strong>' + escapeHtml(file.name || baseName(file.path)) + '</strong>' +
        '<span class="mono" title="' + escapeHtml(detail) + '">' + escapeHtml(detail) + '</span>' +
        '</span>' +
        '<span class="selection-size">' + escapeHtml(formatSize(file.size_bytes)) + '</span>' +
        '<button class="remove-selection" type="button" data-remove-path="' + escapeHtml(file.path) + '" aria-label="移除 ' + escapeHtml(file.name || file.path) + '">×</button>' +
        '</div>';
    }).join('');
  }

  function applyPickedFiles(picked, tab) {
    if (!picked.length) return;
    if (tab === 'image') {
      const known = new Set(state.selections.image.map((file) => file.path));
      state.selections.image = state.selections.image.concat(picked.filter((file) => !known.has(file.path)));
    } else {
      state.selections[tab] = picked.slice(0, 1);
    }
    renderSelection();
    prefillOutputs();
    const imported = picked.filter((file) => file.imported).length;
    notify(imported ? '已导入 ' + imported + ' 个文件。' : '', imported ? 'ok' : '');
  }

  async function pickMedia() {
    if (!state.workspaceRoot) {
      notify('当前项目没有可用的文件目录。', 'bad');
      return;
    }
    const tab = state.activeTab;
    const config = pickerConfig(tab);
    setBusy(true);
    try {
      const result = await bridgeInvoke('media.toolkit.files.pick', {
        workspace_root: state.workspaceRoot,
        mode: config.mode,
        multiple: config.multiple,
      });
      if (!result || result.canceled) return;
      applyPickedFiles(result.files || [], tab);
    } catch (error) {
      notify('选择素材失败：' + describe(error), 'bad');
    } finally {
      setBusy(false);
    }
  }

  async function importDroppedPaths(paths) {
    if (!state.workspaceRoot) {
      notify('当前项目没有可用的文件目录。', 'bad');
      return;
    }
    const tab = state.activeTab;
    const config = pickerConfig(tab);
    if (!config.multiple && paths.length > 1) {
      notify('当前任务一次只能放入一个文件。', 'bad');
      return;
    }
    setBusy(true);
    try {
      const result = await bridgeInvoke('media.toolkit.files.pick', {
        workspace_root: state.workspaceRoot,
        mode: config.mode,
        multiple: config.multiple,
        paths: paths,
      });
      if (!result || result.canceled) return;
      applyPickedFiles(result.files || [], tab);
    } catch (error) {
      notify('导入拖入文件失败：' + describe(error), 'bad');
    } finally {
      setBusy(false);
    }
  }

  function setDragActive(active) {
    el('source-surface').classList.toggle('is-dragging', active);
  }

  function domDroppedPaths(event) {
    return Array.from((event.dataTransfer && event.dataTransfer.files) || [])
      .map((file) => file.path || '')
      .filter(Boolean);
  }

  function bindSelectionDropTarget() {
    const zone = el('source-surface');
    zone.addEventListener('dragenter', (event) => {
      event.preventDefault();
      setDragActive(true);
    });
    zone.addEventListener('dragover', (event) => {
      event.preventDefault();
      if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
    });
    zone.addEventListener('dragleave', (event) => {
      if (!zone.contains(event.relatedTarget)) setDragActive(false);
    });
    zone.addEventListener('drop', (event) => {
      event.preventDefault();
      setDragActive(false);
      const paths = domDroppedPaths(event);
      if (paths.length) void importDroppedPaths(paths);
      else notify('无法读取拖入文件路径，请点击素材区选择。', 'warn');
    });
  }

  async function registerTauriDragEvents() {
    const internals = window.__TAURI_INTERNALS__ || {};
    if (typeof internals.invoke !== 'function' || typeof internals.transformCallback !== 'function') return;
    const label = internals.metadata && internals.metadata.currentWindow && internals.metadata.currentWindow.label;
    const target = label ? { kind: 'Window', label: label } : { kind: 'Any' };
    const listen = (event, handler) => internals.invoke('plugin:event|listen', {
      event: event,
      target: target,
      handler: internals.transformCallback(handler),
    });
    try {
      await listen('tauri://drag-enter', () => setDragActive(true));
      await listen('tauri://drag-over', () => setDragActive(true));
      await listen('tauri://drag-leave', () => setDragActive(false));
      await listen('tauri://drag-drop', (event) => {
        setDragActive(false);
        const payload = event && event.payload ? event.payload : event;
        const paths = (payload && payload.paths) || [];
        if (paths.length) void importDroppedPaths(paths);
      });
    } catch (_) {
      // HTML5 drag/drop remains available where the host allows DOM drops.
    }
  }

  async function pickAudio() {
    if (!state.workspaceRoot) {
      notify('当前项目没有可用的文件目录。', 'bad');
      return;
    }
    setBusy(true);
    try {
      const result = await bridgeInvoke('media.toolkit.files.pick', {
        workspace_root: state.workspaceRoot,
        mode: 'audio',
        multiple: false,
      });
      if (!result || result.canceled || !(result.files || []).length) return;
      const audio = result.files[0];
      el('images-audio').value = audio.path;
      el('pick-audio').textContent = audio.name || baseName(audio.path);
      el('pick-audio').title = audio.imported ? '已导入到项目' : audio.path;
      notify(audio.imported ? '音轨已导入。' : '已选择音轨。', 'ok');
    } catch (error) {
      notify('选择音轨失败：' + describe(error), 'bad');
    } finally {
      setBusy(false);
    }
  }

  function removeSelection(path) {
    const tab = state.activeTab;
    state.selections[tab] = (state.selections[tab] || []).filter((file) => file.path !== path);
    renderSelection();
    prefillOutputs();
  }

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
      const sourceText = ({
        plugin_bundle: '内置',
        plugin_data: '本地',
        path: 'PATH',
      })[runtime.runtime_source] || '就绪';
      badge.textContent = sourceText + ' FFmpeg';
      badge.title = (runtime.runtime_warnings || []).join('\n');
      notify('');
      return;
    }
    badge.className = 'badge bad';
    badge.textContent = '缺少媒体运行时';
    notify((runtime && runtime.message) || '没有找到成对的 ffmpeg / ffprobe。', 'bad');
  }

  function rowsTable(rows) {
    const html = ['<table>'];
    for (const row of rows) {
      if (row.group) {
        html.push('<tr class="group"><td colspan="2">' + escapeHtml(row.group) + '</td></tr>');
        continue;
      }
      html.push('<tr><th>' + escapeHtml(row[0]) + '</th><td>' + escapeHtml(row[1]) + '</td></tr>');
    }
    html.push('</table>');
    return html.join('');
  }

  function selectedImagePaths() {
    const selected = state.selections.image.map((file) => file.path).filter(Boolean);
    if (selected.length) return selected;
    const path = currentPath();
    return path ? [path] : [];
  }

  async function runImageConversion() {
    const paths = selectedImagePaths();
    if (!paths.length) throw new Error('先选择至少一张图片。');
    const firstDir = dirName(paths[0]).replace(/[\\/]$/, '');
    const dimensions = requireCustomDimensions('image-size-preset', imageDimensions());
    const input = {
      workspace_root: state.workspaceRoot,
      paths: paths,
      output_dir: (el('image-output-dir').value || '').trim() || firstDir || '.',
      format: el('image-format').value,
      quality: imageQualityValue(),
      background: el('image-background').value,
      suffix: (el('image-suffix').value || '').trim(),
      overwrite: el('image-overwrite').checked,
    };
    if (dimensions.width > 0) input.width = dimensions.width;
    if (dimensions.height > 0) input.height = dimensions.height;
    const template = (el('image-name-template').value || '').trim();
    if (template) input.name_template = template;

    notify('正在转换 ' + paths.length + ' 张图片…');
    const response = await bridgeInvoke('media.toolkit.image.convert', input);
    const result = response && response.result;
    if (!result) throw new Error('插件没有返回转换结果。');
    const failed = result.failed || [];
    if (failed.length) {
      notify('完成 ' + result.count + ' 张，失败 ' + failed.length + ' 张：\n' +
        failed.map((item) => item.input + '：' + item.error).join('\n'), 'warn');
    } else {
      notify('已转换 ' + result.count + ' 张图片。', 'ok');
    }
    const pathsOut = (result.outputs || []).map((item) => item.output);
    if (pathsOut.length) await showPreviewImages(pathsOut);
  }

  async function showPreviewImages(paths) {
    for (const path of paths.slice(0, 6)) {
      try {
        const preview = await bridgeInvoke('media.toolkit.preview.image', {
          workspace_root: state.workspaceRoot,
          path: path,
        });
        notify('已生成 ' + baseName(preview.path), 'ok');
        return;
      } catch (_) {
        // 非图片产物或不支持预览时不打断主流程。
      }
    }
  }

  function codecLabel(value) {
    return ({
      h264: 'H.264', h265: 'H.265 / HEVC', vp9: 'VP9', copy: '直接复制，不重编码',
    })[value] || value;
  }

  function audioLabel(value) {
    return ({
      aac: 'AAC', opus: 'Opus', mp3: 'MP3', copy: '直接复制', none: '移除音轨',
    })[value] || value;
  }

  function setOptions(select, values, selected) {
    select.innerHTML = values.map((value) =>
      '<option value="' + escapeHtml(value) + '">' + escapeHtml(value === 'copy' ? '直接复制' : audioLabel(value)) + '</option>'
    ).join('');
    if (values.includes(selected)) select.value = selected;
  }

  function syncVideoCodecs() {
    const format = el('video-format').value;
    const video = el('video-codec');
    const audio = el('video-audio-codec');
    const previousVideo = video.value;
    const previousAudio = audio.value;
    const videoValues = format === 'webm' ? ['vp9', 'copy'] : (format === 'mkv' ? ['h264', 'h265', 'vp9', 'copy'] : ['h264', 'h265', 'copy']);
    const audioValues = format === 'webm' ? ['opus', 'copy', 'none'] : (format === 'mkv' ? ['aac', 'opus', 'mp3', 'copy', 'none'] : ['aac', 'copy', 'none']);
    video.innerHTML = videoValues.map((value) =>
      '<option value="' + escapeHtml(value) + '">' + escapeHtml(codecLabel(value)) + '</option>').join('');
    audio.innerHTML = audioValues.map((value) =>
      '<option value="' + escapeHtml(value) + '">' + escapeHtml(audioLabel(value)) + '</option>').join('');
    video.value = videoValues.includes(previousVideo) ? previousVideo : videoValues[0];
    audio.value = audioValues.includes(previousAudio) ? previousAudio : audioValues[0];
    updateVideoPresetAvailability();
  }

  function toggleAudioOnly() {
    const audioOnly = el('audio-only').checked;
    el('audio-only-options').hidden = !audioOnly;
    for (const id of ['video-format', 'video-codec', 'video-audio-codec', 'video-width',
                      'video-height', 'video-fps', 'video-crf', 'video-bitrate', 'video-preset']) {
      el(id).disabled = audioOnly;
    }
    el('video-audio-codec').disabled = audioOnly;
    updateVideoPresetAvailability();
    const path = currentPath();
    if (audioOnly) {
      const format = el('audio-format').value;
      setAutoValue('video-out', suggestOutput('', format === 'm4a' ? '.m4a' : '.' + format, path));
      el('run-hint').textContent = '';
    } else {
      setAutoValue('video-out', suggestOutput('-converted', videoExtension(el('video-format').value), path));
      el('run-hint').textContent = '';
    }
  }

  async function runVideoConversion() {
    const path = currentPath();
    if (!path) throw new Error('先选择一个视频文件。');
    if (el('audio-only').checked) {
      const format = el('audio-format').value;
      const codec = format === 'm4a' ? 'aac' : (format === 'wav' ? 'pcm_s16le' : 'libmp3lame');
      const extension = format === 'm4a' ? '.m4a' : '.' + format;
      const output = (el('video-out').value || '').trim() || suggestOutput('', extension, path);
      const audio = { encode: { codec: codec } };
      if (format !== 'wav') audio.encode.bitrate = el('audio-bitrate').value;
      const recipe = {
        inputs: [{ path: path }],
        video: { disable: true },
        audio: audio,
        output: { path: output },
      };
      notify('正在提交音频提取…');
      const response = await bridgeInvoke('media.toolkit.recipe.run', {
        workspace_root: state.workspaceRoot,
        recipe: recipe,
        kind: 'extract-audio',
      });
      beginJob(response && response.job);
      return;
    }

    const output = (el('video-out').value || '').trim() ||
      suggestOutput('-converted', videoExtension(el('video-format').value), path);
    const videoCodec = el('video-codec').value;
    const input = {
      workspace_root: state.workspaceRoot,
      path: path,
      output: output,
      format: el('video-format').value,
      video_codec: videoCodec,
      audio_codec: el('video-audio-codec').value,
      overwrite: el('video-overwrite').checked,
    };
    if (videoCodec !== 'copy') {
      const dimensions = requireCustomDimensions('video-size-preset', videoDimensions());
      const quality = videoQualitySettings();
      if (dimensions.width > 0) input.width = dimensions.width;
      if (dimensions.height > 0) input.height = dimensions.height;
      const fps = positiveNumber('video-fps');
      const videoBitrate = integerOrZero('video-bitrate');
      const audioBitrate = integerOrZero('video-audio-bitrate');
      if (fps > 0) input.fps = fps;
      if (videoBitrate > 0) input.video_bitrate_kbps = videoBitrate;
      else if (quality.crf > 0) input.crf = quality.crf;
      if (audioBitrate > 0) input.audio_bitrate_kbps = audioBitrate;
      input.preset = quality.speed;
    }
    notify('正在提交视频转换…');
    const response = await bridgeInvoke('media.toolkit.video.convert', input);
    beginJob(response && response.job);
  }

  async function runImagesToVideo() {
    const path = currentPath();
    if (!path) throw new Error('先选择一张图片或填写图片序列路径。');
    const output = (el('images-out').value || '').trim() || suggestOutput('-slideshow', '.mp4', path);
    const dimensions = requireCustomDimensions('images-size-preset', imagesDimensions());
    const input = {
      workspace_root: state.workspaceRoot,
      image_path: path,
      output: output,
      fps: positiveNumber('images-fps') || 30,
      overwrite: el('images-overwrite').checked,
    };
    const durationMs = Math.round(positiveNumber('images-duration') * 1000);
    const crf = integerOrZero('images-crf');
    if (durationMs > 0) input.duration_ms = durationMs;
    if (dimensions.width > 0) input.width = dimensions.width;
    if (dimensions.height > 0) input.height = dimensions.height;
    if (crf > 0) input.crf = crf;
    const audioPath = (el('images-audio').value || '').trim();
    if (audioPath) input.audio_path = audioPath;
    notify('正在提交图片转视频…');
    const response = await bridgeInvoke('media.toolkit.images.to-video', input);
    beginJob(response && response.job);
    const warnings = response && response.plan && response.plan.warnings;
    if (warnings && warnings.length) notify(warnings.join('；'), 'warn');
  }

  async function runAnalysis() {
    const path = currentPath();
    if (!path) throw new Error('先选择要分析的媒体文件。');
    notify('正在分析媒体参数…');
    const response = await bridgeInvoke('media.toolkit.media.analyze', {
      workspace_root: state.workspaceRoot,
      path: path,
    });
    if (!response || !response.report) throw new Error('插件没有返回分析结果。');
    renderAnalysis(response.report);
    notify('参数分析完成。', 'ok');
  }

  function renderAnalysis(report) {
    const rows = [
      ['文件', report.file_name || baseName(report.path || '')],
      ['类型', report.media_type || '—'],
      ['容器', report.container || '—'],
      ['时长', formatDuration(report.duration_ms)],
      ['体积', formatSize(report.size_bytes)],
      ['总码率', formatBitrate(report.bit_rate)],
    ];
    if (report.video) {
      rows.push({ group: '视频' });
      rows.push(['编码', report.video.codec || '—']);
      if (report.video.profile) rows.push(['编码档次', report.video.profile]);
      rows.push(['尺寸', report.video.width + '×' + report.video.height]);
      rows.push(['帧率', report.video.fps ? report.video.fps.toFixed(2) + ' fps' : '—']);
      rows.push(['码率', formatBitrate(report.video.bit_rate)]);
      rows.push(['像素格式', report.video.pixel_format || '—']);
    }
    if (report.audio) {
      rows.push({ group: '音频' });
      rows.push(['编码', report.audio.codec || '—']);
      rows.push(['采样率', report.audio.sample_rate ? report.audio.sample_rate + ' Hz' : '—']);
      rows.push(['声道', report.audio.channels ? report.audio.channels + ' 声道' : '—']);
      rows.push(['码率', formatBitrate(report.audio.bit_rate)]);
    }
    if ((report.warnings || []).length) rows.push(['提醒', report.warnings.join('；')]);
    const node = el('analysis-result');
    node.innerHTML = rowsTable(rows);
    node.hidden = false;
  }

  async function runCurrent() {
    if (!state.workspaceRoot) {
      notify('当前项目没有可用的文件目录。', 'bad');
      return;
    }
    setBusy(true);
    try {
      if (state.activeTab === 'image') await runImageConversion();
      else if (state.activeTab === 'video') await runVideoConversion();
      else if (state.activeTab === 'images') await runImagesToVideo();
      else if (state.activeTab === 'analyze') await runAnalysis();
    } catch (error) {
      notify('处理失败：' + describe(error), 'bad');
    } finally {
      setBusy(false);
    }
  }

  function renderJob(job) {
    if (!job) return;
    const box = el('current-job');
    box.hidden = false;
    el('job-title').textContent = jobKindText(job.kind);
    const stateNode = el('job-state');
    stateNode.textContent = jobStateText(job.status);
    stateNode.className = 'badge ' + jobStateTone(job.status);
    const percent = Math.max(0, Math.min(100, Number(job.progress || 0)));
    const bar = el('job-bar');
    bar.style.width = percent + '%';
    bar.className = 'bar-fill' + (job.status === 'completed' ? ' done' : (job.status === 'failed' ? ' bad' : (job.status === 'interrupted' ? ' warn' : '')));
    el('job-percent').textContent = percent.toFixed(1) + '%';
    el('job-speed').textContent = job.speed ? job.speed : '';
    el('job-output').textContent = job.error || job.output || '';
    el('cancel-button').hidden = terminal(job.status);
  }

  function jobKindText(kind) {
    return ({
      'video.convert': '视频转换',
      'images.to-video': '图片转视频',
      'extract-audio': '提取音轨',
      recipe: '媒体处理',
    })[kind] || (kind ? '处理 · ' + kind : '媒体处理');
  }

  function jobStateText(status) {
    return ({
      queued: '排队中', running: '处理中', completed: '已完成',
      failed: '失败', cancelled: '已取消', interrupted: '已中断',
    })[status] || (status || '—');
  }

  function jobStateTone(status) {
    if (status === 'completed') return 'ok';
    if (status === 'failed') return 'bad';
    if (status === 'cancelled' || status === 'interrupted') return 'warn';
    return 'muted';
  }

  function terminal(status) {
    return ['completed', 'failed', 'cancelled', 'interrupted'].includes(status);
  }

  function beginJob(job) {
    if (!job || !job.id) throw new Error('插件没有返回作业 ID。');
    state.job = job;
    renderJob(job);
    startPolling();
    notify('已提交：' + jobKindText(job.kind) + '。', 'ok');
    void loadHistory();
  }

  function startPolling() {
    stopPolling();
    state.timer = window.setInterval(() => { void pollJob(); }, 1000);
    void pollJob();
  }

  function stopPolling() {
    if (state.timer) window.clearInterval(state.timer);
    state.timer = null;
  }

  async function pollJob() {
    if (!state.job || !state.job.id) return;
    try {
      const response = await bridgeInvoke('media.toolkit.job.status', { job_id: state.job.id });
      const fresh = response && (response.job || response);
      if (!fresh) return;
      state.job = fresh;
      renderJob(fresh);
      if (terminal(fresh.status)) {
        stopPolling();
        if (fresh.status === 'completed') notify('处理完成', 'ok');
        if (fresh.status === 'failed') notify('处理失败：' + (fresh.error || '未知原因'), 'bad');
        if (fresh.status === 'cancelled') notify('处理已取消。', 'warn');
        if (fresh.status === 'interrupted') notify('上次处理因插件进程重启而中断。', 'warn');
        void loadHistory();
      }
    } catch (_) {
      // 短暂断线不打断作业，下一拍继续。
    }
  }

  async function loadHistory() {
    try {
      const result = await bridgeInvoke('media.toolkit.job.list', { limit: 12 });
      const jobs = (result && result.jobs) || [];
      const box = el('job-history');
      if (!jobs.length) {
        box.innerHTML = '<p class="history-empty">暂无任务</p>';
        return;
      }
      box.innerHTML = jobs.map((job) =>
        '<div class="history-item">' +
        '<span class="badge ' + jobStateTone(job.status) + '">' + escapeHtml(jobStateText(job.status)) + '</span>' +
        '<span class="grow mono">' + escapeHtml(job.output || job.input || job.id) + '</span>' +
        '<span class="muted">' + escapeHtml(jobKindText(job.kind)) + '</span>' +
        '</div>'
      ).join('');
    } catch (_) {
      // 历史不是关键路径。
    }
  }

  async function cancelJob() {
    if (!state.job || !state.job.id) return;
    setBusy(true);
    try {
      await bridgeInvoke('media.toolkit.job.cancel', { job_id: state.job.id });
      notify('已请求取消。', 'warn');
    } catch (error) {
      notify('取消失败：' + describe(error), 'bad');
    } finally {
      setBusy(false);
    }
  }

  function activateTab(tab) {
    state.activeTab = tab;
    for (const node of document.querySelectorAll('.tab')) {
      node.classList.toggle('is-active', node.dataset.tab === tab);
    }
    for (const node of document.querySelectorAll('.panel')) {
      node.classList.toggle('is-active', node.dataset.panel === tab);
    }
    const labels = {
      image: '开始转换',
      video: '开始转换',
      images: '生成视频',
      analyze: '分析参数',
    };
    el('run-button').textContent = labels[tab] || '开始处理';
    el('run-hint').textContent = '';
    renderSelection();
    prefillOutputs();
  }

  function bindEvents() {
    for (const tab of document.querySelectorAll('.tab')) {
      tab.addEventListener('click', () => activateTab(tab.dataset.tab));
    }
    el('runtime-button').addEventListener('click', () => { void loadRuntime(); });
    bindSelectionDropTarget();
    el('pick-media').addEventListener('click', () => { void pickMedia(); });
    el('pick-media-more').addEventListener('click', () => { void pickMedia(); });
    el('pick-audio').addEventListener('click', () => { void pickAudio(); });
    el('selection-list').addEventListener('click', (event) => {
      const remove = event.target.closest('[data-remove-path]');
      if (remove) removeSelection(remove.dataset.removePath);
    });
    el('run-button').addEventListener('click', () => { void runCurrent(); });
    el('refresh-jobs').addEventListener('click', () => { void loadHistory(); });
    el('cancel-button').addEventListener('click', () => { void cancelJob(); });

    el('image-output-dir').addEventListener('input', () => { markManual('image-output-dir'); });
    el('video-out').addEventListener('input', () => { markManual('video-out'); });
    el('images-out').addEventListener('input', () => { markManual('images-out'); });

    el('image-size-preset').addEventListener('change', syncImageSizePreset);
    el('image-quality-preset').addEventListener('change', syncImageQualityPreset);
    el('video-size-preset').addEventListener('change', syncVideoSizePreset);
    el('video-quality-preset').addEventListener('change', syncVideoQualityPreset);
    el('images-size-preset').addEventListener('change', syncImagesSizePreset);

    for (const id of ['image-width', 'image-height']) {
      el(id).addEventListener('input', () => { chooseCustom('image-size-preset', 'image-advanced'); });
    }
    el('image-quality').addEventListener('input', () => {
      chooseCustom('image-quality-preset', 'image-advanced');
      el('image-quality-label').textContent = el('image-quality').value;
    });
    for (const id of ['video-width', 'video-height']) {
      el(id).addEventListener('input', () => { chooseCustom('video-size-preset', 'video-advanced'); });
    }
    for (const id of ['video-crf', 'video-bitrate']) {
      el(id).addEventListener('input', () => {
        chooseCustom('video-quality-preset', 'video-advanced');
        if (id === 'video-crf' && positiveNumber(id) > 0) el('video-bitrate').value = '';
        if (id === 'video-bitrate' && positiveNumber(id) > 0) el('video-crf').value = '';
      });
    }
    for (const id of ['images-width', 'images-height']) {
      el(id).addEventListener('input', () => { chooseCustom('images-size-preset', 'images-advanced'); });
    }

    el('image-format').addEventListener('change', () => { prefillOutputs(); });
    el('video-format').addEventListener('change', () => { syncVideoCodecs(); prefillOutputs(); });
    el('video-codec').addEventListener('change', updateVideoPresetAvailability);
    el('audio-only').addEventListener('change', toggleAudioOnly);
    el('audio-format').addEventListener('change', () => { prefillOutputs(); });
  }

  async function boot() {
    bindEvents();
    void registerTauriDragEvents();
    syncVideoCodecs();
    syncImageSizePreset();
    syncImageQualityPreset();
    syncVideoSizePreset();
    syncVideoQualityPreset();
    syncImagesSizePreset();
    toggleAudioOnly();
    renderSelection();
    activateTab('image');
    await loadWorkspace();
    await loadHistory();
    void loadRuntime();
  }

  window.addEventListener('DOMContentLoaded', () => { void boot(); });
})();
