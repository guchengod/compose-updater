# Security Policy

## 支持范围

安全修复优先应用于最新稳定 Release。旧版本建议先升级到最新稳定版本后再验证问题。

## 私密报告

请使用仓库 Security 页面中的 **Report a vulnerability** 提交私密报告，不要在公开 Issue 中披露漏洞、凭据、主机路径或可利用细节。

报告请包含：

- 受影响版本和运行平台；
- 最小复现步骤；
- 潜在影响；
- 已知缓解方式；
- 如适用，提供修复建议。

维护者确认问题后会通过 GitHub Security Advisory 协调修复和披露。

## 部署安全提示

compose-updater 需要访问 Docker Socket，这等同于宿主机级 Docker 控制权限。请仅使用可信镜像、只挂载需要扫描的目录，并保护 `config.json`、Docker Registry 凭据及 Bark Device Key。
