# xlgo v1.0.2 更新计划

> 版本定位：面向新框架的激进整理版本。由于当前框架仍处于早期阶段，v1.0.2 不以保守兼容为首要目标，而是优先修正已经发现的架构边界、默认行为、文档一致性和长期演进风险，避免在用户规模扩大后再进行破坏式调整。

## 0. 实施状态（2026-06-20）

✅ **本计划全部 11 项已完成。**

| 节 | 任务 | 状态 | 主要落点 |
|---|---|---|---|
| 2.1 | 权限中间件去业务化 | ✅ | `middleware/auth.go`、`middleware/auth_test.go` |
| 2.2 | App 启动流程可组合 | ✅ | `app.go`（Option 全套 + `enableAutoMigrate`）、`app_test.go` |
| 2.3 | 框架内部禁止 Fatalf 退出 | ✅ | `app.go` 全部 `return fmt.Errorf(...)` |
| 2.4 | Shutdown 关闭流程修正 | ✅ | `app.go` 使用 `database.CloseAll()` + `errors.Join` |
| 2.5 | 默认路由和 Swagger 可控化 | ✅ | `router/router.go`（`RegisterHealthRoute` / `RegisterSwaggerRoutes`） |
| 2.6 | 健康检查标准化 | ✅ | `/health` 支持 checks + 503，`router/health_test.go` |
| 2.7 | 配置系统 | ✅ | `config.Manager` + `config.SetDefaultManager`，App 真正驱动私有 manager |
| 2.8 | 数据库全局状态治理 | ✅ | `database.Manager`、`ReplicaPicker`、私有 `dbModeContextKey{}`、可插拔方言注册表 |
| 2.9 | AutoMigrate 机制 | ✅ | `WithMigrator` / `WithModels` / `WithAutoMigrate` / `WithoutAutoMigrate` |
| 2.10 | README 更新日志版本错误 | ✅ | README 重写为 v1.0.2 / v1.0.1 / v1.0.0 线 |
| 2.11 | 文档与 CLI 模板同步 | ✅ | GUIDE 1.3 / 3.2 / 4.1 / 8.4.7 / 9.2 更新；CLI 模板补 Swagger/MySQL 注释 |

### v1.0.2 在计划之外的额外收益

- **可插拔方言注册表**：原计划只要求"支持 mysql/postgres"，实际实现升级为 `database.RegisterDialect(DialectSpec{...})` 注册表，应用可一次接入任意 GORM 驱动（SQLite、SQL Server、ClickHouse、TiDB...），DSN 构建器同步登记到 `config` 包。
- **`config.SetDefaultManager`**：让 App 持有的 `config.Manager` 能成为全局便捷函数（`config.Get`/`GetString`...）的数据源，解决 "App 私有 manager 与全局 API 双轨" 的问题。
- **`WithAutoMigrate` / `WithoutAutoMigrate`**：与 `WithMigrator`/`WithModels` 解耦的迁移开关，覆盖"注册了迁移器但需临时关闭"的场景。

### 验证

```bash
go build -buildvcs=false ./...   # ✅ 通过
go vet -buildvcs=false ./...     # ✅ 通过
go test -buildvcs=false ./...    # ✅ 全部 23 个包 PASS
```

---

## 1. 版本原则

### 1.1 本次版本核心目标

v1.0.2 的目标是把 xlgo 从“业务项目沉淀出来的工具集合”进一步整理为“通用 Go Web 框架”：

1. **框架与业务解耦**：框架层不写死具体业务角色、业务流程和业务默认值。
2. **启动流程可组合**：MySQL、Redis、Storage、Swagger、默认路由、AutoMigrate 等能力应可显式启用或关闭。
3. **错误处理专业化**：框架内部不直接 `Fatalf` 退出进程，而是向调用方返回错误。
4. **生命周期清晰化**：初始化、运行、关闭流程应具备明确边界，并为后续 lifecycle hooks 打基础。
5. **高可用基础增强**：健康检查、优雅关闭、连接关闭、依赖状态检查等能力逐步标准化。
6. **文档真实可信**：README、GUIDE、CLI 模板、更新日志与真实版本和代码行为保持一致。
7. **坚持 Go 1.25**：本项目作为新框架，明确使用 Go 1.25，不背负旧版本兼容包袱，允许使用 Go 1.25 的新特性。

