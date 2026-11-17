(function () {
  'use strict';
  const el = (id) => document.getElementById(id);
  const html = (text) => String(text || '').replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  window.MediaLibraryAudio = { create(options) {
    let item = null, key = '', busy = false, audition = false, generation = 0, auditionGeneration = 0, duration = 0, submitted = false;
    const player = () => el('preview-media').querySelector('video,audio');
    function feedback(text, error) { el('audio-feedback').textContent = text || ''; el('audio-feedback').hidden = !text; el('audio-feedback').className = 'inline-feedback' + (error ? ' error' : ''); }
    function range() {
      const start = Number(el('audio-start').value), end = el('audio-end').value.trim() ? Number(el('audio-end').value) : 0;
      const media = player(), duration = media && Number.isFinite(media.duration) ? media.duration : 0;
      if (!Number.isFinite(start) || !Number.isFinite(end) || start < 0 || (end && end - start < .1) || (!end && start > 0)) throw new Error('结束时间需晚于开始时间至少 0.1 秒。');
      if (duration && (start >= duration || (end && end > duration + .05))) throw new Error('片段范围超出素材时长，请调整开始和结束时间。');
      return {start_ms: Math.round(start * 1000), end_ms: Math.round(end * 1000)};
    }
    function setAudition(active) { audition = active; el('audio-audition').textContent = active ? '停止试听' : '试听片段'; }
    function stopAudition(text) { auditionGeneration++; setAudition(false); const media = player(); if (media) media.pause(); if (text !== undefined) feedback(text); }
    function changed() { key = ''; submitted = false; el('audio-submit').disabled = busy || !el('audio-library').value; stopAudition(''); syncSelection(); }
    function syncSelection() {
      const raw = duration || (player() && player().duration) || 0;
      const d = Number.isFinite(raw) ? raw : 0;
      const start = Number(el('audio-start').value) || 0, end = Number(el('audio-end').value) || d;
      el('audio-selection-duration').textContent = end > start ? '所选 ' + (end - start).toFixed(2) + ' 秒' : '完整音轨';
      if (!Number.isFinite(d) || d <= 0) return;
      el('audio-wave-start').value = String(Math.max(0, Math.min(1000, start / d * 1000)));
      el('audio-wave-end').value = String(Math.max(0, Math.min(1000, end / d * 1000)));
      el('audio-wave-selection').style.left = Math.max(0, Math.min(100, start / d * 100)) + '%';
      el('audio-wave-selection').style.width = Math.max(0, Math.min(100, (end - start) / d * 100)) + '%';
    }
    async function preview(next) {
      const token = ++generation; item = next; key = ''; submitted = false; duration = 0; wavePeaks = []; stopAudition('');
      el('audio-waveform').hidden = true; el('audio-wave-status').textContent = '';
      el('audio-editor').open = false;
      el('audio-editor').hidden = !next || next.origin === 'online' || next.origin === 'platform' || !next.id || !['video','audio'].includes(next.kind);
      if (el('audio-editor').hidden) return;
      el('audio-editor-summary').textContent = next.kind === 'audio' ? '裁剪音频并入库' : '提取音频';
      el('audio-title').value = (next.title || next.name || '音频素材') + ' · BGM';
      el('audio-start').value = '0'; el('audio-end').value = ''; el('audio-format').value = 'wav'; el('audio-submit').disabled = true;
      el('audio-loop-selection').checked = false; syncSelection();
      if (options.waveform) {
        el('audio-wave-status').textContent = '正在生成波形…';
        void options.waveform(next).then(data => {
          if (token !== generation) return;
          if (!data || !data.peaks || !data.peaks.length) { el('audio-wave-status').textContent = '波形暂不可用，可用时间输入和播放器标记继续试听。'; return; }
          duration = data.duration_ms / 1000; el('audio-waveform').hidden = false; el('audio-wave-status').textContent = '拖动上下手柄选择起止位置，点击波形定位播放。';
          wavePeaks = data.peaks; syncSelection(); drawWave();
        });
      }
      try {
        const result = await options.invoke('media-library.repository.list', {}); if (token !== generation) return;
        const writable = (result.libraries || []).filter((l) => l.state === 'online' && l.backend_type !== 'smb' && !['read_only','readonly'].includes(l.access_mode));
        el('audio-library').innerHTML = writable.length ? writable.map((l) => '<option value="' + html(l.id) + '">' + html(l.name) + '</option>').join('') : '<option value="">没有可写的本机资源库</option>';
        if (writable.some((l) => l.id === options.defaultLibrary())) el('audio-library').value = options.defaultLibrary();
        el('audio-submit').disabled = busy || !writable.length;
        if (!writable.length) feedback('请先在设置中创建可写的本机资源库。', true);
      } catch (error) { if (token === generation) feedback(options.describe(error), true); }
    }
    function ready() {
      const media = player(); if (!media) return;
      const metadata = () => { if (media === player() && Number.isFinite(media.duration)) { duration = media.duration; syncSelection(); } };
      media.addEventListener('loadedmetadata', metadata); metadata();
      media.addEventListener('timeupdate', () => { const end = Number(el('audio-end').value); if (audition && media === player() && end && media.currentTime >= end) { if (el('audio-loop-selection').checked) media.currentTime = Number(el('audio-start').value) || 0; else stopAudition('片段试听结束。'); } });
      media.addEventListener('ended', () => { if (audition && media === player()) { if (el('audio-loop-selection').checked) { media.currentTime = Number(el('audio-start').value) || 0; void media.play().catch(() => stopAudition('试听已暂停，请重试。')); } else stopAudition('试听结束。'); } });
      media.addEventListener('pause', () => { if (audition && media === player()) { auditionGeneration++; setAudition(false); feedback('试听已暂停。'); } });
    }
    let wavePeaks = [];
    function drawWave() { if (options.drawWave && !el('audio-waveform').hidden) options.drawWave(el('audio-wave-canvas'), wavePeaks); }
    new ResizeObserver(drawWave).observe(el('audio-waveform'));
    ['audio-wave-start','audio-wave-end'].forEach(id => el(id).addEventListener('input', () => {
      if (!duration) return;
      let start = Number(el('audio-wave-start').value) / 1000 * duration, end = Number(el('audio-wave-end').value) / 1000 * duration;
      if (end - start < .1) { if (id === 'audio-wave-start') start = Math.max(0,end - .1); else end = Math.min(duration,start + .1); }
      const bounded = window.MediaLibraryPreview.boundedRange(start, end, duration);
      if (!bounded) return;
      el('audio-start').value = bounded.start.toFixed(2); el('audio-end').value = bounded.end.toFixed(2); changed();
    }));
    el('audio-wave-canvas').addEventListener('click', event => { const media = player(); if (!media || !duration) return; const rect = event.currentTarget.getBoundingClientRect(); media.currentTime = Math.max(0, Math.min(duration, (event.clientX - rect.left) / rect.width * duration)); });
    ['audio-title','audio-start','audio-end','audio-format','audio-library'].forEach((id) => el(id).addEventListener('input', changed));
    el('audio-mark-start').addEventListener('click', () => { const media = player(); if (!media) return; el('audio-start').value = media.currentTime.toFixed(2); changed(); });
    el('audio-mark-end').addEventListener('click', () => { const media = player(); if (!media) return; el('audio-end').value = media.currentTime.toFixed(2); changed(); });
    el('audio-audition').addEventListener('click', async () => {
      if (audition) { stopAudition('试听已停止。'); return; }
      const token = ++auditionGeneration;
      try {
        const r = range(), media = player();
        if (!media || media.readyState < 1) throw new Error('请先等待素材预览加载完成。');
        media.loop = false; media.currentTime = r.start_ms / 1000; setAudition(true); feedback('正在试听所选音轨。');
        await media.play();
        if (token !== auditionGeneration && !audition) media.pause();
      } catch (error) { if (token === auditionGeneration) { setAudition(false); feedback(options.describe(error), true); } }
    });
    el('audio-extract-form').addEventListener('submit', async (event) => {
      event.preventDefault(); if (busy || submitted || !item || !event.currentTarget.reportValidity()) return;
      const source = item, token = generation;
      try {
        const input = Object.assign(range(), {asset_id:source.id, library_id:el('audio-library').value, title:el('audio-title').value.trim(), format:el('audio-format').value, idempotency_key:key || (key = crypto.randomUUID())});
        busy = true; el('audio-submit').disabled = true; el('audio-submit').textContent = '正在创建…'; feedback('正在准备后台音频任务…');
        const job = await options.invoke('media-library.audio.start', input);
        if (token === generation) { submitted = true; feedback('音频任务已加入后台，可继续浏览。'); }
        options.started(job, source);
      } catch (error) { if (token === generation) feedback(options.describe(error), true); }
      finally { busy = false; if (token === generation) { el('audio-submit').textContent = '提取并入库'; el('audio-submit').disabled = submitted || !el('audio-library').value; } else { el('audio-submit').disabled = !el('audio-library').value; } }
    });
    return {preview, ready};
  }};
})();
