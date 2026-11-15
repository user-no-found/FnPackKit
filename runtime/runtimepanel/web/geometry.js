// Viewport-relative launcher preferences contain no business state.
export const launcherSize = 44;
const clamp = (value, min, max) => Math.max(min, Math.min(value, Math.max(min, max)));
const edges = ['left', 'right', 'top', 'bottom'];

export function launcherPoint(position, view) {
  const width = Math.max(0, view.width - launcherSize);
  const height = Math.max(0, view.height - launcherSize);
  return {
    x: view.left + (position.edge === 'left' ? 0 : position.edge === 'right' ? width : position.x * width),
    y: view.top + (position.edge === 'top' ? 0 : position.edge === 'bottom' ? height : position.y * height),
  };
}

export function relativePosition(point, view) {
  const width = Math.max(0, view.width - launcherSize);
  const height = Math.max(0, view.height - launcherSize);
  return { x: width ? clamp((point.x - view.left) / width, 0, 1) : 0,
    y: height ? clamp((point.y - view.top) / height, 0, 1) : 0, edge: '' };
}

export function nearestEdge(position, view) {
  const point = launcherPoint(position, view);
  const distances = [point.x - view.left, view.left + view.width - launcherSize - point.x,
    point.y - view.top, view.top + view.height - launcherSize - point.y];
  return { ...relativePosition(point, view), edge: edges[distances.indexOf(Math.min(...distances))] };
}

export function storedPosition(saved) {
  if (!saved || !Number.isFinite(saved.x) || !Number.isFinite(saved.y) ||
      ![...edges, ''].includes(saved.edge)) return null;
  return { x: clamp(saved.x, 0, 1), y: clamp(saved.y, 0, 1), edge: saved.edge };
}

export function floatingPosition(anchor, width, height, view) {
  const gap = 8;
  const left = view.left + gap, top = view.top + gap;
  const right = view.left + view.width - gap, bottom = view.top + view.height - gap;
  const candidates = [
    { x: anchor.right + gap, y: anchor.top, fits: right - anchor.right - gap >= width },
    { x: anchor.left - gap - width, y: anchor.top, fits: anchor.left - gap - left >= width },
    { x: anchor.left, y: anchor.bottom + gap, fits: bottom - anchor.bottom - gap >= height },
    { x: anchor.left, y: anchor.top - gap - height, fits: anchor.top - gap - top >= height },
  ];
  const point = candidates.find(item => item.fits) || candidates[2];
  return { x: clamp(point.x, left, right - width), y: clamp(point.y, top, bottom - height) };
}