### 1.2 兼容策略

v1.0.2 可以接受破坏式更新，但破坏必须有明确收益：

- 可以调整不合理 API。
- 可以删除或废弃明显业务化的设计。
- 可以改变默认启动策略，只要文档和迁移说明清晰。
- 可以统一命名、配置、模块边界。
- 不为了兼容历史写法牺牲框架长期设计。

建议保留部分快捷 API，但应明确其定位为“默认封装”而非“框架唯一模型”。

---

## 2. 必须修正的问题清单

## 2.1 权限中间件去业务化

### 当前问题

`middleware/auth.go` 中的权限中间件写死了以下用户类型：

```go
super_admin
admin
staff
```

典型代码：

```go
if ut != "super_admin" && ut != "admin" {
    response.Fail(c, "无权限访问")
    c.Abort()
    return
}
```

这属于具体业务系统权限模型，不应该成为通用框架的固定规则。

### 目标设计

将固定角色改为默认角色，并提供通用权限能力。

### 新增默认常量

```go
const (
    DefaultUserTypeSuperAdmin = "super_admin"
    DefaultUserTypeAdmin      = "admin"
    DefaultUserTypeStaff      = "staff"
)
```

这些常量仅作为默认值和快速开始示例使用，不代表用户必须采用这些角色名。

### 新增认证用户结构

```go
type AuthUser struct {
    UserID   uint
    Username string
    Role     string
    UserType string
}
```

### 新增统一获取当前用户方法

```go
func GetAuthUser(c *gin.Context) (AuthUser, bool)
```

该方法从 Gin Context 中读取：

- `ContextKeyUserID`
- `ContextKeyUsername`
- `ContextKeyRole`
- `ContextKeyUserType`

### 新增通用权限中间件

#### 按 user_type 判断

```go
func RequireUserTypes(userTypes ...string) gin.HandlerFunc
```

示例：

```go
middleware.RequireUserTypes("tenant_admin", "platform_admin")
```

#### 按 role 判断

```go
func RequireRoles(roles ...string) gin.HandlerFunc
```

示例：

```go
middleware.RequireRoles("owner", "manager")
```

#### 自定义权限判断

```go
type AuthChecker func(user AuthUser, c *gin.Context) bool

func RequireAuth(checker AuthChecker, messages ...string) gin.HandlerFunc
```

示例：

```go
middleware.RequireAuth(func(user middleware.AuthUser, c *gin.Context) bool {
    return user.UserType == "merchant" && user.Role == "owner"
})
```

### 调整原快捷方法

以下方法可以保留，但必须改为基于通用能力实现：

```go
func AdminRequired() gin.HandlerFunc {
    return RequireUserTypes(DefaultUserTypeSuperAdmin, DefaultUserTypeAdmin)
}

func SuperAdminRequired() gin.HandlerFunc {
    return RequireUserTypes(DefaultUserTypeSuperAdmin)
}

func StaffRequired() gin.HandlerFunc {
    return RequireUserTypes(DefaultUserTypeStaff)
}

func AnyUserRequired() gin.HandlerFunc {
    return RequireUserTypes(
        DefaultUserTypeSuperAdmin,
        DefaultUserTypeAdmin,
        DefaultUserTypeStaff,
    )
}
```

### 测试要求

新增或完善 `middleware/auth_test.go`：

- 未登录访问被拒绝。
- Context 中缺少用户信息时被拒绝。
- `user_type` 类型异常时被拒绝。
- `RequireUserTypes("tenant_admin")` 可通过自定义用户类型。
- `RequireUserTypes("tenant_admin")` 会拒绝其他用户类型。
- `RequireRoles("owner")` 可通过自定义角色。
- `RequireAuth` 可执行复杂自定义判断。
- 默认快捷方法仍符合默认常量语义。

