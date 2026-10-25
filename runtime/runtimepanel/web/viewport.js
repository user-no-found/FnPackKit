// Visible geometry and input capabilities are independent of the fnOS host.
export function visibleViewport() {
  const visual = window.visualViewport;
  const left = visual?.offsetLeft || 0;
  const top = visual?.offsetTop || 0;
  const width = visual?.width || innerWidth;
  let height = visual?.height || innerHeight;
  const keyboard = navigator.virtualKeyboard?.boundingRect;
  if (keyboard?.height > 0 && keyboard.top >= top && keyboard.top < top + height) {
    height = keyboard.top - top;
  }
  const coarse = matchMedia('(pointer: coarse)').matches;
  return { left, top, width, height, coarse,
    touch: navigator.maxTouchPoints > 0,
    compact: width <= 700 || coarse && width <= 1100 };
}

export function observeViewport(update) {
  let pending = false;
  const refresh = () => {
    if (pending) return;
    pending = true;
    requestAnimationFrame(() => { pending = false; update(visibleViewport()); });
  };
  addEventListener('resize', refresh);
  window.visualViewport?.addEventListener('resize', refresh);
  window.visualViewport?.addEventListener('scroll', refresh);
  navigator.virtualKeyboard?.addEventListener('geometrychange', refresh);
  matchMedia('(pointer: coarse)').addEventListener('change', refresh);
  update(visibleViewport());
  return refresh;
}
