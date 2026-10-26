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
  const listIcons = [...document.querySelectorAll('.market-list .extension-brand-icon')];
  const loaded = listIcons.filter(host => {
    const image = host.querySelector('img');
    return host.dataset.iconSource === 'author' && image?.complete && image.naturalWidth > 0;
  });
  card.click();
  let host = null;
  const detailDeadline = Date.now() + 10000;
  while (Date.now() < detailDeadline) {
    await new Promise(resolve => setTimeout(resolve, 200));
    host = document.querySelector('.plugin-product-title .extension-brand-icon');
    if (host) break;
  }
  const image = host?.querySelector('img');
  return {
    title: document.querySelector('.plugin-product-title h3')?.innerText || '',
    listAuthorIcons: listIcons.length,
    loadedAuthorIcons: loaded.length,
    iconSource: host?.getAttribute('data-icon-source') || '',
    currentSrc: image?.currentSrc || '',
    complete: image?.complete || false,
    naturalWidth: image?.naturalWidth || 0,
    naturalHeight: image?.naturalHeight || 0,
  };
})()
