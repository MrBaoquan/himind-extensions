(async () => {
  const results = {};
  for (const kind of ['plugin', 'skill', 'workflow', 'expert']) {
    const tab = document.getElementById(`market-tab-${kind}`);
    if (!tab) throw new Error(`缺少类型页签: ${kind}`);
    tab.click();
    await new Promise(resolve => setTimeout(resolve, 700));
    const frames = [...document.querySelectorAll('.market-list .extension-icon-frame.has-brand-icon')];
    const badges = [...document.querySelectorAll(`.market-list .extension-icon-type-badge.${kind}`)];
    const badge = badges[0];
    const style = badge ? getComputedStyle(badge) : null;
    results[kind] = {
      frames: frames.length,
      badges: badges.length,
      missingBadgeFrames: frames.filter(frame => !frame.querySelector(`.extension-icon-type-badge.${kind}`)).length,
      background: style?.backgroundColor || '',
      color: style?.color || '',
      badgeWidth: badge?.getBoundingClientRect().width || 0,
      badgeHeight: badge?.getBoundingClientRect().height || 0,
      firstTitle: frames[0]?.closest('.market-item')?.querySelector('strong')?.innerText || '',
    };
  }
  const all = document.getElementById('market-tab-all');
  if (all) all.click();
  await new Promise(resolve => setTimeout(resolve, 500));
  return results;
})()
