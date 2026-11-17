import fs from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import assert from 'node:assert/strict';

// Execute the shipped controller. Only rendering is replaced in race tests;
// requests remain deferred so they can resolve in any order.
const source = fs.readFileSync(new URL('./app.js', import.meta.url), 'utf8');
function session() {
  const nodes = new Map();
  function node() {
    return {
      value: '', checked: false, disabled: false, hidden: false, innerHTML: '', textContent: '',
      attributes: {}, listeners: {}, children: [], style: {},
      selectedOptions: [{ textContent: '自动选择' }],
      addEventListener(type, fn) { this.listeners[type] = fn; },
      setAttribute(key, value) { this.attributes[key] = value; },
      getAttribute(key) { return this.attributes[key] || null; },
      querySelector() { return this.label ||= node(); },
      querySelectorAll() { return []; },
      appendChild(child) { this.children.push(child); },
      focus() {}, scrollIntoView() {}, remove() {},
      classList: { add() {}, remove() {}, toggle() {} },
    };
  }
  const get = id => {
    if (!nodes.has(id)) nodes.set(id, node());
    return nodes.get(id);
  };
  const requests = [];
  const context = {
    console, URL, Set, Map, Date, Number, String, JSON, Array, Promise,
    navigator: {},
    document: {
      getElementById: get, querySelectorAll: () => [], querySelector: () => null,
      createElement: node, createTextNode: text => ({ textContent: text }),
      addEventListener() {}, body: { classList: { toggle() {} } },
    },
    window: {
      localStorage: { getItem: () => null }, addEventListener() {}, setTimeout() {}, clearTimeout() {}, setInterval() {}, clearInterval() {},
      __TAURI_INTERNALS__: { invoke(command, input) {
        return new Promise((resolve, reject) => requests.push({ command, input, resolve, reject }));
      } },
    },
  };
  const expose = `globalThis.controller = { state, probe, collectDownloadInput, bindEvents,
    resetComposer, startDownload, renderMedia, loadJobs, installRuntime, updateDownloadSummary, updateOverview,
    loadSite, connectSite, renderSite };
    renderMedia = () => {};`;
  vm.runInNewContext(source.replace("window.addEventListener('DOMContentLoaded', () => { void boot(); });", expose), context);
  const a = context.controller;
  a.bindEvents();
  get('url').value = 'https://example.org/a.mp4';
  a.state.runtime = { state: 'ready', ffmpeg_path: 'ffmpeg' };
  return { get, a, requests };
}

test('editing an initial pending URL discards its result and cannot start a job', async () => {
  const { a, get, requests } = session();
  const pending = a.probe();
  get('url').value = 'https://example.org/b.mp4';
  get('url').listeners.input();
  requests[0].resolve({ title: 'Video A' });
  await pending;
  assert.equal(a.state.media, null);
  assert.equal(get('probe-button').disabled, false);
  assert.equal(get('start-button').disabled, true);
  assert.throws(() => a.collectDownloadInput(), /重新解析/);
  await a.startDownload();
  assert.equal(requests.length, 1);
});

test('connecting Douyin resumes the same pending parse once and preserves options', async () => {
  const { a, get, requests } = session();
  get('url').value = 'https://www.douyin.com/video/123';
  get('dest-dir').value = 'C:\\Downloads\\MyVideos';
  const first = a.probe();
  requests[0].reject(new Error('site_connection_required: connect first'));
  await first;
  assert.equal(get('site-connection').hidden, false);
  assert.equal(get('start-button').disabled, true);
  const connecting = a.connectSite();
  requests[1].resolve({ state: 'connecting' });
  await connecting;
  assert.equal(get('site-connect').disabled, true);
  const poll = a.loadSite();
  requests[2].resolve({ state: 'connected' });
  await new Promise(setImmediate);
  assert.equal(requests.length, 4);
  assert.equal(requests[3].input.input.url, 'https://www.douyin.com/video/123');
  requests[3].resolve({ title: 'Douyin' });
  await poll;
  assert.equal(a.state.media.title, 'Douyin');
  assert.equal(get('dest-dir').value, 'C:\\Downloads\\MyVideos');
  assert.equal(get('site-connection').hidden, true);
});

test('editing a URL while connecting never resumes the old parse', async () => {
  const { a, get, requests } = session();
  get('url').value = 'https://www.douyin.com/video/123';
  const first = a.probe(); requests[0].reject(new Error('site_connection_expired'));
  await first;
  get('url').value = 'https://example.org/new.mp4';
  get('url').listeners.input();
  const poll = a.loadSite(); requests[1].resolve({ state: 'connected' }); await poll;
  assert.equal(requests.length, 2);
  assert.equal(a.state.media, null);
  assert.equal(get('site-connection').hidden, true);
});

