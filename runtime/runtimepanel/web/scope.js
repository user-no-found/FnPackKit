export function scopeNotice(scope) {
  return scope==='shared'
    ?'停止会影响本应用的共享后端及所有正在使用的会话，保留应用数据。'
    :'停止仅影响当前账号的后端，保留应用数据。';
}

export function scopeConfirmation(scope, action) {
  return (action==='restart'?'重启':'停止')+(scope==='shared'
    ?'共享后端会结束正在运行的任务，影响所有正在使用该会话的人。应用数据会保留。是否继续？'
    :'后端会结束当前账号正在运行的终端、AI 或其他后台任务。应用数据会保留。是否继续？');
}
