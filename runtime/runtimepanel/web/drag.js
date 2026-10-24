// Pointer geometry and local position are presentation state, never runtime authority.
export function draggable(button, panel, key) {
  const size = 44;
  let position = { x: 1, y: 0.38, edge: 'right' };
  try {
    const saved = JSON.parse(localStorage.getItem(key));
    if (saved && Number.isFinite(saved.x) && Number.isFinite(saved.y) && ['left','right','top','bottom',''].includes(saved.edge)) {
      position = { x: Math.max(0, Math.min(1, saved.x)), y: Math.max(0, Math.min(1, saved.y)), edge: saved.edge };
    }
  } catch {}
  let gesture = null;
  let suppressClick = false;
  const bounds = () => ({ x: Math.max(0, innerWidth-size), y: Math.max(0, innerHeight-size) });
  function place() {
    const max = bounds();
    const x = position.edge === 'left' ? 0 : position.edge === 'right' ? max.x : position.x*max.x;
    const y = position.edge === 'top' ? 0 : position.edge === 'bottom' ? max.y : position.y*max.y;
    button.style.left = x+'px';
    button.style.top = y+'px';
    button.dataset.edge = position.edge;
    panel.style.left = Math.max(8, Math.min(x, innerWidth-Math.min(320, innerWidth-16)-8))+'px';
    panel.style.top = Math.max(8, Math.min(y+size+8, innerHeight-panel.offsetHeight-8))+'px';
  }
  function save() { try { localStorage.setItem(key, JSON.stringify(position)); } catch {} }
  button.addEventListener('pointerdown', e => {
    if (e.button !== 0) return;
    const rect = button.getBoundingClientRect();
    gesture = { id: e.pointerId, x: e.clientX, y: e.clientY, dx: e.clientX-rect.left, dy: e.clientY-rect.top, moved: false };
    button.setPointerCapture(e.pointerId);
  });
  button.addEventListener('pointermove', e => {
    if (!gesture || gesture.id !== e.pointerId) return;
    if (!gesture.moved && Math.hypot(e.clientX-gesture.x,e.clientY-gesture.y)<6) return;
    gesture.moved = true;
    const max = bounds();
    const x = Math.max(0,Math.min(max.x,e.clientX-gesture.dx));
    const y = Math.max(0,Math.min(max.y,e.clientY-gesture.dy));
    position = { x: max.x ? x/max.x : 0, y: max.y ? y/max.y : 0, edge: '' };
    button.dataset.dragging = 'true';
    place();
  });
  function release(e) {
    if (!gesture || gesture.id !== e.pointerId) return;
    if (gesture.moved) {
      const max = bounds();
      const distances = [['left',position.x*max.x],['right',(1-position.x)*max.x],['top',position.y*max.y],['bottom',(1-position.y)*max.y]].sort((a,b)=>a[1]-b[1]);
      position.edge = distances[0][1] <= 28 ? distances[0][0] : '';
      suppressClick = true;
      save();
    }
    gesture = null;
    delete button.dataset.dragging;
    if (button.hasPointerCapture(e.pointerId)) button.releasePointerCapture(e.pointerId);
    place();
  }
  button.addEventListener('pointerup',release);
  button.addEventListener('pointercancel',release);
  button.addEventListener('click',e=>{
    if (suppressClick) { suppressClick=false; e.preventDefault(); e.stopImmediatePropagation(); }
  },true);
  addEventListener('resize',place);
  place();
  return { place };
}
