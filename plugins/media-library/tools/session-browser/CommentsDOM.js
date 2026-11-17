(() => {
  // Read only rendered, public comment content. Never inspect React state,
  // request bodies, storage, cookies or network responses.
  const visible = n => n && n.getClientRects().length && getComputedStyle(n).visibility !== 'hidden';
  const text = n => n ? (n.innerText || n.textContent || '').trim() : '';
  const rootSelector = '[data-e2e="comment-item"], .comment-item';
  const roots = Array.from(document.querySelectorAll(rootSelector)).filter(visible);
  document.querySelectorAll('video').forEach(video => video.pause());
  const output = [];
  const read = (node, parent) => {
    const info = node.querySelector('.comment-item-info-wrap');
    const authorNode = node.querySelector('[data-e2e="comment-user-name"], [data-e2e="comment-user"] a, a[href*="/user/"]');
    // The standalone desktop page uses semantic wrappers rather than the
    // data-e2e fields used by other Douyin surfaces. The body and date are
    // siblings of the author wrapper; exclude nested reply trees.
    const bodyNode = node.querySelector('[data-e2e="comment-content"], [data-e2e="comment-text"]') || info?.nextElementSibling;
    const timeNode = node.querySelector('[data-e2e="comment-time"]') || bodyNode?.nextElementSibling;
    const stats = node.querySelector('.comment-item-stats-container');
    const author = text(authorNode).slice(0,120), body = text(bodyNode).slice(0,2000), published = text(timeNode).slice(0,100);
    if (!author || !body) return null;
    const key = JSON.stringify([author, body, published]);
    return {key, parent_key: parent || '', author:author.slice(0,120), text:body.slice(0,2000), published:published.slice(0,100),
      likes: text(node.querySelector('[data-e2e="comment-like-count"], [data-e2e="comment-like"]') || stats?.querySelector('p span')),
      avatar_url: node.querySelector('.comment-item-avatar img')?.src || (getComputedStyle(node.querySelector('.comment-item-avatar > div') || node).backgroundImage.match(/^url\(["']?(https:\/\/[^"')]+)["']?\)$/)?.[1] || ''),
      is_author: Array.from((info || node).querySelectorAll('[data-e2e="comment-author-tag"], span')).some(n => text(n) === '作者' && visible(n))};
  };
  for (const node of roots.slice(0, 80)) {
    // Nested comment-item nodes are read as replies only under their parent.
    if (node.parentElement?.closest(rootSelector)) continue;
    const item = read(node, ''); if (!item) continue; output.push(item);
    const replies = node.querySelectorAll('[data-e2e="comment-reply-item"], '+rootSelector);
    for (const reply of replies) { const value = read(reply, item.key); if (value) output.push(value); }
  }
  const panel = document.querySelector('[data-e2e="comment-list"], [data-e2e="comment-list-container"]');
  const challenge = Array.from(document.querySelectorAll('#captcha_container,[class*="captcha-verify"],iframe[src*="captcha"]')).some(n => visible(n) && n.getBoundingClientRect().width > 40 && n.getBoundingClientRect().height > 40);
  const empty = !!panel && /暂无评论|还没有评论|评论已关闭/.test(text(panel)) && !output.length;
  // Perform only ordinary site actions: open comments, expand visible replies
  // and scroll the comment panel. Do not interact with login or challenges.
  if (!challenge) {
    if (!roots.length) {
      const button = document.querySelector('[data-e2e="video-comment-count"], [data-e2e="video-player-comment"], [data-e2e="feed-comment-icon"]');
      if (visible(button) && !window.__hmCommentOpened) { button.click(); window.__hmCommentOpened = true; }
    }
    window.__hmReplyExpanded = window.__hmReplyExpanded || new WeakSet();
    let expanded = 0;
    for (const node of roots.slice(0, 8)) {
      const button = Array.from(node.querySelectorAll('button,span,[role="button"]')).find(n => visible(n) && /^展开\s*\d+\s*条回复$|^展开更多回复$/.test(text(n)));
      if (button && !window.__hmReplyExpanded.has(button) && expanded++ < 2) { window.__hmReplyExpanded.add(button); button.click(); }
    }
    const ancestors = []; for(let n=panel?.parentElement;n;n=n.parentElement) ancestors.push(n);
    const scrollable = (panel ? [panel, ...Array.from(panel.querySelectorAll('*')), ...ancestors] : [document.scrollingElement,...document.querySelectorAll('main,section,div')]).filter(n => visible(n) && n.clientHeight > 100 && n.scrollHeight > n.clientHeight + 20 && (n===document.scrollingElement || /auto|scroll/.test(getComputedStyle(n).overflowY)));
    const target = scrollable.sort((a,b) => b.clientHeight - a.clientHeight)[0];
    if (target) target.scrollBy(0, target.clientHeight * .7);
  }
  const markers = Array.from(document.querySelectorAll('[data-e2e]')).map(n => n.getAttribute('data-e2e')).filter(s => /comment/i.test(s));
  const structures = Array.from(document.querySelectorAll('[class*="comment"]')).filter(visible).slice(0,20).map(n => [n.tagName,n.className,Array.from(n.children).slice(0,8).map(c=>c.tagName+':'+c.className).join('|')].join(' ').slice(0,500));
  if (roots[0]) structures.push(...Array.from(roots[0].querySelectorAll('*')).slice(0,65).map(n=>[n.tagName,n.className,n.getAttribute('data-e2e')||'',n.children.length===0 ? 'text-length:'+text(n).length : 'children:'+n.children.length].join(' ').slice(0,250)));
  return {items: output.slice(0, 160), empty, challenge, markers:[...new Set(markers),...structures].slice(0,100)};
})()
