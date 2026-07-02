# xlgo 框架源码缺陷权威清单（v1.1.1）

> 本文件是**三方合并 + 独立源码核验**后的唯一权威清单，作为后续修复的总执行依据。
>
> - **来源 1**：初版评估报告（5 agent 并行精读 + 根 app.go 独立精读）。
> - **来源 2**：复审复核（`v_1.1.1_fix.md`，逐条回源码核实，含 12 条勘误）。
> - **来源 3**：独立对抗性核验（本次，4 路并行 agent 回源码 `file:line` 再核）。
>
> **核验结论**：13 项 CRITICAL + 8 项 HIGH **全部真实成立，无虚报**。复审的 12 条勘误中 11 条完全成立、1 条扩展（C12e 步长分支）。本清单已将勘误与核验精化**就地合并**（原报告失实之处已改正，不再单列勘误段）。仅 4 处措辞/严重性精化标注 `[核验精化]`。
>
> 每条标注：`[裁定]`（CONFIRM / PARTIAL）、`file:line`、缺陷描述、修复方案、验证方式。后续按文末"修复优先级"逐项执行，每项遵循 `xlgo/CLAUDE.md` 的开发纪律（全局架构优先 / 并发纪律 / 运行验证 / 独立复审 / 反模式清单）。

---

## 总体结论

方向正确、工程化程度不低（实例化 Manager、方言/DSN 注册表、读写分离、Option 模式、生命周期 Hook、优雅关闭、OTel/Prometheus 接入）。但距离"通用、高可用、易上手"差一次以"正确性 + 安全 + 一致性"为主线的收敛。

最突出的问题是**并发纪律**与**跨文件契约一致性**：包级全局指针无锁置换、热重载绕过校验、关键中间件存在安全/正确性缺陷、局部正确而全局断裂（BaseRepo 不接 ctx 路由、多个 `/health` 行为不一、handler 绕过响应模式）。共 **13 项 CRITICAL、8 项 HIGH**。

---

## CRITICAL（安全漏洞 / 数据竞争 / 数据丢失 / 死锁）

### C1 `cache/lock.go` 分布式锁多处致命缺陷  `[CONFIRM]`

- **C1a `WithLockAutoExtend` closed-channel panic + 锁泄漏**（lock.go:192,194,206-208,217-218）：`done` 无缓冲，子 goroutine `defer close(done)`；ctx 取消（:200）或 `ExtendLock` 失败（:206-208）提前返回时 `done` 已 closed，父在 `fn()` 后 `done <- struct{}{}`（:217）→ send-on-closed panic，`Unlock`（:218）不执行，锁持有到 TTL。
- **C1b 裸类型断言**（lock.go:76/108/142）：`result.(int64)` 无 comma-ok，Redis 返回 nil/非整型时 panic。
- **C1c `TryLock` 忽略 ctx**（lock.go:159）：`time.Sleep(retryInterval)` 不响应 `ctx.Done()`，最长阻塞 `maxRetry*retryInterval`。
- **C1d 无 fencing token**（lock.go:19-23）：`Token` 是随机 UUID 非单调递增，TTL 到期后双 worker 并发执行无防护。

**修复**：续期改"父关停 + 子 ack"双 channel，仅父方 `close(stop)`；用 `context.Background()` 派生超时做 `Unlock`；断言一律 comma-ok；TryLock 用 `select { case <-ctx.Done(): return nil, ctx.Err(); case <-time.After(...): }`。

**验证**：`go test -race`——ctx 取消路径、ExtendLock 失败路径、Unlock 必执行；并发抢锁 + TTL 到期场景。

---

### C2 `ws/ws.go` Hub 自发送死锁 + channel 关闭竞态  `[CONFIRM]`

- **C2a 广播失败即自发送死锁**（ws.go:250,272-281）：`h.unregister` 无缓冲，唯一消费者是同一 `Run` goroutine 的 select（:264）。广播分支内 `conn.Send` 失败时 `h.unregister <- conn`（:277），Run 正忙于 broadcast case 无法同时进 unregister case → 永久阻塞，整个 Hub 卡死。`[核验精化]` 经源码确认：map 已由 `:273 h.mu.RLock` 保护，**无 data race**；真 bug 仅为自发送死锁（原报告"并发修改 map"措辞已改正）。
- **C2b `Close()` 与 `Send()` send-on-closed panic**（ws.go:85-86,59-66）：`Close` 先 `close(c.closeChan)` 再 `close(c.send)`；并发 `Send` 的 select 含 `c.send <- data`（:61）与 `<-c.closeChan`（:63），`c.send` 关闭后 send case 永久就绪且 panic，Go select 伪随机可能选中它——`closeChan` 分支**不能**消除竞态。
- **C2c 无 deadline / 无 pong handler / goroutine 泄漏**（ws.go:180,194-222）：`SetReadDeadline/SetWriteDeadline` 有定义（:102-109）却从不内部调用；发 ping（:215）但无 `SetPongHandler`、无读超时 → 半开连接 `ReadMessage` 永久阻塞、goroutine 泄漏。

