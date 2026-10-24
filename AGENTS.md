# FnPackKit 接入与维护入口

FnPackKit 提供 FPK 构建层及供应用链接的 Go 运行机制。先阅读 [README](README.md)，再按需求选择下面的公共接口；接入说明和源码均随完整源码交付，不需要聊天记录或本机任务文件。

## 选择接入方式

| 应用需求 | 入口 |
| --- | --- |
| 常规原生应用随 FPK 发布 | 保留应用原有生产步骤，通过 `fnpackkit.api.build` 与 payload/adapter 构建；使用 README 的完整流水线契约。 |
| 原生应用需要悬浮后端启停面板 | 使用 `runtime/backend` 与 `runtime/runtimepanel`，阅读 [运行面板](docs/runtime-panel.md)。 |
| 前后端在 NAS 本机独立部署，频繁修改且不希望每次重打 FPK | 显式接入 `runtime/webbridge`，阅读 [独立 Web 服务接入](docs/web-bridge.md)，复用同一个原生启停面板。 |

`webbridge` 是可选接入方式，不应把所有原生 FPK 都改成外部服务，也不应因前端有 Web 页面就默认采用它。需要的程序在自身适配层调用 `webbridge.New`；应用自己的身份、配置、二进制和数据不放进 FnPackKit。

## 独立 Web 接入约束

- 应用实现 `webbridge.Application` 的 `Authorize`、`Acquire`、`Running`、`Start` 和 `Stop`。`Authorize` 及控制方法须独立于可停止的业务服务；`Running` 不启动工作，`Stop` 必须完成实际服务退出和必要保存。
- 应用继续拥有可编辑运行配置、版本、授权来源、进程管理与数据策略。共享库接收解析后的配置及验证过的端点，不再创建并行配置或版本源。
- 服务使用 NAS 本机回环地址或受保护的 Unix Socket。需要向 TCP 后端传递 NAS 身份时，配置桥接认证并让后端实际验证它；不能直接信任浏览器提供的身份头。
- 飞牛统一网关入口、Unix Socket、可信 peer 和应用前缀保持一致。`peer.Listener`、`ConnContext`、来源策略及应用权限校验不能省略；不能用裸 TCP 请求伪造网关信任。
- 普通页面、API 与长连接都经过同一个暂停门。控制路由先于业务获取处理，手动停止后刷新、轮询和重连不应再启动后端；停止失败不能报告成功。
- 控制按已认证用户隔离。多用户应用管理各自实例；共享单一服务的接入应限制为明确授权的单一所有者，不能承诺仅停止当前用户却停止所有人的服务。
- 前端 base、路由、Origin 和 HMR 路径由应用按公开前缀配置。普通 WS 无须开启票据；只有配置握手票据时才同时声明 `HandshakeGrants` 和 `WebSocketPaths`。
- HTML 注入保留原页面和 CSP，复用共享面板。业务代码独立更新，修改桥接二进制、内嵌面板、图标或平台权限声明仍涉及 FPK 更新。

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
