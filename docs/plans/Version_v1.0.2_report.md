# xlgo v1.0.2 体检报告

> 资深 Go 视角的代码与架构 Review。目标是把 xlgo 从"业务沉淀工具集"打磨成"通用 / 高可用 / 易上手"的开源 Web 框架。
>
> 整体判断：v1.0.2 已经把"业务耦合 + 不可组合 + 框架内 Fatal"这三个最大的设计债还掉了，框架骨架是健康的。但仍有若干**真实 Bug**和**架构层面的债**，按下方优先级逐项核查即可。

---

## 一、必须立刻修的真实 Bug（带行号）

这些不是设计取舍，是确凿的缺陷，先于一切改进。

### 1. `response.CodeSuccess` 与 `CodeInvalidParams` 撞码 ⚠️

```go
// response/error.go:13-16
CodeSuccess       = 1   // 成功
CodeFail          = 0   // 通用失败
CodeInvalidParams = 1   // 参数错误   ← 跟 Success 同值！
```

只要任何业务调用 `response.FailWithError(c, response.ErrInvalidParams)`，前端拿到的 `code` 跟成功响应一模一样。这是**生产级 bug**。

**建议**：制定明确的码段策略——

- `0` = success（业内更通用），或者 `200`/`0` 二选一
- `1` 留给"通用失败"
- 参数错误使用 `40001` 等业务码段
- 同时为了避免后续重复，写一个 `init()` 自检：

```go
func init() {
    seen := map[int]string{}
    for code, name := range allErrorCodes {
        if old, ok := seen[code]; ok {
            panic(fmt.Sprintf("duplicate error code %d: %s vs %s", code, old, name))
        }
        seen[code] = name
    }
}
```

### 2. CORS 中 `Allow-Credentials` 永远是 `true` ⚠️

```go
// middleware/cors.go:86-91
if corsConfig != nil && corsConfig.AllowCredentials {
    c.Header("Access-Control-Allow-Credentials", "true")
} else {
    c.Header("Access-Control-Allow-Credentials", "true") // 默认允许 ← 错
}
```

并且当 `Origin: *` 时还会被浏览器拒绝。这是 CORS 经典坑。**修复**：

```go
if corsConfig.AllowCredentials && allowedOrigin != "*" {
    c.Header("Access-Control-Allow-Credentials", "true")
}
```

### 3. 日志在开发模式下写两份到同一文件链 ⚠️

```go
// logger/logger.go:95-99
core := zapcore.NewTee(apiCore, dbCore, consoleCore)
Logger = zap.New(core, ...)
```

`Logger` 是全局通用 logger，但 Tee 把 dbCore 也接进去——结果**所有 `logger.Info(...)` 都会同时写到 `api.log` 和 `database.log`**，再加一份控制台。`APILog()`/`DBLog()` 的"分流"形同虚设。

**修复**：通用 logger 只写 console + 一个 app.log；`APILog()`/`DBLog()` 各自独立 core，互不 Tee。

### 4. `DBResolver.BeforeQuery` 是死代码 ⚠️

```go
// database/mysql.go:386-408
func (r *DBResolver) BeforeQuery(db *gorm.DB) { ... }
```

这个 hook 从未通过 `db.Callback().Query().Before(...)` 注册过。所以**读写分离实际上需要业务侧自己调用 `GetDBFromContext(ctx)`**，但 README/GUIDE 暗示它会自动路由。要么把 hook 真正注册上，要么把这段代码删掉、文档明确"显式 `UseReplica/UseMaster`"。

我倾向**删掉**——GORM 官方有 `dbresolver` plugin，实现得更完整（权重、policy）。引入它比自造轮子更稳。

### 5. 重试策略对所有错误一视同仁

```go
// database/mysql.go:213-240
maxRetries := 5
// 不论是 driver 错、密码错、端口错都重试 5 次，指数退避
```

**密码错误**也会让进程在启动阶段死等 1 分钟才报错，体验很差。建议区分：

- `*mysql.MySQLError` Code 1045（access denied）/ 1049（unknown db）等 → 直接返回，不重试
- `net.OpError`、`io.EOF`、上下文未到等 → 重试

### 6. `generateJTI` 忽略 `rand.Read` 错误