---

## 2.2 App 启动流程重构为可组合模式

### 当前问题

`app.go` 的 `Run()` 当前强制执行：

```go
logger.Init(cfg)
database.InitMySQL(cfg)
database.InitRedis(cfg)
database.AutoMigrate()
storage.Init(&cfg.Storage)
wire.InitServices()
router.RegisterDefaultRoutes(a.router)
```

这会导致：

- 纯 HTTP 服务也必须配置 MySQL。
- 不使用 Redis 的项目也被强制初始化 Redis。
- 不使用文件上传的项目也被强制初始化 Storage。
- 用户无法控制 AutoMigrate。
- 用户无法控制默认路由和 Swagger 暴露。
- 测试和最小示例成本过高。

### 目标设计

v1.0.2 应将 App 启动流程改为“显式、可组合、可关闭”。

### 建议新增 App 内部字段

```go
type App struct {
    config   *config.Config
    router   *gin.Engine
    registry *router.Registry
    server   *http.Server

    configPath string

    enableLogger        bool
    enableMySQL         bool
    enableRedis         bool
    enableStorage       bool
    enableDefaultRoutes bool
    enableAutoMigrate   bool
    enableWire          bool
}
```

### 默认策略

由于可以激进调整，建议从 v1.0.2 开始采用更清晰的默认策略：

- `logger` 默认开启。
- `default routes` 默认开启。
- `wire` 默认开启。
- `MySQL`、`Redis`、`Storage` 是否默认开启需要结合快速开始体验决定。

推荐方案：

1. `xlgo.New()` 创建的是轻量 App，不强制初始化 MySQL/Redis/Storage。
2. 用户通过 Option 显式启用组件：

```go
xlgo.WithMySQL()
xlgo.WithRedis()
xlgo.WithStorage()
xlgo.WithAutoMigrate()
```

3. 提供 `xlgo.NewFullStack()` 或 `xlgo.RunFullStack()` 作为 batteries-included 快捷方式。

如果希望过渡成本更低，也可以保留 `New()` 默认全量初始化，但必须提供 `WithoutXxx()`。不过从长期框架设计看，更推荐“显式启用依赖”。

### 建议新增 Option

#### 配置相关

```go
func WithConfigPath(path string) Option
func WithConfig(cfg *config.Config) Option
```

必须修复当前 `WithConfigPath` 空实现问题。

#### 组件启用

```go
func WithLogger() Option
func WithMySQL() Option
func WithRedis() Option
func WithStorage() Option
func WithAutoMigrate() Option
func WithWire() Option
func WithDefaultRoutes() Option
```

#### 组件关闭

如果保留默认开启策略，则必须同时提供：

```go
func WithoutLogger() Option
func WithoutMySQL() Option
func WithoutRedis() Option
func WithoutStorage() Option
func WithoutAutoMigrate() Option
func WithoutWire() Option
func WithoutDefaultRoutes() Option
```

### 推荐使用示例

#### 最小 HTTP 服务

```go
app := xlgo.New(
    xlgo.WithConfigPath("./config.yaml"),
    xlgo.WithDefaultRoutes(),
)
```

#### 标准业务 API

```go
app := xlgo.New(
    xlgo.WithConfigPath("./config.yaml"),
    xlgo.WithLogger(),
    xlgo.WithMySQL(),
    xlgo.WithRedis(),
    xlgo.WithAutoMigrate(),
    xlgo.WithDefaultRoutes(),
    xlgo.WithModules(user.Module{}, order.Module{}),
)
```

#### 文件上传服务

```go
app := xlgo.New(
    xlgo.WithConfigPath("./config.yaml"),
    xlgo.WithStorage(),
)
```

### 测试要求

- `WithConfigPath` 能真实影响配置加载。
- `WithConfig` 能注入配置并运行。
- 未启用 MySQL 时不访问 MySQL 配置。
- 未启用 Redis 时不访问 Redis 配置。
- 未启用 Storage 时不初始化 Storage。
- 关闭默认路由后 `/health` 不注册。
- 显式开启默认路由后 `/health` 可访问。

