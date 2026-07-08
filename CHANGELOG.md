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

## [Unreleased]

> 复审报告 `gpt_check_report_review.md` 第一优先级（致命/进程级可用性）修复。P1 共 4 项，本次发布已推进 M13 cron panic、M1 App 生命周期、M3 logger 生命周期临界区；M8 CSRF JSON body 上限随后推进。

### Breaking ⚠️

- **cache 写操作与计数器/原始 Redis helper 在 Redis 未初始化时返回 `ErrRedisNotReady`**：`Set` / `Delete` / `DeleteByPattern` / `Incr` / `IncrBy` / `Decr` / `GetTTL` / `SetExpire` / `GetRaw` / `SetRaw` 旧行为会静默返回成功或零值，调用方容易误判缓存写入、计数器更新或过期时间设置已经生效；现在统一显式返回错误。公共接口签名不变，但依赖“未启用 Redis 时当作成功”的下游需要改为忽略 `errors.Is(err, cache.ErrRedisNotReady)` 或显式启用 Redis。
- **`cache.WithLock` / `cache.WithLockAutoExtend` 未获取到锁时返回 `ErrLockNotAcquired`**：旧行为返回 `nil` 并跳过业务函数，调用方无法区分“业务执行成功”和“根本没有执行”。同时锁 TTL 小于 1ms、续期/重试间隔非正会返回显式错误，避免 Redis PX=0 或 `time.NewTicker(0)` 崩溃。
- **`jwt.ParseToken` 开始校验 issuer，`RefreshToken` 使用 `jwt.refresh_expire`**：签发者与当前配置不一致的 token 会被拒绝；刷新后的 token 过期时间优先使用 `refresh_expire`，未配置时回退 `expire`。`GenerateTokenWithCustomExpiry` 现在拒绝非正过期时间，`InvalidateTokenByID("")` 返回 `ErrEmptyJTI`。
- **`App.Init()` 由 `sync.Once` 改为生命周期状态机**（app.go，M1）：5 态 `stateCreated/Initializing/Initialized/Stopping/Stopped` + `lifecycleMu`(RWMutex) + `initMu`(Mutex)。`Shutdown` 后或 `Init` 失败后再调 `Init()` 返回新增导出错误 **`xlgo.ErrAppClosed`**（原 `sync.Once` "多次调用返回首次结果"语义不再适用——已关闭的 App 不可再 Init，需新建 App）。
- **`App.Go()` 在 Shutdown 开始或 Init 失败后为 no-op**（app.go，M1）：`state >= stateStopping` 时拒绝 `wg.Add` 直接返回，避免与 `Shutdown` 的 `wg.Wait` 竞争 `sync.WaitGroup` 契约（Add 须 happen-before Wait）。依赖"Shutdown 后仍可 Go"的下游需改用独立 goroutine。
- **生命周期 hook 不允许重入调用 `Init`/`Shutdown`/`Run`**（app.go，M1）：`initMu` 非重入，hook（OnInit/OnStart/OnReady/OnStop）内调用会自锁死锁。需在 hook 内触发关闭应改用信号通道由主流程处理。
- **`logger.DefaultLogger = m` 直接赋值不再驱动包级 facade**（logger/logger.go，M3）：为消除 `SetDefaultLogManager()` 与包级 facade 并发读取默认 manager 的裸全局指针竞态，facade 改为读取内部 atomic 快照。`logger.DefaultLogger` 仍保持 `*LogManager` 类型，旧的 `logger.DefaultLogger.Init/Close/SetLevel/GetLevel` 直接调用仍可用；替换默认 manager 请使用 `logger.SetDefaultLogManager(m)`。
- **`logger.Init` 拒绝明显非法日志配置**（logger/logger.go，M3）：空日志目录、负数 `MaxSize/MaxBackups/MaxAge` 现在直接返回错误。依赖零值日志配置启动 `WithLogger()` 的下游需显式设置 `Log.Dir` 与非负轮转参数。
- **限流器非法配置改为 fail-fast**（middleware/ratelimit.go，M8）：`NewRateLimiter` / `NewRedisRateLimiter` / `NewRedisRateLimiterFailClosed` 现在对 `rate <= 0` 或 `window <= 0` 直接 panic，避免零值窗口/零值配额静默产生不确定限流语义。下游应在配置加载阶段校验限流参数。
- **`handler.BindJSON` 默认限制 JSON body 为 1MiB**（handler/handler.go，M6）：防止入口层无上限读取请求体导致 OOM。需要更大 JSON 的接口请改用 `handler.BindJSONWithMaxBytes(c, req, maxBytes)` 显式声明上限。
- **cron 非法任务配置改为 fail-fast**（cron/cron.go，M13）：`AddTask` 拒绝 nil schedule / nil handler；`Every(<=0)`、`Daily`/`Weekly` 越界时间、非法 weekday 会 panic，避免静默生成不推进或归一化跑偏的调度。
- **repository 查询保护默认开启**（repository/repository.go，M5/N5）：`FindAll` 默认最多返回 `DefaultFindAllLimit=1000` 条；明确需要全表扫描时改用 `FindAllUnbounded`。`FindPage*` / `QueryBuilder.Page` 会归一化 `page/pageSize` 并限制 `MaxPageSize=100`、`MaxPage=10000`。`Find*Ordered` / `QueryBuilder.Order` 只接受简单字段排序（如 `created_at DESC, id ASC`），复杂表达式/raw SQL 会返回 `ErrUnsafeOrder`；`UpdateBatch` 字段名不合法返回 `ErrUnsafeField`。

### Fixed 🐛

- **M9 JWT issuer / refresh expiry 契约修复**：`ParseToken`、`InvalidateToken`、`GetClaimsFromToken` 统一按当前配置校验 issuer；`RefreshToken` 不再忽略 `refresh_expire`；空 JTI 不再写入永不命中的 `jwt_bl:` 黑名单键。
- **M10 分布式锁参数与未获锁语义修复**：锁 TTL 统一校验到 Redis 毫秒粒度；`TryLock` 的非正 retry interval 不再 busy-loop；`WithLockAutoExtend` 的非正 extend interval 不再触发 goroutine panic；`UnlockByKey` 在 Redis 未初始化时与 `ForceUnlock` 一样返回 `ErrRedisNotReady`。
- **M10 cache 剩余错误语义收口**：新增 `cache.ExistsE` 与可选 `CacheExistChecker`，让调用方能区分 key 不存在和 Redis/backend 故障；保留旧 `Exists` bool-only 兼容方法但记录后端错误；`KeyBuilder` 现在忽略 nil option，`WithPrefix` / `WithSeparator` / `WithCacheType` 直接作用于 nil builder 时 no-op，避免扩展配置路径 panic。
- **M11 SSE 换行注入修复**：`WriteEvent` 拒绝带 CR/LF 的 event 名，`WriteMessage` / `WriteEvent` 的 data 按 SSE 多行格式逐行输出，避免用户数据伪造额外 `event:`/`id:` 字段。
- **M15 utils/validation 资源与错误边界修复**：`HTTPClient.Upload` 改为流式 multipart 上传，不再把文件请求体完整缓存在内存中；`AppendFile` / `CopyFile` 返回写侧 `Close` 错误；`CheckPasswordAndUpgrade` 归一化非法 `targetCost`，避免异常配置触发超高 bcrypt cost；`ValidateStruct(nil)` 直接返回 nil。
- **M12 storage/compress 安全边界修复**：本地上传写侧 `Close` 错误会通过返回值暴露并清理残片；OSS `GetSignedURL` 统一经过 object key 净化；`UnzipWithOptions` 解析目标绝对路径失败时 fail-closed。

- **M13 cron handler panic 未 recover 崩进程**（cron/cron.go）：`RunTask` 与 `checkAndRun` 调度 goroutine 统一经新增 `executeTask(t)` 边界 `recover`，panic 转为 error（含 `debug.Stack` 调用栈）记入 `task.LastError` 并向上返回，不再终止进程。外侧 `defer wg.Done()`/`running` 守卫释放不受影响（recover 在边界内完成）。顺带修复 `RunTask` 手动路径此前只更 `LastRun/RunCount`、不记 `LastError` 的子问题（现与调度路径一致）。
- **M6 response/handler 入口防护**（response/error.go，response/response.go，handler/handler.go）：`FailWithError(nil)` / `FailWithDetail(nil, ...)` 回退统一服务器错误响应，不再 nil deref panic；新增 `response.DownloadReader` 支持大文件/对象存储流式下载，旧 `Download` / `DownloadWithContentType` 保持兼容并复用同一响应头逻辑。
- **M7 health/readiness 探活超时**（router/router.go）：`HealthCheck` 新增 `Timeout` 字段，默认每个依赖检查 2s 超时；超时项返回 `"timeout"` 并使 `/health` / `/readyz` 返回 503。单个 check 同时最多一个执行中，panic 会 recover 为错误，避免 k8s/LB/监控探活被挂死依赖无限卡住或无限堆积 goroutine。
- **M13 cron 剩余边界收口**（cron/cron.go）：Stop 后再次 Start 会重建调度器 context，手动和调度执行不再收到已取消 ctx；Start/Stop 生命周期串行化，避免 Stop 等待期间重新 Start 触发 WaitGroup Add/Wait 交错。
- **M5 repository 安全边界收口**（repository/repository.go）：nil ctx 统一按 `context.Background()` 处理；`FindByIDs(nil)` 返回非 nil 空切片；`NewQueryBuilder` 复用 nil DB 明确 panic；批量空 ids 写操作 no-op；默认 `FindAll` 加上限并新增 `FindAllUnbounded`；排序/字段名白名单避免便捷 API 误接 raw SQL。
- **M1 App 生命周期三类缺陷统一治理**（app.go）：
  - **Init 失败无资源回滚** → 新增 `failAfterInit`：markStopping → cancel rootCtx → wg.Wait(10s) → cron 5s 显式超时停止 → `closeResources` 幂等关闭 db/redis/logger，回滚错误 `errors.Join` 进 `initErr` 不吞；先停 goroutine 再关资源，避免"关 DB 时探活 goroutine 仍在用"的竞态。
  - **App.Go 与 wg.Wait race** → `lifecycleMu.RLock` 包住 `wg.Add`，`Shutdown` 持写锁翻 `stateStopping`，保证 Add happens-before Wait。
  - **Shutdown 非幂等/非并发安全** → `shutdownOnce` 保证 `doShutdown` 单次执行，并发调用者返回同一 `shutdownErr`。
  - **OnStop 语义** → 仅 `Init` 曾成功（`wasInitialized`）时在 `doShutdown` 开头执行；Init 失败/未 Init 时 HTTP 从未启动，OnStop 跳过。
  - **资源所有权** → App 只关闭自己成功初始化过的 logger/db/redis/cron，避免一个 Init 失败的新 App 关闭同进程既有全局资源。
  - **复审补强：资源替换事务边界** → App 初始化 logger/db/redis 时先创建 App-owned manager 并保存旧默认 manager 快照；OnInit 或后续步骤失败时恢复旧默认 manager 并关闭新资源，完整成功后才释放旧资源，避免“新 App Init 失败”破坏同进程既有全局 logger/db/redis。
  - **复审补强：health/probing 绑定 App-owned manager** → App 注册的 MySQL/Redis health check 与 DB probing 使用本 App 持有的 manager，不随后续全局默认 manager 替换漂移。
  - **OnReady 早于真实监听成功** → `StartServer` 改为同步 `net.Listen` 且 TLS 证书装配成功后再启动 `Serve` 与执行 OnReady；监听/TLS 失败直接返回，不触发 ready 副作用。
  - **`server.unix_socket` 不可用** → 非空 `unix_socket` 现在走 `net.Listen("unix", path)` + `http.Server.Serve`，不再把 socket path 误传给 TCP `ListenAndServe`。
  - `Init`/`Shutdown` 经 `initMu` 串行化 doInit/doShutdown 长段，杜绝并发改资源。
- **M3 logger 生命周期与全局 manager 并发治理**（logger/logger.go）：
  - **`LogManager.Init()` 锁外写 `m.level`** → `Init` 在通过局部配置校验后持 `m.mu` 完成建目录、构造与发布新 logger，`m.level` 只在同一临界区更新；包级 `Logger/fileWriters` 发布另由 `globalMu` 串行化，避免多个 `LogManager` 实例用各自实例锁保护同一包级状态。
  - **`DefaultLogger` 裸全局指针** → 保留导出变量兼容旧代码，新增内部 atomic 默认 manager 快照与 `GetDefaultLogManager()`；包级 `Init/Close/Sync/SetLevel/GetLevel` 全部经 atomic 读取当前 manager。
  - **stale manager 关闭当前 logger** → 每次发布全局 logger 分配 generation，只有拥有当前 generation 的 `LogManager.Close()` 才能关闭当前全局 logger/writer。
  - **旧 writer 关闭顺序错误** → `Init` 先 atomic 发布新 logger，再关闭旧 lumberjack writer，避免替换窗口内包级读路径拿到指向已关闭 writer 的旧 logger。
  - **`Close()` 与 `Init()` 生命周期互相覆盖** → `Close` 在同一 manager 锁内先快照旧 logger/writer 并发布 Nop，再执行 Sync/Close；关闭错误通过 `errors.Join` 聚合返回，不再静默吞掉 lumberjack `Close` 错误。
