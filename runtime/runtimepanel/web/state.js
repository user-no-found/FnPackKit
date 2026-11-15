// Only deliberate interaction extends deadlines; rendering and lifecycle checks never do.
export function createInteraction({ onChange, onClose, now = () => Date.now(),
  schedule = (run, delay) => setTimeout(run, delay), cancel = id => clearTimeout(id) }) {
  let value = 'revealed';
  let delay = 5000;
  let deadline = now() + delay;
  let timer = null;
  let generation = 0;

  function invalidate() {
    generation++;
    if (timer !== null) cancel(timer);
    timer = null;
  }
  function publish(next) {
    value = next;
    onChange(next);
  }
  function close(reason = 'close') {
    invalidate();
    deadline = null;
    // The panel owner clears one-shot authorization, never the accepted server request.
    onClose(reason);
    publish('half');
  }
  function check() {
    if (deadline !== null && now() >= deadline) close('idle');
    return value;
  }
  function arm() {
    invalidate();
    const owner = generation;
    timer = schedule(() => {
      if (owner !== generation || deadline === null) return;
      timer = null;
      if (now() < deadline) arm();
      else close('idle');
    }, Math.max(0, deadline - now()));
  }
  function enter(next, timeout) {
    delay = timeout;
    deadline = now() + timeout;
    publish(next);
    arm();
  }
  function activity() {
    check();
    if (value === 'half') return;
    deadline = now() + delay;
    arm();
  }
  function open() {
    check();
    enter('open', 5000);
  }
  function activate() {
    check();
    if (value === 'half') enter('revealed', 3000);
    else if (value === 'revealed') enter('open', 5000);
    else close();
  }
  function drag() {
    check();
    enter(value === 'open' ? 'open' : 'revealed', 5000);
  }
  publish(value);
  arm();
  return { get value() { return value; }, get deadline() { return deadline; },
    activate, open, close, activity, drag, check };
}