---

## 2.3 框架内部禁止直接 Fatalf 退出

### 当前问题

`app.go` 中存在：

```go
logger.Fatalf("初始化 MySQL 失败: %v", err)
```

框架内部直接退出进程会让调用方无法处理错误。

### 改造目标

所有初始化错误向上返回。

### 改造示例

```go
if err := database.InitMySQL(cfg); err != nil {
    return fmt.Errorf("初始化 MySQL 失败: %w", err)
}
```

需要覆盖：

- logger 初始化失败。
- MySQL 初始化失败。
- Redis 初始化失败。
- AutoMigrate 失败。
- Storage 初始化失败。
- HTTP Server 启动失败。

### 要求

框架包内部原则上不调用：

```go
os.Exit
log.Fatal
logger.Fatal
logger.Fatalf
panic
```

除非是明确的开发期 helper 或 CLI 命令入口。

---

## 2.4 Shutdown 关闭流程修正

### 当前问题

`App.Shutdown()` 当前调用：

```go
database.Close()
database.CloseRedis()
```

在读写分离场景下，从库连接可能不会被关闭。

### 改造目标

使用：

```go
database.CloseAll()
database.CloseRedis()
```

并聚合错误。

### 建议实现

Go 1.25 可直接使用 `errors.Join`：

```go
var errs []error

if err := database.CloseAll(); err != nil {
    errs = append(errs, err)
}
if err := database.CloseRedis(); err != nil {
    errs = append(errs, err)
}

return errors.Join(errs...)
```

### 测试要求

- 未初始化数据库时 `Shutdown` 不 panic。
- 初始化 replicas 后 `CloseAll` 会清空 replicas 和 DBRead。
- 重复关闭不 panic。
- 关闭错误能向上返回。

---

## 2.5 默认路由和 Swagger 可控化

### 当前问题

`router.RegisterDefaultRoutes()` 当前默认注册：

```go
/swagger/*any
/health
```

Swagger 在生产环境中不应无条件暴露。

### 改造目标

拆分默认路由注册能力。

### 建议新增方法

```go
func RegisterHealthRoute(r *gin.Engine, checks ...HealthCheck)
func RegisterSwaggerRoutes(r *gin.Engine)
func RegisterDefaultRoutes(r *gin.Engine, checks ...HealthCheck)
```

其中 `RegisterDefaultRoutes` 可以继续组合调用前两个方法。

### App Option

```go
func WithHealthRoutes() Option
func WithSwaggerRoutes() Option
func WithDefaultRoutes() Option
func WithoutDefaultRoutes() Option
```

### 推荐生产策略

- 开发环境可以开启 Swagger。
- 生产环境默认不自动开启 Swagger，除非用户显式配置或调用 `WithSwaggerRoutes()`。

### 测试要求

- 健康检查路由可单独开启。
- Swagger 路由可单独开启。
- 默认路由可整体开启。
- 关闭默认路由后不注册 `/health` 和 `/swagger/*any`。

---

## 2.6 健康检查标准化

### 当前问题

当前 `/health` 只返回：

```json
{"status":"ok"}
```

高可用服务一般需要区分 liveness 和 readiness。

### v1.0.2 目标

v1.0.2 只新增并保留一个健康检查接口，避免接口过多导致使用复杂：

```text
GET /health
```

### 初始响应

无检查项时：

```json
{
  "status": "ok"
}
```

有检查项时：

```json
{
  "status": "ok",
  "checks": {
    "mysql": "ok",
    "redis": "disabled"
  }
}
```

检查失败时 `/health` 返回 HTTP 503，并将 `status` 设为 `error`。

### 建议新增健康检查抽象

```go
type HealthChecker interface {
    Name() string
    Check(ctx context.Context) error
}
```

或函数式：

```go
type HealthCheckFunc func(ctx context.Context) error
```

App 后续可支持：