- **M8 middleware/CSRF 与限流边界治理**（middleware/csrf.go，middleware/ratelimit.go）：
  - **CSRF JSON body 读取无上限** → 从 body 提取 `_csrf` 时使用 `http.MaxBytesReader`，默认上限 1MiB（`CSRFConfig.MaxBodyBytes` 可调），超限返回 HTTP 413，避免 pre-auth OOM；仍使用 `ShouldBindBodyWith` 保留下游重复读取 body 的能力。
  - **CSRF cookie SameSite 配置未真正写入** → `CSRF()` 与 `DoubleSubmitCookie()` 设置 cookie 前显式 `SetSameSite`，默认 Lax。
  - **CSRF 局部配置丢默认 cookie 行为** → `CSRFConfig` 归一化补齐 `Path`、`SameSite`、`MaxAge`、`FormField`、`MaxBodyBytes` 等默认值，只覆盖单个字段时不再意外丢失默认 cookie 约束。
  - **CSRF session cookie 显式配置** → 新增 `CSRFConfig.SessionCookie`，需要会话 Cookie 时设置为 `true`；为保持 `CSRFWithConfig(CSRFConfig{})` 默认 1 小时语义，`MaxAge=0` 且未设置 `SessionCookie` 时仍回退默认值。
  - **CSRF TokenLength 负数 panic/退化** → `TokenLength <= 0` 统一回退默认长度，避免配置错误导致 `make([]byte, negative)` panic。
  - **CSRF skip path 前缀误跳过** → `CSRFWithSkip([]string{"/api"})` 仅跳过 `/api` 与 `/api/...`，不再误跳过 `/apix`。
  - **API CSRF token map 只校验时清理过期 token** → `GenerateAPIToken` 颁发新 token 前同步清理过期项，避免长期只发不验场景内存增长。
  - **`RateLimit(nil)` panic** → 改为 fail-closed 返回 HTTP 503 + `CodeServiceUnavailable`，避免未初始化限流器导致请求路径 nil deref。
  - **`RedisRateLimitWithIdentifier` nil 回调 panic** → `identifierFunc == nil` 时回退 `ClientIP()`，并保留空字符串回退逻辑。
- **gosec database 告警收口**（database/manager.go，database/redis.go）：`RoundRobinPicker` 改为 mutex 保护的有界 `int` 计数器，消除 G115 整数转换告警；`RandomPicker` 改用 `crypto/rand` 选择从库，消除 G404 弱随机源告警，随机源失败时安全回退到首个从库；Redis 初始化 ping 失败时不再静默吞掉 `client.Close()` 错误。
- **复审补强：DB/Redis 重建路径资源释放**（database/manager.go，database/redis.go）：Redis 重复初始化会关闭旧 client；DB 重建/重试路径的旧连接池关闭失败不再静默丢弃，会记录 warning 供排查。
- **gosec Close 错误收口**（utils/http.go，compress/compress.go）：HTTP multipart 上传循环中的本地文件 `Close()` 错误不再静默吞掉；gzip/zip 解压输出文件关闭失败会通过 `errors.Join` 返回，并触发残留目标文件清理。
- **gosec 兼容性/误报标注收口**（cmd/xlgo，utils/http.go，utils/crypto.go，utils/file.go，compress/compress.go）：为已存在输入校验或明确调用方契约的路径、客户端请求 cookie、非安全用途 checksum、压缩源路径遍历、兼容模式 HTTP 请求添加精确 `#nosec` 理由；`NewSSRFSafeHTTPClient` / `BlockPrivateNetworks` 仍是处理不可信 URL 的推荐入口。
- **示例参数解析错误处理**（examples/full/main.go）：示例用户详情接口现在检查 `fmt.Sscanf` 错误，非法用户 ID 返回失败响应，不再静默使用零值。

## [1.2.0] - 2026-07-04

> 框架整体评估后的结构性修复：4 轮对抗性评审（deepseek / GLM / Claude / 终审）收口的全部 CRITICAL/HIGH/MEDIUM 问题 + 主线A 包级可变全局并发治理统一。
> 项目处于初级阶段，无下游用户，放心引入破坏性变更。`go vet` + `go build` + `go test -race ./...` 全绿。

### Breaking ⚠️

- **`database.RedisClient` 不再可外部访问**：包级变量改为 unexported。所有消费者必须通过 `database.GetRedis()` 获取客户端。测试注入使用 `database.SetTestRedisClient(c)` 替代直接赋值。
- **`xlgo.WithConfig(cfg)` 不再调用 `config.Set(cfg)`**：配置不再写入全局状态。依赖 `config.Get()` 获取注入配置的下游代码需改用 `WithConfigPath`。
- **`App.Init()` 内部改为 `sync.Once`**：多次调用返回首次执行的结果（含错误），不再是之前的"第二次直接返回 nil"。
- **`database.DefaultRedis` 类型变更**（database/redis.go）：由 `*RedisManager` 改为 `atomic.Pointer[RedisManager]`，消除裸指针无锁置换的数据竞争（C-1）。下游若直接调用 `DefaultRedis.Init(...)` 等方法需改用 `InitRedis(...)` facade，或 `DefaultRedis.Load().Init(...)`。
- **`storage.DefaultStorage` 类型变更**（storage/storage.go）：由 `*StorageManager` 改为 `atomic.Pointer[StorageManager]`，与 `config.defaultManager`/`database.DefaultRedis` 并发保护对齐。下游直接调用方法需改用 facade 或 `.Load()`。
- **`validation.Validator` 类型变更**（validation/validator.go）：由 `*validator.Validate` 改为 `atomic.Pointer[validator.Validate]`，消除无锁读写竞态（H-13）。下游直接读 `validation.Validator.Struct(...)` 需改用 `ValidateStruct(...)`，或 `validation.Validator.Load().Struct(...)`。
- **`repository.FindWhereOrdered` / `FindPageWhereOrdered` 签名变更**（repository/repository.go）：`args []any` 改为 `args ...any`，与同类方法统一；因 Go 变长参数须为末尾参数，`order` 前置于 `query`。新签名：`FindWhereOrdered(ctx, order, query string, args ...any)`、`FindPageWhereOrdered(ctx, page, pageSize int, order, query string, args ...any)`（H-15）。
- **`response.Response` 的 `Data`/`RequestID` 去掉 `omitempty`**（response/response.go）：`data` 与 `request_id` 字段在所有响应中恒存在（失败时 `data:null`、未装 RequestID 中间件时 `request_id:""`），下游严格按 schema 解析不再缺字段（M-38/M-39）。
- **`cache.IsLocked`/`GetLockTTL`/`ForceUnlock` Redis 不可用时改返 `ErrRedisNotReady`**（cache/lock.go，M-E）：原返 `(false/0, nil)` 与"锁确实未占用/已过期"不可区分，调用方可能误以为可获取锁而进入临界区。现统一为：锁操作（正确性相关）Redis 不可用返 `ErrRedisNotReady`，调用方 `errors.Is(err, ErrRedisNotReady)` 区分；cache 数据操作（Get/Set/Incr，性能层）保持 best-effort 静默。
- **`config.Load()` 返回深拷贝**（config/config.go，M-G）：新增 `(*Config).Clone()` 深拷贝所有切片字段（CORS/Upload/Storage 白名单），`Load()` 与 reload 回调改返 Clone。原"防御性拷贝"为浅拷贝，切片字段与内部 `m.cfg` 共享底层数组，调用方 append/sort/改元素会污染全局。`Get()` 仍返回内部只读指针（热路径零分配），需可变副本用 `Clone()`。
- **`handler.GetPage` 加 page 上限 10000**（handler/handler.go，M-D）：防 `?page=999999999` 产生超大 OFFSET 拖垮 DB（深分页 DoS）。超过上限钳制到 `MaxPage`，需更深遍历应改游标/keyset 分页。
- **`utils.EqualsIgnoreCase` 改 `strings.EqualFold`**（utils/strings.go，L-C）：原仅 ASCII 字节折叠，非 ASCII（如 `É`/`é`）误判为不等。现 Unicode 大小写折叠，行为更正确。
- **`utils.ReadFile` 去 `FileExists` 前置检查**（utils/file.go，M-F）：消除 TOCTOU 竞态，直接 `os.ReadFile`。文件不存在返 `*os.PathError`，调用方改用 `errors.Is(err, os.ErrNotExist)` 判断（原字符串 `"file not found"` 不再返回）。
- **`database.DefaultManager` 类型变更**（database/manager.go，主线A 收口）：由 `*Manager` 改为 `atomic.Pointer[Manager]`，消除原裸指针（外部直接 `database.DefaultManager = ...` 赋值与请求 goroutine 经 facade 读取）的数据竞争，与 `database.DefaultRedis`/`storage.DefaultStorage`/`cache.defaultCachePtr`/`jwt.defaultManager`/`config.defaultManager` 对齐——框架内包级可变全局一律 atomic.Pointer。新增 `database.SetDefaultManager(m)`（atomic.Store，并发安全）与 `database.GetDefaultManager()`（atomic 读取）。下游若直接调用 `DefaultManager.Init/Master` 等方法需改用 `InitDB`/`GetDB` 等 facade，或 `DefaultManager.Load().Init(...)`，或经 `GetDefaultManager()` 取实例；原 `database.DefaultManager = myDB` 直接赋值改为 `database.SetDefaultManager(myDB)`。

### Fixed 🐛

#### 第一轮：CRITICAL + HIGH 基金会战

- **U1 UUIDShort 生成错误**（utils/uuid.go）：`uuid.New().String()[:32]` 产生的 32 字符保留了 4 个破折号（UUID 格式为 36 字符含 4 个 `-`，`[:32]` 截断末尾 4 个 hex 字符但保留破折号）。改为 `strings.ReplaceAll(uuid.New().String(), "-", "")`，现在正确生成 32 位纯十六进制字符串。
- **M1 filterSensitiveFields 假过滤**（middleware/logger.go）：旧实现用 `strings.ReplaceAll` 在 key 后面追加 `"[FILTERED]"` 而不删除原始值，导致 `{"password":"mypass"}` → `{"password":"[FILTERED]""mypass"}` 密码仍然可见。改为编译期正则 `sensitiveFieldsRE` 匹配 `"key":"value"` 整体并替换 value 部分为 `[FILTERED]`。
- **A1 App.Init 并发竞态**（app.go）：`a.initialized` 是普通 bool，并发 `Init()` 调用会同时通过检查导致双重初始化。改为 `sync.Once`，`doInit()` 提取为内部方法，错误通过 `a.initErr` 保存。
- **D3/CK1 Redis 客户端访问竞态与不一致**（database/redis.go, cache/lock.go）：`database.RedisClient` 全局变量在 `Init`/`Close` 中有锁写入但可被外部无锁读取（数据竞态）；`cache/lock.go` 直接引用 `RedisClient` 而非 `GetRedis()`（绕过 RedisManager 抽象）。修复方案：`RedisClient` → unexported `redisClient`；`GetRedis()` 加入回退逻辑（优先 `DefaultRedis.Client()`，回退内部 `redisClient`）；lock.go 全部改为 `rdb := database.GetRedis()` 单次获取 + nil 检查。
- **M2 Metrics in-flight gauge 泄漏**（middleware/metrics.go）：`httpRequestsInFlight.Inc()` 后无 `defer Dec()`，handler panic 时 gauge 永久膨胀。改为 `Inc()` 后立即 `defer Dec()`，不依赖 Recover 中间件顺序。
- **R1 FailWithError 丢弃 Detail**（response/error.go）：`FailWithError` 直接 `writeResp(c, err.Code, err.Message, nil)` 丢弃了 `err.Detail`。改为 `resp := err.ToResponse(); writeResp(c, resp.Code, resp.Msg, resp.Data)`。

#### 第二轮：并发纪律红线 + 生命周期/泄漏修复

> 来源：glm_check_report_framework.md 结构性评估。聚焦"包级可变全局统一治理"与"重建路径资源释放"两条主线。

- **C-1/H-4 `database.DefaultRedis` 并发竞态 + `redisClient` 双源**（database/redis.go）：`DefaultRedis` 改 `atomic.Pointer[RedisManager]`（init Store 兜底），所有 facade 经 `Load()`；废弃 `redisClient` 包级变量，`GetRedis` 单源化；`SetTestRedisClient` 改为在当前 manager 上持锁 `setClientForTest` 替换 client，消除双源真相与无锁写竞态。
- **H-13 `validation.Validator` 无锁读写**（validation/validator.go）：改 `atomic.Pointer[validator.Validate]`；`InitValidator` 先注册全部自定义规则再 Store，确保 Load 得到的 validator 永远完整。
- **H-11 + 主线A `storage` 死代码全局 + `DefaultStorage` 裸指针**（storage/storage.go）：删除从未被读取的死代码 `var storage Storage`；`DefaultStorage` 改 `atomic.Pointer[StorageManager]`。
- **H-10 `response.Error.WithDetail` 并发不安全**（response/error.go）：原实现 `e.Detail = detail` mutate 共享的预定义 `Err*`（并发写竞争 + 全局污染）。改为返回新 `*Error` 拷贝。
- **H-6 `middleware.RedisRateLimiter.failClosed` 数据竞争**（middleware/ratelimit.go）：改 `atomic.Bool`，`SetFailClosed`/`Allow` 全用 atomic，支持运行期并发切换策略。
- **H-7 `middleware.GetCSRFToken` 裸断言 + 非恒定时间比较**（middleware/csrf.go）：`token.(string)` 改 comma-ok；CSRF token 比较改 `subtle.ConstantTimeCompare`（两处），防时序侧信道。
- **H-14/M-64/M-65 `trace.Close` sync.Once 泄漏 + Init 回滚不全**（trace/trace.go）：`Close` 去 `sync.Once` 改 Swap+Shutdown，支持 Close→Init→Close 多轮生命周期（原第二次 Close no-op 致 exporter 泄漏）；`Init` `!Enabled` 分支 Swap+Shutdown 旧 provider；传播器失败时不切 otel 全局（移到成功路径末尾），避免指向已 Shutdown provider。
- **H-8/H-9 `ws.Hub.Stop` double-close panic + WaitGroup Add/Wait 竞态**（ws/ws.go）：`Stop` 用 `sync.Once` 包 `close(stop)`（删除"stopped 标志"虚假注释）；用 `runDone chan` + `runStarted atomic.Bool` 替代 WaitGroup，消除 `wg.Add(0→1)` 与 `wg.Wait` 的 happens-before 竞态，Stop 先于 Run 调用也不泄漏。
- **H-1 `app.OnReady` 失败资源泄漏**（app.go）：失败时走 `Shutdown()` 释放 HTTP 端口/后台 goroutine/资源，不再直接 return 致监听 goroutine 永久阻塞。
- **H-2 `app.Go` + `Init` 失败 goroutine 泄漏**（app.go）：`Init` 失败时 cancel rootCtx + 等 wg（10s 超时兜底），通知已通过 `App.Go` 启动的后台 goroutine 退出。
- **H-12 `utils.HTTPClient.SetSkipTLS` 数据竞争**（utils/http.go）：加 `sync.RWMutex`；`SetSkipTLS`/`SetTimeout` 写锁下重建 client/transport 并释放旧 transport 空闲连接；`do`/`DoWithResponse`/`Close` 读锁快照后无锁调 `Do`。修复注释自承"需重建 Transport"却未重建的不一致。

