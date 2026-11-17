const el = (id) => document.getElementById(id);
const state = { workspaceRoot: '', video: null, durationMs: 0, times: [], framePaths: [], selectedIndex: -1, rendering: false };

function invokeCapability(capabilityId, input = {}) {
  const internals = window.__TAURI_INTERNALS__ || {};
  if (typeof internals.invoke !== 'function') return Promise.reject(new Error('当前不在 HiMind Agent 扩展视图窗口中'));
  return internals.invoke('invoke_plugin_view_capability', { capabilityId, input });
}

function fileBase(path) { return String(path || '').split(/[\\/]/).pop() || ''; }
function formatDuration(ms) { const sec = Math.max(0, Math.round(Number(ms || 0) / 1000)); return Math.floor(sec / 60) + ':' + String(sec % 60).padStart(2, '0'); }
function setStatus(text, bad = false) { const node = el('status'); node.textContent = text; node.classList.toggle('error', Boolean(bad)); }
function setBusy(value) { state.rendering = Boolean(value); el('pick-video').disabled = value; el('load-frames').disabled = value || !state.video; el('render-cover').disabled = value || !state.video || state.selectedIndex < 0 || !el('title').value.trim(); }
function extractDuration(result) { return Number(result?.duration_ms || result?.video?.duration_ms || result?.media?.duration_ms || 0); }
function describe(error) { return String(error?.message || error || '未知错误'); }

async function loadWorkspace() {
  try {
    const context = await window.__TAURI_INTERNALS__.invoke('get_plugin_view_context', {});
    state.workspaceRoot = context?.workspace_root || '';
    el('workspace-status').textContent = state.workspaceRoot ? '本机服务已连接' : '没有可用工作区';
  } catch (_) {
    el('workspace-status').textContent = '本机服务连接失败';
  }
}

async function pickVideo() {
  if (!state.workspaceRoot) return setStatus('没有可用工作区。', true);
  setBusy(true);
  try {
    const picked = await invokeCapability('media.toolkit.files.pick', { workspace_root: state.workspaceRoot, mode: 'video', multiple: false });
    const file = picked?.files?.[0];
    if (!picked || picked.canceled || !file) {
      setBusy(false);
      setStatus('未选择视频。');
      return;
    }
    state.video = file;
    state.times = []; state.framePaths = []; state.selectedIndex = -1;
    el('source-card').hidden = false;
    el('source-name').textContent = file.name || fileBase(file.path);
    const probe = await invokeCapability('media.toolkit.probe', { workspace_root: state.workspaceRoot, path: file.path });
    state.durationMs = extractDuration(probe);
    el('source-meta').textContent = state.durationMs ? `时长 ${formatDuration(state.durationMs)} · 可生成候选帧` : '已选择视频';
    el('load-frames').disabled = false;
    el('frame-grid').innerHTML = '';
    setBusy(false);
    setStatus('视频已就绪，下一步生成候选画面。');
  } catch (error) { setStatus('选择视频失败：' + describe(error), true); setBusy(false); }
}

function candidateTimes() {
  const duration = state.durationMs || 10000;
  return Array.from({ length: 9 }, (_, i) => Math.max(0, Math.min(duration - 20, Math.round(duration * (0.1 + i * 0.1)))));
}

async function loadFrames() {
  if (!state.video) return;
  setBusy(true);
  setStatus('正在生成候选画面联系表…');
  try {
    const interval = Math.max(1, Math.floor((state.durationMs || 10000) / 9));
    const stamp = Date.now();
    const result = await invokeCapability('media.toolkit.frames.contact-sheet', {
      workspace_root: state.workspaceRoot,
      path: state.video.path,
      output: `.himind/video-cover/candidates/${stamp}-contact.jpg`,
      columns: 3,
      rows: 3,
      interval_ms: interval,
      tile_width: 480,
      quality: 3
    });
    if (!result?.path) throw new Error('联系表生成失败');
    const preview = await invokeCapability('media.toolkit.preview.image', { workspace_root: state.workspaceRoot, path: result.path });
    state.times = Array.from({ length: 9 }, (_, i) => Math.min(Math.max(0, state.durationMs - 20), i * (result.interval_ms || interval)));
    state.sheetPath = result.path;
    state.selectedIndex = -1;
    el('frame-grid').innerHTML = `<div class="contact-sheet-wrap"><img src="${preview.data_url}" alt="九宫格候选画面">${state.times.map((time, i) => `<button class="sheet-cell" data-index="${i}" aria-label="选择第 ${i+1} 格，${formatDuration(time)}"></button>`).join('')}</div>`;
    [...el('frame-grid').querySelectorAll('.sheet-cell')].forEach((node) => node.addEventListener('click', () => selectFrame(Number(node.dataset.index))));
    el('focus-field').hidden = false;
    setBusy(false);
    setStatus('请点击九宫格里的任一画面完成选帧。');
  } catch (error) { setStatus('生成候选画面失败：' + describe(error), true); setBusy(false); }
}