**修复**：广播分支持写锁单次遍历、行内 `delete + conn.Close()`，去掉 channel 回环；`Close()` 删除 `close(c.send)`，仅以 `closeChan` 作关闭信号；读循环前置 `SetReadDeadline + SetPongHandler`，每次写前 `SetWriteDeadline`，ping 周期 `<` pongWait。

**验证**：`go test -race`——并发广播失败 + 并发 Close/Send；半开连接超时退出、无 goroutine 泄漏（pprof 前后对比）。

---

### C3 `sse/sse.go` 断连泄漏 goroutine + 算力（AI 主场景）  `[CONFIRM]`

- **C3a 循环无 `ctx.Done()` 分支**（sse.go:79/104/120/142）：四个 `for range ch` 仅靠 ch 关闭或写错误退出。
- **C3b（核心）写/Flush 错误被吞，断连永不触发守卫**（sse.go:38-43,47-51,54-60）：`WriteEvent`/`WriteMessage` 丢弃 `fmt.Fprintf` 返回错误且恒 `return nil`，`Flush()` 无返回值；`WriteJSON` 仅在 `json.Marshal` 失败时返错，否则透传 nil。故 `StreamText` 等的 `if err := WriteJSON(...); err != nil` 守卫**只对 marshal 失败生效，对客户端断连永不触发** → 消费循环不退出 + 上游生产者（常为昂贵 LLM 流）持续运行直到进程结束。
- **C3c 手设 `Transfer-Encoding: chunked`**（sse.go:23）：HTTP/1.1 冗余，HTTP/2 非法（应交由 server 分帧）。

**修复**：`WriteEvent/WriteMessage` 返回 `fmt.Fprintf` 错误；Stream 系列改 `for { select { case <-c.Request.Context().Done(): return ctx.Err(); case v, ok := <-ch: ... } }`；**生产者也必须接收由 `c.Request.Context()` 派生的取消信号**；删除 chunked 手设头。

**验证**：实跑——客户端断连后断言生产者停止、无 goroutine 泄漏（pprof）。

---

### C4 `storage/storage.go` 路径穿越 + 无校验  `[CONFIRM]`

- **C4a 路径穿越**（按方法分级，storage.go:152/163/173/269/279/295/61/108/216/243）：`filepath.Join` 内含 `Clean`，`..` 可逃逸根目录。最严重：`Delete`/`Get`/`Exists`/`GetURL` 及 OSS 的 `Delete`/`Get`/`Exists`——`path` 全程受控（任意删/读/探测/URL 构造）。`Upload`/`UploadFromBytes` 仅 `subdir` 受控、文件名服务端随机（:72/:122 `uniqueFilename`）→ 任意目录写但无法精确覆盖。OSS 变体为未净化 object key。
- **C4b 无扩展名/类型/大小校验**：全文件无 `file.Size` 引用、无扩展名白名单、无 MIME 校验；`evil.php`、超大文件直传。
- **C4c 全量读入内存**（storage.go:165/287）：Local `os.ReadFile`、OSS `io.ReadAll` 无上限 → OOM。

**修复**：新增 `resolve(rel)` 做 `filepath.Abs` + 前缀锚定校验，`Delete/Get/Exists/Upload` 统一经过；上传前查 `file.Size` 上限 + 扩展名白名单 + `http.DetectContentType` 嗅探前 512B；`Get` 提供流式 `io.ReadCloser` 或 `io.LimitReader` 封顶。

**验证**：路径穿越（`../` 拒绝）、Zip-Slip、超大输入封顶、`evil.php` 拒绝——必须有用例实跑。

---

### C5 `compress/compress.go` Zip-Slip + 解压炸弹  `[CONFIRM]`

- **C5a Zip-Slip**（compress.go:176,195）：`filePath := path.Join(dstDir, file.Name)` 无逃逸校验，`os.Create` 可覆盖任意文件；且用 `path.Join` 非 `filepath.Join`，Windows 分隔符处理不当。
- **C5b 解压炸弹**（区分成立，compress.go:37/87/201）：`GzipDecompress`（:37 `io.ReadAll`）为 **OOM**；`GzipDecompressFile`（:87）/`Unzip`（:201）为 `io.Copy` 写盘，属**磁盘耗尽**而非 OOM。`[核验精化]` 原报告"均 OOM"措辞已改正。

**修复**：解压前 `filepath.Clean(dstDir)` + 前缀锚定校验，拒绝 `..`；跳过/拒绝 `file.Mode()&os.ModeSymlink != 0` 的符号链接条目；`io.CopyN` 单条目封顶 + 累计上限；`GzipDecompress` 用 `io.LimitReader`。

**验证**：Zip-Slip 拒绝、符号链接穿越拒绝、解压炸弹封顶——必须有用例。

---

### C6 `middleware/csrf.go` API CSRF 模式功能性失效（map 遮蔽）  `[CONFIRM]`