#### 第三轮：P0 安全阻断 + P1 并发/资源收口（Claude 终审）

> 来源：claude_check_report_framework.md / claude_check_report_module.md。在 deepseek/GLM 修复后的版本上补审，闭合两者共同漏掉的 JWT 算法混淆等安全默认值问题。

- **#1 JWT 算法混淆（alg confusion）**（jwt/jwt.go）：`ParseWithClaims` 统一传 `jwt.WithValidMethods(["HS256","HS384","HS512"])`，keyfunc 内断言 `*jwt.SigningMethodHMAC`，拒绝 alg=none 及非 HMAC 算法（防公钥当 HMAC 密钥伪造 token）。回归 `TestParseTokenRejectsAlgNone`。
- **#2 JWT 空密钥 fail-closed**（jwt/jwt.go）：新增 `secretKey()`，`GenerateToken*`/`ParseToken*` 空 secret 返 `ErrEmptySecret`。回归 `TestEmptySecretFailsClosed`。
- **#3 JWT 不支持算法拒绝**（jwt/jwt.go）：`signingMethod` 改返 `(SigningMethod, error)`，RS256 等返 `ErrUnsupportedAlgorithm`，不再静默回退 HS256。回归 `TestUnsupportedAlgorithmFailsClosed`。
- **#4 上传大小实测封顶**（storage/storage.go）：新增 `enforceUploadSize`/`enforceMaxReader`，本地与 OSS 上传拷贝阶段按实际字节封顶，超限返 `ErrUploadTooLarge` 并清理部分落盘文件。修复原信任客户端 `file.Size` 的绕过。
- **#5 HTTP header/cookie map 竞态**（utils/http.go）：`SetHeader/SetHeaders/SetCookie` 加写锁，`do/DoWithResponse` 经 `snapshotHeadersCookies()` 读锁快照。
- **#6 HTTP SSRF 防护**（utils/http.go）：新增 `BlockPrivateNetworks` + `NewSSRFSafeHTTPClient()` + `SetBlockPrivateNetworks()`，经 `net.Dialer.Control` 在连接建立时拦截回环/私有/链路本地/元数据 IP，覆盖重定向每一跳。
- **#7–#21 P1 并发/资源/泄露收口**：console 写锁（`writeMu`）；config 包级 `Load/LoadWithWatch` `pkgLoadMu` 串行化 + debounce timer Stop；CSRF `ShouldBindBodyWith`（body 可重放）；cron `StopWithTimeout`/`WithCron()` 接入 App 生命周期；database `m.cfg` 经 `getCfg/setCfg` 锁内读写；`isTransientDBError` 移除过宽 `"database"` 子串；router `applyOnce sync.Once` + 版本 map 排序；logger `filterSensitiveQuery` 脱敏；response `SetExposeDetail`（生产隐藏 Detail）；validation 密码 MaxLength=72 + phone 全数字 + `RegisterValidation` `must` 检查；trace noop 包 + `defer span.End()` + `[unmatched]` 低基数 span 名；storage `NewLocalStorage` Abs fail-closed；cmd/xlgo 项目名/模块路径校验 + `os.Exit(1)` + 失败回滚。

#### 第四轮：终审剩余项收口（last_report.md）

> 来源：last_report.md 终审。在第三轮基础上逐项回读核实后修复，均经 `go test -race` 验证。

- **H-A 从库连接池上限截断**（database/manager.go）：`MaxOpenConns/2` 在配置为 1 时得 0（`database/sql` 视 0 为无限制），与"从库减少"意图相反、资源失控。新增 `replicaMaxOpenConns()`：`>0` 取 `max(1, /2)`，`<=0` 与主库一致无限。
- **H-B `router.GroupWithMiddlewareGroup` nil panic**（router/router.go）：改走 `ensureRegistry()`（与其他全局 helper 一致），把未初始化 nil 解引用 panic 转成可定位错误。
- **M-A JWT 黑名单无超时 + 吞错**（jwt/jwt.go）：`Add`/`IsBlacklisted` 改用 `context.WithTimeout(1s)`（`blacklistCtx`），把鉴权路径阻塞上限收敛到 1s；`IsBlacklisted` 用 `.Result()` 替代 `.Val()` 显式处理错误（fail-open 保留但记录告警）。
- **M-B `Recover` 响应已写出时无效写 500**（middleware/recover.go）：加 `c.Writer.Written()` 守卫，已写出时仅 Abort + 记录，避免流式/部分写场景下 `response.Custom` 沦为 no-op 致客户端收到截断响应。
- **M-C `RedisRateLimiter` 多次 `GetRedis()` nil-deref 窗口**（middleware/ratelimit.go）：`Allow`/`GetCount`/`Reset` 改为取一次 `rdb` 复用，消除两次调用间 `CloseRedis` 致第二次 nil-deref 的窗口。
- **M-D `handler.GetPage` 深分页 DoS**（handler/handler.go）：加 `page` 上限 `MaxPage=10000`（见 Breaking）。
- **M-E Redis 不可用失败语义统一**（cache/lock.go，见 Breaking）：锁操作返 `ErrRedisNotReady`；`IsLocked` 用 `.Result()` 返错（合并 L-G）。
- **M-F `HashFile`/`ReadFile` OOM 与 TOCTOU**（utils/crypto.go, utils/file.go）：`HashFile` 改流式 `io.Copy(h, f)`（恒定内存，可哈希任意大文件）；`ReadFile` 去 `FileExists` 前置检查（见 Breaking）。
- **M-G `config.Get()` 返回内部指针切片别名**（config/config.go，见 Breaking）：新增 `Clone()`，`Load()` 与 reload 回调返深拷贝；`Get()` 保持热路径零分配返只读指针。
- **M-H `cron.checkAndRun` 持锁 spawn 阻塞管理 API**（cron/cron.go）：锁内只收集到期任务（CAS + 推进 NextRun + `wg.Add`），锁外 spawn。`wg.Add` 必须在锁内以保证 `Stop` 的 `wg.Wait` 等到本批（否则 Stop 可能在任务间提前返回、后启动 goroutine `wg.Done` 下溢 panic）。
- **L-A compress 解压残留文件**（compress/compress.go）：`GzipDecompressFileWithOptions`/`unzipFile` 失败时（超限/拷贝错误）`os.Remove` 部分落盘文件，避免被拒炸弹遗留残片。
- **L-B utils 正则重编译**（utils/validator.go）：`IsPhone`/`IsEmail`/`IsIPv4`/`IsIDCard` 正则提为包级 `MustCompile`，避免每次调用重编译。
- **L-C `EqualsIgnoreCase` 非 ASCII 误判**（utils/strings.go，见 Breaking）：改 `strings.EqualFold`。
- **L-D `redisLimiters` 死代码**（middleware/ratelimit.go）：删除从未被读写的包级 `redisLimiters` map 与其 `init()`。
- **L-E `logger.Logger` 导出变量**（logger/logger.go）：标 `Deprecated:`，引导用包级 `Info/Debug/...` 函数（atomic 读取，并发安全）。
- **L-H `ws.SetCheckOrigin` 无锁写**（ws/ws.go）：文档明确"仅启动前调用"，运行期切换 Origin 用自有 upgrader 实例。
- **L-J `cron.RunTask` 用 `s.ctx`**（cron/cron.go）：文档说明 Stop 后 handler 收到 canceled ctx 的预期行为。

> **未修（评估后决定）**：L-K `model` 时间戳 `omitempty`——`encoding/json` 对 `time.Time`（结构体）的 `omitempty` 是 no-op，报告建议的修复无效；真正的修复需改 `*time.Time`（破坏性 + nil 安全隐患），收益与成本不匹配，暂不做。L-I `response.writeResp` nil-c 守卫——nil `*gin.Context` 是程序员错误，panic 是恰当反馈，加静默守卫反而掩盖 bug。L-F/L-M/L-N/L-O 为文档/命名类，已就地注释说明或属设计取舍。

### Changed 🔄

- **`database.GetRedis()` 加入回退逻辑**：优先返回 `DefaultRedis.Client()`，若未初始化则回退到内部 `redisClient`（测试注入路径）。确保 `SetTestRedisClient` + `GetRedis()` 闭环。
- **`database.SetTestRedisClient(c)` 新增**：供测试注入 mock Redis 客户端，返回旧值便于清理恢复。

---

## [v1.1.1] - 2026-06-28

> v1.1.1 后的安全/正确性补丁，依据 `version_1.1.1_report.md` 权威缺陷清单逐项修复（13 CRITICAL + 8 HIGH）。

### Fixed 🐛

#### P3 清理第三批（MEDIUM/MINOR 收尾）

> 续前两批。本批为校验收紧、全局状态并发、Windows 控制台、trace 遗留、logger 级别、测试质量。`go test -race` + `go vet` + `gosec` 通过。

- **M5 身份证无校验位 + 用户名首字节**（validation/validator.go）：18 位身份证补 GB 11643-1999 校验位验证（`validateIDCardChecksum`），原仅查长度+格式可被任意构造通过；`username` 验证首字符改按 `[]rune` 取，避免非 ASCII 首字节误判（原 `rune(username[0])` 取字节）。
- **M13 cache globalKeyBuilder 无锁 + 无 sync.Once**（cache/keybuilder.go）：`globalKeyBuilder` 加 `sync.RWMutex` 读写保护，`GetKeyBuilder` 用 `sync.Once` 保证自动初始化只执行一次，消除 check-then-init 竞态。`SetPrefix` 补实例非并发安全注释。
- **M12 NewRedisCache 构造时快照 client**（cache/cache.go）：`redisCache` 不再构造时 `database.GetRedis()` 快照（Init Redis 之前构造则永久 nil no-op），改每次操作实时取 client，使"先构造后 Init Redis"顺序也能正确工作。
- **M18 trace 显式设 codes.Ok + Close 无 double-close 守卫**（trace/trace.go）：成功路径不再 `SetStatus(codes.Ok, "")`（OTel 规范默认 UNSET，显式 Ok 会掩盖子 Span 错误状态）；`Close` 用 `sync.Once` 保证只 Shutdown 一次，重复调用安全。
- **M19 logger 无法显式设级别**（logger/logger.go）：三个 core（app/api/db/console）改共享 `zap.AtomicLevel`，新增 `LogManager.SetLevel`/`GetLevel` + 包级 `SetLevel`/`GetLevel`，支持运行期热切换日志级别。
- **M17 console_windows 死代码 + 着色句柄分裂**（console/console_windows.go）：移除从未被调用的 `EnableVirtualTerminal`；`printColor` 原对 `syscall.Stdout` 设置颜色、文本却写 `c.output`（非 stdout 时二者分裂），改为按 `c.output` 实际类型取句柄（`*os.File` 用 Fd，否则退化为纯文本）。
- **N2 repository_test 空壳**（repository/repository_test.go）：原全为注释空壳、CRUD 零覆盖，改为编译期断言 `var _ BaseRepository[T] = (*BaseRepo[T])(nil)` 锁定接口契约（实现与接口漂移即编译失败）。
- **N3 test MockStorage 签名不符 + SetupRouter 文档**（test/test.go）：`MockStorage.Upload` 签名对齐真实 `storage.Upload(file *multipart.FileHeader, subdir)`，新增 `UploadFromBytes` 对齐 `storage.UploadFromBytes`；`SetupRouter` 补文档说明刻意返回裸 `gin.New()`（不含框架中间件，由测试方控制）。
- **Added**：`logger.LogManager.SetLevel`/`GetLevel`、`logger.SetLevel`/`GetLevel`、`validation.validateIDCardChecksum`（未导出）。无 breaking（新 API；身份证校验位收紧可能拒绝先前"格式正确但校验位错"的输入——这正是修复目的）。

#### P3 清理第二批（MEDIUM/MINOR 文档/正确性/校验）

> 续上一批。本批以正确性修复 + 文档/命名澄清为主，风险分级处理，`go test -race` + `go vet` + `gosec` 通过。

