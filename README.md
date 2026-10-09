# FnPackKit

FnPackKit 使用 BuildFlow 的同一套步骤执行器、日志、进程取消和产物发布机制，在原生 Linux 上生成并验证 fnOS FPK。应用提供自己的构建步骤、模板与 payload 声明；工具不保存应用清单，也不包含应用身份、图标或专属二进制规则。最终应用直接链接 `runtime/` 中的 Go 共享机制，NAS 无需安装 FnPackKit。

Python 要求 3.11 或更新。`pyproject.toml` 是 FnPackKit 工具版本的唯一来源。依赖方向为应用 → FnPackKit → BuildFlow；普通 Linux/Windows 项目可以直接使用 BuildFlow。Python 包声明 BuildFlow 的 GitHub 来源，本地源码初始化不安装系统环境或下载工具。

首次使用源码 checkout 时执行一次初始化，不需要手动 `export`：

```sh
/path/to/FnPackKit/setup.sh --buildflow /path/to/BuildFlow
/path/to/FnPackKit/build.sh --project /path/to/application/project.json \
  --fnpack /usr/local/bin/fnpack
/path/to/FnPackKit/build.sh --workspace /path/to/workspace --discover
```

若已执行 BuildFlow 的 `setup.sh`，直接运行 `/path/to/FnPackKit/setup.sh` 即可。初始化通过 BuildFlow 的共享机制保存 `BUILDFLOW_PATH`、`FNPACKKIT_PATH`，不保存下级应用、浏览器或项目配置。缺少 BuildFlow 时明确提示初始化命令；不猜测相邻目录、不下载依赖。重复初始化由同一个机制合并工具记录。

共享配置位于 `${XDG_CONFIG_HOME:-$HOME/.config}/buildflow/environment.json`，同目录 `environment.sh` 是初始化生成的 shell 环境投影。`setup.sh` 和 `build.sh` 每次启动读取该投影；用户无需重开终端。BuildFlow 初始化也管理 shell 的加载入口。配置文件通过 `setup.sh` 维护，不手工编辑；移动 checkout 后重新运行初始化。显式设置的环境变量优先，包括无效或空路径会明确失败，不能悄悄回退到已登记位置。`--buildflow` 优先于环境变量。

`PYTHON` 可指定解释器。Python API 调用可继续使用显式 `PYTHONPATH=/path/to/FnPackKit/src:/path/to/BuildFlow/src`，或在项目 Python 环境中安装对应包。工具不自动更新自身依赖。没有明确项目时仅扫描本次 `--workspace`，默认深度 4、目录预算 1000；可以用 `--max-depth`、`--max-directories` 调整。发现只读 JSON，不导入 adapter、不执行命令、不跟随符号链接。无法构建的声明与部分扫描警告可见。交互选择需要 TTY，CI 应传 `--project`。不扫描整个 home 或自动登记项目。

## 应用描述与 payload

应用唯一 `project.json` 包含 BuildFlow 声明和 FPK 声明。以下展示字段结构，应用自行提供实际身份与命令：

```json
{
  "app_id": "example",
  "display_name": "Example",
  "version": "1.2.3",
  "buildflow": {
    "schema": 1,
    "requires": ">=0.1,<0.2",
    "targets": ["fpk"],
    "steps": [
      {"name": "原生构建", "argv": ["{python}", "scripts/payload.py", "{work}"], "cwd": "{project}"}
    ]
  },
  "fnpackkit": {
    "schema": 1,
    "requires": ">=0.1,<0.2",
    "payload": "{work}/payload.json"
  }
}
```

`project.json` 顶层 `version` 是应用版本的唯一权威。开始构建时，BuildFlow 在项目锁内显示已安装版本和项目版本，先选择正式版 `stable` 或测试版 `test`，再输入本次版本。直接回车采用默认：同类型正式版递增 patch，测试版递增至少两位的尾号；正式 `1.2.3` 切换测试默认为 `1.2.3-01`，测试 `1.2.3-02` 切换正式默认为 `1.2.3`。手动或默认选择均原子写回同一个 `project.json`，再加载 adapter，后续步骤都使用新值。构建失败保留所选项目版本，不回滚用户的版本选择。

FnPackKit 只读 `/var/apps/<app_id>/manifest` 获取已安装版本，支持 fnOS 的空格对齐 `key = value` 格式。安装目录不存在显示未安装；目录存在但 manifest 缺失、权限不足、字段损坏或 appname 不匹配会明确报错。已安装版本仅供比较，不用于计算建议版本；历史安装版本原样显示。

