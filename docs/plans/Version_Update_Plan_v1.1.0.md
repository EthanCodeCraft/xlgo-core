# xlgo v1.1.0 实施计划 — HA & Manager 化

> 本文件为 v1.1.0 版本的逐项实施计划，对应体检报告（`docs/plans/Version_v1.0.2_report.md`）第三~四章 #10-#24 架构/HA 改进。
> 范围：13 项（#20 RedisRateLimiter、#23 Recover 带 request_id 已实现，本次核对配合）。

## Context

v1.0.3（bug fix）与 v1.0.4（DX & Docs）已发布推送，GitHub Release 已创建，工作区干净。v1.0.2 体检报告规划的 v1.1.0 阶段共 13 项架构/HA 改进，目标是把 xlgo 从"一半组件可注入、一半是单例"的撕裂状态，贯彻到"通用 / 高可用 / 易上手"。

本次一次性推完 13 项，发 v1.1.0。已确认的边界决策：

- **版本策略**：v1.1.0 接受少量 breaking（#11 删 wire 包、#14 删 `AppConfig.TokenExpire`、删 `StartServerWithPort`/`GracefulShutdown`、`JWTConfig.Expire` 类型变更），在 CHANGELOG「升级说明」章节列出；其余（#15 response）用全局开关默认兼容存量。
- **#10 Manager 化**：storage/cache/redis/jwt/logger **5 个全做**（含 logger），照 `database.Manager` + 包级 facade 蓝本。
- **#15 response**：全局 `SetMode(ModeBusiness|ModeREST)`，默认 `ModeBusiness`（现状全 200 + 业务码）兼容；`ModeREST` 下 401/404/500 返回对应 HTTP status，body 仍带业务码。可在 `ServerConfig.ResponseMode` 配置。
- **#11 wire**：删掉整个 `wire` 包（其事 App Option 已覆盖）。
- **#18**：接受 `prometheus/client_golang` 新依赖。
- **#12**：只提供 `WithHook(Hook{...})` 机制，不引入 etcd 依赖、不提供内置示例。

## 实现方案（按依赖顺序分 6 组）

### 组 1：配置层（其余项的基础）

**#13 Server 参数配置化** — `config/config.go:84-88`
`ServerConfig` 增字段：`ReadTimeout/WriteTimeout/IdleTimeout/ShutdownTimeout/MaxHeaderBytes`、`TLS{Enabled,CertFile,KeyFile}`、`UnixSocket string`、`ResponseMode string`。`app.go:408-414` 的 `http.Server` 改为读这些字段（缺省回退到当前硬编码值）。TLS 开启时用 `ListenAndServeTLS`；`UnixSocket` 非空时优先 unix socket。Shutdown 超时读 `ShutdownTimeout`。

**#14 JWTConfig.Expire time.Duration + 删 TokenExpire** — `config/config.go:44, 209-213`
`JWTConfig.Expire` 改 `time.Duration`（mapstructure + viper string 解析 `"24h"`），新增 `RefreshExpire time.Duration`、`Issuer`、`Algorithm`。**删 `AppConfig.TokenExpire`**（breaking）。`jwt/jwt.go` 内过期取值改读 `JWTConfig.Expire`。Duration 解析依赖 mapstructure decode hook。

**#16 Config Validate** — 新增 `config/config.go` `(*Config).Validate() error`
校验：`Server.Port` 范围、`JWT.Secret` 非空且 ≥32 字符（启用 jwt 时）、启用 mysql 时关键字段、Duration 非负等。`config.Manager.Load`（`config/manager.go:340`）解析后自动调用，错误包裹返回，把"运行时第一次请求才暴露"提前到启动。

**#15 response Mode 开关** — `response/response.go`
新增 `type Mode int`、`ModeBusiness/ModeREST`、包级 `currentMode` + `SetMode(m)`、`Mode()`。`Fail/Unauthorized/NotFound/ServerError/RateLimit/FailWithCode` 内：`ModeREST` 时按错误码/错误类型映射 HTTP status（`ErrUnauthorized→401`、`ErrNotFound→404`、`ErrServer→500`、`ErrRateLimit→429`、参数类→400），`c.JSON(status, Response{...})`；`ModeBusiness` 维持 200。`App.Init` 末尾按 `ServerConfig.ResponseMode` 调 `response.SetMode`。新增 `response.Custom(c, httpStatus, code, data)` 显式 API。

### 组 2：组件 Manager 化（#10，5 组件，照 database.Manager 蓝本）

蓝本参考 `database/manager.go:180-192`（`DefaultManager` 包级实例 + `SetDefaultManager` + 包级 facade 代理 + 实例方法）。每个组件统一模式：
- 新增 `type Manager struct{ ...; mu sync.Mutex }` + `var DefaultXxx = &Manager{}`
- `SetDefaultManager(m)` 提升用户实例到全局
- 包级 `Init/Get/操作` 函数代理到 `DefaultXxx`（**保留，兼容存量**）
- `App` 持有各 Manager 实例（`App` 加字段），`WithXxx` 时初始化 App 自己的实例并 `SetDefaultXxx`

