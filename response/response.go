package response

import (
	"bytes"
	"io"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
)

// Response 统一响应结构
//
// M-38/M-39 修复（breaking）：Data/RequestID 去掉 omitempty，让 data 与 request_id
// 字段在所有响应中恒存在（失败时 data=null、未装 RequestID 中间件时 request_id=""）。
// 原实现用 omitempty 导致失败响应无 data 字段、空切片被 omit、未装中间件时无 request_id，
// 破坏"统一响应结构"契约，下游严格按 schema 解析会出错。
type Response struct {
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	Data      any    `json:"data"`
	RequestID string `json:"request_id"` // 请求追踪ID
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
	writeResp(c, CodeFail, msg, nil)
}

// FailWithCode 失败响应（自定义错误码）
func FailWithCode(c *gin.Context, code int, msg string) {
	writeResp(c, code, msg, nil)
}

// Unauthorized 未授权响应
func Unauthorized(c *gin.Context, msg string) {
	writeResp(c, CodeUnauthorized, msg, nil)
}

// NotFound 资源不存在响应
func NotFound(c *gin.Context, msg string) {
	writeResp(c, CodeNotFound, msg, nil)
}

// ServerError 服务器错误响应
func ServerError(c *gin.Context, msg string) {
	writeResp(c, CodeServerError, msg, nil)
}

// RateLimit 请求过于频繁响应
func RateLimit(c *gin.Context) {
	writeResp(c, CodeRateLimit, "请求过于频繁，请稍后再试", nil)
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

// contentDisposition 生成 RFC 5987 兼容的 Content-Disposition 值（M6 修复）。
// 同时给出 ASCII filename（向后兼容旧客户端）与 filename*（UTF-8 百分号编码，支持中文等非 ASCII），
// 避免直接拼接导致中文文件名乱码。
func contentDisposition(filename string) string {
	ascii := asciiFallbackName(filename)
	enc := url.PathEscape(filename)
	// PathEscape 把空格编码为 %20（符合 RFC 5987），无需额外处理。
	return "attachment; filename=\"" + ascii + "\"; filename*=UTF-8''" + enc
}

// asciiFallbackName 生成仅含 ASCII 的回退文件名：非 ASCII 字符替换为下划线，
// 空文件名回退为 "download"。
func asciiFallbackName(filename string) string {
	if filename == "" {
		return "download"
	}
	b := make([]byte, 0, len(filename))
	for i := 0; i < len(filename); i++ {
		c := filename[i]
		if c >= 0x20 && c < 0x7f && c != '"' && c != '\\' {
			b = append(b, c)
		} else {
			b = append(b, '_')
		}
	}
	if len(b) == 0 {
		return "download"
	}
	return string(b)
}

// Download 文件下载响应
func Download(c *gin.Context, filename string, data []byte) {
	DownloadReader(c, filename, "application/octet-stream", int64(len(data)), bytesReader(data))
}

// DownloadWithContentType 文件下载（自定义Content-Type）
func DownloadWithContentType(c *gin.Context, filename string, contentType string, data []byte) {
	DownloadReader(c, filename, contentType, int64(len(data)), bytesReader(data))
}

// DownloadReader 流式下载响应。
//
// 对大文件/对象存储下载优先使用此函数，避免先把完整内容读入 []byte 驻留内存。
// contentLength 传 -1 表示未知长度；Gin 会省略 Content-Length。
func DownloadReader(c *gin.Context, filename, contentType string, contentLength int64, r io.Reader) {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	headers := map[string]string{
		"Content-Disposition": contentDisposition(filename),
	}
	c.DataFromReader(http.StatusOK, contentLength, contentType, r, headers)
}

// bytesReader 避免在公开 API 中暴露 bytes 包细节，同时让旧 []byte API 复用流式写路径。
func bytesReader(data []byte) io.Reader {
	return bytes.NewReader(data)
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
