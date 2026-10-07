# Passkey 代码审查与清理

日期：2026-10-04。范围：本次 Passkey 新增代码及其触及的密码修改、会话、配置保存、数据库导入、CLI 恢复、前端和接口契约。

依据 `CONTRIBUTING.md`、`docs/architecture.md` 和已确认的 Passkey 需求方案，对发现的问题直接整改。

后续高风险认证专项审查及恢复保护补强见 [Passkey 安全审查](passkey-security-review.md)。

## 已修复的问题

| 等级 | 问题 | 修复与验证 |
| --- | --- | --- |
| P1 | Passkey 请求读取网络正文时持有全局认证锁，慢请求会阻塞其他认证操作 | 在加锁前完成 64 KiB 有界读取，加锁后重新检查会话；覆盖慢正文、超限和读取期间撤销会话 |
| P1 | CLI 重置密码可能用旧对象覆盖较新的会话版本；页面修改密码后重新查询可能采用另一轮密码修改的版本 | 数据库原子递增版本；服务在事务内返回本次修改的用户快照，控制器据此签发会话；覆盖竞争更新和过期版本拒绝 |
| P1 | 数据库恢复后，Xray 重启失败会跳过认证失效和凭据归属处理 | 将处理移到运行时迁移和重启之前；本机 Passkey 配置、共享代理策略、凭据清理及版本刷新使用同一事务，包含 SQLite 到 PostgreSQL 导入路径；覆盖失败回滚和空代理策略保留 |
| P2 | 调试 SQL 日志可能输出凭据公钥材料及稳定用户句柄 | Passkey 数据访问使用独立静默 GORM 会话，列表只读取展示字段；覆盖调试日志不包含凭据材料和句柄 |
| P2 | 前端手写响应类型和断言、手动维护服务端状态，偏离 Zod / TanStack Query 约定 | 将 Go 契约纳入生成器；新增字段及 WebAuthn 响应 schema、共享 HTTP 接口层和查询/失效钩子；保留草稿原始版本以防后台刷新覆盖并发配置 |
| P2 | 控制器承担数据库和 WebAuthn 验证逻辑，配置与凭据界面混在单个组件中 | 将凭据验证、挑战存储和认证生命周期逻辑归入 service；独立配置表单、凭据列表和操作编排；规范导入、状态常量和注释长度 |

## 最终职责

- `internal/web/controller/passkey.go`：HTTP 绑定、浏览器会话 / CSRF、请求来源、限流和响应。
- `internal/web/service/panel/passkey*.go`：WebAuthn 验证、挑战生命周期、凭据事务和归属。
- `internal/web/service/authentication.go`：会话撤销、TOTP 恢复和导入后的认证状态处理。
- `frontend/src/schemas/passkey.ts`：基于生成 Go 契约的 Zod schema。
- `frontend/src/api/passkey.ts`、`api/queries/usePasskeys.ts`：共享 HTTP 客户端、响应校验、缓存及失效。
- `PasskeyConfigForm.tsx`、`PasskeyCredentials.tsx`、`PasskeySection.tsx`：配置草稿、凭据展示及敏感操作编排。
- `frontend/src/utils/passkey.ts`：纯浏览器二进制协议转换。

## 本地验证

- 基于 main (`8f00e780`) 的独立 Passkey 分支重新验证：Go 全量随机顺序测试 48 个有测试的包通过。
- `go vet ./...`、本次 Go 文件的 gofmt / goimports 校验通过。
- 前端全量：181 个测试文件、1800 个测试通过；lint、类型检查、全部 725 个源文件的格式检查及生产构建通过。
- 生成文件重新生成后哈希一致；`git diff --check` 和管理脚本 Bash 语法检查通过。
- Chromium 虚拟认证器连接真实控制器、HTTPS 和临时 SQLite，完成配置确认取消 / 保存、注册、独立登录、改名、撤销和会话失效。中文、英文桌面及移动端截图已检查。

`golangci-lint` 在本地未安装，完整 Go lint 门禁仍由仓库 CI 执行。真实 Windows Hello / Apple / 安全密钥、生产反代和 PostgreSQL 验收仍需对应环境；虚拟认证器测试不替代这些验证。