- **C6a map 遮蔽**（csrf.go:228,247-249,271-273,281-284）：`CSRFForAPI()` 内 `tokens := make(map[string]bool)`（:228）是**局部变量**，闭包校验读它（:247-249）；`GenerateAPIToken` 写的是**包级** `tokens`（:272，声明于 :281-284）。两者从不相交 → 颁发的 token 永不在校验 map 里，**所有非安全方法请求被判"CSRF Token 无效"拒绝，API CSRF 模式整体不可用**。且局部 map 每次调用 `CSRFForAPI()` 重建。
- **C6b 只增不减、无过期、验证不消费**（csrf.go:281-284,272,257）：包级 `tokens` 无 delete/TTL/淘汰，内存 DoS + token 永久可重放。
- **C6c 矛盾点修正**（csrf.go:60/123-135/328/346）：`[核验精化]` `CSRF()` cookie 模式 token 经 **cookie** 下发（:123-131）+ 上下文暴露（:135），HttpOnly=true 在该模式下**合理**（前端经 `GetCSRFToken` 取 token，非读 cookie），原报告对 :60 的指控**证伪**。真正矛盾在 `DoubleSubmitCookie`（:328 `SetCookie(...HttpOnly=true)` 与 :346 要求 JS 回填 `X-CSRF-Token` header）：HttpOnly 使 JS 读不到 cookie → 无法回填 → 双重提交对真实前端不可用。

**修复**：删除 `CSRFForAPI` 内的 `tokens`/`mu` 两个局部声明使其绑定包级；改单次消费 + TTL（`map[string]time.Time`，验证后 `delete`），生产环境落 Redis `SETEX`+`GETDEL`；`DoubleSubmitCookie` 的 cookie 改 `HttpOnly=false`，`CSRF()` 维持 true。

**验证**：颁发→校验闭环（token 真正生效）、单次消费、TTL 过期、`DoubleSubmitCookie` 前端可回填——必须有用例。

---

### C7 `middleware/cors.go` 通配符后缀绕过  `[CONFIRM]`

- **C7a**（cors.go:52-57）：`*.example.com` → `domain="example.com"`，`strings.HasSuffix(origin, domain)` 未锚定 host → `https://notexample.com`、`https://evil-example.com` 被接受。
- **C7b**（cors.go:75-77,103-105）：开发态无条件回显任意 Origin；若同时 `AllowCredentials:true` 则构成凭据型反射。

**修复**：`*.` 通配改用 `net/url` 解析 host，要求真实子域边界；开发态兜底限制为 localhost 列表，不回显任意 Origin，且回显来源不与 credentials 并存。

**验证**：`notexample.com` 拒绝、真实子域通过、凭据型反射不成立——必须有用例。

---

### C8 `middleware/recover.go` 500 状态丢失（默认 ModeBusiness）  `[CONFIRM]`

- 默认模式 `ModeBusiness=iota=0`（mode.go:16），`currentMode` 零值即默认（:23）。`httpStatusFor(CodeServerError)` 在默认模式返回 200（mode.go:60-65）。`Recover` 中 `FailWithCode → writeResp → c.JSON(200,...)`（response.go:68-75）已 flush 锁定状态，随后 `c.AbortWithStatus(500)`（recover.go:33）因 `w.Written()==true` 成为 no-op（gin responseWriter `WriteHeader` 在 Written 后仅 WARNING 不改码），客户端收 HTTP 200 + body `code:500`，网关/APM 按 status 看不到 panic。`RecoverWithDetail`（:57-58）同病。
- **ModeREST 无此 bug**：`statusForCode(CodeServerError)=500`（mode.go:48-49），状态一致。

**修复**：用不受 Mode 影响的 `response.Custom(c, http.StatusInternalServerError, response.CodeServerError, "服务器内部错误", nil)` 显式写 500，并去掉事后 `AbortWithStatus`。

**验证**：真实触发 panic，断言**实际 HTTP 状态码 = 500**（非 200）——必须有用例。

---

### C9 `jwt/jwt.go` 黑名单缺陷  `[CONFIRM]`

- **C9a 无 Redis 静默失效**（jwt.go:71-101）：`Add`/`IsBlacklisted` 在 client==nil 时静默 `return nil`/`return false`，登出/吊销无效且无信号。
- **C9b（最危险）RefreshToken 吞 Add 错误**（jwt.go:281-293）：`tokenBlacklist.Add(claims.JTI, ...)`（:289）返回值被丢弃，仍 `return GenerateToken(...)`（:292）→ Redis 抖动时旧 token 未拉黑、新旧 token 双有效，会话固定窗口。叠加 C9a 静默 nil，失败被双重吞没。
- **C9c SetDefaultJWTManager 无锁置换**（jwt.go:124-129,139,243,289）：包级 `DefaultJWT`/`tokenBlacklist` 裸写，与请求 goroutine 读竞争。

**修复**：`RefreshToken` 对 `Add` 错误 `return "", fmt.Errorf(...)`；无 Redis 时让 `Add` 返回 `ErrBlacklistUnavailable` 或启动期告警一次；包级指针改 `atomic.Pointer` 或文档强制"服务前调用"。

**验证**：刷新令牌闭环——Redis 故障时刷新失败（不签发新 token）、旧 token 入黑名单后失效——必须有用例。

---

### C10 `config/config.go` 全局 Manager 无锁置换 + 热重载绕过校验  `[CONFIRM]`