- **M4 datetime StartOfWeek DST 落错日**（utils/datetime.go）：原用 `t.Add(-N*24h)` 回退到周一，DST 切换日 24h ≠ 1 个日历日会落错日。改为按日历日 `time.Date(..., Day-(weekday-1), ...)` 计算，保留原时区。`ParseDateInt` 补注释说明非法输入会被 time.Date 静默规范化、调用方须校验。
- **M11 HealthCheck 同步 ping 无超时 + WriteQuery 命名误导**（database/manager.go）：包级 `HealthCheck()` 的 `sqlDB.Ping()` 改 `pingWithTimeout`（3s 超时，尊重调用方 ctx deadline），避免探针被慢/挂起的 DB 长期阻塞。`WriteQuery` 补注释说明其命名沿用历史、实际为读取语义（强制主库 read-your-writes）。
- **M9 DSN 密码不转义 + 时区硬编**（config/config.go）：MySQL DSN 密码改 `url.QueryEscape`，Postgres DSN 密码改单引号包裹+内嵌单引号翻倍，避免含 `@`/`:`/空格/引号 的密码破坏 DSN。新增 `DatabaseConfig.Timezone` 字段（MySQL loc / Postgres TimeZone 可配，空则保持原默认 `Local`/`Asia/Shanghai` 向后兼容）。
- **M7 ToResponse 丢 Detail**（response/error.go）：`Error.ToResponse()` 把 `Detail` 放入 `data.detail`（非空时），不再丢失细节信息。
- **M14 timeout 软超时文档化**（middleware/timeout.go）：注释明确软超时语义——仅注入带 deadline 的 ctx，不主动中断 handler；纯 CPU/不查 ctx 的 handler 不生效，硬中断需配合 `http.Server.WriteTimeout` 或 handler 内 `select ctx.Done`。
- **C3 收尾：生产者取消信号契约**（sse/sse.go）：包文档化断连契约——框架消费循环已监听 `c.Request.Context()` 断连即退，但生产者（LLM 流）必须自行监听同一 ctx 在取消时停止，否则上游持续运行浪费算力。`StreamText` 注释已有，补包级 doc。
- **M20 生成器非法标识符 + fileExists 权限误判**（cmd/xlgo）：`make handler my-thing` 原 `cases.Title` 得 `My-ThingHandler`（非法标识符）；新增 `sanitizeIdent` 把非字母数字转下划线再 Title，得 `MyThingHandler`。`fileExists` 注释澄清权限错误的判定语义。
- **N1 BaseModelWithTime 命名误导**（model/base.go）：补注释说明与 BaseModel 唯一区别是 `type:datetime`（部分 MySQL 丢毫秒），名字 "WithTime" 易误导，保留仅为兼容。
- **N4 Nl2br 死分支 + IsEmpty 文档不符**（utils/）：`Nl2br` 的 `case '\n'` 内 `r == '\r'` 半恒假（r 恒为 '\n'）已清理；`IsEmpty` 文档原称支持 slice/map 但实现仅支持 string/[]byte/nil，文档修正为实际行为。
- **N6 sse KeepAlive 触发 onmessage**（sse/sse.go）：心跳由 `data: \n\n`（触发客户端 onmessage）改 SSE 注释行 `: ping\n\n`（不产生消息事件，更符合心跳语义）。
- **Added**：`config.DatabaseConfig.Timezone`。无 breaking（Timezone 零值保持原默认时区）；DSN 密码转义对合法密码无影响（无特殊字符的密码转义后不变）。Postgres DSN 格式变更（password 加单引号）对下游 GORM 透明。

#### P3 安全/正确性轻量清理（一批 MEDIUM/MINOR）

> 风险分级处理：安全与逻辑正确性项实际修复 + 针对性用例；纯文档/感知项仅加注释。全部经 `go test -race` + `go vet` + `gosec` 验证。

- **M15 requestid 头注入**（middleware/requestid.go）：原无条件信任客户端 `X-Request-ID`，可注入 CRLF 伪造响应头/日志。新增 `sanitizeRequestID`：仅接受可见 ASCII（0x20-0x7e）、无换行、长度 ≤128，非法则忽略并重新生成。合法 ASCII ID 仍沿用客户端值（向后兼容）。
- **C7/N7 ws CheckOrigin 默认 true（CSWSH）**（ws/ws.go）：默认 `CheckOrigin` 由恒 `true` 改为同源校验（空 Origin 放行非浏览器客户端、否则要求 Origin host 与请求 Host 一致），防 Cross-Site WebSocket Hijacking。新增 `AllowOrigins(origins...)` 辅助多可信域名场景。**Breaking ⚠️**：原默认放行所有跨域 WS 连接，现拒绝跨域；依赖跨域 WS 的下游需用 `ws.SetCheckOrigin` 或 `ws.AllowOrigins(...)` 显式放行。
- **C5/N5 HTTPClient Upload FD 累积 + 响应体无上限**（utils/http.go）：`Upload` 循环内 `defer file.Close` 改为显式关闭，避免大批量上传累积 FD；`do` 的 `io.ReadAll(resp.Body)` 改 `io.LimitReader` 封顶（默认 32MB，可经 `HTTPClientConfig.MaxResponseBodySize` 配置，-1 不限），防异常服务端返回超大响应打爆内存。
- **M16/B18 压缩写侧 defer Close 吞错**（compress/compress.go）：`GzipCompressFile`/`Zip` 的 `defer gz.Close()`/`defer zipWriter.Close()`/`defer archive.Close()` 改为显式关闭并向上传播错误——flush 失败（归档损坏）不再被吞成成功返回。
- **M6 Download 中文文件名乱码**（response/response.go）：`Content-Disposition` 由直接拼接 `filename=` 改为 RFC 5987：同时给 ASCII 回退 `filename="..."` 与 UTF-8 百分号编码 `filename*=UTF-8''...`，中文等非 ASCII 文件名不再乱码。
- **M8 CodeDataAlreadyExists 状态不一致**（response/mode.go）：`CodeDataAlreadyExists` 在 ModeREST 下原落 200，与同语义的 `CodeDataConflict`(409) 不一致。映射到 409 Conflict。
- **M10 driver 拼写错误静默回退 MySQL**（database/dialect.go）：未注册驱动回退 MySQL 时新增 `logger.Warnf` 告警（含已注册驱动列表），避免拼错 driver 名（如 `postgrs`）静默回退导致连接错误难排查。
- **M2 AddQueries 实为 Set**（utils/url.go）：`AddQueries` 原用 `query.Set`（覆盖同名），与 `AddQuery`（追加）语义不一致。改为 `query.Add`，同 key 多值共存。
- **M3 file.go 路径穿越感知**（utils/file.go）：文件工具函数加包级文档警告——直接操作调用方路径不做穿越校验，不可信输入须调用方自行净化（框架 storage 包已做防护）。
- **Added**：`ws.AllowOrigins`、`utils.HTTPClientConfig.MaxResponseBodySize`。**Breaking**：`ws` 默认 CheckOrigin 收紧为同源（见 C7）。无配置/migration 变更（MaxResponseBodySize 零值默认 32MB）。

#### H6：`repository/repository.go` BaseRepo 不接 GetDBFromContext + 读写分离失效 + 事务无法 join + 分页不一致（repository/repository.go, database/manager.go）

`BaseRepo` 构造时捕获 `r.db`，所有方法 `r.db.WithContext(ctx)` 从不调 `database.GetDBFromContext` → 读写分离形同虚设（读全走主库）、外层 ctx 事务无法 join、`WithTransaction` 内方法走 `r.db` 拿不到事务。叠加 `Update` 用 `Save` 全列覆写（H6a）、`FindPage` 的 count+list 为两条独立语句高并发下 total/items 不一致（H6d）、`QueryBuilder` 终结方法未克隆且 `Count` 受残留 Limit/Offset 截断（H6e）。修复：

- **H6c 连接路由**：新增 `readConn(ctx)`/`writeConn(ctx)`，优先级为「外层 ctx 事务（`database.TxFromContext`）> 本 repo 事务（`r.tx`）> 路由 db > `r.db` 回退」。读走 `database.GetDBFromContext`（默认从库，支持 `UseMaster`/`UseReplica`），写走 `database.GetWriteDB()`（主库，不路由到只读从库）。`DefaultManager` 未初始化（如单测注入 sqlite）时回退 `r.db`，兼容下游 `NewBaseRepo[T](database.GetDB())`。
- **H6c 事务 join**：`BaseRepo` 新增未导出 `tx` 字段；`WithTransaction` 创建 `txRepo` 时注入 `tx`，其方法自动 join 事务。新增 `database.WithTx(ctx, tx)`/`TxFromContext(ctx)` 支持跨层/跨 repo join（外层 `database.TransactionWithContext` 拿到的 tx 经 `WithTx` 注入 ctx 后传给 repo 方法即可参与同一事务）。`WithTransaction` 签名**不变**。
- **H6a 局部更新**：新增 `UpdateFields(ctx, model, conds...)` 基于 `gorm.Updates`（struct 仅更新非零字段、map 可显式置零），避免 `Save` 全列覆写丢失更新/零值不可辨。`Update`（Save）保留并文档化其全列覆写语义。
- **H6b 软删除契约**：`Delete` 文档化行为契约——`T` 内嵌 `gorm.DeletedAt`/`gorm.Model` 时软删除，否则硬删除（泛型类型约束无法编译期强制）。
- **H6d 分页一致性**：`FindPage`/`FindPageOrdered`/`FindPageWhere`/`FindPageWhereOrdered` 的 count+list 包进单事务（同一快照），消除高并发下 total/items 不一致。
- **H6e QueryBuilder 克隆**：终结方法（`Find`/`First`/`Count`/`Page`）基于 `Session(&gorm.Session{})` 克隆，不污染 `qb.db`；`Count`/`Page` 的 count 额外 `Limit(-1).Offset(-1)` 剥离残留分页条件。文档标注 QueryBuilder 单次使用、非并发安全。
- **Added**：`database.WithTx`/`database.TxFromContext`、`repository.BaseRepo.UpdateFields`。无既有 API 签名/配置/migration 变更（非 breaking）。行为变更：读操作默认路由到从库（原全走主库）、写操作显式走主库、分页查询包单事务（每页一次 BEGIN/COMMIT，见下）。

#### C10：`config/config.go` 全局 Manager 无锁置换 + 热重载绕过 Validate + StopWatcher 空函数（config/config.go）

- **C10a 全局 Manager 无锁置换**：包级 `defaultManager` 原为裸 `*Manager` 指针，`Load`/`LoadWithWatch`/`SetDefaultManager` 直接赋值，与 `Get`/`GetViper`/`GetString` 等请求 goroutine 的无锁读存在数据竞争。改为 `atomic.Pointer[Manager]`，所有包级便捷函数经 `Load()`/`Store()` 原子读写。
- **C10b 热重载绕过 Validate**：`OnConfigChange` 与 `Reload` 原均不调 `Validate()`（仅 `Load` 调用），非法配置（坏端口、负超时、短密钥）直接发布。`Reload` 与文件监听路径统一走 `reload()`：读取/解析/校验任一步失败均保留旧配置并返回错误，仅新配置通过 `Validate` 后才替换 `m.cfg` 并触发回调。
- **C10c Load 返回可变指针**：`Load` 原返回 `&cfg` 与 `m.cfg` 同一指针，调用方可变并竞争。改为返回防御性浅拷贝，调用方修改返回值不污染全局读取路径。
- **C10d StopWatcher 空函数**：原 `StopWatcher()` 为空，viper 内部 watcher goroutine + fd 永不释放。改为自管 `fsnotify.Watcher`（监听配置文件所在目录以兼容编辑器改写/k8s ConfigMap 原子替换，按文件名过滤 + 200ms 去抖），`StopWatcher` 关闭 watcher 并等待监听 goroutine 退出（`watchDone`），幂等。废弃 viper `WatchConfig`/`OnConfigChange`。
- 无 API 签名/配置结构变更；行为变更（热重载非法配置保留旧配置而非发布、StopWatcher 真正释放监听资源）。

#### C11：`database/manager.go` 池泄漏 + Master/Replicas 无锁读 + 健康状态陈旧（database/manager.go）

- **C11b InitDB 重试泄漏**：`InitDB` 原直接 `m.master = gorm.Open(...)`，`gorm.Open` 成功但 `Ping` 失败时旧池不关、下轮覆盖 `m.master`，每次重试泄漏一池。改为先打开到局部变量，仅 `Ping` 通过后才在锁内安装为 `m.master` 并关闭旧主库池；`Ping`/`DB()` 失败时关闭刚打开的池。
- **C11c InitDBWithReplicas 泄漏**：原 `m.replicas = nil` 前不关旧从库池，且从库 `DB()`/`Ping` 失败时 `continue` 不关刚打开的池。改为重建前在锁内取出旧从库、重置健康状态后逐个 `closeDB`；从库构建失败时关闭刚打开的池；新从库先构建到局部切片再原子安装。
- **C11a 健康状态陈旧**：`initReplicaHealth` 的 `replicaHealthSet` 早返回使重新 `InitDBWithReplicas` 后健康切片与新 replicas 长度错位。新增 `resetReplicaHealth`，`InitDBWithReplicas`/`Close` 重建/关闭前调用，使下次 `initReplicaHealth` 按新 replicas 长度重建。
- **C11d Master/Replicas 无锁读**：`Master()`/`Replicas()` 原裸读 `m.master`/`m.replicas`，与 `Close`/`InitDB` 写竞争。改为全程持 `m.mu` 锁；`Replicas()` 返回拷贝；`Replica()` 的空从库判断移入锁内；`FromContext`/`HealthCheck`/`Transaction`/`TransactionWithContext`/`WriteQuery`/包级 `HealthCheck` 改经 `Master()`/`Replicas()` 读取；`probeOnce` 快照 `replicaHealthy` 切片头避免与重置竞争。
- **C11f 包级 Close 仅关主库**：包级 `Close()` 原仅关 master 且无锁，命名误导致从库泄漏。改为委托 `CloseAll()`（关主+从并重置健康状态）。
- **C11e（非缺陷）**：`RoundRobinPicker` `int(n-1)%len` 取模后仍在 `[0,len)` 内，无 panic/正确性问题；`RandomPicker` 全局 `math/rand` 仅锁竞争。属微优化，非功能 bug，未改。
- 无 API 签名/配置结构变更；行为变更（包级 `Close` 现关闭从库、`InitDB` 重试/重建不再泄漏旧池、`Master`/`Replicas` 加锁读取）。gosec G115/G404 为 `RoundRobinPicker`/`RandomPicker` 既有项（C11e 范围外）。

#### C9c：`jwt/jwt.go` 包级 `DefaultJWT`/`tokenBlacklist` 无锁置换（jwt/jwt.go）

