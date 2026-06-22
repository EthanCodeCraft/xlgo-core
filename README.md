# xlgo Web Framework

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=for-the-badge&logo=go" alt="Go Version">
  <img src="https://img.shields.io/badge/Gin-v1.9-00ADD8?style=for-the-badge" alt="Gin Version">
  <img src="https://img.shields.io/badge/License-MIT-green?style=for-the-badge" alt="License">
</p>

xlgo 是一个基于 Go + Gin 的轻量级 Web 开发框架，提供了完整的后端开发基础设施，包括配置管理、数据库访问、缓存、认证、日志、文件存储等常用功能。

## 框架特性

- **配置管理** - 支持 YAML 配置文件，环境变量覆盖，配置热更新
- **数据库** - 基于 GORM 的可插拔方言注册表，内置 MySQL / PostgreSQL，可注册任意 GORM 驱动；支持自动迁移、重试、连接池、读写分离
- **缓存** - Redis 缓存，支持分布式缓存、键前缀、TTL，SCAN 优化
- **认证** - JWT 认证，支持 Token 黑名单、刷新机制
- **日志** - 分级日志（API、数据库），日志轮转
- **中间件** - CORS、限流、日志、认证、CSRF 防护
- **文件存储** - 本地存储 + 阿里云 OSS 支持
- **实时通信** - SSE 流式响应 + WebSocket 支持
- **定时任务** - 内置任务调度器
- **验证器** - 请求参数验证，支持自定义错误消息
- **错误处理** - 统一错误码体系
- **CLI 工具** - 脚手架工具，快速创建项目和代码

## 快速开始

> **环境要求**：xlgo 基于 Go 1.25+ 构建，请确保本地已安装 Go 1.25 或更高版本。

### 1. 安装

```bash
# 安装脚手架工具
go install github.com/EthanCodeCraft/xlgo-core/cmd/xlgo@latest

# 创建新项目
xlgo new myproject

# 进入目录
cd myproject

# 安装依赖
go mod tidy
```

### 2. 创建配置文件 config.yaml

```yaml
server:
  port: 8080
  mode: development

database:
  driver: mysql          # mysql（默认）或 postgres
  host: localhost
  port: 3306
  user: root
  password: your_password
  name: your_database
  max_idle_conns: 10
  max_open_conns: 100
  # dsn: "自定义连接字符串，设置后优先于上面的字段"

redis:
  host: localhost
  port: 6379
  password: ""
  db: 0

jwt:
  secret: your_jwt_secret_key
  expire: 86400

storage:
  driver: local
  local:
    path: ./public
    base_url: http://localhost:8080/public

log:
  dir: ./logs
  max_size: 100
  max_backups: 30
  max_age: 30
  compress: true
```

### 3. 运行项目

```bash
go run main.go
```

访问 http://localhost:8080/health 检查服务状态。

> v1.0.2 起，`xlgo.New()` 默认是轻量应用，不会自动初始化 MySQL、Redis、Storage 或 Swagger。需要完整基础设施时请显式使用 `WithMySQL()`、`WithRedis()`、`WithStorage()`、`WithSwaggerRoutes()`，或直接使用 `xlgo.NewFullStack()`。

---

## 核心功能

### 配置管理

```go
// 加载配置
cfg, err := config.Load("./config.yaml")

// 获取配置
cfg := config.Get()
serverPort := cfg.Server.Port

// 配置热更新
cfg, err := config.LoadWithWatch("./config.yaml", func(newCfg *config.Config) {
    log.Println("配置已更新")
})

// 注册配置变更回调
config.RegisterCallback(func(cfg *config.Config) {
    // 处理配置变更
})

// 手动重新加载
config.Reload()
```

### 数据库操作

xlgo 基于 GORM，主库与从库的驱动由配置 `database.driver` 决定，内置支持 `mysql`（默认）与 `postgres`，也可通过 `database.dsn` 使用任意自定义连接字符串。v1.0.2 起 GORM 方言通过 **可插拔注册表** 管理，应用可自行接入 SQLite、SQL Server、ClickHouse 等任意 GORM 驱动而无需修改框架。

