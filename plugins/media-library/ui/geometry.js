(function () {
  'use strict';
  // An explicit developer shortcut measures the live WebView DOM. Never read text/URLs.
  const groups = [
    { name: 'discovery', container: '#view-discovery' },
    { name: 'bookmarks', container: '#view-bookmarks' },
    { name: 'intake', container: '.intake-dialog' },
    { name: 'acquisitions', container: '.acquisition-dialog' },
    { name: 'audio', container: '#audio-editor', header: '#audio-editor-summary', body: '#audio-extract-form', fullWidth: '#audio-title', alignedRight: '#audio-submit' },
    { name: 'music-card', container: '#discovery-results > .music-card:first-child', header: '#discovery-results > .music-card:first-child > .remote-card-copy', body: '#discovery-results > .music-card:first-child > .music-usage-list', fullWidth: '#discovery-results > .music-card:first-child > .music-usage-list > summary' },
  ];
  const visible = (node) => node && node.getClientRects().length && getComputedStyle(node).visibility !== 'hidden';
  function measure(selector) {
    const node = document.querySelector(selector), r = node.getBoundingClientRect(), s = getComputedStyle(node);
    const border = { left: parseFloat(s.borderLeftWidth) || 0, right: parseFloat(s.borderRightWidth) || 0 };
    return { selector, rect: { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height }, padding: { left: parseFloat(s.paddingLeft) || 0, right: parseFloat(s.paddingRight) || 0 }, border, scrollbar_width: Math.max(0, node.offsetWidth - node.clientWidth - border.left - border.right) };
  }
  groups.push({ name: 'preview', container: '.detail-info', header: '.detail-info > .detail-info-head', body: '#preview-meta', fullWidth: '#preview-meta' });
  groups.push({ name: 'preview-source', container: '#preview-source-panel', header: '#preview-source-panel > h3', body: '#preview-source-meta', fullWidth: '#preview-source-meta' });
  groups.push({ name: 'comments', container: '#preview-comments-panel', header: '.comments-head', body: '#comments-body', fullWidth: '#comments-list' });
  groups.push({ name: 'generation', container: '#generation-results', header: '.generation-result-head', body: '#generation-output-list', fullWidth: '#generation-output-list' });
  groups.push({name:'image-editor',container:'#image-editor',header:'.image-editor-head',body:'.image-editor-footer',fullWidth:'.image-editor-save'});
  const sampleSelectors = ['#discovery-query', '#discovery-platform', '#discovery-submit', '#discovery-cancel', '#discovery-support', '#discovery-feedback', '#discovery-results', '#discovery-results > .remote-card:first-child', '#discovery-results > .remote-card:first-child h3', '#discovery-results > .remote-card:first-child .remote-card-actions', '#discovery-results > .remote-card:first-child [data-discovery-collect]', '#discovery-more', '#bookmark-results', '#bookmark-query', '#intake-url', '#intake-format', '#start-intake', '#acquisition-jobs', '#acquisition-jobs .job-message', '#legacy-downloads', '#legacy-downloads > summary', '#import-legacy-downloads', '#legacy-feedback', '#legacy-download-list', '#legacy-download-list .legacy-path'];
  sampleSelectors.push('.generation-result-body','#generation-help','#generation-spec','#generation-records','#generation-new','#generation-reference-list','.generation-dialog','.generation-picker-item','.generation-picker-art img','#generation-undo','#generation-savebar','#generation-selected','#generation-snapshot','#generation-record-filter');
  sampleSelectors.push('#view-generation','.generation-head','.generation-layout','#generation-form','.generation-form-fields','.generation-form-footer','#generation-results','#generation-prompt','#generation-route','#generation-library','#generation-history','#generation-submit','#generation-feedback','#generation-output-list','#generation-collapse','#generation-availability','#open-generation');
  sampleSelectors.push('#audio-editor-summary', '#audio-title', '#audio-start', '#audio-end', '#audio-format', '#audio-library', '#audio-audition', '#audio-submit', '#audio-feedback', '#discovery-modes', '#browse-bgm');
  sampleSelectors.push(...["#preview-media","#preview-media video","#close-preview","#retry-preview","#remote-preview-actions","#remote-preview-collect","#remote-preview-audio","#discovery-results > .remote-card:first-child .remote-video-cover","#discovery-results > .remote-card:first-child .remote-title"]);
  sampleSelectors.push('#discovery-results > .remote-card:first-child .remote-card-copy','#discovery-results > .remote-card:first-child .remote-engagement','#discovery-results > .remote-card:first-child .remote-engagement-time','#discovery-results > .music-card:first-child .music-usage-row .remote-engagement');
  sampleSelectors.push('#preview', '.detail-head', '.detail-content', '.preview-stage', '#preview-inspector', '.preview-inspector-tabs', '.preview-inspector-scroll', '.detail-footer', '#preview-tools', '#preview-filmstrip', '#preview-meta', '#preview-source-meta', '#asset-note', '#audio-waveform', '#audio-wave-canvas', '#audio-wave-start', '#audio-wave-end', '#preview-operation-slot', '#preview-play', '#preview-toggle-info');
  sampleSelectors.push('#preview-media .empty-state', '#preview-media .empty-state strong', '#preview-media .empty-state .meta', '#preview-time', '#audio-wave-status', '#audio-selection-duration');
  sampleSelectors.push('#preview-comments-tab', '#preview-comments-panel', '.comments-head', '.comments-actions', '#comments-status', '#comments-body', '#comments-list', '.comment-card', '.comment-text', '#comments-refresh', '#comments-save', '#comments-more');
  sampleSelectors.push("#image-editor",".image-editor-head",".image-editor-body",".image-editor-footer",".image-editor-settings",".image-editor-save","#image-editor-stage","#image-editor-canvas","#image-editor-versions","#image-editor-apply","#image-editor-save","#image-editor-feedback","#image-editor-close","#image-editor-ai-availability","#image-editor-generate","#image-editor-width","#image-editor-library","#image-editor-prompt","#image-editor-compare","#image-editor-selection");
  function rgb(value) { const parts = String(value).match(/[\d.]+/g); return parts && parts.length >= 3 ? parts.slice(0, 3).map(Number) : [255, 255, 255]; }
  function luminance(color) { const a = color.map((v) => { v /= 255; return v <= .04045 ? v / 12.92 : Math.pow((v + .055) / 1.055, 2.4); }); return .2126 * a[0] + .7152 * a[1] + .0722 * a[2]; }
  function ratio(a, b) { const x = luminance(a), y = luminance(b); return (Math.max(x, y) + .05) / (Math.min(x, y) + .05); }
  function sample(selector) {
    const node = document.querySelector(selector), s = getComputedStyle(node);
    let parent = node, background = 'rgb(255,255,255)';
    while (parent) { const color = getComputedStyle(parent).backgroundColor; if (color !== 'transparent' && color !== 'rgba(0, 0, 0, 0)') { background = color; break; } parent = parent.parentElement; }
    return Object.assign(measure(selector), { font_size: parseFloat(s.fontSize) || 0, text_contrast: ratio(rgb(s.color), rgb(background)), border_contrast: ratio(rgb(s.borderLeftColor), rgb(background)), focused: node === document.activeElement, scroll_width: node.scrollWidth, client_width: node.clientWidth });
  }
  let busy = false;
  document.addEventListener('keydown', async (event) => {
    const shortcut = event.key === 'F8' || (event.ctrlKey && event.shiftKey && !event.altKey && (event.code === 'KeyG' || String(event.key).toLowerCase() === 'g'));
    if (!shortcut || busy) return;
    event.preventDefault(); busy = true;
    let status = document.getElementById('geometry-diagnostic-status');
    if (!status) { status = document.createElement('div'); status.id = 'geometry-diagnostic-status'; status.setAttribute('role', 'status'); status.style.cssText = 'position:fixed;bottom:16px;left:16px;z-index:60;padding:8px 12px;background:var(--color-surface);color:var(--color-text);border:1px solid var(--color-border-strong);border-radius:6px'; document.body.appendChild(status); }
    status.hidden = false; status.textContent = '正在记录布局报告…';
    try {
      const snapshot = { viewport: { width: innerWidth, height: innerHeight, dpr: devicePixelRatio, scroll_width: document.documentElement.scrollWidth }, groups: groups.filter((g) => visible(document.querySelector(g.body || g.container))).map((g) => ({ name: g.name, elements: { container: measure(g.container), header: measure(g.header || g.container + ' > .remote-head'), body: measure(g.body || g.container + ' > .remote-body'), fullWidth: [measure(g.fullWidth || (g.name === 'acquisitions' ? '.acquisition-dialog > .remote-body > .acquisition-jobs' : g.container + ' > .remote-body > :first-child'))], alignedRight: g.alignedRight ? [measure(g.alignedRight)] : g.name === 'intake' ? [measure('#start-intake')] : [] } })) };
      const media = document.querySelector('#preview-media video,#preview-media audio');
      snapshot.media = { present: Boolean(media), paused: media ? media.paused : true, current_time: media && Number.isFinite(media.currentTime) ? media.currentTime : 0, duration: media && Number.isFinite(media.duration) ? media.duration : 0, ready_state: media ? media.readyState : 0, error_code: media && media.error ? media.error.code : 0, cover_count: document.querySelectorAll('.remote-video-cover img').length, loaded_covers: Array.from(document.querySelectorAll('.remote-video-cover img')).filter((i) => i.complete && i.naturalWidth > 0).length };
      snapshot.samples = sampleSelectors.filter((selector) => visible(document.querySelector(selector))).map(sample);
      await window.__TAURI_INTERNALS__.invoke('invoke_plugin_view_capability', { capabilityId: 'media-library.ui.geometry.record', input: snapshot });
      status.textContent = '布局报告已记录';
    } catch (_) { status.textContent = '布局报告未记录，请重新打开视图后重试。'; }
    finally { busy = false; setTimeout(() => { status.hidden = true; }, 5000); }
  }, true);
})();
