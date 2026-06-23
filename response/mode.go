package response

import (
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

// Mode 响应模式
type Mode int32

const (
	// ModeBusiness 业务码模式（默认）：所有响应 HTTP 200，错误信息通过 body 中的 code 表达。
	// 兼容存量"业务码 in body"玩法。
	ModeBusiness Mode = iota
	// ModeREST REST 模式：失败响应按业务码映射对应的 HTTP status（401/404/500...），
	// body 仍带业务码。便于 APM / Prometheus / 网关 / Sentry 按 status 区分异常。
	ModeREST
)

// currentMode 当前响应模式，默认 ModeBusiness。原子读写，可在运行时切换。
var currentMode atomic.Int32

// SetMode 设置全局响应模式。
func SetMode(m Mode) { currentMode.Store(int32(m)) }

// GetMode 返回当前响应模式。
func GetMode() Mode { return Mode(currentMode.Load()) }

// statusForCode 按业务码推断 HTTP status（用于 ModeREST）。
// 已知框架错误码显式映射；用户/文件/数据模块的业务错误码（1xxxx~3xxxx）
// 属业务语义而非 HTTP 错误，保持 200；操作失败类（4xxxx）映射 400。
func statusForCode(code int) int {
	switch code {
	case CodeSuccess:
		return http.StatusOK
	case CodeUnauthorized, CodeTokenExpired, CodeTokenInvalid:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound, CodeUserNotFound, CodeFileNotFound, CodeDataNotFound:
		return http.StatusNotFound
	case CodeDataConflict:
		return http.StatusConflict
	case CodeRateLimit:
		return http.StatusTooManyRequests
	case CodeServerError:
		return http.StatusInternalServerError
	case CodeServiceUnavailable:
		return http.StatusServiceUnavailable
	}
	if code >= 40000 && code < 50000 {
		return http.StatusBadRequest
	}
	return http.StatusOK
}

// httpStatusFor 返回当前模式下失败响应对应的 HTTP status。
func httpStatusFor(code int) int {
	if GetMode() == ModeREST {
		return statusForCode(code)
	}
	return http.StatusOK
}

// writeResp 统一写入响应，按当前 Mode 决定 HTTP status。
func writeResp(c *gin.Context, code int, msg string, data any) {
	c.JSON(httpStatusFor(code), Response{
		Code:      code,
		Msg:       msg,
		Data:      data,
		RequestID: getRequestID(c),
	})
}

// Custom 显式指定 HTTP status 与业务码的响应，不受 Mode 影响。
// 适用于需要精确控制 HTTP status 的场景（如 REST 模式下的特殊端点）。
func Custom(c *gin.Context, httpStatus, code int, msg string, data any) {
	c.JSON(httpStatus, Response{
		Code:      code,
		Msg:       msg,
		Data:      data,
		RequestID: getRequestID(c),
	})
}