```go
// 初始化数据库（驱动由配置决定）
database.InitDB(cfg)
defer database.Close()

// 主从读写分离（从库 DSN 需与主库驱动匹配）
database.InitDBWithReplicas(cfg, []string{
    "root:pass@tcp(slave1:3306)/db",
    "root:pass@tcp(slave2:3306)/db",
})

// 读操作自动路由到从库
users := database.GetReadDB().Find(&users)

// 强制使用主库
ctx := database.UseMaster(context.Background())
database.GetDBFromContext(ctx).Find(&users)

// 事务
database.Transaction(func(tx *gorm.DB) error {
    return tx.Create(&user).Error
})

// 健康检查
status := database.HealthCheck()
```

v1.0.2 起数据库状态由实例化的 `database.Manager` 管理，全局函数（`GetDB`、`GetReadDB`、`CloseAll` 等）作为默认 `DefaultManager` 的 facade 保留。可通过 `database.NewManager(cfg)` 创建独立管理器，并通过 `ReplicaPicker` 自定义从库选择策略：

```go
// 独立管理器（不影响全局 DefaultManager）
mgr := database.NewManager(cfg)
if err := mgr.Open(context.Background()); err != nil {
    return err
}
defer mgr.Close()

// 从库选择策略：轮询（默认随机）
database.SetReplicaPicker(&database.RoundRobinPicker{})
```

#### 注册自定义 GORM 方言

通过 `database.RegisterDialect` 一次注册即可让 `database.driver: <name>` 生效，DSN 构建器会同步登记到 `config` 包，因此 `cfg.Database.DSN()` 也会识别新驱动：

```go
import (
    "github.com/EthanCodeCraft/xlgo-core/config"
    "github.com/EthanCodeCraft/xlgo-core/database"
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
)

func init() {
    database.RegisterDialect(database.DialectSpec{
        Name:      "sqlite",
        Aliases:   []string{"sqlite3"},
        Dialector: func(dsn string) gorm.Dialector { return sqlite.Open(dsn) },
        DSN:       func(c *config.DatabaseConfig) string { return c.Name }, // Name 当作文件路径
    })
}
```

诊断接口：`database.RegisteredDialects()` 返回当前已注册的驱动名（含别名），`database.LookupDialect(name)` 用于检查某个驱动是否已就绪。未注册的驱动会回退到 MySQL 以保持向后兼容。

### Repository 泛型 CRUD

```go
// 创建仓库
userRepo := repository.NewBaseRepo[model.User](database.GetDB())

// 基础 CRUD
user, err := userRepo.FindByID(ctx, 1)
err := userRepo.Create(ctx, &user)
err := userRepo.Update(ctx, &user)
err := userRepo.Delete(ctx, 1)

// 分页查询
result, err := userRepo.FindPage(ctx, 1, 20)
// result.Items, result.Total, result.Page, result.PageSize

// 条件查询
users, err := userRepo.FindWhere(ctx, "status = ?", 1)

// 链式查询
users, err := userRepo.NewQueryBuilder().
    Where("status = ?", 1).
    Order("created_at DESC").
    Limit(10).
    Find(ctx)

// 批量操作
err := userRepo.CreateBatch(ctx, users)
err := userRepo.DeleteBatch(ctx, []uint{1, 2, 3})

// 事务支持
err := userRepo.WithTransaction(ctx, func(txRepo *repository.BaseRepo[model.User]) error {
    return txRepo.Create(ctx, &user)
})
```

### Redis 缓存

```go
// 初始化缓存
cache.Init()

// 使用缓存
ctx := context.Background()
cacheService := cache.GetCache()

// 设置缓存
cacheService.Set(ctx, "user:1", user, 10*time.Minute)

// 获取缓存
var user User
if cacheService.Get(ctx, "user:1", &user) {
    // 缓存命中
}

// 删除缓存
cacheService.Delete(ctx, "user:1")

// 按模式删除（使用 SCAN 优化）
cacheService.DeleteByPattern(ctx, "user:*")
```

### JWT 认证

