(function () {
  'use strict';
  const el = (id) => document.getElementById(id);
  const html = (value) => String(value == null ? '' : value).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const platformName = (id) => ({ douyin: '抖音', bilibili: '哔哩哔哩', youtube: 'YouTube', stock: '素材站' }[id] || id || '在线作品');
  function publicURL(value) { try { const u = new URL(value); return ['http:', 'https:'].includes(u.protocol) && !u.username && !u.password ? u.href : ''; } catch (_) { return ''; } }
  const link = (url, title) => publicURL(url) ? '<a href="' + html(publicURL(url)) + '" target="_blank" rel="noopener noreferrer">' + html(title) + '</a>' : '';
  const activeStates = new Set(['queued', 'resolving', 'downloading', 'verifying', 'importing', 'canceling']);
  const statusName = (status) => ({ queued: '排队中', resolving: '正在读取信息', downloading: '正在下载', verifying: '正在校验', importing: '正在入库', canceling: '正在取消', cancelled: '已取消', completed: '已完成', failed: '任务失败', interrupted: '已中断', pending_import: '待入库', waiting_for_target: '等待目标目录', waiting_for_login: '等待平台连接' }[status] || status);
  const empty = (title, detail) => '<div class="remote-empty"><strong>' + html(title) + '</strong><span class="meta">' + html(detail) + '</span></div>';

  function engagementMarkup(item) {
    if (item.provider !== 'douyin') return '';
    const stats = item.engagement || {};
    const counters = [['like_count', '点赞'], ['favorite_count', '收藏'], ['comment_count', '评论'], ['share_count', '分享']];
    const observed = stats.observed_at ? new Date(stats.observed_at) : null;
    const validTime = observed && Number.isFinite(observed.getTime());
    const timeText = validTime ? observed.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false }) : '';
    const fields = counters.map(([key, label]) => {
      const value = stats[key], known = Number.isSafeInteger(value) && value >= 0;
      const compact = !known ? '—' : value >= 1e8 ? (value / 1e8).toFixed(1).replace(/\.0$/, '') + '亿' : value >= 1e4 ? (value / 1e4).toFixed(1).replace(/\.0$/, '') + '万' : value.toLocaleString('zh-CN');
      const detail = '抖音作品' + label + '：' + (known ? value.toLocaleString('zh-CN') : '暂不可用');
      return '<span title="' + html(detail) + '">' + label + ' <span class="remote-counter">' + compact + '</span></span>';
    }).join('');
    return '<div class="remote-engagement meta" title="抖音作品统计；— 表示暂不可用">' + fields + (validTime ? '<time class="remote-engagement-time" datetime="' + html(stats.observed_at) + '" title="' + html('获取于 ' + observed.toLocaleString('zh-CN') + '；非实时更新') + '">获取于 ' + html(timeText) + '</time>' : '') + '</div>';
  }

  window.MediaLibraryIntake = { engagementMarkup, create(options) {
    const call = options.invoke;
    const message = options.describe;
    const state = { view: 'assets', info: null, parsedURL: '', parseToken: 0, parsing: false, intakeToken: 0, starting: false, key: '', libraries: [], jobs: [], active: 0, total: 0, jobBusy: new Set(), account: null, runtime: null, settings: false, timer: null, polling: false, overlayFocus: null, bookmarks: [], discovery: [], providers: [], discoveryToken: 0, query: '', page: 1, more: false, previewToken: 0 };
    const intakeHome = el('intake-modal').parentNode;
    function feedback(id, text, tone) { const node = el(id); node.textContent = text || ''; node.hidden = !text; node.className = 'inline-feedback' + (tone ? ' ' + tone : ''); }
    function newKey() { return window.crypto && crypto.randomUUID ? crypto.randomUUID() : 'request-' + Date.now() + '-' + Math.random().toString(16).slice(2); }
    function overlay() { return !el('intake-modal').hidden ? el('intake-modal') : (!el('acquisitions-modal').hidden ? el('acquisitions-modal') : null); }
    document.addEventListener('focusin', (event) => {
      const target = event.target;
      const dialog = target.closest('.intake-dialog, .acquisition-dialog');
      if (!dialog || !target.closest('.remote-body')) return;
      const header = dialog.querySelector('.remote-head'), footer = dialog.querySelector('.remote-footer');
      const box = target.getBoundingClientRect(), viewport = dialog.getBoundingClientRect();
      const top = (header ? header.getBoundingClientRect().bottom : viewport.top) + 8;
      const bottom = (footer ? footer.getBoundingClientRect().top : viewport.bottom) - 8;
      if (box.bottom > bottom) dialog.scrollTop += box.bottom - bottom;
      else if (box.top < top) dialog.scrollTop -= top - box.top;
    });
    function showOverlay(id, focusID) {
      const current = overlay(); if (current) hideOverlay(current.id);
      state.overlayFocus = document.activeElement; el(id).hidden = false; document.body.classList.add('has-overlay');
      document.querySelector('.app-shell').inert = true; el(focusID).focus();
    }
    function hideOverlay(id) {
      if (id === 'intake-modal') { state.intakeToken++; state.parseToken++; state.parsing = false; el('resolve-intake').disabled = false; }
      el(id).hidden = true; document.querySelector('.app-shell').inert = false;
      if (id === 'intake-modal' && state.embedded) {
        state.embedded = false; el('preview-operation-slot').hidden = true;
        el(id).classList.toggle('embedded-intake', false); intakeHome.appendChild(el(id));
      }
      document.body.classList.toggle('has-overlay', !el('settings-modal').hidden || !el('preview').hidden || !el('confirm-modal').hidden);
      if (state.overlayFocus && state.overlayFocus.isConnected) state.overlayFocus.focus(); state.overlayFocus = null;
    }
    function invalidate() { state.info = null; state.parsedURL = ''; state.key = ''; state.parseToken++; el('intake-details').hidden = true; el('start-intake').disabled = true; el('bookmark-intake').disabled = true; el('bookmark-intake').textContent = '收藏作品'; el('resolve-intake').hidden = false; el('resolve-intake').textContent = '继续'; }
    async function openIntake(url, audioOnly, context) {
      if (state.starting) return;
      if (!el('intake-modal').hidden) hideOverlay('intake-modal');
      state.embedded = Boolean(context && context.embedded && !el('preview').hidden);
      state.embeddedSource = state.embedded && context.source;
      if (state.embedded) {
        el('preview-operation-slot').hidden = false; el('preview-operation-slot').appendChild(el('intake-modal'));
        el('intake-modal').classList.toggle('embedded-intake', true);
      }
      state.preferredAudio = Boolean(audioOnly); el('intake-bgm').checked = Boolean(audioOnly);
      el('intake-title').textContent = audioOnly ? '提取音频' : '下载作品';
      el('intake-url').value = typeof url === 'string' ? url.trim() : ''; invalidate();
      showOverlay('intake-modal', 'intake-url'); feedback('intake-feedback', ''); el('intake-open-settings').hidden = true;
      const token = state.intakeToken = (state.intakeToken || 0) + 1;
      const current = () => token === state.intakeToken && !el('intake-modal').hidden;
      state.targetLoading = true; state.libraryError = ''; state.libraries = [];
      el('intake-library').innerHTML = '<option value="">正在读取资源库…</option>'; el('intake-library').disabled = true; updateTarget();
      // A result already identifies the work. Read it immediately, independently of the target lookup.
      const resolving = el('intake-url').value ? resolveLink({ preventDefault() {} }) : Promise.resolve();
      try {
        const payload = await call('media-library.repository.list', {}); if (!current()) return; state.libraries = payload.libraries || [];
        const writable = state.libraries.filter((item) => item.backend_type !== 'smb' && item.access_mode !== 'read_only' && item.state === 'online');
        el('intake-library').innerHTML = writable.length ? writable.map((item) => '<option value="' + html(item.id) + '">' + html(item.name) + '</option>').join('') : '<option value="">没有可写的本机资源库</option>';
        if (writable.some((item) => item.id === options.defaultLibrary())) el('intake-library').value = options.defaultLibrary();
        if (!writable.length) el('intake-mode').value = 'save';
      } catch (error) { if (current()) { state.libraryError = '资源库读取失败：' + message(error) + '。可选择“仅保存文件”，或关闭后重试。'; el('intake-library').innerHTML = '<option value="">资源库读取失败</option>'; } }
      finally { if (current()) { state.targetLoading = false; el('intake-library').disabled = Boolean(state.libraryError); updateTarget(); } }
      await resolving;
    }
    function updateTarget() {
      const isAudio = el('intake-format').value === 'audio'; el('intake-bgm-field').hidden = !isAudio; el('intake-audio-hint').hidden = !isAudio;
      if (!isAudio) el('intake-bgm').checked = false;
      const importing = el('intake-mode').value === 'import';
      el('intake-library-field').hidden = !importing; el('intake-destination-field').hidden = importing;
      el('intake-target-hint').textContent = importing && state.targetLoading ? '正在读取资源库…' : importing && state.libraryError ? state.libraryError : importing && !el('intake-library').value ? '请先在设置中创建本机资源库，或选择“仅保存文件”。' : (importing ? '同一内容只保留一份文件，来源分别记录。' : '保留原文件名；目标已有不同内容时自动改名。');
      el('start-intake').disabled = !state.info || state.starting || (importing && (state.targetLoading || Boolean(state.libraryError) || !el('intake-library').value));
      el('start-intake').textContent = state.starting ? '正在创建…' : isAudio ? (importing ? '提取并入库' : '提取音频') : (importing ? '下载并入库' : '开始下载');
    }
    async function resolveLink(event) {
      event.preventDefault(); if (state.parsing || !el('intake-resolve-form').reportValidity()) return;
      invalidate(); const url = el('intake-url').value.trim(); const token = ++state.parseToken;
      state.parsing = true; el('resolve-intake').disabled = true; el('resolve-intake').hidden = true; feedback('intake-feedback', '正在读取作品信息…'); el('intake-open-settings').hidden = true;
      try {
        const info = await call('media-library.remote.resolve', { url, playlist: false });
        if (token !== state.parseToken || el('intake-modal').hidden || el('intake-url').value.trim() !== url) return;
        if (info.is_playlist) throw new Error('此链接包含多个作品，请暂用媒体下载工具处理播放列表。');
        state.info = info; state.parsedURL = url; state.key = newKey();
        el('intake-music-meta').hidden = !info.music; el('intake-music-meta').textContent = info.music ? '视频用曲：' + (info.music.title || '未命名音乐') + (info.music.author ? ' · ' + info.music.author : '') : '';
        el('intake-media-title').textContent = info.title || '未命名作品';
        el('intake-media-meta').textContent = [info.uploader, info.duration_seconds ? Math.round(info.duration_seconds) + ' 秒' : '', (info.formats || []).length + ' 个格式'].filter(Boolean).join(' · ');
        el('intake-format').innerHTML = '<option value="best">最佳画质</option><option value="1080p">最高 1080p</option><option value="720p">最高 720p</option><option value="480p">最高 480p</option><option value="audio">仅音频 · MP3</option>' + (info.formats || []).filter((format) => format.vcodec && format.vcodec !== 'none').map((format) => '<option value="id:' + html(format.format_id) + '">原始格式 · ' + html(format.resolution || format.format_id) + ' · ' + html(format.ext) + '</option>').join('');
        if (state.preferredAudio) { el('intake-format').value = 'audio'; el('intake-bgm').checked = true; }
        el('intake-subtitles').innerHTML = '<option value="">不下载字幕</option>' + (info.subtitles || []).map((lang) => '<option value="' + html(lang) + '">' + html(lang) + '</option>').join('');
        el('intake-subtitles-field').hidden = !(info.subtitles || []).length;
        el('intake-details').hidden = false; el('bookmark-intake').disabled = false; el('intake-open-settings').hidden = true; feedback('intake-feedback', ''); updateTarget();
      } catch (error) { if (token === state.parseToken) { feedback('intake-feedback', message(error), 'error'); el('intake-open-settings').hidden = false; el('resolve-intake').hidden = false; el('resolve-intake').textContent = '重试'; } }
      finally { if (token === state.parseToken) { state.parsing = false; el('resolve-intake').disabled = false; } }
    }
    function bookmarkPayload(info) { return { source_url: info.source_url || info.webpage_url || state.parsedURL, title: info.title || '', kind: info.kind || 'video', author: info.author || info.uploader || '', duration_seconds: Number(info.duration_seconds) || 0, license: info.license || '' }; }
    async function bookmarkInfo(info, button) {
      button.disabled = true;
      try { await call('media-library.bookmark.save', bookmarkPayload(info)); button.textContent = '已收藏'; if (button.id === 'bookmark-intake') feedback('intake-feedback', '已收藏，可在“收藏”中添加备注。'); }
      catch (error) { button.disabled = false; feedback(button.id === 'bookmark-intake' ? 'intake-feedback' : button.id === 'remote-preview-bookmark' ? 'preview-feedback' : 'discovery-feedback', '收藏失败：' + message(error), 'error'); }
    }
    async function startAcquisition() {
      if (!state.info || state.starting || el('intake-url').value.trim() !== state.parsedURL) return;
      const format = el('intake-format').value;
      const input = { url: state.info.webpage_url || state.parsedURL, title: state.info.title || '', mode: el('intake-mode').value, idempotency_key: state.key || (state.key = newKey()), playlist: false };
      if (input.mode === 'import') { if (!el('intake-library').value) return; input.library_id = el('intake-library').value; }
      else if (el('intake-destination').value.trim()) input.dest_dir = el('intake-destination').value.trim();
      if (format.startsWith('id:')) input.format_id = format.slice(3); else if (format === 'audio') input.audio_only = true; else input.quality = format;
      if (input.audio_only && el('intake-bgm').checked) input.purpose = 'bgm';
      if (el('intake-subtitles').value) input.subtitles = [el('intake-subtitles').value];
      state.starting = true; updateTarget(); feedback('intake-feedback', '正在创建后台任务…');
      const embedded = state.embedded, source = state.embeddedSource, token = state.intakeToken;
      try { const job = await call('media-library.acquisition.start', input); if (token === state.intakeToken) { hideOverlay('intake-modal'); invalidate(); } if (embedded && options.acquisitionStarted) { options.acquisitionStarted(job, source); await loadJobs(); schedule(); } else if (token + 1 === state.intakeToken) await openJobs(); }
      catch (error) { feedback('intake-feedback', message(error), 'error'); el('intake-open-settings').hidden = false; }
      finally { state.starting = false; updateTarget(); }
    }
    async function loadBookmarks() {
      el('bookmark-results').innerHTML = empty('正在读取收藏…', ''); feedback('bookmark-feedback', '');
      try {
        const payload = await call('media-library.bookmark.list', { query: el('bookmark-query').value.trim() }); state.bookmarks = payload.items || [];
        el('bookmark-results').innerHTML = state.bookmarks.length ? state.bookmarks.map((item, index) => '<article class="remote-card"><div class="remote-card-copy"><span class="meta">' + html(platformName(item.provider)) + '</span><h3>' + html(item.title) + '</h3><p class="meta">' + html(item.author || '') + '</p>' + link(item.source_url, '打开作品页面') + '</div><div class="remote-card-actions"><button type="button" class="ghost" data-bookmark-preview="' + index + '">预览</button><button type="button" class="primary" data-bookmark-collect="' + index + '">下载</button><button type="button" class="ghost danger-text" data-bookmark-remove="' + index + '">移除收藏</button></div><details class="bookmark-notes"><summary>备注与标签</summary><form data-bookmark-edit="' + index + '"><label>备注<textarea name="note" rows="2" maxlength="2000">' + html(item.note) + '</textarea></label><label>标签<input name="tags" maxlength="500" value="' + html((item.tags || []).join('，')) + '" placeholder="用逗号分隔" /></label><button type="submit" class="ghost">保存备注</button><span class="meta" data-bookmark-saved></span></form></details></article>').join('') : empty(el('bookmark-query').value.trim() ? '没有匹配的收藏' : '还没有收藏', el('bookmark-query').value.trim() ? '换个关键词再试。' : '在在线发现或添加链接后点击“收藏”。');
      } catch (error) { el('bookmark-results').innerHTML = empty('收藏暂不可用', '重新进入此页可重试。'); feedback('bookmark-feedback', message(error), 'error'); }
    }
    async function bookmarkAction(event) {
      const preview = event.target.closest('[data-bookmark-preview]'); if (preview) { options.previewRemote(state.bookmarks, Number(preview.dataset.bookmarkPreview)); return; }
      const collect = event.target.closest('[data-bookmark-collect]'); if (collect) { void openIntake(state.bookmarks[Number(collect.dataset.bookmarkCollect)].source_url); return; }
      const remove = event.target.closest('[data-bookmark-remove]'); if (!remove) return;
      const item = state.bookmarks[Number(remove.dataset.bookmarkRemove)];
      if (!(await options.confirm({ title: '移除收藏？', message: '只移除作品线索，已下载的素材和文件会保留。', confirmText: '移除收藏', danger: true }))) return;
      try { await call('media-library.bookmark.remove', { remote_id: item.remote_id }); await loadBookmarks(); } catch (error) { feedback('bookmark-feedback', message(error), 'error'); }
    }
    async function saveBookmarkNote(event) {
      const form = event.target.closest('[data-bookmark-edit]'); if (!form) return; event.preventDefault(); const item = state.bookmarks[Number(form.dataset.bookmarkEdit)]; const button = form.querySelector('button'); button.disabled = true;
      try { const updated = Object.assign(bookmarkPayload(item), { note: form.elements.note.value, tags: form.elements.tags.value.split(/[,，]/).map((s) => s.trim()).filter(Boolean) }); const saved = await call('media-library.bookmark.save', updated); Object.assign(item, saved); form.querySelector('[data-bookmark-saved]').textContent = '已保存'; }
      catch (error) { feedback('bookmark-feedback', message(error), 'error'); } finally { button.disabled = false; }
    }
    function updateSupport() {
      const douyin = el('discovery-platform').value === 'douyin';
      if (!douyin) state.musicMode = false;
      el('discovery-modes').hidden = !douyin; el('discovery-query').required = !state.musicMode;
      el('discovery-query').placeholder = state.musicMode ? '音乐关键词；留空查找热门 BGM' : '搜索视频、图片或音频';
      el('discovery-submit').textContent = state.musicMode ? '查找音乐' : '搜索';
      ['video','music'].forEach((mode) => { const on = mode === 'music' ? Boolean(state.musicMode) : !state.musicMode; el('discovery-' + mode).classList.toggle('active', on); el('discovery-' + mode).setAttribute('aria-pressed', String(on)); });
      const provider = state.providers.find((item) => item.id === el('discovery-platform').value);
      const supported = el('discovery-platform').value === 'stock' || Boolean(provider && provider.search);
      el('discovery-submit').disabled = !supported || Boolean(state.searchID);
      el('discovery-support').textContent = supported ? (el('discovery-platform').value === 'stock' ? '使用已配置的素材站，单个来源失败不影响其他结果。' : (provider.message || '')) : (provider ? provider.message : '关键词搜索尚未接入，可先添加作品链接。');
    }
    function previewCover(item, action, index) {
      const cover = publicURL(item.preview_url || item.thumbnail);
      return '<button type="button" class="remote-video-cover" ' + action + '="' + index + '" aria-label="播放 ' + html(item.title || '视频') + '">' + (cover ? '<img src="' + html(cover) + '" alt="" loading="lazy" referrerpolicy="no-referrer" />' : '') + '<span class="remote-play" aria-hidden="true">▶</span><span class="remote-play-label">播放预览</span></button>';
    }
    function renderDiscovery() {
      if (state.musicMode) {
        el('discovery-results').innerHTML = state.discovery.length ? state.discovery.map((group, index) => '<article class="remote-card music-card"><div class="remote-card-copy"><span class="meta">抖音音乐 · 已加载样本中 ' + group.videos.length + ' 个视频使用</span><h3>' + html(group.music.title || '未命名音乐') + '</h3><p class="meta">' + html(group.music.author || '作者未标注') + '</p>' + link(group.music.source_url, '打开音乐页面') + '</div><details class="music-usage-list"><summary>播放视频或提取音频（' + group.videos.length + '）</summary>' + group.videos.map((video, vi) => '<div class="music-usage-row">' + previewCover(video, 'data-music-preview', index + ':' + vi) + '<div><button type="button" class="remote-title" data-music-preview="' + index + ':' + vi + '">' + html(video.title || '播放视频') + '</button><p class="meta">' + html(video.author || '') + '</p>' + engagementMarkup(video) + link(video.source_url, '打开原作品') + '</div><button type="button" class="primary" data-music-audio="' + index + ':' + vi + '">提取音频</button></div>').join('') + '</details></article>').join('') : empty('未识别到视频用曲', '换个关键词重试，或添加视频链接提取音轨。');
        el('discovery-more').hidden = !state.more; return;
      }
      el('discovery-results').innerHTML = state.discovery.length ? state.discovery.map((item, index) => '<article class="remote-card">' + previewCover(item, 'data-discovery-preview', index) + '<div class="remote-card-copy"><span class="meta">' + html(platformName(item.provider)) + '</span><h3><button type="button" class="remote-title" data-discovery-preview="' + index + '">' + html(item.title || item.id) + '</button></h3><p class="meta">' + html(item.author || item.uploader || '') + '</p>' + engagementMarkup(item) + link(item.source_url, '打开原作品') + '</div><div class="remote-card-actions"><button type="button" class="primary" data-discovery-collect="' + index + '">' + (item.origin === 'stock' ? '预览与入库' : '下载') + '</button><button type="button" class="ghost" data-discovery-bookmark="' + index + '">收藏</button></div></article>').join('') : empty('没有匹配结果', '更换关键词，或在设置中检查来源。');
      el('discovery-more').hidden = !state.more;
    }
    async function searchDiscovery(more) {
      const query = el('discovery-query').value.trim(); if ((!query && !state.musicMode) || el('discovery-submit').disabled) return;
      if (more && query !== state.query) more = false;
      const token = ++state.discoveryToken; const platform = el('discovery-platform').value; const page = more ? state.page + 1 : 1;
      const searchID = Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) => byte.toString(16).padStart(2, '0')).join(''); state.searchID = searchID; state.searchProvider = platform;
      el('discovery-cancel').hidden = platform === 'stock';
      el('discovery-submit').disabled = true; el('discovery-more').disabled = true; feedback('discovery-feedback', '正在搜索…');
      if (!more) { state.discovery = []; el('discovery-results').innerHTML = empty('正在搜索…', ''); }
      try {
        const payload = platform === 'stock' ? await call('media-library.search', { query, kinds: ['image', 'video', 'audio'], per_source: 12, page }) : await call(state.musicMode ? 'media-library.music.discover' : 'media-library.discovery.search', { provider: platform, query, cursor: more ? state.cursor : '', request_id: searchID });
        if (token !== state.discoveryToken || query !== el('discovery-query').value.trim()) return;
        state.query = query; state.page = page; state.cursor = payload.next_cursor || ''; state.more = platform === 'stock' ? false : Boolean(state.cursor);
        if (state.musicMode) {
          if (!more) { state.discovery = []; state.musicSampleIDs = new Set(); state.musicIdentifiedIDs = new Set(); }
          for (const sample of payload.sampled_videos || []) { state.musicSampleIDs.add(sample.video_id); if (sample.identified) state.musicIdentifiedIDs.add(sample.video_id); }
          for (const group of payload.items || []) { const prior = state.discovery.find((g) => g.music_id === group.music_id); if (!prior) state.discovery.push(group); else { const seenVideos = new Set(prior.videos.map((v) => v.source_url)); prior.videos.push(...group.videos.filter((v) => !seenVideos.has(v.source_url))); } }
          state.discovery.sort((a,b) => b.videos.length - a.videos.length); renderDiscovery();
          feedback('discovery-feedback', '已加载 ' + state.musicSampleIDs.size + ' 个视频样本，识别 ' + state.musicIdentifiedIDs.size + ' 个视频的用曲。' + (payload.warnings || []).join('；')); return;
        }
        const found = (payload.items || []).map((item) => Object.assign({}, item, { origin: platform === 'stock' ? 'stock' : 'platform' }));
        const seen = new Set(); state.discovery = (more ? state.discovery.concat(found) : found).filter((item) => { const key = item.remote_id || (item.id ? item.provider + ':' + item.id : item.source_url); if (seen.has(key)) return false; seen.add(key); return true; });
        renderDiscovery(); const failures = (payload.providers || []).filter((item) => item.status !== 'ok').map((item) => item.name + '：' + (item.error || '尚未配置'));
        feedback('discovery-feedback', failures.join('；') || ((payload.warnings || []).join('；')), failures.length ? 'warn' : '');
      } catch (error) { if (token === state.discoveryToken) { feedback('discovery-feedback', message(error), 'error'); if (!more) el('discovery-results').innerHTML = empty('搜索未完成', '检查平台连接或网络后重试。'); } }
      finally { if (state.searchID === searchID) { state.searchID = ''; el('discovery-cancel').hidden = true; updateSupport(); el('discovery-more').disabled = false; } }
    }
    function cancelSearch(showFeedback) {
      const id = state.searchID; state.searchID = ''; state.discoveryToken++;
      if (id && state.searchProvider !== 'stock') void call('media-library.discovery.cancel', { request_id: id }).catch(() => {});
      el('discovery-cancel').hidden = true; el('discovery-more').disabled = false; updateSupport();
      if (id && showFeedback) { feedback('discovery-feedback', '搜索已取消。'); if (!state.discovery.length) el('discovery-results').innerHTML = empty('搜索已取消', '可以更换关键词后重新搜索。'); }
    }
    async function loadJobs() {
      const payload = await call('media-library.acquisition.list', { limit: 50 }); state.jobs = payload.items || []; state.active = Number(payload.active) || 0; state.total = Number(payload.total) || 0;
      if (options.jobsChanged) options.jobsChanged(state.jobs);
      el('acquisition-count').hidden = !state.active; el('acquisition-count').textContent = state.active; el('acquisitions-summary').textContent = state.active ? state.active + ' 个进行中 · 关闭面板后继续' : state.total + ' 个任务 · 文件与记录分别保留';
      if (!el('acquisitions-modal').hidden) renderJobs(); feedback('acquisitions-feedback', ''); el('reload-acquisitions').hidden = true;
    }
    function renderJobs() {
      const focused = document.activeElement.closest('[data-job-action]'); const restore = focused && { id: focused.dataset.jobId, action: focused.dataset.jobAction };
      el('acquisition-jobs').innerHTML = state.jobs.length ? state.jobs.map((job) => {
        const active = activeStates.has(job.status); const retry = !active && job.status !== 'completed'; const count = (job.files || []).length;
        const button = (action, title) => '<button type="button" class="ghost" data-job-id="' + html(job.job_id) + '" data-job-action="' + action + '"' + (state.jobBusy.has(job.job_id) ? ' disabled' : '') + '>' + title + '</button>';
        return '<article class="acquisition-job"><div class="job-heading"><h3>' + html(job.title || job.url) + '</h3><span class="job-status">' + html((job.task_kind === 'audio' && active ? '音频处理中' : statusName(job.status))) + '</span></div>' + (active ? '<progress max="100" value="' + Math.min(100, Math.max(0, Number(job.progress) || 0)) + '"></progress>' : '') + '<p class="meta">' + (job.task_kind === 'audio' ? '提取音频并入库' : job.purpose === 'bgm' ? '提取 BGM' : job.mode === 'import' ? '下载并入库' : '仅保存文件') + ' · ' + (count ? count + ' 个输出文件' : '等待下载') + '</p>' + (job.message ? '<p class="job-message">' + html(job.message) + '</p>' : '') + (['PLATFORM_VERIFICATION', 'SOURCE_UNAVAILABLE'].includes(job.error_code) ? link(job.url, '打开作品页面') : '') + '<div class="job-actions">' + (active && job.status !== 'canceling' ? button('cancel', '取消任务') : '') + (retry ? button('retry', job.error_code === 'SESSION_CHANGED' ? '使用当前账号重试' : (count ? '继续保存或入库' : '重试')) : '') + (job.status === 'completed' && job.error_code === 'CATALOG_PENDING' ? button('retry', '重试目录同步') : '') + (job.status === 'completed' && job.library_id ? button('library', '查看资源库') : '') + (count ? button('paths', '复制文件路径') : '') + button('diagnostic', '复制诊断') + (!active ? button('remove', '移除记录') : '') + '</div></article>';
      }).join('') : empty('还没有后台任务', '点击“添加链接”，选择保存文件或下载并入库。');
      if (restore) { const target = Array.from(el('acquisition-jobs').querySelectorAll('[data-job-action]')).find((node) => node.dataset.jobId === restore.id && node.dataset.jobAction === restore.action); if (target) target.focus({ preventScroll: true }); }
    }
    async function openJobs() { showOverlay('acquisitions-modal', 'close-acquisitions'); el('acquisition-jobs').innerHTML = empty('正在读取任务…', ''); try { await loadJobs(); } catch (error) { jobsFailed(error); } if (el('legacy-downloads').open) void loadLegacy(); schedule(); }
    async function loadLegacy() {
      try {
        const payload = await call('media-library.legacy.list', {});
        el('legacy-download-list').innerHTML = (payload.items || []).length ? payload.items.map((item) => '<article class="acquisition-job"><div class="job-heading"><h3>' + html(item.title || '未命名作品') + '</h3><span class="job-status">' + html(item.status === 'canceled' ? '已取消' : statusName(item.status)) + '</span></div>' + link(item.url, '打开作品页面') + (item.outputs || []).map((path) => '<p class="legacy-path">' + html(path) + '</p>').join('') + '<p class="meta">原任务归属：媒体下载工具</p></article>').join('') : empty('尚未导入旧记录', '导入只增加历史引用，不移动文件或接管任务。');
      } catch (error) { feedback('legacy-feedback', '读取旧记录失败：' + message(error), 'error'); }
    }
    async function importLegacy() {
      const button = el('import-legacy-downloads'); if (button.disabled) return;
      button.disabled = true; feedback('legacy-feedback', '正在读取旧工具的已结束记录…');
      let imported = 0, existing = 0, skipped = 0;
      try {
        for (let offset = 0; offset < 10000; offset += 200) {
          const payload = await call('media.downloader.job.list', { statuses: ['completed', 'failed', 'canceled'], limit: 200, offset, sort: 'created_asc' });
          const rows = payload.items || [];
          // Only stable history fields cross the owner boundary; no logs or covers.
          const items = rows.map((item) => ({ job_id: item.job_id, status: item.status, title: item.title || '', url: item.url, outputs: item.outputs || [], finished_at: item.finished_at || '' }));
          const result = await call('media-library.legacy.import', { items });
          imported += result.imported || 0; existing += result.existing || 0; skipped += result.skipped || 0;
          if (rows.length < 200 || offset + rows.length >= payload.total) break;
          if (offset === 9800) throw new Error('本次已处理 10000 条，更多记录请继续在旧工具中查看。');
        }
        feedback('legacy-feedback', '新增 ' + imported + ' 条，已有 ' + existing + ' 条，跳过 ' + skipped + ' 条。文件位置未改变。');
      } catch (error) { feedback('legacy-feedback', '本次新增 ' + imported + ' 条。旧记录导入未完成：' + message(error) + '。请确认媒体下载工具已启用后重试。', 'error'); }
      finally { button.disabled = false; await loadLegacy(); }
    }
    function jobsFailed(error) { feedback('acquisitions-feedback', '任务暂不可用：' + message(error), 'error'); el('reload-acquisitions').hidden = false; }
    async function jobAction(event) {
      const button = event.target.closest('[data-job-action]'); if (!button) return;
      const id = button.dataset.jobId, action = button.dataset.jobAction, job = state.jobs.find((item) => item.job_id === id); if (!job || state.jobBusy.has(id)) return;
      if (action === 'library') { hideOverlay('acquisitions-modal'); options.openLibrary(job.library_id); return; }
      if (action === 'remove' && !(await options.confirm({ title: '移除任务记录？', message: '已下载和已入库的文件会保留。', confirmText: '移除记录', danger: true }))) return;
      const rebind = action === 'retry' && job.error_code === 'SESSION_CHANGED';
      if (rebind && !(await options.confirm({ title: '使用当前平台账号？', message: '将使用当前连接的账号重新读取作品信息。已下载文件会保留。', confirmText: '使用当前账号' }))) return;
      state.jobBusy.add(id); button.disabled = true;
      try {
        if (action === 'paths') await navigator.clipboard.writeText((job.files || []).map((file) => file.path).join('\n'));
        else if (action === 'diagnostic') { const diagnostic = await call('media-library.acquisition.log', { job_id: id }); await navigator.clipboard.writeText(JSON.stringify(diagnostic, null, 2)); }
        else await call('media-library.acquisition.' + action, Object.assign({ job_id: id }, rebind ? { use_current_account: true } : {}));
        if (action === 'paths' || action === 'diagnostic') feedback('acquisitions-feedback', '已复制。'); else await loadJobs();
      } catch (error) { feedback('acquisitions-feedback', message(error), 'error'); }
      finally { state.jobBusy.delete(id); renderJobs(); schedule(); }
    }
    async function refreshSettings() {
      const results = await Promise.allSettled([call('media-library.download.status', {}), call('media-library.account.status', {})]);
      if (results[0].status === 'fulfilled') { state.runtime = results[0].value; el('download-runtime-state').textContent = state.runtime.version ? 'yt-dlp ' + state.runtime.version + (state.runtime.ffmpeg_path ? ' · 音频与组合格式可用' : ' · 音频提取需要 ffmpeg') : (state.runtime.message || '下载组件未安装'); el('install-download-runtime').disabled = state.runtime.state === 'ready'; el('install-download-runtime').textContent = state.runtime.state === 'ready' ? '已安装' : '安装组件'; }
      else { el('download-runtime-state').textContent = '检查失败，请重新打开此页。'; feedback('acquisition-settings-feedback', message(results[0].reason), 'error'); }
      if (results[1].status === 'fulfilled') { state.account = results[1].value; renderAccount(); } else { el('douyin-account-state').textContent = '连接状态读取失败'; feedback('acquisition-settings-feedback', message(results[1].reason), 'error'); }
      schedule();
    }
    function renderAccount() { const account = state.account || {}; el('douyin-account-state').textContent = account.message || '尚未连接'; el('connect-douyin-account').disabled = account.state === 'connecting'; el('connect-douyin-account').textContent = account.state === 'connecting' ? '等待官网连接…' : account.state === 'connected' ? '重新连接' : '连接抖音'; el('disconnect-douyin-account').hidden = !['connected', 'connecting', 'expired'].includes(account.state); }
    function invalidateDouyinSearch() { if (el('discovery-platform').value === 'douyin') { cancelSearch(false); state.discovery = []; state.cursor = ''; state.more = false; el('discovery-more').hidden = true; el('discovery-results').innerHTML = empty('平台连接已更新', '重新搜索以使用当前连接。'); } }
    async function connectAccount() { invalidateDouyinSearch(); el('connect-douyin-account').disabled = true; feedback('acquisition-settings-feedback', ''); try { state.account = await call('media-library.account.connect', {}); renderAccount(); } catch (error) { feedback('acquisition-settings-feedback', message(error), 'error'); el('connect-douyin-account').disabled = false; } schedule(); }
    function schedule() {
      clearTimeout(state.timer); state.timer = null; if (document.hidden) return;
      const connecting = state.settings && !el('settings-modal').hidden && state.account && state.account.state === 'connecting';
      if (!state.active && !connecting) return;
      state.timer = setTimeout(() => { void poll(); }, connecting ? 2000 : 5000);
    }
    async function poll() {
      if (state.polling) return; state.polling = true;
      try { if (state.active) await loadJobs(); if (state.settings && !el('settings-modal').hidden && state.account && state.account.state === 'connecting') { state.account = await call('media-library.account.status', {}); renderAccount(); } }
      catch (error) { jobsFailed(error); state.active = 0; }
      finally { state.polling = false; schedule(); }
    }
    async function preview(item) {
      const token = ++state.previewToken; el('asset-provenance').hidden = true;
      if (!item || item.entry_type !== 'library') return;
      try { const payload = await call('media-library.holdings.provenance', { item_id: item.id }); if (token !== state.previewToken) return;
        const origins = payload.origins || [], attachments = payload.attachments || []; const derivations = payload.audio_derivations || []; if (!origins.length && !attachments.length && !derivations.length) return;
        el('asset-provenance').innerHTML = '<h3>来源与加工</h3>' + origins.map((origin) => '<p>' + (origin.provider === 'local' ? '来源：本地素材' : link(origin.source_url, platformName(origin.provider) + ' · ' + (origin.provider_id || '原始页面'))) + '</p>').join('') + attachments.map((file) => '<p class="meta">字幕 · ' + html(file.language || '未标注语言') + ' · ' + html(file.name) + '</p>').join(''); el('asset-provenance').innerHTML += derivations.map((d) => '<p class="meta">' + html(d.purpose === 'bgm' ? '用途：BGM' : '音频') + ' · ' + html(({video_audio:'视频音轨',audio_excerpt:'音频片段',extracted_audio:'提取音轨'})[d.role] || '音频加工') + (d.parameters.end_ms ? ' · ' + (d.parameters.start_ms / 1000).toFixed(2) + '–' + (d.parameters.end_ms / 1000).toFixed(2) + ' 秒' : '') + '</p>' + (d.music_title ? '<p>用曲：' + link(d.music_url, d.music_title + (d.music_author ? ' · ' + d.music_author : '')) + '</p>' : '')).join(''); el('asset-provenance').hidden = false;
      } catch (_) { if (token === state.previewToken) { el('asset-provenance').textContent = '来源信息暂不可用，请重新打开素材。'; el('asset-provenance').hidden = false; } }
    }
    el('open-intake').addEventListener('click', () => { void openIntake(); }); el('close-intake').addEventListener('click', () => hideOverlay('intake-modal'));
    el('intake-modal').addEventListener('click', (event) => { if (event.target.closest('[data-close-intake]')) hideOverlay('intake-modal'); });
    el('intake-url').addEventListener('input', () => { invalidate(); state.parsing = false; el('resolve-intake').disabled = false; feedback('intake-feedback', ''); el('intake-open-settings').hidden = true; }); el('intake-resolve-form').addEventListener('submit', (event) => { void resolveLink(event); });
    ['intake-mode', 'intake-library', 'intake-format', 'intake-subtitles', 'intake-destination'].forEach((id) => el(id).addEventListener('change', () => { state.key = newKey(); updateTarget(); }));
    el('start-intake').addEventListener('click', () => { void startAcquisition(); }); el('bookmark-intake').addEventListener('click', (event) => { if (state.info) void bookmarkInfo(state.info, event.currentTarget); });
    el('intake-open-settings').addEventListener('click', () => { hideOverlay('intake-modal'); options.openSettings('acquisition'); });
    el('pick-intake-destination').addEventListener('click', async () => { try { const result = await window.__TAURI_INTERNALS__.invoke('pick_workspace_directory', {}); if (result && result.path) { el('intake-destination').value = result.path; state.key = newKey(); } } catch (_) { feedback('intake-feedback', '无法打开目录选择器，请直接填写路径。', 'warn'); } });
    el('bookmark-form').addEventListener('submit', (event) => { event.preventDefault(); void loadBookmarks(); }); el('bookmark-results').addEventListener('click', (event) => { void bookmarkAction(event); }); el('bookmark-results').addEventListener('submit', (event) => { void saveBookmarkNote(event); });
    el('discovery-form').addEventListener('submit', (event) => { event.preventDefault(); void searchDiscovery(false); }); el('discovery-more').addEventListener('click', () => { void searchDiscovery(true); });
    el('discovery-cancel').addEventListener('click', () => cancelSearch(true));
    el('discovery-query').addEventListener('input', () => { cancelSearch(false); state.more = false; el('discovery-more').hidden = true; feedback('discovery-feedback', ''); updateSupport(); });
    ['video','music'].forEach((mode) => el('discovery-' + mode).addEventListener('click', () => { cancelSearch(false); state.musicMode = mode === 'music'; state.discovery = []; state.more = false; el('discovery-more').hidden = true; updateSupport(); feedback('discovery-feedback', ''); el('discovery-results').innerHTML = empty(state.musicMode ? '查找视频中的音乐' : '输入关键词查找视频', state.musicMode ? '输入关键词，或直接查找热门 BGM 的视频样本。用曲次数来自搜索样本。' : '也可以添加作品链接。'); }));
    el('discovery-platform').addEventListener('change', () => { cancelSearch(false); state.discovery = []; state.more = false; el('discovery-more').hidden = true; updateSupport(); el('discovery-results').innerHTML = empty('选择平台后搜索作品', '也可以添加作品链接。'); feedback('discovery-feedback', ''); });
    el('discovery-results').addEventListener('click', (event) => { const play = event.target.closest('[data-discovery-preview],[data-music-preview]'); if (play) { if (play.dataset.musicPreview) { const [g,v] = play.dataset.musicPreview.split(':').map(Number); options.previewRemote(state.discovery[g].videos, v); } else { const index = Number(play.dataset.discoveryPreview), item = state.discovery[index]; if (item.origin === 'stock') options.previewOnline(item); else options.previewRemote(state.discovery, index); } return; } const musicAudio = event.target.closest('[data-music-audio]'); if (musicAudio) { const [g,v] = musicAudio.dataset.musicAudio.split(':').map(Number); void openIntake(state.discovery[g].videos[v].source_url, true); return; } const collect = event.target.closest('[data-discovery-collect]'); const bookmark = event.target.closest('[data-discovery-bookmark]'); if (collect) { const item = state.discovery[Number(collect.dataset.discoveryCollect)]; if (item.origin === 'stock') options.previewOnline(item); else void openIntake(item.source_url); } if (bookmark) void bookmarkInfo(state.discovery[Number(bookmark.dataset.discoveryBookmark)], bookmark); });
    el('open-acquisitions').addEventListener('click', () => { void openJobs(); }); el('close-acquisitions').addEventListener('click', () => hideOverlay('acquisitions-modal')); el('acquisitions-modal').addEventListener('click', (event) => { if (event.target.closest('[data-close-acquisitions]')) hideOverlay('acquisitions-modal'); }); el('acquisition-jobs').addEventListener('click', (event) => { void jobAction(event); }); el('reload-acquisitions').addEventListener('click', async () => { try { await loadJobs(); schedule(); } catch (error) { jobsFailed(error); } });
    el('legacy-downloads').addEventListener('toggle', () => { if (el('legacy-downloads').open) void loadLegacy(); });
    el('import-legacy-downloads').addEventListener('click', () => { void importLegacy(); });
    el('connect-douyin-account').addEventListener('click', () => { void connectAccount(); });
    el('disconnect-douyin-account').addEventListener('click', async () => { if (!(await options.confirm({ title: '断开抖音连接？', message: '使用此连接的下载会停止，已下载文件保留。', confirmText: '断开', danger: true }))) return; invalidateDouyinSearch(); try { state.account = await call('media-library.account.disconnect', {}); renderAccount(); } catch (error) { feedback('acquisition-settings-feedback', message(error), 'error'); } });
    el('install-download-runtime').addEventListener('click', async () => { const button = el('install-download-runtime'); button.disabled = true; button.textContent = '正在安装…'; feedback('acquisition-settings-feedback', '正在下载并校验组件，可以关闭设置；安装完成后状态会更新。'); try { await call('media-library.download.install', {}); feedback('acquisition-settings-feedback', '下载组件已安装。'); await refreshSettings(); } catch (error) { feedback('acquisition-settings-feedback', message(error), 'error'); button.disabled = false; button.textContent = '重试安装'; } });
    document.addEventListener('visibilitychange', () => { if (document.hidden) clearTimeout(state.timer); else { void loadJobs().then(schedule).catch(jobsFailed); } });
    if (typeof ResizeObserver === 'function') new ResizeObserver((entries) => { document.documentElement.style.setProperty('--app-bar-height', entries[0].target.getBoundingClientRect().height + 'px'); }).observe(document.querySelector('.app-bar'));
    document.addEventListener('keydown', (event) => {
      const modal = overlay(); if (!modal || !el('confirm-modal').hidden || !el('settings-modal').hidden) return;
      if (event.key === 'Escape') { event.preventDefault(); event.stopImmediatePropagation(); hideOverlay(modal.id); return; }
      if (event.key === 'Tab') { const controls = Array.from(modal.querySelectorAll('button:not(:disabled),input:not(:disabled),select:not(:disabled),textarea:not(:disabled),a[href]')).filter((node) => node.getClientRects().length); const first = controls[0], last = controls[controls.length - 1]; if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); } else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); } }
    }, true);
    return {
      closeEmbedded: () => { if (state.embedded) hideOverlay('intake-modal'); },
      bookmark: bookmarkInfo,
      refreshJobs: async () => { await loadJobs(); schedule(); },
      async start() { try { const providers = await call('media-library.discovery.providers', {}); state.providers = providers.items || []; updateSupport(); } catch (_) { updateSupport(); } try { await loadJobs(); schedule(); } catch (error) { jobsFailed(error); } },
      enterView(view) { if (view !== 'discovery' && state.searchID) cancelSearch(false); state.view = view; if (view === 'bookmarks') void loadBookmarks(); if (view === 'discovery') updateSupport(); },
      settings(tab) { state.settings = tab === 'acquisition' && !el('settings-modal').hidden; if (state.settings) { feedback('acquisition-settings-feedback', ''); void refreshSettings(); } else schedule(); },
      jobs: openJobs,
      open: openIntake,
      preview,
    };
  } };
})();
