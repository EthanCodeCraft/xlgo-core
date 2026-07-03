package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/EthanCodeCraft/xlgo-core/utils"
	"github.com/gin-gonic/gin"
)

// HealthCheck 健康检查
// @Summary 健康检查
// @Description 检查服务是否正常运行
// @Tags 系统
// @Accept json
// @Produce json
// @Success 200 {object} map[string]string
// @Router /health [get]
//
// 响应体与 router.RegisterHealthRoute 收敛为同一 schema（H8d）：
// 恒 200 + {"status":"ok"}，不走 response 业务信封，便于 K8s 探针直读。
// 需要依赖探活（mysql/redis…失败 503）时改用 router.RegisterHealthRoute 传入 checks。
func HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// BindJSON 绑定 JSON 请求
func BindJSON(c *gin.Context, req any) error {
	return c.ShouldBindJSON(req)
}

// BindQuery 绑定 Query 参数
func BindQuery(c *gin.Context, req any) error {
	return c.ShouldBindQuery(req)
}

// MaxPage GetPage 允许的最大页码（M-D 修复：防深分页 DoS）。
// 超大 page 产生超大 OFFSET，大表上为性能灾难/DoS 面。钳制到该上限；
// 需更深度遍历的业务应改用游标/keyset 分页（框架不内置，避免功能扩张）。
const MaxPage = 10000

// GetPage 获取分页参数
func GetPage(c *gin.Context) (int, int) {
	page := c.DefaultQuery("page", "1")
	pageSize := c.DefaultQuery("page_size", "20")

	p := utils.ToIntDefault(page, 1)
	ps := utils.ToIntDefault(pageSize, 20)

	if p < 1 {
		p = 1
	}
	// M-D 修复：钳制 page 上限，防 ?page=999999999 产生超大 OFFSET 拖垮 DB。
	if p > MaxPage {
		p = MaxPage
	}
	if ps < 1 {
		ps = 20
	}
	if ps > 100 {
		ps = 100
	}

	return p, ps
}

// GetIDFromPath 从路径获取 ID
func GetIDFromPath(c *gin.Context, paramName string) (uint, bool) {
	idStr := c.Param(paramName)
	idStr = strings.Trim(idStr, "/")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		return 0, false
	}
	return uint(id), true
}

// ===== 类型安全的参数获取函数 =====

// QueryInt 获取 Query 参数中的整数（带默认值）
func QueryInt(c *gin.Context, key string, def int) int {
	return utils.ToIntDefault(c.Query(key), def)
}

// QueryInt64 获取 Query 参数中的 int64（带默认值）
func QueryInt64(c *gin.Context, key string, def int64) int64 {
	return utils.ToInt64Default(c.Query(key), def)
}

// QueryFloat64 获取 Query 参数中的 float64（带默认值）
func QueryFloat64(c *gin.Context, key string, def float64) float64 {
	return utils.ToFloat64Default(c.Query(key), def)
}

// QueryBool 获取 Query 参数中的布尔值（带默认值）
func QueryBool(c *gin.Context, key string, def bool) bool {
	val := c.Query(key)
	if val == "" {
		return def
	}
	return val == "true" || val == "1" || val == "yes"
}

// PathInt 从路径参数获取整数（带默认值）
func PathInt(c *gin.Context, key string, def int) int {
	val := strings.Trim(c.Param(key), "/")
	return utils.ToIntDefault(val, def)
}

// PathInt64 从路径参数获取 int64（带默认值）
func PathInt64(c *gin.Context, key string, def int64) int64 {
	val := strings.Trim(c.Param(key), "/")
	return utils.ToInt64Default(val, def)
}

// PathUint64 从路径参数获取 uint64（带默认值）
func PathUint64(c *gin.Context, key string, def uint64) uint64 {
	val := strings.Trim(c.Param(key), "/")
	return utils.ToUint64Default(val, def)
}

// FormInt 获取 POST 表单参数中的整数（带默认值）
func FormInt(c *gin.Context, key string, def int) int {
	return utils.ToIntDefault(c.PostForm(key), def)
}

// FormInt64 获取 POST 表单参数中的 int64（带默认值）
func FormInt64(c *gin.Context, key string, def int64) int64 {
	return utils.ToInt64Default(c.PostForm(key), def)
}

// FormUint64 获取 POST 表单参数中的 uint64（带默认值）
func FormUint64(c *gin.Context, key string, def uint64) uint64 {
	return utils.ToUint64Default(c.PostForm(key), def)
}

// FormFloat64 获取 POST 表单参数中的 float64（带默认值）
func FormFloat64(c *gin.Context, key string, def float64) float64 {
	return utils.ToFloat64Default(c.PostForm(key), def)
}

// FormBool 获取 POST 表单参数中的布尔值（带默认值）
func FormBool(c *gin.Context, key string, def bool) bool {
	val := c.PostForm(key)
	if val == "" {
		return def
	}
	return val == "true" || val == "1" || val == "yes" || val == "on"
}

// FormString 获取 POST 表单参数中的字符串（带默认值）
func FormString(c *gin.Context, key string, def string) string {
	val := c.PostForm(key)
	if val == "" {
		return def
	}
	return val
}

// ParseInt 解析整数
func ParseInt(s string, defaultValue int) int {
	return utils.ToIntDefault(s, defaultValue)
}

// BadRequest 返回参数/请求错误响应。
//
// 委托 response.FailWithCode（CodeFail），遵循当前响应模式（ModeBusiness 下 HTTP 200，
// 错误信息通过 body 中的 code 表达；ModeREST 下 CodeFail 属业务失败不映射 HTTP 错误，仍 200），
// 并写入 RequestID——与 response 模式系统一致，不再硬编 HTTP 状态码、不再丢失链路追踪。
func BadRequest(c *gin.Context, msg string) {
	response.FailWithCode(c, response.CodeFail, msg)
}

// InternalError 返回服务器错误响应。
//
// 委托 response.ServerError（CodeServerError），遵循当前响应模式（ModeBusiness 下 HTTP 200，
// ModeREST 下 HTTP 500），并写入 RequestID——与 response 模式系统一致，
// 不再硬编 HTTP 状态码、不再丢失链路追踪。
func InternalError(c *gin.Context, msg string) {
	response.ServerError(c, msg)
}