```go
// 生成 Token（自动包含 JTI）
token, err := jwt.GenerateToken(userID, username, "admin", "admin")

// 解析 Token
claims, err := jwt.ParseToken(tokenString)

// 使 Token 失效（使用 JTI，高效）
jwt.InvalidateToken(tokenString)

// 获取 Token 的 JTI
jti, err := jwt.GetJTI(tokenString)
```

**JWT 黑名单优化**：使用 JTI（JWT ID）替代完整 Token 存储，大幅节省 Redis 内存。

### 分布式锁

```go
// 安全加锁（返回 LockToken）
token, err := cache.NewLock(ctx, cache.KLock("order:123"), 30*time.Second)
if token != nil {
    defer cache.Unlock(ctx, token) // 只有持有者能释放
    // 执行业务逻辑
}

// 续期锁（长任务场景）
cache.ExtendLock(ctx, token, 30*time.Second)

// 自动续期执行
err := cache.WithLockAutoExtend(ctx, key, 30*time.Second, 10*time.Second, func() error {
    // 长任务自动续期
    return nil
})
```

**分布式锁安全特性**：使用 Lua 脚本 + UUID Token，只有锁的持有者才能释放。

### Redis 分布式限流

```go
// 内存限流（单实例）
r.Use(middleware.CustomRateLimit(100, time.Minute))

// Redis 分布式限流（多实例共享）
r.Use(middleware.RedisRateLimit("api_limit", 100))
r.Use(middleware.LoginRedisRateLimit())  // 登录限流
r.Use(middleware.APIRedisRateLimit())    // API 限流
```

### 请求验证

```go
type LoginRequest struct {
    Username string `json:"username" label:"用户名" validate:"required,username" msg_required:"请输入用户名"`
    Password string `json:"password" label:"密码" validate:"required,password" error:"密码格式不正确"`
    Phone    string `json:"phone" label:"手机号" validate:"omitempty,phone" msg_phone:"请输入正确的手机号"`
}

// 绑定并验证
errors, ok := validation.ShouldBindAndValidate(c, &req)
if !ok {
    response.Fail(c, errors.FirstMessage())
    return
}
```

**验证器支持的自定义标签**：

- `label:"中文名"` - 字段显示名称
- `error:"通用错误消息"` - 所有验证失败时显示
- `msg_required:"必填项"` - 特定规则的错误消息
- `msg_min:"最少5个字符"` - 针对 min 规则的错误消息

### 统一错误码

```go
// 使用预定义错误
response.FailWithError(c, response.ErrUserNotFound)

// 带详细信息
response.FailWithDetail(c, response.ErrPasswordWrong, "连续错误3次将锁定账户")

// 自定义错误
err := response.NewError(10001, "自定义错误")
```

**预定义错误码**：

- 用户模块：`ErrUserNotFound`, `ErrPasswordWrong`, `ErrTokenExpired` 等
- 文件模块：`ErrFileTooLarge`, `ErrFileTypeInvalid` 等
- 数据模块：`ErrDataNotFound`, `ErrDataConflict` 等

### 文件上传

```go
// 上传文件
file, err := c.FormFile("file")
path, err := storage.Upload(file, "images")
url := storage.GetURL(path)

// 上传字节数组
path, err := storage.UploadFromBytes(data, "filename.jpg", "images")

// 删除文件
err := storage.Delete(path)

// 获取文件内容
data, err := storage.Get(path)

// 检查文件是否存在
exists := storage.Exists(path)
```

### SSE 流式响应

```go
// AI 对话场景
func ChatHandler(c *gin.Context) {
    ch := make(chan string)
    go func() {
        defer close(ch)
        for _, chunk := range aiResponse {
            ch <- chunk
        }
    }()
    sse.StreamChunks(c, ch)
}

// 带消息 ID 的流式响应
sse.StreamWithID(c, "msg_123", ch)
```

### WebSocket

```go
// 简单使用
r.GET("/ws", ws.HandleFunc(func(conn *ws.Connection, message []byte) {
    conn.SendText("收到: " + string(message))
}))

// 广播模式
hub := ws.NewHub()
go hub.Run()
hub.Broadcast([]byte("广播消息"))
```

