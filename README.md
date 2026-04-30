# xlgo Web Framework

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=for-the-badge&logo=go" alt="Go Version">
  <img src="https://img.shields.io/badge/Gin-v1.9-00ADD8?style=for-the-badge" alt="Gin Version">
  <img src="https://img.shields.io/badge/License-MIT-green?style=for-the-badge" alt="License">
</p>

xlgo 是一个基于 Go + Gin 的轻量级 Web 开发框架，提供了完整的后端开发基础设施，包括配置管理、数据库访问、缓存、认证、日志、文件存储等常用功能。

## 框架特性

- **配置管理** - 支持 YAML 配置文件，环境变量覆盖，配置热更新
- **数据库** - MySQL + GORM，支持自动迁移、重试机制、连接池、读写分离
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
  host: localhost
  port: 3306
  user: root
  password: your_password
  name: your_database
  max_idle_conns: 10
  max_open_conns: 100

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

```go
// 初始化 MySQL
database.InitMySQL(cfg)
defer database.Close()

// 主从读写分离
database.InitMySQLWithReplicas(cfg, []string{
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

// 管理员权限
r.Use(middleware.AdminRequired())

// 超级管理员权限
r.Use(middleware.SuperAdminRequired())

// 员工权限
r.Use(middleware.StaffRequired())

// 任意用户（管理员或员工）
r.Use(middleware.AnyUserRequired())

// 获取用户信息
userID := middleware.GetUserID(c)
username := middleware.GetUsername(c)
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
│   ├── mysql.go        # MySQL 连接（支持读写分离）
│   └── redis.go        # Redis 连接
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

### v2.1.0 (2026-04-30)

- **分布式锁安全增强** - Lua 脚本 + UUID Token，只有持有者能释放锁
- **JWT 黑名单优化** - 使用 JTI 替代完整 Token，大幅节省 Redis 内存
- **HTTP Client 连接池** - Transport 初始化时创建，连接可复用
- **优雅关闭机制** - 监听系统信号，等待请求处理完成
- **Redis 分布式限流** - 滑动窗口算法，多实例共享限流状态
- **Repository 扩展** - 分页查询、链式查询、批量操作、事务支持
- **CORS 配置完善** - 支持配置文件、通配符域名
- **日志中间件增强** - 可记录请求体、慢请求警告、敏感字段过滤
- **新增测试** - cache、middleware 新增多项测试用例

### v2.0.0 (2026-04-30)

- **新增工具函数库** - 111 个实用函数（随机数、字符串、时间、转换、文件、URL、验证、加密、HTTP 客户端、UUID）
- **新增彩色控制台输出** - console 包支持 Debug/Info/Success/Warn/Error 五级彩色输出
- **新增压缩解压** - compress 包支持 Gzip/Zip 压缩解压
- **新增键名前缀管理** - cache.K() 自动添加站点前缀，解决多项目共用 Redis 冲突
- **新增分布式锁** - cache.Lock/TryLock/WithLock 完整实现
- **新增计数器** - cache.Incr/Decr/IncrBy 支持
- **新增 RequestID 中间件** - 请求追踪支持
- **新增 Recover 中间件** - Panic 恢复
- **新增类型安全参数获取** - handler.QueryInt/PathInt/FormInt 等
- **新增路由架构** - router 包支持模块化、版本化 API、中间件分组、RESTful CRUD
- **新增 AppConfig** - 站点别名、环境判断
- **CLI 工具重构** - 模块化结构，模板分离
- **单元测试覆盖** - 17 个包有测试（68%覆盖）

### v1.1.0 (2026-04-29)

- 新增配置热更新支持
- 新增数据库读写分离
- 新增 CSRF 防护中间件
- 新增 SSE 流式响应支持
- 新增 WebSocket 支持
- 新增定时任务调度器
- 新增 CLI 脚手架工具
- 新增测试工具包
- 新增统一错误码体系
- 新增密码加密工具
- 新增请求验证器（支持自定义错误消息）
- 实现 OSS 存储上传
- 优化缓存 DeleteByPattern 使用 SCAN
- 修复限流器 goroutine 泄漏问题

### v1.0.0 (2024-04)

- 初始版本发布
- 基础框架功能
- 完整示例代码

---

## 许可证

MIT License - 详见 [LICENSE](LICENSE) 文件