```go
// jwt/jwt.go:40-44
func generateJTI() string {
    bytes := make([]byte, 16)
    rand.Read(bytes)  // ← 错误丢弃
    return base64.URLEncoding.EncodeToString(bytes)
}
```

`crypto/rand.Read` 在 Linux 早期启动或某些容器中**确实会失败**。失败时 JTI 会是全零，黑名单可能误判。改为返回 `(string, error)` 或在失败时 `panic` 都比静默吞错好。

### 7. `repository.QueryBuilder.Page` 的 Count 受残留 limit 影响

```go
// repository/repository.go:404-417
countDB := qb.db.Session(&gorm.Session{})  // ← 复用了已 Limit/Offset 的 db
if err := countDB.WithContext(ctx).Count(&total).Error; err != nil { ... }
```

如果用户先 `.Limit(10)` 再 `.Page(...)`，`countDB` 会带 LIMIT，Count 是错的。需要 `Limit(-1).Offset(-1).Order("")`：

```go
countDB := qb.db.Session(&gorm.Session{}).Limit(-1).Offset(-1).Order("")
```

### 8. `OSSStorage.Upload` 文件名冲突风险

```go
// storage/storage.go:205
objectKey := fmt.Sprintf("%s/%d%s", filepath.Join(...), now.UnixNano(), ext)
```

并发上传 / 容器集群同纳秒会产生**完全相同的 key**，OSS 会覆盖。补一个随机后缀或 uuid：

```go
objectKey := fmt.Sprintf("%s/%d-%s%s", dir, now.UnixNano(), randHex(8), ext)
```

### 9. `go.mod` indirect 里的可疑版本

```
google.golang.org/genproto/googleapis/api v0.0.0-20260401024825-9d38bb4040a9
```

这是 2026-04-01 的 pseudo-version，搭配 `golang.org/x/crypto v0.49.0`、`golang.org/x/net v0.52.0` 看起来正常，但需要确认这是 OTel 1.43 强制带来的传递依赖，还是历史 `go.sum` 没整理。建议跑一次 `go mod tidy && go mod verify` 之后人工 review。

---

## 二、架构层面的"债"（影响通用性与多实例化）

v1.0.2 把 config 和 database 改成了 Manager + 全局 facade 的双轨，但还有几个核心组件**没跟上这套抽象**：

### 10. Storage / Cache / Redis / JWT / Logger 仍是单例

| 组件 | 全局变量 | 多实例可能性 |
|---|---|---|
| `storage.storage` | 包级 var | 不能同时连 OSS + 本地 |
| `cache.globalCache` | 包级 var | 不能为不同业务设不同 prefix/TTL 默认值 |
| `database.RedisClient` | 包级 var | 不能多 Redis（缓存 + 队列 + 限流分库） |
| `jwt.tokenBlacklist` | 包级 var | 不能区分 user-token 和 refresh-token blacklist |
| `logger.Logger` | 包级 var | 不能区分多 app/多模块独立日志 |

**建议**：照 `database.Manager` + `database.DefaultManager` 的模式，每个组件提供 `XxxManager` 类型 + 全局便捷 facade，App 持有自己的 Manager 实例。这样：

- 单元测试可以注入 mock
- 多 App 共存（比如同进程跑 admin + api 两个 Engine）
- 微服务里组件解耦

优先级：**Redis Manager 最重要**（因为 JWT、Cache、RateLimiter、分布式锁都依赖它，目前全是访问 `database.RedisClient`，没法替换）。

### 11. `wire` 包名误导

```go
// wire/wire.go - 整个文件 32 行
func InitServices() { ... }
```

它叫 `wire`，但跟 Google Wire 没关系，也不是 DI 容器。新用户会困惑。建议二选一：

- **删掉**——其实现的事 App 通过 Option 已经做了
- **真正引入 Wire 或 fx/uber**——给一个最小 DI 范式

### 12. `App.Init()` / `App.Run()` 缺少 Lifecycle Hooks

现在的 App 内部是硬编码顺序：config → logger → mysql → redis → storage → wire → migrate → routes。如果用户想插入"Migrate 之前先初始化分布式锁，避免多副本同时迁移"或者"启动后注册到服务发现"，没有钩子。

**建议**（v1.1.0 路线）：

