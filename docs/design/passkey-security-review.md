# Passkey 专项安全审查

日期：2026-10-04。范围：当前工作区 Passkey 登录、注册、管理、配置界面、会话、密码/TOTP 恢复、数据库导入及新增依赖。审查包含代码分析、故障注入、真实签名攻击测试、竞态检测和 Chromium 虚拟认证器验证。未执行生产环境渗透测试。

## 威胁与安全边界

考虑匿名攻击者、伪造代理头、挑战/授权重放、被盗浏览器 Cookie、已知旧密码、CLI 与 HTTP 并发恢复，以及数据库导入失败或进程中断。数据库管理员、操作系统管理员、被控制的同源脚本和设备私钥失窃超出本功能能单独防护的边界。

WebAuthn 验证对照 [W3C 注册流程](https://www.w3.org/TR/webauthn-3/#sctn-registering-a-new-credential) 和 [认证流程](https://www.w3.org/TR/webauthn-3/#sctn-verifying-assertion)，并核对本地 `go-webauthn v0.18.2` 的解析、验证和备份标志检查实现。签名验证使用库实现，项目负责账户归属、挑战生命周期、配置版本、会话撤销和管理授权。

## 本轮发现与修复

| 编号 / 级别 | 攻击条件与原有风险 | 修复与证据 |
| --- | --- | --- |
| S1 / 高 | 有有效 Cookie 和旧密码；CLI 恢复恰好发生在密码检查之后。管理授权重新加载账户，可能将旧验证结果绑定新 epoch；配置保存只检查配置版本，CLI 密码/Passkey 重置未必改变该版本 | 密码验证返回原账户快照；授权保存原 epoch。配置事务锁定账户并检查验证时 epoch。`TestPasskeyGrantCannotAdoptRecoveryEpoch`、`TestPasskeyConfigRejectsConcurrentRecovery` |
| S2 / 高 | 数据库恢复先发布备份数据，随后刷新认证状态。旧 Cookie 可能短暂匹配旧 epoch；刷新失败可能持续保留匹配状态 | 替换前创建独立、同步落盘的恢复记录，暂停新 HTTP 请求与浏览器/登录身份接受。刷新成功后解除；失败或中断在启动阶段恢复，失败时拒绝启动 HTTP。覆盖事务失败、进程内状态丢失、无效记录、策略保留、外来凭证清理、旧 Cookie 和两种登录方式拒绝 |
| S3 / 中 | 浏览器请求已通过 API 认证，缓慢提交通用设置正文；读取期间账户被撤销，请求随后仍可修改安全配置 | 设置正文绑定完成、取得认证锁后重新验证身份。密码修改补充空身份处理，避免撤销期间空指针。`TestGeneralSettingsRechecksRevokedSessionAfterBodyRead` |
| S4 / 中 | 匿名客户端缓慢提交密码登录正文；新增的认证读锁在读取正文之前取得，导致恢复、撤销及其他认证写操作等待网络输入 | 密码登录在完整绑定和基本校验后取得锁，锁内检查恢复暂停状态。`TestPasswordSlowBodyDoesNotBlockSecurityMutations` |

恢复记录为数据库路径附加 `.auth-restore`，保存本机 Passkey 部署策略和共享代理策略，不含设备私钥、凭证、密码或 TOTP。文件以 `0600` 创建并同步；非 Windows 平台同步目录。Windows 目录同步不受支持，本地未证明掉电场景。损坏记录会阻止启动，应修复原记录/数据库，不能直接删除保护记录。导入替换阶段中断时，恢复可能进一步撤销会话；保留本机设置的导入会清理凭证并要求重新绑定。

## 攻击与回归覆盖

| 边界 | 已有实现和本轮补充验证 |
| --- | --- |
| 账户归属 | RP + credential ID + user handle 共同定位账户；缺失或错误 user handle 拒绝，不回退到首个管理员 |
| 密码/TOTP 与管理入口 | 当前本地密码及已启用 TOTP；浏览器 Cookie 和独立 CSRF 必需；Bearer/mTLS 单独不能管理 Passkey；授权绑定目的、目标、用户 epoch、配置版本且单次使用 |
| 验证器输出 | 注册与登录要求 UP、UV；拒绝备份资格变化及 BS 无 BE；真实 P-256 签名负例验证，失败不落凭证、不创建会话 |
| 来源与传输 | 实际 HTTPS或受信代理的 HTTPS 终止；Host 精确匹配配置；签名内 origin 还须匹配本次挑战的来源，其他已配置端口也不能替换；跨源请求拒绝 |
| 重放与浏览器绑定 | 挑战 120 秒、授权 5 分钟，服务端原子消费；不同浏览器不能借用或消耗合法挑战；已有并发消费、过期及重放检查 |
| 凭证与撤销 | 最大 10 个/账户，数据库唯一索引；凭证更新以原数据做 CAS；单设备计数回退拒绝，备份凭证的风险信号记录日志；密码/凭证恢复检查失效 |
| 资源和泄露 | Passkey 正文 64 KiB；挑战全局/浏览器有上限，开始及失败限流；列表不含协议材料，调试 SQL 不打印凭证与稳定句柄；网络读取不持有认证锁 |
| 前端 | 复用 HTTP/CSRF 客户端、Zod 与查询缓存；设备操作取消和并发提交保护；不持久化密码/TOTP；使用虚拟认证器复测完整管理和登录流程 |

## 实测结果

- 基于 main (`8f00e780`) 的独立 Passkey 分支重新验证；`go test ./... -shuffle=on -count=1`：48 个有测试的包通过，日志 `.cache/passkey-main-go-test.log`。
- 认证相关 5 个包的定向 `go test -race` 通过，日志 `.cache/security-race-final.log`。
- `go build ./...`、`go vet ./...` 通过；Go 文件格式/导入与差异检查按仓库规范校验。
- 前端全量 181 个测试文件、1800 个测试通过，日志 `.cache/passkey-main-frontend-test.log`；lint、类型、格式与构建通过。Chromium 虚拟 CTAP2 认证器完成配置、注册、独立登录、改名、撤销及会话失效，日志 `.cache/passkey-main-browser.log`。
- Go 官方 `govulncheck v1.8.0` 在当前 Windows/CGO 环境扫描：实际调用 0 个漏洞，导入包 0 个漏洞。模块级提示 [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932)，涉及 `x/crypto/openpgp`；项目未导入该包，未发现可达调用。完整记录 `.cache/security-govulncheck-verbose.log`。工具覆盖范围参考 [Go 官方说明](https://go.dev/doc/security/vuln/)。
- `npm audit --omit=dev`：生产依赖 0 个公告漏洞，记录 `.cache/security-npm-audit.json`。上游 [go-webauthn 公告页](https://github.com/go-webauthn/webauthn/security/advisories) 在审查时未列出公开公告；该结果不证明不存在未知漏洞。

## 上线前仍需验证

1. 真实 Windows Hello、Apple 平台及安全密钥的 UV、注册/取消、跨设备登录与撤销行为。
2. 生产 HTTPS 与反代拓扑：可信代理地址、转发头覆盖、Host/端口、Cookie 属性、CSRF以及客户端 IP 限流。
3. PostgreSQL 的并发事务、CLI 恢复、三种导入路径及失败/重启恢复。SQLite 事务和本地故障注入不替代 PostgreSQL 验收。
4. Linux/CGO 的 `govulncheck` 和完整 CI lint。本地尝试 Linux 扫描：禁用 CGO 缺少 SQLite API，启用 CGO 的 Windows 编译器缺少 Linux 头文件，未完成该平台调用图扫描。未安装的 `golangci-lint` 门禁仍需 CI。

当前设计保留密码登录与恢复渠道，且 `attestation=none` 接受同步凭证；认证成功不等于设备硬件可信性证明。完整恢复旧备份可能重新带回后来撤销的凭证，这是备份恢复的已记录边界；必要时本地重置 Passkey。挑战、认证锁和恢复暂停状态针对单面板实例，不支持多实例共享数据库同时提供认证。已有请求/长连接不由新 HTTP 暂停中间件整体排空；认证完成路径和敏感修改另有版本与会话检查。

Passkey 默认关闭。本轮确认的问题已修复并通过本地验证；生产启用前应完成以上环境验收。
