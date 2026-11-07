# 独立本地应用与 FPK 访问桥接

本模式用于前端、后端、管理服务、配置和生产数据均由本地应用拥有的部署，遵循所有 FPK 共用的 [公共部署与访问契约](fpk-access.md)。默认访问经 fnOS Web 入口，不要求局域网业务直连或额外登录；普通随包应用同样遵循公共访问约定，但不因此将业务移到包外。

未安装 FPK 时，本地应用仍能通过本机原生入口启动并读写同一份数据；该入口可以是命令、Unix Socket 或受保护的回环接口，不意味着必须另开局域网站点。已有业务登录按应用实际需要保留，不是本模式统一新增的要求。安装 FPK 后，用户可在 FPK 的 Web 管理面板中启动、停止、重启和更新实际后端。FPK 使用 `webbridge.NewControlledRelay` 提供面板和控制入口，并将实际管理动作委托给本地服务。

“不承载业务逻辑”不表示“没有管理能力”。FPK 可以创建面板、注入控制 UI、发起管理操作和显示进度/结果；业务实现、构建更新和真实进程管理仍在本地。只有原生应用已有完整管理页面、无需 FPK 面板时，才选择纯透传 `NewRelay`。

## 所有权与数据位置

下表是目录示意，不是默认路径。实际位置必须由主应用现有配置明确提供并解析；共享库不创建第二份应用配置或项目登记。

| 位置示意 | 所有者与内容 |
| --- | --- |
| `<应用目录>/repo/frontend/`、`<应用目录>/repo/backend/` | 本地仓库拥有前后端、管理程序源码和依赖声明 |
| `<应用目录>/repo/` 内的构建输出、依赖环境 | 由本地应用构建，按项目规则忽略，不复制到 FPK |
| `<应用目录>/state/config/` | 本地应用的唯一运行配置、私有设置 |
| `<应用目录>/state/data/`、`uploads/`、`logs/` | 数据库、账号、会话、上传和更新日志；与源码更新分别管理 |
| FPK 安装目录 | manifest、图标、入口、桥接二进制、管理面板及平台文件；无业务源码、业务前端构建资源或生产数据 |

本地数据不等于 Git 内容：数据库、账号、上传、私有配置和日志不得随源码提交。源码和数据可以使用主应用配置的不同本地磁盘位置，但都不能依赖 FPK 分配的持久化目录。路径应规范化后核对，不能通过符号链接又落回 FPK 管理目录。缺少或无法读取配置时明确报错，不能静默改用当前工作目录或新建另一套空库。

Git 更新只修改配置的工作区；构建和重启不得清空、覆盖或重建生产数据。数据库升级、备份和恢复属于本地应用自己的流程，FPK 生命周期不执行这些操作。

## 进程与控制边界

```text
用户 Web 操作
  → 飞牛入口 / FPK 管理面板与 NewControlledRelay
  → 本地常驻管理服务（实际状态、更新与进程操作）
  → 实际业务后端 / 本地仓库 / 本地生产数据

本地原生 Web 或命令入口
  → 同一本地管理服务和业务应用
```

本地管理服务由主应用在普通用户下独立启动并保持运行。停止业务后端仅结束业务进程及其任务、完成保存；不能结束管理服务自身。页面、状态和轮询不得隐式构建或启动已停止的业务。更新已停止的业务后仍保持停止，显式启动才恢复。

FPK 面板提供更新、应用本地修改、启动、停止和重启按钮。面板负责管理意图、权限与确认入口；本地服务再次校验后执行 Git、构建、校验、实际进程操作和保存。更新正在运行的后端时，先检查工作区和配置分支、获取并固定远端最新提交，然后停止旧进程并确认退出，应用该提交、构建校验、启动新版并检查就绪。返回的状态和运行版本必须来自真实进程；更新失败不返回假成功。无需更新代码时可用重启按钮单独替换进程。

区分包生命周期与后端管理：飞牛应用中心对 FPK 包的启动、停止、升级或卸载只影响访问桥接；用户点击 FPK 管理面板中的后端启停、重启和更新按钮，则实际控制本地业务。FPK 包关闭后原生访问仍可用，数据不随包卸载删除。若本地管理服务不可达，FPK 面板显示实际失败，不假报后端已启动或更新成功。