```go
type Hook struct {
    Name     string
    OnInit   func(*App) error  // Init 流程内
    OnStart  func(*App) error  // 监听端口前
    OnReady  func(*App)        // 端口就绪后
    OnStop   func(*App) error  // Shutdown 前
}

func WithHook(h Hook) Option
```

并提供两个内置示例：`hooks.RegisterEtcd(...)`、`hooks.RegisterDistributedMigrate(...)`。

### 13. Server 参数全部硬编码

```go
// app.go:400-406
ReadTimeout:  15 * time.Second,
WriteTimeout: 30 * time.Second,
IdleTimeout:  60 * time.Second,
```

加上 `Shutdown` 30s 超时。这些都该进 `ServerConfig`：

```yaml
server:
  port: 8080
  read_timeout: 15s
  write_timeout: 30s
  idle_timeout: 60s
  shutdown_timeout: 30s
  max_header_bytes: 1048576
  tls:
    enabled: false
    cert_file: ""
    key_file: ""
  unix_socket: ""        # 优先级高于 port
```

### 14. `JWTConfig.Expire` 与 `AppConfig.TokenExpire` 重复且单位不明

两个字段都是过期秒数，都没有 `time.Duration` 类型。Go 项目应优先用 `time.Duration` + viper 的 `string` 解析（`"24h"`、`"30m"`），**单位看就懂**：

```go
type JWTConfig struct {
    Secret        string        `mapstructure:"secret"`
    Expire        time.Duration `mapstructure:"expire"`         // "24h"
    RefreshExpire time.Duration `mapstructure:"refresh_expire"` // "168h"
    Issuer        string        `mapstructure:"issuer"`
    Algorithm     string        `mapstructure:"algorithm"`      // HS256/RS256
}
```

`AppConfig.TokenExpire` 直接删掉。

### 15. `response` 把 4xx/5xx 全压成 HTTP 200

```go
// response/response.go:32-39
func Success(c *gin.Context, data any) {
    c.JSON(http.StatusOK, ...)  // ← 永远 200
}
// Unauthorized / Fail / NotFound / ServerError 全部 200
```

这是国内典型"业务码 in body"的玩法，**对接 APM、Prometheus、APISIX/网关、Sentry 都很难受**——它们靠 HTTP status 区分异常。建议：

1. 默认仍保留业务码模式（兼容存量），但允许通过全局开关切到"REST 模式"：

```go
response.SetMode(response.ModeREST)  // 或在 ServerConfig 中
// 401 错误 → 返回 HTTP 401, body 带业务 code
```

2. 或者更优雅：`Fail` 带一个明示的 HTTP status：

```go
response.Fail(c, response.ErrUnauthorized)  // 自动 401
response.Custom(c, http.StatusBadRequest, ErrInvalidParams, nil)
```

### 16. 配置缺少 Validate

`config.Manager.Load` 解析完直接返回，对必填字段 / 取值范围都没校验。建议加 `Validate() error`：

```go
func (c *Config) Validate() error {
    if c.Server.Port <= 0 || c.Server.Port > 65535 { ... }
    if c.JWT.Secret != "" && len(c.JWT.Secret) < 32 { ... }
    // 启用 mysql 时强制要求关键字段
    return nil
}
```

并在 `Manager.Load` 内自动调用——配置错把启动时间从"运行时第一次请求"提前到"进程启动"，是高可用的小细节。

---

## 三、高可用 / 生产就绪的缺口

### 17. 没有 Liveness / Readiness 区分

v1.0.2 的 `/health` 只有一个，对 K8s 不友好。K8s probe 期望：

- `/livez`：进程是否活着（**永远不依赖外部**，只检查 goroutine、内存）
- `/readyz`：是否可以接流量（依赖 mysql/redis 通透）

建议在保持 `/health` 兼容的同时加：

```go
xlgo.WithLivenessRoute()   // GET /livez
xlgo.WithReadinessRoute()  // GET /readyz, 复用 healthChecks
```

### 18. 没有 Prometheus / Metrics 中间件

通用 Web 框架不带 metrics endpoint 是硬伤。建议：

```go
import "github.com/prometheus/client_golang/prometheus/promhttp"

func WithMetricsRoute(path ...string) Option  // 默认 /metrics
```

