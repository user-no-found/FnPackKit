# 本地仓库运行与 FPK 桥接

本模式遵循所有 FPK 的 [公共部署与访问契约](fpk-access.md)：默认使用宿主机本地服务和 fnOS Web 入口，不要求开放局域网业务端口或建立额外登录体系。本地原生入口可以是命令、Unix Socket 或回环接口；保护该入口与 Git 认证仍是必要的本机操作边界，不代表要提供独立局域网站点。

本模式让普通用户的本地 Git 工作区成为唯一业务源码，构建、校验和启动均从该仓库进行，不向 FPK 复制业务。`runtime/localrepo` 位于独立本地管理服务；FPK 使用 `webbridge.NewControlledRelay` 提供启停/更新面板，通过 Control 请求实际执行。先读 [独立本地应用契约](independent-application.md)，确认目录、原生入口和语言无关的控制接口。

| 位置 | 内容 |
| --- | --- |
| FPK 安装目录 | 桥接程序、入口、图标、权限与生命周期文件 |
| 本地 Git 根目录 | 业务源码、依赖环境和由应用生成的构建产物 |
| 本地应用配置的数据目录 | 配置、数据库、账号、会话、上传和日志；独立于 FPK 目录且不进入 Git，与源码更新分别管理 |

应用继续拥有目录、Git 路径、远端别名、分支、命令、超时和数据策略的可编辑配置。共享库只接收解析后的值，不创建应用登记或第二份配置文件，也不自动改变任何已安装应用的目录。

## 运行用户与权限

仓库侧控制程序在仓库所属的普通 NAS 用户下运行；`localrepo.New` 拒绝 root、用户身份切换及不属于当前用户的仓库。Git、构建和校验均不调用 sudo，不修改目录属主或系统权限。Git 使用该用户已有的认证配置，并禁止终端交互提示；认证失败直接报告失败，不能停机后才发现无法拉取。

本模式的 FPK 与仓库侧管理服务是独立进程。管理服务由本地原生入口启动，业务停止后仍在。FPK 可以提供管理面板、发起启停/重启/更新并读取真实结果；实际 Git、构建和进程操作由本地管理器执行，业务数据配置也由本地拥有。管理入口验证原生会话或受保护的桥接认证与权限，不信任裸 TCP 的用户头。

具体应用仍实现 [WebBridge 授权及网关契约](web-bridge.md)。配置源路径、启动命令和 Git 远端来自服务端配置，浏览器只提交预先声明的操作意图。

## 创建管理器

导入 `github.com/user-no-found/FnPackKit/runtime/localrepo`。下面的 `cfg` 由应用现有加载器读取，`runtime` 实现真实进程管理：

```go
manager, err := localrepo.New(ctx, localrepo.Config{
    Directory: cfg.RepositoryDirectory,
    Git:       cfg.GitExecutable,
    Remote:    cfg.GitRemote,
    Branch:    cfg.GitBranch,
    Build:     cfg.BuildCommands,
    Verify:    cfg.VerifyCommands,
    Timeout:   cfg.OperationTimeout,
    Output:    privateLog,
}, runtime)
```

`Directory` 必须是本地 Git 工作区根目录，不能指向其中某个子目录。符号链接在创建时解析为固定规范路径，运行期间路径被替换会拒绝操作。`Git` 是明确的绝对可执行文件路径。`Remote` 使用由字母、数字、下划线和连字符组成的本地远端别名，例如 `origin`；不由浏览器指定 URL。`Branch` 必须是有效分支。

`Build` 可以为空，适用于无需编译的程序。`Verify` 至少声明一条校验命令，`Timeout` 必须显式为正。命令使用 `Command{Program, Args}`，不通过隐式 shell 拼接。相对脚本路径从同一仓库解析；裸程序名按进程环境查找。应用需要环境设置时使用其受控启动脚本或既有用户环境。构建产物与虚拟环境应加入该仓库的 `.gitignore`。

命令输出有 8 MiB 的检查上限，非忽略未跟踪文件最多 4096 个，超过时明确失败。Git 源码差异不写入日志；Git 错误与构建校验日志写入 `Output` 指定的私有目标，不提供给公共页面。默认不运行 Git 子模块更新；遇到索引中的子模块会拒绝检查，依赖多个 Git 工作区的项目需要应用自己的完整依赖工作流。

## 真实进程接口

```go
type Runtime interface {
    State(context.Context) (localrepo.RuntimeState, error)
    Start(context.Context, localrepo.Launch) error
    Stop(context.Context) error
}
```

- `State` 支持并发读取，返回实际进程的存活状态、工作目录和启动时版本。不能每次重新读取工作区后声称那是运行版本。
- `Start` 使用 `Launch.Command(program, args...)` 创建命令，复用 `process.Start` 或应用自己的进程管理器。命令工作目录固定为仓库，随后检查就绪。方法的 `ctx` 限制启动与就绪等待，不是业务进程的长期寿命；命令本身不会绑定这个短期操作上下文。
- `Stop` 结束实际业务进程和其子任务，完成必要保存后返回；不结束本地管理服务或 FPK 桥接。不能只断开代理。启动证据不符、启动失败或源码在启动期间变化时，管理器会调用 `Stop` 清理该次尝试。