```go
func WithHealthCheck(name string, check HealthCheckFunc) Option
```

v1.0.2 可以先做基础实现，为 v1.1.x 扩展预留。

---

## 2.7 配置系统问题纳入 v1.0.2 改造

### 当前问题

`config.Load()` 使用 `sync.Once`，导致：

- 测试中难以重复加载不同配置。
- 多 App 实例不友好。
- `WithConfigPath` 难以正确实现。
- 配置热更新实例边界不清晰。

### 激进改造目标

v1.0.2 可以开始引入实例化配置管理器。

### 建议新增类型

```go
type Manager struct {
    mu        sync.RWMutex
    v         *viper.Viper
    cfg       *Config
    callbacks []func(*Config)
}
```

### 建议 API

```go
func NewManager(path string) *Manager
func (m *Manager) Load() (*Config, error)
func (m *Manager) LoadWithWatch(onChange func(*Config)) (*Config, error)
func (m *Manager) Get() *Config
func (m *Manager) Reload() error
func (m *Manager) RegisterCallback(cb func(*Config))
```

### 全局兼容层

可以保留全局函数，但内部基于默认 manager：

```go
func Load(path string) (*Config, error)
func Get() *Config
func Set(cfg *Config)
func Reload() error
```

### 关键要求

- 去除全局 `sync.Once` 对重复加载的限制，或提供 `ResetForTest()`。
- App 优先持有自己的 config manager，而不是强依赖全局配置。
- 全局 API 只作为便捷入口。

---

## 2.8 数据库全局状态治理

### 当前问题

`database/mysql.go` 使用全局变量：

```go
var (
    DB *gorm.DB
    DBRead *gorm.DB
    replicas []*gorm.DB
)
```

这对框架长期发展不利。

### v1.0.2 目标

引入数据库 Manager，逐步替代全局变量。

### 建议类型

```go
type Manager struct {
    master   *gorm.DB
    replicas []*gorm.DB
    picker   ReplicaPicker
}
```

### 建议 API

```go
func NewManager(cfg *config.Config) *Manager
func (m *Manager) Open(ctx context.Context) error
func (m *Manager) OpenWithReplicas(ctx context.Context, replicaDSNs []string) error
func (m *Manager) Master() *gorm.DB
func (m *Manager) Replica() *gorm.DB
func (m *Manager) FromContext(ctx context.Context) *gorm.DB
func (m *Manager) Close() error
func (m *Manager) HealthCheck(ctx context.Context) error
```

### Context key 修正

当前：

```go
context.WithValue(ctx, "db_mode", "master")
```

应改为私有类型 key，避免冲突：

```go
type dbModeContextKey struct{}
```

### Replica 选择策略

v1.0.2 可先提供：

- Round-robin
- Random

后续版本再增加：

- 权重
- 健康摘除
- 熔断

### 全局兼容层

可以保留：

```go
func InitMySQL(cfg *config.Config) error
func GetDB() *gorm.DB
func GetReadDB() *gorm.DB
func CloseAll() error
```

但内部应委托给默认 Manager。

---

## 2.9 AutoMigrate 机制调整

### 当前问题

`database.AutoMigrate()` 当前是空实现：

```go
func AutoMigrate() error {
    logger.Info("数据库表结构迁移完成")
    return nil
}
```

但 `App.Run()` 强制调用它，语义不清晰。

### 改造目标

迁移应由用户显式注册。

### 建议 API

```go
type Migrator func(db *gorm.DB) error

func WithMigrator(m Migrator) Option
func WithModels(models ...any) Option
```

示例：

```go
app := xlgo.New(
    xlgo.WithMySQL(),
    xlgo.WithModels(&User{}, &Order{}),
)
```

或：

```go
app := xlgo.New(
    xlgo.WithMigrator(func(db *gorm.DB) error {
        return db.AutoMigrate(&User{}, &Order{})
    }),
)
```

### 要求

- 不再强制执行空的 `database.AutoMigrate()`。
- 如果没有注册 migrator，不执行迁移。
- 迁移错误返回给调用方。

