(async () => {
  document.getElementById('market-tab-skill')?.click();
  await new Promise(resolve => setTimeout(resolve, 700));
  return { tab: 'skill', badges: document.querySelectorAll('.market-list .extension-icon-type-badge.skill').length };
})()
