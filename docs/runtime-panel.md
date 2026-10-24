# 共享后端控制面板

`runtime/backend` 管理每个已认证用户的手动暂停门；`runtime/runtimepanel` 提供 Go 嵌入的 JS/CSS、控制接口、HTML 注入和停机页面。业务后端停止时，轻量网关继续提供启动入口。整个应用的完全关闭由飞牛应用中心负责。

需要独立部署本机 Web 服务的应用可使用 [webbridge 组合接口](web-bridge.md) 连接这些机制。`Handler.Recovery` 在获取或连接失败时提供独立恢复页，保留控制资源并返回 503，不将失败描述为停止成功或已保存数据。

## 应用接入

应用实现 `backend.Target`：`Running` 只查询当前真实进程，不启动工作；`Start` 复用既有初始化路径；`Stop` 处理在途请求、保存与进程退出。三个方法均验证应用权限和身份，并支持并发的状态读取。应用负责传入由自身配置加载器解析的资源和超时策略。

`backend.New(target)` 创建一个网关生命周期内共享的控制器。所有普通业务实例获取必须经过 `Controller.Use(ctx, identity, acquire)`；手动操作通过 `Controller.Execute`。不能只给控制按钮接入共享状态，而让普通请求绕过暂停门。

`runtimepanel.Handler{Prefix: prefix, Name: displayName, Execute: controller.Execute}` 使用应用现有的网关前缀和项目显示名称。先运行可信 Unix peer、NAS 身份、应用授权、Origin、路径和会话验证，再调用 `Handle`；组件不替代这些边界。控制资源路由必须在业务实例获取前处理。

若 `Use` 返回 `backend.ErrPaused`，调用 `Handler.Unavailable`：文档导航获得独立停机页面，业务请求和 WebSocket 获得 503。页面与控制资源不依赖已停止的业务服务。状态轮询和自动重连不会唤醒手动暂停的实例。

代理请求设置 `Accept-Encoding: identity`，在 HTML 响应中调用 `runtimepanel.Inject(response, prefix)`。组件只改写具有明确完整 head 的 200 HTML 文档，定位时跳过脚本、样式、注释、属性和模板中的标签文本。令牌字符串、空响应、HTML 片段和边界不明确的页面原样保留正文及 Cookie/缓存元数据，不改 API 或 WebSocket；保留原页面 CSP，并为严格 nonce 策略沿用原有脚本 nonce。压缩 HTML、过大的入口页面和冲突 nonce 策略会明确失败，不能通过删除 CSP 解决。JS/CSS 随共享 Go 模块嵌入，无运行时 CDN。

## HTTP 协议

路径以应用前缀为基准：

| 路径/方法 | 用途 |
| --- | --- |
| `/_runtime/control` GET | 不唤醒后端的状态查询 |
| `/_runtime/control` POST | `{action, confirm}` 意图，仅接受 start、stop、restart |
| `/_runtime/manifest` GET | 当前应用名称、前缀和 UI 扩展声明 |
| `/_runtime/{bootstrap.js,panel.js,drag.js,panel.css}` GET/HEAD | 已认证的共享控制资源 |

POST 使用 `Content-Type: application/json`、`X-FnPackKit-Control: 1`，并通过应用原有的 Origin 策略。stop/restart 要求 `confirm: true`。控制体不接受 UID、用户名、进程地址、凭据或 shell 命令。身份作为服务端已验证参数传入。

状态包括 running、stopped、paused、starting、stopping、unavailable。失败不会冒充停止成功；停止或重启失败时，暂停门继续阻止普通请求创建新实例。显式启动成功后解除暂停。

手动暂停保存在当前网关进程内，同一用户的所有标签页共享；应用服务重启后恢复正常按需启动。空闲回收不会设置手动暂停，各应用仍负责自己的空闲策略和活跃请求判定。停止会结束正在执行的任务，保留数据与历史不等于恢复被终止的进程。

## 交互与扩展

悬浮按钮可拖动，靠近四个边缘时吸附并半隐藏。悬停、键盘聚焦或打开面板时完全显示；位置是当前浏览器按应用前缀保存的界面偏好。调整视口会将按钮和面板限制在可见区域，支持减少动画偏好。

`runtimepanel.Action{ID, Label, Selector}` 注册当前文档中既有按钮的入口。共享面板隐藏重复入口，点击扩展时按选择器查找当前按钮；SPA 替换节点后继续使用新按钮，目标暂时移除时入口停用，重新挂载后恢复。业务权限仍由该按钮对应的应用后端检查。FnTuiOS 用该接口保留 sip 的终端显示设置。扩展不接受来自浏览器的任意执行命令。

## 验证入口

共享 Go 包使用 `cd runtime && go test -race ./...`。`FNPACKKIT_BROWSER_E2E=1 python3 -m unittest discover -s tests -p test_runtime_panel_browser.py` 使用 BuildFlow 已登记的真实浏览器验证界面和停机交互。应用应另行验证自己的真实进程、身份、数据保留和认证/传输链路。