---

## 2.10 README 更新日志版本错误修正

### 当前问题

`README.md` 当前更新日志写成：

```md
### v2.1.0 (2026-04-30)
...
### v2.0.0 (2026-04-30)
```

但当前实际版本应为 `v1.0.1`，本次计划版本为 `v1.0.2`。该日志会给用户造成误解。

### 修正目标

将 README 更新日志改为真实版本线。

### 建议改法

本次发布后 README 应包含：

```md
## 更新日志

### v1.0.2 (计划中 / 发布日期按实际填写)

- 权限中间件通用化，移除业务角色硬编码。
- App 启动流程改为可组合模式。
- 修复 WithConfigPath 空实现问题。
- 框架初始化错误改为返回 error。
- 默认路由、Swagger、健康检查可配置。
- Shutdown 关闭全部数据库连接。
- 文档和 CLI 模板同步更新。

### v1.0.1

- 根据真实已发布内容整理。

### v1.0.0

- 初始版本发布。
```

如果 v2.0.0 / v2.1.0 中的内容实际已经存在于当前代码，应归并到 v1.0.1 或 v1.0.0 的历史描述中，而不是继续使用错误的大版本号。

### 要求

- 删除或修正 `v2.0.0`、`v2.1.0` 错误标题。
- README 顶部 badge 如有版本信息，也要同步。
- GUIDE 中如有类似版本描述，统一修正。

---

## 2.11 文档与 CLI 模板同步

### 涉及文件

```text
README.md
GUIDE.md
cmd/xlgo/templates.go
```

### 必须更新内容

1. 最小启动示例。
2. 标准业务 API 启动示例。
3. 自定义权限示例。
4. 显式启用 MySQL/Redis/Storage 的示例。
5. 关闭或开启 Swagger 的示例。
6. 健康检查接口说明。
7. 配置文件完整示例。
8. Go 版本说明：明确要求 Go 1.25+。
9. 更新日志修正为 v1.x 版本线。

### CLI 模板调整方向

生成项目不应默认塞入过多不可控依赖。建议模板生成：

```go
app := xlgo.New(
    xlgo.WithConfigPath("./config.yaml"),
    xlgo.WithLogger(),
    xlgo.WithDefaultRoutes(),
)
```

如果模板选择 API/fullstack 模式，再加入：

```go
xlgo.WithMySQL()
xlgo.WithRedis()
xlgo.WithAutoMigrate()
```

后续 CLI 可支持：

```bash
xlgo new myproject --template minimal
xlgo new myproject --template api
xlgo new myproject --template fullstack
```

---

## 3. 推荐实施顺序

## Phase 1：修正业务耦合与明显 bug

1. 改造 `middleware/auth.go`。
2. 补充 `middleware/auth_test.go`。
3. 修复 `WithConfigPath` 空实现。
4. 修正 `README.md` 更新日志版本错误。

## Phase 2：重构 App 启动流程

1. App 增加组件启用/关闭选项。
2. MySQL/Redis/Storage/AutoMigrate 改为显式启用或可关闭。
3. 默认路由、Health、Swagger 拆分。
4. `Run()` 中所有初始化错误改为返回 error。

## Phase 3：生命周期和关闭流程

1. `Shutdown()` 改为关闭全部数据库连接。
2. 关闭 Redis、日志、限流器等组件时聚合错误。
3. 准备后续 lifecycle hook 的内部结构。

## Phase 4：配置和数据库管理器

1. 引入 `config.Manager`。
2. 去除或弱化全局 `sync.Once` 限制。
3. 引入 `database.Manager`。
4. 修正 DB context key。
5. 全局 API 改为 facade。

## Phase 5：健康检查和文档模板

1. 保持单一 `/health` 接口。
2. 支持依赖检查状态和失败时 HTTP 503。
3. 更新 README/GUIDE。
4. 更新 CLI 模板。
5. 更新 CHANGELOG 或 README 更新日志。

---

## 4. v1.0.2 验收标准

### 4.1 功能验收