test('late status responses cannot overwrite a newer connection operation', async () => {
  const { a, requests } = session();
  const old = a.loadSite();
  const connecting = a.connectSite(); requests[1].resolve({ state: 'connecting' }); await connecting;
  requests[0].resolve({ state: 'connected' }); await old;
  assert.equal(a.state.site.state, 'connecting');
});

test('an expired session after reconnect prompts again without an automatic loop', async () => {
  const { a, get, requests } = session();
  get('url').value = 'https://www.douyin.com/video/123';
  const first = a.probe(); requests[0].reject(new Error('site_connection_required')); await first;
  const poll = a.loadSite(); requests[1].resolve({ state: 'connected' });
  await new Promise(setImmediate);
  requests[2].reject(new Error('site_connection_expired')); await poll;
  assert.equal(requests.length, 3);
  assert.equal(a.state.site.state, 'expired');
  assert.equal(get('probe-button').disabled, false);
  assert.equal(get('site-connect').disabled, false);
});

test('extractor rejection keeps the login and shows a readable error without reconnecting', async () => {
  const { a, get, requests } = session();
  get('url').value = 'https://www.douyin.com/jingxuan/search/test?modal_id=123';
  get('dest-dir').value = 'C:\\Downloads\\Videos';
  a.state.site = { state: 'connected' };
  const pending = a.probe();
  const message = '抖音已连接，但站点未向下载器返回视频详情。登录状态已保留；当前下载器尚不能解析此视频，请稍后重试。';
  requests[0].reject(new Error('plugin capability media.downloader.info.get failed: ' + JSON.stringify({ code: -32000, message })));
  await pending;
  assert.equal(a.state.site.state, 'connected');
  assert.equal(a.state.resumeProbe, null);
  assert.equal(get('site-connection').hidden, true);
  assert.ok(get('probe-state').innerHTML.includes('解析失败：' + message));
  assert.ok(!get('probe-state').innerHTML.includes('plugin capability'));
  assert.equal(get('probe-button').disabled, false);
  assert.equal(get('dest-dir').value, 'C:\\Downloads\\Videos');
});

test('changing playlist restores controls and late errors cannot overwrite newer success', async () => {
  const { a, get, requests } = session();
  const first = a.probe();
  get('playlist').checked = true;
  get('playlist').listeners.change();
  assert.equal(get('probe-button').disabled, false);
  const second = a.probe();
  requests[1].resolve({ title: 'Playlist B', is_playlist: true });
  await second;
  requests[0].reject(new Error('old request failed'));
  await first;
  assert.equal(a.state.media.title, 'Playlist B');
  assert.equal(a.collectDownloadInput().playlist, true);
  assert.equal(get('probe-button').disabled, false);
});

test('cancel and reset ignore old results without enabling a newer pending probe', async () => {
  const { a, get, requests } = session();
  const first = a.probe();
  get('cancel-probe').listeners.click();
  assert.equal(get('cancel-probe').hidden, true);
  const second = a.probe();
  requests[0].resolve({ title: 'Old' });
  await first;
  assert.equal(get('probe-button').disabled, true);
  a.resetComposer();
  requests[1].resolve({ title: 'Also old' });
  await second;
  assert.equal(a.state.media, null);
  assert.equal(get('probe-button').disabled, false);
});

test('Enter while parsing does not duplicate requests; invalid input gets an associated error', async () => {
  const { a, get, requests } = session();
  const pending = a.probe();
  await a.probe();
  assert.equal(requests.length, 1);
  requests[0].resolve({ title: 'A' });
  await pending;
  assert.equal(get('start-button').disabled, false);
  get('url').value = 'not-a-url';
  await a.probe();
  assert.equal(get('url').attributes['aria-invalid'], 'true');
  assert.equal(a.state.media, null);
  get('url').listeners.input();
  assert.equal(get('url').attributes['aria-invalid'], 'false');
});

test('programmatic URL change without input event is checked before applying a result', async () => {
  const { a, get, requests } = session();
  const pending = a.probe();
  get('url').value = 'https://example.org/b.mp4';
  requests[0].resolve({ title: 'A' });
  await pending;
  assert.equal(a.state.media, null);
  assert.equal(get('probe-button').disabled, false);
});

