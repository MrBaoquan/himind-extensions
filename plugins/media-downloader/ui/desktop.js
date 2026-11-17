(function () {
  'use strict';

  const DRAFT_KEY = 'himind.media-downloader.draft.v1';
  const settingsIds = ['setting-dest', 'setting-quality', 'setting-max-bytes', 'setting-max-items', 'setting-theme', 'setting-page-size', 'setting-poll', 'setting-density'];
  const composerIds = ['url', 'playlist', 'dest-dir', 'quality', 'max-bytes', 'max-items', 'audio-only'];
  const controls = new Map();
  let baseline = {};
  let draft = {};
  let activeSelect = null;
  let popup = null;
  let tooltip = null;
  let tooltipTarget = null;
  let initialized = false;
  let storageFailed = false;
  const byId = id => document.getElementById(id);
  const readFields = ids => Object.fromEntries(ids.filter(id => byId(id)).map(id => [id, byId(id).type === 'checkbox' ? byId(id).checked : byId(id).value]));
  const equal = (a, b) => JSON.stringify(a) === JSON.stringify(b);

  function writeDraft() {
    try {
      if (draft.settings || draft.composer) localStorage.setItem(DRAFT_KEY, JSON.stringify(draft));
      else localStorage.removeItem(DRAFT_KEY);
      storageFailed = false;
    } catch (_) { storageFailed = true; }
    showDraftState();
  }

  function showDraftState() {
    if (byId('settings-draft-state')) {
      byId('settings-draft-state').textContent = draft.settings
        ? storageFailed ? '有未保存的设置 · 草稿暂存失败，请保存后关闭' : '有未保存的设置 · 已暂存草稿，重开可恢复'
        : '设置已保存';
    }
    if (byId('settings-discard')) byId('settings-discard').hidden = !draft.settings;
  }

  function captureDraft(event) {
    if (settingsIds.includes(event.target.id)) {
      const values = readFields(settingsIds);
      draft.settings = equal(values, baseline) ? null : values;
    } else if (composerIds.includes(event.target.id)) {
      const values = readFields(composerIds);
      draft.composer = values.url && values.url.trim() ? values : null;
    } else return;
    writeDraft();
  }

  function applyFields(values, ids) {
    if (!values || typeof values !== 'object') return;
    ids.forEach(id => {
      const node = byId(id);
      if (!node || !Object.prototype.hasOwnProperty.call(values, id)) return;
      if (node.type === 'checkbox') node.checked = values[id] === true;
      else if (typeof values[id] === 'string' && values[id].length <= 4000) node.value = values[id];
    });
  }

  function restoreDraft() {
    applyFields(draft.settings, settingsIds);
    applyFields(draft.composer, composerIds);
    if (draft.composer && byId('composer-badge')) {
      byId('composer-badge').hidden = false;
      byId('composer-badge').textContent = '草稿已恢复 · 请重新解析';
    }
    showDraftState();
    syncControls();
    return { settings: !!draft.settings, composer: !!draft.composer };
  }

  function settingsSaved() {
    baseline = readFields(settingsIds);
    draft.settings = null;
    writeDraft();
  }

  function closeSelect(restoreFocus) {
    const previous = activeSelect;
    activeSelect = null;
    if (popup) popup.hidden = true;
    if (previous) {
      const button = controls.get(previous);
      if (button) {
        button.setAttribute('aria-expanded', 'false');
        if (restoreFocus && button.isConnected && !button.disabled) button.focus({ preventScroll: true });
      }
    }
  }

  function placeFloating(node, anchor, minWidth) {
    const box = anchor.getBoundingClientRect();
    const width = Math.min(Math.max(box.width, minWidth), Math.max(0, innerWidth - 16));
    node.style.width = width + 'px';
    node.style.left = Math.max(8, Math.min(box.left, innerWidth - width - 8)) + 'px';
    const below = innerHeight - box.bottom - 12;
    const above = box.top - 12;
    const useAbove = below < 180 && above > below;
    node.style.maxHeight = Math.max(32, Math.min(320, useAbove ? above : below)) + 'px';
    node.style.top = (useAbove ? Math.max(8, box.top - node.offsetHeight - 4) : box.bottom + 4) + 'px';
  }

  function selectLabel(select) {
    const label = select.closest('label');
    const caption = label && label.querySelector('span');
    return select.getAttribute('aria-label') || (caption && caption.textContent.trim()) || '选择';
  }

  function syncControls() {
    controls.forEach((button, select) => {
      const selected = select.selectedOptions[0];
      const label = selected ? selected.textContent : '请选择';
      if (button.textContent !== label) button.textContent = label;
      button.disabled = select.disabled;
      button.setAttribute('aria-label', selectLabel(select) + '：' + label);
      if (button.hidden !== select.hidden) button.hidden = select.hidden;
      if (activeSelect === select && (select.disabled || !button.offsetParent)) closeSelect(false);
    });
  }

  function openSelect(select, last) {
    closeSelect(false);
    hideTooltip();
    syncControls();
    const button = controls.get(select);
    if (!button || button.disabled) return;
    activeSelect = select;
    button.setAttribute('aria-expanded', 'true');
    popup.replaceChildren();
    Array.from(select.options).forEach(option => {
      if (option.hidden) return;
      const item = document.createElement('button');
      item.type = 'button';
      item.className = 'desktop-option' + (option.selected ? ' selected' : '');
      item.textContent = option.textContent;
      item.disabled = option.disabled;
      item.setAttribute('role', 'option');
      item.setAttribute('aria-selected', String(option.selected));
      item.addEventListener('click', () => {
        select.value = option.value;
        select.dispatchEvent(new Event('change', { bubbles: true }));
        syncControls();
        closeSelect(true);
      });
      popup.appendChild(item);
    });
    popup.hidden = false;
    placeFloating(popup, button, 160);
    const items = Array.from(popup.querySelectorAll('button:not(:disabled)'));
    const selected = popup.querySelector('.selected:not(:disabled)');
    const target = last ? items[items.length - 1] : selected || items[0];
    if (target) { target.focus({ preventScroll: true }); target.scrollIntoView({ block: 'nearest' }); }
  }

  function enhanceSelect(select) {
    if (controls.has(select)) return;
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'desktop-select';
    button.id = select.id + '-control';
    button.setAttribute('aria-haspopup', 'listbox');
    button.setAttribute('aria-expanded', 'false');
    button.setAttribute('aria-controls', 'desktop-select-popup');
    button.addEventListener('click', () => activeSelect === select ? closeSelect(true) : openSelect(select));
    button.addEventListener('keydown', event => {
      if (event.isComposing) return;
      if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
        event.preventDefault();
        event.stopPropagation();
        openSelect(select, event.key === 'End' || event.key === 'ArrowUp');
      }
    });
    select.after(button);
    controls.set(select, button);
    // Keep the source select as a functional fallback if this module fails to load.
    select.classList.add('desktop-select-source');
    select.tabIndex = -1;
    select.addEventListener('change', syncControls);
    const label = select.closest('label');
    if (label) label.addEventListener('click', event => {
      if (event.target === label || event.target.tagName === 'SPAN') { event.preventDefault(); button.focus(); }
    });
  }

  function showTooltip(target) {
    const text = target.dataset.tooltip || target.getAttribute('aria-label');
    if (!text || !target.offsetParent) return;
    tooltipTarget = target;
    tooltip.textContent = text;
    tooltip.hidden = false;
    placeFloating(tooltip, target, Math.min(320, Math.max(120, text.length * 7)));
  }

  function hideTooltip() {
    tooltipTarget = null;
    if (tooltip) tooltip.hidden = true;
  }

  function enhanceTooltips() {
    document.querySelectorAll('[title]').forEach(node => {
      const title = node.getAttribute('title');
      if (title) node.dataset.tooltip = title;
      node.removeAttribute('title');
    });
  }

  function init() {
    if (initialized) return;
    initialized = true;
    baseline = readFields(settingsIds);
    try {
      const saved = JSON.parse(localStorage.getItem(DRAFT_KEY) || '{}');
      if (saved && typeof saved === 'object') draft = saved;
    } catch (_) { draft = {}; }
    popup = document.createElement('div');
    popup.id = 'desktop-select-popup';
    popup.className = 'desktop-select-popup';
    popup.setAttribute('role', 'listbox');
    popup.hidden = true;
    document.body.appendChild(popup);
    tooltip = document.createElement('div');
    tooltip.id = 'desktop-tooltip';
    tooltip.className = 'desktop-tooltip';
    tooltip.hidden = true;
    document.body.appendChild(tooltip);
    document.querySelectorAll('select').forEach(enhanceSelect);
    enhanceTooltips();
    syncControls();
    const observer = new MutationObserver(records => {
      if (records.some(record => !popup.contains(record.target) && record.target !== tooltip)) {
        enhanceTooltips();
        syncControls();
      }
    });
    observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['title', 'disabled', 'hidden'] });
    // Values can also change through the controller without a DOM mutation.
    document.addEventListener('click', () => requestAnimationFrame(syncControls));
    document.addEventListener('input', captureDraft);
    document.addEventListener('change', captureDraft);
    document.addEventListener('pointerdown', event => {
      hideTooltip();
      if (activeSelect && !popup.contains(event.target) && event.target !== controls.get(activeSelect)) closeSelect(false);
    });
    document.addEventListener('keydown', event => {
      hideTooltip();
      if (event.isComposing || !activeSelect) return;
      if (event.key === 'Escape') {
        event.preventDefault(); event.stopImmediatePropagation(); closeSelect(true);
      } else if (event.key === 'Tab') {
        // Return to the trigger, then let the browser move to the next field.
        closeSelect(true);
      } else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
        event.preventDefault(); event.stopImmediatePropagation();
        const items = Array.from(popup.querySelectorAll('button:not(:disabled)'));
        const index = items.indexOf(document.activeElement);
        const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1
          : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
        if (items[next]) items[next].focus();
      } else if (event.key.length === 1 && !event.ctrlKey && !event.metaKey) {
        const item = Array.from(popup.querySelectorAll('button:not(:disabled)')).find(node => node.textContent.toLowerCase().startsWith(event.key.toLowerCase()));
        if (item) { event.preventDefault(); item.focus(); }
      }
    }, true);
    document.addEventListener('focusin', event => {
      const target = event.target.closest('[data-tooltip],button[aria-label]');
      if (target) showTooltip(target);
    });
    document.addEventListener('focusout', hideTooltip);
    document.addEventListener('pointerover', event => {
      const target = event.target.closest('[data-tooltip],button[aria-label]');
      if (target) showTooltip(target);
    });
    document.addEventListener('pointerout', event => { if (tooltipTarget && !tooltipTarget.contains(event.relatedTarget)) hideTooltip(); });
    document.addEventListener('scroll', event => {
      hideTooltip();
      if (popup && !popup.contains(event.target)) closeSelect(false);
    }, true);
    window.addEventListener('resize', () => { hideTooltip(); closeSelect(false); });
    window.addEventListener('blur', () => { hideTooltip(); closeSelect(false); });
    if (byId('settings-discard')) byId('settings-discard').addEventListener('click', () => {
      applyFields(baseline, settingsIds);
      draft.settings = null;
      writeDraft(); syncControls();
    });
    showDraftState();
  }

  window.MediaDesktop = { init, restoreDraft, settingsSaved, syncControls, hasSettingsDraft: () => !!draft.settings,
    clearComposerDraft() { draft.composer = null; writeDraft(); } };
})();