- 用户可以创建不依赖 MySQL/Redis/Storage 的最小应用。
- 用户可以显式启用 MySQL/Redis/Storage。
- 用户可以关闭 Swagger 或默认路由。
- 用户可以使用自定义 user_type、role 或自定义函数做权限判断。
- `super_admin/admin/staff` 只作为默认常量，不再是框架唯一权限模型。
- `WithConfigPath` 能真实生效。
- 初始化失败返回 error，不直接退出进程。
- Shutdown 能关闭主库、从库、Redis 等资源。
- README 更新日志版本线修正为 v1.x。

### 4.2 测试验收

必须通过：

```bash
go test ./...
```

建议新增或更新测试覆盖：

- `middleware/auth_test.go`
- `app_test.go`
- `router/router_test.go`
- `config/config_test.go`
- `database/mysql_test.go`

### 4.3 文档验收

- README 中快速开始示例可运行。
- GUIDE 中权限示例与真实 API 一致。
- CLI 模板生成的项目可 `go test ./...` 或 `go run`。
- README 更新日志不存在错误的 v2.0.0/v2.1.0 表述。
- 文档明确写明 Go 1.25+。

---

## 5. 建议发布说明草案

```md
## v1.0.2

### Breaking Changes

- App 启动流程调整为更显式的组件启用模式，MySQL、Redis、Storage、AutoMigrate 等能力可通过 Option 控制。
- 权限中间件不再将 `super_admin`、`admin`、`staff` 作为框架固定权限模型，仅保留为默认用户类型常量。
- 框架初始化失败时返回 error，不再在框架内部直接 Fatal 退出。

### Added

- 新增 `middleware.AuthUser` 和 `middleware.GetAuthUser()`。
- 新增 `middleware.RequireUserTypes()`。
- 新增 `middleware.RequireRoles()`。
- 新增 `middleware.RequireAuth()`。
- 新增 App 组件控制 Option：`WithMySQL`、`WithRedis`、`WithStorage`、`WithAutoMigrate`、`WithDefaultRoutes` 等。
- 新增单一 `/health` 健康检查规划，支持检查项状态。
- 新增或规划实例化 `config.Manager`、`database.Manager`。

### Changed

- `AdminRequired()`、`SuperAdminRequired()`、`StaffRequired()`、`AnyUserRequired()` 改为基于通用权限中间件实现。
- `WithConfigPath()` 改为真实生效。
- `App.Run()` 初始化流程改为可组合、可返回错误。
- `App.Shutdown()` 改为关闭全部数据库连接。
- 默认路由和 Swagger 注册逻辑拆分。
- README/GUIDE/CLI 模板同步新启动方式。

### Fixed

- 修复 README 更新日志错误使用 v2.0.0/v2.1.0 的问题。
- 修复 `WithConfigPath()` 空实现问题。
- 修复读写分离场景下从库连接可能未关闭的问题。
```

---

## 6. 后续版本预告

v1.0.2 之后建议继续推进：

### v1.1.0

- 完整 Lifecycle Hooks：`OnStart`、`OnShutdown`、`OnReady`。
- 插件系统：`Plugin` / `Module` / `Provider`。
- 完整 Health Registry。
- 更完善的数据库 replica 健康摘除。

### v1.2.0

- RBAC/Permission 扩展包。
- 多租户支持基础设施。
- OpenTelemetry 深度集成。
- CLI 多模板支持。

---

## 7. 本计划结论

v1.0.2 应该是 xlgo 的一次“框架化校准”版本，而不是小修小补版本。

本次更新应优先解决：

1. 权限模型业务耦合。
2. App 启动流程不可控。
3. 初始化错误直接退出。
4. 默认 Swagger 和默认路由不可控。
5. 配置和数据库全局状态对测试、多实例不友好。
6. README 更新日志版本错误。
7. 文档与真实行为不一致。

由于 xlgo 仍是新框架，本阶段应果断修正不合理设计，明确 Go 1.25+，为后续用户增长前打好 API 和架构基础。