顺序（按依赖）：**redis → cache → jwt → storage → logger**

1. **redis** — `database/redis.go`：新增 `type RedisManager struct{ cfg; client *redis.Client; mu }` + `DefaultRedis`。`InitRedis/CloseRedis/GetRedis/HealthCheckRedis` 代理。下游 5 处直接读 `database.RedisClient`（`jwt/jwt.go`、`middleware/ratelimit.go`、`cache/cache.go`、`cache/lock.go` 20+ 处、`app.go`）全部改为 `database.GetRedis()`。`cache/lock.go` 改为接受 `*redis.Client` 参数或内部 `GetRedis()`。

2. **cache** — `cache/cache.go`：`type CacheManager struct{ client *redis.Client; svc CacheService; mu }` + `DefaultCache`。`redisCache.client` 从硬编码 `database.RedisClient` 改为构造时注入。`Init/GetCache` 代理。`cache/lock.go` 的分布式锁函数改走 `DefaultCache` 或显式传 client。

3. **jwt** — `jwt/jwt.go`：`type Manager struct{ blacklist *TokenBlacklist; cfg *config.JWTConfig; mu }` + `DefaultJWT`。`TokenBlacklist.Add/IsBlacklisted` 内部 `database.RedisClient` 改 `database.GetRedis()`。`GenerateToken/ParseToken/InvalidateToken/RefreshToken/...` 代理到 `DefaultJWT`。

4. **storage** — `storage/storage.go`：`type Manager struct{ cfg; current Storage; mu }` + `DefaultStorage`。`Init/GetStorage/SetStorage/Upload/...` 代理到 `DefaultStorage.current`。最干净，无外部下游。

5. **logger** — `logger/logger.go`：`type Manager struct{ cfg; logger/apiLog/dbLog *zap.Logger; fileWriters; mu }` + `DefaultLogger`。包级 `Init/Sync/Close/Info/.../APILog/DBLog` 代理。**特殊**：logger 是 `App.Init` 最先初始化的组件，`DefaultLogger` 初始化前包级函数须安全降级到 Nop（现状 `Close()` 已重置为 Nop，沿用）。下游 8 包的 `logger.Info(...)` 调用点无需改（仍走包级 facade）。

`App` struct（`app.go:41-62`）加字段：`redisMgr *database.RedisManager`、`cacheMgr *cache.CacheManager`、`jwtMgr *jwt.Manager`、`storageMgr *storage.Manager`、`loggerMgr *logger.Manager`。

### 组 3：App 生命周期

**#12 Lifecycle Hooks** — `app.go`
新增 `type Hook struct{ Name string; OnInit func(*App) error; OnStart func(*App) error; OnReady func(*App); OnStop func(*App) error }` + `WithHook(Hook) Option`。`App` 加 `hooks []Hook`。`Init()` 内组件初始化完成后按序调 `OnInit`；`StartServer` 监听前调 `OnStart`，端口就绪后调 `OnReady`；`Shutdown` 开头调 `OnStop`。各 hook 错误中断流程并返回。

**#22 App.Go + in-flight goroutine** — `app.go`
`App` 加 `wg sync.WaitGroup` + `ctx context.Context`（根 ctx）+ `cancel`。新增 `App.Go(fn func(ctx context.Context))`：`wg.Add(1); go func(){ defer wg.Done(); fn(ctx) }()`。`Shutdown` 在 `OnStop` 后、关 HTTP 前调 `cancel()` 并 `wg.Wait()`（带 `ShutdownTimeout` 超时）。

**#11 删 wire 包** — 删 `wire/wire.go`（整包）。清理 `app.go` 的 `WithWire/WithoutWire/enableWire` 及 `Init()` 中 `wire.InitServices()` 调用。`cache.Init()` 原由 wire 触发，改由 `WithRedis`/`WithCache` 触发（或 `App.Init` 显式调）。

**清理双轨** — `app.go:494-537`：删 `StartServerWithPort`、`GracefulShutdown`（与 `App.StartServer`/`App.Shutdown` 重复）。breaking，升级说明列出。

### 组 4：中间件与路由

**#24 RequestID 默认装入** — `app.go:~348`
`App.Init` 中间件链改为无条件 `a.router.Use(middleware.RequestID())`（在 Recovery 之前），让每个响应/panic 日志都带 request_id。核对 #23：`middleware/recover.go:20` 已 `GetRequestID(c)`，配合 #24 后 panic 日志 request_id 非空。移除 `gin.Recovery()` 双重保险（保留 `middleware.Recover()` 一个即可）。

**#19 请求级 Timeout 中间件** — 新增 `middleware/timeout.go`
`func Timeout(d time.Duration) gin.HandlerFunc`：`ctx, cancel := context.WithTimeout(c.Request.Context(), d); defer cancel(); c.Request = c.Request.WithContext(ctx); c.Next()`。可通过 `WithRequestTimeout(d)` Option 装入全局，下游 GORM/Redis 走 `c.Request.Context()` 级联取消。