并提供一个 `middleware.Metrics()`——HTTP latency、status code、in-flight 这些标配指标。

### 19. 没有请求级超时中间件

`http.Server.ReadTimeout` 是连接级。**业务级**超时需要 `middleware.Timeout(5*time.Second)`：

```go
func Timeout(d time.Duration) gin.HandlerFunc {
    return func(c *gin.Context) {
        ctx, cancel := context.WithTimeout(c.Request.Context(), d)
        defer cancel()
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```

下游 GORM/Redis 调用走 `c.Request.Context()` 才能真正级联取消。

### 20. RateLimiter 内存版无法集群共享

`middleware/ratelimit.go` 的 `RateLimiter` 是单进程内存版，多副本部署时限流器各管各的。Redis 版本（`RedisRateLimiter`）应该一并提供，并且：

- 用 lua 脚本实现 token bucket 或滑动窗口（避免多次 round-trip）
- 默认每个限流器有 `Name`，方便 Prometheus 上报"被限流次数"

### 21. 没有依赖健康自愈

主库宕机后 `database.Manager.master` 会一直握着断连。建议：

- `Pool.SetConnMaxIdleTime` 配置化
- 探活定时任务：每 30s ping 一次，连续 N 次失败标记"unhealthy"，readiness 立即返回 503
- Replica 健康剔除：`ReplicaPicker` 支持权重 + 健康度（v1.1.0 路线）

### 22. Graceful shutdown 没等业务 in-flight goroutine

现在 `Shutdown` 只关 HTTP server。如果业务在 handler 里 spawn 了后台 goroutine（异步发短信、写日志），它们会被进程退出强制砍掉。建议：

- App 暴露 `App.Go(fn func(ctx context.Context))`，内部维护 `sync.WaitGroup`
- Shutdown 时 `wg.Wait()` 带超时

### 23. `gin.Recovery` + `middleware.Recover` 双重保险但没 trace_id

panic 时只记录 stack，没有 request_id / trace_id 关联。建议 Recover 中间件改为：

```go
func Recover() gin.HandlerFunc {
    return func(c *gin.Context) {
        defer func() {
            if r := recover(); r != nil {
                rid := c.GetString("request_id")
                logger.Error("panic recovered",
                    zap.String("request_id", rid),
                    zap.Any("error", r),
                    zap.ByteString("stack", debug.Stack()))
                response.ServerError(c, "服务器内部错误")
            }
        }()
        c.Next()
    }
}
```

### 24. RequestID 中间件没默认装入

`response.go` 依赖 `c.GetString("request_id")` 但默认中间件链里没有这一环。建议 `app.Init` 时无条件 `Use(middleware.RequestID())`，让每个响应都带 `request_id`，trace 才有意义。

---

## 四、易上手 / 开发体验

### 25. 模块路径与 import alias 不一致

`go.mod` 是 `github.com/EthanCodeCraft/xlgo-core`，CLAUDE.md 又提"本地导入用 xlgo"——新用户第一次 `go mod tidy` 多半会撞墙。建议：

- 模块路径直接定为 `github.com/EthanCodeCraft/xlgo`（去掉 `-core`），**包名仍然 `xlgo`**
- README 第一段就给完整 import 语句，不要让用户猜

### 26. 代码里大量 "评分: ⭐⭐⭐⭐⭐" 注释

`response.go`、`storage.go`、`repository.go`、`config.go`、`cache.go`、`middleware/cors.go` ……到处都是：

```go
// 评分: ⭐⭐⭐⭐⭐
// 理由: 文件下载封装，自动设置响应头
```

这些是 AI 生成留下的"自夸"，**对外发布的库代码里出现这个会显得不专业**。建议批量删掉。如果想留设计理由，改成 `// Why: ...` 风格的简洁注释。

### 27. 大量 `Without*` Option 实际上不需要

```go
WithLogger / WithoutLogger
WithMySQL / WithoutMySQL
WithRedis / WithoutRedis
...
```

每对都是 v1.0.2 在"全开 vs 全关"摇摆产生的副产品。既然已经定调"`xlgo.New()` 是轻量"，那 `WithoutLogger` 的用途就只剩"用了 `NewFullStack` 又想关掉一个"。**建议**：

