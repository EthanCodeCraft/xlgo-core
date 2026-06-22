# xlgo 示例

两个可运行示例，帮助快速上手 xlgo。

## minimal — 最小 HTTP 服务

不依赖 MySQL / Redis / Storage，纯 HTTP + 健康检查。第一次接触 xlgo 从这里开始。

```bash
go run ./examples/minimal
```

访问 http://localhost:8081/health（健康检查）与 http://localhost:8081/api/v1/（示例路由）。

## full — 完整业务 API

包含 MySQL + Redis + JWT + 一个 user CRUD（登录发 token、认证路由、创建/查询用户）。

**运行前需准备**：
- MySQL（修改 `examples/full/config.yaml` 的 `database` 配置）
- Redis（修改 `examples/full/config.yaml` 的 `redis` 配置）

```bash
go run ./examples/full
```

启动后自动迁移 user 表。接口：

| 方法 | 路径 | 说明 | 认证 |
|---|---|---|---|
| POST | /api/v1/login | 登录，返回 token | 否 |
| GET  | /api/v1/users/:id | 查询用户 | 是（Bearer token） |
| POST | /api/v1/users | 创建用户 | 是（Bearer token） |

登录示例：
```bash
curl -X POST http://localhost:8082/api/v1/login \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"secret"}'
```

> 示例代码为演示用途，密码明文存储、未做参数校验，生产环境请使用 bcrypt 哈希密码并配合 `validation` 包校验入参。
