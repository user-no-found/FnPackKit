# 独立本机 Web 服务接入

`runtime/webbridge` 是可选 Go 组合接口，适用于在 NAS 本机独立部署、经常修改前后端的应用。FPK 提供稳定的飞牛桌面入口和轻量桥接进程，业务页面、API 和程序由独立服务提供。应用显式调用 `webbridge.New` 后使用这一入口；其他应用继续使用自己的集成方式。

访问链路为飞牛桌面 iframe → fnOS 统一网关 → FPK Unix Socket → `webbridge.Bridge` → 本机业务服务。前端、API、WebSocket 和 SSE 均保留在同一个公开应用前缀下。平台契约见官方[应用入口](https://developer.fnnas.com/docs/core-concepts/app-entry/)及[统一网关](https://developer.fnnas.com/docs/core-concepts/gateway-registration/)文档；具体 fnOS 版本和实际移动端/FN Connect 路径需由消费应用验证。

## 应用与包的职责

FPK 保留 manifest、图标、入口、资源/权限声明、桥接二进制与生命周期脚本。业务程序和前端资源在应用自己管理的独立部署目录或容器中，用户数据与程序更新分别管理。业务服务发布、前端重新构建及缓存更新不需要重新封包；修改桥接二进制、内嵌控制面板或包声明仍涉及 FPK 更新。

飞牛应用中心的启动、停止和卸载管理桥接进程。应用自行决定这些生命周期操作是否也停止独立业务服务及保留哪些数据。应用中心显示包版本；业务版本由独立服务返回，二者不必相同。

## 公共接入契约

导入 `github.com/user-no-found/FnPackKit/runtime/webbridge`。应用实现：

```go
type Application interface {
    Running(context.Context, identity.Request) (bool, error)
    Start(context.Context, identity.Request) error
    Stop(context.Context, identity.Request) error
    Authorize(*http.Request, identity.Request) error
    Acquire(context.Context, identity.Request, string) (*webbridge.Endpoint, error)
}
```

- `Authorize` 在每个业务、控制、资源或票据请求前检查当前应用访问权限及会话。这项检查及控制方法须不依赖已停止的业务进程。身份已通过受信 Unix 连接及网关策略校验；应用仍需核对其权威授权来源，必要时调用 `auth.Check`。NSS 存在、入口可见或用户名匹配本身不建立权限。
- `Running` 只检查该用户的真实运行状态，不启动实例。
- `Start` 启动并确认该用户的服务已就绪；已运行时应安全复用。
- `Stop` 停止实际服务，处理在途 HTTP/流连接、保存与退出，失败返回错误。桥接层不执行任意 shell 命令，也不把中断转发当成停止成功。
- `Acquire` 接收去掉应用前缀后的路径，例如 `/`、`/api/items`、`/hmr`，返回本次请求应使用的端点。它可以复用应用既有的按需启动流程；若部署流程要求手动启动，也可以在未运行时返回错误。端点应已创建并验证，不需要每次请求重建连接池。

控制器的暂停状态按已认证用户隔离。实现这些方法时必须保持同样的用户范围；共享一个全局后端的应用应限制为明确授权的单一所有者，不能向多个用户承诺仅停止自己的实例。应用对并发、业务权限及实际进程的最终判断仍由自身负责。

## 端点与运行配置

从应用现有的配置加载器读取实际地址、连接超时、认证和 Cookie 设置，再创建不可变端点。接口不读取另一份配置文件，不保存项目登记，也不自动下载或启动开发服务器。

```go
endpoint, err := webbridge.NewEndpoint(webbridge.EndpointConfig{
    URL:             cfg.WebOrigin, // 例如 http://127.0.0.1:8000
    StripPrefix:     true,
    ForwardIdentity: true,
    Authorization:   cfg.BridgeAuthorization,
    Cookies:         cfg.ApplicationCookies,
    Transport:       configuredTransport,
})
if err != nil {
    return err
}
```

| 字段 | 契约 |
| --- | --- |
| `URL` | 必填 HTTP(S) origin；不含用户信息、子路径、查询或 fragment。TCP 只接受数字回环 IP 和显式非零端口，不接受域名或局域网地址。 |
| `UnixSocket` | 可选绝对规范路径。指定时使用 HTTP 连接本机 Socket，URL 的 authority 用作后端 Host；应用须保护 Socket 及其父目录。 |
| `StripPrefix` | 默认 false，后端收到完整公开应用路径；true 时仅去掉应用前缀，例如 `/app/example/api` → `/api`。 |
| `ForwardIdentity` | 默认 false；true 时由服务端身份重建 `X-Trim-Userid/Username`。TCP 模式必须同时设置桥接认证。 |
| `Authorization` | 可选服务端认证 Header 值。后端必须验证它，仅配置而未验证不能建立信任；不得使用飞牛会话 Cookie 或 `TRIM_API_TOKEN`。 |
| `Cookies` | 默认空列表。只转发显式允许的应用 Cookie；重复同名请求 Cookie 拒绝。响应 Cookie 去掉 Domain，并限制在本应用路径；不支持需要根路径的 `__Host-` Cookie。 |
| `Transport` | 可选，克隆应用配置好的 `*http.Transport`，保留其超时及 TLS 设置。nil 使用 Go 默认 transport 设置；两种方式均禁用环境 HTTP 代理。 |

应用读取配置并创建端点后，配置文件的改动不会自动生效。使用应用已有的重载或重启流程更新端点；`Acquire` 返回新端点即可支持独立服务地址随重启变化。退役旧端点时调用 `CloseIdleConnections`，活跃流和 WebSocket 由应用的真实停止流程处理。

请求转发前清除浏览器提供的身份、代理、Authorization 和 Cookie 头，再按端点声明重建；带下划线的对应身份/代理别名也会清除。不会转发未经校验的管理员标志。公开 Origin 保留，后端应显式配置允许的公开 origin，而非信任任意客户端身份头。

一个服务同时提供前端和 API 时，`Acquire` 返回同一个端点。使用前端开发服务器与独立 API 时，可按规范相对路径选择已准备的端点：

```go
if relative == "/api" || strings.HasPrefix(relative, "/api/") {
    return app.apiEndpoint(user)
}
return app.frontendEndpoint(user)
```

这种路径选择与端点配置由应用拥有。两类请求都会经过同一用户的暂停门，任何独立转发路径都不能绕过它。

## 连接飞牛网关与控制面板

```go
bridgeConfig := webbridge.Config{
    Name:           displayName,
    Gateway:        gatewayConfig,
    Actions:        applicationPanelActions,
    ReportError:    reportPrivateProxyError,
}
bridge, err := webbridge.New(bridgeConfig, application)
if err != nil {
    return err
}
trusted, err := peer.NewListener(unixListener, trustedGatewayUIDs, connectionLifetime)
if err != nil {
    return err
}
server := &http.Server{
    Handler:           bridge,
    ConnContext:       trusted.ConnContext,
    ReadHeaderTimeout: headerTimeout,
}
return server.Serve(trusted)
```

`gatewayConfig` 是应用现有的 `gatewaypolicy.Config`；前缀来自同一应用配置，并与 FPK 入口一致。监听 Socket 放在 `${TRIM_APPDEST}` 中声明的位置，可信 peer UID 和连接寿命由部署配置提供。直接通过 TCP 或未标记的 Unix listener 调用 Bridge 会被拒绝。`ReportError` 可选，只交给应用自己的受控日志，浏览器错误不包含端点或内部错误细节。

原有电源悬浮按钮、拖动吸附、状态、启停确认及扩展入口直接复用 [运行面板](runtime-panel.md)。控制资源在业务获取之前处理；即使业务服务关闭也可加载。手动停止后刷新页面、轮询 API 和 WebSocket 重连都不会再次调用 `Acquire`。停止失败同样保持暂停，显式启动成功才解除。暂停保存在桥接进程内，重启整个桥接进程会清除它。

正常 HTML 文档自动注入面板，API 和流不注入。转发请求使用 `Accept-Encoding: identity`；前端服务须允许未压缩 HTML。保留业务 CSP 与兼容 nonce，业务应允许嵌入飞牛同源窗口及加载共享控制资源；不通过删除 CSP 兼容任意页面。HTML 使用 `Cache-Control: no-store`，导航请求去掉条件缓存头，静态资源缓存仍由业务服务管理。内部重定向和 Cookie 路径映射回应用前缀；外部 HTTP(S) 重定向仍由业务应用的认证与跳转策略负责。

业务获取或代理失败时，文档导航返回带同一控制面板的 503 恢复页；API/流返回对应 503/502 错误。恢复页不声称服务已经停止或数据已经保存。手动暂停时则提供原有停机页面，API/流返回 503。

## WebSocket、热更新与独立发布

浏览器使用实际公开 origin 和应用前缀构造 HTTP/WS 地址。前端框架的 base、路由和 HMR 公开路径也应配置到此前缀；桥接不修改任意 HTML/JS 中写死的根路径，不自动实现框架热更新或后端热重载。生产前端仍需构建并发布最新资源，后端代码仍需由其服务管理器重载或重启。

普通 WebSocket 使用既有 Origin 策略，无须声明 `WebSocketPaths`。需要一次性握手票据时，在调用 `webbridge.New` 前同时设置票据配置和唯一的路径；值继续来自应用配置：

```go
bridgeConfig.Gateway.HandshakeGrants = &gatewaypolicy.GrantConfig{
    Lifetime: cfg.GrantLifetime,
    Capacity: cfg.GrantCapacity,
    PerUser:  cfg.GrantPerUser,
}
bridgeConfig.WebSocketPaths = cfg.WebSocketPaths // 例如 /ws、/hmr
```

POST `/_gateway/grant/ws` 仅为配置的 `/ws` 签票，`/_gateway/grant/hmr` 仅为 `/hmr` 签票；仅配置一个 WS 路径时也接受 `/_gateway/grant`。路径均相对公开应用前缀。未配置路径返回 404，签票不获取或启动业务实例。

现有 `web/gateway-transport.js` 可把相应签票地址作为 `grantEndpoint`；每次握手重新获取证明，浏览器请求只携带意图。原生开发服务器的 HMR 客户端未接入该辅助程序时，应使用精确配置且经实际握手验证的 Origin 策略。共享票据不替代业务会话和长连接授权。

## 验证

普通共享回归：在 `runtime/` 执行 `go test -race ./...`。若默认临时目录带 noexec，按本机环境设置可执行的 `GOTMPDIR`。

真实浏览器回归使用应用已有的 BuildFlow 浏览器登记，在项目根运行：

```sh
PYTHONPATH="$BUILDFLOW_PATH/src" python3 - <<'PY'
import os
import subprocess
from buildflow.browser import resolve_browser

tools = resolve_browser()
subprocess.run(
    ["go", "test", "-race", "-count=1", "-run", "TestBrowserIndependentDeploymentAndControls", "-v", "./webbridge"],
    cwd="runtime", check=True,
    env={**os.environ, **tools.environment(), "FNPACKKIT_BROWSER_E2E": "1"},
)
PY
```

该回归通过真实 Unix 入口和本机 HTTP 服务，在模拟桌面 iframe 中修改外部前端文件、更换后端发布并启停真实 HTTP listener。它验证共享协议和浏览器行为；消费应用仍需验证自己的进程管理、数据保存、多用户权限及真实 fnOS 桌面/App/FN Connect 环境。
