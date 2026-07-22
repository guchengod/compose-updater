# Changelog

本项目遵循 [Semantic Versioning](https://semver.org/)。

## [v0.6.1] - 2026-07-22

- 收窄运行记录栏，为运行摘要和执行时间线保留更多可视空间。
- 飞牛目录选择器中的“我的文件”和“团队文件”各只保留一个入口。
- 新增 `web` 命令，让原生二进制和 Docker 部署复用飞牛运行中心前端、运行记录、立即运行和配置 API。
- 非本机 Web 监听强制要求 HTTP Basic 登录，避免把 Docker 控制与配置能力直接暴露到网络。

## [v0.6.0] - 2026-07-22

- 飞牛页面重构为运行中心，新增真实运行历史、项目状态、执行时间线、汇总指标和可展开技术详情。
- 新增“立即运行”和“重新运行本次”，由管理进程安全重启调度服务并强制执行一轮更新检查；任务运行中会阻止重复触发。
- 运行事件持久化到应用数据目录，升级或管理进程重启后仍可查看最近 30 次结果。
- 目录选择器改为飞牛双栏文件选择样式，左侧展示授权目录/存储空间，右侧支持逐层浏览、选中和双击进入。
- Registry 代理测试将 Docker Registry 的 `401 Unauthorized` 识别为“代理连通且 Registry 要求认证”的预期响应。
- 页面导航和状态图标统一使用 Tabler Icons，现有 CLI、Docker 镜像和跨平台二进制打包方式保持不变。

## [v0.5.1] - 2026-07-22

- 飞牛配置页新增 NAS 服务端目录选择器，扫描目录和跳过目录均可浏览选择。
- 修复飞牛统一网关通过 Unix Socket 转发时，保存配置被错误拒绝为跨站请求的问题。
- 修复飞牛桌面入口和窗口标题显示为 `{display_name}` 的问题。

## [v0.5.0] - 2026-07-22

- 新增飞牛 fnOS x86/ARM FPK，包含桌面入口、统一网关和独立生命周期脚本。
- 新增管理员配置页面，覆盖扫描范围、更新策略、Registry 代理和 Bark 通知配置。
- 配置保存前严格校验并原子写入，成功后自动重启更新进程；Bark Device Key 仅可写、不回显。
- 新增独立 FPK 构建与发布工作流，不改变现有 Docker、CLI 和跨平台 Release 构建。

## [v0.4.1] - 2026-07-22

- 新增 `skip_dirs` 配置；扫描命中指定绝对目录时直接剪枝，不受 `depth` 影响。

## [v0.4.0] - 2026-07-22

首次公开发布：

- 扫描并更新正在运行的 Docker Compose 项目。
- 支持稳定版本、最新发布标签、固定 SHA 和 `latest` 镜像策略。
- 根据 Compose 镜像引用查询 Docker Hub、GHCR 或私有 Registry。
- 支持 Registry 鉴权、程序侧查询代理和 Docker Engine 拉取检查。
- 原子回写 Compose、备份、失败恢复及 running/healthy 稳定性验证。
- Bark 可用更新、成功和失败通知。
- 提供六个平台原生二进制和双架构 GHCR 镜像。

[v0.4.1]: https://github.com/guchengod/compose-updater/compare/v0.4.0...v0.4.1
[v0.4.0]: https://github.com/guchengod/compose-updater/releases/tag/v0.4.0
[v0.5.0]: https://github.com/guchengod/compose-updater/compare/v0.4.1...v0.5.0
[v0.5.1]: https://github.com/guchengod/compose-updater/compare/v0.5.0...v0.5.1
[v0.6.0]: https://github.com/guchengod/compose-updater/compare/v0.5.1...v0.6.0
[v0.6.1]: https://github.com/guchengod/compose-updater/compare/v0.6.0...v0.6.1
