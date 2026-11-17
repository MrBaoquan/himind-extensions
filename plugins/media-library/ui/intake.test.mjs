import fs from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import assert from 'node:assert/strict';

const source = fs.readFileSync(new URL('./intake.js', import.meta.url), 'utf8');
const tick = () => new Promise(setImmediate);
function session() {
  const nodes = new Map(), requests = [];
  const get = id => {
    if (!nodes.has(id)) nodes.set(id, {
      id, value: '', hidden: false, disabled: false, checked: false, textContent: '', innerHTML: '', listeners: {},
      classList: { add() {}, toggle() {} },
      addEventListener(type, fn) { this.listeners[type] = fn; },
      focus() {}, reportValidity() { return Boolean(get('intake-url').value); },
      closest() { return null; }, querySelectorAll() { return []; },
    });
    return nodes.get(id);
  };
  for (const id of ['intake-modal', 'acquisitions-modal', 'settings-modal', 'preview', 'confirm-modal']) get(id).hidden = true;
  get('intake-mode').value = 'import'; get('intake-format').value = 'best';
  const document = { getElementById: get, addEventListener() {}, querySelector: get, activeElement: get('trigger'), body: get('body') };
  const context = { document, URL, console, setTimeout, clearTimeout, crypto: { randomUUID: () => 'key' }, window: {} };
  context.window.crypto = context.crypto;
  vm.runInNewContext(source, context);
  const controller = context.window.MediaLibraryIntake.create({
    invoke: (id, input) => new Promise((resolve, reject) => requests.push({ id, input, resolve, reject })),
    describe: e => e.message, defaultLibrary: () => 'local',
  });
  const request = (id, n = 0) => requests.filter(r => r.id === id)[n];
  const targets = (n = 0) => { get('intake-library').value = 'local'; request('media-library.repository.list', n).resolve({ libraries: [{ id: 'local', name: '素材库', backend_type: 'local', state: 'online' }] }); };
  const submit = () => get('intake-resolve-form').listeners.submit({ preventDefault() {} });
  return { get, controller, requests, request, targets, submit };
}
const a = 'https://www.douyin.com/video/111';
const b = 'https://www.douyin.com/video/222';
const info = title => ({ title, formats: [], subtitles: [] });

test('known work reads once immediately, before targets, and never starts a download implicitly', async () => {
  const s = session(), opening = s.controller.open(a);
  assert.equal(s.request('media-library.remote.resolve').input.url, a);
  assert.equal(s.get('resolve-intake').hidden, true);
  s.submit();
  assert.equal(s.requests.filter(r => r.id === 'media-library.remote.resolve').length, 1);
  s.request('media-library.remote.resolve').resolve(info('A')); await tick();
  assert.equal(s.get('intake-details').hidden, false);
  assert.equal(s.get('start-intake').disabled, true);
  s.targets(); await opening;
  assert.equal(s.get('start-intake').disabled, false);
  assert.equal(s.get('start-intake').textContent, '下载并入库');
  s.get('intake-mode').value = 'save'; s.get('intake-mode').listeners.change();
  assert.equal(s.get('start-intake').textContent, '开始下载');
  assert.equal(s.requests.some(r => r.id === 'media-library.acquisition.start'), false);
});

test('closing and reopening rejects stale work and target responses', async () => {
  const s = session(), old = s.controller.open(a);
  s.get('close-intake').listeners.click();
  const latest = s.controller.open(b);
  s.targets(1); s.request('media-library.remote.resolve', 1).resolve(info('B')); await latest;
  s.request('media-library.repository.list').reject(new Error('old target error'));
  s.request('media-library.remote.resolve').resolve(info('A')); await old; await tick();
  assert.equal(s.get('intake-media-title').textContent, 'B');
  assert.equal(s.get('intake-feedback').hidden, true);
  assert.equal(s.get('start-intake').disabled, false);
});

test('editing while reading invalidates the response and requires continuing with the new link', async () => {
  const s = session(), opening = s.controller.open(a); s.targets();
  s.get('intake-url').value = b; s.get('intake-url').listeners.input();
  s.request('media-library.remote.resolve').resolve(info('A')); await opening;
  assert.equal(s.get('intake-details').hidden, true);
  assert.equal(s.get('resolve-intake').textContent, '继续');
  assert.equal(s.get('start-intake').disabled, true);
  s.submit(); assert.equal(s.request('media-library.remote.resolve', 1).input.url, b);
  s.request('media-library.remote.resolve', 1).resolve(info('B')); await tick();
  assert.equal(s.get('intake-media-title').textContent, 'B');
});

test('work failure exposes retry and retry reads the current URL once', async () => {
  const s = session(), opening = s.controller.open(a); s.targets();
  s.request('media-library.remote.resolve').reject(new Error('作品暂不可用')); await opening;
  assert.equal(s.get('resolve-intake').hidden, false);
  assert.equal(s.get('resolve-intake').textContent, '重试');
  assert.equal(s.get('start-intake').disabled, true);
  s.submit(); s.submit();
  assert.equal(s.requests.filter(r => r.id === 'media-library.remote.resolve').length, 2);
  s.request('media-library.remote.resolve', 1).resolve(info('A')); await tick();
  assert.equal(s.get('resolve-intake').hidden, true);
  assert.equal(s.get('intake-feedback').hidden, true);
});

test('target failure leaves resolved work available for saving a file', async () => {
  const s = session(), opening = s.controller.open(a);
  s.request('media-library.repository.list').reject(new Error('离线'));
  s.request('media-library.remote.resolve').resolve(info('A')); await opening;
  assert.equal(s.get('intake-details').hidden, false);
  assert.match(s.get('intake-target-hint').textContent, /资源库读取失败/);
  assert.equal(s.get('start-intake').disabled, true);
  s.get('intake-mode').value = 'save'; s.get('intake-mode').listeners.change();
  assert.equal(s.get('start-intake').disabled, false);
});

test('manual entry is empty, audio entry auto-reads and labels the actual output', async () => {
  const s = session(), manual = s.controller.open(); s.targets(); await manual;
  assert.equal(s.get('intake-url').value, '');
  assert.equal(s.request('media-library.remote.resolve'), undefined);
  const audio = s.controller.open(a, true); s.targets(1);
  s.request('media-library.remote.resolve').resolve(info('A')); await audio;
  assert.equal(s.get('intake-format').value, 'audio');
  assert.equal(s.get('intake-bgm').checked, true);
  assert.equal(s.get('start-intake').textContent, '提取并入库');
});