CI 使用 `--build-version 1.2.4`、`--build-version 1.2.4-01`，或 `--next-version --channel stable|test`；非交互环境缺少明确选版方式会失败，不等待输入。`--channel` 可在交互时预选类型。没有顶层 `version` 的简易项目沿用自身 payload 版本，不进入选版流程，也不能传这些选版参数。通用命令步骤可用 `{version}` 接收选定值。

应用命令按选定版本写出 payload manifest；下例展示选择 `1.2.4` 后的结果。所有相对输入路径以 payload manifest 所在目录为基准。`destination` 相对封包 staging，必须是安全路径。`mode` 是 JSON 整数，例如 `493` 表示 `0755`。模板目录由应用拥有，`.in` 文件中的 `@UPPER_TOKEN@` 由 `values` 替换；缺少值或残留嵌套 token 会失败。运行配置中的其他占位符应由应用运行时 Go 配置模块处理，避免把包含待运行时替换 token 的文件声明为 `.in`。

```json
{
  "schema": 1,
  "template": "/path/to/application/packaging/fnos",
  "files": [
    {"source": "/path/to/build/program", "destination": "app/bin/program", "mode": 493}
  ],
  "values": {"APP_ID": "example", "VERSION": "1.2.4", "PLATFORM": "x86"},
  "artifact_name": "example_1.2.4_x86.fpk",
  "required": ["app/bin/program"],
  "metadata": {"source_revision": "application-supplied-fixed-revision"}
}
```

模板必须包含可渲染的 `manifest`、`app/` 以及 fnpack 对该应用要求的配置、生命周期脚本和图标。`APP_ID` 必须与 project 一致；manifest 的 appname/version/platform 必须与 payload 声明一致；项目声明 `version` 时，payload `VERSION` 必须与其精确相等。数据、脚本、动态程序和静态程序均可作为普通 payload 文件；ELF 架构和运行依赖检查由应用步骤负责。文件和模板不接受符号链接、特殊文件、特殊权限位、重复或覆盖目标。

所有应用的 FPK 发布信息由公共 `src/fnpackkit/publisher.py` 统一拥有：`maintainer`（开发者）和 `distributor`（发布者）固定为 `user-no-found`，`maintainer_url` 和 `distributor_url` 固定为 `https://github.com/user-no-found`。应用无需在模板或 project 配置中声明这四项；已有值也会被覆盖。公共封包在模板渲染及文件复制完成后、计算 staging 哈希前写入这些字段，并用既有实际归档校验确认最终包一致。重复字段或格式损坏会在改写前报错。应用的名称、版本、`source` 以及上游 LICENSE 内容仍由应用提供。

## Python 构建适配

复杂应用用 `"adapter": "scripts/build.py:plan"` 替代 `"payload"`。二者互斥。只有显式选中并发起构建后，工具才加载该应用的受信 Python 文件；adapter 是与构建用户同权的代码，不是安全沙箱。`plan(project)` 接受 BuildFlow `Project`，返回 `steps`、`payload` 及可选 `metadata`、`verify`、`artifacts`：

```python
from buildflow.contracts import Step
from fnpackkit.contracts import Payload, PayloadFile


def plan(project):
    def compile_application(ctx):
        ctx.runner.run(["make"], cwd=project.root)

    def payload(ctx):
        return Payload(
            template=project.root / "packaging/fnos",
            files=[PayloadFile(project.root / "build/program", "app/bin/program", 0o755)],
            values={"APP_ID": project.data["app_id"], "VERSION": project.data["version"], "PLATFORM": "x86"},
            artifact_name=f"{project.data['app_id']}_{project.data['version']}_x86.fpk",
        )

    return {"steps": [Step("编译", compile_application)], "payload": payload}
```

`payload(ctx)` 可返回一个 `Payload`、多个架构的 `list[Payload]`，或 manifest 的 `Path`。全部步骤共享 `ctx.project`、`ctx.work`、`ctx.output`、`ctx.runner` 和 `ctx.metadata`。应用负责单次解析上游修订，后续构建复用同一固定输入，并把来源、工具链和验证边界放入 metadata。应用薄入口与统一 CLI 均调用 `fnpackkit.api.build(project_path, *, steps=None, payload=None, verify=None, artifacts=None, fnpack="fnpack", metadata=None, ui=None, build_version=None, next_version=False, channel=None)`；显式传 steps/payload 可直接嵌入已有构建程序。低层 `fnpackkit.packaging.package(ctx, payload, fnpack="fnpack")` 返回 BuildFlow `Artifact` 列表。

