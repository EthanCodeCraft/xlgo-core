package response

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

// 业务错误码定义
// 格式：模块(2位) + 功能(2位) + 错误类型(2位)
const (
	// 通用错误 00xxxx
	CodeSuccess            = 1   // 成功
	CodeFail               = 0   // 通用失败
	CodeInvalidParams      = 1   // 参数错误
	CodeUnauthorized       = 401 // 未授权
	CodeForbidden          = 403 // 无权限
	CodeNotFound           = 404 // 资源不存在
	CodeRateLimit          = 429 // 请求过于频繁
	CodeServerError        = 500 // 服务器错误
	CodeServiceUnavailable = 503 // 服务不可用

	// 用户模块错误 01xxxx
	CodeUserNotFound      = 10001 // 用户不存在
	CodeUserAlreadyExists = 10002 // 用户已存在
	CodeUserDisabled      = 10003 // 用户已禁用
	CodePasswordWrong     = 10004 // 密码错误
	CodePasswordWeak      = 10005 // 密码强度不足
	CodePhoneInvalid      = 10006 // 手机号无效
	CodeEmailInvalid      = 10007 // 邮箱无效
	CodeLoginFailed       = 10008 // 登录失败
	CodeTokenExpired      = 10009 // Token 已过期
	CodeTokenInvalid      = 10010 // Token 无效

	// 文件模块错误 02xxxx
	CodeFileNotFound     = 20001 // 文件不存在
	CodeFileTooLarge     = 20002 // 文件过大
	CodeFileTypeInvalid  = 20003 // 文件类型不支持
	CodeFileUploadFailed = 20004 // 文件上传失败

	// 数据模块错误 03xxxx
	CodeDataNotFound      = 30001 // 数据不存在
	CodeDataAlreadyExists = 30002 // 数据已存在
	CodeDataInvalid       = 30003 // 数据无效
	CodeDataConflict      = 30004 // 数据冲突

	// 业务模块错误 04xxxx
	CodeOperationFailed   = 40001 // 操作失败
	CodeOperationTimeout  = 40002 // 操作超时
	CodeBusinessRuleError = 40003 // 业务规则错误
)

// Error 业务错误
type Error struct {
	Code    int    // 错误码
	Message string // 错误消息
	Detail  string // 详细信息（可选）
}

// NewError 创建业务错误
func NewError(code int, message string) *Error {
	return &Error{
		Code:    code,
		Message: message,
	}
}

// NewErrorWithDetail 创建带详细信息的业务错误
func NewErrorWithDetail(code int, message, detail string) *Error {
	return &Error{
		Code:    code,
		Message: message,
		Detail:  detail,
	}
}

// Error 实现 error 接口
func (e *Error) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("[%d] %s: %s", e.Code, e.Message, e.Detail)
	}
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

// WithDetail 添加详细信息
func (e *Error) WithDetail(detail string) *Error {
	e.Detail = detail
	return e
}

// ToResponse 转换为响应结构
func (e *Error) ToResponse() Response {
	return Response{
		Code: e.Code,
		Msg:  e.Message,
	}
}

// 预定义错误
var (
	ErrInvalidParams      = NewError(CodeInvalidParams, "参数错误")
	ErrUnauthorized       = NewError(CodeUnauthorized, "请先登录")
	ErrForbidden          = NewError(CodeForbidden, "无权限访问")
	ErrNotFound           = NewError(CodeNotFound, "资源不存在")
	ErrRateLimit          = NewError(CodeRateLimit, "请求过于频繁")
	ErrServerError        = NewError(CodeServerError, "服务器错误")
	ErrServiceUnavailable = NewError(CodeServiceUnavailable, "服务暂时不可用")

	// 用户相关
	ErrUserNotFound      = NewError(CodeUserNotFound, "用户不存在")
	ErrUserAlreadyExists = NewError(CodeUserAlreadyExists, "用户已存在")
	ErrUserDisabled      = NewError(CodeUserDisabled, "用户已禁用")
	ErrPasswordWrong     = NewError(CodePasswordWrong, "密码错误")
	ErrPasswordWeak      = NewError(CodePasswordWeak, "密码强度不足")
	ErrPhoneInvalid      = NewError(CodePhoneInvalid, "手机号无效")
	ErrEmailInvalid      = NewError(CodeEmailInvalid, "邮箱无效")
	ErrLoginFailed       = NewError(CodeLoginFailed, "登录失败")
	ErrTokenExpired      = NewError(CodeTokenExpired, "登录已过期")
	ErrTokenInvalid      = NewError(CodeTokenInvalid, "Token 无效")

	// 文件相关
	ErrFileNotFound     = NewError(CodeFileNotFound, "文件不存在")
	ErrFileTooLarge     = NewError(CodeFileTooLarge, "文件过大")
	ErrFileTypeInvalid  = NewError(CodeFileTypeInvalid, "文件类型不支持")
	ErrFileUploadFailed = NewError(CodeFileUploadFailed, "文件上传失败")

	// 数据相关
	ErrDataNotFound      = NewError(CodeDataNotFound, "数据不存在")
	ErrDataAlreadyExists = NewError(CodeDataAlreadyExists, "数据已存在")
	ErrDataInvalid       = NewError(CodeDataInvalid, "数据无效")
	ErrDataConflict      = NewError(CodeDataConflict, "数据冲突")

	// 业务相关
	ErrOperationFailed  = NewError(CodeOperationFailed, "操作失败")
	ErrOperationTimeout = NewError(CodeOperationTimeout, "操作超时")
	ErrBusinessRule     = NewError(CodeBusinessRuleError, "业务规则错误")
)

// FailWithError 使用预定义错误响应
func FailWithError(c *gin.Context, err *Error) {
	c.JSON(200, Response{
		Code:      err.Code,
		Msg:       err.Message,
		RequestID: getRequestID(c),
	})
}

// FailWithDetail 使用预定义错误并添加详细信息
func FailWithDetail(c *gin.Context, err *Error, detail string) {
	c.JSON(200, Response{
		Code:      err.Code,
		Msg:       err.Message,
		Data:      gin.H{"detail": detail},
		RequestID: getRequestID(c),
	})
}
