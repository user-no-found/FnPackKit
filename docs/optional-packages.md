# 可选构建包

FnPackKit 可在应用原有生产步骤之前选择并冻结本地可选源码输入。只有应用的唯一 `project.json` 显式声明下列字段才启用；不声明的消费者保持原流水线，没有额外扫描、步骤或 metadata。

```json
"fnpackkit": {
  "schema": 1,
  "requires": ">=0.1,<0.2",
  "adapter": "scripts/build.py:plan",
  "optional_packages": {"schema": 1}
}
```

## 仓库与本地目录

目录固定为项目根 `optional/`，不能配置其他位置。每个包是用户自行 clone 的正规独立 Git checkout，命名为 `<主项目Git仓库名>Optional-<功能id>`。主项目仓库名来自本地 Git `origin` 的 basename，不来自 `package_name`、`display_name` 或 `app_id`。例如主 origin 为 `git@github.com:owner/FnDSH.git`，功能 `cyberstrike` 的目录与包 origin basename 都必须为 `FnDSHOptional-cyberstrike`。

主仓库、可选包及目标源码的 origin 均禁止 HTTP(S) URL userinfo、URL 密码、query 或 fragment；使用系统 Git 凭据机制，不能将 token 写进 URL。SSH 的 `git@host:owner/repo.git` 或 `ssh://git@host/owner/repo.git` 用户名合法。origin 在不记录原始配置输出的本地通道读取并验证后才可进入构建记录；错误不回显被拒绝的 URL。manifest 的 `source.origin` 采用同一规则。

主仓库应忽略 `/optional/`。包不能是 submodule、符号链接、worktree 或共享 objects/alternates 的 checkout；不进入主仓库提交。FnPackKit 不 clone、fetch、pull、更新包、创建远端或配置 submodule。主项目提供自己的上游源码和构建步骤，构建前选择只扫描本地声明，不获取上游。

包应建立为独立私密远端。建立与交付时由仓库管理者使用已认证的 `gh` 核对真实可见性，例如：

```sh
gh repo view owner/FnDSHOptional-cyberstrike --json nameWithOwner,visibility
```

结果必须为 `PRIVATE`；不得从 manifest 的声明推断私密性。本地目录和 Git origin 无法纯本地证明远端当前可见性，静态 scan 不访问网络、不调用 `gh`。构建通过也不等于完成远端隐私核验。

## 静态 manifest

只查看 `optional/` 的直接子目录及其 `fpk-option.json`，不递归发现嵌套包，不 import Python、不执行包内代码。缺少目录或没有候选时没有菜单。带 manifest 的无效仓库、坏 JSON、错误 id/命名、重复 id 或不安全输入明确失败，不悄悄忽略。

```json
{
  "schema": 1,
  "id": "cyberstrike",
  "label": "CyberStrike 会话宿主",
  "project": "FnDSH",
  "source": {
    "origin": "https://example.invalid/upstream.git",
    "revision": "0123456789abcdef0123456789abcdef01234567"
  },
  "patches": ["patches/host.patch"],
  "files": [
    {"source": "files/packages/host/new.ts", "destination": "packages/host/new.ts"},
    {"source": "tests/host.test.ts", "destination": "packages/host/host.test.ts", "mode": 420}
  ],
  "tests": ["tests/host.test.ts"]
}
```