应用需要在实际 FPK 上运行原生或浏览器验证时，提供 `verify(ctx, package_artifacts)`。它在所有包生成并通过共享归档检查后、发布前执行，收到本次构建的 `tuple[Artifact, ...]`，仅包含 FPK 和校验和文件；通过 `artifact.name.endswith(".fpk")` 选择实际包。成功返回 `None`，失败抛异常；包括 `False` 在内的其他返回值会被拒绝，不会误标成功。

`artifacts(ctx)` 可返回报告声明 `list[Artifact]`，例如本地化覆盖率和实际包验证报告。它仅在全部验证成功后求值；报告必须是本次 `ctx.work` 内的普通文件且声明名唯一。FnPackKit 将路径和 SHA-256 登记到 `ctx.metadata.application_reports`，报告留在工作区，不放入产物目录。执行顺序始终为应用步骤 → 共享 FPK 封包检查 → 可选应用实际包验证 → 一次发布。验证失败时保留本次诊断及上次成功产物，不更新 `last-success`。

FPK 成功前会检查实际外层 tar 和内层 app.tgz，拒绝路径穿越、重复条目、链接、特殊条目、权限异常及超出预算的归档。实际所有文件的内容、大小和权限与 staging 对比。仅允许 fnpack 的已验证转换：manifest 排版、app.tgz 的原生 MD5 checksum、缺省空 desktop 字段，以及两份相同内容的 `config/resource`、`config/privilege`。最终身份校验使用 SHA-256；原生 MD5 只用于核对 fnpack 格式字段。

成功输出直接位于应用 `output/`，只包含各版本的 FPK 和 `.fpk.sha256`，不创建 run 子目录。同名且内容不同的文件拒绝覆盖；内容相同可复用。每包 `.build.json` 留在 `.build/<run-id>/fpk/...`，精确路径登记为 `metadata.fpk_reports`；BuildFlow 总报告位于 `.build/<run-id>/report.json`。同一项目持有构建锁；所有步骤成功才原子更新 `.build/last-success.json`，其中 schema 2 包含 run_id、相对 `.build` 的 report 路径及本次产物名称/大小/SHA-256。失败日志留在本次 `.build/<run-id>/`，上一成功产物与指针保留。构建终端显示阶段及结果，完整子命令输出写入日志。默认不安装、升级或重启 NAS 服务。

## Go 运行机制

`runtime/go.mod` 的导入路径是 `github.com/user-no-found/FnPackKit/runtime`，目标为原生 Linux。应用最终二进制链接这些包，应用配置继续拥有运行设置及最终策略判断：

| 包 | 公共接口与边界 |
| --- | --- |
| `identity` | `Resolve(Request)` 双向核对 NSS UID/名称及当前附加组；`SystemAccount` 只解析服务账号；`ValidName` 排除多记录及选项语法。 |
| `peer` | `NewListener(net.Listener, []uint32, time.Duration)` 只接受 Linux Unix socket，基于 `SO_PEERCRED` 拒绝非受信 peer；`ConnContext` 只标记该 listener 真实接受的连接，供 HTTP server 使用；`CloseConnections` 关闭当前连接。 |
| `gatewaypolicy` | `New(Config)`、`Policy.Check` 统一请求来源、路径与 FN 身份头边界；`Audit`、`WriteRejection`、`WriteFailure` 输出受限诊断。 |
| `auth` | `Headers(http.Header)` 在已受信连接上严格解析单值 FN 身份头；`Check(ctx, Request, Authorizer)` 要求调用者提供当前应用授权策略，缺少策略拒绝。 |
| `securefs` | `RootPath`、`RootDirectory`、`UserDirectory` 核对根控制的路径及用户私有目录；`RestoreUserState` 在离线生命周期中恢复升级后变为 root 的用户状态。 |
| `process` | `Start` 管理实际子进程句柄及一次 Wait；`Child.Done/Alive/Terminate/Result` 管理生命周期；`Credential` 拒绝 root 和无效 UID/GID。 |

