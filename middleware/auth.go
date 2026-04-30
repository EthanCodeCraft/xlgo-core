package middleware

import (
	"strings"

	"github.com/EthanCodeCraft/xlgo-core/jwt"
	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/gin-gonic/gin"
)

const (
	// ContextKeyUserID 用户ID上下文键
	ContextKeyUserID = "user_id"
	// ContextKeyUsername 用户名上下文键
	ContextKeyUsername = "username"
	// ContextKeyRole 角色上下文键
	ContextKeyRole = "role"
	// ContextKeyUserType 用户类型上下文键
	ContextKeyUserType = "user_type"
)

// AuthRequired JWT 认证中间件
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			response.Unauthorized(c, "请先登录")
			c.Abort()
			return
		}

		// 解析 Bearer Token
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			response.Unauthorized(c, "认证格式错误")
			c.Abort()
			return
		}

		tokenString := parts[1]
		claims, err := jwt.ParseToken(tokenString)
		if err != nil {
			response.Unauthorized(c, "登录已过期，请重新登录")
			c.Abort()
			return
		}

		// 将用户信息存入上下文
		c.Set(ContextKeyUserID, claims.UserID)
		c.Set(ContextKeyUsername, claims.Username)
		c.Set(ContextKeyRole, claims.Role)
		c.Set(ContextKeyUserType, claims.UserType)

		c.Next()
	}
}

// AdminRequired 管理员权限中间件
func AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		userType, exists := c.Get(ContextKeyUserType)
		if !exists {
			response.Unauthorized(c, "请先登录")
			c.Abort()
			return
		}

		// 安全的类型断言
		ut, ok := userType.(string)
		if !ok {
			response.Unauthorized(c, "用户信息异常")
			c.Abort()
			return
		}

		if ut != "super_admin" && ut != "admin" {
			response.Fail(c, "无权限访问")
			c.Abort()
			return
		}

		c.Next()
	}
}

// SuperAdminRequired 超级管理员权限中间件
func SuperAdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		userType, exists := c.Get(ContextKeyUserType)
		if !exists {
			response.Unauthorized(c, "请先登录")
			c.Abort()
			return
		}

		// 安全的类型断言
		ut, ok := userType.(string)
		if !ok {
			response.Unauthorized(c, "用户信息异常")
			c.Abort()
			return
		}

		if ut != "super_admin" {
			response.Fail(c, "需要超级管理员权限")
			c.Abort()
			return
		}

		c.Next()
	}
}

// StaffRequired 员工权限中间件
func StaffRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		userType, exists := c.Get(ContextKeyUserType)
		if !exists {
			response.Unauthorized(c, "请先登录")
			c.Abort()
			return
		}

		// 安全的类型断言
		ut, ok := userType.(string)
		if !ok {
			response.Unauthorized(c, "用户信息异常")
			c.Abort()
			return
		}

		if ut != "staff" {
			response.Fail(c, "无权限访问")
			c.Abort()
			return
		}

		c.Next()
	}
}

// AnyUserRequired 任意用户权限中间件（允许 admin/super_admin/staff）
func AnyUserRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		userType, exists := c.Get(ContextKeyUserType)
		if !exists {
			response.Unauthorized(c, "请先登录")
			c.Abort()
			return
		}

		// 安全的类型断言
		ut, ok := userType.(string)
		if !ok {
			response.Unauthorized(c, "用户信息异常")
			c.Abort()
			return
		}

		// 允许所有内部用户类型
		if ut != "super_admin" && ut != "admin" && ut != "staff" {
			response.Fail(c, "无权限访问")
			c.Abort()
			return
		}

		c.Next()
	}
}

// GetUserID 从上下文获取用户ID
func GetUserID(c *gin.Context) uint {
	userID, exists := c.Get(ContextKeyUserID)
	if !exists {
		return 0
	}
	// 安全的类型断言
	id, ok := userID.(uint)
	if !ok {
		return 0
	}
	return id
}

// GetUsername 从上下文获取用户名
func GetUsername(c *gin.Context) string {
	username, exists := c.Get(ContextKeyUsername)
	if !exists {
		return ""
	}
	// 安全的类型断言
	name, ok := username.(string)
	if !ok {
		return ""
	}
	return name
}

// GetUserType 从上下文获取用户类型
func GetUserType(c *gin.Context) string {
	userType, exists := c.Get(ContextKeyUserType)
	if !exists {
		return ""
	}
	// 安全的类型断言
	ut, ok := userType.(string)
	if !ok {
		return ""
	}
	return ut
}
