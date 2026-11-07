# 按需使用 Docker

**非必要不使用 Docker。** 普通 Go、Python、Node.js 和其他可直接运行的服务优先采用原生 FPK 与所需的运行时依赖。只有业务已经容器化，或确有复杂依赖隔离需求时，才使用本页的可选共享能力。引入 FnPackKit 不会安装 Docker、创建容器，也不会把原生应用改成容器应用。

## 飞牛内置组件与应用依赖

fnOS 的内置 Docker 由系统服务和 Docker 管理界面提供，不是名为 `docker` 的应用中心包。不要填写 `install_dep_apps=docker`；这会让安装器查找不存在的应用依赖。通过真实 Docker CLI 检查 daemon、Compose 和所需 runtime。缺少组件时提供清晰提示，不自行替换 Docker、GPU 驱动或 NVIDIA Container Toolkit。

每个 Compose 项目只选择一个生命周期拥有者。静态 Docker 模板可声明 `config/resource` 的 `docker-project.projects`，交给平台管理。需要动态发现设备、生成私有环境或组合原生网关的应用，可由原生生命周期调用本页的共享 Go API，不同时声明平台托管的同一项目。平台可能在安装回调前解析 Docker resource，不能让这个阶段依赖回调后才存在的 `.env`。两种方式均由应用中心触发启停，但不能重复托管。

## 安装前检查

构建 adapter 将共享的独立 Python 检查程序放入包外层，并通过 package hook 调用；它不依赖尚未部署的应用，也不要求 NAS 安装 FnPackKit：

```python
from fnpackkit.contracts import LifecycleCommand
from fnpackkit.docker import preflight_file

# Include preflight_file() in Payload.files.
hooks["install_init"] = LifecycleCommand(
    "package", "cmd/hooks/fnpack_docker.py", ("--runtime", "nvidia"))
```

不需要特殊 runtime 时省略参数。检查只访问现有服务，失败写入生命周期诊断日志；它不下载镜像、不修改配置，也不启用系统服务。

## 原生生命周期调用

Go 导入 `github.com/user-no-found/FnPackKit/runtime/docker`。此包只供应用授权的本机生命周期使用，不能把 Docker socket、执行器或 Compose 配置权交给网页请求和业务容器。

- `Client.Check(ctx, runtime)`：检查 Compose、daemon 和可选 runtime。
- `Client.EnsureImage(ctx, digest)`：复用已有镜像；缺失时只拉取指定 SHA-256 摘要。
- `Project.Up(ctx)` / `Down(ctx, seconds)`：管理指定 Compose 项目；Down 保留数据卷。
- `Client.Running(ctx, Container)`：核对项目、服务及额外标签后返回运行状态。
- `Client.CheckVolume(ctx, Volume)`：允许新卷不存在，拒绝使用其他项目的数据。
- `Client.RemoveVolume(ctx, Volume)`：仅删除符合项目、卷键及全部额外标签的卷；缺失可重复执行。

`Client.Binary` 默认 `/usr/bin/docker`，显式值必须为规范绝对路径。命令使用参数数组、清理后的环境、输出上限及超时；不继承 `DOCKER_HOST`、NAS token 或调用方 Docker 环境。调用方显式设置拉取、启动、停止的 context deadline；未提供 deadline 的单次命令最多运行 30 秒。共享包不自动安装组件，不做系统清理或镜像删除。

`Project` 接收 `Name`、`ComposeFile` 和可选 `EnvFile`。名称只标识本应用，父目录必须由受信任的生命周期身份控制。每次执行前检查配置为当前执行身份拥有、单硬链接的普通文件且其他身份不可写；环境文件必须私有。root 生命周期因此只使用 root 管理的部署文件。若由已获 Docker 权限的普通身份调用，只能使用该身份控制的文件；这不是权限提升接口。

`Container` 的 `Name/Project/Service` 和 `Volume` 的 `Name/Project/Key` 必须明确。额外 `Labels` 由应用指定；平台 Compose 项目与服务/卷标签始终核对。配置、容器名称、镜像摘要、数据保留决定、GPU 选择及 HTTP 就绪条件继续属于应用，不能在共享包中固定某个产品。

## 启停与卸载

安装时检查并准备配置，应用启动时准备镜像、创建容器并确认业务就绪，之后再开放原生同源网关。网页、API 和 WebSocket 仍遵守公共访问契约，容器化不要求浏览器直连局域网端口。

升级和配置变更先停本应用。卸载默认保留数据，只有用户明确选择删除时才调用 `RemoveVolume`。共享包不会接管其他项目，也不会发现并删除开发验证容器。旧测试资源的退出由具体项目按已确认的范围管理。