`auth.ValidateMode` 只接受显式 `per-user-process` 或 `trusted-proxy` 集成声明；它不替应用完成认证对接。任意 TCP 请求或客户端头不能直接调用 `Headers` 后获得可信身份。NSS 账号存在不等于 FN 账号已启用、应用权限有效或 UID 从未被重用；这些需调用者的权威授权来源。长连接寿命提供有界重验机会，不表示权限撤销立即生效。上游账号绑定、角色、Cookie、会话撤销、数据权限及业务验证由应用负责。未知认证模式明确拒绝，不能为任意 Web 应用宣称无适配免登。

`securefs.RestoreUserState(parent, RestoreOptions{ExcludedUIDs: serviceUIDs, MaxEntries: 100000, MaxDepth: 64})` 仅用于 Linux amd64/arm64 的 root 离线入口；应用必须先停止所有状态写入者并持有生命周期锁。父目录必须已存在、由 root 控制且不可被组或其他用户写入。工具只处理规范十进制 NAS UID 目录，通过当前 NSS 双向解析 UID/姓名及主组；排除的服务 UID 和非 UID 条目保持原样。预算必须显式传入，条目上限为 1000000、深度上限为 256。

恢复先完整检查所有候选树，再按子项到父目录的顺序重新核对 inode 并修改属主/属组，保持内容与权限位。顶层 UID 目录必须是 0700；只进入 root 拥有且不可被组或其他用户写入的目录，遇到目标用户已拥有的目录便跳过整个子树。目标用户已拥有的普通文件保持原样，后代符号链接及其目标不修改；顶层 UID 符号链接、root 文件硬链接、特殊节点、特殊权限位和外来属主导致失败。`openat2` 禁止符号链接祖先与所有挂载点穿越（包括同一设备的 bind mount）；内核不支持时失败关闭。当前 NSS 不能证明历史 UID 未被删除后重新分配，调用方仍需管理账户生命周期。

预检失败不改变任何属主；执行期间失败可能留下已经完成的子树，返回报告记录实际完成数。工具不回滚，重试会跳过已恢复的用户子树，避免进入用户可修改的目录执行特权操作。`UserDirectory` 的严格运行时检查保持不变；恢复不用于运行目录或任意文件的递归 `chown`。root 回归仅在隔离临时目录执行，需显式设置 `FNPACKKIT_ROOT_RESTORE_TEST=1`，可用 `FNPACKKIT_ROOT_RESTORE_UID` 指定已有 NSS 测试用户；普通非 root 测试会验证真实 fd 遍历边界，但不会执行属主修改。

共享包不提供可接收任意命令的 root daemon。应用的特权 broker 应维持窄协议，仅传递受验证的身份，使用 NSS 权限及安全目录构造非 root worker。应用专属工作进程顺序、状态保存和认证转接仍属于应用实现。

Python wheel 交付构建层；Go 核心是独立 Go 模块，不放入 Python site-packages。源码发行包（sdist）包含同一份权威 `runtime/` Go 源码、测试及根 `LICENSE`，没有手工维护的第二份运行核心。需要编译最终应用时，将 `FNPACKKIT_PATH` 指向完整源码 checkout 或解压后的 sdist 根目录；单独安装 wheel 不会自动提供 Go 源码。两种来源都满足 `FNPACKKIT_PATH/runtime/go.mod` 的应用构建契约。使用已有本地发行包时：

```sh
# 构建依赖只安装到自己的项目环境；BuildFlow wheel 同样来自已准备的发行包。
/path/to/project-venv/bin/python -m pip install --no-deps \
  /path/to/artifacts/buildflow-0.1.0-py3-none-any.whl \
  /path/to/artifacts/fnpackkit-0.1.0-py3-none-any.whl
mkdir -p /path/to/tool-sources
tar -xzf /path/to/artifacts/fnpackkit-0.1.0.tar.gz -C /path/to/tool-sources
export FNPACKKIT_PATH=/path/to/tool-sources/fnpackkit-0.1.0
/path/to/project-venv/bin/python -m fnpackkit --project /path/to/application/project.json
```

命令中的包版本应与选用的发行包一致。源码依赖当前也可直接使用已核对的 Git checkout；GitHub 来源在 Python 包依赖中声明，发布前的本地验证不意味着远程包已可下载。构建不会隐式 clone、拉取工具更新或自动安装系统环境。

应用临时 Go modfile 使用 `replace github.com/user-no-found/FnPackKit/runtime => $FNPACKKIT_PATH/runtime`，保留源码位置和输入指纹，避免把机器路径写入提交的 go.mod。Go 模块没有独立手写产品版本；应用来源记录应保留实际工具检出修订及工作树输入指纹。应用分发运行核心或最终链接程序时，应将源码包根 `LICENSE` 一并纳入应用许可证目录。


