package response

import (
	"net/http"

	"github.com/EthanCodeCraft/xlgo-core/utils"
	"github.com/gin-gonic/gin"
)

// Response 统一响应结构
type Response struct {
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	Data      any    `json:"data,omitempty"`
	RequestID string `json:"request_id,omitempty"` // 请求追踪ID
}

// PageData 分页数据结构
type PageData struct {
	Items    any   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// getRequestID 从上下文获取请求ID
func getRequestID(c *gin.Context) string {
	return c.GetString("request_id")
}

// Success 成功响应
func Success(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{
		Code:      CodeSuccess,
		Msg:       "操作成功",
		Data:      data,
		RequestID: getRequestID(c),
	})
}

// SuccessWithMsg 成功响应（自定义消息）
func SuccessWithMsg(c *gin.Context, msg string, data any) {
	c.JSON(http.StatusOK, Response{
		Code:      CodeSuccess,
		Msg:       msg,
		Data:      data,
		RequestID: getRequestID(c),
	})
}

// Fail 失败响应
func Fail(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, Response{
		Code:      CodeFail,
		Msg:       msg,
		RequestID: getRequestID(c),
	})
}

// FailWithCode 失败响应（自定义错误码）
func FailWithCode(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, Response{
		Code:      code,
		Msg:       msg,
		RequestID: getRequestID(c),
	})
}

// Unauthorized 未授权响应
func Unauthorized(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, Response{
		Code:      CodeUnauthorized,
		Msg:       msg,
		RequestID: getRequestID(c),
	})
}

// NotFound 资源不存在响应
func NotFound(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, Response{
		Code:      CodeNotFound,
		Msg:       msg,
		RequestID: getRequestID(c),
	})
}

// ServerError 服务器错误响应
func ServerError(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, Response{
		Code:      CodeServerError,
		Msg:       msg,
		RequestID: getRequestID(c),
	})
}

// RateLimit 请求过于频繁响应
func RateLimit(c *gin.Context) {
	c.JSON(http.StatusOK, Response{
		Code:      CodeRateLimit,
		Msg:       "请求过于频繁，请稍后再试",
		RequestID: getRequestID(c),
	})
}

// Page 分页响应
func Page(c *gin.Context, items any, total int64, page, pageSize int) {
	Success(c, PageData{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// Download 文件下载响应
// 评分: ⭐⭐⭐⭐⭐
// 理由: 文件下载封装，自动设置响应头
func Download(c *gin.Context, filename string, data []byte) {
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Header("Content-Length", utils.ToString(len(data)))
	c.Data(http.StatusOK, "application/octet-stream", data)
}

// DownloadWithContentType 文件下载（自定义Content-Type）
func DownloadWithContentType(c *gin.Context, filename string, contentType string, data []byte) {
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Header("Content-Length", utils.ToString(len(data)))
	c.Data(http.StatusOK, contentType, data)
}

// HTML HTML内容响应
func HTML(c *gin.Context, data string) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, data)
}

// Redirect 页面跳转
func Redirect(c *gin.Context, code int, url string) {
	c.Redirect(code, url)
}