- 必需字段为 `schema`、`id`、`label`、`project`、`patches`、`files`；`source`、`tests` 与 `shared_patch_targets` 可省略。拒绝未知字段、重复 JSON 键和任意脚本入口。
- `id` 是小写字母/数字组成、可用单个 `-` 分隔的功能标识；`label` 是非空单行文本（最多 120 字符）；`project` 精确等于主仓库 origin basename。
- `patches` 是按顺序执行的 UTF-8 Git unified text diff 路径。只接受普通文件的文本修改、新增、删除；拒绝二进制补丁、rename、带引号/空白的目标路径、symlink/submodule 模式及纯 mode-change patch。Git 实际解析出的所有路径也必须与预检路径一致。
- 所有 patches 完成后按声明顺序新增 `files`。`source` 相对包根，`destination` 相对目标源码根；不覆盖已有目标，也不覆盖 patch 触及路径。可选 `mode` 为 JSON 整数 `420`（0644，默认）或 `493`（0755）。
- `source` 是可选的精确约束，只允许 `origin`、`revision`；revision 必须是完整 Git commit。应用提供目标的真实 Git 身份，不能用显示版本替代。
- `tests` 只声明需要冻结的包内测试文件，可与 `files.source` 相同。共享工具不运行语言测试；应用步骤从冻结 manifest 的 source/destination 映射确定投影后的测试路径并执行。
- 所有输入和目标拒绝绝对路径、`..`、`.git`（忽略大小写）、反斜线、控制字符、链接、硬链接和特殊文件。manifest 上限 1 MiB，每项普通输入 16 MiB；直接子项和每类声明最多 4096 条，扫描输入总预算 128 MiB。

### 显式共享文本补丁目标

缺省仍拒绝不同选中包的 patch 触及同一目标文件。需要协作修改既有文本文件时，每个触及该目标的包都必须在自己的 manifest 中声明，例如：

```json
"shared_patch_targets": ["packages/host/existing.ts"]
```

该字段是最多 4096 项、不重复的规范相对路径数组，不能使用 `./`、重复斜线或尾斜线等别名。每个声明目标必须实际由该包的 patch 修改；悬空声明、仅通过 `files` 投影或无内容变化均拒绝。单独选择其中一个包也必须满足这些约束，不要求其它协作包同时入选。

只有所有触及者都显式声明，且每一步都是对已有普通 UTF-8 文本文件的修改，才允许共享。目标不能包含 NUL 二进制数据；新增、删除、rename、binary 或任何 mode-change 元数据都不能用于声明目标，文件模式必须保持原样。`files` 与 patch/其它 `files` 的投影碰撞、文件与目录路径冲突仍拒绝，声明不提供覆盖特权。

执行严格遵循冻结记录的 `selected` 包顺序及各包 `patches` 顺序，每个 patch 都在前一步结果上运行普通 `git apply --check` 和 `git apply`；不自动重排、合并，也不启用 force、fuzz 或 three-way。后续冲突使整个 selection 失败，不将前面成功的 patch 写入 target。

使用此字段的应用必须在唯一 `project.json` 中将 `fnpackkit.requires` 下限设为 `>=0.1.1`（例如 `>=0.1.1,<0.2`），并同步升级实际工具。旧工具会拒绝此新字段；不提供隐式降级或兼容分支。

## 选择与冻结

启用后，在版本确认之后、BuildFlow 构建标题及步骤之前扫描、选择并冻结；这不是生产 Step，不增加步骤计数。准备回调在同一项目锁内、日志/Context/取消处理就绪后执行，不在薄入口或导入 adapter 时运行。缺少目录、空目录或没有候选均直接进入原生产步骤，没有可选提示。

TTY 菜单经 `ctx.interaction()` 暂停动态输出，再通过共享前台终端写入同步显示候选名称、id 和默认全选的 `[x]`。回车、`all` 或 `*` 接受全部；逗号分隔的编号/id 替换默认集合；`0` 或 `none` 不安装；`q`/EOF 取消，Ctrl-C 沿用共享取消处理。功能列表在读取输入之前可见：

```text
构建版本 [0.0.0-18]（回车采用）:
可选功能（默认全部安装）：
  1. [x] CyberStrike 原生会话 (cyberstrike)
选择可选功能 [全部]（编号/id用逗号分隔；0不安装，q取消）:
已选可选功能: CyberStrike 原生会话
BuildFlow · 原生构建
```

非 TTY 默认选择全部本地候选，只显示候选与结果，不等待输入。`--features` 显式指定子集，`--no-features` 显式不选，两者均不再追问。应用薄入口原样透传这些共享参数：

```sh
./build.sh --features cyberstrike
./build.sh --features cyberstrike,other --features third
./build.sh --no-features
```