## FPK 侧：提供管理面板并委托真实操作

`ControlledService` 包含 `Authorize`、`Acquire` 和 `Control`。前两者校验平台访问与选择业务端点，`Control` 请求本地管理接口并返回实际 `backend.Snapshot`。FPK 不必实现任何业务语言的进程管理，可用共享 `ControlClient` 连接 Python、Node、Java、Go 等后端提供的 HTTP 接口。

```go
package entry

import (
    "context"
    "errors"
    "net/http"
    "time"

    "github.com/user-no-found/FnPackKit/runtime/backend"
    "github.com/user-no-found/FnPackKit/runtime/gatewaypolicy"
    "github.com/user-no-found/FnPackKit/runtime/identity"
    "github.com/user-no-found/FnPackKit/runtime/runtimepanel"
    "github.com/user-no-found/FnPackKit/runtime/webbridge"
)

type entryService struct {
    endpoint *webbridge.Endpoint
    control  *webbridge.ControlClient
    access   func(*http.Request, identity.Request) error
}

func (s entryService) Authorize(r *http.Request, id identity.Request) error {
    return s.access(r, id) // 实际飞牛应用权限检查，独立于可停止的业务进程
}

func (s entryService) Acquire(context.Context, identity.Request, string) (*webbridge.Endpoint, error) {
    return s.endpoint, nil // 已配置的常驻本地服务，不运行 Git、构建或启动程序
}

func (s entryService) Control(ctx context.Context, id identity.Request, command backend.Command) (backend.Snapshot, error) {
    return s.control.Execute(ctx, id, command)
}

// connection 与 policy 由应用现有加载器从唯一配置中解析。
func New(name string, policy gatewaypolicy.Config, connection webbridge.EndpointConfig,
    controlPath string, operationTimeout time.Duration,
    access func(*http.Request, identity.Request) error) (*webbridge.Bridge, error) {
    if access == nil {
        return nil, errors.New("application authorization is required")
    }
    endpoint, err := webbridge.NewEndpoint(connection)
    if err != nil {
        return nil, err
    }
    control, err := webbridge.NewControlClient(endpoint, controlPath, operationTimeout)
    if err != nil {
        endpoint.CloseIdleConnections()
        return nil, err
    }
    bridge, err := webbridge.NewControlledRelay(webbridge.Config{
        Name: name, Gateway: policy,
        Commands: []runtimepanel.Command{
            runtimepanel.UpdateBackendCommand(), runtimepanel.RebuildLocalCommand(),
        },
    }, entryService{endpoint: endpoint, control: control, access: access})
    if err != nil {
        endpoint.CloseIdleConnections()
    }
    return bridge, err
}
```

调用方从既有配置提供 `EndpointConfig.URL`、`Authorization`、`Cookies` 等连接值，示例不定义另一种配置格式。保留前缀时使用 `StripPrefix: false`。端点允许受保护的 Unix Socket 或显式回环地址；TCP 设置 `ForwardIdentity: true` 时必须配置桥接认证且由本地服务实际验证。可信 Unix peer、`ConnContext`、应用权限和 Origin 校验沿用 [WebBridge 契约](web-bridge.md)。桥接认证不替代业务登录和业务权限。

`NewControlledRelay` 提供 FPK 面板、HTML 注入及 `/_runtime/` 管理资源。`Commands` 声明更新等按钮，内置启停和自定义动作统一调用 `Control`；本地管理服务持有真实暂停/运行状态，桥接重启不会把它重置。业务停止或无法获取页面时，FPK 仍可显示自己的管理页。`bridge.Status` 也委托本地状态读取。此模式不接受另一个 FPK 本地 `ExecuteCommand` 回调，避免将实际执行分散成两套实现。

纯透传 `NewRelay` 仍可选择，但它只适合完全使用原生管理 UI 的应用。其“只要求 Authorize/Acquire、不创建面板”限制不能套用到 `NewControlledRelay`。

## 语言无关的本地控制协议

