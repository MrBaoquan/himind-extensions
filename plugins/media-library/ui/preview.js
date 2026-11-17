(function () {
  'use strict';
  const html = value => String(value == null ? '' : value).replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
  const identity = item => item ? [item.origin || item.entry_type, item.provider || '', item.id || item.source_url || item.path].join(':') : '';
  const clock = value => { const n = Math.max(0, Number(value) || 0); return Math.floor(n / 60) + ':' + String(Math.floor(n % 60)).padStart(2, '0'); };
  function boundedRange(start, end, duration) {
    const d = Number(duration);
    if (!Number.isFinite(d) || d < .1) return null;
    const a = Math.min(Math.max(Number(start) || 0, 0), Math.max(0, d - .1));
    const b = Math.min(Math.max(Number(end) || d, a + .1), d);
    return {start: a, end: b, duration: b - a};
  }
  function safeLink(url, label) {
    try { const u = new URL(url); if (!['http:', 'https:'].includes(u.protocol) || u.username || u.password) return ''; return '<a href="' + html(u.href) + '" target="_blank" rel="noopener noreferrer">' + html(label) + '</a>'; } catch (_) { return ''; }
  }
  window.MediaLibraryPreview = { identity, boundedRange, create(options) {
    const el = id => document.getElementById(id);
    const sheet = el('preview'), host = el('preview-media');
    const comments = window.MediaLibraryComments.create({invoke:options.invoke, describe:options.describe});
    let current = null, media = null, list = [], generation = 0, activeTab = 'info', dirty = false;
    let zoom = 1, panX = 0, panY = 0, fit = true, drag = null;
    let volume = 1, muted = false, speed = 1, loop = false, frames = [];
    const drafts = new Map(), waves = new Map(), tasks = new Map();
    try { for (const [key, draft] of JSON.parse(sessionStorage.getItem('himind.media.preview-drafts') || '[]')) drafts.set(key, draft); } catch (_) {}
    function capture() {
      if (!current || !dirty || el('user-state').hidden) return;
      drafts.set(identity(current), {tags: el('asset-tags').value, rating: el('asset-rating').value, note: el('asset-note').value});
      while (drafts.size > 40) drafts.delete(drafts.keys().next().value);
      try { sessionStorage.setItem('himind.media.preview-drafts', JSON.stringify([...drafts])); } catch (_) {}
    }
    function refreshMarkers() {
      const tags = [...new Set(el('asset-tags').value.split(/[,，、;；]/).map(s => s.trim()).filter(Boolean))].slice(0, 20);
      el('asset-tag-chips').innerHTML = tags.map((tag, i) => '<button type="button" data-remove-tag="' + i + '" title="移除标签">' + html(tag) + ' ×</button>').join('');
      const rating = Number(el('asset-rating').value);
      el('asset-stars').innerHTML = [1,2,3,4,5].map(n => '<button type="button" data-rating="' + n + '" class="' + (n <= rating ? 'selected' : '') + '" aria-label="' + n + '星" aria-pressed="' + (n === rating) + '">★</button>').join('') + '<button type="button" data-rating="0" aria-label="清除评分">×</button>';
    }
    function edited() { dirty = true; el('user-state-feedback').textContent = '未保存 · 切换素材会保留草稿'; refreshMarkers(); capture(); }
    ['asset-tags','asset-rating','asset-note'].forEach(id => el(id).addEventListener('input', edited));
    el('asset-tag-chips').addEventListener('click', event => { const b = event.target.closest('[data-remove-tag]'); if (!b) return; const tags = el('asset-tags').value.split(/[,，、;；]/).map(s => s.trim()).filter(Boolean); tags.splice(Number(b.dataset.removeTag), 1); el('asset-tags').value = tags.join(', '); edited(); });
    el('asset-stars').addEventListener('click', event => { const b = event.target.closest('[data-rating]'); if (!b) return; el('asset-rating').value = b.dataset.rating; edited(); });
    function userLoaded(item, payload) {
      if (identity(item) !== identity(current)) return;
      const draft = drafts.get(identity(item));
      if (!dirty) {
        el('asset-tags').value = draft ? draft.tags : (payload.tags || []).join(', ');
        el('asset-rating').value = draft ? draft.rating : String(payload.rating || 0);
        el('asset-note').value = draft ? draft.note : payload.note || '';
        dirty = Boolean(draft);
      }
      refreshMarkers(); if (dirty) el('user-state-feedback').textContent = '有未保存的草稿';
    }
    function saved(item, savedValues) {
      if (identity(current) !== identity(item)) {
        const old = drafts.get(identity(item));
        if (old && ['tags','rating','note'].every(k => old[k] === savedValues[k])) {
          drafts.delete(identity(item));
          try { sessionStorage.setItem('himind.media.preview-drafts', JSON.stringify([...drafts])); } catch (_) {}
        }
        return;
      }
      const unchanged = el('asset-tags').value === savedValues.tags && el('asset-rating').value === savedValues.rating && el('asset-note').value === savedValues.note;
      dirty = !unchanged;
      if (unchanged) { drafts.delete(identity(item)); try { sessionStorage.setItem('himind.media.preview-drafts', JSON.stringify([...drafts])); } catch (_) {} }
      else capture();
      refreshMarkers(); el('user-state-feedback').textContent = unchanged ? '已保存' : '已保存提交的内容 · 还有新修改未保存';
    }
    function showInfo(open) {
      sheet.classList.toggle('info-collapsed', !open);
      el('preview-toggle-info').textContent = open ? '收起信息' : '显示信息';
      el('preview-toggle-info').setAttribute('aria-expanded', String(open));
      if (fit) requestAnimationFrame(fitImage);
    }
    function tab(name) {
      activeTab = name; el('preview-adoption-panel').hidden = true;
      document.querySelectorAll('[data-preview-tab]').forEach(b => { const on = b.dataset.previewTab === name; b.classList.toggle('active', on); b.setAttribute('aria-selected', String(on)); });
      document.querySelectorAll('[data-preview-panel]').forEach(p => { p.hidden = p.dataset.previewPanel !== name; });
      showInfo(true);
      if (name === 'comments') void comments.activate();
    }
    document.querySelectorAll('[data-preview-tab]').forEach(b => b.addEventListener('click', () => tab(b.dataset.previewTab)));
    el('preview-toggle-info').addEventListener('click', () => showInfo(sheet.classList.contains('info-collapsed')));
    el('preview-fullscreen').addEventListener('click', async () => { try { if (document.fullscreenElement) await document.exitFullscreen(); else await sheet.requestFullscreen(); } catch (_) { options.feedback('当前窗口无法进入全屏，请最大化窗口查看。', 'warn'); } });
    function strip(index) {
      const panel = el('preview-filmstrip'); panel.hidden = list.length < 2;
      const begin = Math.max(0, Math.min(index - 5, list.length - 12)), end = Math.min(list.length, begin + 12);
      panel.innerHTML = list.slice(begin, end).map((item, offset) => { const src = options.thumbnail(item); return '<button type="button" data-preview-index="' + (begin + offset) + '" aria-current="' + (begin + offset === index) + '" title="' + html(item.title || item.name || '素材') + '">' + (src ? '<img src="' + html(src) + '" alt="" loading="lazy" />' : '<span>' + html(item.title || item.name || item.kind) + '</span>') + '</button>'; }).join('');
    }
    el('preview-filmstrip').addEventListener('click', event => { const b = event.target.closest('[data-preview-index]'); if (b) options.navigate(Number(b.dataset.previewIndex)); });
    function sources(item) {
      const rows = [ ['原始作品', safeLink(item.source_url, '打开原作品')], ['作者', html(item.author || item.uploader || '')], ['许可', safeLink(item.license_url, item.license || item.license_name || '查看许可') || html(item.license || item.license_name || '许可未标注')], ['文件位置', item.path ? '<span class="path-text">' + html(item.path) + '</span><button type="button" class="path-copy" data-copy-path="' + html(item.path) + '">复制</button>' : ''], ['分类路径', html(item.collection || item.folder || '')] ];
      el('preview-source-meta').innerHTML = rows.filter(r => r[1]).map(r => '<dt>' + r[0] + '</dt><dd>' + r[1] + '</dd>').join('');
      el('preview-engagement').innerHTML = options.engagement(item);
    }
    function begin(item, index, items, switching) {
      ['edit-image','image-video'].forEach(action => el('preview-'+action).hidden = item.kind !== 'image' || !item.id || ['online','platform','generation-reference'].includes(item.origin));
      const originPanel = document.getElementById('asset-generation-origin'); if(originPanel) originPanel.remove();
      if(item.id && item.entry_type === 'library') void options.invoke('media-library.asset.lineage',{asset_id:item.id}).then(data=>{
        if(identity(current)!==identity(item) || !data.items || !data.items.length)return;
        const panel=document.createElement('section');panel.id='asset-generation-origin';panel.className='generation-lineage';
        const title=document.createElement('h3');title.textContent='来源与加工记录';panel.append(title);
        data.items.forEach(record=>{
          if(record.schema_version==='himind.image-edit-manifest.v1'){
            const input=record.input||{},params=input.options||{},chain=input.version_chain||[];
            const heading=document.createElement('p');heading.textContent='图片编辑 · '+(input.generation_id?'AI 改图':'媒体处理工具');panel.append(heading);
            const editedAt=chain[0]?.created_at||record.created_at;
            const info=document.createElement('p');info.className='meta';info.textContent=[editedAt?new Date(editedAt).toLocaleString('zh-CN'):'',input.width&&input.height?input.width+'×'+input.height:'',chain.length>1?(chain.length-1)+' 步编辑':'','原图保留'].filter(Boolean).join(' · ');panel.append(info);
            const descriptions=[params.crop?'裁剪 '+params.crop.width+'×'+params.crop.height:'',params.rotate?'旋转 '+params.rotate+'°':'',params.flip_h?'水平翻转':'',params.flip_v?'垂直翻转':'',params.width?'输出宽 '+params.width:'',params.height?'输出高 '+params.height:'',params.format?'格式 '+params.format.toUpperCase():'',params.format!=='png'&&params.quality?'质量 '+params.quality:''].filter(Boolean);
            const details=document.createElement('details'),summary=document.createElement('summary'),parameters=document.createElement('p');summary.textContent='处理参数';parameters.className='meta';parameters.textContent=descriptions.join(' · ')||'保留当前版本';details.append(summary,parameters);panel.append(details);
            if(input.generation_id){const button=document.createElement('button');button.type='button';button.className='ghost';button.textContent='查看改图任务';button.onclick=()=>options.openGeneration(input.generation_id,false);panel.append(button);}
            return;
          }
          const p=document.createElement('p');p.textContent=(record.execution && record.execution.model ? record.execution.model+' · ' : '')+(record.input.prompt||record.input.text||'');panel.append(p);
          const info=document.createElement('p');info.className='meta';const input=record.input||{};info.textContent=[record.created_at?new Date(record.created_at).toLocaleString('zh-CN'):'',input.size,input.ratio,input.duration_seconds?input.duration_seconds+' 秒':'',(record.references||[]).length+' 张参考图'].filter(Boolean).join(' · ');panel.append(info);
          if(record.session_id){const actions=document.createElement('div');actions.className='generation-actions';[['查看这次生成',false],['复用参数',true]].forEach(([label,reuse])=>{const button=document.createElement('button');button.type='button';button.className='ghost';button.textContent=label;button.onclick=()=>options.openGeneration(record.session_id,reuse);actions.append(button);});panel.append(actions);}
          (record.references||[]).forEach((reference,index)=>{const button=document.createElement('button');button.type='button';button.className='ghost';button.textContent='查看参考图 '+(index+1);button.onclick=()=>options.previewReference(record,index);panel.append(button);});
          const names={n:'数量',size:'尺寸',ratio:'比例',duration_seconds:'时长（秒）',voice:'音色',speed:'语速',format:'格式',quality:'质量',resolution:'分辨率'};
          const details=document.createElement('details'),summary=document.createElement('summary'),parameters=document.createElement('p');summary.textContent='完整参数';parameters.className='meta';parameters.textContent=Object.entries(input).filter(([key])=>names[key]).map(([key,value])=>names[key]+'：'+value).join(' · ')||'使用模型默认参数';details.append(summary,parameters);panel.append(details);
        });el('preview-source-panel').append(panel);
      }).catch(()=>{});
      capture(); generation++; current = item; list = items; media = null; frames = []; dirty = false;
      zoom = 1; panX = panY = 0; fit = true; drag = null;
      el('preview-tools').hidden = true; el('preview-show-jobs').hidden = true; el('preview-job-library').hidden = true;
      el('preview-adopt-action').hidden = ['platform','generation','generation-reference'].includes(item.origin) || item.entry_type === 'library';
      el('preview-audio-action').hidden = ['online','platform','generation','generation-reference'].includes(item.origin) || !['audio','video'].includes(item.kind) || !item.id;
      el('preview-organize-empty').hidden = item.entry_type === 'candidate' || !el('preview-audio-action').hidden;
      el('preview-organize-empty').textContent = item.entry_type === 'library' ? '此入库资源暂不支持修改标记；来源与加工记录可在“来源”查看。' : '此在线作品暂不支持编辑本地标记。';
      el('remote-preview-bookmark').disabled = false; el('remote-preview-bookmark').textContent = '收藏作品';
      el('preview-adoption-panel').hidden = true;
      document.querySelectorAll('[data-preview-panel]').forEach(p => { p.hidden = p.dataset.previewPanel !== activeTab; });
      if (!switching) showInfo(innerWidth >= 1060);
      strip(index); sources(item); userLoaded(item, item);
      comments.begin(item); if (activeTab === 'comments') void comments.activate();
    }
    function fitImage() {
      const img = host.querySelector('img'); if (!img || !img.naturalWidth) return;
      zoom = Math.min(host.clientWidth / img.naturalWidth, host.clientHeight / img.naturalHeight, 1); panX = panY = 0; fit = true; renderImage();
    }
    function renderImage() {
      const img = host.querySelector('img'); if (!img) return;
      const maxX = Math.max(0, (img.naturalWidth * zoom - host.clientWidth) / 2), maxY = Math.max(0, (img.naturalHeight * zoom - host.clientHeight) / 2);
      panX = Math.max(-maxX, Math.min(maxX, panX)); panY = Math.max(-maxY, Math.min(maxY, panY));
      img.style.width = img.naturalWidth + 'px'; img.style.height = img.naturalHeight + 'px'; img.style.transform = 'translate(' + panX + 'px,' + panY + 'px) scale(' + zoom + ')';
      el('preview-zoom').textContent = Math.round(zoom * 100) + '%';
    }
    function scale(factor) { fit = false; zoom = Math.max(.005, Math.min(8, zoom * factor)); renderImage(); }
    el('preview-fit').addEventListener('click', fitImage);
    el('preview-actual').addEventListener('click', () => { fit = false; zoom = 1; panX = panY = 0; renderImage(); });
    el('preview-zoom-in').addEventListener('click', () => scale(1.25)); el('preview-zoom-out').addEventListener('click', () => scale(.8));
    host.addEventListener('wheel', event => { if (current && current.kind === 'image') { event.preventDefault(); scale(event.deltaY < 0 ? 1.1 : 1 / 1.1); } }, {passive: false});
    host.addEventListener('dblclick', () => { if (current && current.kind === 'image') { if (fit) { fit = false; zoom = 1; renderImage(); } else fitImage(); } });
    host.addEventListener('pointerdown', event => { if (event.button !== 0 || !event.target.matches('img')) return; drag = {x: event.clientX, y: event.clientY, px: panX, py: panY}; event.target.setPointerCapture(event.pointerId); event.target.classList.add('dragging'); event.preventDefault(); });
    host.addEventListener('pointermove', event => { if (!drag) return; panX = drag.px + event.clientX - drag.x; panY = drag.py + event.clientY - drag.y; renderImage(); });
    ['pointerup','pointercancel'].forEach(name => host.addEventListener(name, () => { drag = null; const img = host.querySelector('img'); if (img) img.classList.remove('dragging'); }));
    new ResizeObserver(() => { if (fit) fitImage(); drawAudioWave(); }).observe(host);
    function drawWave(canvas, values) {
      if (!canvas || !values.length || !canvas.clientWidth) return;
      const ratio = Math.min(devicePixelRatio || 1, 2), width = canvas.clientWidth, height = canvas.clientHeight || 96;
      canvas.width = width * ratio; canvas.height = height * ratio; const ctx = canvas.getContext('2d'); ctx.scale(ratio, ratio); ctx.clearRect(0,0,width,height);
      ctx.fillStyle = getComputedStyle(sheet).getPropertyValue('--color-primary').trim() || '#2563eb';
      const step = width / values.length; values.forEach((n,i) => { const h = Math.max(1, n * (height - 8)); ctx.fillRect(i * step, (height - h) / 2, Math.max(1,step * .8), h); });
    }
    function drawAudioWave() { drawWave(el('preview-audio-canvas'), frames); }
    async function waveform(item) {
      if (!item || ['online','platform','generation'].includes(item.origin) || !item.id || !['audio','video'].includes(item.kind)) return null;
      const key = [identity(item), item.bytes, item.modified_at].join('|'); if (waves.has(key)) return waves.get(key);
      const promise = options.invoke('media-library.audio.waveform', {asset_id: item.id}).catch(() => { waves.delete(key); return null; });
      waves.set(key, promise); while (waves.size > 12) waves.delete(waves.keys().next().value);
      return promise;
    }
    function updateTime() { if (!media) return; el('preview-time').textContent = clock(media.currentTime) + ' / ' + clock(media.duration); el('preview-play').textContent = media.paused ? '播放' : '暂停'; }
    function ready(item) {
      const token = generation; media = host.querySelector('video,audio');
      jobsChanged([]);
      el('preview-tools').hidden = false; el('preview-image-tools').hidden = item.kind !== 'image'; el('preview-play-tools').hidden = item.kind === 'image';
      if (item.kind === 'image') { const img = host.querySelector('img'); img.draggable = false; img.addEventListener('load', fitImage, {once: true}); if (img.complete) fitImage(); return; }
      if (!media) return;
      media.volume = volume; media.muted = muted; media.playbackRate = speed; media.loop = loop;
      const player = media;
      for (const name of ['timeupdate','loadedmetadata','play','pause','ended']) player.addEventListener(name, () => { if (token === generation) updateTime(); });
      player.addEventListener('volumechange', () => { if (token !== generation) return; volume = player.volume; muted = player.muted; });
      updateTime();
      if (item.kind === 'audio') {
        const art = host.querySelector('.audio-art'); if (art) { art.className = 'preview-audio-wave'; art.innerHTML = '<canvas id="preview-audio-canvas" class="preview-audio-wave"></canvas>'; }
        void waveform(item).then(data => { if (token !== generation) return; frames = data && data.peaks || []; if (frames.length) drawAudioWave(); else if (art) { art.textContent = '音频预览 · 可直接播放'; art.className = 'meta'; } });
      }
    }
    async function play() { if (!media) return; try { if (media.paused) await media.play(); else media.pause(); } catch (_) { options.feedback('暂时无法播放，请重试或打开所在位置。', 'warn'); } }
    el('preview-play').addEventListener('click', () => { void play(); });
    el('preview-back').addEventListener('click', () => { if (media) media.currentTime = Math.max(0, media.currentTime - 10); });
    el('preview-forward').addEventListener('click', () => { if (media && Number.isFinite(media.duration)) media.currentTime = Math.min(media.duration, media.currentTime + 10); });
    el('preview-speed').addEventListener('change', () => { speed = Number(el('preview-speed').value) || 1; if (media) media.playbackRate = speed; });
    el('preview-loop').addEventListener('click', () => { loop = !loop; el('preview-loop').setAttribute('aria-pressed', String(loop)); if (media) media.loop = loop; });
    document.addEventListener('keydown', event => {
      if (sheet.hidden || !el('intake-modal').hidden || !el('settings-modal').hidden || !el('confirm-modal').hidden) return;
      if (event.target.closest('input,textarea,select,button,a,video,audio,[contenteditable="true"]')) return;
      if (event.code === 'Space' && media) { event.preventDefault(); void play(); }
    });
    const form = el('adopt-form'); el('preview-adopt-slot').append(form, el('redistribution-notice'), el('adopt-button'));
    el('preview-adopt-action').addEventListener('click', () => { showInfo(true); document.querySelectorAll('[data-preview-panel]').forEach(p => { p.hidden = true; }); el('preview-adoption-panel').hidden = false; el('adopt-name').focus(); });
    el('preview-audio-action').addEventListener('click', () => { tab('organize'); el('audio-editor').open = true; el('audio-start').focus(); });
    el('preview-show-jobs').addEventListener('click', () => options.jobs());
    let finishedLibrary = '';
    function jobsChanged(jobs) {
      const tracked = tasks.get(identity(current));
      if (!tracked) return;
      const job = jobs.find(j => j.job_id === tracked.job_id) || tracked;
      tasks.set(identity(current), job);
      const names = {queued:'排队中',resolving:'正在读取信息',downloading:'正在下载',verifying:'正在校验',importing:'正在入库',completed:'已完成',failed:'任务失败',interrupted:'已中断',cancelled:'已取消',canceling:'正在取消',waiting_for_target:'等待目标目录',waiting_for_login:'等待平台连接',pending_import:'待入库'};
      options.feedback((names[job.status] || '任务已创建') + (['downloading','importing','verifying'].includes(job.status) ? ' · ' + Math.round(Number(job.progress) || 0) + '%' : '') + (job.message ? ' · ' + job.message : ''), job.status === 'failed' ? 'error' : '');
      el('preview-show-jobs').hidden = false;
      finishedLibrary = job.status === 'completed' ? job.library_id || '' : '';
      el('preview-job-library').hidden = !finishedLibrary;
    }
    el('preview-job-library').addEventListener('click', () => { if (finishedLibrary) options.openLibrary(finishedLibrary); });
    function started(job, source) { const item = source || current; if (item && job && job.job_id) tasks.set(identity(item), job); if (identity(item) === identity(current)) { options.feedback('任务已加入后台，可继续浏览其他素材。'); el('preview-show-jobs').hidden = false; } options.taskStarted(job); }
    function close() { capture(); comments.close(); generation++; media = null; current = null; drag = null; }
    return {begin, ready, close, capture, userLoaded, saved, refreshMarkers, waveform, drawWave, tab, showInfo, started, jobsChanged};
  }};
})();
