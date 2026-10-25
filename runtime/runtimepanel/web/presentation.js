import { draggable } from './drag.js';
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
    const style=getComputedStyle(parent);
    const bounds=parent.getBoundingClientRect();
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

// Applications declare their launcher and bottom toolbar; no application DOM is guessed.
export function installPresentation(host, button, panel, config, close) {
  const layout = config.layout || {};
  const shadow = host.shadowRoot;
  const backdrop = document.createElement('div');
  backdrop.className = 'backdrop'; backdrop.hidden = true;
  backdrop.setAttribute('aria-hidden', 'true');
  shadow.insertBefore(backdrop, button);
  backdrop.addEventListener('click', close);
  let viewport = visibleViewport();
  let available = viewport;
  let observedAvoid = null;
  let observedLauncher = null;
  let scheduled = false;
  const drag = draggable(button, panel, 'fnpackkit-runtime-position:' + config.prefix,
    () => available, () => !viewport.compact);
  const sizeObserver = new ResizeObserver(place);
  const launcherObserver = new MutationObserver(place);
  sizeObserver.observe(panel);

  function place() {
    if (scheduled) return;
    scheduled = true;
    requestAnimationFrame(() => { scheduled = false; position(); });
  }

  function position() {
    const launcher = find(layout.launcher);
    if (launcher !== observedLauncher) {
      if (observedLauncher) sizeObserver.unobserve(observedLauncher);
      launcherObserver.disconnect();
      observedLauncher = launcher;
      if (launcher) {
        sizeObserver.observe(launcher);
        launcherObserver.observe(launcher, { attributes: true, attributeFilter: ['hidden','style','class'] });
      }
    }
    if (launcher && 'disabled' in launcher) launcher.disabled = false;
    button.hidden = visible(launcher);
    const avoid = find(layout.avoid);
    if (avoid !== observedAvoid) {
      if (observedAvoid) sizeObserver.unobserve(observedAvoid);
      observedAvoid = avoid;
      if (avoid) sizeObserver.observe(avoid);
    }
    let bottom = viewport.top + viewport.height;
    if (present(avoid)) {
      const rect = avoid.getBoundingClientRect();
      if (rect.left < viewport.left + viewport.width && rect.right > viewport.left &&
          rect.bottom >= bottom - 2 && rect.top < bottom) bottom = Math.max(viewport.top, rect.top);
    }
    available = { ...viewport, height: bottom - viewport.top };
    host.dataset.compact = String(viewport.compact);
    panel.setAttribute('aria-modal', String(viewport.compact));
    backdrop.hidden = panel.hidden || !viewport.compact;
    Object.assign(backdrop.style, { left: viewport.left+'px', top: viewport.top+'px',
      width: viewport.width+'px', height: viewport.height+'px' });
    panel.style.maxHeight = Math.max(0, available.height - 16)+'px';
    panel.style.maxWidth = Math.max(0, available.width - 16)+'px';
    panel.style.width = Math.max(0, Math.min(viewport.compact ? available.width - 16 : 320,
      available.width - 16))+'px';
    if (viewport.compact) {
      button.style.left = (available.left + 8)+'px';
      button.style.top = Math.max(available.top + 8, bottom - 52)+'px';
      panel.style.left = (available.left + 8)+'px';
      panel.style.top = Math.max(available.top + 8, bottom - panel.offsetHeight - 8)+'px';
    } else drag.place();
  }

  const observer = new MutationObserver(place);
  observer.observe(document.body, { childList: true, subtree: true });
  document.addEventListener('scroll', place, true);
  observeViewport(value => { viewport = value; place(); });
  return {
    place,
    focusLauncher() {
      const launcher = find(layout.launcher);
      (visible(launcher) ? launcher : button).focus({ preventScroll: true });
    },
  };
}
