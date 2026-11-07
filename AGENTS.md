# FnPackKit 接入与维护入口

FnPackKit 提供 FPK 构建层及供应用链接的 Go 运行机制。先阅读 [README](README.md)，再按需求选择下面的公共接口；接入说明和源码均随完整源码交付，不需要聊天记录或本机任务文件。

## 所有 FPK 的公共边界

先遵循 [FPK 公共部署与访问契约](docs/fpk-access.md)，再选择接入方式；本规则不只针对独立应用或特殊打包。

- 所有 FPK 默认由 fnOS 宿主机承载；有 Web 界面时，页面、API、WebSocket 与 SSE 经 fnOS Web／统一网关的同源应用前缀访问。浏览器不直连宿主机内部回环地址或局域网业务端口；无 Web 界面的服务无需增加 UI。
- 默认不要求局域网业务监听、独立站点、额外账号、登录或设备配对。局域网接口、中转和外部回调仅按明确业务需要扩展，不能为了预留接口而自动开放监听或新增登录体系；配置继续由主应用拥有。
- 不需要额外局域网鉴权，不等于取消 fnOS 身份、应用权限、来源/CSRF 校验及本机管理接口保护。受保护的 Unix peer 或回环机器认证是本机通信边界，不是让用户再次登录；已有业务登录不自动移除。
- 访问方式与 payload 内容分别选择：普通原生 FPK 可以携带业务程序；仅独立模式将业务和数据留在包外。不能统一强制 WebBridge、外置业务、额外控制面板或 root 权限。
- 静态封包检查不证明实际监听和统一入口已正确；应用须验证真实页面、API、流和管理权限，不能以局域网直连替代默认入口验收。

## 选择接入方式

Docker 为可选部署方式：**非必要不使用 Docker**，优先原生应用与运行时依赖。确需容器隔离时先读 [Docker 共享接口](docs/docker.md)，复用 `runtime/docker` 与 `fnpackkit.docker.preflight_file`，不要复制应用私有的 Docker 通用启停、探测和数据卷清理实现。应用继续拥有配置、镜像、身份、GPU 与业务就绪策略。fnOS 内置 Docker 不是名为 `docker` 的应用中心依赖。

| 应用需求 | 入口 |
| --- | --- |
| 常规原生应用随 FPK 发布 | 保留应用原有生产步骤，通过 `fnpackkit.api.build` 与 payload/adapter 构建；使用 README 的完整流水线契约。 |
| 构建时选择独立私密增强源码 | 显式声明 `fnpackkit.optional_packages={"schema":1}`，在版本选择后、构建标题与步骤前准备选择/冻结，复用 `selection`、`apply_source`、`validate_selection`；阅读 [可选构建包](docs/optional-packages.md)，不在薄入口或 plan 中获取上游。 |
| 原生应用需要悬浮后端启停面板 | 使用 `runtime/backend` 与 `runtime/runtimepanel`，阅读 [运行面板](docs/runtime-panel.md)。 |
| 前后端和数据独立于 FPK，用户通过 FPK 面板启停、更新后端 | 使用 `webbridge.NewControlledRelay`，先读 [独立本地应用契约](docs/independent-application.md)；FPK 提供面板并委托本地服务执行管理操作。 |
| 唯一业务源码在本地 Git 仓库，从仓库构建与运行，FPK 可管理后端 | 在本地管理服务中使用 `runtime/localrepo`，FPK 使用 `NewControlledRelay`；阅读 [本地仓库运行](docs/local-repository.md)。 |
| 原生应用已有完整管理界面，只需全部透传 | 可选 `webbridge.NewRelay`；这是不使用 FPK 面板的透传方式，不是独立应用的唯一方式。 |
| 应用明确要求由桥接进程提供后端启停面板 | 使用 `webbridge.New` 托管方式，阅读 [WebBridge 接入](docs/web-bridge.md)，遵循其进程与暂停门契约。 |

`webbridge` 是可选接入方式，不应把所有原生 FPK 都改成外部服务。程序按职责选择 `NewControlledRelay`、纯透传 `NewRelay` 或托管 `New`；应用自己的身份、配置、二进制和数据不放进 FnPackKit。

## 独立本地应用的 AI 接入边界

