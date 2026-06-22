# xlgo 框架使用指南

## 目录

1. [快速开始](#1-快速开始)
2. [配置管理](#2-配置管理)
3. [日志系统](#3-日志系统)
4. [数据库操作](#4-数据库操作)
5. [缓存与 Redis](#5-缓存与redis)
6. [请求处理](#6-请求处理)
7. [响应与错误处理](#7-响应与错误处理)
8. [中间件](#8-中间件)
9. [认证与授权](#9-认证与授权)
10. [文件存储](#10-文件存储)
11. [工具函数库](#11-工具函数库)
12. [实时通信](#12-实时通信)
13. [定时任务](#13-定时任务)
14. [链路追踪](#14-链路追踪)
15. [压缩解压](#15-压缩解压)
16. [测试工具](#16-测试工具)
17. [CLI 脚手架](#17-cli脚手架)
18. [最佳实践](#18-最佳实践)

---

## 1. 快速开始

> **环境要求**：xlgo 需要 **Go 1.25+**。本项目作为新框架不背负旧版本兼容包袱，可直接使用 Go 1.25 的新特性。

### 1.1 安装框架

```bash
# 创建新项目
xlgo new myproject

# 或手动安装
go get github.com/EthanCodeCraft/xlgo-core
```

### 1.2 项目结构

```
myproject/
├── config.yaml        # 配置文件
├── main.go            # 入口文件
├── handler/           # 请求处理器
├── model/             # 数据模型
├── repository/        # 数据仓库
├── service/           # 业务逻辑
├── middleware/        # 自定义中间件
├── public/            # 静态文件
├── logs/              # 日志目录
└── go.mod
```

### 1.3 最简示例

```go
package main

import (
    xlgo "github.com/EthanCodeCraft/xlgo-core"
    "github.com/EthanCodeCraft/xlgo-core/middleware"
    "github.com/EthanCodeCraft/xlgo-core/response"
    "github.com/gin-gonic/gin"
)

func main() {
    // v1.0.2 起 xlgo.New 默认是轻量应用，不会自动初始化 MySQL/Redis/Storage，
    // 也不会自动注册 /health 或 /swagger/* 路由。
    // 通过 Option 显式启用所需组件即可。
    app := xlgo.New(
        xlgo.WithConfigPath("./config.yaml"),
        xlgo.WithLogger(),
        xlgo.WithDefaultRoutes(), // 同时启用 /health 与 /swagger/*
        xlgo.WithMiddlewares(middleware.Logger(), middleware.CORS()),
    )

    // 通过 Engine 直接挂路由，或使用 xlgo.WithModules(...) 注册模块
    app.GetRouter().GET("/hello", func(c *gin.Context) {
        response.Success(c, gin.H{"message": "Hello World"})
    })

    if err := app.Run(); err != nil {
        panic(err)
    }
}
```

> 想要 batteries-included 体验，可改用 `xlgo.NewFullStack(...)` 或
> `xlgo.RunFullStack(...)`，它会一次性启用 Logger / MySQL / Redis / Storage /
> Wire / Health / Swagger / AutoMigrate 等组件。

**优雅关闭特性：**
- 监听系统信号（SIGINT、SIGTERM）
- 等待请求处理完成（最多 30 秒）
- 按顺序关闭各组件（限流器、数据库、Redis、日志）

---

## 2. 配置管理

### 2.1 配置文件结构

```yaml
# config.yaml
app:
  name: "我的应用"
  site_name: "my_app" # 站点别名（重要：多项目共用Redis时区分）
  version: "1.0.0"
  env: "dev" # dev/test/prod
  debug: true
  base_url: "http://localhost:8080"
  token_expire: 86400 # Token过期时间(秒)

server:
  port: 8080
  mode: development # development/production

database:
  host: localhost
  port: 3306
  user: root
  password: your_password
  name: mydb
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

log:
  dir: ./logs
  max_size: 100 # MB
  max_backups: 30
  max_age: 30 # 天
  compress: true
```

### 2.2 使用配置

```go
import "github.com/EthanCodeCraft/xlgo-core/config"

// 加载配置
cfg, err := config.Load("./config.yaml")

// 获取配置值
port := cfg.Server.Port
dbHost := cfg.Database.Host

// 获取站点别名（用于缓存键前缀）
siteName := cfg.GetSiteName()

// 判断环境
if cfg.IsProduction() {
    gin.SetMode(gin.ReleaseMode)
}

// 热更新配置
config.LoadWithWatch("./config.yaml", func(newCfg *config.Config) {
    // 配置变更时的回调
})

// 动态获取配置值
debug := config.GetBool("app.debug")
```

---

## 3. 日志系统

### 3.1 初始化日志

```go
import "github.com/EthanCodeCraft/xlgo-core/logger"

// 初始化
logger.Init(cfg)

// 程序退出前同步
// v1.0.2 起 Sync 返回 error，可按需处理
_ = logger.Sync()
```

### 3.2 日志级别

```go
// 基础日志
logger.Debug("调试信息")
logger.Info("普通信息")
logger.Warn("警告信息")
logger.Error("错误信息")

// 格式化日志
logger.Infof("用户 %s 登录成功", username)
logger.Warnf("请求失败: %v", err)

// 致命错误（会终止程序）—— 仅在业务进程入口（main 等）使用
// v1.0.2 起，框架内部已禁止使用 Fatal/Fatalf，初始化错误改为 error 返回
logger.Fatalf("数据库连接失败: %v", err)

// 结构化日志
logger.Info("用户登录",
    zap.String("username", "admin"),
    zap.Int("user_id", 123),
)
```

### 3.3 彩色控制台输出

`console` 包定位是**开发期彩色 stdout 工具**——跟 `fmt.Println` 同级，不写文件、不感知运行环境、不做任何隐式行为。

```go
import "github.com/EthanCodeCraft/xlgo-core/console"

console.Debug("调试信息")   // 青色
console.Info("普通信息")    // 白色
console.Success("成功信息") // 绿色
console.Warn("警告信息")    // 黄色
console.Error("错误信息")   // 红色

// 自定义配置
c := console.New(
    console.WithColor(true),
    console.WithTime(true),
    console.WithCaller(true),       // skip 可选，默认 2
    console.WithLevel(console.LevelInfo),
)
c.Debug("此条不会输出，已被 LevelInfo 过滤")
c.Info("自定义输出")
```

#### console vs logger：怎么选？

| 维度 | `console` | `logger` |
|---|---|---|
| 定位 | 开发期肉眼调试 | 业务可观测性记录 |
| 输出目标 | stdout（彩色） | 文件 + stdout |
| 持久化 | ❌ | ✅ 滚动归档 |
| 结构化 | 文本 | JSON 字段 |
| 性能 | 一般 | zap 高性能 |
| 默认级别 | `LevelDebug`（全开） | dev=Debug / prod=Info |
| 适用场景 | 临时打印、开发联调 | 用户登录、订单事件、审计日志 |

**简单判断**：

- 这条信息上线后想留 → 用 `logger`
- 这条信息上线就该消失 → 用 `console`，并在 main 中显式切到高级别

#### 上线前显式收紧 console 输出

```go
func main() {
    cfg, _ := config.Load("./config.yaml")

    // 显式选择：生产期 console 只看 Warn / Error
    if cfg.IsProduction() {
        console.SetLevel(console.LevelWarn)
    }

    // 或者完全静默 console（业务全靠 logger）
    // console.SetLevel(console.LevelSilent)

    app := xlgo.New(...)
    app.Run()
}
```

> **注意**：xlgo 不会自动根据 `app.env` 切换 console 级别——选择权完全在调用方。
> 我们认为隐式切换会带来"开发期看到的 / 生产期看到的"行为不一致，调试体验更糟。

---

## 4. 数据库操作

xlgo 基于 GORM，驱动由配置 `database.driver` 决定。v1.0.2 起 GORM 方言通过 **可插拔注册表** 管理：内置 `mysql`（默认）与 `postgres`（含 `postgresql`、`pg` 别名），应用可通过 `database.RegisterDialect` 接入任意 GORM 驱动；`database.dsn` 字段始终可用于手写连接串。

### 4.1 初始化数据库

```go
import "github.com/EthanCodeCraft/xlgo-core/database"

// 初始化数据库（驱动由配置决定，等价于 database.DefaultManager.InitDB(cfg)）
database.InitDB(cfg)

// 关闭全部连接（含从库）
defer database.CloseAll()

// 获取数据库实例
db := database.GetDB()         // 主库
read := database.GetReadDB()   // 从库（无从库时回退主库）
```

### 4.1.1 主从读写分离

```go
// 从库 DSN 列表需与主库驱动匹配
database.InitDBWithReplicas(cfg, []string{
    "root:pass@tcp(slave1:3306)/db",
    "root:pass@tcp(slave2:3306)/db",
})

// 选择策略：默认 Random，可换成 RoundRobin
database.SetReplicaPicker(&database.RoundRobinPicker{})

// 强制使用主库（事务、需要实时数据的场景）
ctx := database.UseMaster(context.Background())
database.GetDBFromContext(ctx).Find(&users)

// 强制使用从库（报表查询）
ctx = database.UseReplica(context.Background())
database.GetDBFromContext(ctx).Find(&reports)
```

### 4.1.2 实例化 Manager

需要多套数据库连接（如平台库 + 租户库）时，`database.NewManager(cfg)` 创建独立管理器，互不影响：

```go
mgr := database.NewManager(cfg)
if err := mgr.Open(context.Background()); err != nil {
    return err
}
defer mgr.Close()

mgr.SetPicker(&database.RoundRobinPicker{})
db := mgr.FromContext(ctx)
```

### 4.1.3 注册自定义 GORM 驱动

`database.RegisterDialect` 一次注册即让 `database.driver: <name>` 生效，DSN 构建器同步登记到 `config` 包：

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

之后只需把配置改成 `database.driver: sqlite` 即可。SQL Server / ClickHouse / TiDB 等同理。

诊断接口：

- `database.RegisteredDialects()` - 列出已注册驱动（含别名）
- `database.LookupDialect(name)` - 检查某驱动是否可用
- `config.RegisteredDrivers()` - 列出已登记的 DSN 构建器

未注册的驱动会回退到 MySQL 以保持向后兼容。

### 4.2 定义模型

```go
import "github.com/EthanCodeCraft/xlgo-core/model"

type User struct {
    model.BaseModel              // 包含 ID, CreatedAt, UpdatedAt
    Username string `gorm:"size:50;unique" json:"username"`
    Email    string `gorm:"size:100" json:"email"`
    Password string `gorm:"size:255" json:"-"`
    Status   int    `gorm:"default:1" json:"status"`
}

func (User) TableName() string {
    return "users"
}
```

### 4.3 使用 Repository（扩展版）

```go
import "github.com/EthanCodeCraft/xlgo-core/repository"

// 创建仓库
userRepo := repository.NewBaseRepo[model.User](database.GetDB())

// 基础 CRUD
user, err := userRepo.FindByID(ctx, 1)
users, err := userRepo.FindAll(ctx)
users, err := userRepo.FindByIDs(ctx, []uint{1, 2, 3})
err := userRepo.Create(ctx, &user)
err := userRepo.Update(ctx, &user)
err := userRepo.Delete(ctx, 1)

// 统计数量
count, err := userRepo.Count(ctx)
count, err := userRepo.CountWhere(ctx, "status = ?", 1)

// 分页查询（内置）
result, err := userRepo.FindPage(ctx, 1, 20)
// result.Items - 数据列表
// result.Total - 总数
// result.Page - 当前页
// result.PageSize - 每页数量

// 条件分页查询
result, err := userRepo.FindPageWhere(ctx, 1, 20, "status = ?", 1)

// 分页并排序
result, err := userRepo.FindPageOrdered(ctx, 1, 20, "created_at DESC")

// 条件查询
user, err := userRepo.FindOne(ctx, "email = ?", "test@example.com")
users, err := userRepo.FindWhere(ctx, "status = ?", 1)
users, err := userRepo.FindWhereOrdered(ctx, "status = ?", 1, []any{}, "created_at DESC")

// 排序查询
users, err := userRepo.FindOrdered(ctx, "created_at DESC", 10)

// 批量操作
err := userRepo.CreateBatch(ctx, users)
err := userRepo.UpdateBatch(ctx, []uint{1, 2}, "status", 2)
err := userRepo.DeleteBatch(ctx, []uint{1, 2, 3})

// 存在性检查
exists, err := userRepo.Exists(ctx, 1)
exists, err := userRepo.ExistsWhere(ctx, "email = ?", "test@example.com")

// 软删除恢复
err := userRepo.Restore(ctx, 1)
err := userRepo.RestoreBatch(ctx, []uint{1, 2})

// 查询已删除的记录
deletedUsers, err := userRepo.FindDeleted(ctx)

// 链式查询（灵活构建）
users, err := userRepo.NewQueryBuilder().
    Where("status = ?", 1).
    Where("role = ?", "admin").
    Order("created_at DESC").
    Limit(10).
    Find(ctx)

// 链式分页查询
result, err := userRepo.NewQueryBuilder().
    Where("status = ?", 1).
    Order("created_at DESC").
    Page(ctx, 1, 20)

// 事务支持
err := userRepo.WithTransaction(ctx, func(txRepo *repository.BaseRepo[model.User]) error {
    // 创建用户
    if err := txRepo.Create(ctx, &user); err != nil {
        return err
    }
    // 更新关联数据
    return txRepo.Update(ctx, &profile)
})
```

### 4.4 Model 基础模型

```go
import "github.com/EthanCodeCraft/xlgo-core/model"

// BaseModel 包含 ID、CreatedAt、UpdatedAt、DeletedAt
type User struct {
    model.BaseModel              // 自动包含 ID、时间戳、软删除
    Username string `gorm:"size:50;unique" json:"username"`
    Email    string `gorm:"size:100" json:"email"`
    Status   int    `gorm:"default:1" json:"status"`
}

// BaseModel 字段说明
// ID        uint           - 主键
// CreatedAt time.Time      - 创建时间
// UpdatedAt time.Time      - 更新时间
// DeletedAt gorm.DeletedAt - 软删除时间（不返回给前端）
```

---

## 5. 缓存与 Redis

### 5.1 初始化缓存

```go
import "github.com/EthanCodeCraft/xlgo-core/cache"

// 初始化（通常在应用启动时）
cache.Init()

// 获取缓存实例
c := cache.GetCache()
```

### 5.2 缓存操作

```go
// 设置缓存
c.Set(ctx, "user:1", userData, 30*time.Minute)

// 获取缓存
var user User
if c.Get(ctx, "user:1", &user) {
    // 缓存命中
}

// 删除缓存
c.Delete(ctx, "user:1")

// 批量删除（按模式）
c.DeleteByPattern(ctx, "user:*")

// 检查是否存在
exists := c.Exists(ctx, "user:1")
```

### 5.3 键名前缀管理（多站点共用 Redis）

```go
import "github.com/EthanCodeCraft/xlgo-core/cache"

// 自动从配置读取 site_name 作为前缀
// config.yaml: app.site_name: "my_app"

cache.K("user:1")        // → "cache:my_app:user:1"
cache.KTemp("token")     // → "temp:my_app:token"
cache.KPerm("config")    // → "perm:my_app:config"
cache.KLock("order:123") // → "lock:my_app:order:123"
cache.KCounter("visit")  // → "counter:my_app:visit"
cache.KSession("sid")    // → "session:my_app:sid"

// 使用带前缀的缓存
c.Set(ctx, cache.K("user:1"), userData, ttl)
c.Get(ctx, cache.K("user:1"), &user)
```

### 5.4 分布式锁（安全增强版）

**使用 Lua 脚本 + UUID Token，只有锁的持有者才能释放锁：**

```go
// 安全加锁（返回 LockToken）
token, err := cache.NewLock(ctx, cache.KLock("pay:123"), 30*time.Second)
if token != nil {
    defer cache.Unlock(ctx, token) // 只有持有者能释放
    // 执行业务逻辑
}

// 带重试的锁
token, err := cache.TryLock(ctx, key, ttl, 100*time.Millisecond, 10)
if token != nil {
    defer cache.Unlock(ctx, token)
}

// 自动管理锁（简单场景）
err := cache.WithLock(ctx, key, ttl, func() error {
    return nil
})

// 自动续期锁（长任务场景）
err := cache.WithLockAutoExtend(ctx, key, 30*time.Second, 10*time.Second, func() error {
    // 执行时间超过 TTL 时自动续期
    return nil
})

// 续期锁（手动续期）
cache.ExtendLock(ctx, token, 30*time.Second)

// 检查锁是否被占用
locked, err := cache.IsLocked(ctx, key)

// 强制释放锁（管理场景）
cache.ForceUnlock(ctx, key)
```

**安全特性：**
- Lua 脚本保证原子性操作
- UUID Token 标识锁的持有者
- 只有持有者才能释放锁（防止误释放）
- 支持续期（长任务不会因锁过期而中断）

### 5.5 计数器

```go
// 自增
n, err := cache.Incr(ctx, cache.KCounter("page_view"))

// 指定增量
n, err := cache.IncrBy(ctx, key, 10)

// 自减
n, err := cache.Decr(ctx, key)

// 设置过期时间
cache.SetExpire(ctx, key, 1*time.Hour)

// 获取剩余时间
ttl, err := cache.GetTTL(ctx, key)
```

---

## 6. 请求处理

### 6.1 参数获取

```go
import "github.com/EthanCodeCraft/xlgo-core/handler"

// 类型安全的参数获取（推荐）

// Query参数
page := handler.QueryInt(c, "page", 1)            // Query参数 → int
id := handler.QueryInt64(c, "id", 0)              // Query参数 → int64
price := handler.QueryFloat64(c, "price", 0.0)    // Query参数 → float64
enabled := handler.QueryBool(c, "enabled", false) // Query参数 → bool

// 路径参数（RESTful API）
id := handler.PathInt(c, "id", 0)                 // 路径参数 → int
id := handler.PathInt64(c, "id", 0)               // 路径参数 → int64
id := handler.PathUint64(c, "id", 0)              // 路径参数 → uint64

// 表单参数（POST提交）
count := handler.FormInt(c, "count", 0)           // 表单参数 → int
id := handler.FormInt64(c, "id", 0)               // 表单参数 → int64
id := handler.FormUint64(c, "id", 0)              // 表单参数 → uint64
price := handler.FormFloat64(c, "price", 0.0)     // 表单参数 → float64
enabled := handler.FormBool(c, "enabled", false)  // 表单参数 → bool
name := handler.FormString(c, "name", "")         // 表单参数 → string

// 分页参数
page, pageSize := handler.GetPage(c)              // 默认 page=1, pageSize=20

// 路径ID
id, ok := handler.GetIDFromPath(c, "id")

// 绑定请求
var req LoginRequest
handler.BindJSON(c, &req)
```

### 6.2 请求验证

```go
import "github.com/EthanCodeCraft/xlgo-core/validation"

type RegisterRequest struct {
    Username string `json:"username" label:"用户名" validate:"required,min=3,max=20" msg_required:"用户名不能为空"`
    Password string `json:"password" label:"密码" validate:"required,password" msg_required:"密码不能为空"`
    Email    string `json:"email" label:"邮箱" validate:"required,email" msg_required:"邮箱不能为空"`
    Phone    string `json:"phone" label:"手机号" validate:"omitempty,phone"`
}

// 绑定并验证
errors, ok := validation.ShouldBindAndValidate(c, &req)
if !ok {
    response.Fail(c, errors.Error())
    return
}
```

### 6.3 验证规则

| 规则       | 说明                                  |
| ---------- | ------------------------------------- |
| `required` | 必填                                  |
| `min=3`    | 最小长度                              |
| `max=20`   | 最大长度                              |
| `email`    | 邮箱格式                              |
| `phone`    | 手机号格式                            |
| `password` | 密码强度（至少 8 位，包含字母和数字） |
| `url`      | URL 格式                              |
| `ip`       | IP 地址格式                           |

---

## 7. 响应与错误处理

### 7.1 统一响应格式

```json
{
  "code": 1,
  "msg": "操作成功",
  "data": {},
  "request_id": "abc123"
}
```

### 7.2 成功响应

```go
import "github.com/EthanCodeCraft/xlgo-core/response"

// 基础成功响应
response.Success(c, gin.H{"user": user})

// 自定义消息
response.SuccessWithMsg(c, "注册成功", gin.H{"user_id": 123})

// 分页响应
response.Page(c, users, total, page, pageSize)
```

### 7.3 错误响应

```go
// 基础失败
response.Fail(c, "参数错误")

// 自定义错误码
response.FailWithCode(c, response.CodeUserNotFound, "用户不存在")

// 使用预定义错误
response.FailWithError(c, response.ErrUserNotFound)

// 带详细信息
response.FailWithDetail(c, response.ErrPasswordWrong, "连续错误3次将锁定")
```

### 7.4 预定义错误码

> 注：参数错误等业务化错误码请由业务项目自行定义，框架不再内置 `ErrInvalidParams`。
> 推荐使用 `response.NewError(40001, "参数错误")` 在业务侧统一管理。

| 错误               | 码     | 说明       |
| ------------------ | ------ | ---------- |
| `ErrUnauthorized`  | 401    | 未授权     |
| `ErrForbidden`     | 403    | 无权限访问 |
| `ErrNotFound`      | 404    | 资源不存在 |
| `ErrRateLimit`     | 429    | 请求过于频繁 |
| `ErrServerError`   | 500    | 服务器错误 |
| `ErrUserNotFound`  | 10001  | 用户不存在 |
| `ErrPasswordWrong` | 10004  | 密码错误   |

### 7.5 特殊响应

```go
// 文件下载
response.Download(c, "report.xlsx", fileData)

// HTML响应
response.HTML(c, "<html>...</html>")

// 页面跳转
response.Redirect(c, 302, "https://example.com")
```

---

## 8. 中间件

### 8.1 内置中间件

```go
import "github.com/EthanCodeCraft/xlgo-core/middleware"

// 日志中间件（默认配置）
r.Use(middleware.Logger())

// API 专用日志（记录请求体）
r.Use(middleware.LoggerForAPI())

// 调试日志（最详细，记录请求体和响应体）
r.Use(middleware.LoggerForDebug())

// 最简日志（只记录基本信息）
r.Use(middleware.LoggerMinimal())

// 自定义日志配置
r.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
    LogRequestBody:       true,                  // 记录请求体
    LogResponseBody:      false,                 // 不记录响应体
    MaxBodyLength:        2048,                  // 最大记录长度
    SkipPaths:            []string{"/health"},   // 跳过路径
    SlowRequestThreshold: 500 * time.Millisecond, // 慢请求阈值
}))

// CORS跨域
r.Use(middleware.CORS())

// 请求ID（追踪）
r.Use(middleware.RequestID())

// Panic恢复
r.Use(middleware.Recover())

// 认证中间件
r.Use(middleware.AuthRequired())

// 自定义用户类型权限
r.Use(middleware.RequireUserTypes("tenant_admin", "platform_admin"))

// 自定义角色权限
r.Use(middleware.RequireRoles("owner", "manager"))

// 默认快捷权限（super_admin/admin/staff 只是默认常量）
r.Use(middleware.AdminRequired())

// CSRF防护
r.Use(middleware.CSRF())
```

### 8.2 CORS 配置（支持配置文件）

```yaml
# config.yaml 添加 CORS 配置
cors:
  allowed_origins:
    - "https://example.com"
    - "https://app.example.com"
    - "*.example.com"  # 支持通配符
  allowed_methods:
    - "GET"
    - "POST"
    - "PUT"
    - "DELETE"
  allowed_headers:
    - "Content-Type"
    - "Authorization"
  allow_credentials: true
  max_age: 86400  # 预检请求缓存时间（秒）
```

```go
// 使用配置文件的 CORS
r.Use(middleware.CORS())

// 指定域名列表
r.Use(middleware.CORSWithOrigins([]string{
    "https://example.com",
    "https://app.example.com",
}))

// 允许所有来源（仅开发环境）
r.Use(middleware.CORSWithWildcard())

// API 专用 CORS（不允许凭证）
r.Use(middleware.CORSForAPI())
```

**生产环境 CORS 配置建议：**
- 必须配置具体的 `allowed_origins`
- 不使用 `*` 通配符
- 根据需要配置 `allow_credentials`

### 8.3 CSRF 防护

```go
// Cookie模式（Web应用）
r.Use(middleware.CSRF())

// 双重提交Cookie模式（无状态）
r.Use(middleware.DoubleSubmitCookie())

// API模式
r.Use(middleware.CSRFForAPI())

// 获取Token
token := middleware.GetCSRFToken(c)
```

### 8.3 限流（支持 Redis 分布式限流）

```go
// 初始化限流器
middleware.InitRateLimiters()

// 内存限流（单实例）
r.POST("/login", middleware.LoginRateLimit(), handler.Login)     // 每分钟10次
r.POST("/upload", middleware.UploadRateLimit(), handler.Upload)  // 每分钟20次
r.Use(middleware.APIRateLimit())                                  // 每分钟100次

// 自定义内存限流
r.Use(middleware.CustomRateLimit(50, time.Minute)) // 每分钟50次

// Redis 分布式限流（多实例共享）
r.Use(middleware.RedisRateLimit("api_limit", 100))        // 每分钟100次
r.Use(middleware.LoginRedisRateLimit())                    // 登录限流
r.Use(middleware.APIRedisRateLimit())                      // API限流
r.Use(middleware.UploadRedisRateLimit())                   // 上传限流

// 自定义 Redis 限流
r.Use(middleware.CustomRedisRateLimit("custom", 50, time.Minute))

// 自定义标识限流（如按用户ID）
r.Use(middleware.RedisRateLimitWithIdentifier("user_limit", 100, func(c *gin.Context) string {
    return fmt.Sprintf("user:%d", middleware.GetUserID(c))
}))

// 停止限流器（应用关闭时）
defer middleware.StopRateLimiters()
```

**内存限流 vs Redis 限流：**
- 内存限流：单实例使用，简单高效
- Redis 限流：多实例共享，滑动窗口算法，分布式场景必需

### 8.4 路由架构

框架提供灵活的路由系统，支持模块化、版本化 API 和中间件分组。

#### 8.4.1 模块化路由

```go
import "github.com/EthanCodeCraft/xlgo-core/router"

// 方式一：实现 Module 接口
type UserModule struct{}

func (m *UserModule) Name() string { return "user" }
func (m *UserModule) Register(r *gin.RouterGroup) {
    r.GET("/users", listUsers)
    r.POST("/users", createUser)
    r.GET("/users/:id", getUser)
}

// 方式二：函数式注册
router.ModuleFunc(func(r *gin.RouterGroup) {
    r.GET("/users", listUsers)
})

// 使用
app := xlgo.New(
    xlgo.WithModules(&UserModule{}),
)
```

#### 8.4.2 版本化 API

```go
// 单版本
v1 := router.NewVersion("v1", "/api/v1")
v1.AddModuleFunc("user", func(r *gin.RouterGroup) {
    r.GET("/users", listUsersV1)
})

// 多版本共存
v1 := router.NewVersion("v1", "/api/v1")
v1.AddModuleFunc("user", func(r *gin.RouterGroup) {
    r.GET("/users", listUsersV1)  // 旧版本
})

v2 := router.NewVersion("v2", "/api/v2")
v2.AddModuleFunc("user", func(r *gin.RouterGroup) {
    r.GET("/users", listUsersV2)          // 新版本
    r.GET("/users/:id/profile", getProfile) // 新增功能
})

app := xlgo.New(xlgo.WithVersions(v1, v2))

// 结果：
// GET /api/v1/users  -> listUsersV1
// GET /api/v2/users  -> listUsersV2
```

#### 8.4.3 版本级中间件

```go
v1 := router.NewVersion("v1", "/api/v1",
    middleware.CustomRateLimit(100, time.Minute), // 版本级限流
    middleware.AuthRequired(),                    // 版本级认证
)
v1.AddModuleFunc("user", userRoutes)

// 该版本所有路由自动应用限流和认证
```

#### 8.4.4 中间件分组

```go
// 创建分组
authGroup := router.NewMiddlewareGroup("auth",
    middleware.AuthRequired(),
    middleware.RequireUserTypes("tenant_admin", "platform_admin"),
)

publicGroup := router.NewMiddlewareGroup("public",
    middleware.CustomRateLimit(1000, time.Minute),
)

// 注册分组
registry := router.NewRegistry(engine)
registry.RegisterMiddlewareGroup(authGroup)
registry.RegisterMiddlewareGroup(publicGroup)

// 使用分组
userGroup := router.GroupWithMiddlewareGroup(engine, "/users", "auth")
userGroup.GET("/:id", getUser)
```

#### 8.4.5 RESTful CRUD

```go
registry.RegisterModuleFunc("product", func(r *gin.RouterGroup) {
    rest := router.NewRESTful(r, "/products")

    // 一行注册标准 CRUD
    rest.CRUD(
        listProducts,    // GET /products
        getProduct,      // GET /products/:id
        createProduct,   // POST /products
        updateProduct,   // PUT /products/:id
        deleteProduct,   // DELETE /products/:id
    )
})

// 部分 CRUD（只注册需要的）
rest := router.NewRESTful(r, "/articles")
rest.CRUD(listArticles, getArticle, createArticle, nil, nil)
```

#### 8.4.6 全局注册方式

```go
engine := gin.New()
router.Init(engine)

// 全局中间件
router.Use(middleware.CORS(), middleware.Logger())

// 注册模块
router.RegisterModule(&UserModule{})
router.RegisterModuleFunc("health", func(r *gin.RouterGroup) {
    r.GET("/health", healthCheck)
})

// 注册版本
router.RegisterVersion(router.NewVersion("v1", "/api/v1"))

// 应用路由
router.Apply()

xlgo.StartServer(engine, 8080)
```

#### 8.4.7 默认路由

v1.0.2 起，`xlgo.New()` 默认是 **轻量应用**，不会自动注册任何默认路由。
按需通过下列 Option 显式启用：

```go
xlgo.New(
    xlgo.WithHealthRoutes(),  // 注册 /health
    xlgo.WithSwaggerRoutes(), // 注册 /swagger/*any
    // 或一步到位：
    xlgo.WithDefaultRoutes(), // 同时注册 /health 与 /swagger/*any
)
```

也可以使用 `xlgo.NewFullStack(...)` / `xlgo.RunFullStack(...)` 启用全部默认组件。

> 生产环境建议关闭 Swagger，仅保留 `WithHealthRoutes()`，避免文档接口意外暴露。

---

## 9. 认证与授权

### 9.1 JWT 使用（黑名单优化版）

**使用 JTI（JWT ID）替代完整 Token 存储黑名单，大幅节省 Redis 内存：**

```go
import "github.com/EthanCodeCraft/xlgo-core/jwt"

// 生成Token（自动包含唯一 JTI）
token, err := jwt.GenerateToken(userID, username, "admin", "admin")

// 解析Token
claims, err := jwt.ParseToken(tokenString)

// 使Token失效（使用 JTI，内存占用约 30 字节）
jwt.InvalidateToken(tokenString)

// 获取 Token 的 JTI
jti, err := jwt.GetJTI(tokenString)

// 直接通过 JTI 撤销 Token
jwt.InvalidateTokenByID(jti, expiryTime)

// 自定义过期时间
token, err := jwt.GenerateTokenWithCustomExpiry(userID, username, "admin", "admin", 3600)

// 刷新Token（旧Token自动加入黑名单）
newToken, err := jwt.RefreshToken(oldToken)

// 获取已过期Token的信息（不验证过期）
claims, err := jwt.GetClaimsFromToken(tokenString)
```

**黑名单优化说明：**
- 每个Token自动生成唯一JTI（约24字节）
- 黑名单键名：`jwt_bl:{jti}`（而非完整Token）
- 内存节省：从数百字节降到约30字节每条记录

### 9.2 获取用户信息

```go
// 一次性取出全部认证信息（v1.0.2 推荐）
if user, ok := middleware.GetAuthUser(c); ok {
    _ = user.UserID
    _ = user.Username
    _ = user.Role
    _ = user.UserType
}

// 单字段便捷函数（与旧版兼容）
userID := middleware.GetUserID(c)
username := middleware.GetUsername(c)
userType := middleware.GetUserType(c) // super_admin/admin/staff 等，由业务定义
```

### 9.3 密码加密

```go
import "github.com/EthanCodeCraft/xlgo-core/validation"

// 加密密码
hash, err := validation.HashPassword("password123")

// 验证密码
if validation.CheckPassword(hash, "password123") {
    // 密码正确
}

// 验证并自动升级成本
match, needUpgrade, newHash, err := validation.CheckPasswordAndUpgrade(hash, password, 12)
```

---

## 10. 文件存储

### 10.1 本地存储

```yaml
storage:
  driver: local
  local:
    path: ./public
    base_url: http://localhost:8080/public
```

### 10.2 阿里云 OSS

```yaml
storage:
  driver: oss
  oss:
    endpoint: oss-cn-hangzhou.aliyuncs.com
    bucket: my-bucket
    access_key_id: your_key
    access_key_secret: your_secret
    base_url: https://my-bucket.oss-cn-hangzhou.aliyuncs.com
```

### 10.3 使用存储

```go
import "github.com/EthanCodeCraft/xlgo-core/storage"

// 初始化
storage.Init(&cfg.Storage)

// 上传文件（从请求中获取）
file, _ := c.FormFile("file")
path, err := storage.Upload(file, "images")

// 获取访问 URL
url := storage.GetURL(path)

// 上传字节数据
path, err := storage.UploadFromBytes(data, "avatar.jpg", "images")

// 删除文件
err := storage.Delete(path)

// 获取文件内容
data, err := storage.Get(path)

// 检查文件是否存在
if storage.Exists(path) {
    // 文件存在
}
```

---

## 11. 工具函数库

### 11.1 随机数生成

```go
import "github.com/EthanCodeCraft/xlgo-core/utils"

// 随机字符串（16位）
token := utils.RandString(16)

// 随机数字（6位验证码）
code := utils.RandDigit(6)

// 范围随机数
n := utils.RandInt(1, 100)
```

### 11.2 字符串处理

```go
// 检查空白
if utils.IsBlank(str) { }

// 批量检查
if utils.IsAnyBlank(name, email, phone) { }

// 默认值
name := utils.DefaultIfBlank(name, "未知")

// Unicode长度（中文）
len := utils.StrLen("你好世界") // 4

// 截取子串（支持中文）
sub := utils.Substr("你好世界", 0, 2) // "你好"

// 不区分大小写比较
if utils.EqualsIgnoreCase("Admin", "admin") { }
```

### 11.3 时间日期

```go
// 时间戳
now := utils.NowUnix()        // 秒
now := utils.NowTimestamp()   // 毫秒

// 时间戳转时间
t := utils.FromUnix(unix)
t := utils.FromTimestamp(ms)

// 格式化
s := utils.FormatDateTime(t)  // "2006-01-02 15:04:05"
s := utils.FormatDate(t)      // "2006-01-02"

// 获取当天开始/结束
start := utils.StartOfDay(t)
end := utils.EndOfDay(t)

// 获取当月开始/结束
start := utils.StartOfMonth(t)
end := utils.EndOfMonth(t)
```

### 11.4 类型转换

```go
// 字符串转数字
n := utils.ToInt("123")
n := utils.ToIntDefault("abc", 0)  // 失败返回默认值

n := utils.ToInt64("123")
f := utils.ToFloat64("3.14")

// 分页计算
totalPages := utils.CalcPageCount(100, 10)  // 10页
offset := utils.CalcOffset(2, 20)           // 20
```

### 11.5 文件操作

```go
// 检查存在
utils.FileExists(path)
utils.DirExists(path)

// 确保目录存在
utils.EnsureDir(path)

// 读写文件
data, err := utils.ReadFile(path)
err := utils.WriteFile(path, data)
err := utils.AppendFile(path, []byte("\nnew line"))

// 复制文件
err := utils.CopyFile(dst, src)

// 获取文件大小
size, err := utils.FileSize(path)
```

### 11.6 格式验证

```go
// 手机号
if utils.IsPhone("13812345678") { }

// 邮箱
if utils.IsEmail("test@example.com") { }

// IPv4
if utils.IsIPv4("192.168.1.1") { }

// 身份证
if utils.IsIDCard(id) { }

// 纯数字
if utils.IsNumeric(str) { }

// 字母数字
if utils.IsAlphanumeric(str) { }
```

### 11.7 HTTP 客户端（连接池优化版）

```go
import "github.com/EthanCodeCraft/xlgo-core/utils"

// 创建客户端（Transport 在初始化时创建，连接池可复用）
client := utils.NewHTTPClient()

// 自定义配置
client = utils.NewHTTPClientWithConfig(utils.HTTPClientConfig{
    Timeout:             30 * time.Second,
    MaxIdleConns:        100,              // 最大空闲连接数
    MaxIdleConnsPerHost: 10,               // 每主机最大空闲连接
    IdleConnTimeout:     90 * time.Second, // 空闲连接超时
})

// 链式配置
client.SetTimeout(30 * time.Second)
client.SetHeader("Authorization", "Bearer xxx")
client.SetSkipTLS(false) // 生产环境建议设为 false

// GET请求
data, err := client.Get(url, map[string]string{"page": "1"})

// POST表单
data, err := client.Post(url, map[string]string{"name": "test"})

// POST JSON
data, err := client.PostJSON(url, map[string]any{"name": "test"})

// PUT请求
data, err := client.Put(url, map[string]any{"id": 1})

// DELETE请求
data, err := client.Delete(url)

// 上传文件
files := []utils.UploadFile{
    {FieldName: "file", FilePath: "/path/to/file"},
}
data, err := client.Upload(url, files, params)

// 从字节数据上传
data, err := client.UploadFromBytes(url, "file", "image.jpg", fileData, params)

// 自定义请求
data, err := client.Request("PATCH", url, bodyData)

// 关闭客户端（释放连接池资源）
client.Close()
```

**HTTPClient 特性：**
- Transport 初始化时创建，连接池可复用
- 支持连接池参数配置
- 全局默认客户端：`utils.DefaultHTTPClient()`
- 快捷函数：`utils.HTTPGet/HTTPPost/HTTPPostJSON`

### 11.8 UUID

```go
// UUID v4
uuid := utils.UUID()         // "550e8400-e29b-41d4-a716-446655440000"

// 短UUID（无横线）
uuid := utils.UUIDShort()    // "550e8400e29b41d4a716446655440000"

// 验证UUID
if utils.UUIDValid(uuid) { }
```

### 11.9 函数评估表

| 分类 | 高分函数（⭐⭐⭐⭐⭐） | 说明 |
|------|---------------------|------|
| **随机** | `RandString/RandDigit` | sync.Pool 优化性能 |
| **字符串** | `IsBlank/DefaultIfBlank/StrLen` | 空值处理、Unicode支持 |
| **时间** | `FormatDateTime/StartOfDay/EndOfMonth` | 标准格式、边界计算 |
| **转换** | `ToIntDefault/CalcPageCount/CalcOffset` | 安全转换、分页计算 |
| **文件** | `FileExists/DirExists/EnsureDir/CopyFile` | 路径检查、目录创建 |
| **验证** | `IsIPv4/IsNumeric/IsAlphanumeric` | 格式验证 |
| **URL** | `ParseURL/URLBuilder` | 链式构建URL |
| **加密** | `SHA256/Base64Encode/Base64URLEncode` | 安全哈希、编码 |
| **HTTP** | `NewHTTPClient/Get/PostJSON/Upload` | 连接池、链式调用 |
| **UUID** | `UUID/UUIDShort` | UUID生成 |

**设计改进点：**

| 改进 | 说明 |
|------|------|
| 性能优化 | `RandString/RandDigit` 使用 sync.Pool 复用随机源 |
| 类型安全 | 移除使用反射的函数，保持类型安全 |
| 式调用 | `HTTPClient` 和 `URLBuilder` 支持链式调用 |
| 零依赖 | 仅依赖 `google/uuid`，其余使用标准库 |

---

## 12. 实时通信

### 12.1 SSE 流式响应

```go
import "github.com/EthanCodeCraft/xlgo-core/sse"

// AI对话场景
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

// 手动写入
writer, _ := sse.NewSSEWriter(c)
writer.WriteEvent("message", "Hello")
writer.WriteMessage("World")
writer.WriteDone()
```

### 12.2 WebSocket

```go
import "github.com/EthanCodeCraft/xlgo-core/ws"

// 简单使用
r.GET("/ws", ws.HandleFunc(func(conn *ws.Connection, message []byte) {
    conn.SendText("收到: " + string(message))
}))

// 广播模式
hub := ws.NewHub()
go hub.Run()

// 注册连接
hub.Register(conn)

// 广播消息
hub.Broadcast([]byte("广播消息"))

// 获取连接数
count := hub.Count()
```

---

## 13. 定时任务

### 13.1 添加任务

```go
import "github.com/EthanCodeCraft/xlgo-core/cron"

// 间隔执行（每5分钟）
cron.AddTask("cleanup", cron.Every(5*time.Minute), func(ctx context.Context) error {
    return cleanupOldData()
})

// 每天固定时间（凌晨2点）
cron.AddTask("report", cron.Daily(2, 0), generateReport)

// 每周执行（周一上午10点）
cron.AddTask("weekly", cron.Weekly(time.Monday, 10, 0), weeklyTask)

// 简化 Cron（分钟+小时）
cron.AddTask("noon", cron.Cron("0", "12"), doSomething) // 每天12:00

// 完整 Cron 表达式（5字段）
cron.AddTask("complex", cron.ParseCron("*/15 * * * *"), doSomething) // 每15分钟
cron.AddTask("monthly", cron.ParseCron("0 0 1 * *"), doSomething)    // 每月1号凌晨
cron.AddTask("workday", cron.ParseCron("0 9-17 * * 1-5"), doSomething) // 工作日9-17点
```

### 13.2 启动与停止

```go
// 启动调度器
cron.Start()

// 停止调度器
cron.Stop()

// 获取调度器实例进行更多操作
scheduler := cron.GetScheduler()
scheduler.RemoveTask("cleanup")     // 移除任务
tasks := scheduler.ListTasks()      // 查看任务列表
scheduler.RunTask("report")         // 立即执行任务
scheduler.DisableTask("cleanup")    // 禁用任务
scheduler.EnableTask("cleanup")     // 启用任务
```

---

## 14. 链路追踪

### 14.1 初始化

```go
import "github.com/EthanCodeCraft/xlgo-core/trace"

trace.Init(trace.Config{
    Enabled:       true,
    ServiceName:   "my-service",
    Endpoint:      "localhost:4318",
    ExporterType:  "otlp-http",
    SampleRatio:   1.0,
})
defer trace.Close(ctx)
```

### 14.2 使用中间件

```go
// 自动追踪所有请求
r.Use(trace.Middleware("my-service"))

// 响应头自动添加 X-Trace-ID
```

### 14.3 业务追踪

```go
// 创建子Span
ctx, span := trace.StartSpan(c, "db_query")
defer span.End()

// 记录错误
trace.RecordError(c, err)

// 添加属性
trace.SetAttribute(c, "user_id", 123)

// 获取TraceID
traceID := trace.GetTraceID(c)
```

---

## 15. 压缩解压

### 15.1 Gzip

```go
import "github.com/EthanCodeCraft/xlgo-core/compress"

// 压缩数据
compressed, err := compress.GzipCompress(data)

// 解压数据
data, err := compress.GzipDecompress(compressed)

// 压缩文件
err := compress.GzipCompressFile("src.txt", "dst.gz")

// 解压文件
err := compress.GzipDecompressFile("src.gz", "dst.txt")
```

### 15.2 Zip

```go
// 打包文件/目录
err := compress.Zip("archive.zip", []string{"file1.txt", "dir/"})

// 解压到目录
err := compress.Unzip("archive.zip", "./output")
```

---

## 16. 测试工具

### 16.1 API 测试

```go
import "github.com/EthanCodeCraft/xlgo-core/test"

func TestUserAPI(t *testing.T) {
    router := test.SetupRouter()

    // POST请求
    resp := test.POST(router, "/api/users").
        WithJSON(map[string]any{"name": "test"}).
        Execute()
    resp.AssertOK(t)
    resp.AssertCode(t, 1)

    // GET请求
    resp = test.GET(router, "/api/users/1").Execute()
    resp.AssertOK(t)
    resp.AssertJSONKeyExists(t, "data.id")
}
```

---

## 17. CLI 脚手架

### 17.1 创建项目

```bash
# 创建新项目
xlgo new myproject

# 指定模块名
xlgo new myproject --module github.com/company/myproject
```

### 17.2 生成代码

```bash
# 生成Handler
xlgo make handler user

# 生成Model
xlgo make model user

# 生成Repository
xlgo make repository user

# 生成Service
xlgo make service user
```

---

## 18. 最佳实践

### 18.1 项目分层

```
handler/    → 接收请求、参数验证、调用Service
service/    → 业务逻辑、事务管理
repository/ → 数据访问、CRUD操作
model/      → 数据模型定义
```

### 18.2 错误处理

```go
func GetUser(c *gin.Context) {
    id := handler.PathInt64(c, "id", 0)
    if id == 0 {
        // 参数错误码由业务侧定义；这里直接返回通用失败 + 自定义消息
        response.Fail(c, "参数错误")
        return
    }

    user, err := userService.GetByID(id)
    if err != nil {
        trace.RecordError(c, err)
        response.FailWithError(c, response.ErrUserNotFound)
        return
    }

    response.Success(c, user)
}
```

### 18.3 多站点配置

```yaml
# A站点
app:
  site_name: "site_a"

# B站点
app:
  site_name: "site_b"
```

缓存自动隔离：

- site_a: `cache:site_a:user:1`
- site_b: `cache:site_b:user:1`

### 18.4 环境区分

```go
if cfg.IsProduction() {
    gin.SetMode(gin.ReleaseMode)
    // 生产环境收紧 console 输出（仅保留 Warn / Error）
    // 业务事件请使用 logger 包记录
    console.SetLevel(console.LevelWarn)
}
```

---

## 附录：完整示例

### 用户登录接口

```go
package handler

import (
    "github.com/EthanCodeCraft/xlgo-core/cache"
    "github.com/EthanCodeCraft/xlgo-core/response"
    "github.com/EthanCodeCraft/xlgo-core/validation"
    "github.com/gin-gonic/gin"
)

type LoginRequest struct {
    Username string `json:"username" label:"用户名" validate:"required" msg_required:"用户名不能为空"`
    Password string `json:"password" label:"密码" validate:"required,password" msg_required:"密码不能为空"`
}

func Login(c *gin.Context) {
    var req LoginRequest
    if !validation.ShouldBindAndValidate(c, &req) {
        return
    }

    // 查询用户
    user, err := userService.GetByUsername(req.Username)
    if err != nil {
        response.FailWithError(c, response.ErrUserNotFound)
        return
    }

    // 验证密码
    if !validation.CheckPassword(user.Password, req.Password) {
        response.FailWithError(c, response.ErrPasswordWrong)
        return
    }

    // 生成Token
    token, _ := jwt.GenerateToken(user.ID, user.Username, "admin", "admin")

    // 缓存用户信息
    cache.GetCache().Set(c.Request.Context(),
        cache.KSession(token),
        user,
        time.Duration(cfg.JWT.Expire)*time.Second)

    response.Success(c, gin.H{
        "token":    token,
        "user_id":  user.ID,
        "username": user.Username,
    })
}
```

---

_文档版本: v1.0.2_
_最后更新: 2026-06-20_