- **C10a**（config.go:579/585/628-634/605-607）：`defaultManager` 包级指针被 `Load`/`LoadWithWatch`/`SetDefaultManager` 裸写，与 `Get` 等读者竞争。
- **C10b**（config.go:486-501/543-575）：`OnConfigChange` 与 `Reload` 均**不调 `Validate()`**（Validate 仅在 `Load` :435 调用），非法配置（坏端口、负超时、短密钥）直接发布；解析失败 `return` 静默吞。
- **C10c**（config.go:441/444）：`Load` `return &cfg` 即 `m.cfg` 同一指针，调用方可变并竞争；热重载只换 `m.cfg`，**不重建 DB/Redis 池**，对子系统装饰性。
- **C10d**（config.go:507）：`StopWatcher` 空函数，viper watcher goroutine + fd 永不释放。

**修复**：`defaultManager` 改 `atomic.Pointer[Manager]`；热重载补 `unmarshal` 失败告警 + `newCfg.Validate()` 失败保留旧配置；`Load` 返回防御性拷贝；自管 `fsnotify.Watcher` 以便 `StopWatcher` 真正 `Close`。

**验证**：`go test -race`——`SetDefaultManager` 并发读写；热重载坏配置保留旧配置；`StopWatcher` 后 watcher goroutine 退出。

---

### C11 `database/manager.go` 并发与生命周期  `[CONFIRM]`

- **C11a 健康状态陈旧（不越界）**（manager.go:144-155,117-128,218-241）：`initReplicaHealth` 有 `replicaHealthSet` 早返回，重新 `InitDBWithReplicas` 不重置 → 健康切片与新 replicas 长度错位。`[核验精化]` 经源码确认 `Replica()`（:120）与 `probeOnce`（:224/230/233/237）均有 `i < len(...)` 守卫，**不越界 panic**；真问题是新从库健康状态陈旧/被排除（原报告"越界"措辞已改正）。
- **C11b InitDB 重试泄漏**（manager.go:346-389）：`gorm.Open` 成功但 `Ping` 失败时旧池不关、下轮覆盖 `m.master`，每次重试泄漏一池。
- **C11c InitDBWithReplicas 泄漏**（manager.go:423）：`m.replicas = nil` 前不关旧从库池。
- **C11d Master()/Replicas() 无锁读**（manager.go:97-104）：与 `Close`/`InitDB` 写竞争，可能返回已关闭/nil 池。
- **C11e 非缺陷**（manager.go:39-45/51-56）：`[核验精化]` `RoundRobinPicker` `int(n-1)%len` 取模后仍在 `[0,len)` 内，无 panic/正确性问题；`RandomPicker` 全局 `math/rand` 仅锁竞争。**属微优化，非功能 bug**（原报告若主张为 bug 则证伪）。
- **C11f 包级 Close() 仅关主库**（manager.go:524-536/539-541/277-307）：包级 `Close()` 仅关 master 且无锁，`CloseAll()`/方法 `Close()` 关全部，命名误导致用户泄漏从库。

**修复**：`InitDBWithReplicas` 重建前关旧主/从池并重置 `replicaHealthSet=false`、`replicaHealthy=nil`；`InitDB` 重试在覆盖前 `sqlDB.Close()`；`Master/Replicas/Replica` 全程加锁或改 `atomic.Pointer`；包级 `Close()` 改为委托 `CloseAll()`（或废弃）。

**验证**：`go test -race`——`Master`/`Replicas` 并发读写；重试路径无池泄漏（连接数前后对比）。

---

### C12 `cron/cron.go` 数据竞争 + 重叠执行 + 漂移  `[CONFIRM]`

- **C12a**（cron.go:142-143,104-125,200-209）：`runTask` 无锁写 `LastRun`/`RunCount`，`GetTask`/`ListTasks` 返回 live 指针并发读 → data race。
- **C12b 无 running 守卫**（cron.go:147-149,200-209）：`NextRun` 在 handler 完成后才更新，长任务跨 tick 被 `checkAndRun` 反复 spawn。
- **C12c Interval 漂移**（cron.go:148,218-219）：`Next` 以 handler 完成后 `time.Now()` 锚定，每周期累积 handler 时长。
- **C12d Weekly 跳过当天未到点目标**（cron.go:255-263）：`daysUntil <= 0 → +7` 仅按 weekday 差值判断，不比较当天时刻；当天目标时刻未到（如周一 9:00 目标周一 12:00）被跳一周。`DailySchedule`（:236）用 `next.Before(now)||next.Equal(now)` 正确处理，Weekly 缺失此比较。
- **C12e cron 解析缺陷**（cron.go:310-434,426-433）：`parseInt` 忽略非数字逐位累积。`1-5,8` 因先判 `-`（:316）→ `parseInt("5,8")=58`，范围被破坏为 1..58 且逗号项丢失；`7` 周日不匹配（Go Sunday=0）。`[核验精化]` `garbage` → `parseInt=0`：列表/范围分支中 `0==value`，**分/时/周字段在 value=0 时错误触发**，日(1-31)/月(1-12)永不触发；**但步长分支 `*/garbage` → step=0 → `:331 return true` 匹配全部**，故日/月在步长场景也会误触发（复审漏看步长分支，本清单补全）。

