(() => {
  const rect = element => element ? { x: Math.round(element.getBoundingClientRect().x), y: Math.round(element.getBoundingClientRect().y), width: Math.round(element.getBoundingClientRect().width), height: Math.round(element.getBoundingClientRect().height), right: Math.round(element.getBoundingClientRect().right) } : null;
  const workspace = document.querySelector('.market-workspace');
  const browser = document.querySelector('.market-browser');
  const result = document.querySelector('.plugin-catalog-result');
  const list = document.querySelector('.market-list');
  const item = document.querySelector('.market-item');
  const frame = document.querySelector('.market-item .extension-icon-frame');
  const badge = document.querySelector('.market-item .extension-icon-type-badge');
  return {
    viewport: { width: innerWidth, height: innerHeight, scrollWidth: document.documentElement.scrollWidth },
    workspace: rect(workspace),
    browser: rect(browser),
    result: rect(result),
    resultChildren: [...(result?.children || [])].map(rect),
    list: rect(list),
    item: rect(item),
    frame: rect(frame),
    badge: rect(badge),
    itemScrollWidth: item?.scrollWidth || 0,
    browserScrollWidth: browser?.scrollWidth || 0,
  };
})()