### 定时任务

```go
// 每隔 5 分钟执行
cron.AddTask("cleanup", cron.Every(5*time.Minute), func(ctx context.Context) error {
    return cleanupOldData()
})

// 每天凌晨 2 点执行
cron.AddTask("daily_report", cron.Daily(2, 0), generateReport)

// 每周一上午 9 点执行
cron.AddTask("weekly", cron.Weekly(time.Monday, 9, 0), weeklyTask)

// 完整 Cron 表达式
cron.AddTask("every15min", cron.ParseCron("*/15 * * * *"), doSomething)
cron.AddTask("monthly", cron.ParseCron("0 0 1 * *"), doSomething) // 每月1号

// 启动调度器
cron.Start()
defer cron.Stop()
```

### CSRF 防护

```go
// 基本使用
r.Use(middleware.CSRF())

// 获取 Token（返回给前端）
token := middleware.GetCSRFToken(c)

// 跳过指定路径
r.Use(middleware.CSRFWithSkip([]string{"/api/webhook"}))

// API 模式（双重提交 Cookie）
r.Use(middleware.DoubleSubmitCookie())
```

### 密码加密

```go
// 加密密码
hash, err := validation.HashPassword("password123")

// 验证密码
if validation.CheckPassword(hash, "password123") {
    // 密码正确
}

// 带成本升级的验证
match, needUpgrade, newHash, err := validation.CheckPasswordAndUpgrade(hash, password, 12)
```

---

## 中间件

### 认证中间件

```go
// JWT 认证（必须登录）
r.Use(middleware.AuthRequired())

// 自定义用户类型权限
r.Use(middleware.RequireUserTypes("tenant_admin", "platform_admin"))

// 自定义角色权限
r.Use(middleware.RequireRoles("owner", "manager"))

// 自定义复杂权限判断
r.Use(middleware.RequireAuth(func(user middleware.AuthUser, c *gin.Context) bool {
    return user.UserType == "merchant" && user.Role == "owner"
}))

// 默认快捷方法（super_admin/admin/staff 只是框架默认常量）
r.Use(middleware.AdminRequired())
r.Use(middleware.SuperAdminRequired())
r.Use(middleware.StaffRequired())
r.Use(middleware.AnyUserRequired())

// 获取用户信息
user, ok := middleware.GetAuthUser(c)
userID := middleware.GetUserID(c)
username := middleware.GetUsername(c)
role := middleware.GetRole(c)
userType := middleware.GetUserType(c)
```

### 限流中间件

```go
// 登录限流（每分钟 10 次）
r.POST("/login", middleware.LoginRateLimit(), handler.Login)

// 上传限流（每分钟 20 次）
r.POST("/upload", middleware.UploadRateLimit(), handler.Upload)

// 自定义限流
r.Use(middleware.CustomRateLimit(50, time.Minute))

// 停止限流器（应用关闭时）
defer middleware.StopRateLimiters()
```

---

## 响应格式

框架使用统一的响应格式：

```json
{
  "code": 1,
  "msg": "操作成功",
  "data": {},
  "request_id": "abc123"
}
```

```go
// 成功响应
response.Success(c, data)
response.SuccessWithMsg(c, "自定义消息", data)

// 失败响应
response.Fail(c, "错误消息")

// 分页响应
response.Page(c, list, total, page, pageSize)

// 错误响应
response.Unauthorized(c, "请先登录")
response.NotFound(c, "资源不存在")
response.ServerError(c, "服务器错误")
response.RateLimit(c)
```

---

## CLI 工具

```bash
# 创建新项目
xlgo new myproject

# 创建新项目并指定模块路径
xlgo new myproject --module github.com/myorg/myproject

# 生成代码
xlgo make handler user    # 创建 handler/user.go
xlgo make repository user # 创建 repository/user_repository.go
xlgo make model user      # 创建 model/user.go
xlgo make service user    # 创建 service/user_service.go

# 显示版本
xlgo version
```

---

## 测试

框架提供完整的测试工具包：