`Launch.Version()` 提供本次校验的源码快照。构建、校验与启动命令均收到 `FNPACKKIT_LOCAL_SOURCE` JSON，其中包含提交号、源码指纹和 `dirty`。解释执行的后端可以在实际子进程启动时调用 `localrepo.StartupVersion()` 保存这个值，并通过受认证状态通道报告；同时必须报告自己的实际工作目录。

编译型后端应在构建时把这份快照嵌入最终产物，并由运行程序报告嵌入值。不能让旧二进制只读取新的启动环境变量就冒充新构建。`localrepo` 检查声明的构建与校验过程及进程证据，不替应用证明某个编译命令正确生成了目标程序。依赖锁、编译选项、生成文件及产物验证由应用的构建流程负责。

## 操作含义

| 操作 | 行为 |
| --- | --- |
| `Start(ctx, user)` | 未运行时构建、校验并启动当前本地代码；允许未提交修改并标记 `dirty`；已经运行则复用该进程 |
| `Stop(ctx, user)` | 停止实际后端，不改变 Git 或用户数据 |
| `Rebuild(ctx, user)` | 停止正在运行的后端，构建校验当前本地代码，成功后恢复运行；原先停止则保持停止；不拉取远端 |
| `Update(ctx, user)` | 检查干净工作区及配置分支，获取远端并固定提交，仅快进更新；构建校验后恢复原先运行的后端 |
| `SourceStatus(ctx, user)` | 区分当前工作区和实际进程启动版本，不获取远端或启动服务 |

更新不会自动暂存、重置、覆盖未提交文件、丢弃本地提交或推送。分叉、领先于目标的本地提交、错误分支、未提交内容和认证失败都会拒绝更新。远端获取与快进可行性检查在停机前完成；原本停止的服务不会因更新而启动。

已经快进后若构建或校验失败，源码保留实际快进结果，后端保持停止并报告失败。库不偷偷回退仓库，也不宣称发布成功。修复后可重新应用本地修改或显式启动。库不迁移或回滚数据库。

源码指纹包括 HEAD、索引/工作区差异和非忽略未跟踪文件内容。构建校验前后及启动前后会复查，变化则拒绝作为成功候选。被忽略的构建输出不属于业务源码指纹；应用校验负责这些产物。每个工作区使用一个管理器，多个进程之间的独占锁由应用负责；外部编辑器和 Git 命令仍可能改变同一可写工作区，这种运行方式不提供开发/生产文件隔离。

## 接入 WebBridge 和面板

本地管理服务可组合 `*localrepo.Manager`、`backend.Controller` 和鉴权后的 `runtimepanel.Handler` 实现控制 API。FPK 的 `ControlledService.Control` 可用 `webbridge.NewControlClient` 调用该接口，管理按钮和面板由 FPK 提供；真实运行与暂停状态仍在本地。鉴权仅允许该工作区所有者，普通页面/轮询不隐式启动业务。Acquire 选择业务端点，不代替管理请求。

面板命令使用 `runtimepanel.RebuildLocalCommand()` 和 `runtimepanel.UpdateBackendCommand()`，分别表示“应用本地修改”和“更新后端”。本地服务先创建 `controller := backend.New(manager)`，再在经过鉴权的命令回调中选择固定方法：

```go
var commandErr error
switch command {
case "rebuild-local":
    commandErr = manager.Rebuild(ctx, user)
case "update-backend":
    commandErr = manager.Update(ctx, user)
default:
    commandErr = backend.ErrAction
}
snapshot, statusErr := controller.Execute(ctx, user, backend.Command{Action: "status"})
return snapshot, errors.Join(commandErr, statusErr)
```

`controller.Execute` 读取本地管理服务持有的暂停与真实进程状态，桥接重启不会清除它。自定义回调不能自行拼一个“已运行”结果。进程管理器对启动、停止、构建和更新串行处理；长操作期间源状态可报告暂不可读，控制页仍可加载。

实现 `backend.SourceReporter` 的目标会在原有状态响应中附带 `source`。面板展示本地代码与运行版本、未提交修改及是否需要构建重启。仓库路径、远端 URL、命令及认证材料不会进入这个状态对象。源码暂不可读不会阻止停止后端。未实现该可选接口的目标没有源码区块。

## 验证边界

在 `runtime/` 使用当前项目 Go 工具链执行 `go test -race ./...`，在可执行临时目录下验证。`localrepo` 测试创建合成 Git 远端、独立工作区和真实子进程，验证快进、工作目录、启动证据、修改保护、取消及共享网关暂停。面板浏览器验证沿用 [运行面板验证入口](runtime-panel.md)。

这些检查不需要访问私密生产库。实际应用仍需由维护者完成其进程接口、配置、数据保存和 fnOS/FN Connect 验证。业务改动通常只需在本地仓库构建并重启；桥接二进制、内嵌面板、入口或权限声明变化仍需要重新打包安装 FPK。