**修复**：`runTask` 计数写入纳入锁、Getter 返回值拷贝；加 per-task `running atomic.Bool` 或持锁的 `checkAndRun` 内先推进 `NextRun` 再 spawn；Interval 以上次 `NextRun` 锚定；Weekly 用 `((day-now)+7)%7` 且整日期时间 `!After(now)` 才 +7；cron 解析改 `strconv.Atoi` 返错 + 字段范围校验 + 周日 `7→0` + 列表分支独立于范围分支。

**验证**：`go test -race`——`runTask` 并发；`1-5,8` 仅匹配 1-5 与 8；Weekly 当天未到点不跳周。

---

### C13 `trace/trace.go` opt-in 即崩  `[CONFIRM]`

- **C13a nil tracer panic**（trace.go:58,160,217,225）：包级 `tracer` 未 `Init` 即 nil，`Middleware`/`StartSpan` 等无守卫 → 首个请求 panic。（`Init` 即便 `Enabled=false` 也设 Noop tracer，故仅"从未 Init"才崩。）
- **C13b 未知导出器 → nil + stdout 不存在**（trace.go:110-125,84-91）：switch 仅 otlp-http/otlp-grpc，`default` 返回 `nil, nil` 喂 `WithBatcher(nil)`；文档承诺的 `stdout` 未实现。
- **C13c OTLP 默认 HTTPS 无 WithInsecure**（trace.go:113-120）：对 `localhost:4318` 等明文 collector 握手失败。
- **C13d Middleware 不更新 c.Request**（trace.go:172）：仅 `c.Set("otel_ctx", ctx)`，未 `c.Request = c.Request.WithContext(ctx)`，下游 `c.Request.Context()` 拿不到 span。
- **C13e b3/jaeger 未实现**（trace.go:128-138）：switch 仅 `w3c` + default，文档承诺的 `b3`/`jaeger` 静默回落 W3C。

**修复**：`getTracer()` 懒初始化默认 `otel.Tracer("xlgo")`；实现 stdout 导出器、`default` 返错；`Config` 增 `Insecure bool` 条件追加 `WithInsecure()`；Middleware 补 `c.Request = c.Request.WithContext(ctx)`；接入 contrib b3/jaeger propagator 或裁剪文档。

**验证**：未 Init 不 panic；未知导出器返错；下游 `c.Request.Context()` 含 span。

---

## HIGH（严重正确性 / 一致性 / 框架集成断裂）

### H1 `utils/random.go` 不安全 RNG 且文档反向推荐  `[CONFIRM]`

`randPool` 用 `math/rand` + `time.Now().UnixNano()` 播种（random.go:4,12），`RandString`（:31）/`RandDigit`（:54）取自该池，本文件无 `crypto/rand`。`[核验精化]` `GUIDE.md:1208` `token := utils.RandString(16)`、`:1211` `code := utils.RandDigit(6)` **主动推荐用于 token 与 6 位验证码**，`:1397/1412` 仅宣传 sync.Pool 性能无安全警告——比"应强文档警告"更严重（原报告措辞已改正为反向推荐）。

**修复**：新增 `RandStringSecure`（`crypto/rand`），文档/示例的 token/OTP/重置码改用之，并在 `RandString` 注释警示非安全用途。

**验证**：`RandStringSecure` 输出不可预测性（统计用例）；文档示例改用安全版本。

---

### H2 `utils/http.go` 默认关闭 TLS 校验  `[CONFIRM]`

`DefaultHTTPClientConfig.SkipTLSVerify:true`（http.go:53）→ `InsecureSkipVerify`（:67），`HTTPGet/HTTPPost/HTTPPostJSON` 经 `DefaultHTTPClient()` 全部默认可被 MITM。

**修复**：默认 `false`，仅显式开启。

**验证**：默认配置 `InsecureSkipVerify=false`；显式开启才放行自签证书。

---

### H3 `middleware/logger.go` 无上限读 body → OOM（默认关闭）  `[PARTIAL]`

`io.ReadAll(c.Request.Body)`（logger.go:60）无 `MaxBytesReader`，`MaxBodyLength` 仅在读完后截断日志副本（:64-65），全 body 已驻留并二次 buffer。`[核验精化]` **`DefaultLoggerConfig.LogRequestBody: false`（:32）默认不读 body**，需用户显式开启 `LogRequestBody: true` 才触发——bug 本身成立，但默认配置安全，严重性降档（两份原报告均未提及默认关闭）。

**修复**：源头 `io.LimitReader(body, limit+1)` 读取 + `io.MultiReader` 复原；或 `http.MaxBytesReader` 做硬上限（无论是否记日志）。

**验证**：开启 `LogRequestBody` 后超限 body 被截断、无 OOM。

---

### H4 `middleware/ratelimit.go` 限流语义错误  `[CONFIRM]`