- `Without*` 全部标 `Deprecated`
- 文档统一推荐组合：
  - `xlgo.New(...)` + 显式 `With*`
  - `xlgo.NewFullStack(...)` 全开
- 真要关单项，让 FullStack 接受函数式排除：`xlgo.NewFullStack(xlgo.Disable("redis"))`

### 28. CLI 模板太单一

`xlgo new` 只生成一种模板。`Version_Update_Plan_v1.0.2.md` 第 884 行已经规划了：

```
xlgo new myproject --template minimal
xlgo new myproject --template api
xlgo new myproject --template fullstack
xlgo new myproject --template grpc      # 建议补
xlgo new myproject --template microservice
```

是时候做了。最小模板对降低"上手第一公里"的心理负担非常关键。

### 29. 缺一个 examples/ 目录

我看到 README 里有 `make run` → `go run ./example`，但仓库里**没有 example 目录**。新用户 clone 之后跑不起来。建议至少补两个：

- `examples/minimal/` —— 50 行能跑
- `examples/full/` —— mysql + redis + jwt + 一个 user CRUD

### 30. CHANGELOG 与 Version_Update_Plan 应分离

`Version_Update_Plan_v1.0.2.md` 是规划文档，不应放仓库根。建议：

```
docs/
  ├── CHANGELOG.md          # 追加格式，每个版本 Added/Changed/Fixed
  ├── plans/
  │   └── v1.0.2.md         # 历史规划归档
  ├── architecture.md
  └── migration/v1.0.1-to-v1.0.2.md
```

---

## 五、值得期待的 v1.1+ 路线

按优先级排成一个推荐的迭代节奏：

### v1.0.3（Bug Fix Release，1～2 周）

**修真实 bug，不破坏 API**

- ✅ #1 错误码冲突 + 自检
- ✅ #2 CORS Allow-Credentials
- ✅ #3 Logger Tee bug
- ✅ #4 删掉死代码 DBResolver / 引入 gorm dbresolver
- ✅ #6 `generateJTI` 错误处理
- ✅ #7 QueryBuilder.Page Count
- ✅ #8 OSS 文件名冲突
- ✅ #9 go.mod tidy 复查
- ✅ #26 删除"评分"注释

### v1.0.4（DX & Docs）

- ✅ #25 模块路径修正
- ✅ #28 CLI 多模板
- ✅ #29 examples/
- ✅ #30 文档结构调整

### v1.1.0（HA & Manager 化）

- #10 Storage/Cache/Redis/JWT 全部 Manager 化
- #12 Lifecycle Hooks
- #13 Server 参数全配置化
- #14 `time.Duration` 配置
- #16 Config Validate
- #17 livez / readyz
- #18 metrics 中间件
- #19 请求级 Timeout
- #20 Redis 限流器
- #21 主库探活 + replica 健康剔除
- #22 等业务 goroutine
- #23 Recover 带 request_id
- #24 RequestID 默认装入

### v1.2.0（生态）

- 内置 OpenAPI 3.x（替代 swaggo，后者已半弃维）
- gRPC Gateway 模板
- 多租户模板（参考 #1 错误码 namespace）
- RBAC 扩展包
- DDD / Clean Architecture 项目模板

---

## 六、整体判断

xlgo 的 v1.0.2 已经迈过了"内部工具集"到"框架"的这道坎，特别是**dialect 注册表**、**config.Manager + SetDefaultManager**、**database.Manager** 这三个设计是真正的框架式抽象，证明设计上是在往正确方向走。

但要打到"通用 / 高可用 / 易上手"，**最值得马上做的两件事**是：

1. **先把第一节那 9 个 Bug 修掉**——它们里任意一个被开源用户踩到，都会发 issue 质疑框架质量。
2. **把 Storage/Cache/Redis/JWT 也 Manager 化**——这是把 v1.0.2 的好抽象"贯彻到底"。否则现在是**一半组件可注入，一半是单例**的撕裂状态，对中大型项目和单元测试都不友好。

建议从 v1.0.3 的 Bug Fix 开始动手，优先级：

> #1 (错误码冲突) → #2 (CORS) → #3 (Logger Tee) → #4 (DBResolver 死代码)

这几个改完都不破坏 API，可以一个 PR 一个 commit 走 review。
