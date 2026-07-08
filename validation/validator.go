package validation

import (
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

// Validator 全局验证器实例。
//
// H-13 修复：改用 atomic.Pointer 保护读写，消除原裸指针（InitValidator 无锁写、
// ValidateStruct 无锁读）在运行期热重载/并发校验时的数据竞争。类型由 *validator.Validate
// 变更为 atomic.Pointer[validator.Validate]（breaking：下游若直接读 validation.Validator.Struct
// 需改用 ValidateStruct，或 validation.Validator.Load().Struct）。
var Validator atomic.Pointer[validator.Validate]

// ValidationError 验证错误
type ValidationError struct {
	Field   string `json:"field"`   // 字段名（使用 label 或 json tag）
	Label   string `json:"label"`   // 字段中文名（用于显示）
	Message string `json:"message"` // 错误消息
}

// ValidationErrors 验证错误列表
type ValidationErrors []ValidationError

// Error 实现 error 接口
func (ve ValidationErrors) Error() string {
	var msgs []string
	for _, e := range ve {
		if e.Label != "" {
			msgs = append(msgs, e.Label+": "+e.Message)
		} else {
			msgs = append(msgs, e.Field+": "+e.Message)
		}
	}
	return strings.Join(msgs, "; ")
}

// ToMap 转换为 map
func (ve ValidationErrors) ToMap() map[string]string {
	m := make(map[string]string)
	for _, e := range ve {
		m[e.Field] = e.Message
	}
	return m
}

// ToLabelMap 转换为带标签的 map
func (ve ValidationErrors) ToLabelMap() map[string]string {
	m := make(map[string]string)
	for _, e := range ve {
		if e.Label != "" {
			m[e.Label] = e.Message
		} else {
			m[e.Field] = e.Message
		}
	}
	return m
}

// First 获取第一个错误
func (ve ValidationErrors) First() *ValidationError {
	if len(ve) == 0 {
		return nil
	}
	return &ve[0]
}

// FirstMessage 获取第一个错误消息
func (ve ValidationErrors) FirstMessage() string {
	if len(ve) == 0 {
		return ""
	}
	return ve[0].Message
}

// InitValidator 初始化验证器
func InitValidator() {
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		// 注册自定义标签名函数（优先使用 label，其次 json）
		v.RegisterTagNameFunc(func(fld reflect.StructField) string {
			// 优先使用 label tag 作为字段显示名
			label := fld.Tag.Get("label")
			if label != "" {
				return label
			}

			// 其次使用 json tag
			name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
			if name == "-" {
				return ""
			}
			return name
		})

		// 注册自定义验证规则（先注册再 Store，避免并发 ValidateStruct 拿到
		// "已 Store 但自定义规则未注册完"的 validator）
		registerCustomValidations(v)

		// 全部配置完成后再发布，确保 Load 得到的 validator 永远是完整的
		Validator.Store(v)
	}
}

