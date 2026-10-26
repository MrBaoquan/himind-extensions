(async () => {
  const nav = [...document.querySelectorAll('button, a')].find(item => item.innerText.trim() === '我的能力');
  if (!nav) throw new Error('我的能力入口不存在');
  nav.click();
  await new Promise(resolve => setTimeout(resolve, 900));
  const results = {};
  for (const label of ['插件', '技能', '工作流', '专家', '项目规则']) {
    const tab = [...document.querySelectorAll('.installed-tabs [role="tab"]')].find(item => item.innerText.includes(label));
    if (!tab) {
      results[label] = { tabFound: false };
      continue;
    }
    tab.click();
    await new Promise(resolve => setTimeout(resolve, 900));
    const frames = [...document.querySelectorAll('.extension-icon-frame')];
    const badges = [...document.querySelectorAll('.extension-icon-type-badge')];
    results[label] = {
      tabFound: true,
      heading: document.querySelector('h1')?.innerText || '',
      frames: frames.length,
      badges: badges.length,
      kinds: [...new Set(frames.map(item => item.dataset.iconKind))],
      sources: [...new Set(frames.map(item => item.dataset.iconSource))],
      sizes: frames.slice(0, 3).map(item => ({ width: Math.round(item.getBoundingClientRect().width), height: Math.round(item.getBoundingClientRect().height) })),
      firstTitle: document.querySelector('.plugin-detail-title h3, .skill-detail-title h3, .workflow-detail-title h3, .expert-studio-copy strong')?.innerText || '',
    };
  }
  const pluginTab = [...document.querySelectorAll('.installed-tabs [role="tab"]')].find(item => item.innerText.includes('插件'));
  pluginTab?.click();
  await new Promise(resolve => setTimeout(resolve, 700));
  return results;
})()