**#18 Prometheus metrics** — 新依赖 `prometheus/client_golang`
新增 `middleware/metrics.go`：`middleware.Metrics()` 记录 HTTP latency / status code / in-flight（histogram + counter + gauge）。新增 `router/metrics.go`：`RegisterMetricsRoute(r, path...)` 默认 `/metrics` 挂 `promhttp.Handler()`。新增 `WithMetricsRoute(path...)` Option。`App.Init` 装入 `Metrics()` 中间件 + 路由。

**#17 livez/readyz** — `router/router.go`
新增 `RegisterLivenessRoute(r)` → `GET /livez` 永不依赖外部（进程存活即 200）。新增 `RegisterReadinessRoute(r, checks...)` → `GET /readyz` 复用 `HealthCheck`，失败 503。新增 `WithLivenessRoute()`/`WithReadinessRoute()` Option。`/health` 保留兼容。`livez` 不查依赖、`readyz` 查依赖，对 K8s probe 友好。

### 组 5：依赖健康自愈（#21）

**#21 主库探活 + replica 健康剔除** — `database/manager.go`
- `database.Manager` 加 `healthy bool` + `consecutiveFailures int` + `healthMu`。`Pool.SetConnMaxIdleTime` 配置化（`DatabaseConfig` 加 `ConnMaxIdleTime`）。
- 探活：`App.Init` 末尾用 `App.Go` 起后台 goroutine，每 30s `HealthCheck(ctx)`，连续 N 次失败标记 `healthy=false`；`readyz`/`/health` 读此标记返回 503。
- `ReplicaPicker` 接口加健康度：replica ping 失败剔除轮询，恢复后重新纳入。`RoundRobinPicker`/`RandomPicker` 实现 health-aware 选取。

### 组 6：收尾与发版

- `app.go:27` `const Version = "1.1.0"`。
- `CHANGELOG.md` 加 `[1.1.0]` 章节（Added/Changed/Fixed + **升级说明** breaking 列表）。README 更新日志 + 底部链接。
- `examples/`（full/minimal）同步：full 例子的配置文件加 `server.read_timeout` 等新字段、`jwt.expire: 24h`，验证 5 组件 Manager 化后仍可跑。
- 测试：`go test ./...` 全绿；为 Manager 化、Validate、response Mode、livez/readyz、metrics、timeout、App.Go 补单测。
- `go mod tidy`。

## 关键文件清单

| 文件 | 涉及 issue |
|---|---|
| `app.go` | #10 App 字段、#12 hooks、#13 server、#19/#18/#17/#24 路由中间件、#21 探活、#22 App.Go、#11 删 wire、删双轨、Version |
| `config/config.go` | #13 ServerConfig、#14 JWTConfig+删TokenExpire、#16 Validate |
| `config/manager.go` | #16 Load 调 Validate、Duration decode hook |
| `response/response.go` | #15 Mode 开关 + status 映射 |
| `database/redis.go` | #10 redis Manager |
| `database/manager.go` | #21 探活 + replica 健康剔除 |
| `cache/cache.go` `cache/lock.go` | #10 cache Manager + 解耦 RedisClient |
| `jwt/jwt.go` | #10 jwt Manager + #14 Expire |
| `storage/storage.go` | #10 storage Manager |
| `logger/logger.go` | #10 logger Manager |
| `middleware/{requestid,recover}.go` | #24 装入 + #23 核对 |
| `middleware/timeout.go`(新) | #19 |
| `middleware/metrics.go`(新) | #18 |
| `router/router.go` `router/metrics.go`(新) | #17 livez/readyz + #18 /metrics |
| `wire/wire.go` | #11 删除 |
| `go.mod` | #18 prometheus 依赖 |
| `CHANGELOG.md` `README.md` | 发版 |

## 验证

1. `go mod tidy && go build ./...` — 编译通过。
2. `go test ./...` — 全绿，含新增单测。
3. `go run ./example`（full 栈）— 启动无错，`/livez`→200、`/readyz`→200（依赖在时）、`/metrics`→prometheus 文本、`/health` 兼容；配置错误时 `Validate` 在启动期拦截。
4. 手动验证 breaking：删 wire 后 `example` 不再 import wire；`AppConfig.TokenExpire` 删除后 grep 无残留；`response.SetMode(ModeREST)` 下 `Unauthorized` 返回 401。
5. `App.Go` 起一个长任务 goroutine，发 SIGTERM，确认 `Shutdown` 等其退出（日志可见）。
6. 主库探活：手动关 mysql，30s 后 `/readyz`→503、`/health`→503；恢复后自动转 200。
7. 发版：commit + annotated tag `v1.1.0` → `git push xlgo-core main && git push xlgo-core v1.1.0`；release 内容写本地 `gitHub_release_v1.1.0.md`（.gitignore 忽略），用户网页创建。
