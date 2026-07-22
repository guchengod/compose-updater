# 飞牛 fnOS 安装与使用

Compose Updater 提供独立的飞牛 FPK。FPK 内置原生更新程序、后台管理服务、统一网关入口和配置页面，不依赖本项目的 Docker 镜像；原有 Docker、CLI 和跨平台二进制部署方式保持不变。

## 1. 安装前准备

需要满足：

- fnOS 已安装并可以正常使用 Docker；
- `docker compose version` 在 NAS 上执行成功；
- 需要更新的 Compose 项目使用绝对目录，并允许 Compose Updater 写入 Compose 文件；
- NAS 可以访问目标镜像仓库，或已经准备 HTTP/HTTPS/SOCKS5 代理。

> [!IMPORTANT]
> FPK 以 `root` 身份运行。原因是它需要访问 Docker Socket、读取容器状态、拉取镜像、重建容器，并在固定标签更新时原子改写 Compose 文件。只应从本项目 GitHub Releases 下载 FPK，安装前建议核对 `SHA256SUMS-fnOS`。

## 2. 选择安装包

在飞牛终端执行：

```bash
uname -m
```

根据结果下载对应的 Release 附件：

| `uname -m` | FPK |
|---|---|
| `x86_64` / `amd64` | `ComposeUpdater-vX.Y.Z-x86.fpk` |
| `aarch64` / `arm64` | `ComposeUpdater-vX.Y.Z-arm.fpk` |

从 [GitHub Releases](https://github.com/guchengod/compose-updater/releases/latest) 下载 FPK 和校验文件，然后校验：

```bash
sha256sum -c SHA256SUMS-fnOS
```

## 3. 安装 FPK

可以在飞牛应用中心使用“手动安装”选择下载的 `.fpk` 文件。开发或终端环境也可使用官方工具：

```bash
appcenter-cli install-fpk /path/to/ComposeUpdater-vX.Y.Z-x86.fpk
```

安装完成后，飞牛桌面会出现 **Compose Updater** 入口。只有飞牛管理员可以打开配置 API；页面通过飞牛统一网关和 Unix Socket 访问后台，不额外暴露 TCP 端口。

## 4. 首次配置

首次安装生成一份安全配置：扫描路径和跳过路径都指向应用自身，因此在管理员明确添加目录前不会更新其他项目。

打开桌面入口后配置：

1. 在“扫描范围”添加 Compose 项目的绝对父目录，例如 `/vol1/docker`。
2. 在“跳过目录”加入不希望进入的目录，尤其是 Compose Updater 自身、数据库数据目录和备份目录。
3. 设置扫描层级。`0` 只检查根目录，最大为 `5`；跳过目录不受层级影响，会剪掉整个子树。
4. 设置五段 Cron、时区和是否启动后立即检查。
5. 选择“仅更新稳定版”。开启是默认策略；关闭后跟随仓库能够确定的最新发布标签。
6. 如果 NAS 访问 Registry 需要代理，填写 `registry_proxy`。它只影响程序查询标签和摘要；`docker compose pull` 使用 Docker 守护进程自己的代理。
7. 按需启用 Bark。Device Key 是只写字段：页面不会回显，留空保存会保留原值，也可以明确勾选清除。
8. 点击“保存并重启”。配置先经过严格校验和原子写入，成功后后台更新进程立即重启。

页面覆盖配置文件的全部公开字段：

| 页面字段 | JSON 字段 | 说明 |
|---|---|---|
| 扫描目录 | `paths` | 至少一个存在且可访问的绝对目录 |
| 跳过目录 | `skip_dirs` | 命中目录及其所有后代均跳过 |
| 扫描层级 | `depth` | `0`–`5` |
| Cron | `schedule` | 标准五段表达式 |
| 时区 | `timezone` | IANA 时区，例如 `Asia/Shanghai` |
| 启动后立即检查 | `run_on_start` | 应用启动后执行一次完整更新 |
| 仅更新稳定版 | `stable_only` | 默认 `true` |
| Registry 代理 | `registry_proxy` | HTTP、HTTPS、SOCKS5 或 SOCKS5H |
| Bark | `bark.*` | Endpoint、Device Key、环境变量名和分组 |

## 5. 更新行为

- 程序只处理已经运行、声明 `image:` 且不包含 `build:` 的服务；停止的项目不会被自动启动。
- Registry 由 Compose 中的镜像地址决定：Docker Hub、GHCR、Harbor 或私有 Registry 都会查询各自的实际仓库。
- 固定版本和 SHA 标签会查询目标版本；发生变化时先备份并改写 Compose，再拉取并重建。
- `latest` 不改写 Compose，但每次会拉取并比较本地运行镜像 ID；摘要变化才重建。
- 更新成功、发现可用更新以及中间错误都会按 Bark 配置通知。
- 拉取、重建或健康检查失败时，程序恢复 Compose 文件并尝试恢复旧服务。

## 6. 日志与故障排查

管理服务日志位于：

```text
/var/apps/ComposeUpdater/var/fnos-manager.log
```

配置文件位于：

```text
/var/apps/ComposeUpdater/etc/config.json
```

常用检查：

```bash
docker version
docker compose version
tail -n 200 /var/apps/ComposeUpdater/var/fnos-manager.log
```

如果 Registry 查询成功但 `docker compose pull` 超时，需要配置 Docker 守护进程代理；页面中的 Registry 代理不会改变 Docker Engine 的网络设置。

如果页面提示扫描目录不可访问，确认路径真实存在，并确认填写的是 NAS 上的绝对路径，而不是容器内部路径。

## 7. 升级与卸载

安装更高版本 FPK 会保留 `/var/apps/ComposeUpdater/etc/config.json`。升级回调只在配置不存在时创建默认文件。卸载前请自行备份需要保留的配置；卸载应用不会回滚已经完成的镜像更新或 Compose 文件变更。

## 8. 从源码构建 FPK

安装飞牛官方 `fnpack` 1.2.3 和 Go 1.26，然后执行：

```bash
FNPACK=/usr/local/bin/fnpack make fnos-build VERSION=v0.5.0
```

产物：

```text
build/fnos/ComposeUpdater-v0.5.0-x86.fpk
build/fnos/ComposeUpdater-v0.5.0-arm.fpk
```

构建脚本分别交叉编译 Linux amd64/arm64 的 `compose-updater` 和 `fnos-manager`，再调用官方 `fnpack` 打包。FPK 构建目录位于 `fnos/`，不会改变现有 `make build`、`make build-all` 或 `docker build` 行为。