## 公共网关来源策略

`gatewaypolicy.Config` 包含 `Prefix`、`AllowedHosts`、`ExternalOriginPorts`、`AllowedOrigins`、`AllowSameOriginFetch` 和可选 `HandshakeGrants`。应用从可编辑运行配置一次加载并验证后传入 `New`；共享库不预置应用名、域名或 NAS 端口。现有服务修改运行配置后重启或按应用明确的重载机制生效。

HTTP server 必须使用经过信任配置的 `peer.Listener`，并设置 `server.ConnContext = trusted.ConnContext`。共享策略只接受该 listener 真正通过内核 Unix peer 检查的连接上下文；仅传一个 UID、伪造请求头或裸 TCP 都不能建立该标记。`Policy.Check` 先确认标记与唯一 FN UID/名称头，再检查 Host、应用前缀、Origin 和 Fetch Metadata。合法头不是最终应用授权；应用 broker 仍需核对 NSS 和权限。

启用 `AllowSameOriginFetch` 时，浏览器提供单值 `Sec-Fetch-Site: same-origin` 且 Origin 是合法完整 HTTP(S) origin，可用于恢复可信代理改写 Host/端口后的同源关系。浏览器脚本不能设置该请求头，但原始客户端可以，因此它不能独立替代可信连接与身份边界。`same-site`、`cross-site`、重复/合并/非法 Origin、`Origin: null` 始终拒绝；`none` 只允许无 Origin 的 GET/HEAD 导航，不能用于 WebSocket 或写操作。WebSocket 必须有 Origin，同源字体的 `cors` 模式正常接受。

缺少 Fetch Metadata 时保留严格规则：精确 Host authority，或同 hostname 且 Host 丢失端口时命中按 HTTP/HTTPS 配置的 `ExternalOriginPorts`，或完整精确匹配 `AllowedOrigins`。`AllowedOrigins` 不允许通配、路径、查询或用户信息；配置不会绕过身份、路径和跨站拒绝。`Forwarded`、`X-Forwarded-*` 不能扩大策略。

公网明文 HTTP 或某些代理可能不提供 Fetch Metadata；来源未知时仍安全拒绝，需根据实际外部 origin 显式配置，不能宣称自动兼容所有 relay。Origin/Fetch Metadata 都不能防止应用自身的同源 XSS。

真实浏览器的 WebSocket 握手可能不发送 Fetch Metadata。此时应用可启用 `HandshakeGrants: &GrantConfig{Lifetime, Capacity, PerUser}`：生命周期为 1..300 整秒、全局容量 1..8192、每 UID 配额为 1..Capacity，具体默认值由应用运行配置一次决定。`IssueGrant(request, targetPath)` 只对已经通过同一 peer、NAS 身份与 HTTP Origin/Fetch 策略的 POST 签发随机 256 bit 不透明一次性票据。`targetPath` 由应用选择并限制在规范前缀内；应用可固定为自己的 WS 路由，不必接受客户端指定路径。

应用的 POST `/_gateway/grant` 仅调用 `IssueGrant` 与 `WriteGrant`，返回不可缓存的有限 JSON，不设置跨域读取权限，不启动 worker。票据在本 policy 实例内存中绑定 UID、名称、外部 Origin、精确 WS 路径与过期时间；重启失效。同一 UID 超额只替换自己的最旧票据，全局满则拒绝；错误 UID/Origin/path、非 GET 和非法协议项不能消耗合法票据，匹配成功后加锁原子消费。代理内部 Host 可能分别改写 HTTP 与 Upgrade，因此它仅独立经过语法/白名单检查，不作为外部同源证明绑定。

`gateway-transport.js` 导出 `FnPackKitGatewayTransport.create(transportFactory, wsURL, {grantEndpoint, protocols?, timeoutMs?})`。factory 只需返回通用 `start(sink)`、`send(data)`、`close()` 对象及可选 `socket`，没有特定终端框架依赖。每个连接尝试创建新 wrapper，先发一次有界同源 POST，再把票据放入 `Sec-WebSocket-Protocol`，同时提供非秘密 marker `fnpackkit.gateway.v1`；不写 URL query、storage 或日志。取消中的请求不能晚建 socket，未连接时 send 明确失败。