`controlPath` 是应用配置的本地管理路由，可与业务公开前缀不同。`ControlClient` 使用已验证的本机端点和明确超时，不重试管理命令、不跟随重定向，也不携带浏览器 Cookie。接口不要求后端导入 Go 包：

| 请求 | 本地服务的职责 |
| --- | --- |
| `GET controlPath` | 返回实际状态，不启动业务 |
| `POST controlPath`，`{"action":"start"}` | 启动并确认就绪后返回 |
| `POST controlPath`，`{"action":"stop","confirm":true}` | 停止旧进程、保存并确认退出后返回 |
| `POST controlPath`，`{"action":"restart","confirm":true}` | 停止旧进程并启动新版/当前版本，确认就绪 |
| `POST controlPath`，`{"action":"update-backend","confirm":true}` | 读取配置分支的最新提交，安全更新并替换正在运行的后端 |
| `POST controlPath`，`{"action":"rebuild-local","confirm":true}` | 应用当前本地修改并按原运行状态恢复 |

正常响应包含 `schema: 1`、`state`（running/stopped/paused/starting/stopping/unavailable）与 `paused`，可附加 `source` 中的实际工作区/运行提交。失败用非 2xx 或 `failed: true` 并保留真实状态；无效或超限响应不能当作成功。启动/重启只有状态确认为 running 才成功，停止也必须确认 stopped/paused。

管理服务实际验证桥接认证或 Unix peer，再解释已验证的用户身份。客户端发送连接配置中的机器认证及必要身份；POST 的 Origin 为本地服务自身 origin，不伪造浏览器 Sec-Fetch 身份。若应用提供原生浏览器会话，管理服务应将其与机器认证调用区分，浏览器会话继续独立检查 Origin/CSRF；不因此要求新增原生登录页。不能直接信任裸 TCP 的 FN 用户头。

前端 base、API 和流地址使用应用配置的公开前缀。示例保留前缀，因此本地原生入口也能使用相同路径；需要去前缀时由应用明确配置并验证管理资源的公开 URL。桥接不修补业务代码里写死的根路径。

## 本地侧：管理先于业务

管理服务可以用任意语言实现上述控制 API。Go 应用也可以组合 `backend.Controller`、`runtimepanel.Handler` 与 [本地仓库管理器](local-repository.md)。`localrepo.Manager` 实例位于本地管理进程，FPK 的 `entryService.Control` 请求它执行操作。

```go
controller := backend.New(manager)
panel := runtimepanel.Handler{
    Prefix: cfg.PublicPrefix, Name: cfg.DisplayName,
    Execute: controller.Execute,
    Commands: []runtimepanel.Command{
        runtimepanel.UpdateBackendCommand(),
        runtimepanel.RebuildLocalCommand(),
    },
}
panel.ExecuteCommand = func(ctx context.Context, id identity.Request, command string) (backend.Snapshot, error) {
    var err error
    switch command {
    case "update-backend":
        err = manager.Update(ctx, id)
    case "rebuild-local":
        err = manager.Rebuild(ctx, id)
    default:
        err = backend.ErrAction
    }
    status, statusErr := controller.Execute(ctx, id, backend.Command{Action: "status"})
    return status, errors.Join(err, statusErr)
}
```

本地管理入口按实际传输验证 Unix peer 或机器认证，再处理控制并路由业务；若另有原生浏览器会话，则继续检查其业务授权及 Origin/CSRF，不为 FPK 新增一套局域网登录。业务停止时管理 API 仍可访问，FPK 可用其自身管理页继续启动业务。本地原生访问也可以使用 `runtimepanel.Handler` 与 `Inject` 提供自己的面板；共享注入避免重复安装。

本地命令入口和 Web 控制调用同一套管理逻辑与配置。管理器负责跨进程独占、真实句柄、启动就绪及停止保存；路径和命令由服务端配置固定，浏览器不能指定任意可执行文件或数据目录。

## 首次连接与交互授权

安装入口必须初始化与本地管理服务的连接，或提供独立于现有失效连接的恢复入口。
不能把已有连接作为修复连接的前置条件，也不能将连接缺失报告成后端已经停止。

