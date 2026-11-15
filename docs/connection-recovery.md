# 前台连接恢复

`web/connection-recovery.js` 提供 `FnPackKitConnectionRecovery.create(options)`，协调网页从后台、锁屏或冻结状态恢复后的业务连接检查与状态补齐。它不拥有 WebSocket/SSE 协议，不替代应用的认证、连接管理或订阅实现。

该脚本是显式选用的独立资源：应用构建从所选完整 checkout/sdist 复制并记录摘要，在自己的入口加载后注册回调。仅加载脚本不会开始恢复；`create()` 安装监听和调度间隔检测，但不执行初始业务请求。现有网关、面板和 transport 不会自动加载它，未注册的消费者行为不变。修改共享源码不改变已安装 FPK，消费者接入并重新构建升级后才生效。

## 应用接口

```javascript
const recovery = FnPackKitConnectionRecovery.create({
    // 可选：只读地验证“当前业务通道”是否仍能往返。
    check: async ({ signal }) => connection.probe({ signal }),
    // 必须在新通道真正就绪后完成，而不是只发起连接。
    reconnect: async ({ signal }) => connection.replace({ signal }),
    // 每次恢复都会执行，包括 check=true；补快照/订阅，不重放写操作。
    resync: async ({ signal }) => subscriptions.restore({ signal }),
    onState: snapshot => renderConnectionState(snapshot),
    timing: publicRuntimeConfiguration.connectionRecovery
});

// 用户明确要求重试时调用；返回 void，结果由 state/onState 观察。
recovery.recover();
// 页面所属实例卸载时调用；不会关闭应用的业务连接。
recovery.close();
```

`connection`、`subscriptions`、渲染函数和配置是示意的应用接口，不是 FnPackKit 提供的对象。应用继续拥有唯一运行配置加载器，只把需要公开的恢复参数传入，不建立另一份浏览器配置或传递密钥。

| 参数 | 契约 |
| --- | --- |
| `reconnect(context)` | 必需函数；替换失效连接，完成握手、认证和通道就绪后 settle。 |
| `resync(context)` | 必需函数；幂等地恢复订阅或拉取快照，完成状态补齐后 settle。 |
| `check(context)` | 可选函数；返回严格 boolean。true 跳过 reconnect，但仍执行 resync；false 或普通异常进入 reconnect。非法返回值使该次尝试失败，不作为健康或不健康隐式转换。省略时每次恢复都重建。 |
| `onState(snapshot)` | 可选展示观察者；同步异常和异步 rejection 被隔离，不决定业务结果。组件不输出另一个日志流；应用通过既有日志机制记录必要诊断。 |
| `timing` | 可选参数对象；见下表。未知选项、未知 timing 键及非法值在 create 时立即报错。 |

每次尝试的所有业务回调收到同一个冻结的 `{signal, reason, attempt}`；重试创建新的 context 和 signal。attempt 从 1 开始，reason 为 manual、visible、pageshow、focus、online 或 timer-gap，不能把 attempt 数字单独当成跨轮次的所有权标识。回调返回普通值或 Promise 均可；reconnect/resync 的成功以正常完成为准，不使用返回值宣称业务操作结果。

**健康含义不能简化成 HTTP 200、`navigator.onLine` 或 WebSocket 的 OPEN。**它们不证明原业务通道正在投递事件。check 应使用该通道已有的只读请求/确认机制；不能为了探活发送任意协议帧或业务命令。正常业务可以长期没有消息，组件不把消息静默当成断线，也不从 `/_gateway/status` 的 ready 推断 WS 健康。

## 恢复与生命周期

一次恢复按 `check → 必要时 reconnect → resync` 顺序执行。只有全部完成后才发布 ready。check=true 不会让协调器略过 resync；应用若要避免健康通道上的冗余全量读取，须在自己的 resync 中验证本次 signal 对应的检查证明及当前订阅/状态完整性，失效时不能伪报补齐完成。

- visibilitychange 观察到隐藏时记录待验证需求并取消当前尝试；每次真正回到前台都先执行应用 check，短暂隐藏或后台定时器继续运行也不例外。隐藏不是断线证明，健康时不执行 reconnect；未提供 check 时保守重建。不会仅凭重新可见就发布 ready。
- pagehide 取消，pageshow 在页面曾中断或 `persisted=true` 时恢复；普通首次 pageshow 不发起额外请求。
- offline 暂停；online 触发恢复，但页面仍隐藏时不调用业务回调。
- focus 仅在已经记录中断或检测到调度长间隔时兜底；普通窗口聚焦及输入框 focus 不会无条件重连。
- 定时检查墙上时间的调度间隔，以处理部分宿主未派发生命周期事件的暂停。系统时钟向前跳变也可能触发一次检查；它不是业务心跳或网络成功证明。
- 调度间隔只是额外的恢复线索，不区分桌面/移动端，也不代表通道失效。后台计时器一直运行时原业务通道仍可能停止下行，因此前台返回不能只依赖 timer-gap；健康的短/长后台返回均通过 check 保留原连接。
- 同一恢复期间的 visible/pageshow/focus/online 等事件合并，不开启第二条流水线。新的隐藏/离线中断会取消旧代，前台恢复在旧回调退出后执行。