function selectFrame(index) {
  if (index < 0 || index >= state.times.length) return;
  state.selectedIndex = index;
  [...el('frame-grid').querySelectorAll('.sheet-cell')].forEach((node, i) => node.classList.toggle('selected', i === index));
  setBusy(false);
  setStatus(`已选择第 ${index + 1} 格，约 ${formatDuration(state.times[index])}。`);
}

async function renderCover() {
  if (state.selectedIndex < 0) return setStatus('请先选择一帧候选画面。', true);
  if (!el('title').value.trim()) return setStatus('主标题不能为空。', true);
  setBusy(true); setStatus('正在抽取高分辨率画面并渲染封面…');
  try {
    const stamp = Date.now();
    const extracted = await invokeCapability('media.toolkit.frames.extract', {
      workspace_root: state.workspaceRoot, path: state.video.path,
      output_dir: `.himind/video-cover/source/${stamp}`, times_ms: [state.times[state.selectedIndex]], width: 2160, format: 'png'
    });
    const source = extracted?.files?.[0];
    if (!source) throw new Error('未能抽取高分辨率画面');
    const result = await invokeCapability('media.cover.layout.render', {
      workspace_root: state.workspaceRoot,
      source_image: source,
      output_dir: `.himind/video-cover/outputs/${stamp}`,
      filename: fileBase(state.video.name || state.video.path).replace(/\.[^.]+$/, '') + '-cover',
      template_id: 'wanmei-d-v1',
      title: el('title').value.trim(),
      detail: el('detail').value.trim(),
      focus_x: 0.5,
      focus_y: Number(el('focus-y').value) / 100,
      outputs: [
        { width: 1080, height: 1440, format: 'png' },
        { width: 1728, height: 2304, format: 'png' }
      ],
      inline: true
    });
    if (!result?.preview_data_url) throw new Error('渲染结果缺少预览');
    el('cover-placeholder').hidden = true;
    el('cover-preview').src = result.preview_data_url; el('cover-preview').hidden = false;
    el('mini-cover-image').src = result.preview_data_url; el('mobile-check').hidden = false;
    el('quality-row').hidden = false;
    el('safe-score').textContent = String(result.quality?.title_safe_score ?? '--');
    el('title-lines').textContent = String(result.quality?.title_lines ?? '--');
    el('detail-lines').textContent = String(result.quality?.detail_lines ?? '--');
    el('outputs').innerHTML = (result.files || []).map((file) => `<div class="output-item"><strong>${file.width}×${file.height} · ${file.format.toUpperCase()}</strong><br>${file.path}</div>`).join('');
    setBusy(false);
    const warnings = result.warnings || [];
    setStatus(warnings.length ? '封面已生成；注意：' + warnings.join('；') : '封面已生成。请检查标题可读性、画面焦点和列表效果。', warnings.length > 0);
  } catch (error) { setStatus('生成封面失败：' + describe(error), true); setBusy(false); }
}

function updateCount(input, output) { el(output).textContent = el(input).value.length + ' / ' + el(input).maxLength; setBusy(state.rendering); }

el('pick-video').addEventListener('click', pickVideo);
el('load-frames').addEventListener('click', loadFrames);
el('render-cover').addEventListener('click', renderCover);
el('title').addEventListener('input', () => updateCount('title','title-count'));
el('detail').addEventListener('input', () => updateCount('detail','detail-count'));
el('focus-y').addEventListener('input', () => { const v=Number(el('focus-y').value); el('focus-value').textContent=v===50?'居中':v<50?'偏上':'偏下'; });
loadWorkspace();