共享响应可以带 `problem`，使用固定 `code`、安全的 `message` 和
`password_required` 标识。连接配置无效、尚未绑定、认证拒绝、管理端无法连接、
状态格式错误和原生更新未配置会分别显示。不要把本地异常、命令或凭据直接交给页面。

显式需要系统授权的适配器可在 `Control` 接收 `Command.Password`。面板在操作
被要求授权后显示一次性密码输入，用户再次确认后提交同一动作；取消、关闭和提交
会清空输入，不使用浏览器存储。应用必须验证已认证的 NAS 身份并执行固定授权检查，
不得从浏览器接受可执行文件、任意参数或业务路径。共享库自身不自动执行 sudo。

密码只进入本机适配器；`ControlClient` 仅向原生管理 API 转发 action/confirm，
不转发密码。已有 `CommandFunc` 不支持凭据时会明确拒绝，避免静默丢弃授权。
原生业务的 Git、构建和进程管理仍在本地用户下执行。

桥接确需读取私有连接或验证系统授权时，FPK 可沿用其显式 root 生命周期声明，
同时使用 `UninstallPolicy(state_owner="root")` 和 current 身份的生命周期钩子。
共享运行机制先验证命名空间、设备、链接和所属账号，再将该 FPK 的状态及运行目录
保留为 root 所有；仅清理该应用声明的范围。此模式不与 NAS 用户目录模式混用。
初始化同时收紧安装器可能扩展的权限：私密状态移除组和其他用户访问，运行文件
移除组和其他用户写入。无密钥的包内程序引用与私密连接文件须分别校验权限，
不能假设安装器完全保留归档中的权限位。
权限准备在安装、配置、升级及启动前的声明阶段执行；生成的 main:status 和 main:stop 不执行权限交接。
root 模式的静态载荷整理保留运行目录中 Socket/FIFO 的所属账号、组和权限，由应用自身的受保护运行机制管理这些 IPC 对象。状态查询不得递归修复活跃服务的权限。
普通应用默认仍使用 package 状态所有权；不能为了显示管理面板而统一提升权限。

## AI 接入入口

共享入口 `AGENTS.md` 应引导到本页。应用仓库自己的 `AGENTS.md` 则必须说明以下事实；配置键与命令使用该应用已有名称，不把示例另建为权威配置：

- 部署模式：独立本地应用，FPK 使用带管理能力的 `NewControlledRelay`；纯透传仅在明确不需要 FPK 面板时选择。
- 唯一源码工作区、主配置加载器及其文件位置。
- 数据、上传、日志位置从哪些配置键读取；缺失时如何明确失败。
- 原生构建、校验、启动、停止、重启和更新命令，以及长期管理服务的启动入口。
- 业务状态、实际运行版本和管理日志的查看入口。
- FPK 提供管理面板、平台校验与控制请求，实际操作由本地服务处理；业务修改不进入安装目录，数据不进入 Git、不随包生命周期删除。

AI 修改前先核对实际配置解析后的工作区、数据目录、管理进程及运行版本，不输出凭据。不复制生产代码到第二个目录，不把正在运行的旧程序冒充当前源码；不得因为读不到配置而创建一套新的“默认生产数据”。已有应用的数据位置或迁移必须由维护者根据其配置确认，本契约不会自动迁移用户数据。

## 验收与验证范围

- 未安装或未启动 FPK 时，本机原生入口能启动业务并读写配置的数据；已有业务登录时验证其正常工作，不为验收额外创建局域网站点或登录。
- 原生访问与经 FPK 访问看到相同进程、运行版本和数据。
- 停止业务后管理服务、面板和控制 API 仍可用；刷新不启动业务。
- Web 更新和重启实际发生在本地管理服务，返回真实结果；停止状态和数据跨操作保留。
- 关闭或移除桥接后本地服务和数据不变；重建桥接后仍读取本地状态。
- FPK payload 含桥接、后端管理面板及平台必要文件；业务源码、业务前端产物和生产数据不被装入其中。

共享验证使用合成 Git 远端、真实独立管理/业务进程、临时本地数据、Unix 网关和实际浏览器。它证明共享机制与控制流程，不证明具体应用已经完成改造或真实 fnOS/FN Connect 部署；后者由应用维护者验证。
