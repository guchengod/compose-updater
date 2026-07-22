# Contributing

感谢你改进 compose-updater。

## 开发环境

- Go 1.26 或更高版本
- Docker Engine
- Docker Compose v2

## 提交流程

1. Fork 仓库并从 `main` 创建分支。
2. 修改代码并为行为变化补充测试。
3. 运行：

   ```bash
   gofmt -w .
   go test ./...
   go test -race ./...
   go vet ./...
   ```

4. 确保没有提交 `config.json`、`.env`、Registry 凭据或通知密钥。
5. 提交 Pull Request，说明问题、实现方式、兼容性影响和验证结果。

## 设计原则

- SQLite 或额外服务不是运行依赖；保持单二进制部署。
- 依据 Docker/Registry 的真实状态做判断，不使用日志文本猜测更新结果。
- 更新失败必须保留可恢复路径，并在结果中暴露原始错误。
- 新配置字段应保持向后兼容，缺省值必须安全且有测试。
- 不自动更新未运行服务，也不自动处理本地 `build:` 服务。

## 报告安全问题

安全问题不要创建公开 Issue，请按照 [SECURITY.md](SECURITY.md) 使用 GitHub 私密漏洞报告。
