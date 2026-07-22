# Changelog

本项目遵循 [Semantic Versioning](https://semver.org/)。

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