`SetDefaultJWTManager` 原裸写包级 `DefaultJWT`/`tokenBlacklist`，与请求 goroutine（`ParseToken`/`RefreshToken`/`InvalidateToken`/`InvalidateTokenByID`/`IsTokenRevoked` 读 `tokenBlacklist`）存在数据竞争（C9c，C9a/b 已在 C9b 修复 fail-closed，此项是遗留并发隐患）。修复：
- 新增内部 `defaultManager atomic.Pointer[Manager]` 作真实存储，`init()` Store；包级函数经 `currentManager()`/`currentBlacklist()`（atomic 读取）访问，消除裸指针读写竞争。
- `SetDefaultJWTManager` 改用 `defaultManager.Store(m)` 原子置换；移除裸写的包级 `tokenBlacklist` 变量。
- `DefaultJWT` 保留为导出 `*Manager` 兼容别名（类型不变，非 breaking），由 `SetDefaultJWTManager` 同步维护；注释标注直接读 `DefaultJWT` 非并发安全，并发访问应用包级函数或 `SetDefaultJWTManager`。
- 无 API 签名变更；`DefaultJWT` 类型不变（非 breaking）。行为变更：包级黑名单读写改经 atomic，`SetDefaultJWTManager` 可安全在请求期调用。

#### H3：`middleware/logger.go` 请求/响应 body 无上限读 → OOM（middleware/logger.go）

`LoggerWithConfig` 在 `LogRequestBody:true` 时用 `io.ReadAll(c.Request.Body)` 无封顶读入内存，`MaxBodyLength` 仅在读完后截断**日志副本**，全 body 已驻留并二次 buffer——多 GB POST 可 OOM；响应侧 `bodyLogWriter.body` 同样无上限累积。默认 `LogRequestBody:false` 使默认安全，但 `LoggerForAPI`/`LoggerForDebug` 显式开启即暴露。修复：
- 请求体新增 `readBodyBounded(c, maxLen)`：`io.LimitReader(body, maxLen+1)` 仅向内存读入最多 `maxLen+1` 字节（+1 检测截断），通过 `io.MultiReader(已读前缀, 原始 body 剩余)` 复原 `c.Request.Body`——**下游处理器仍得完整请求体**；日志副本截断到 `maxLen`。
- 响应体 `bodyLogWriter` 增 `maxLen` 字段，捕获缓冲区封顶；`Write`/`WriteString` 仍把完整响应写入下游 `ResponseWriter`，仅捕获缓冲区封顶。
- `LoggerWithConfig` 入口归一化 `MaxBodyLength`：`<=0` 时回退默认值（1024），确保请求/响应两侧捕获均有上限，消除手配 `MaxBodyLength:0` 时响应侧无上限的 OOM 残留路径。
- 无 API 签名/配置结构变更；行为变更：`MaxBodyLength` 现同时门控响应体捕获（此前响应侧无视该值无上限累积，属 bug 修正）；`MaxBodyLength<=0` 不再意味"无上限"，统一回退默认上限。

#### H7：`logger/logger.go` 全局指针写有锁读无锁 + `Field.Duration` 签名与实现矛盾（logger/logger.go, logger/field.go）

`Init`/`Close` 持 `m.mu`（实例锁）写包级 `Logger`/`sugar`/`apiLog`/`dbLog`，但 `Info`/`Error`/`APILog()`/`DBLog()`/`Sync` 等请求期函数无锁裸读——锁与被保护对象作用域错配（实例锁保护包级全局变量），热重载 re-Init/Close 与请求日志存在数据竞争（H7a）。另 `Field.Duration` 签名为 `func(key string, value interface{})`，`case zap.Field` 分支 `return v` 丢弃 `key`，签名与实现矛盾（H7b）。修复：
- **H7a**：新增内部 `loggerPtr`/`sugarPtr`/`apiLogPtr`/`dbLogPtr atomic.Pointer[...]` 作真实存储，`init()` Store 为 Nop；`Info`/`Debug`/`Warn`/`Error`/`Fatal`/`Debugf`-`Fatalf`/`APILog`/`DBLog`/`Sync` 读路径统一经 `currentLogger()`/`currentSugar()`/`currentAPILog()`/`currentDBLog()`（atomic Load，nil 防御回退 Nop），消除请求期裸读竞争。`Init`/`Close` 在 `m.mu` 下 Store atomic。
- **H7a 兼容别名**：`Logger` 保留为导出 `*zap.Logger` 兼容别名（类型不变，非 breaking），由 `Init`/`Close` 在 `m.mu` 下同步维护；注释标注直接读 `Logger` 变量在 re-Init/Close 期间非并发安全，并发访问应用包级函数。
- **H7b**：`Field.Duration` 签名改为 `func(key string, value time.Duration) zap.Field`，直接委托 `zap.Duration(key, value)`，类型安全、key 不再可能被丢弃。
- 顺带收紧 `os.MkdirAll` 日志目录权限 `0o755`→`0o750`（与 storage 目录权限一致，gosec G301）。
- 无 API 签名/配置结构变更（`Logger` 类型不变）；行为变更：包级日志读路径改经 atomic，re-Init/Close 可安全与请求日志并发。`Field.Duration` 签名变更为**类型收紧**（`interface{}`→`time.Duration`），旧调用方传 `time.Duration` 不受影响，传 `zap.Field` 等非 Duration 类型将编译失败（属修复目的）。

#### C12：`cron/cron.go` 数据竞争 + 重叠执行 + 漂移 + Weekly 跳周 + cron 解析缺陷（cron/cron.go）

`cron/cron.go` 存在 5 子项缺陷（C12a–C12e）。修复：
- **C12a 数据竞争**：`runTask` 原无锁写 `LastRun`/`RunCount`，`GetTask`/`ListTasks` 返回 live 指针并发读 → data race。改为 `LastRun`/`RunCount`/`NextRun` 写入一律在 `s.mu` 写锁内；`GetTask`/`ListTasks` 返回拷贝快照（`cp := *task`）。
- **C12b 无重叠守卫**：`checkAndRun` 每秒 tick，长任务跨 tick 被反复 spawn 同一任务并发执行。新增 per-task `running *atomic.Bool` 守卫，`checkAndRun` 与 `RunTask` 均经 `CompareAndSwap(false,true)` 占用，正在执行则跳过/返错。
- **C12c Interval 漂移**：`NextRun` 原在 handler 完成后以 `time.Now()` 锚定，每周期累积 handler 时长。改为 `checkAndRun` spawn 前 `task.NextRun = task.Schedule.Next(task.NextRun)`（以上次 `NextRun` 锚定），`runTask`/`RunTask` 不再更新 `NextRun`。
- **C12d Weekly 跳周**：原 `daysUntil <= 0 → +7` 仅按 weekday 差值，不比较当天时刻，当天目标未到点被跳一周。重写为 `((day-now)+7)%7` 加天数后 `!next.After(now)` 才 +7，当天未到点返回本周、已过返回下周。
- **C12e cron 解析缺陷**：`parseInt` 忽略非数字逐位累积，`1-5,8` 因先判 `-` 被当范围（`parseInt("5,8")=58`）、`garbage`→0 误触发、`*/garbage`→step=0 匹配全部、周日 `7` 不匹配。重写 `matchField`：列表分支独立于范围分支（先按逗号拆，每项判 `*/n`/`a-b/n`/`a-b`/单值），全用 `strconv.Atoi` 返错；weekday `7→0`，范围 `lo>hi` 环绕；歧义范围 `0-7`/`7-0` 拒绝。新增 `ParseCronStrict(expr) (*FullCronSchedule, error)` 严格校验；`ParseCron` 保留原签名，非法回退默认全 `*`。
- 无 API 签名变更（`ParseCron` 仍返 `*FullCronSchedule`，`AddTask`/`RunTask`/`GetTask`/`ListTasks` 签名不变）；新增 `ParseCronStrict`（非 breaking）。`Task` 新增未导出 `running` 字段（外部不可构造）。行为变更：`GetTask`/`ListTasks` 返回拷贝（修改返回值不影响内部状态）；`RunTask` 占用守卫期间再次调用返"任务正在执行中"错误；长任务不再重叠；调度不漂移；Weekly 当天未到点不再跳周；cron 解析拒绝非法表达式。

#### C13：`trace/trace.go` opt-in 即崩 + 未实现导出器/传播器 + Middleware 不更新 c.Request（trace/trace.go）

`trace/trace.go` 存在 5 子项缺陷（C13a–C13e）。修复：
- **C13a nil tracer panic**：包级 `tracer`/`tracerProvider` 原为裸指针，未 `Init` 即 nil，`Middleware`/`StartSpan`/`StartSpanFromContext`/`GetTracer` 裸用 → 首个请求 panic。改为 `atomic.Pointer` + `init()` Store Noop 兜底，`getTracer()` 永不 nil；`Init` 原子替换，`Close` Shutdown 后 Store 回 Noop（防 Close 后再用 panic）。`GetContext` 裸断言改 comma-ok。
- **C13b 未知导出器 + stdout 缺失**：`createExporter` `default` 原返 `nil, nil` 喂 `WithBatcher(nil)`，文档承诺的 `stdout` 未实现。新增 `case "stdout"`（官方 `stdouttrace` 包）；`default` 返 `fmt.Errorf`（不再喂 nil）。
- **C13c OTLP 默认 HTTPS 无 WithInsecure**：`Config` 增 `Insecure bool`（零值 false=TLS，opt-in 明文，安全默认）；`Insecure` 时 otlp-http/otlp-grpc 追加 `WithInsecure()`，对 `localhost:4318` 等明文 collector 不再握手失败。
- **C13d Middleware 不更新 c.Request**：原仅 `c.Set("otel_ctx", ctx)`，下游 `c.Request.Context()` 拿不到 span。补 `c.Request = c.Request.WithContext(ctx)`（保留 `c.Set` 兼容）。
- **C13e b3/jaeger 未实现**：`createPropagator` 原仅 `w3c` + default 静默回落 W3C。新增 `case "b3"`（contrib b3 propagator，单头+多头）；`case "jaeger"` 映射 W3C TraceContext（现代 Jaeger agent 透传 W3C，不引入不稳定的 jaegerremix 模块）；`default` 返错（不再静默回落）；`Init` 在非法 propagator 时返错并回滚已创建 provider。
- 顺带修复 `resource.Merge` SchemaURL 冲突（`resource.Default()` 与 `semconv v1.24.0` schema 不一致致 `Init` 报错）——改用空 schema URL 合并属性。
- 新增依赖：`go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.43.0`、`go.opentelemetry.io/contrib/propagators/b3 v1.43.0`（均与 OTel core v1.43.0 同版本族）。
- 无 API 签名变更（`Init`/`Middleware`/`StartSpan`/`GetTracer`/`Close` 签名不变）；`Config` 新增 `Insecure` 字段（零值兼容，非 breaking）。行为变更：未 Init 不 panic；未知导出器/传播器返错；`Insecure` opt-in 明文；Middleware 更新 c.Request；b3 实现、jaeger 映射 W3C。`Propagator` 空字符串现按 `w3c` 处理（兼容）。

#### H5：`handler` 业务码与 HTTP 状态混乱 + 丢失 RequestID（handler/handler.go）

`handler.BadRequest`/`handler.InternalError` 原直接 `c.JSON(http.StatusBadRequest/StatusInternalServerError, response.Response{...})`：硬编 HTTP 400/500 **绕过响应模式系统**（ModeBusiness 下所有失败响应本应 HTTP 200，错误经 body code 表达），且不写 `RequestID`（对比 `response.writeResp` 在 mode.go:73 写入）——与 `response` 体系不一致、丢失链路追踪。这正是"handler 绕过 response 模式系统"反模式。修复：
- `BadRequest` 委托 `response.FailWithCode(c, CodeFail, msg)`，`InternalError` 委托 `response.ServerError(c, msg)`——复用 `writeResp` 路径，遵循当前 `Mode` 并写入 `RequestID`。
- 无 API 签名变更；`net/http` 导入随之移除。行为变更见下方升级说明。

#### H4b：`middleware/ratelimit.go` CustomRateLimit goroutine 泄漏（middleware/ratelimit.go）

`CustomRateLimit` 每次调用 `NewRateLimiter`（启动一个 cleanup goroutine）但创建的 limiter 无任何句柄，`StopRateLimiters` 仅停止 `loginLimiter`/`apiLimiter`/`uploadLimiter` 不感知自定义限流器 → cleanup goroutine 永久泄漏。修复：
- 新增包级 `customLimiters []*RateLimiter` 登记表（受 `limitersMu` 保护）；`CustomRateLimit` 创建后登记入表。
- `StopRateLimiters` / `InitRateLimiters` 经 `drainCustomLimiters()` 取出并停止已登记的自定义限流器，释放 cleanup goroutine。
- 无 API 签名变更；无行为变更（仅修复 goroutine 泄漏，限流语义不变）。`StopRateLimiters` 现可正确停止所有自定义限流器（与 H4a 报告建议一致）。

#### H4c：`middleware/ratelimit.go` RedisRateLimiter fail-open + 裸断言（middleware/ratelimit.go）

`RedisRateLimiter.Allow` 原有两个缺陷（H4c）：
- **H4c-1 fail-open**：Redis 错误时 `return true, err`（放行），中间件层 `err != nil → c.Next()` 同样放行——**含登录防爆破场景静默失效**（Redis 抖动窗口限流失效，攻击者可借机爆破）。无 fail-closed 选项。
- **H4c-2 裸断言**：`result.(int64)` 无 comma-ok，Redis 返回非 int64 时 panic（当前 Lua 脚本恒返整数不会触发，属脆弱性）。

修复：
- **H4c-1**：`RedisRateLimiter` 新增 `failClosed` 字段（零值 false=兼容默认 fail-open）。`Allow` 在 Redis 未启用/错误/断言失败时按策略决定：fail-closed 返 `(false, err)` 拒绝，fail-open 返 `(true, err)` 放行（兼容旧行为）。中间件层抽取 `redisLimitDecision`——不再无条件 fail-open，按 `allowed` 值决定：fail-closed 故障拒绝返 **503**（`CodeServiceUnavailable`，区别于真实超限的 429），fail-open 故障放行。
- **H4c-2**：`result.(int64)` 改 comma-ok，断言失败返 `ErrRedisRateLimiterUnexpectedResult`（按 failClosed 策略拒绝/放行）而非 panic。
- 新增构造函数 `NewRedisRateLimiterFailClosed`、切换方法 `SetFailClosed`、中间件 `RedisRateLimitFailClosed`/`CustomRedisRateLimitFailClosed`、导出错误 `ErrRedisRateLimiterUnavailable`/`ErrRedisRateLimiterUnexpectedResult`。
- **`LoginRedisRateLimit` 改 fail-closed**（行为变更，见升级说明）：登录防爆破场景 Redis 故障时拒绝，防限流静默失效。
- 无 API 签名变更（既有函数签名不变）；`RedisRateLimiter` 新增未导出 `failClosed` 字段。



