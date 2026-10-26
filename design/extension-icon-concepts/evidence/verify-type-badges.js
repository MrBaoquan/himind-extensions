(async () => {
  const market = [...document.querySelectorAll('button')].find(item => item.innerText.trim() === '市场');
  if (market) market.click();
  let card = null;
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    await new Promise(resolve => setTimeout(resolve, 300));
    card = [...document.querySelectorAll('button')].find(item => item.innerText.includes('开源热榜') && item.innerText.includes('v0.6.5'));
    if (card) break;
  }
  if (!card) throw new Error('等待开源热榜 v0.6.5 超时');
  const listFrames = [...document.querySelectorAll('.market-list .extension-icon-frame.has-brand-icon')];
  const listBadges = [...document.querySelectorAll('.market-list .extension-icon-type-badge')];
  const badgeKinds = listBadges.reduce((counts, badge) => {
    const kind = badge.classList.contains('plugin') ? 'plugin' : badge.classList.contains('skill') ? 'skill' : badge.classList.contains('workflow') ? 'workflow' : badge.classList.contains('expert') ? 'expert' : badge.classList.contains('instruction') ? 'instruction' : 'unknown';
    counts[kind] = (counts[kind] || 0) + 1;
    return counts;
  }, {});
  card.click();
  let frame = null;
  const detailDeadline = Date.now() + 10000;
  while (Date.now() < detailDeadline) {
    await new Promise(resolve => setTimeout(resolve, 200));
    frame = document.querySelector('.plugin-product-title .extension-icon-frame');
    if (frame) break;
  }
  const art = frame?.querySelector('.extension-icon-art');
  const badge = frame?.querySelector('.extension-icon-type-badge');
  const image = art?.querySelector('img');
  const rect = element => element ? { width: element.getBoundingClientRect().width, height: element.getBoundingClientRect().height } : null;
  return {
    title: document.querySelector('.plugin-product-title h3')?.innerText || '',
    listAuthorFrames: listFrames.length,
    listTypeBadges: listBadges.length,
    badgeKinds,
    detailFrame: rect(frame),
    detailArt: rect(art),
    detailBadge: rect(badge),
    detailKind: frame?.getAttribute('data-icon-kind') || '',
    iconSource: frame?.getAttribute('data-icon-source') || '',
    imageComplete: image?.complete || false,
    naturalWidth: image?.naturalWidth || 0,
    naturalHeight: image?.naturalHeight || 0,
  };
})()
