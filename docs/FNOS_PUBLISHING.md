# 飞牛应用中心发布材料

本文档用于维护 Compose Updater 的飞牛应用中心审核材料。所有兼容性和测试结论都必须来自真实环境，不以构建成功代替安装验证。

## 应用信息

| 字段 | 内容 |
|---|---|
| 应用名称 | Compose Updater |
| 应用标识 | `ComposeUpdater` |
| 应用类型 | 原生后台服务与统一网关管理页面 |
| 开发者/发布者 | `guchengod` |
| 项目主页 | <https://github.com/guchengod/compose-updater> |
| 使用教程 | <https://github.com/guchengod/compose-updater/blob/main/docs/FNOS.md> |
| 开源协议 | MIT |
| 最低 fnOS 版本 | `1.2.0203` |
| 支持架构 | x86_64、ARM64 |

应用简介：

> 自动发现并更新 fnOS 上正在运行的 Docker Compose 项目。支持 Docker Hub、GHCR 和私有镜像仓库，提供稳定版与 latest 更新策略、Registry 代理、Bark 通知、失败恢复，以及可视化运行记录和项目状态。

## 发布附件

每个正式版本应提交：

- `ComposeUpdater-vX.Y.Z-x86.fpk`
- `ComposeUpdater-vX.Y.Z-arm.fpk`
- `SHA256SUMS-fnOS`
- `ICON.PNG`（64 x 64，sRGB）
- `ICON_256.PNG`（256 x 256，sRGB）
- 真实 fnOS 环境截图
- 本文档中的测试结果和权限说明

FPK 和校验文件从对应的 [GitHub Release](https://github.com/guchengod/compose-updater/releases/latest) 获取，不重新打包或替换已经发布的版本附件。

## 权限与安全说明

FPK 以 `root` 身份运行，因为核心功能需要同时完成以下特权操作：

- 访问 `/var/run/docker.sock`，读取容器状态、拉取镜像并重建服务；
- 读取管理员明确选择的 Compose 项目目录；
- 固定标签发生变化时原子改写 Compose 文件并创建备份；
- 更新失败时恢复 Compose 文件并重新启动旧服务。

管理页面仅允许飞牛管理员访问，通过飞牛统一网关和应用 Unix Socket 通信，不开放额外 TCP 端口。外部目录由管理员在飞牛授权设置或应用目录选择器中明确选择；首次安装默认只扫描应用自身目录。Bark Device Key 不回显，配置原子写入，运行记录保存在应用私有数据目录。

## 正式测试矩阵

发布前在每个声明支持的架构上记录结果：

| 场景 | x86_64 / fnOS 1.2.0203 | ARM64 / fnOS 版本 |
|---|---|---|
| 全新安装并出现桌面入口 | 待验证 | 待验证 |
| 从上一版本升级并保留配置 | 待验证 | 待验证 |
| 打开运行中心和读取历史 | 待验证 | 待验证 |
| 选择扫描目录和跳过目录 | 待验证 | 待验证 |
| 保存配置并重启服务 | 待验证 | 待验证 |
| Registry 代理测试 | 待验证 | 待验证 |
| 立即运行并识别自定义 Compose 项目名 | 待验证 | 待验证 |
| `latest` 镜像摘要变化后重建 | 待验证 | 待验证 |
| 固定标签更新、Compose 回写与备份 | 待验证 | 待验证 |
| 拉取或健康检查失败后恢复 | 待验证 | 待验证 |
| Bark 成功与失败通知 | 待验证 | 待验证 |
| 停止、启动、卸载 | 待验证 | 待验证 |

如果没有真实 ARM64 fnOS 设备，不得将交叉编译或 QEMU 启动记录写成 ARM64 真机验证；应向飞牛审核人员明确说明并请求协助测试。

## 截图清单

截图必须来自与提交 FPK 相同的正式版本，页面顶部应显示版本号，不使用 `dev` 构建：

1. 飞牛桌面与 Compose Updater 图标。
2. 运行记录首页，包含成功运行摘要和执行时间线。
3. 项目状态页，包含最新、已更新和失败状态示例。
4. 飞牛风格目录选择器。
5. 扫描范围、更新策略与 Registry 配置页。
6. Bark 通知配置页，截图前隐藏 Device Key 和其他隐私数据。

截图不得包含手机号、Device Key、Cookie、访问令牌、内网外网域名或不需要公开的目录结构。

## 提交与后续更新

初次申请使用飞牛官网“应用中心 → 请求上架 → 立即申请”的官方表单。工作人员联系后，发送对应 GitHub Release、FPK、校验文件、图标、截图、测试矩阵和权限说明。

后续版本保持 `appname=ComposeUpdater` 不变，递增 SemVer 版本，更新 `CHANGELOG.md`，重新完成真实设备回归测试，再提交新 FPK；不要覆盖旧版本的已发布附件。