手动 `recover()` 使用同样的去抖与单执行约束，隐藏/离线时只记录恢复需求；当前有效恢复中的重复请求合并。超时已可见、但旧回调仍在排空时，只需请求一次重试：组件记录请求，等排空后开启新预算，不要求再点击一次，也不并发执行。耗尽尝试后不会无限循环，下一次明确恢复事件或手动请求开启新的尝试预算。中断后恢复也使用新的预算。

回调只能恢复客户端通道和读取权威状态，不得自动发送用户消息、重放支付/保存等写操作，或把用户手动停止的后端重新唤醒。服务端智能体和业务任务不因组件恢复而重启。应用仍负责权限、实际进程和数据策略。

## 超时、取消与资源清理

所有阶段共享一次尝试的 deadline。超时会中止 signal 并及时发布 `failed/timeout`。普通失败按有限退避重试；超时后的重试也必须等旧回调真正 settle。

**回调必须遵守 AbortSignal：**收到取消后停止请求/订阅操作，完成必要清理，并且不再提交旧代状态。协调器在每个阶段前后检查所有权，取消后不继续下一阶段或发布迟到的 ready；它不能撤销应用回调内部已经执行的副作用。

若回调忽略取消且 Promise 不完成，组件继续持有执行占用，不会同时启动新的 reconnect/resync。此时超时可见，但恢复被该回调阻塞；`close()` 也不能强制终止任意应用代码。不要用不带取消的永久 Promise 作为回调，也不要在 Promise settle 后继续执行未被跟踪的连接操作。

`close()` 幂等：移除本实例的监听器、清理调度/退避/deadline 计时器并取消当前 signal，之后的 recover 和迟到结果不再生效。它不替代应用自身的业务连接关闭/订阅释放。

## 参数

全部时间单位为毫秒；参数在 create 时复制校验，变更后需关闭原实例并重新注册。部署值由主应用现有可编辑配置提供。

| timing 键 | 默认值 | 含义 |
| --- | ---: | --- |
| `debounceMs` | 100 | 合并恢复事件的等待时间；允许 0。 |
| `attemptTimeoutMs` | 15000 | 一次完整 check/reconnect/resync 的 deadline。 |
| `retryBaseMs` | 500 | 第一次重试等待时间，之后翻倍。 |
| `retryMaxMs` | 10000 | 重试等待上限，不小于 retryBaseMs。 |
| `maxAttempts` | 3 | 每轮总尝试次数，含第一次；整数 1..100。 |
| `watchIntervalMs` | 1000 | 调度间隔检查频率。 |
| `suspendGapMs` | 5000 | 判定调度中断的间隔，必须大于 watchIntervalMs。 |

除 debounceMs 外，时间为正整数；时间均不超过浏览器计时器上限 2147483647。

## 状态

`recovery.state` 返回冻结快照 `{state, reason, attempt, code}`，不包含连接地址、异常正文、请求体或凭据。初始值为 `{state:'idle', reason:null, attempt:0, code:null}`；观察者不在 create 时回调。

| state | 含义 |
| --- | --- |
| idle | 已注册，尚未开始恢复。 |
| waiting | 页面隐藏或离线，等待恢复条件。reason 为 hidden/offline。 |
| checking / reconnecting / resyncing | 对应业务回调正在执行。 |
| retrying | 上次尝试失败，等待退避。 |
| ready | 当前恢复及状态补齐完成；不是永久在线保证。 |
| failed | 尝试耗尽或 deadline 已触发。timeout 时旧回调可能仍在取消清理，完成后如有预算可转为 retrying。 |
| closed | 协调器已关闭。 |

失败 code 为 check_failed、reconnect_failed、resync_failed、timeout 或 recovery_failed；展示文案由应用提供。onState 应只做展示/诊断，不在观察者中循环调用 recover。

## 验证范围

Node 回归覆盖短/长后台返回、后台定时器持续运行与节流、健康检查后仍执行 resync、单执行、有限重试、超时取消、观察者同步隐藏/关闭和资源清理。独立浏览器夹具以真实 WebSocket 的只读确认验证健康返回不换 socket；在计时器仍运行但下行停止时，检查失败才恢复连接与快照，同时验证不刷新页面、保留草稿及不重放写操作。

```sh
node --test tests/connection_recovery*.test.mjs
PYTHONPATH=/path/to/FnPackKit/src:/path/to/BuildFlow/src \
  FNPACKKIT_BROWSER_E2E=1 python3 -m unittest discover -s tests \
  -p test_connection_recovery_browser.py -v
```

浏览器用例通过 BuildFlow 的用户级登记选择已有 Node、Playwright 与 Chromium，不自动下载依赖。

合成 pageshow/visibilitychange、手机视口或桌面浏览器断流，不等于 Android 飞牛 App 的真实后台冻结。消费者接入后仍须在局域网与 FN Connect 分别验证切换 App、锁屏恢复、订阅补齐、草稿保留及服务端任务不中断。