`Allow` 每次放行都 `v.lastSeen = time.Now()`，重置分支 `time.Since(lastSeen) > window` 对持续客户端永不成立 → count 单调累加，稳态客户端（低于 rate）被误限流，须静默满 window 才解锁（算例：rate=10/min、9 req/min 客户端也会被误限）。修复：
- `visitor.lastSeen` 改名 `windowStart`，语义改为"当前固定窗口起点"，仅在新窗口开始时设置，放行时不变更。
- `Allow` 窗口过期时重置 count + 新 windowStart；放行 count++ 不更新 windowStart；超限拒绝。
- 新增 `nowFunc` 字段 + `SetNowFunc` 导出方法（默认 time.Now，测试可注入可控时钟，避免真实 Sleep flaky）。
- 注：固定窗口允许窗口边界突发（2×rate），如需平滑用 Redis 版滑动窗口。H4b（CustomRateLimit goroutine 泄漏）/H4c（Redis fail-open + 裸断言）属独立缺陷，后续跟进。

#### C9b：`jwt/jwt.go` 刷新令牌撤销失败仍签发（jwt/jwt.go）

`RefreshToken` 原丢弃 `tokenBlacklist.Add` 错误仍 `return GenerateToken(...)`，Redis 抖动时旧 token 未拉黑、新旧 token 双有效，形成会话固定窗口。叠加 C9a：`Add`/`IsBlacklisted` 在 `client==nil` 时静默 `return nil`/`false`，黑名单失效无信号。修复：
- **C9b**：`RefreshToken` 对 `Add` 错误 `return "", fmt.Errorf(...)`，fail-closed 不签发新 token。
- **C9a**：`Add` 无 Redis 时返 `ErrBlacklistUnavailable`（新增导出错误），让 `RefreshToken`/`InvalidateToken`/`InvalidateTokenByID` 感知黑名单不可用并 fail-closed；`IsBlacklisted` 无 Redis 仍返 false（验证侧 fail-open 是无 Redis 部署固有局限，文档约束安全场景必须启用 Redis）。

#### H1：`utils/random.go` 不安全 RNG 且文档反向推荐（utils/random.go）

`randPool` 用 `math/rand` + `time.Now().UnixNano()` 播种，`RandString`/`RandDigit`/`RandInt`/`RandInt64` 取自该池，非密码学安全、可预测（`-race` 下并发同纳秒取池实例甚至生成相同序列）。GUIDE.md 原主动推荐 `RandString(16)` 用于 token、`RandDigit(6)` 用于 OTP 验证码，使可预测性可被实际利用。修复：
- 新增 `RandStringSecure(n) (string, error)` / `RandDigitSecure(n) (string, error)`，基于 `crypto/rand` + `big.Int` 索引（拒绝采样无偏），不可预测；`n>1<<20` 返 `ErrRandInvalidLength` 保护熵池。
- `RandString`/`RandDigit` 加安全警示注释（禁止用于 token/OTP/重置码/会话 ID）；保留用于非安全场景（测试数据、随机展示等）。
- GUIDE.md token/OTP 示例改用 Secure 版本并标注错误处理；高分函数表区分"随机（安全/非安全）"；移除"sync.Pool 性能"误导宣传，改为并列说明。
- gosec G404（randPool 的 math/rand）加 `#nosec` 留痕（非安全函数，安全场景用 Secure 版本）。

#### H2：`utils/http.go` 默认关闭 TLS 校验（utils/http.go）

`DefaultHTTPClientConfig.SkipTLSVerify` 原为 `true`，`NewHTTPClient()` → `HTTPGet`/`HTTPPost`/`HTTPPostJSON` 经 `DefaultHTTPClient()` 全部默认 `InsecureSkipVerify: true`，可被中间人攻击（MITM）。改为默认 `false`（校验 TLS）；自签证书场景需显式 `SetSkipTLS(true)` 或配置 `SkipTLSVerify: true`。`SetSkipTLS` 注释补充安全警示。gosec G402 加 `#nosec` 留痕（默认 false，opt-in 跳过）。

#### H8：路由/注册中心全局单例无锁 + Apply 不幂等 + metrics 依赖调用顺序 + 三个 `/health` 行为不一（router/router.go, router/metrics.go, handler/handler.go, app.go）

- **H8a 全局注册中心无锁 + 无 nil 守卫**：包级 `globalRegistry` 原为裸 `*Registry`，`Init` 写、`Use`/`RegisterModule`/`RegisterVersion`/`Apply` 等全局 helper 读存在数据竞争；且 `Init` 之前调用任意全局 helper 触发晦涩的 nil 解引用 panic。改为 `atomic.Pointer[Registry]`，读写均经 `Load()`/`Store()`；新增 `ensureRegistry()`，未初始化时以明确信息 panic（`router: 全局注册中心未初始化，请先调用 router.Init(engine)`），把 nil 解引用转成可定位的初始化顺序错误。
- **H8b Apply 不幂等**：`Registry.Apply` 原无幂等位，二次调用重复 `engine.Use` 装入全局中间件并触发 Gin 重复路由 panic。新增 `applied` 标记，二次及以后 `Apply` 直接返回，中间件与路由仅装入一次。
- **H8c metrics 依赖调用顺序**：`RegisterMetricsRoute` 原用 `r.Use(middleware.Metrics())`，Gin `engine.Use` 仅对其后注册的路由生效，先于其注册的路由不被采集（依赖调用顺序）。改为：`RegisterMetricsRoute` 仅注册 `/metrics` 暴露端点；采集中间件经新增 `Registry.SetMetricsMiddleware` 在 `Apply` 内作首个全局中间件装入，覆盖所有经注册中心注册的业务路由，不依赖注册顺序。`/metrics` 自身与 `/health` 等基础路由直接挂 engine、不经采集中间件，不被自采集（保留原意图）。
- **H8d 三个 `/health` 行为/响应体不一**：`RegisterHealthRoute`（可 503）、`defaultModule`（恒 200 `{"status":"ok"}`）、`handler.HealthCheck`（恒 200 经 `response.Success` 包成 `{code,msg,data}` 信封）三处 schema/行为各异。抽取统一 `healthHandler(checks)`，`RegisterHealthRoute`/`RegisterReadinessRoute`/`defaultModule` 均委托之；`handler.HealthCheck` 响应体收敛为 `{"status":"ok"}`（不再走 response 业务信封），便于 K8s 探针直读。
- **H8d 收尾：defaultModule 与 Register* 并存重复路由 panic（footgun）**：`defaultModule`（经 `WithModules` 注册 `/health`+`/swagger/*any`）与 `RegisterHealthRoute`/`RegisterSwaggerRoutes`（经 `WithDefaultRoutes` 注册同名路由）并存时触发 Gin `handlers are already registered` panic。新增 `registerGETOnce(r, path, h)` 幂等注册辅助，`RegisterHealthRoute`/`RegisterLivenessRoute`/`RegisterReadinessRoute`/`RegisterSwaggerRoutes`/`RegisterMetricsRoute`/`defaultModule` 全部经之：(GET, path) 已存在则静默跳过，首次注册胜出。`*gin.Engine` 经 `Routes()` 精确预检（不吞 panic，真正不同的路由冲突仍按 gin 原语义 panic）；`*gin.RouterGroup`（gin 未暴露 engine，无法预检）用 recover 兜底，仅吞 gin 重复路由 panic（`already registered` / `conflicts with existing wildcard`），最坏情况退化为原行为。
- **Breaking ⚠️**：`handler.HealthCheck` 响应体由 `{code,msg,data:{status:"ok"}}` 改为 `{"status":"ok"}`，与 `router.RegisterHealthRoute` 同 schema。直接断言旧信封字段的下游需改断言 `status` 字段。需依赖探活（mysql/redis 失败 503）时改用 `router.RegisterHealthRoute(checks...)`。
- **Changed**：框架基础路由注册（health/livez/readyz/swagger/metrics/defaultModule）改为幂等，重复注册静默跳过（首次胜出）——消除并存组合的 panic footgun，非破坏性（原本重复注册即 panic，现安全跳过）。
- **Added**：`router.Registry.SetMetricsMiddleware`、`router.registerGETOnce`/`ensureRegistry`/`healthHandler`（未导出）。无配置/migration 变更。

#### C3：`sse/sse.go` 断连泄漏 goroutine + 算力（AI 主场景，sse/sse.go）

- **C3b 写/Flush 错误被吞**：`WriteEvent`/`WriteMessage` 原丢弃 `fmt.Fprintf` 错误且恒 `return nil`，导致 `StreamText` 等的 `if err := WriteJSON(...); err != nil` 守卫只对 marshal 失败生效、对客户端断连永不触发 → 消费循环不退出 + 上游 LLM 流持续运行直到进程结束。改为传播 `fmt.Fprintf` 写错误。
- **C3a 循环无 ctx.Done**：`Stream`/`StreamText`/`StreamChunks`/`StreamWithID` 的 `for range ch` 改 `for { select { case <-ctx.Done(): return ctx.Err(); case v,ok:=<-ch: ... } }`，客户端断连即退出。`SSEWriter` 加 `ctx` 字段（NewSSEWriter 存 `c.Request.Context()`），并对 nil ctx 回退 `context.Background()` 防御。
- **C3c 手设 chunked 头**：删除 `Transfer-Encoding: chunked`（HTTP/1.1 冗余、HTTP/2 非法），交由 server 自动分帧。
- 生产者契约文档化：`StreamText` 注释说明生产者应监听 `c.Request.Context()`，取消时停止上游 LLM 流（框架无法单方面停止生产者）。

#### C7：`middleware/cors.go` 通配符后缀绕过 + 开发态任意 Origin 回显（middleware/cors.go）

- **C7a 通配后缀绕过**：`*.example.com` 原用 `strings.HasSuffix(origin, domain)` 未锚定 host 边界，`https://notexample.com`、`https://evil-example.com` 被接受。改用 `net/url` 解析 origin 的 host，要求 host 以 `.domain` 结尾（真实子域边界）且不等于 apex 自身。抽取 `matchOrigin` 函数（精确匹配 + 通配子域，大小写不敏感、支持端口与 FQDN 尾点）。
- **C7b 开发态任意 Origin 回显**：开发态原无条件回显任意 Origin，若同时 `AllowCredentials=true` 构成凭据型反射。改仅对 localhost/127.0.0.1/::1 回显（`isLocalhostOrigin`），杜绝任意站点携凭证访问。
- **C7 收尾（信息泄露收敛）**：未匹配 origin 时不再发送 `Access-Control-Allow-Methods`/`Allow-Headers`/`Expose-Headers`/`Max-Age`，避免向未授权 origin 暴露 API 允许的方法/头清单。这些头现仅在 origin 匹配时随 `Allow-Origin` 一并发送。

#### C1：`cache/lock.go` 分布式锁 panic/泄漏/裸断言（cache/lock.go）

- **C1a `WithLockAutoExtend` send-on-closed panic + 锁泄漏**：续期改"父关停 + 子 ack"双 channel（`close(stop)` + `<-finished`），消除旧 `done <- struct{}{}` 向已关闭 channel send 的 panic。`Unlock` 用 `context.WithTimeout(context.Background(), 5s)` 派生超时，避免原 ctx 已取消致解锁失败再泄漏。`fn()` panic 路径加 `defer` 兜底，保证 panic 时也停止续期 goroutine 并释放锁（独立复审发现 CRITICAL）。
- **C1a 一致性（HIGH）**：`WithLock` 的 `defer Unlock` 同改 Background 超时 ctx + defer 兜底，与 `WithLockAutoExtend` 一致。
- **C1b 裸类型断言**：新增 `toInt64(v)` 辅助函数（comma-ok），`NewLock`/`Unlock`/`ExtendLock` 三处 `result.(int64)` 改用之，断言失败返 `ErrLockUnexpectedResult` 而非 panic。新增导出错误 `ErrLockUnexpectedResult`。
- **C1c `TryLock` 忽略 ctx**：`time.Sleep` 改 `select { ctx.Done()/time.After }`，响应取消。
- **C1d 无 fencing token**：`LockToken` 文档化设计局限（需 Redis INCR + 下游校验，框架无法单方面保证），不引入破坏性数据结构变更。

#### C2：`ws` Hub 死锁 + send-on-closed panic + 半开连接泄漏（ws/ws.go）

- **C2a 广播死锁**：`Hub.Run` 的 broadcast 分支原在 `conn.Send` 失败时 `h.unregister <- conn`（向自身消费的 channel 发送，无接收者）→ 永久阻塞、整个 Hub 卡死。改持写锁单次遍历，失败连接行内 `delete + conn.Close()`，去掉 channel 回环。`Send` 改非阻塞投递（缓冲满返回 `ErrSendBufferFull`），避免持写锁期间因慢消费者/已死连接阻塞最长 `pongWait` 导致 Hub stall（C2a-residual）。
- **C2b send-on-closed panic**：`Close()` 不再 `close(c.send)`，仅 `close(c.closeChan)` + `c.conn.Close()`；`Send` 前置 `IsClosed()` 快速失败 + select 兜底。消除 `Close` 与并发 `Send` 的 send-on-closed panic。
- **C2c 半开连接泄漏**：`Handle` 读循环前置 `SetReadDeadline(pongWait)` + `SetPongHandler`（重置读 deadline）；`writePump` 每次写前 `SetWriteDeadline(writeWait)`、ping 周期 `pingPeriod = pongWait*9/10`；写失败主动 `Close` 触发读循环退出，加速半开连接回收。新增常量 `pongWait=60s`/`pingPeriod=54s`/`writeWait=10s` 与导出错误 `ErrSendBufferFull`。