```go
func TestUserAPI(t *testing.T) {
    router := test.SetupRouter()

    // 创建请求
    resp := test.POST(router, "/api/v1/users").
        WithJSON(map[string]any{"name": "test"}).
        WithToken("xxx").
        Execute()

    // 断言
    resp.AssertOK(t)

    // 解析响应
    var result map[string]any
    resp.ParseJSON(&result)
}
```

---

## 目录结构

```
xlgo/
├── app.go              # 应用入口
├── cache/
│   ├── cache.go        # 缓存服务
│   ├── keybuilder.go   # 键名前缀管理
│   └── lock.go         # 分布式锁
├── cmd/
│   └── xlgo/           # CLI 脚手架
├── compress/
│   └── compress.go     # Gzip/Zip 压缩解压
├── config/
│   └── config.go       # 配置管理（支持热更新）
├── console/
│   └── console.go      # 彩色控制台输出
├── cron/
│   └── cron.go         # 定时任务调度
├── database/
│   ├── manager.go     # 数据库管理器（主从、Picker、Init/Close/HealthCheck）
│   ├── dialect.go     # GORM 方言注册表（mysql / postgres，可扩展）
│   └── redis.go       # Redis 连接
├── handler/
│   └── handler.go      # 基础处理器（类型安全参数获取）
├── jwt/
│   └── jwt.go          # JWT 工具
├── logger/
│   ├── logger.go       # 日志实现
│   └── field.go        # 日志字段工具
├── middleware/
│   ├── auth.go         # JWT 认证中间件
│   ├── cors.go         # CORS 跨域中间件
│   ├── csrf.go         # CSRF 防护中间件
│   ├── logger.go       # 请求日志中间件
│   ├── ratelimit.go    # 限流中间件
│   ├── requestid.go    # 请求ID中间件
│   └── recover.go      # Panic恢复中间件
├── model/
│   └── base.go         # 基础模型
├── repository/
│   └── repository.go   # 基础仓库（泛型CRUD）
├── response/
│   ├── response.go     # 统一响应格式
│   └── error.go        # 统一错误码
├── router/
│   └── router.go       # 路由注册中心（模块化/版本化）
├── sse/
│   └── sse.go          # SSE 流式响应
├── storage/
│   └── storage.go      # 文件存储（本地+OSS）
├── test/
│   └── test.go         # 测试工具包
├── trace/
│   └── trace.go        # 链路追踪
├── utils/
│   ├── random.go       # 随机数生成
│   ├── strings.go      # 字符串处理
│   ├── datetime.go     # 时间日期
│   ├── convert.go      # 类型转换
│   ├── file.go         # 文件操作
│   ├── url.go          # URL处理
│   ├── validator.go    # 格式验证
│   ├── crypto.go       # 加密编码
│   ├── http.go         # HTTP客户端
│   └── uuid.go         # UUID生成
├── validation/
│   ├── validator.go    # 请求验证器
│   ├── password.go     # 密码强度验证
│   └── hash.go         # 密码加密
├── wire/
│   └── wire.go         # 依赖注入
└── ws/
    └── ws.go           # WebSocket 支持
```

---

## 部署指南

### Docker 部署

```dockerfile
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -o server .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/server .
COPY --from=builder /app/config.yaml .
RUN mkdir -p /app/public /app/logs
EXPOSE 8080
CMD ["./server", "-config", "./config.yaml"]
```

```bash
docker build -t xlgo-app:latest .
docker run -d -p 8080:8080 xlgo-app:latest
```

---

## 更新日志

> 完整变更历史见 [CHANGELOG.md](./CHANGELOG.md)。

### v1.0.3 (2026-06-22)