策略在常规硬边界通过后核验所有显式票据，包括原本严格 Origin 已匹配的请求；无效票据不能退回放行路径。有效票据弥补 WS 缺少 Fetch Metadata 的同源证明，不绕过 `same-site/cross-site` 或非法 Origin 的拒绝。网关消费后剥除票据和 marker，仅把原有应用子协议转给后端。应用的 `ReverseProxy.ModifyResponse` 调用 `CompleteHandshake(response)`：有应用子协议时保持正常协商；原本没有应用子协议且后端未选择时只回固定 marker，绝不反射票据。缺少 Fetch Metadata 且没有精确来源配置的 HTTP mint 仍会拒绝，不为发票据放宽来源策略。

`Rejection.Code` 是固定原因。`Audit` 只输出 method、路由类别、host/origin 端口、origin scheme、authority 是否相同及有限 metadata，不包含域名、路径、query、Cookie、认证头、用户名或 UID。`WriteRejection` 生成 403 JSON；`WriteFailure` 允许 403/502/503。响应携 `X-FnPackKit-Rejection`、服务端随机的 `X-FnPackKit-Request-ID` 和 `no-store`，调用者可用返回的 ID 关联审计；不回显客户端 ID。

共享浏览器组件来源为 `FNPACKKIT_PATH/web/gateway-status.js`、`.css` 与 `gateway-transport.js`。应用构建从该目录复制并记录实际源码摘要；文件随 checkout/sdist 交付，与 `runtime/` 使用同一权威源码，wheel 继续只提供 Python 构建层。

应用提供同前缀 POST `/_gateway/status` 无副作用端点：走同一 policy/身份/Cookie 检查，通过后仅返回 `{"schema":1,"status":"ready"}`，不启动用户 worker。拒绝响应使用共享 JSON 协议。浏览器探测只能说明 HTTP 入口结果，不能把 `ready` 当成 WebSocket 已连通。

加载组件后调用 `FnPackKitConnectionStatus.create({endpoint, labels, container?, timeoutMs?, onRetry?})`；默认 timeout 为 5000 毫秒，可设置 250..30000。应用转接 `connecting()`、`connected()`、首个实际输出后的 `output()`、`failed()` 和销毁时的 `close()`；组件不接管 renderer、输入或业务会话。连接建立后仍显示等待首帧，实际输出才隐藏。可选重试按钮仅调用显式 `onRetry`，不自动重启会话。

可见文案由应用提供：必需字段由 JS 中唯一 `requiredLabels` 数组定义，拒绝原因由唯一 `reasons` 集合定义；应用可在构建时校验其翻译覆盖。`labels.reasons` 可映射固定原因，没有映射使用通用拒绝文案。每次连接失败最多发一次有超时的同源 POST，最多读取 4096 字节，只显示固定 schema/code、服务端 ID 和可验证 HTTP 状态；不显示上游 HTML、正文或 URL。旧探测结果不能覆盖已恢复的状态。

## 验证

```sh
PYTHONPATH=/path/to/FnPackKit/src:/path/to/BuildFlow/src \
  FNPACKKIT_E2E=1 python3 -m unittest discover -s tests -v
cd runtime && go test ./...
```

FPK E2E 使用真实 `fnpack create/build` 和临时泛化应用，保留打印出的 fixture 及产物路径；安全测试检查实际 Unix 凭据、当前非 root NSS 账号、非法身份、路径和归档。运行这些检查不需要创建 NAS 用户或修改已安装服务。真实 FN 入口、两不同用户的应用权限与数据隔离仍需在应用级和相应设备上验证。

共享浏览器回归使用已有 Playwright 与 Chromium，通过显式路径运行，不安装浏览器或新环境：

```sh
FNPACKKIT_PLAYWRIGHT=/path/to/playwright/index.mjs \
FNPACKKIT_CHROMIUM=/path/to/chrome node tests/browser_status.mjs
FNPACKKIT_PLAYWRIGHT=/path/to/playwright/index.mjs \
FNPACKKIT_CHROMIUM=/path/to/chrome node tests/browser_transport.mjs
```

测试打印保留的临时证据目录，状态测试输出截图和 JSON；transport 测试用真实 WebSocket 验证 marker、应用子协议、每次新票据及取消。共享 Go 安全回归通过真实 Unix HTTP/WS 边界检验票据绑定、期限、并发单次消费和配额。最终应用仍需在自己的真实代理链路验证桌面与移动端。