- **H4a 稳态客户端被误限流**（ratelimit.go:48-74）：`Allow` 每次放行都 `v.lastSeen = time.Now()`（:55/63/72），重置分支 `time.Since(lastSeen) > window`（:61）对持续客户端永不成立。`[核验精化]` 算例（rate=10/min，客户端 9 req/min）：count 单调累加至 10，第 11 次起 BLOCK；**并非"永久 BLOCK"**——BLOCK 期间 lastSeen 停在第 10 次放行值，满 1 分钟窗口后 `time.Since` 超窗口会重置一次。真实缺陷是滑动窗口语义错误：持续低于 rate 的稳态客户端被过度限制且与"每分钟 N 次"承诺不符（原报告"之后约 1 req/window"措辞更准，"永久 BLOCK"过强）。
- **H4b CustomRateLimit 泄漏**（ratelimit.go:41-42,297-300,229-245）：`NewRateLimiter` spawn 清理 goroutine，`CustomRateLimit` 返回的 limiter 无句柄、`StopRateLimiters` 不感知 → 每个 `CustomRateLimit` 路由泄漏一 goroutine。
- **H4c fail-open + 无 comma-ok**（ratelimit.go:160,163）：Redis 错误 fail-open（含登录防爆破静默失效）；`result.(int64)` 无 comma-ok（当前脚本恒返整数不会 panic，属脆弱性）。

**修复**：H4a 改真正固定窗口——放行时**不**更新 `lastSeen`（仅窗口起点设置）；自定义 limiter 登记入全局以便 `StopRateLimiters` 停止；Redis 断言改 comma-ok，安全型限流考虑 fail-closed 或可配置。

**验证**：稳态客户端（9 req/min，rate=10/min）不被限流；超限客户端被拦；`StopRateLimiters` 停止所有 limiter（含 Custom）。

---

### H5 `response`/`handler` 业务码与 HTTP 状态混乱  `[CONFIRM]`

- **H5a**（response.go:52-79,33,43；mode.go:13-29,60-65）：`[核验精化]` 经源码确认 `response/mode.go` 模式系统真实存在——所有 `Fail*` 经 `writeResp`（:68）→ `httpStatusFor`（:60-65）受 `Mode` 控制；仅 `Success`（:33）/`SuccessWithMsg`（:43）等硬编 200。结论"ModeBusiness 默认全 200"成立，但原报告"全硬编 c.JSON(200)"不准确（漏看模式系统，已改正）。
- **H5b**（handler.go:157-170）：`handler.BadRequest`/`InternalError` 硬编 HTTP 400/500，绕过 Mode，且**不写 RequestID**（对比 `writeResp` 在 :73 写 `RequestID`），与 business 模式不一致、丢链路。

**修复**：两个 helper 委托 `response.FailWithCode`/`ServerError`（或 `response.Custom` 保留 RequestID）。

**验证**：`handler.BadRequest` 响应含 RequestID 且遵循 Mode；状态码与业务码映射一致。

---

### H6 `repository/repository.go` BaseRepo 数据安全 + 框架集成断裂  `[CONFIRM]`

- **H6a**（repository.go:50）：`Update` 用 `Save` 全列覆写，丢失更新、零值不可辨。
- **H6b**（repository.go:53-56,59-61）：`Delete` 注释称软删，但泛型 `T` 无 `gorm.DeletedAt` 时静默硬删，契约不可由类型强制；`HardDelete` 才显式 `Unscoped()`。
- **H6c（核心）**（repository.go:29-31,325,514）：`r.db` 构造时捕获，所有方法 `r.db.WithContext(ctx)` **从不调 `database.GetDBFromContext`** → 读写分离失效、外层 ctx 事务无法 join，`WithTransaction`（:325）另开嵌套 tx 拿不到外层。
- **H6d**（repository.go:156,167）：`FindPage` count/list 两查询无事务，高并发 total 与 items 不一致。
- **H6e**（repository.go:406,412）：`[核验精化]` 经源码确认 `Page` 的 count **已用 `qb.db.Session(&gorm.Session{}).Limit(-1).Offset(-1)` 克隆**（:406），count 未被污染（原报告"count 被破坏"证伪）；但 Find 侧（:412）在原 `qb.db` 上追加 `.Offset().Limit()` 会污染 `qb.db`（重复调用 Page 残留）——"Find 侧污染"成立。

**修复**：新增 `conn(ctx)` 优先取 `GetDBFromContext(ctx)`，全方法替换 `r.db.WithContext`；分页 count+list 包进单事务；终结方法一律基于 `Session{}` 克隆，文档标注 QueryBuilder 单次性/非并发安全；补 `UpdateFields`（`Updates` 局部更新）。

**验证**：`UseMaster(ctx)`/`UseReplica(ctx)` 经 BaseRepo 生效；外层事务可 join；`FindPage` total 与 list 一致（并发场景）。

---

### H7 `logger/logger.go` 全局指针写有锁读无锁  `[PARTIAL]`

`Init`/`Close` 持 `m.mu` 写包级 `Logger/sugar/apiLog/dbLog`（logger.go:121-131,193-200），但 `Info`/`Error`/`APILog()`（:235-247,280-282）无锁读 → 热重载 re-Init/关闭与请求日志竞争。`[核验精化]` 机制精确化：`m.mu` 是 `LogManager` **实例锁**，保护的却是**包级全局变量**（`Logger/sugar/...`）——"用实例锁保护全局变量"，锁与被保护对象作用域错配（原报告"锁却是 per-manager"措辞已精确化）。

