# Changelog

xlgo 框架更新日志。本文档遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 规范，
版本号遵循 [语义化版本 SemVer](https://semver.org/lang/zh-CN/)。

> **如何阅读**：每个版本下分类列出变更类型——
> - **Breaking**：⚠️ 破坏性变更，升级前必须阅读迁移说明
> - **Added**：新增功能
> - **Changed**：变更已有功能（非破坏性）
> - **Deprecated**：标记为废弃，未来版本会移除
> - **Removed**：移除的功能
> - **Fixed**：Bug 修复
> - **Security**：安全相关修复

---

## [1.0.3] - 2026-06-22

> 本版本定位为 **bug fix release**：收口 v1.0.2 引入的破坏性清理，并修复 4 个轻量 bug + 依赖复查。

### Removed 🗑️

#### ⚠️ Breaking — 清理 v1.0.2 兼容别名（database 包）

xlgo 仍是早期框架，本次彻底移除 v1.0.2 临时保留的兼容别名，避免长期累积技术债。

**移除内容**：

- `database.InitMySQL(cfg)` 包级函数
- `database.InitMySQLWithReplicas(cfg, replicas)` 包级函数
- `(*Manager).InitMySQL(cfg)` 实例方法
- `(*Manager).InitMySQLWithReplicas(cfg, replicas)` 实例方法
- `database.driverName(driver)` 内部辅助（已被 `driverDescription` 替代）

**迁移指南**：

```go
// ❌ 旧
database.InitMySQL(cfg)
database.InitMySQLWithReplicas(cfg, replicas)

// ✅ 新（驱动由 cfg.Database.Driver 决定，可以是 mysql / postgres / 自定义注册的方言）
database.InitDB(cfg)
database.InitDBWithReplicas(cfg, replicas)
```

**为什么现在动手**：

- xlgo 还在小范围使用，破坏式调整成本最低
- "默认开启可插拔方言"已经是 v1.0.2 的正式 API，再叫 `InitMySQL` 名实不符
- 早期保留别名 → 长期变成永久负担的反面教材太多，与其在 v1.0.4 / v1.1 删，不如现在删

#### 删除死代码 `database.DBResolver`

`database.DBResolver` 类型与其 `BeforeQuery` 方法**从未被注册**到 GORM callback chain（既没有 `db.Callback().Query().Before(...)` 的调用，也没有任何 plugin 包装），属于纯死代码。文档暗示的"自动读写分离"实际上从未生效——读写分离一直依赖业务侧显式调用 `database.UseMaster(ctx)` / `database.UseReplica(ctx)`。

**移除内容**：

- `database.DBResolver` 类型
- `(*DBResolver).BeforeQuery` 方法

**对用户影响**：

- 几乎无影响。该类型从未在框架内部被使用，也未被文档推荐为 public API
- 若你的代码 `database.DBResolver{}` 出现编译错误，说明你曾尝试将其注册到 GORM callback；这种用法并不能让"读路由从库"自动生效，请改用：

  ```go
  // 强制主库（事务、写后立刻读）
  ctx := database.UseMaster(c.Request.Context())
  user, err := repo.FindByID(ctx, id)

  // 显式读从库（报表、统计）
  ctx := database.UseReplica(c.Request.Context())
  list, err := repo.FindAll(ctx)
  ```

未来若需要"基于 callback 的自动路由"，建议直接接入官方 [`gorm.io/plugin/dbresolver`](https://github.com/go-gorm/dbresolver)，它有完整的权重 / policy / 健康摘除支持，比自造轮子更稳。

### Changed

#### 文件重命名：`database/mysql.go → database/manager.go`

文件内容自 v1.0.2 引入可插拔方言注册表后，已经与 MySQL 解耦——本版本同时清理了 `InitMySQL` / `InitMySQLWithReplicas` / `driverName` 兼容别名（详见下方 Removed 段），文件中已经全部是通用代码（`Manager`、`ReplicaPicker`、`Init/Close/HealthCheck`、`UseMaster/UseReplica` 等）。继续叫 `mysql.go` 误导新用户认为框架仅支持 MySQL。

**对用户影响**：

- **导入路径无变化**：`github.com/EthanCodeCraft/xlgo-core/database` 不变，所有公开 API 都还在
- 只有直接 `git grep mysql.go` 或在 issue / PR review 里提到该文件的内部协作会感知

测试文件同步重命名为 `database/manager_test.go`。

### Added ✨

#### console 包：显式 level 控制

为 `console` 包补齐显式级别屏蔽能力，让用户在 main 中**显式**控制何时收紧调试输出，避免上线前到处屏蔽 `console.Debug` / `console.Info` 调用。

**API 增量**：

- `console.LevelSilent` — 完全静默
- `console.WithLevel(l Level)` — 构造时设置级别
- `(*Console).SetLevel(l)` / `(*Console).Level()` — 实例方法
- `console.SetLevel(l)` / `console.GetLevel()` — 包级 API（操作 Default 实例）
- `(Level).String()` — 可读名称

**典型用法**：

```go
func main() {
    cfg, _ := config.Load("./config.yaml")

    // 显式收紧：生产期只保留 Warn / Error
    if cfg.IsProduction() {
        console.SetLevel(console.LevelWarn)
    }
    // 或完全静默：console.SetLevel(console.LevelSilent)

    app := xlgo.New(...)
    app.Run()
}
```

**设计立场**：

- console 包**不会**根据 `app.env` 自动切级别——选择权完全在调用方，避免"dev 看到的 / prod 看到的"行为不一致
- console 仍然是**纯彩色 stdout 工具**，不写文件、不感知环境、跟 `fmt.Println` 同级
- 业务可观测信息（用户登录、订单事件、审计日志等"上线必须保留的"）请使用 `logger` 包；console 只用于开发期肉眼调试
- 完整对比表见 [GUIDE.md §3.3](./GUIDE.md#33-彩色控制台输出)

并发安全：level 通过 `atomic.Int32` 存取，运行期热切换无锁。

### Changed

#### console.WithCaller 签名收敛

`WithCaller(show bool, skip int)` 改为 `WithCaller(show bool, skip ...int)`——`skip` 99% 用户用不到，强制传是 API 噪音。无 breaking：旧调用 `WithCaller(true, 2)` 仍然合法。

### Fixed 🐛

#### Logger Tee 重复写入修复（logger 包）

修复 `logger.Init` 把 `apiCore` 与 `dbCore` 都 Tee 进通用 `Logger`，导致**每条 `logger.Info(...)` 同时落到 `api.log` + `database.log` + console 三份**的 bug。`APILog()` / `DBLog()` 的"分流"在旧实现中形同虚设，且日志体积凭空翻倍。

**修复内容**：

1. **三个 logger 各自独立**：
   - `Logger`（通用）→ `logs/app.log` + console
   - `APILog()`     → `logs/api.log` + console
   - `DBLog()`      → `logs/database.log` + console
   - 互不 Tee，互不串扰
2. **新增 `logger.Close()`**：关闭文件句柄并把全局 logger 重置为 Nop。`App.Shutdown` 已自动调用。
3. **Init 健壮性**：`Init(nil)` 不再 panic 改为返回 error；构造失败时不会留下半初始化状态（旧实现 mkdir 之后任意一步失败都会半切换全局变量）。
4. **`Sync()` 全覆盖**：旧实现只 sync `Logger`，apiLog / dbLog 缓冲不会落盘；新实现 sync 全部三个 logger，并识别忽略 stdout/stderr 平台相关的预期错误。
5. **生产默认级别从 `Warn` 调整为 `Info`**：原默认在生产丢失大量业务信息，多数项目反而需要在配置里覆盖回 Info；新默认更符合直觉。Debug 级别仍仅在开发模式生效。

**新增文件输出**：

启动后日志目录会出现一个新文件 `logs/app.log`（之前所有通用日志都被串写进 `api.log` / `database.log`）。如果你的运维脚本配置了**只**采集 `api.log` / `database.log`，请补上 `app.log`。

**新增测试覆盖**（`logger/logger_test.go`）：
- `TestLoggerNoCrossWriting` — 三个 logger 互不串扰（这是核心修复的回归测试）
- `TestLoggerInitNilConfig`  — `Init(nil)` 返回 error
- `TestLoggerSyncBeforeInit` — 未初始化时 `Sync()` 安全返回 nil

#### JWT JTI 生成忽略 `rand.Read` 错误（jwt 包）

`generateJTI()` 调用 `crypto/rand.Read` 却丢弃返回的 error，且函数签名只返回 `string`，无法把失败传播给调用方。一旦 `rand.Read` 失败（极罕见，但理论上可能），会基于全零字节生成 JTI，所有 token 的 JTI 完全相同，黑名单机制失效。

**修复**：`generateJTI()` 改为 `(string, error)`，`GenerateToken` / `GenerateTokenWithCustomExpiry` 传播该错误。

#### `QueryBuilder.Page` 统计行数被残留 Limit 截断（repository 包）

`Page()` 用 `qb.db.Session(&gorm.Session{})` 复制查询做 Count，但未清除残留的 `Limit`/`Offset`。若调用方先 `.Limit(n).Offset(m)` 再 `.Page(...)`，Count 会被包成 `SELECT count(*) FROM (... LIMIT n)` 子查询，返回的 `total` 被截断为 ≤ n，分页总数错误。

**修复**：countDB 增加 `.Limit(-1).Offset(-1)`（GORM 官方惯用法，表示移除该条件）。新增 DryRun 模式回归测试 `repository/page_internal_test.go`，校验 Count SQL 不含 `LIMIT`、Find SQL 仍含分页 `LIMIT`。

#### OSS / 本地存储文件名冲突（storage 包）

4 处上传路径（`LocalStorage.Upload` / `LocalStorage.UploadFromBytes` / `OSSStorage.Upload` / `OSSStorage.UploadFromBytes`）仅用 `time.Now().UnixNano()` 作为文件名。同一纳秒内的并发上传会生成相同 objectKey，后者覆盖前者。

**修复**：新增 `uniqueFilename(now, ext)` 辅助函数，格式 `<unixNano>-<8字节crypto/rand hex>.<ext>`，4 处统一改用。新增 `storage/unique_internal_test.go` 验证格式与 100 次近似唯一性。

#### 数据库重试策略对不可恢复错误无效（database 包）

`Manager.InitDB` 的重试循环对所有失败都退避重试 5 次。但认证失败（`Access denied`）、未知数据库（`Unknown database`）、非法 DSN（`invalid DSN`）、未注册驱动（`unknown driver` / `unsupported driver`）、不支持的认证插件（`authentication plugin`）属于配置类错误，重试无意义，反而把启动失败延迟 1+2+4+8+16=31 秒。

**修复**：新增 `isTransientDBError`，上述错误判为不可恢复，首次出现即直接返回。连接拒绝、I/O 超时等网络类错误仍正常重试。新增 `database/retry_internal_test.go` 用例表覆盖 8 种错误。

### Security 🔒

#### CORS 中间件修复（middleware/cors.go）

修复多个 CORS 安全与规范遵守问题。**这是行为变更**——升级后不正确的 CORS 配置会更严格，符合 W3C CORS 规范。

**修复内容**：

1. **`Access-Control-Allow-Credentials` 永远是 `true`** — 旧实现 `if/else` 两个分支都设了 `"true"`，相当于即使配置 `AllowCredentials=false` 也会发送凭证头。修复后**只在显式启用且 Origin 不是 `*` 时**才发送该头。
2. **`*` + `credentials: true` 的规范违规** — 旧实现配置 `AllowedOrigins=["*"]` 且 `AllowCredentials=true` 时会同时发送 `Allow-Origin: *` 与 `Allow-Credentials: true`，**浏览器会直接拒绝响应**。修复后此场景下回显具体 Origin（spec 允许的兼容做法）。
3. **缺失 `Vary: Origin`** — 当回显具体 Origin 时，下游 CDN / 网关必须按 Origin 区分缓存，否则可能把 A 用户的 CORS 响应缓存给 B 用户。修复后自动加 `Vary: Origin`。
4. **开发环境兜底改为回显具体 Origin** — 旧实现开发环境直接发 `*`，与 credentials 不兼容；新实现回显具体 Origin，开发环境也能正常调试带 Cookie 的请求。

**升级影响**：

- 如果你**没有**显式设置 `cors.allow_credentials`：响应将不再带 `Access-Control-Allow-Credentials: true`，前端如果依赖了 Cookie/Authorization，需要在配置里显式打开：

  ```yaml
  cors:
    allowed_origins: ["https://your-frontend.example"]
    allow_credentials: true   # 显式启用
  ```

- 如果你配置了 `allowed_origins: ["*"]` 且 `allow_credentials: true`：行为更安全（不再发 `*`），无需改动。
- 已经显式列出 origin 列表的配置：完全无影响。

**新增测试覆盖**（`middleware/middleware_test.go`）：
- `TestCORSAllowCredentialsDefault` — 默认不发凭证头
- `TestCORSAllowCredentialsExplicitOrigin` — 显式 origin + credentials 正常工作
- `TestCORSWildcardWithCredentials` — `*` + credentials 时回显具体 origin
- `TestCORSWildcardWithoutCredentials` — `*` 单独使用保持通配符语义
- `TestCORSOriginNotAllowed` — 非白名单 origin 不回显（防反射型 CORS 漏洞）

### Breaking ⚠️

#### 错误码体系重构（response 包）

修复 `CodeSuccess` 与 `CodeInvalidParams` 撞码的生产级 bug（两者都等于 `1`，导致业务错误响应被前端误判为成功）。

**数值变更**：

| 常量 | 旧值 | 新值 |
|---|---|---|
| `response.CodeSuccess` | `1` | **`0`** |
| `response.CodeFail` | `0` | **`1`** |

**移除**：

- `response.CodeInvalidParams`（与 `CodeSuccess` 撞码）
- `response.ErrInvalidParams`

**迁移指南**：

1. **前端代码**：`if (resp.code === 1) { /* 成功 */ }` → `if (resp.code === 0) { /* 成功 */ }`
2. **后端代码**：

   ```go
   // ❌ 编译失败
   response.FailWithError(c, response.ErrInvalidParams)

   // ✅ 推荐：业务侧自行定义参数错误码（不再由框架内置）
   var ErrInvalidParams = response.NewError(40001, "参数错误")
   response.FailWithError(c, ErrInvalidParams)

   // ✅ 或直接使用通用失败响应 + 自定义消息
   response.Fail(c, "用户名格式错误")
   ```

3. **手写常量比较**：`if resp.Code == 0 { /* fail */ }` → `if resp.Code == 1 { /* fail */ }`

**为什么**：

- 业内主流约定 `0 = success`（gRPC、HTTP-style 业务码、阿里云 / 腾讯云 OpenAPI 等），改回常规更利于对接
- 参数错误码各业务系统差异极大（有的用 `400`、有的用 `40001`、有的用 `1001`），框架不应内置
- 撞码不修是真实生产风险，必须破坏式修正

**新增编译期防撞码保护**：`response/error.go` 末尾新增 `_errorCodeUniquenessGuard` map，任何后续 `Code*` 常量重复都会在 `go build` 阶段直接报 `duplicate key in map literal`，杜绝再次撞码。新增 `Code*` 时**必须**登记到该 map。

### Dependencies 📦

#### `go mod tidy` 补全 postgres 方言间接依赖

v1.0.2 引入可插拔方言注册表后，`gorm.io/driver/postgres` 成为直接依赖，但其传递依赖（`jackc/pgpassfile` / `jackc/pgservicefile` / `jackc/pgx/v5` / `jackc/puddle/v2` / `golang.org/x/sync`）此前未在 `go.mod` 显式登记。`go mod tidy` 已补全，避免在干净环境构建时拉到不可预期的版本。

#### 安全相关补丁升级（仅补丁/小版本，无 API 变更）

| 依赖 | 旧 | 新 |
|---|---|---|
| `golang.org/x/crypto` | v0.49.0 | v0.53.0 |
| `github.com/golang-jwt/jwt/v5` | v5.2.1 | v5.3.1 |
| `github.com/gorilla/websocket` | v1.5.1 | v1.5.3 |

连同其传递依赖（`golang.org/x/net`、`x/sys`、`x/text`、`x/sync`、`x/tools`）一并升级。全量 `go test ./...` 与 `go vet ./...` 通过。

#### 暂缓升级（留待下一个小版本）

以下直接依赖存在可用更新，但跨越多个小版本或含破坏性变更，**不在本次 bugfix release 范围内**，留待 v1.0.4 / v1.1 专门评估：

- `github.com/gin-gonic/gin` v1.9.1 → v1.12.0
- `github.com/go-playground/validator/v10` v10.19.0 → v10.30.3
- `gorm.io/gorm` v1.25.10 → v1.31.1（及其 driver v1.5 → v1.6）
- `github.com/aliyun/aliyun-oss-go-sdk` v2.2.9 → v3.0.2（**major 版本，破坏性**，需迁移）
- `github.com/spf13/viper` v1.18.2、`go.opentelemetry.io/otel` v1.43.0、`go.uber.org/zap` v1.27.0、`github.com/fsnotify/fsnotify` v1.7.0 等

---

## [1.0.2] - 2026-06-20

> 详见 [README 更新日志](./README.md#更新日志) 中的 v1.0.2 章节，本节列出关键摘要。

### Added

- **数据库**：可插拔方言注册表（`database.RegisterDialect`），内置 `mysql` / `postgres`，支持任意 GORM 驱动
- **数据库**：实例化 `database.Manager`，`ReplicaPicker` 接口（`RoundRobinPicker` / `RandomPicker`）
- **配置**：实例化 `config.Manager`，`SetDefaultManager` 让 App 私有 manager 推为全局默认
- **App**：`WithFullStack` / `NewFullStack` / `RunFullStack` batteries-included 入口
- **App**：`Migrator` 类型与 `WithMigrator` / `WithModels`，迁移由用户显式注册
- **App**：组件 Option 全套（`WithLogger / WithMySQL / WithRedis / WithStorage / WithWire / WithHealthRoutes / WithSwaggerRoutes / WithDefaultRoutes / WithAutoMigrate` 及 `Without*` 对应项）
- **权限**：通用 `AuthUser`、`GetAuthUser`、`RequireUserTypes` / `RequireRoles` / `RequireAuth`
- **健康检查**：`/health` 支持注册 `HealthCheck`，失败返回 HTTP 503

### Changed (Breaking)

- **App**：`xlgo.New()` 默认不再初始化 MySQL / Redis / Storage，也不注册 `/health` 与 `/swagger/*`；需显式 `With*` 启用
- **权限**：`super_admin / admin / staff` 调整为默认常量而非固定业务模型
- **错误处理**：框架初始化失败一律 `return error`，不再 `Fatalf` 退出进程

### Fixed

- 修复 `WithConfigPath` 此前的空实现问题
- 修复读写分离场景下从库连接可能未关闭的问题（改为 `database.CloseAll()` + `errors.Join`）
- 修复此前 README 中错误的 v2.0.0 / v2.1.0 更新日志表述

---

## [1.0.1] - 2026-04-30

### Added

- 工具函数库、彩色控制台输出、压缩解压、RequestID、Recover 中间件
- 缓存键名前缀、分布式锁、计数器、Redis 分布式限流
- 增强 JWT 黑名单、Repository、CORS、日志中间件和优雅关闭能力
- 路由架构：模块化、版本化 API、中间件分组和 RESTful CRUD
- 配置热更新、数据库读写分离、CSRF、SSE、WebSocket、定时任务、CLI、测试工具、统一错误码

---

## [1.0.0] - 2024-04

### Added

- 初始版本发布
- 基础框架功能
- 完整示例代码

[Unreleased]: https://github.com/EthanCodeCraft/xlgo-core/compare/v1.0.3...HEAD
[1.0.3]: https://github.com/EthanCodeCraft/xlgo-core/releases/tag/v1.0.3
[1.0.2]: https://github.com/EthanCodeCraft/xlgo-core/releases/tag/v1.0.2
[1.0.1]: https://github.com/EthanCodeCraft/xlgo-core/releases/tag/v1.0.1
[1.0.0]: https://github.com/EthanCodeCraft/xlgo-core/releases/tag/v1.0.0