test('raw video stream selection disables conflicting controls and sends no quality cap', async () => {
  const { a, get, requests } = session();
  const pending = a.probe();
  requests[0].resolve({ title: 'A' });
  await pending;
  get('quality').value = '720p';
  get('format').value = '137';
  a.updateDownloadSummary();
  assert.equal(get('quality').disabled, true);
  assert.equal(get('audio-only').disabled, true);
  assert.equal(a.collectDownloadInput().quality, 'best');
  assert.equal(get('summary-quality').textContent, '由原始格式决定');
  get('format').value = '';
  get('audio-only').checked = true;
  a.updateDownloadSummary();
  assert.equal(get('format').disabled, true);
  assert.equal(get('summary-quality').textContent, '不适用（仅音频）');
});

test('raw stream options identify video with audio and streams needing merge', () => {
  const { a, get } = session();
  a.renderMedia({ title: 'A', formats: [
    { format_id: '137', vcodec: 'avc1', acodec: 'none', height: 1080 },
    { format_id: '18', vcodec: 'avc1', acodec: 'aac', height: 360 },
    { format_id: '140', vcodec: 'none', acodec: 'aac' },
  ] });
  assert.equal(get('format').children.length, 2);
  assert.match(get('format').children[0].textContent, /自动合并音频/);
  assert.match(get('format').children[1].textContent, /音视频/);
});

test('initial silent task failure shows persistent error instead of empty success', async () => {
  const { a, get, requests } = session();
  const pending = a.loadJobs({ silent: true });
  requests[0].reject(new Error('network unavailable'));
  await pending;
  assert.equal(a.state.jobsLoaded, false);
  assert.equal(get('jobs-error').hidden, false);
  assert.equal(get('tasks-empty').hidden, true);
  assert.match(get('jobs-error-message').textContent, /尚未读取到记录/);
  assert.equal(get('tasks-body').innerHTML, '');
  assert.equal(get('stat-active').textContent, '—');
  assert.equal(get('stat-output').textContent, '—');
  assert.equal(get('overview-records').textContent, '未读取');
  assert.match(get('stats-freshness').textContent, /未读取/);
});

test('overview distinguishes a measured zero from stale statistics after a refresh failure', async () => {
  const { a, get, requests } = session();
  const first = a.loadJobs({ silent: true });
  requests[0].resolve({ items: [], total: 0, summary: { total: 0, active: 0, completed: 0, failed: 0, output_bytes: 0 } });
  await first;
  assert.equal(get('stat-active').textContent, '0');
  assert.equal(get('stat-output').textContent, '0 B');
  assert.equal(get('overview-records').textContent, '0 条');
  const refresh = a.loadJobs({ silent: true });
  requests[1].reject(new Error('offline'));
  await refresh;
  assert.equal(get('stat-active').textContent, '0');
  assert.match(get('stats-freshness').textContent, /可能已过期/);
});

test('failed refresh retains last valid task data and successful retry clears error', async () => {
  const { a, get, requests } = session();
  a.state.jobsLoaded = true;
  a.state.jobs = [{ job_id: 'existing', title: 'Existing', status: 'completed' }];
  const pending = a.loadJobs({ silent: true });
  requests[0].reject(new Error('offline'));
  await pending;
  assert.equal(a.state.jobs[0].job_id, 'existing');
  assert.equal(get('jobs-error').hidden, false);
  assert.match(get('jobs-error-message').textContent, /保留上次/);
  const retry = a.loadJobs({ silent: true });
  requests[1].resolve({ items: [], total: 0 });
  await retry;
  assert.equal(a.state.jobsError, '');
  assert.equal(get('jobs-error').hidden, true);
  assert.equal(get('tasks-empty-title').textContent, '还没有下载记录');
  a.state.filters.query = 'not found';
  const filtered = a.loadJobs({ silent: true });
  requests[2].resolve({ items: [], total: 0 });
  await filtered;
  assert.equal(get('tasks-empty-title').textContent, '没有匹配的下载记录');
});

test('installation has shared busy state and persistent failure with a retry', async () => {
  const { a, get, requests } = session();
  const pending = a.installRuntime();
  await a.installRuntime();
  assert.equal(requests.length, 1);
  assert.equal(get('overview-install').disabled, true);
  assert.equal(get('runtime-install-btn').disabled, true);
  assert.match(get('runtime-install-state').textContent, /正在下载并校验/);
  requests[0].reject(new Error('connection failed'));
  await pending;
  assert.equal(get('overview-install').disabled, false);
  assert.equal(get('runtime-install-btn').disabled, false);
  assert.equal(get('runtime-install-state').hidden, false);
  assert.match(get('runtime-install-state').textContent, /安装失败/);
});