- 用户要求“没有 FPK 也能运行，但 FPK 要能管理后端”时使用 `NewControlledRelay`，接口为 Authorize + Acquire + Control。FPK 可以创建/注入面板，提供启停、重启、更新按钮并显示实际结果；禁止把“不承载业务逻辑”误解为“不提供管理能力”。
- 本地管理服务拥有 Git、构建、实际进程句柄和数据；FPK 的 Control 适配器通过本地 HTTP/Unix 请求委托执行，可使用 `NewControlClient`。后端可为任意语言，不必导入 Go 库；不能假报 Start/Stop 成功或向 FPK 复制业务源码、数据库。
- 先读取应用自己的 AGENTS、权威配置加载器、原生构建/管理入口与实际运行版本。目录从应用现有配置获取并规范化，不能把文档示例变成另一套默认配置。
- 前后端、依赖、构建产物、常驻管理服务和生产数据都由本地应用拥有；业务数据不进入 FPK 安装或其持久化目录，不进入 Git。缺失数据路径时明确失败，不能静默创建另一套空库。
- FPK 面板可启动、停止、重启和更新后端。更新先核对工作区/远端，再停止旧进程并确认退出，应用最新固定提交、构建校验并启动新版，返回真实运行版本；原本停止时更新后仍保持停止。业务停止后 FPK 控制页和本地管理服务仍可用，轮询不自行启动业务。
- 飞牛应用中心对 FPK 包的启动、停止、升级或卸载只管理桥接；FPK 面板里的后端启停和更新按钮则必须实际控制本地业务。两类操作不能混淆。业务数据不随包卸载删除；原生访问和 FPK 使用同一进程、配置和数据。

## 独立 Web 接入约束

- `NewControlledRelay` 实现 `ControlledService` 的 Authorize、Acquire、Control，Control 返回本地服务的实际 Snapshot。纯透传 `NewRelay` 只要求 Authorize/Acquire；托管 `New` 实现含 Running/Start/Stop 的 Application。控制不能依赖已停止的业务。
- 应用继续拥有可编辑运行配置、版本、授权来源、进程管理与数据策略。共享库接收解析后的配置及验证过的端点，不再创建并行配置或版本源。
- 服务使用 NAS 本机回环地址或受保护的 Unix Socket。需要向 TCP 后端传递 NAS 身份时，配置桥接认证并让后端实际验证它；不能直接信任浏览器提供的身份头。
- 飞牛统一网关入口、Unix Socket、可信 peer 和应用前缀保持一致。`peer.Listener`、`ConnContext`、来源策略及应用权限校验不能省略；不能用裸 TCP 请求伪造网关信任。
- 管理服务的控制路由先于业务获取处理；页面、API 与长连接遵守同一暂停状态。Relay 的状态在本地服务中，托管方式在桥接中。手动停止后刷新、轮询和重连不启动后端；停止失败不能报告成功。
- 控制按已认证用户隔离。多用户应用管理各自实例；共享单一服务的接入应限制为明确授权的单一所有者，不能承诺仅停止当前用户却停止所有人的服务。
- 前端 base、路由、Origin 和 HMR 路径由应用按公开前缀配置。普通 WS 无须开启票据；只有配置握手票据时才同时声明 `HandshakeGrants` 和 `WebSocketPaths`。
- `NewControlledRelay` 与 `New` 可以注入共享管理面板并保留业务 CSP；只有纯透传 `NewRelay` 不注入。业务代码独立更新；修改 FPK 中的桥接、面板、图标或权限声明仍需要更新包。
- 本地仓库模式只在仓库所属普通用户下执行 Git 和构建；更新仅快进干净分支，不能自动清理或覆盖用户改动。应用报告实际子进程启动时版本，不能把当前工作区冒充运行版本；已手动停止的后端更新后仍保持停止。

## 获取完整接口

Go 导入路径是 `github.com/user-no-found/FnPackKit/runtime`；权威源码位于本项目 `runtime/`。应用构建使用选定完整 checkout 或解压 sdist 的 `FNPACKKIT_PATH/runtime`，同时分发根 `LICENSE`。

Python wheel 只提供 Python 构建层，不包含独立 Go 模块或这套完整源码入口。需要桥接接口时须获取完整 checkout/sdist；不要只安装 wheel 后复制零散 Go 文件，也不要假设另一个 AI 能读取本机未提交改动。

## 验证入口

- Go 共享机制：在 `runtime/` 执行 `go test -race ./...`；按本机环境选择满足 `go.mod` 的工具链及可执行临时目录。
- Python 封包：按 README 设置 BuildFlow 源码依赖，执行 `python3 -m unittest discover -s tests -v`；真实 fnpack 检查显式设置 `FNPACKKIT_E2E=1`。
- 桥接真实浏览器：按 [接入文档的验证命令](docs/web-bridge.md#验证) 使用登记的 BuildFlow 浏览器环境和 `-count=1`；不能用只返回 200 的页面替代 HTTP、WS、启停和暂停门检查。
- 源码交付：设置 `FNPACKKIT_DISTRIBUTION_E2E=1` 后运行 `python3 -m unittest discover -s tests -p test_distribution.py -v`，检查实际源码包与权威接口、文档、面板资源的字节一致性，以及 wheel 的独立 Python 交付边界；可用 `FNPACKKIT_DISTRIBUTION_OUTPUT` 保留本次发行包。
- 应用的实际 FPK、真实 OS 进程与数据、多用户、fnOS 桌面/App/FN Connect 均由接入应用验证，不能把共享夹具结果当作实机结论。

维护文档描述当前使用契约；临时检查输出、实现任务及本机路径不属于源码交付。保留已有应用接入方式与用户工作，提交或发布仍须用户明确授权。