// registerCustomValidations 注册自定义验证规则。
//
// P1 #18：每个 RegisterValidation 的错误都必须检查——注册失败会导致该 tag 静默不存在、
// 所有输入被视为通过（password/idcard 等 fail-open 安全漏洞）。用 must 包装，在 init
// 期以明确信息 panic（配置类错误应启动即暴露，而非运行期静默放行）。
func registerCustomValidations(v *validator.Validate) {
	must := func(tag string, fn validator.Func) {
		if err := v.RegisterValidation(tag, fn); err != nil {
			panic(fmt.Sprintf("validation: 注册校验规则 %q 失败: %v", tag, err))
		}
	}

	// 密码强度验证
	must("password", func(fl validator.FieldLevel) bool {
		password := fl.Field().String()
		valid, _ := ValidatePassword(password)
		return valid
	})

	// 手机号验证（中国大陆）
	must("phone", func(fl validator.FieldLevel) bool {
		phone := fl.Field().String()
		if len(phone) != 11 || !strings.HasPrefix(phone, "1") {
			return false
		}
		// P1 #17：要求全数字——原实现仅查长度+前缀，"1abcdefghij" 这类会误通过。
		for i := 0; i < len(phone); i++ {
			if phone[i] < '0' || phone[i] > '9' {
				return false
			}
		}
		return true
	})

	// 用户名验证（字母开头，允许字母数字下划线）
	must("username", func(fl validator.FieldLevel) bool {
		username := fl.Field().String()
		if len(username) < 3 || len(username) > 20 {
			return false
		}
		// 首字符按 rune 取，避免非 ASCII 首字节误判（M5：原 rune(username[0]) 取字节）。
		runes := []rune(username)
		if len(runes) == 0 || !isLetter(runes[0]) {
			return false
		}
		for _, r := range runes {
			if !isLetter(r) && !isDigit(r) && r != '_' {
				return false
			}
		}
		return true
	})

	// 手机号严格验证（验证运营商号段）
	must("phone_strict", func(fl validator.FieldLevel) bool {
		phone := fl.Field().String()
		if len(phone) != 11 {
			return false
		}
		if !strings.HasPrefix(phone, "1") {
			return false
		}
		// 检查号段
		prefix := phone[:3]
		validPrefixes := []string{
			"130", "131", "132", "133", "134", "135", "136", "137", "138", "139",
			"145", "146", "147", "148", "149",
			"150", "151", "152", "153", "155", "156", "157", "158", "159",
			"166", "167",
			"170", "171", "172", "173", "174", "175", "176", "177", "178",
			"180", "181", "182", "183", "184", "185", "186", "187", "188", "189",
			"191", "198", "199",
		}
		for _, p := range validPrefixes {
			if prefix == p {
				return true
			}
		}
		return false
	})

	// 身份证号验证（18 位带校验位；15 位仅格式，向后兼容旧号段）。
	must("idcard", func(fl validator.FieldLevel) bool {
		id := fl.Field().String()
		if len(id) != 18 && len(id) != 15 {
			return false
		}
		// 基本格式：前 17 位（18 位号）或全部（15 位号）须为数字，18 位号末位可为 X。
		for i, c := range id {
			if i == len(id)-1 && len(id) == 18 {
				if !isDigit(c) && c != 'X' && c != 'x' {
					return false
				}
			} else {
				if !isDigit(c) {
					return false
				}
			}
		}
		// 18 位号校验校验位（M5：原仅查长度+格式，无校验位可被任意构造通过）。
		if len(id) == 18 {
			if !validateIDCardChecksum(id) {
				return false
			}
		}
		return true
	})
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// validateIDCardChecksum 校验 18 位身份证校验位（GB 11643-1999，M5）。
// 前 17 位按权重 [7,9,10,5,8,4,2,1,6,3,7,9,10,5,8,4,2] 加权求和 mod 11，
// 对照码表 [1,0,X,9,8,7,6,5,4,3,2] 得期望末位。
func validateIDCardChecksum(id string) bool {
	weights := [17]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	checkCodes := [11]byte{'1', '0', 'X', '9', '8', '7', '6', '5', '4', '3', '2'}
	sum := 0
	for i := 0; i < 17; i++ {
		c := id[i]
		if c < '0' || c > '9' {
			return false
		}
		sum += int(c-'0') * weights[i]
	}
	expected := checkCodes[sum%11]
	last := id[17]
	// 末位 X 大小写不敏感
	if last == 'x' {
		last = 'X'
	}
	return last == expected
}

// ValidateStruct 验证结构体
func ValidateStruct(s any) ValidationErrors {
	if s == nil {
		return nil
	}

	v := Validator.Load()
	if v == nil {
		InitValidator()
		v = Validator.Load()
	}

	err := v.Struct(s)
	if err == nil {
		return nil
	}

	return parseValidationErrors(err, s)
}

// parseValidationErrors 解析验证错误（支持自定义错误消息）
func parseValidationErrors(err error, s any) ValidationErrors {
	var errors ValidationErrors

	if validationErrors, ok := err.(validator.ValidationErrors); ok {
		for _, e := range validationErrors {
			fieldName := e.Field()
			label := fieldName // Field() 返回的是 label 或 json tag

			// 尝试获取原始字段名和自定义错误消息
			if s != nil {
				t := reflect.TypeOf(s)
				if t.Kind() == reflect.Ptr {
					t = t.Elem()
				}
				if t.Kind() == reflect.Struct {
					// 获取原始字段名
					originalField := getOriginalFieldName(t, e.StructField())
					if originalField != "" {
						fieldName = originalField
					}

					// 获取自定义错误消息
					field, found := t.FieldByName(e.StructField())
					if found {
						customMsg := getCustomErrorMessage(field, e.Tag())
						if customMsg != "" {
							errors = append(errors, ValidationError{
								Field:   fieldName,
								Label:   label,
								Message: customMsg,
							})
							continue
						}
					}
				}
			}

			errors = append(errors, ValidationError{
				Field:   fieldName,
				Label:   label,
				Message: getErrorMessage(e),
			})
		}
	}

	return errors
}

// getOriginalFieldName 获取原始字段名（从 json tag）
func getOriginalFieldName(t reflect.Type, structField string) string {
	field, found := t.FieldByName(structField)
	if !found {
		return ""
	}

	jsonTag := field.Tag.Get("json")
	if jsonTag == "" || jsonTag == "-" {
		return structField
	}

	name := strings.SplitN(jsonTag, ",", 2)[0]
	if name == "" {
		return structField
	}
	return name
}

// getCustomErrorMessage 获取自定义错误消息
// 支持格式：
//   - error:"自定义错误消息"
//   - msg_required:"必填项"  (针对特定验证规则)
//   - msg_min:"最少5个字符"
func getCustomErrorMessage(field reflect.StructField, tag string) string {
	// 优先查找特定规则的错误消息
	specificTag := fmt.Sprintf("msg_%s", tag)
	msg := field.Tag.Get(specificTag)
	if msg != "" {
		return msg
	}

	// 其次查找通用错误消息
	msg = field.Tag.Get("error")
	if msg != "" {
		return msg
	}

	// 最后查找 msg tag
	msg = field.Tag.Get("msg")
	if msg != "" {
		return msg
	}

	return ""
}

// getErrorMessage 获取默认验证错误消息
func getErrorMessage(e validator.FieldError) string {
	switch e.Tag() {
	case "required":
		return "此字段为必填项"
	case "email":
		return "邮箱格式不正确"
	case "min":
		return fmt.Sprintf("长度不能少于 %s 个字符", e.Param())
	case "max":
		return fmt.Sprintf("长度不能超过 %s 个字符", e.Param())
	case "len":
		return fmt.Sprintf("长度必须为 %s 个字符", e.Param())
	case "gte":
		return fmt.Sprintf("必须大于或等于 %s", e.Param())
	case "lte":
		return fmt.Sprintf("必须小于或等于 %s", e.Param())
	case "gt":
		return fmt.Sprintf("必须大于 %s", e.Param())
	case "lt":
		return fmt.Sprintf("必须小于 %s", e.Param())
	case "eq":
		return fmt.Sprintf("必须等于 %s", e.Param())
	case "ne":
		return fmt.Sprintf("不能等于 %s", e.Param())
	case "oneof":
		return fmt.Sprintf("必须是以下值之一: %s", e.Param())
	case "url":
		return "URL 格式不正确"
	case "uri":
		return "URI 格式不正确"
	case "uuid":
		return "UUID 格式不正确"
	case "alphanum":
		return "只能包含字母和数字"
	case "alpha":
		return "只能包含字母"
	case "numeric":
		return "必须是数字"
	case "password":
		return "密码强度不足，需包含大小写字母和数字，至少8位"
	case "phone":
		return "手机号格式不正确"
	case "phone_strict":
		return "手机号无效，请输入正确的手机号"
	case "username":
		return "用户名必须以字母开头，只能包含字母、数字和下划线，长度3-20"
	case "idcard":
		return "身份证号格式不正确"
	default:
		return fmt.Sprintf("验证失败: %s", e.Tag())
	}
}

// BindAndValidate 绑定并验证请求
func BindAndValidate(c *gin.Context, req any) ValidationErrors {
	if err := c.ShouldBind(req); err != nil {
		return parseValidationErrors(err, req)
	}
	return ValidateStruct(req)
}

// ShouldBindAndValidate 绑定并验证请求，返回是否成功
func ShouldBindAndValidate(c *gin.Context, req any) (ValidationErrors, bool) {
	errors := BindAndValidate(c, req)
	return errors, len(errors) == 0
}

// BindJSON 绑定 JSON 并验证
func BindJSON(c *gin.Context, req any) ValidationErrors {
	if err := c.ShouldBindJSON(req); err != nil {
		return parseValidationErrors(err, req)
	}
	return ValidateStruct(req)
}

// BindQuery 绑定 Query 并验证
func BindQuery(c *gin.Context, req any) ValidationErrors {
	if err := c.ShouldBindQuery(req); err != nil {
		return parseValidationErrors(err, req)
	}
	return ValidateStruct(req)
}

// BindForm 绑定 Form 并验证
func BindForm(c *gin.Context, req any) ValidationErrors {
	if err := c.ShouldBind(req); err != nil {
		return parseValidationErrors(err, req)
	}
	return ValidateStruct(req)
}