**H7b**（field.go:24-31）：`Duration(key, value)` 在 `case zap.Field:` 直接 `return v` 丢弃 `key`，签名与实现矛盾。

**修复**：四个全局改 `atomic.Pointer[zap.Logger]`，读侧原子 load；`Duration` 改 `func(string, time.Duration) zap.Field`。

**验证**：`go test -race`——热重载 re-Init 与并发日志；`Duration` 保留 key。

---

### H8 路由/注册中心全局单例与调用顺序陷阱  `[CONFIRM]`

- **H8a**（router.go:233,247-268）：`globalRegistry` 无 nil 守卫、无锁，Init 前调 `Use`/`RegisterModule`/`Apply` nil-panic。
- **H8b**（router.go:210-229）：`Registry.Apply` 不幂等，二次 Apply 重复 `engine.Use` + Gin 重复路由 panic。
- **H8c**（metrics.go:25）：`RegisterMetricsRoute` 用 `r.Use`，Gin `engine.Use` 只对之后注册的路由生效 → 依赖调用顺序，先注册的路由不被采集。
- **H8d**（router.go:48-57,103-105；handler.go:21-25）：三个 `/health` 行为/schema 各异：`RegisterHealthRoute`（可 503）vs `defaultModule`（恒 200 `gin.H`）vs `handler.HealthCheck`（恒 200 经 `response.Success` 包成 `{code,msg,data}` schema 不同）；并存还会 Gin 重复路由 panic。

**修复**：`ensureRegistry()` 守卫 + 文档化"先 Init"；`Apply` 加 `applied` 幂等位；metrics 在 `Apply` 内作首个全局中间件；`/health` 收敛为单一实现（其余委托 `runHealthChecks`）。

**验证**：Init 前调用不 panic；二次 Apply 不重复注册；`/health` 单一实现且失败 503；metrics 覆盖所有路由。

---

## MEDIUM（确认项摘要）

> 以下均经核验 CONFIRM，仅个别措辞已就地修正。

