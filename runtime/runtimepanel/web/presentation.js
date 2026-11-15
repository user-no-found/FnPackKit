import { draggable } from './drag.js';
import { floatingPosition } from './geometry.js';
import { observeViewport, visibleViewport } from './viewport.js';

function find(selector) {
  if (typeof selector !== 'string' || !selector) return null;
  try { return document.querySelector(selector); } catch { return null; }
}
function present(node) {
  if (!node?.isConnected || !node.getClientRects().length) return false;
  const style = getComputedStyle(node);
  return style.visibility !== 'hidden' && style.display !== 'none';
}
function visible(node) {
  if (!present(node)) return false;
  const view = visibleViewport();
  const clip = { left:view.left, top:view.top, right:view.left+view.width, bottom:view.top+view.height };
  for (let parent=node.parentElement; parent; parent=parent.parentElement) {
    const style=getComputedStyle(parent), bounds=parent.getBoundingClientRect();
    if (style.overflowX !== 'visible') {
      clip.left=Math.max(clip.left,bounds.left); clip.right=Math.min(clip.right,bounds.right);
    }
    if (style.overflowY !== 'visible') {
      clip.top=Math.max(clip.top,bounds.top); clip.bottom=Math.min(clip.bottom,bounds.bottom);
    }
  }
  const rect=node.getBoundingClientRect();
  return rect.left>=clip.left-1 && rect.right<=clip.right+1 && rect.top>=clip.top-1 && rect.bottom<=clip.bottom+1;
}

// Applications declare their launcher/avoid region; both inputs use the same nonmodal float.
export function installPresentation(host, button, panel, config, interaction) {
  const layout = config.layout || {};
  const shadow = host.shadowRoot;
  const insets = document.createElement('div');
  insets.className = 'viewport-insets';
  insets.setAttribute('aria-hidden', 'true');
  shadow.append(insets);
  let viewport = visibleViewport();
  let available = viewport;
  let launcher = null;
  let observedAvoid = null;
  let observedLauncher = null;
  let scheduled = false;
  let keyboard = false;
  document.addEventListener('keydown', event => { if (event.isTrusted) keyboard = true; }, true);
  document.addEventListener('pointerdown', event => { if (event.isTrusted) keyboard = false; }, true);
  function editableFocused() {
    let node = document.activeElement;
    while (node?.shadowRoot?.activeElement) node = node.shadowRoot.activeElement;
    return node?.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(node?.tagName || '');
  }
  shadow.addEventListener('pointerdown', event => {
    if (event.target.closest?.('button') && editableFocused()) event.preventDefault();
  });
  function focusControl(node) {
    if (keyboard) node.focus({ preventScroll:true });
  }
  function preserveEditor(event) {
    if (editableFocused()) event.preventDefault();
  }
  const drag = draggable(button, 'fnpackkit-runtime-position:' + config.prefix,
    () => available, { activity: interaction.activity, dragging: interaction.drag, placePanel });
  const sizeObserver = new ResizeObserver(place);
  const launcherObserver = new MutationObserver(place);
  sizeObserver.observe(panel);
  panel.setAttribute('aria-modal', 'false');

  function place() {
    if (scheduled) return;
    scheduled = true;
    requestAnimationFrame(() => { scheduled = false; position(); });
  }
  function placePanel() {
    const anchor = visible(launcher) ? launcher : button;
    const point = floatingPosition(anchor.getBoundingClientRect(), panel.offsetWidth, panel.offsetHeight, available);
    panel.style.left = point.x + 'px';
    panel.style.top = point.y + 'px';
  }
  function position() {
    launcher = find(layout.launcher);
    if (launcher !== observedLauncher) {
      if (observedLauncher) {
        sizeObserver.unobserve(observedLauncher);
        observedLauncher.removeEventListener('pointerdown', preserveEditor);
      }
      launcherObserver.disconnect();
      observedLauncher = launcher;
      if (launcher) {
        launcher.addEventListener('pointerdown', preserveEditor);
        sizeObserver.observe(launcher);
        launcherObserver.observe(launcher, { attributes:true, attributeFilter:['hidden','style','class'] });
      }
    }
    if (launcher && 'disabled' in launcher && launcher.disabled) launcher.disabled = false;
    button.hidden = visible(launcher);
    const avoid = find(layout.avoid);
    if (avoid !== observedAvoid) {
      if (observedAvoid) sizeObserver.unobserve(observedAvoid);
      observedAvoid = avoid;
      if (avoid) sizeObserver.observe(avoid);
    }
    const safe = getComputedStyle(insets);
    const inset = side => parseFloat(safe[side]) || 0;
    const top = viewport.top + inset('paddingTop');
    const left = viewport.left + inset('paddingLeft');
    let bottom = viewport.top + viewport.height - inset('paddingBottom');
    const right = viewport.left + viewport.width - inset('paddingRight');
    if (present(avoid)) {
      const rect = avoid.getBoundingClientRect();
      if (rect.left < right && rect.right > left && rect.bottom >= bottom - 2 && rect.top < bottom)
        bottom = Math.max(top, rect.top);
    }
    available = { ...viewport, left, top, width:Math.max(0,right-left), height:Math.max(0,bottom-top) };
    host.dataset.compact = String(viewport.compact);
    panel.style.maxHeight = Math.max(0, available.height - 16) + 'px';
    panel.style.maxWidth = Math.max(0, available.width - 16) + 'px';
    panel.style.width = Math.max(0, Math.min(320, available.width - 16)) + 'px';
    drag.place();
  }
  function outside(event) {
    if (!event.isTrusted || panel.hidden) return;
    const path = event.composedPath();
    if (!path.includes(host) && !path.includes(launcher)) interaction.close('outside');
  }
  document.addEventListener('pointerdown', outside, true);
  document.addEventListener('click', outside, true);
  let ownsEscape = false;
  document.addEventListener('keydown', event => {
    if (event.key !== 'Escape') return;
    if (panel.hidden && (!ownsEscape || !event.repeat)) { ownsEscape = false; return; }
    const inside = event.composedPath().includes(host);
    ownsEscape = true;
    event.preventDefault(); event.stopPropagation();
    if (!panel.hidden) interaction.close('escape');
    if (inside) focusControl(visible(launcher) ? launcher : button);
  }, true);
  document.addEventListener('keyup', event => {
    if (event.key !== 'Escape' || !ownsEscape) return;
    ownsEscape = false;
    event.preventDefault(); event.stopPropagation();
  }, true);
  addEventListener('blur', () => { ownsEscape = false; });
  // A stationary hover or retained focus is not activity, nor is a programmatic focus callback.
  for (const name of ['pointerdown','pointerup','click','pointermove','keydown','input','wheel','touchmove']) {
    panel.addEventListener(name, event => {
      if (event.isTrusted && (name !== 'pointermove' || event.movementX || event.movementY)) interaction.activity();
    }, { passive: name === 'wheel' || name === 'touchmove' });
  }
  button.addEventListener('keydown', event => { if (event.isTrusted) interaction.activity(); });
  button.addEventListener('pointermove', event => {
    if (event.isTrusted && (event.movementX || event.movementY)) interaction.activity();
  });
  const observer = new MutationObserver(place);
  observer.observe(document.body, { childList:true, subtree:true, attributes:true,
    attributeFilter:['hidden','style','class'] });
  document.addEventListener('scroll', place, true);
  observeViewport(value => { viewport = value; place(); });
  return { place, settle: drag.settle, focusControl,
    focusLauncher() { focusControl(visible(launcher) ? launcher : button); },
  };
}
