import fs from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import assert from 'node:assert/strict';

const source = fs.readFileSync(new URL('./intake.js', import.meta.url), 'utf8');
const context = { window: {} };
vm.runInNewContext(source.replace('  window.MediaLibraryIntake =', '  window.renderEngagement = engagementMarkup; window.MediaLibraryIntake ='), context);
const render = stats => context.window.renderEngagement({ provider: 'douyin', engagement: stats });

test('search counters keep measured zero distinct from missing and invalid values', () => {
  const markup = render({ like_count: 0, favorite_count: null, comment_count: -1, share_count: '123' });
  assert.match(markup, /点赞 <span class="remote-counter">0</);
  assert.equal((markup.match(/remote-counter">—</g) || []).length, 3);
  assert.doesNotMatch(markup, /<time/);
  assert.equal((render().match(/remote-counter">—</g) || []).length, 4);
});

test('large counts use Chinese units with exact tooltips and observation time', () => {
  const markup = render({ like_count: 123456, favorite_count: 9876, comment_count: 10000, share_count: 123456789, observed_at: '2026-11-14T10:00:00Z' });
  for (const value of ['12.3万', '9,876', '1万', '1.2亿', '123,456', '123,456,789', '获取于', '非实时更新']) assert.ok(markup.includes(value), value);
  assert.match(markup, /datetime="2026-11-14T10:00:00Z"/);
});

test('invalid dates and platform data cannot inject markup or cross platforms', () => {
  const markup = render({ like_count: '<img src=x onerror=alert(1)>', observed_at: '"><img src=x>' });
  assert.doesNotMatch(markup, /<img|<time/);
  assert.equal(context.window.renderEngagement({ provider: 'youtube', engagement: { like_count: 100 } }), '');
});