- **M1**（random.go:76-78）：`RandInt` 反向入参静默 swap，掩盖调用 bug。
- **M2**（url.go:34）：`AddQueries` 实为 Set；零值 map 未初始化会 panic。
- **M3**（file.go:39-60）：无穿越校验（caller-aware）。
- **M4**（datetime.go:64-71）：`StartOfWeek` `Add(-24h)` 跨 DST 落错日；`ParseDateInt` 静默规范化。
- **M5**（validator.go:127 等）：身份证无校验位、IPv4 容忍前导零、Email 宽松、`rune(username[0])` 取字节。
- **M6**（response.go:94）：`Download` `Content-Disposition` 未 RFC-5987 编码，中文乱码。
- **M7**（error.go:187-189；response.go:68-75）：`[核验精化]` `FailWithError → writeResp` **会写 RequestID**（:73），原报告"丢 RequestID"证伪，**仅丢 `Detail`**（需用 `FailWithDetail`）。
- **M8**（mode.go:42-44）：`CodeDataAlreadyExists` 落 200 而 `CodeDataConflict`→409，语义相近映射不一致。
- **M9**（config.go:245-266）：DSN 不转义密码；`loc=Local`/`TimeZone=Asia/Shanghai` 硬编。
- **M10**（config.go:245-254）：拼错 driver 静默回退 MySQL，报错误导。
- **M11**（manager.go:577-607 等）：`HealthCheck` 同步 ping 无超时确认；`WriteQuery` 实为 `Find` 命名误导；`TransactionWithContext` 强制 master **不算 bug**（事务应走主库）。
- **M12**（cache.go:37-41,185-192）：`NewRedisCache` 构造时快照 client，Init 前构造永久 nil no-op。
- **M13**（keybuilder.go:148-187）：`globalKeyBuilder` 无 `sync.Once`、`SetPrefix` 无锁。
- **M14**（timeout.go:29）：软超时（设计取舍，非 bug，需文档强调 opt-in 性质）：`WithContext` 注入正确，但 handler 不查 ctx 则无效，无硬墙钟上限。
- **M15**（requestid.go:11-14）：信任客户端 `X-Request-ID` 无校验，头注入/日志伪造。
- **M16**（compress.go:104,107,146）：写侧 `defer Close` 吞错致归档损坏返回 nil 确认；`os.PathSeparator` 在 Windows 产 `\` 违反 zip 规范确认；`[核验精化]` **"Walk 闭包内 defer file.Close 累积 FD"证伪**——`defer file.Close()`（:146）在 per-file 的 WalkFunc 内，每文件返回即关闭，不累积。
- **M17**（console_windows.go）：`EnableVirtualTerminal` 死代码；着色 syscall.Stdout 与 `c.output` 分裂。
- **M18**（trace.go:187-189）：成功设 `codes.Ok` 应 UNSET；`Close` 无 double-close 守卫。
- **M19**（logger.go:88-92）：无法显式设日志级别。
- **M20**（templates.go:488-492,540-544）：`make handler my-thing` → `My-ThingHandler` 非法标识符确认；`fileExists` 权限错误误判为存在确认；`[核验精化]` 生成的 service 构造时调 `database.GetDB()` 存 nil（`BaseRepo{db:nil}`）**不即刻 panic**，延迟到首次查询才 nil deref。

---

## MINOR（全部确认）

- **N1** `BaseModelWithTime` 仅 `type:datetime` 之差（丢毫秒），命名误导。
- **N2** `BaseRepository` 接口为 struct 子集且无 `var _` 约束；`repository_test.go` 全空壳、CRUD 零覆盖。
- **N3** `SetupRouter` 返回裸 `gin.New()` 不含框架中间件；`MockStorage.Upload` 签名与真实 storage 不匹配。
- **N4** `Nl2br` 死分支（crypto.go:92-98）；`IsEmpty` 文档承诺 slice/map 实际不支持。
- **N5** `Upload` 循环内 `defer file.Close` 累积 FD（http.go:209-214）；`do` 无上限 `ReadAll`；`once` 字段声明未用。
- **N6** `KeepAlive` 发 `data: \n\n` 触发 onmessage，应为注释行 `: ping`。
- **N7** `CheckOrigin` 默认 `true`（CSWSH）；`MessageType` 枚举与 WS opcode 无关、装饰性。
- 另：多处测试未跑 `-race`，上述数据竞争无一被覆盖。

---

## 与 CLAUDE.md 文档不符之处

1. "Hot reload via `LoadWithWatch()`" —— 实际跳过 `Validate`、不重建 DB/Redis 池，对子系统无效（C10）。
2. "`database.CloseAll()` 关主+从" —— 正确，但孪生 `Close()` 泄漏从库，文档误导（C11）。
3. "`/health` 支持 HealthCheck、失败 503" —— `DefaultModule` 路径永远 200 不跑检查（H8）。
4. "`BaseRepo[T]` 提供 CRUD" —— 不参与读复制分离与 ctx 事务，与框架 DB 体系脱节（H6）。
5. "`ReplicaPicker` strategies" —— `RoundRobin` 非缺陷（C11e 已澄清）、`Random` 用全局 rand。
6. trace 文档承诺 `stdout`/`b3`/`jaeger` 均未实现（C13）。
7. "framework code never calls Fatal" —— **核实成立**（仅 facade 函数本身）。
8. logger FD 泄漏修复 + 跨写测试 —— **核实成立且有回归测试**，质量好。

---

## 修复优先级（投入产出 + 风险，执行依据）

**P0 安全/数据/功能性失效（必须先修）**：
C6（API CSRF 不可用）、C8（panic 返 200）、C4（路径穿越）、C5（Zip-Slip/炸弹）、C2（WS 死锁/关闭竞态）、C1（锁 panic/泄漏）、C7（CORS 绕过）、C3（SSE 断连泄漏）、H2（默认关 TLS）、H1（不安全 RNG 且被推荐用于 OTP）、C9b（刷新令牌撤销失败仍签发）、H4a（误限流）。

**P1 并发/正确性**：
C10/C11（全局 Manager 无锁置换 + 热重载校验 + 池泄漏）、C9a/c（黑名单静默/无锁）、H3（logger body OOM）、H7（logger 读写竞争）、C12（cron 竞争/重叠/解析）、C13（trace nil-panic 与导出器）、H5（response/handler 一致性 + RequestID）。

**P2 框架集成一致性**：
H6（BaseRepo 接入 GetDBFromContext + 分页事务）、H8（路由/health 统一 + Apply 幂等）、C3 收尾（生产者取消信号）、M14 timeout 文档化。

**P3 清理**：
MEDIUM/MINOR 各项 + 全量补 `-race`。

---

## 验证方式（通用）

- 并发缺陷逐项补 `go test -race`：cron `runTask` 并发、logger 热重载、WS 广播失败、cache lock 取消、config `SetDefaultManager` 并发、database `Master/Replicas` 并发。
- 安全项针对性用例：路径穿越（`../` 拒绝）、Zip-Slip、符号链接穿越、CORS 后缀绕过、Recover 真实 panic 验 HTTP 500、上传扩展名白名单、API CSRF 颁发→校验闭环、JWT 刷新撤销闭环。
- 端到端：`examples/full` 验 `/health` 503、SSE 断连即停、限流固定窗口语义、优雅关闭无 goroutine 泄漏（pprof 前后对比）。
- 每个修复缺陷配回归用例（修复前红、修复后绿）。
- 本机 `go build/test` 需加 `-buildvcs=false`（H:\worker 有 svn 干扰）。

---

## 核验元数据

- 独立核验日期：2026-06-24
- 核验方式：4 路并行 agent 回源码 `file:line` 再核（非读文档）
- 核验结论：13 CRITICAL + 8 HIGH 全部 CONFIRM/PARTIAL（缺陷真实存在），无 REFUTE、无 NOTFOUND、无虚报
- 精化项（4 处，均为措辞/严重性，不改变缺陷成立）：H3（默认关闭降档）、H4a（非永久 BLOCK）、H7（锁作用域错配）、C12e（步长分支补全）
- 本清单取代 `v_1.1.1_fix.md` 作为执行依据；`v_1.1.1_fix.md` 可保留作复核留痕
