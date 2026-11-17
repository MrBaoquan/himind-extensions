(function () {
  'use strict';
  const escape = value => String(value == null ? '' : value).replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
  const avatarOK = value => /^data:image\/(?:jpeg|png|webp);base64,[A-Za-z0-9+/=]+$/.test(value || '');
  const count = value => new Intl.NumberFormat('zh-CN', {notation: Number(value) >= 10000 ? 'compact' : 'standard', maximumFractionDigits: 1}).format(value);
  const time = value => { const date = new Date(value); return Number.isFinite(date.getTime()) ? date.toLocaleString('zh-CN', {hour12:false}) : ''; };
  function card(item, reply) {
    const author = escape(item.author || '用户');
    const picture = avatarOK(item.avatar) ? '<img src="' + escape(item.avatar) + '" alt="" loading="lazy">' : '<span>' + escape(Array.from(item.author || '评')[0]) + '</span>';
    return '<article class="comment-card' + (reply ? ' comment-reply' : '') + '"><div class="comment-avatar">' + picture + '</div><div class="comment-copy"><div class="comment-author"><strong>' + author + '</strong>' + (item.is_author ? '<span class="comment-badge">作者</span>' : '') + '</div><p class="comment-text">' + escape(item.text) + '</p><div class="comment-meta">' + (item.published ? '<span>' + escape(item.published) + '</span>' : '') + (Number.isFinite(item.like_count) ? '<span>点赞 ' + count(item.like_count) + '</span>' : '') + '</div>' + (!reply && item.replies && item.replies.length ? '<details class="comment-replies"><summary>展开 ' + item.replies.length + ' 条已读取回复</summary>' + item.replies.map(r => card(r, true)).join('') + '</details>' : '') + '</div></article>';
  }
  window.MediaLibraryComments = {card, create({invoke, describe = error => String(error.message || error)}) {
    const el = id => document.getElementById(id);
    let generation = 0, reference = {}, supported = true, initialized = false, busy = false, saving = false, requestID = '';
    let items = [], sampleID = '', cursor = '', observed = '', loaded = 0, mode = '', stored = null;
    function status(message, error) { el('comments-status').textContent = message; el('comments-status').classList.toggle('is-error', Boolean(error)); }
    function controls() {
      el('comments-refresh').disabled = busy || !supported;
      el('comments-cancel').hidden = !busy;
      el('comments-save').disabled = busy || saving || !sampleID || !items.length;
      el('comments-snapshot').disabled = busy || !stored;
      el('comments-more').hidden = !cursor;
      el('comments-more').disabled = busy;
      el('comments-sort').disabled = busy || !items.length;
      el('comments-body').setAttribute('aria-busy', String(busy));
    }
    function render() {
      let view = [...items];
      if (el('comments-sort').value === 'likes') view.sort((a,b) => (b.like_count ?? -1) - (a.like_count ?? -1));
      el('comments-list').innerHTML = view.map(item => card(item, false)).join('');
      el('comments-count').textContent = items.length ? items.length + ' 条' : '';
      el('comments-context').textContent = observed ? (mode === 'snapshot' ? '已存快照 · ' : '官网采样 · ') + time(observed) + (loaded > items.length ? ' · 共读取 ' + loaded + ' 条' : '') : '';
      controls();
    }
    function snapshot() {
      if (!stored) return;
      const value = stored.snapshot; items = value.items || []; sampleID = ''; cursor = ''; observed = value.observed_at; loaded = items.length; mode = 'snapshot';
      el('comments-sort').value = 'likes'; status('已保存的热门采样，评论和回复可能已变化。'); render();
    }
    async function cancel() {
      const id = requestID; generation++; busy = false; requestID = ''; status('读取已取消，可重新读取。'); controls();
      if (id) try { await invoke('media-library.discovery.cancel', {request_id:id}); } catch (_) {}
    }
    async function read(more) {
      if (busy || !supported) return;
      const token = generation, ref = {...reference};
      busy = true; requestID = more ? '' : crypto.randomUUID().replace(/-/g, '');
      const id = requestID;
      status(more ? '正在加载评论…' : '正在读取官网评论…如出现验证，请在官网窗口完成。'); controls();
      try {
        const data = await invoke('media-library.comments.list', {...ref, ...(more ? {cursor} : {request_id:id})});
        if (token !== generation) return;
        items = more ? items.concat(data.items || []) : data.items || []; sampleID = data.sample_id || ''; cursor = data.next_cursor || ''; observed = data.observed_at || ''; loaded = data.loaded_count || items.length; mode = 'live';
        if (!more) el('comments-sort').value = 'official';
        status(items.length ? '仅展示官网本次可见采样，回复需展开；不代表完整评论列表。' : '官网显示暂无评论或评论已关闭。'); render();
      } catch (error) { if (token === generation) status(describe(error), true); }
      finally { if (token === generation) { busy = false; requestID = ''; controls(); } }
    }
    async function activate() {
      if (initialized) return; initialized = true;
      if (!supported) { status('评论暂支持有抖音来源的视频与音频。'); return; }
      const token = generation;
      let checked = false;
      busy = true; status('正在查找已保存的评论快照…'); controls();
      try {
        const result = await invoke('media-library.comments.snapshot.get', reference);
        if (token !== generation) return;
        stored = result.snapshot ? result : null;
        checked = true;
        if (stored) snapshot();
      } catch (error) {
        if (token !== generation) return;
        // An unlinked local asset is a supported empty state, not a login loop.
        status(describe(error), true);
      } finally { if (token === generation) { busy = false; controls(); } }
      if (token === generation && supported && checked && !stored) await read(false);
    }
    async function save() {
      if (saving || busy || !sampleID) return;
      const token = generation; saving = true; status('正在保存热门评论快照…'); controls();
      try {
        const result = await invoke('media-library.comments.snapshot.save', {...reference, sample_id:sampleID});
        if (token !== generation) return;
        stored = result; status('已保存 ' + (result.snapshot?.items?.length || 0) + ' 条热门采样，来源相同的入库素材也可查看。');
      } catch (error) { if (token === generation) status(describe(error), true); }
      finally { if (token === generation) { saving = false; controls(); } }
    }
    function close() { if (requestID) void invoke('media-library.discovery.cancel', {request_id:requestID}).catch(() => {}); generation++; requestID=''; busy=false; saving=false; }
    function begin(item) {
      close(); reference = item.source_url ? {url:item.source_url} : {item_id:item.id || ''};
      supported = item.source_url ? /^https:\/\/(?:www\.)?douyin\.com\/(?:video\/\d+|\?[^#]*modal_id=\d+)/.test(item.source_url) : item.entry_type === 'library';
      initialized=false; items=[]; sampleID=''; cursor=''; observed=''; loaded=0; mode=''; stored=null;
      el('comments-sort').value='official'; status(supported ? '打开评论后按需读取。' : '评论暂支持有抖音来源的视频与音频。'); render();
    }
    el('comments-refresh').addEventListener('click', () => { void read(false); });
    el('comments-cancel').addEventListener('click', () => { void cancel(); });
    el('comments-more').addEventListener('click', () => { void read(true); });
    el('comments-save').addEventListener('click', () => { void save(); });
    el('comments-snapshot').addEventListener('click', snapshot);
    el('comments-sort').addEventListener('change', render);
    return {begin, activate, close};
  }};
})();