> 本版本定位为 **bug fix release**：收口 v1.0.2 引入的破坏性清理，并修复 4 个轻量 bug + 依赖复查。完整说明见 [CHANGELOG.md#unreleased](./CHANGELOG.md#unreleased)。

#### 🐛 Fixed — JWT JTI 生成忽略 `rand.Read` 错误

`generateJTI()` 丢弃 `crypto/rand.Read` 的 error，失败时会基于全零字节生成 JTI，导致所有 token 的 JTI 相同、黑名单机制失效。改为 `(string, error)` 并在 `GenerateToken` / `GenerateTokenWithCustomExpiry` 传播错误。

#### 🐛 Fixed — `QueryBuilder.Page` 统计行数被残留 Limit 截断

`Page()` 复制查询做 Count 时未清除残留 `Limit`/`Offset`，调用方先 `.Limit(n)` 再 `.Page(...)` 会让 Count 被包成子查询，返回 `total` 被截断为 ≤ n。countDB 改为 `.Limit(-1).Offset(-1)` 清除残留条件。

#### 🐛 Fixed — OSS / 本地存储文件名冲突

4 处上传路径仅用 `time.Now().UnixNano()` 命名，同纳秒并发上传会撞名覆盖。新增 `uniqueFilename(now, ext)`（`<unixNano>-<8字节crypto/rand hex>.<ext>`），4 处统一改用。

#### 🐛 Fixed — 数据库重试策略对不可恢复错误无效

`InitDB` 对认证失败（`Access denied`）、未知数据库（`Unknown database`）、非法 DSN、未注册驱动等配置类错误也退避重试 5 次，白白延迟 31 秒。新增 `isTransientDBError`，这类错误首次出现即返回；网络类错误仍正常重试。

#### 📦 Dependencies — `go mod tidy` + 安全补丁升级

- `go mod tidy` 补全 postgres 方言传递依赖（`jackc/pgx` 家族、`golang.org/x/sync`），`gorm.io/driver/postgres` 提升为直接依赖
- 安全补丁升级（无 API 变更）：`golang.org/x/crypto` v0.49→v0.53、`golang-jwt/jwt/v5` v5.2.1→v5.3.1、`gorilla/websocket` v1.5.1→v1.5.3
- 暂缓升级（留待 v1.0.4 / v1.1）：`gin` / `validator` / `gorm` / `aliyun-oss-go-sdk` v2→v3（major 破坏性）等跨版本升级

#### ⚠️ Breaking — 清理 v1.0.2 兼容别名（database 包）

```go
// ❌ 移除
database.InitMySQL(cfg)
database.InitMySQLWithReplicas(cfg, replicas)
(*Manager).InitMySQL / InitMySQLWithReplicas

// ✅ 改用（v1.0.2 起就是正式 API，驱动由 cfg.Database.Driver 决定）
database.InitDB(cfg)
database.InitDBWithReplicas(cfg, replicas)
```

xlgo 仍是早期框架，趁此一次彻底清理 v1.0.2 临时保留的别名，避免长期累积技术债。

#### 🗑️ Removed — 删除死代码 `database.DBResolver`

`database.DBResolver.BeforeQuery` 从未被注册到 GORM callback chain，属于纯死代码。文档曾暗示的"自动读写分离"实际从未生效——读写分离一直依赖业务侧显式调用 `database.UseMaster(ctx)` / `database.UseReplica(ctx)`。

需要 callback 级自动路由的用户请直接接入官方 [`gorm.io/plugin/dbresolver`](https://github.com/go-gorm/dbresolver)。

#### 🔄 Changed — 文件重命名 `database/mysql.go → database/manager.go`

文件内容已与 MySQL 解耦（v1.0.2 引入可插拔方言注册表后），继续叫 `mysql.go` 误导。**导入路径无变化**，公开 API 全部保留。

#### ✨ Added — console 包显式 level 控制

`console` 包新增显式级别屏蔽能力：

```go
console.SetLevel(console.LevelWarn)    // 只看 Warn / Error
console.SetLevel(console.LevelSilent)  // 完全静默
```

**定位明确**：console 是**开发期彩色 stdout 工具**（跟 `fmt.Println` 同级），**不写文件、不感知环境**。业务可观测信息请使用 `logger`。框架不会自动根据 `app.env` 切换级别——选择权完全在调用方，避免"dev / prod 行为不一致"的隐式陷阱。

完整对比表见 [GUIDE.md §3.3](./GUIDE.md#33-彩色控制台输出)。

#### 🐛 Fixed — Logger 重复写入修复

修复通用 `Logger` 把 `apiCore` 与 `dbCore` 都 Tee 进来导致**每条日志写三份**的 bug。

- `Logger`（通用）→ `logs/app.log` + console
- `APILog()`     → `logs/api.log` + console
- `DBLog()`      → `logs/database.log` + console
- 互不串扰，磁盘体积砍掉 2/3

**新增**：`logger.Close()` 显式关闭文件句柄，`App.Shutdown` 已自动调用；`Init(nil)` 改为返回 error 而非 panic；生产默认级别从 `Warn` 调整为 `Info`。

**升级注意**：日志目录会新增 `logs/app.log` 文件（之前通用日志被串写进了 `api.log`/`database.log`），运维采集脚本如有需要请补上。

#### 🔒 Security — CORS 中间件修复

修复 CORS 中间件多个安全与规范遵守问题：

- **`Access-Control-Allow-Credentials` 永远是 `true`** — 旧实现 `if/else` 两个分支都设了 `"true"`，导致即使配置 `AllowCredentials=false` 也会发送凭证头
- **`*` + `credentials: true` 的规范违规** — 同时发送会被浏览器拒绝；修复后此场景回显具体 Origin
- **缺失 `Vary: Origin`** — 防止 CDN / 网关把 A 用户的 CORS 响应缓存给 B 用户
- 非白名单 Origin 不再被回显，防反射型 CORS 漏洞

**升级影响**：如果你之前依赖"默认允许凭证"的隐式行为，需要在配置里显式启用：

```yaml
cors:
  allowed_origins: ["https://your-frontend.example"]
  allow_credentials: true
```

#### ⚠️ Breaking — 错误码体系重构

修复 `CodeSuccess` 与 `CodeInvalidParams` 撞码的生产级 bug（两者都等于 `1`，导致业务错误响应被前端误判为成功）。

**数值变更**：

| 常量 | 旧值 | 新值 |
|---|---|---|
| `response.CodeSuccess` | `1` | **`0`** |
| `response.CodeFail` | `0` | **`1`** |

**移除**：

- `response.CodeInvalidParams`（与 `CodeSuccess` 撞码，且参数错误码应由业务自定义）
- `response.ErrInvalidParams`

**迁移指南**：

```go
// ❌ 旧代码（编译失败）
response.FailWithError(c, response.ErrInvalidParams)

// ✅ 推荐：业务侧自定义错误码
var ErrInvalidParams = response.NewError(40001, "参数错误")
response.FailWithError(c, ErrInvalidParams)

// ✅ 或直接使用通用失败响应
response.Fail(c, "用户名格式错误")
```

**前端**：`if (resp.code === 1)` → `if (resp.code === 0)`。

新增 `_errorCodeUniquenessGuard` 编译期防撞码 map，任何后续 `Code*` 常量重复都会在 `go build` 阶段直接报错。

详细迁移说明见 [CHANGELOG.md](./CHANGELOG.md)。

### v1.0.2 (2026-06-20)

#### 数据库
- **可插拔方言注册表** - 新增 `database.RegisterDialect` / `LookupDialect` / `RegisteredDialects`，应用可一次注册即让 `database.driver: <name>` 生效，DSN 构建器同步登记到 `config` 包；内置 `mysql` 与 `postgres`（含 `postgresql`、`pg` 别名），未注册驱动回退 MySQL
- **多数据库驱动** - `database.driver` 支持 `mysql`（默认）与 `postgres`，新增 `database.InitDB` / `InitDBWithReplicas`（v1.0.3 起原 `InitMySQL*` 别名已移除）
- **数据库管理器** - 引入实例化 `database.Manager` 持有主从连接,提供 `Master/Replica/FromContext/HealthCheck` 等方法
- **从库选择策略** - 新增 `ReplicaPicker` 接口与 `RoundRobinPicker` / `RandomPicker` 实现，可通过 `SetReplicaPicker` 自定义
- **私有上下文键** - `db_mode` 字符串键替换为私有 `dbModeContextKey{}` 类型，避免上下文键名冲突
- **DSN 构建器注册表** - `config` 包新增 `RegisterDSNBuilder` / `LookupDSNBuilder` / `RegisteredDrivers`，`DatabaseConfig.DSN()` 改为查注册表，自定义 `CustomDSN` 优先

#### App 启动流程
- **轻量默认** - `xlgo.New()` 默认不再初始化 MySQL / Redis / Storage，也不注册 `/health` 与 `/swagger/*`；通过 Option 显式启用
- **组件 Option** - 新增 `WithLogger / WithMySQL / WithRedis / WithStorage / WithWire / WithHealthRoutes / WithSwaggerRoutes / WithDefaultRoutes / WithAutoMigrate` 及对应 `WithoutXxx` 关闭项
- **迁移控制** - 新增 `Migrator` 类型与 `WithMigrator / WithModels`，注册时自动开启 `WithAutoMigrate`，`WithoutAutoMigrate` 可显式关闭；不再强制调用空的 `database.AutoMigrate()`
- **batteries-included** - 新增 `WithFullStack` / `NewFullStack` / `RunFullStack`，一键启用全部默认组件
- **错误传播** - 框架初始化失败一律 `return error`，不再在框架内部直接 `Fatalf` 退出进程

#### 权限中间件
- **去业务化** - `super_admin/admin/staff` 调整为默认常量而非固定业务模型
- **通用能力** - 新增 `AuthUser` 结构体、`GetAuthUser`、`RequireUserTypes / RequireRoles / RequireAuth`，旧的 `AdminRequired/SuperAdminRequired/StaffRequired/AnyUserRequired` 改为基于通用能力实现

#### 配置管理
- **实例化 Manager** - 新增 `config.Manager`，提供 `Load / LoadWithWatch / Reload / RegisterCallback / Get / GetViper / Set` 等方法，支持多实例与重复加载
- **App 持有 Manager** - `WithConfigPath` 创建 App 私有 manager 并通过新增的 `config.SetDefaultManager` 推为全局默认，使 `config.Get / GetString` 等便捷函数仍然可用
- **修复** - 修复 `WithConfigPath` 此前的空实现问题

#### 健康检查与默认路由
- **拆分注册** - `router` 包新增 `RegisterHealthRoute` / `RegisterSwaggerRoutes`，原 `RegisterDefaultRoutes` 改为组合调用
- **检查项与状态** - `/health` 支持注册 `HealthCheck`，失败返回 HTTP 503 与 `{"status":"error", "checks":{...}}`
- **App 集成** - 启用 MySQL / Redis 时自动追加对应健康检查项，可通过 `WithHealthCheck` 注册自定义检查

#### 资源关闭
- **Shutdown 修复** - 改为 `database.CloseAll()` 关闭主从连接，并使用 `errors.Join` 聚合限流器、日志、Redis 等组件的关闭错误
- **Storage 可选化** - 未初始化 Storage 时返回 `ErrStorageNotInitialized`，不再 nil panic

#### 文档与 CLI
- **CLI 修复** - `xlgo version` 更新为 v1.0.2，脚手架模板改为依赖 `github.com/EthanCodeCraft/xlgo-core` 并在注释中提示 Swagger / MySQL / Redis / FullStack 的开启方式
- **GUIDE 同步** - 1.3 最简示例、3.2 日志注释、8.4.7 默认路由、9.2 用户信息获取均更新为 v1.0.2 行为
- **历史日志修正** - 修复此前 README 中错误的 v2.0.0 / v2.1.0 更新日志表述

### v1.0.1 (2026-04-30)

- 新增工具函数库、彩色控制台输出、压缩解压、RequestID、Recover 中间件
- 新增缓存键名前缀、分布式锁、计数器、Redis 分布式限流
- 增强 JWT 黑名单、Repository、CORS、日志中间件和优雅关闭能力
- 新增路由架构，支持模块化、版本化 API、中间件分组和 RESTful CRUD
- 完善配置热更新、数据库读写分离、CSRF、SSE、WebSocket、定时任务、CLI、测试工具和统一错误码

### v1.0.0 (2024-04)

- 初始版本发布
- 基础框架功能
- 完整示例代码

---

## 许可证

MIT License - 详见 [LICENSE](LICENSE) 文件
