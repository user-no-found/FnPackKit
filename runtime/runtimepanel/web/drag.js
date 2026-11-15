import { launcherPoint, relativePosition, nearestEdge, storedPosition } from './geometry.js';

// Pointer ownership is local to this launcher; a second finger cannot replace a gesture.
export function draggable(button, key, viewport, { activity, dragging, placePanel,
  now = () => Date.now() }) {
  let position = { x: 1, y: 0.38, edge: 'right' };
  try { position = storedPosition(JSON.parse(localStorage.getItem(key))) || position; } catch {}
  let gesture = null;
  let suppressed = null;

  function place() {
    const point = launcherPoint(position, viewport());
    button.style.left = point.x + 'px';
    button.style.top = point.y + 'px';
    button.dataset.edge = position.edge;
    placePanel();
  }
  function save() { try { localStorage.setItem(key, JSON.stringify(position)); } catch {} }
  function snap() {
    position = nearestEdge(position, viewport());
    save();
    place();
  }
  function finish(event, cancelled = false) {
    if (!gesture || gesture.id !== event.pointerId) return;
    const owner = gesture;
    if (!cancelled && event.isTrusted) activity();
    if (gesture !== owner) return;
    gesture = null;
    delete button.dataset.dragging;
    if (owner.moved || cancelled) suppressed = { id: owner.id, until: now() + 600 };
    if (owner.moved) {
      snap();
      if (!cancelled && event.isTrusted) dragging();
    }
    try { if (button.hasPointerCapture(owner.id)) button.releasePointerCapture(owner.id); } catch {}
    place();
  }
  button.addEventListener('pointerdown', event => {
    if (event.button !== 0 || event.isPrimary === false || gesture) return;
    suppressed = null;
    if (event.isTrusted) activity();
    // The logical full-button origin avoids the half-hidden CSS transform offset.
    gesture = { id: event.pointerId, x: event.clientX, y: event.clientY,
      origin: launcherPoint(position, viewport()), moved: false };
    try { button.setPointerCapture(event.pointerId); } catch {}
  });
  button.addEventListener('pointermove', event => {
    if (!gesture || gesture.id !== event.pointerId) return;
    const owner = gesture;
    if (event.isTrusted) activity();
    if (gesture !== owner) return;
    const dx = event.clientX - gesture.x, dy = event.clientY - gesture.y;
    if (!gesture.moved && Math.hypot(dx, dy) < 6) return;
    if (!gesture.moved) {
      gesture.moved = true;
      button.dataset.dragging = 'true';
      if (event.isTrusted) dragging();
    }
    position = relativePosition({ x: gesture.origin.x + dx, y: gesture.origin.y + dy }, viewport());
    place();
  });
  button.addEventListener('pointerup', event => finish(event));
  button.addEventListener('pointercancel', event => finish(event, true));
  button.addEventListener('lostpointercapture', event => finish(event, true));
  button.addEventListener('click', event => {
    if (!suppressed || now() > suppressed.until || event.detail === 0) return;
    if (event.pointerId !== undefined && event.pointerId !== suppressed.id) return;
    suppressed = null;
    event.preventDefault();
    event.stopImmediatePropagation();
  }, true);
  return { place, settle() {
    if (gesture) finish({ pointerId: gesture.id }, true);
    snap();
  } };
}
