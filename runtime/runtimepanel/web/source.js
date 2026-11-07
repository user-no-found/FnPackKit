function version(value) {
  if (!value || typeof value.commit !== 'string' || !/^[a-f0-9]{40,64}$/.test(value.commit)) return '未知';
  const local = value.dirty ? ' · 有本地修改' : '';
  const fingerprint = value.dirty && typeof value.fingerprint === 'string'
    ? '（'+value.fingerprint.slice(0,8)+'）' : '';
  return value.commit.slice(0,12)+local+fingerprint;
}

export function renderSource(container, source) {
  container.replaceChildren();
  container.hidden = !source;
  if (!source) return;
  const line = text => {
    const node = document.createElement('p');
    node.textContent = text;
    container.append(node);
  };
  line('本地代码：'+(source.workspace ? version(source.workspace) : '暂不可读'));
  line('运行版本：'+(source.running ? version(source.running) : '后端未运行'));
  if (source.unavailable) line('源码状态暂不可读，请稍后刷新。');
  else if (source.changed) line('本地代码已变化，需构建并重启。');
}