`--features` 与 `--no-features` 互斥。Python API 为 `build(..., features=["cyberstrike"], no_features=False)`；默认 `features=None`，显式不选用 `no_features=True`。请求不存在的 id 失败；未 opt-in 的项目不接受这些显式可选功能请求。

本次 `ctx.metadata["optional_packages"]` 与 `ctx.work / "optional-packages.json"` 保存同一 JSON 记录。`selection(ctx)` 返回已重核的独立副本：

```python
{
    "schema": 1,
    "project": "FnDSH",  # 无候选时为 None
    "selected": [{
        "id": "cyberstrike", "label": "CyberStrike 会话宿主",
        "path": "/absolute/project/optional/FnDSHOptional-cyberstrike",
        "origin": "git@github.com:owner/FnDSHOptional-cyberstrike.git",
        "revision": "uncommitted",  # 或完整 HEAD
        "manifest": {},  # 完整解析的 manifest
        "manifest_sha256": "...",
        "manifest_snapshot": "/absolute/work/optional-inputs/cyberstrike/manifest.json",
        "inputs": [{"path": "patches/host.patch", "sha256": "...", "snapshot": "/absolute/work/optional-inputs/cyberstrike/0"}]
    }],
    "fingerprint": "..."
}
```

所有选中包的 origin、HEAD、原始 manifest 字节、patch/files/tests 字节及 SHA-256 均冻结。尚无 commit 的开发包明确记录 `uncommitted`，不伪造版本；内容仍有完整 hash。快照路径属于本次 work，跨运行缓存比较应使用 `fingerprint`，不能用快照绝对路径或仅 HEAD 判断等价。`validate_selection(record)` 重新核对已选包的 Git 身份、原件和冻结副本，供应用发布前和 package-only 门禁使用；它只进行本地读取，不下载任何内容。共享 API 在封包前和应用验证/报告回调后的发布前自动重核，阻止构建期间变化的输入发布。未 opt-in 时 `selection(ctx)` 返回 `None`。

## 安全投影与应用职责

```python
from fnpackkit.optional_packages import apply_source, selection, validate_selection


def build_source(ctx):
    chosen = selection(ctx)
    # 应用自行准备无硬链接、位于本次 ctx.work 内的独立 Git checkout。
    target = ctx.work / "source"
    # 应用完成自己的 baseline 投影，固定 upstream 身份后：
    result = apply_source(ctx, target, source_origin=origin, source_revision=revision)
    validate_selection(chosen)
```

`apply_source(ctx, target, *, source_origin=None, source_revision=None)` 要求 target 为本次 `ctx.work` 内的独立私有 Git checkout，不能是 `ctx.work` 本身，也不能与包库重叠。允许应用自己的 baseline tracked/untracked 修改，不要求 `git status` 干净。可传入的身份必须与目标实际 origin/HEAD 一致；manifest 的 source 约束也核对该身份。

共享工具先核对完整选中输入和所有触及路径、链接、碰撞、Git 身份，再在本次 work 的隔离临时树中依序预检与应用整组 patches/files。全部成功后再次核对原输入、冻结输入和目标触及文件是否变化，最后只将已验证变化事务写入指定 target。每个文件先写入同目录临时普通文件、设置模式，再原子替换目标，不原地截断只读文件。普通执行异常恢复原文件字节/权限并移除新增文件、空目录；单项回滚或清理失败不会阻断其余恢复，错误同时保留原始原因与所有失败路径，明确报告仍需处理的内容。预检失败不改变 target。返回 `{"fingerprint": ..., "source": {"origin": ..., "revision": ...}, "paths": [...]}`。没有选中项时路径列表为空。

调用方独占该私有 stage，不能让另一个构建并发写入；该机制不是抵御同用户恶意并发进程的沙箱，也不提供断电/持久存储故障后的 crash recovery。包库、主项目上游缓存及 Git HEAD/index 不被投影修改。frontend、测试、部署及后续生产必须全部使用同一个 target；应用负责实际语言测试与包级验证，并将 selection 指纹及投影源码内容纳入生产证据，不能仅凭一个上游 HEAD 复用跨选项二进制。
