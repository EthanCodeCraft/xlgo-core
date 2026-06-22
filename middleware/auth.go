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

	// DefaultUserTypeSuperAdmin 默认超级管理员用户类型
	DefaultUserTypeSuperAdmin = "super_admin"
	// DefaultUserTypeAdmin 默认管理员用户类型
	DefaultUserTypeAdmin = "admin"
	// DefaultUserTypeStaff 默认员工用户类型
	DefaultUserTypeStaff = "staff"
)

// AuthUser 当前认证用户
type AuthUser struct {
	UserID   uint
	Username string
	Role     string
	UserType string
}

// AuthChecker 自定义权限检查函数
type AuthChecker func(user AuthUser, c *gin.Context) bool

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

// RequireAuth 自定义权限中间件
func RequireAuth(checker AuthChecker, messages ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := GetAuthUser(c)
		if !ok {
			abortAuthContext(c)
			return
		}

		if checker == nil || !checker(user, c) {
			abortForbidden(c, messages...)
			return
		}

		c.Next()
	}
}

// RequireUserTypes 用户类型权限中间件
func RequireUserTypes(userTypes ...string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(userTypes))
	for _, userType := range userTypes {
		allowed[userType] = struct{}{}
	}

	return RequireAuth(func(user AuthUser, c *gin.Context) bool {
		_, ok := allowed[user.UserType]
		return ok
	})
}

// RequireRoles 角色权限中间件
func RequireRoles(roles ...string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}

	return RequireAuth(func(user AuthUser, c *gin.Context) bool {
		_, ok := allowed[user.Role]
		return ok
	})
}

// AdminRequired 管理员权限中间件
func AdminRequired() gin.HandlerFunc {
	return RequireUserTypes(DefaultUserTypeSuperAdmin, DefaultUserTypeAdmin)
}

// SuperAdminRequired 超级管理员权限中间件
func SuperAdminRequired() gin.HandlerFunc {
	return RequireUserTypes(DefaultUserTypeSuperAdmin)
}

// StaffRequired 员工权限中间件
func StaffRequired() gin.HandlerFunc {
	return RequireUserTypes(DefaultUserTypeStaff)
}

// AnyUserRequired 任意用户权限中间件（默认允许 admin/super_admin/staff）
func AnyUserRequired() gin.HandlerFunc {
	return RequireUserTypes(DefaultUserTypeSuperAdmin, DefaultUserTypeAdmin, DefaultUserTypeStaff)
}

func abortAuthContext(c *gin.Context) {
	if _, exists := c.Get(ContextKeyUserID); !exists {
		response.Unauthorized(c, "请先登录")
	} else {
		response.Unauthorized(c, "用户信息异常")
	}
	c.Abort()
}

func abortForbidden(c *gin.Context, messages ...string) {
	if len(messages) > 0 && messages[0] != "" {
		response.Fail(c, messages[0])
	} else {
		response.FailWithError(c, response.ErrForbidden)
	}
	c.Abort()
}

// GetAuthUser 从上下文获取认证用户
func GetAuthUser(c *gin.Context) (AuthUser, bool) {
	userID, exists := c.Get(ContextKeyUserID)
	if !exists {
		return AuthUser{}, false
	}
	id, ok := userID.(uint)
	if !ok {
		return AuthUser{}, false
	}

	username, exists := c.Get(ContextKeyUsername)
	if !exists {
		return AuthUser{}, false
	}
	name, ok := username.(string)
	if !ok {
		return AuthUser{}, false
	}

	role, exists := c.Get(ContextKeyRole)
	if !exists {
		return AuthUser{}, false
	}
	r, ok := role.(string)
	if !ok {
		return AuthUser{}, false
	}

	userType, exists := c.Get(ContextKeyUserType)
	if !exists {
		return AuthUser{}, false
	}
	ut, ok := userType.(string)
	if !ok {
		return AuthUser{}, false
	}

	return AuthUser{
		UserID:   id,
		Username: name,
		Role:     r,
		UserType: ut,
	}, true
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

// GetRole 从上下文获取角色
func GetRole(c *gin.Context) string {
	role, exists := c.Get(ContextKeyRole)
	if !exists {
		return ""
	}
	// 安全的类型断言
	r, ok := role.(string)
	if !ok {
		return ""
	}
	return r
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