#### C5：`compress` Zip-Slip + 解压炸弹（compress/compress.go）

- **C5a Zip-Slip**：`unzipFile` 改用 `filepath.Join` + 前缀锚定（`absDst+sep`），拒绝条目名含 `..` 逃逸、绝对路径、以分隔符开头；拒绝符号链接条目（`ModeSymlink`）防经软链二次穿越。修复前 `file.Name` 可含 `../`，`os.Create` 覆盖任意文件。
- **C5b 解压炸弹**：`GzipDecompress` 由 `io.ReadAll` 改 `io.LimitReader`；`GzipDecompressFile`/`Unzip` 由 `io.Copy` 改 `io.CopyN` 单条目封顶 + Unzip 累计封顶。新增 `DecompressOptions{MaxBytes, MaxTotalBytes}`（0=默认，-1=不限）与 `*WithOptions` 变体；默认单流/单条目 100MB、Unzip 累计 1GB。

#### C4：`storage` 路径穿越 + 无上传校验 + Get OOM（storage/storage.go）

- **C4a 路径穿越**：Local 的 `Delete/Get/Exists/Upload/UploadFromBytes` 的相对路径全经新增 `safeJoin` 前缀锚定（`rootAbs+sep`），拒绝绝对路径/NUL/`..` 逃逸，杜绝任意文件删/读/探测与任意目录写。OSS 的 `Delete/Get/Exists/Upload/UploadFromBytes` 全经新增 `sanitizeObjectKey` 拒绝含 `..`/绝对路径/空/NUL 的 key。
- **C4b 上传校验**：新增可配置 `UploadPolicy{MaxSizeBytes, AllowedExts, AllowedMIMEs}`（嵌入 `local`/`oss` 配置）。`AllowedMIMEs` 非空时用 `http.DetectContentType` 嗅探前 512B（取主类型比较）并拼回头部。零值不限（兼容下游）。
- **C4c Get 读封顶**：Local `Get` 由 `os.ReadFile` 改为 `io.LimitReader` 封顶，OSS `Get` 同理；默认上限 100MB（`max_read_bytes`：0=默认，-1=不限，正数=该值），防全量读入内存 OOM。
- **HIGH（跨平台）**：OSS object key 拼接由 `filepath.Join`（Windows 产 `\`）改 `path.Join` + `sanitizeObjectKey` 归一化 `\`→`/`，保证 Windows/Linux 部署 key 一致。
- 附：`MkdirAll` 权限 0755→0750（gosec G301）。

### 升级说明 🛠️

- **H6 行为变更（非破坏性，正向修复）**：
  - `BaseRepo` 读操作（`FindByID`/`FindAll`/`FindPage`/`FindWhere`/`Count`/`Exists`/...）默认路由到**从库**（原全部走构造时捕获的主库），支持 `database.UseMaster(ctx)`/`UseReplica(ctx)` 显式路由。未配置从库时仍走主库（`Replica()` 无从库回退主库）。
  - `BaseRepo` 写操作（`Create`/`Update`/`Delete`/`*Batch`/`Restore`/...）显式走**主库**，即便 ctx 标记 `UseReplica` 也不写到从库。
  - `FindPage*` 的 count+list 现包进单事务（每页一次 BEGIN/COMMIT）以保证 total/items 快照一致。高频分页接口会有极小额外往返开销；若不可接受可自行用 `QueryBuilder.Page`（轻量、不包事务）。
  - 跨层/跨 repo 事务 join：外层 `database.TransactionWithContext`（或任意 `*gorm.DB` 事务）中，用 `database.WithTx(ctx, tx)` 注入 ctx 后传给 `BaseRepo` 方法即可参与同一事务。
  - 新增 `BaseRepo.UpdateFields`（局部更新，推荐替代 `Update` 的全列覆写）；`Update`（`Save`）行为不变但文档化其全列覆写语义。
  - `QueryBuilder` 标注为单次使用、非并发安全；终结方法现克隆不污染构建器，`Count`/`Page` 的 count 剥离残留 Limit/Offset。
  - 无 API 签名/配置/migration 变更；下游 `NewBaseRepo[T](database.GetDB())` 用法完全兼容。
- **C8 行为变更（非破坏性）**：panic 响应的 HTTP 状态由 200（ModeBusiness）改为 500，body 不变（`code:500` + msg + `request_id`）。ModeREST 行为不变。下游若按"panic 返 200"做适配（极罕见）需注意。已知局限：若 handler 在 panic 前已 flush 部分响应，HTTP 状态无法再改写（HTTP 固有局限，非本次引入）。
- **C6 行为变更**：API 模式 CSRF token 改为单次消费（每次成功 POST 后需重新 `GenerateAPIToken`）+ 30min TTL；`DoubleSubmitCookie` 的 cookie `HttpOnly` 由 true 改 false。原 API 模式整体不可用，故无真实回归。
- **C4 行为变更（非破坏性）**：
  - 含 `..`/绝对路径的 `Delete/Get/Exists` 路径现被拒绝（`ErrPathTraversal`），合法相对路径不受影响。
  - `Get` 默认读取上限 100MB，超限返回错误；需读大文件请配置 `storage.local.max_read_bytes: -1`（不限）或具体值。OSS 同理（`storage.oss.max_read_bytes`）。
  - 上传目录权限 0755→0750（仅 owner/group 可访问）。
  - 新增可选配置 `storage.local.upload` / `storage.oss.upload`（`max_size_bytes`/`allowed_exts`/`allowed_mime_types`），零值不限制以兼容现有下游；生产环境建议显式配置。
  - 安全约束：本地存储根目录应为框架独占，不与用户可控内容混用（防符号链接二次穿越）。
- **C5 行为变更（非破坏性）**：
  - `Unzip` 现默认拒绝含 `..`/绝对路径的条目（`ErrPathTraversal`）与符号链接条目（`ErrSymlinkEntry`），合法归档不受影响。
  - `GzipDecompress`/`GzipDecompressFile`/`Unzip` 默认解压上限：单流/单条目 100MB、Unzip 累计 1GB，超限返回 `ErrDecompressLimit`。需解压更大文件用 `*WithOptions` 变体设 `MaxBytes: -1`（不限）或具体值。
  - 原函数签名保留；新增 `GzipDecompressWithOptions`/`GzipDecompressFileWithOptions`/`UnzipWithOptions`。
- **C2 行为变更（非破坏性）**：
  - `Connection.Send` 改为非阻塞投递：发送缓冲满时返回 `ErrSendBufferFull`（新导出错误）而非阻塞等待。原阻塞语义的下游需改为重试或关闭连接。
  - `Hub` 广播对发送失败（含缓冲满/已关闭）的连接行内移除并关闭——慢消费者会被踢除（ws 广播 best-effort 语义）。
  - WebSocket 连接现启用读写超时与 ping/pong 心跳：半开连接在 `pongWait`（60s）内退出，不再永久阻塞 goroutine。
  - 公共 API 签名不变；`Connection.send` 不再被 close（内部行为）。
- **C1 行为变更（非破坏性）**：
  - `WithLockAutoExtend`/`WithLock` 的解锁改用独立 `context.Background()` 超时（5s），原 ctx 已取消也能解锁（语义：取消业务 ≠ 继续独占锁）。
  - `WithLockAutoExtend` fn panic 时仍释放锁（defer 兜底）。
  - `TryLock` 重试等待响应 ctx 取消。
  - 新增导出错误 `ErrLockUnexpectedResult`（Lua 返回非 int64）。
- **C7 行为变更（非破坏性，收紧）**：
  - `*.example.com` 通配不再匹配 `notexample.com`/`evil-example.com` 等后缀相同但非真实子域的域名；apex `example.com` 不由通配覆盖，需显式配置。
  - 开发态 CORS 兜底仅对 localhost/127.0.0.1/::1 回显 Origin，不再回显任意 Origin。原本依赖开发态回显任意域名的下游需改用显式白名单。
  - 未匹配 origin 的响应不再携带 `Access-Control-Allow-Methods`/`Allow-Headers`/`Expose-Headers`/`Max-Age`（信息泄露收敛）；匹配时正常发送。
- **C3 行为变更（非破坏性）**：
  - `StreamText`/`StreamChunks`/`StreamWithID`/`Stream` 在客户端断连（`c.Request.Context()` 取消）时返回 `context.Canceled` 而非永久阻塞。
  - `WriteEvent`/`WriteMessage` 现可能返回写错误（旧实现恒 nil）；下游若忽略返回值不受影响。
  - 响应不再手设 `Transfer-Encoding: chunked`。
  - 公共 API 签名不变；`SSEWriter` 新增私有 `ctx` 字段（外部字面量构造需走 `NewSSEWriter`）。
- **H2 行为变更（可能影响下游）**：`utils` HTTP 客户端默认**校验 TLS**（`DefaultHTTPClientConfig.SkipTLSVerify` 由 `true` 改 `false`）。`HTTPGet`/`HTTPPost`/`HTTPPostJSON`/`NewHTTPClient()` 不再默认跳过证书校验。下游访问**自签证书**的内网/开发服务会因证书校验失败报错，需显式 `client.SetSkipTLS(true)` 或 `NewHTTPClientWithConfig(HTTPClientConfig{SkipTLSVerify: true})`。生产环境应保持默认校验。
- **H1 行为变更（Breaking，删除函数）**：
  - **删除** `utils.RandString` / `RandDigit`（math/rand 版本）。字符串随机的用途几乎都是安全场景（token/OTP/验证码/会话 ID），保留 math/rand 版本会诱导误用（H1 的根因正是 GUIDE 推荐 RandString 做 token）。下游迁移：
    - token/OTP/验证码/会话 ID → `RandStringSecure` / `RandDigitSecure`（crypto/rand）。
    - 非安全场景需高性能随机串 → 直接用标准库 `math/rand`。
  - **保留** `RandInt` / `RandInt64`（范围随机，有明确非安全场景：负载均衡/游戏/A-B 分桶），加非安全警示注释。
  - 新增 `RandStringSecure` / `RandDigitSecure`（基于 `crypto/rand`，返 `(string, error)`）与 `ErrRandInvalidLength`。
  - 新增 `RandIntSecure` / `RandInt64Secure`（基于 `crypto/rand` + `big.Int` 拒绝采样无偏，返 `(T, error)`），用于安全 nonce 范围、防猜抽奖、密钥分桶等。
  - GUIDE 示例改用 Secure 版本。
- **C9b 行为变更（可能影响下游）**：
  - `jwt.RefreshToken` 在旧 token 撤销失败（Redis 不可用/抖动）时**不再签发新 token**（fail-closed），返回错误。原行为是丢弃错误仍签发，致新旧 token 双有效。
  - `jwt.InvalidateToken`/`InvalidateTokenByID`/`TokenBlacklist.Add` 在无 Redis 时返回 `ErrBlacklistUnavailable`（新增导出错误），不再静默成功。
  - `IsBlacklisted` 无 Redis 仍返 false（验证侧 fail-open，无 Redis 部署固有局限——安全敏感场景必须启用 Redis）。
  - 签名不变；新增 `ErrBlacklistUnavailable`。
- **H4a 行为变更（非破坏性）**：内存限流器 `RateLimiter` 改固定窗口语义，稳态客户端（低于 rate 的持续请求）不再被误限流。`Allow`/`NewRateLimiter`/`Stop` 签名不变；新增 `SetNowFunc`（测试用可控时钟）。
- **H5 行为变更（非破坏性）**：`handler.BadRequest`/`handler.InternalError` 不再硬编 HTTP 400/500，改为遵循当前响应模式（委托 `response` 体系）：默认 `ModeBusiness` 下两者均返回 HTTP 200（错误经 body `code` 表达，与 `response.Fail*` 一致）；`ModeREST` 下 `InternalError` 返回 500（`CodeServerError` 映射），`BadRequest` 返回 200（`CodeFail` 属业务失败不映射 HTTP 错误，与 `response.Fail` 一致）。两者现写入 `RequestID`。下游若依赖 `handler.BadRequest` 恒返 400 需改用 `response.Custom(c, 400, code, msg, nil)` 或业务自定义 4xxxx 错误码。
- **H4c 行为变更（可能影响下游）**：
  - **`LoginRedisRateLimit` 改 fail-closed**：Redis 故障/未启用时由放行改为拒绝（HTTP 503，`CodeServiceUnavailable`）。登录防爆破场景下 Redis 故障不再静默放行（原 fail-open 致限流失效）。下游登录接口须确保 Redis 可用，否则登录会在 Redis 故障时不可用（安全语义：宁拒勿放）。
  - 其余 Redis 限流中间件（`RedisRateLimit`/`APIRedisRateLimit`/`UploadRedisRateLimit`/`CustomRedisRateLimit`/`RedisRateLimitWithIdentifier`）保持 fail-open（兼容默认）。
  - 新增 fail-closed 变体：`RedisRateLimitFailClosed`/`CustomRedisRateLimitFailClosed`/`NewRedisRateLimiterFailClosed`/`SetFailClosed`，供安全敏感场景选用。
  - 新增导出错误 `ErrRedisRateLimiterUnavailable`/`ErrRedisRateLimiterUnexpectedResult`。
  - 无既有 API 签名变更。
- 无 API 签名变更、无 migration。新增测试依赖 `github.com/alicebob/miniredis/v2`。

---

## [1.1.1] - 2026-06-23

> 本版本为 v1.1.0 的补丁发布：补 ServerConfig.Host 字段、统一面向用户文案为中文、修正 README 过时/错误描述。

### Added ✨

#### ServerConfig.Host（绑定地址）

`server` 新增 `host` 字段，控制监听地址：

- `host: ""`（默认）→ `:8080`，监听所有接口（0.0.0.0），向后兼容
- `host: "127.0.0.1"` → `127.0.0.1:8080`，仅本机（前面有 nginx 时常用）
- `host: "10.0.0.5"` → 绑定内网网卡

避免生产环境无意暴露在 0.0.0.0。启动日志相应区分"所有接口"/指定地址。

### Changed 🔄

#### 面向用户文案统一中文

v1.1.0 前部分面向用户/调用的文案为英文，与其余中文文案不一致。本次统一为中文：

- `middleware/recover.go`：`"Panic recovered"` → `"panic 已恢复"`；`"Panic: %v"` → `"服务器内部错误: %v"`（消除同文件内中英矛盾）
- `middleware/logger.go`：5 处日志消息（慢请求/请求错误/客户端请求错误/API 请求/请求）改中文
- `middleware/metrics.go`：3 个 Prometheus `Help` 文本改中文
- `app.go` / `database/manager.go` / `logger/logger.go`：英文 error 改中文

**保留英文**（非文案，属协议/约定/技术必需）：JSON 字段名（`code`/`msg`/`data`）、health 探针状态枚举（`ok`/`error`/`disabled`）、Prometheus metric `Name`（命名规则限制）、`database/manager.go` 中匹配 MySQL 驱动错误串的英文（`"Access denied"` 等，改了会失效）、Redis/CSRF Token/JWT/OSS 等技术专有名词。

### Fixed 🐛

#### README 错误描述修正

v1.1.0 后 README 存在过时/错误描述，照抄会导致新用户启动失败，本次修正：

- **删除目录结构里已移除的 `wire/` 段**（wire 包 v1.1.0 已删）
- **快速开始配置示例**：`jwt.secret` 补足 ≥32 字节（否则被 v1.1.0 `Validate` 拦截启动失败）；`expire: 86400`（int 秒）改为 `expire: "24h"`（`time.Duration`），补 `refresh_expire`/`issuer`/`algorithm`
- `server` 段补 `host`/`read_timeout`/`write_timeout`/`idle_timeout`/`shutdown_timeout`/`response_mode` 字段
- v1.0.2 更新日志标注 `WithWire` 已于 v1.1.0 移除
- 目录结构补 v1.1.0 新文件：`middleware/metrics.go`、`middleware/timeout.go`、`router/metrics.go`、`config/validate.go`、`response/mode.go`
- 框架特性段重写为三组（架构可注入 / 生产就绪 / 基础功能），补全 v1.1.0 能力
- 响应格式段补 `Mode` 开关（`ModeBusiness`/`ModeREST`）与 `Custom` API

#### README 首段重写

原首段描述（"轻量级 Web 开发框架，提供完整后端基础设施"）过于普通化，适用于多数 Gin 脚手架，无辨识度。重写为：

- tagline 点明核心差异：组件全部 Manager 化，简单调用与注入实例兼得
- "为什么是 xlgo"段：对比一般 Gin 脚手架的包级单例痛点 + 对照代码
- 5 条差异化卖点（可注入 / 生产就绪内置 / 零 Fatal / 默认轻量 / 可插拔方言）
- 30 秒上手极简可跑示例

### 升级说明 🛠️

从 v1.1.0 升级无破坏性变更，`go get github.com/EthanCodeCraft/xlgo-core@v1.1.1` 即可。`host` 字段默认空，行为与 v1.1.0 一致。

---

## [1.1.0] - 2026-06-23

> 本版本定位为 **HA & Manager 化 release**：高可用与生产就绪改进 + 组件 Manager 化。对应体检报告 #10-#24。
> 含少量破坏性变更，升级前请阅读下方「升级说明」。

### Breaking ⚠️

详见下方「升级说明」。

- 删除 `wire` 包及其 `WithWire` Option（其事 App Option 已覆盖）。`WithoutWire` 保留为空 stub 以兼容调用。
- 删除 `AppConfig.TokenExpire` 字段（与 `JWTConfig.Expire` 重复），过期统一由 `jwt.expire` 配置。
- `JWTConfig.Expire` 类型由 `int`（秒）改为 `time.Duration`（如 `"24h"`）。
- 删除 `StartServerWithPort` 与 `GracefulShutdown` 双轨函数（与 `App.StartServer`/`App.Shutdown` 重复）。

### Added ✨

#### 组件 Manager 化（#10）

storage / cache / redis / jwt / logger 五个组件照 `database.Manager` 模式新增 `XxxManager` + `DefaultXxx` + `SetDefaultXxxManager`，包级 facade 保留兼容存量。支持多实例与测试注入 mock：

```go
// 注入自定义实现 / 多实例
database.SetDefaultRedisManager(myRedisMgr)
cache.SetDefaultCacheManager(mockCacheMgr)
jwtMgr := jwt.NewJWTManagerWithRedis(refreshRedisClient) // 独立黑名单
```

#### Lifecycle Hooks（#12）

```go
xlgo.New(
    xlgo.WithHook(xlgo.Hook{
        Name:    "register-service",
        OnStart: func(a *xlgo.App) error { return registerToDiscovery() },
        OnStop:  func(a *xlgo.App) error { return deregisterFromDiscovery() },
    }),
)
```

各阶段：`OnInit`（Init 内组件就绪后）/ `OnStart`（监听前）/ `OnReady`（端口就绪后）/ `OnStop`（Shutdown 开头）。

#### App.Go + in-flight goroutine（#22）

`App.Go(func(ctx context.Context))` 启动受 App 生命周期管理的后台 goroutine，Shutdown 时 cancel ctx 并 `wg.Wait`（带 `shutdown_timeout` 超时），避免业务异步任务被进程退出强制砍掉。

#### Server 参数配置化（#13）

`server` 新增 `read_timeout`/`write_timeout`/`idle_timeout`/`shutdown_timeout`/`max_header_bytes`/`tls`/`unix_socket`/`response_mode`，缺省回退原硬编码值。支持 TLS 与 unix socket 监听。

#### JWTConfig time.Duration（#14）

`jwt.expire`/`refresh_expire` 用 `time.Duration`（`"24h"`/`"168h"`），新增 `issuer`/`algorithm`（HS256/HS384/HS512）。删除冗余的 `AppConfig.TokenExpire`。

#### Config Validate（#16）

`Config.Validate()` 在 `Manager.Load` 解析后自动调用，校验端口范围、JWT 密钥长度（≥32 字节）、启用数据库时关键字段、TLS 证书、Duration 非负等。把配置错误从"运行时第一次请求"提前到"进程启动"。

#### response REST 模式（#15）

`response.SetMode(ModeBusiness|ModeREST)`，默认 `ModeBusiness`（全 200 + 业务码，兼容存量）。`ModeREST` 下失败响应按错误码映射 HTTP status（401/404/429/500...），body 仍带业务码，便于 APM/Prometheus/网关按 status 区分异常。可在 `server.response_mode` 配置。新增 `response.Custom(c, httpStatus, code, msg, data)`。

#### livez / readyz（#17）

```go
xlgo.New(xlgo.WithLivenessRoute(), xlgo.WithReadinessRoute())
// GET /livez  永不依赖外部，始终 200（K8s livenessProbe）
// GET /readyz 复用 healthChecks，失败 503（K8s readinessProbe）
```

`/health` 保留兼容。`WithFullStack`/`NewFullStack` 默认启用。

#### Prometheus metrics（#18）

```go
xlgo.New(xlgo.WithMetricsRoute()) // 默认 /metrics
```

`middleware.Metrics()` 采集 `http_requests_total` / `http_request_duration_seconds` / `http_requests_in_flight`。新增 `prometheus/client_golang` 依赖。

#### 请求级 Timeout 中间件（#19）

`middleware.Timeout(d)` 为每个请求的 context 设 deadline，下游 GORM/Redis 走 `c.Request.Context()` 级联取消。可通过 `WithRequestTimeout(d)` 装入全局。

#### 依赖健康自愈（#21）

`database.Manager` 后台探活（`App.Go` 启动，每 `health_check_interval` ping 一次）：主库连续失败达阈值标记不健康，`/readyz`/`/health` 返回 503；从库失败临时剔除读流量，恢复自动重新纳入。新增 `database.ConnMaxIdleTime`/`health_check_interval`/`health_check_failure_threshold` 配置。

#### RequestID 默认装入（#24）

`App.Init` 无条件装入 `middleware.RequestID()`（在 Recovery 之前），让每个响应/panic 日志都带 `request_id`。移除 `gin.Recovery()` 双重保险，统一用 `middleware.Recover()`（#23 已带 request_id）。

### 升级说明 🛠️

1. **wire 包删除**：移除 `import "github.com/EthanCodeCraft/xlgo-core/wire"` 与 `wire.InitServices()`/`WithWire()` 调用。原由 wire 触发的 `cache.Init()` 现由 `WithRedis` 自动触发。
2. **AppConfig.TokenExpire 删除**：改用 `jwt.expire` 配置 token 过期。grep `token_expire` 清理旧配置。
3. **JWTConfig.Expire 类型变更**：YAML 由 `expire: 86400`（秒）改为 `expire: "24h"`（Duration 字符串）。代码中 `time.Duration(cfg.JWT.Expire) * time.Second` 改为直接 `cfg.JWT.Expire`。
4. **StartServerWithPort / GracefulShutdown 删除**：改用 `App.Run()` / `App.Shutdown()`。
5. **JWT 密钥长度**：`Config.Validate` 要求启用 JWT 时 secret ≥32 字节，原短密钥会在启动期被拦截，请生成足够长的随机密钥。
6. **配置文件**：建议补 `server.read_timeout` 等字段（缺省自动回退，不强制），`jwt.expire` 必须改为 Duration 字符串。

---

## [1.0.4] - 2026-06-22

> 本版本定位为 **DX & Docs release**：开发体验与文档改进，无破坏性 API 变更。对应体检报告 #25/#27/#28/#29/#30。

### Added ✨

#### CLI 多模板（#28）

`xlgo new` 新增 `--template` 参数，支持三种脚手架模板：

```bash
xlgo new myapp --template minimal     # 轻量 HTTP，无 MySQL/Redis 依赖
xlgo new myapp --template api         # 标准业务 API，含分层目录（默认）
xlgo new myapp --template fullstack   # 全组件，NewFullStack 一键启用
```

- `minimal`：仅 logger + health + 示例路由，目录结构最小化，第一次接触 xlgo 从这里开始
- `api`：含 handler/model/repository/service 分层 + MySQL/Redis/JWT 配置（默认模板）
- `fullstack`：`NewFullStack` 全组件 + Swagger + Storage

#### examples/ 目录（#29）

新增两个可运行示例，帮助快速上手：

- `examples/minimal/` — 50 行可跑，不依赖外部服务
- `examples/full/` — MySQL + Redis + JWT + user CRUD 完整示例（登录发 token、认证路由、创建/查询用户）
- `examples/README.md` — 运行说明与接口文档

#### docs/ 文档结构（#30）

- 新增 `docs/` 目录，`docs/plans/` 归档历史规划与体检报告
- 新增 `docs/README.md` 文档索引
- `Version_Update_Plan_v1.0.2.md` → `docs/plans/`
- `Version_v1.0.2_report.md` → `docs/plans/`
- 早期 `report.md` → `docs/plans/v2.0-review.md`
- `CHANGELOG.md` / `GUIDE.md` 按惯例保留在仓库根目录

### Changed 🔄

#### 模块路径文档改进（#25）

经评估**保留** `xlgo-core` 模块路径——`-core` 后缀反映这是 xlgo 多产品系列（xlgo-core / xlgo-orm / xlgo-ai ...）的核心产品，不去掉。模块路径（`github.com/EthanCodeCraft/xlgo-core`）与包名（`xlgo`）不同是 Go 惯例（cf. `github.com/gin-gonic/gin` → 包名 `gin`）。

改进文档说明，消除新用户 `go mod tidy` 撞墙的困惑：

- README 快速开始新增「模块路径与包名」小节，给出完整 import 示例：
  ```go
  import xlgo "github.com/EthanCodeCraft/xlgo-core"
  ```
- CLAUDE.md `Import Path Note` 措辞明确化，说明 module path / package name / `-core` 语义

#### Without* Option 定位文档化（#27）

经调研 `Without*` 系列 Option 有真实用例（测试覆盖「先开再关」语义 + `NewFullStack` 后排除单项），**不删除、不标 Deprecated**，改为文档化其定位：

- `app.go` `WithoutLogger` 注释说明：`Without*` 主要用于 `NewFullStack` / `RunFullStack` 启用全部组件后排除个别项
- README 快速开始补充用法说明：`xlgo.NewFullStack(xlgo.WithoutSwaggerRoutes())`

### 依赖与构建

- `.gitignore` 整理：忽略 `CLAUDE.md`、构建产物（`*.exe` / `bin/`）、临时发版文件（`gitHub_release_*.md`）

### 升级说明

v1.0.4 **无破坏性变更**，从 v1.0.3 升级只需：

```bash
go get github.com/EthanCodeCraft/xlgo-core@v1.0.4
go mod tidy
```

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

[Unreleased]: https://github.com/EthanCodeCraft/xlgo-core/compare/v1.2.0...HEAD
[1.2.0]: https://github.com/EthanCodeCraft/xlgo-core/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/EthanCodeCraft/xlgo-core/releases/tag/v1.1.1
[1.1.0]: https://github.com/EthanCodeCraft/xlgo-core/releases/tag/v1.1.0
[1.0.4]: https://github.com/EthanCodeCraft/xlgo-core/releases/tag/v1.0.4
[1.0.3]: https://github.com/EthanCodeCraft/xlgo-core/releases/tag/v1.0.3
[1.0.2]: https://github.com/EthanCodeCraft/xlgo-core/compare/v1.0.1...v1.0.3
[1.0.1]: https://github.com/EthanCodeCraft/xlgo-core/releases/tag/v1.0.1
[1.0.0]: https://github.com/EthanCodeCraft/xlgo-core/releases/tag/v1.0.0
