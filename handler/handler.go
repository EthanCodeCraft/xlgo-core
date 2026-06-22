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
// @Success 200 {object} response.Response
// @Router /health [get]
func HealthCheck(c *gin.Context) {
	response.Success(c, gin.H{
		"status": "ok",
	})
}

// BindJSON 绑定 JSON 请求
func BindJSON(c *gin.Context, req any) error {
	return c.ShouldBindJSON(req)
}

// BindQuery 绑定 Query 参数
func BindQuery(c *gin.Context, req any) error {
	return c.ShouldBindQuery(req)
}

// GetPage 获取分页参数
func GetPage(c *gin.Context) (int, int) {
	page := c.DefaultQuery("page", "1")
	pageSize := c.DefaultQuery("page_size", "20")

	p := utils.ToIntDefault(page, 1)
	ps := utils.ToIntDefault(pageSize, 20)

	if p < 1 {
		p = 1
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

// BadRequest 返回 400 错误
func BadRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, response.Response{
		Code: response.CodeFail,
		Msg:  msg,
	})
}

// InternalError 返回 500 错误
func InternalError(c *gin.Context, msg string) {
	c.JSON(http.StatusInternalServerError, response.Response{
		Code: response.CodeServerError,
		Msg:  msg,
	})
}
