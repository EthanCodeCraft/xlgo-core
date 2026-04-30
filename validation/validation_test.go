package validation_test

import (
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/validation"
	"golang.org/x/crypto/bcrypt"
)

// ===== Password Tests =====

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		password string
		valid    bool
		msg      string
	}{
		{"Abc12345", true, ""},                    // 有效
		{"abc12345", false, "密码必须包含大写字母"}, // 缺大写
		{"ABC12345", false, "密码必须包含小写字母"}, // 缺小写
		{"Abcdefgh", false, "密码必须包含数字"},    // 缺数字
		{"Abc123", false, "密码长度不能少于8位"},   // 太短
		{"", false, "密码长度不能少于8位"},         // 空
	}

	for _, tt := range tests {
		valid, msg := validation.ValidatePassword(tt.password)
		if valid != tt.valid {
			t.Errorf("ValidatePassword(%s) valid = %v, want %v", tt.password, valid, tt.valid)
		}
		if !valid && msg != tt.msg {
			t.Errorf("ValidatePassword(%s) msg = %s, want %s", tt.password, msg, tt.msg)
		}
	}
}

func TestValidatePasswordWithConfig(t *testing.T) {
	// 自定义配置：不要求特殊字符
	config := validation.PasswordConfig{
		MinLength:      6,
		MaxLength:      20,
		RequireUpper:   false,
		RequireLower:   true,
		RequireDigit:   true,
		RequireSpecial: false,
	}

	valid, msg := validation.ValidatePasswordWithConfig("abc123", config)
	if !valid {
		t.Errorf("ValidatePasswordWithConfig should be valid: %s", msg)
	}

	// 要求特殊字符
	config2 := validation.PasswordConfig{
		MinLength:      8,
		MaxLength:      20,
		RequireUpper:   true,
		RequireLower:   true,
		RequireDigit:   true,
		RequireSpecial: true,
	}

	valid2, msg2 := validation.ValidatePasswordWithConfig("Abc12345", config2)
	if valid2 {
		t.Error("Should require special character")
	}
	if msg2 != "密码必须包含特殊字符" {
		t.Errorf("msg = %s, want '密码必须包含特殊字符'", msg2)
	}

	// 包含特殊字符
	valid3, msg3 := validation.ValidatePasswordWithConfig("Abc123!@#", config2)
	if !valid3 {
		t.Errorf("Should be valid: %s", msg3)
	}
}

func TestDefaultPasswordConfig(t *testing.T) {
	cfg := validation.DefaultPasswordConfig
	if cfg.MinLength != 8 {
		t.Errorf("MinLength = %d, want 8", cfg.MinLength)
	}
	if cfg.MaxLength != 128 {
		t.Errorf("MaxLength = %d, want 128", cfg.MaxLength)
	}
	if !cfg.RequireUpper || !cfg.RequireLower || !cfg.RequireDigit {
		t.Error("Default config should require upper, lower, digit")
	}
}

// ===== Hash Password Tests =====

func TestHashPassword(t *testing.T) {
	password := "testPassword123"

	hash, err := validation.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}

	// 验证 hash 不等于原密码
	if hash == password {
		t.Error("Hash should not equal original password")
	}

	// 验证 hash 非空
	if hash == "" {
		t.Error("Hash should not be empty")
	}

	// 验证可以匹配
	if !validation.CheckPassword(hash, password) {
		t.Error("CheckPassword should return true for correct password")
	}

	// 验证错误密码不匹配
	if validation.CheckPassword(hash, "wrongPassword") {
		t.Error("CheckPassword should return false for wrong password")
	}
}

func TestHashPasswordWithCost(t *testing.T) {
	password := "testPassword123"

	// 测试不同 cost
	hash, err := validation.HashPasswordWithCost(password, 10)
	if err != nil {
		t.Fatalf("HashPasswordWithCost error: %v", err)
	}

	cost, err := validation.GetPasswordCost(hash)
	if err != nil {
		t.Fatalf("GetPasswordCost error: %v", err)
	}
	if cost != 10 {
		t.Errorf("Cost = %d, want 10", cost)
	}

	// 测试 cost 边界
	hash2, err := validation.HashPasswordWithCost(password, 3) // 低于 MinCost
	if err != nil {
		t.Fatalf("HashPasswordWithCost with low cost error: %v", err)
	}
	cost2, _ := validation.GetPasswordCost(hash2)
	if cost2 < bcrypt.MinCost {
		t.Errorf("Cost should be at least MinCost")
	}
}

func TestCheckPasswordAndUpgrade(t *testing.T) {
	password := "testPassword123"

	// 使用低 cost 创建 hash
	hash, _ := validation.HashPasswordWithCost(password, 4)

	// 检查并尝试升级
	match, needUpgrade, newHash, err := validation.CheckPasswordAndUpgrade(hash, password, 12)
	if err != nil {
		t.Fatalf("CheckPasswordAndUpgrade error: %v", err)
	}

	if !match {
		t.Error("Password should match")
	}

	if !needUpgrade {
		t.Error("Should need upgrade (cost 4 -> 12)")
	}

	if newHash == "" {
		t.Error("New hash should be provided")
	}

	// 验证新 hash 可用
	if !validation.CheckPassword(newHash, password) {
		t.Error("New hash should work")
	}

	// 使用高 cost，不需要升级
	hash2, _ := validation.HashPasswordWithCost(password, 12)
	match2, needUpgrade2, _, _ := validation.CheckPasswordAndUpgrade(hash2, password, 12)
	if !match2 {
		t.Error("Password should match")
	}
	if needUpgrade2 {
		t.Error("Should not need upgrade")
	}

	// 错误密码
	match3, _, _, _ := validation.CheckPasswordAndUpgrade(hash, "wrongPassword", 12)
	if match3 {
		t.Error("Wrong password should not match")
	}
}

func TestGetPasswordCost(t *testing.T) {
	password := "testPassword123"
	hash, _ := validation.HashPasswordWithCost(password, 12)

	cost, err := validation.GetPasswordCost(hash)
	if err != nil {
		t.Fatalf("GetPasswordCost error: %v", err)
	}
	if cost != 12 {
		t.Errorf("Cost = %d, want 12", cost)
	}

	// 无效 hash
	_, err = validation.GetPasswordCost("invalidHash")
	if err == nil {
		t.Error("GetPasswordCost should fail with invalid hash")
	}
}

// ===== Validation Errors Tests =====

func TestValidationErrors(t *testing.T) {
	errors := validation.ValidationErrors{
		{Field: "name", Label: "姓名", Message: "必填"},
		{Field: "email", Label: "邮箱", Message: "格式错误"},
	}

	// Error 方法
	errStr := errors.Error()
	if errStr != "姓名: 必填; 邮箱: 格式错误" {
		t.Errorf("Error() = %s", errStr)
	}

	// ToMap 方法
	m := errors.ToMap()
	if m["name"] != "必填" {
		t.Error("ToMap failed")
	}

	// ToLabelMap 方法
	lm := errors.ToLabelMap()
	if lm["姓名"] != "必填" {
		t.Error("ToLabelMap failed")
	}

	// First 方法
	first := errors.First()
	if first.Field != "name" {
		t.Error("First failed")
	}

	// FirstMessage 方法
	msg := errors.FirstMessage()
	if msg != "必填" {
		t.Errorf("FirstMessage = %s", msg)
	}

	// 空 errors
	emptyErrors := validation.ValidationErrors{}
	if emptyErrors.First() != nil {
		t.Error("Empty First should return nil")
	}
	if emptyErrors.FirstMessage() != "" {
		t.Error("Empty FirstMessage should return empty")
	}
}

// ===== Struct Validation Tests =====

type TestUser struct {
	Name     string `json:"name" label:"姓名" binding:"required" msg_required:"姓名不能为空"`
	Email    string `json:"email" label:"邮箱" binding:"required,email" msg_required:"邮箱不能为空" msg_email:"邮箱格式不正确"`
	Age      int    `json:"age" label:"年龄" binding:"gte=0,lte=150"`
	Password string `json:"password" binding:"min=8" msg_min:"密码至少8位"`
}

func TestValidateStruct(t *testing.T) {
	validation.InitValidator()

	// 有效数据
	validUser := TestUser{
		Name:     "张三",
		Email:    "test@example.com",
		Age:      25,
		Password: "password123",
	}

	errors := validation.ValidateStruct(validUser)
	if errors != nil {
		t.Errorf("Valid struct should have no errors: %v", errors)
	}

	// 无效数据
	invalidUser := TestUser{
		Name:     "",
		Email:    "invalid-email",
		Age:      200,
		Password: "short",
	}

	errors2 := validation.ValidateStruct(invalidUser)
	if errors2 == nil {
		t.Error("Invalid struct should have errors")
	}

	// 检查错误数量
	if len(errors2) < 3 {
		t.Errorf("Should have at least 3 errors, got %d", len(errors2))
	}

	// 检查自定义消息
	firstMsg := errors2.FirstMessage()
	if firstMsg != "姓名不能为空" {
		t.Errorf("FirstMessage = %s, want '姓名不能为空'", firstMsg)
	}
}

func TestValidateStructNil(t *testing.T) {
	// 空结构体
	emptyUser := TestUser{}
	errors := validation.ValidateStruct(emptyUser)
	if errors == nil {
		t.Error("Empty struct should have errors")
	}
}

type TestPhone struct {
	Phone string `json:"phone" binding:"phone" msg_phone:"手机号格式不正确"`
}

func TestValidatePhone(t *testing.T) {
	validation.InitValidator()

	// 有效手机号
	valid := TestPhone{Phone: "13812345678"}
	errors := validation.ValidateStruct(valid)
	if errors != nil {
		t.Errorf("Valid phone should pass: %v", errors)
	}

	// 无效手机号 - 长度错误
	invalidLen := TestPhone{Phone: "1234567"}
	errors2 := validation.ValidateStruct(invalidLen)
	if errors2 == nil {
		t.Error("Invalid phone length should fail")
	}

	// 无效手机号 - 不以1开头
	invalidPrefix := TestPhone{Phone: "23812345678"}
	errors3 := validation.ValidateStruct(invalidPrefix)
	if errors3 == nil {
		t.Error("Phone not starting with 1 should fail")
	}
}

type TestUsername struct {
	Username string `json:"username" binding:"username" msg_username:"用户名格式不正确"`
}

func TestValidateUsername(t *testing.T) {
	validation.InitValidator()

	tests := []struct {
		username string
		valid    bool
	}{
		{"abc123", true},       // 有效
		{"Abc123", true},       // 有效（大写开头）
		{"user_name", true},    // 有效（包含下划线）
		{"123abc", false},      // 无效（数字开头）
		{"ab", false},          // 无效（太短）
		{"a!bc", false},        // 无效（特殊字符）
	}

	for _, tt := range tests {
		u := TestUsername{Username: tt.username}
		errors := validation.ValidateStruct(u)
		valid := errors == nil
		if valid != tt.valid {
			t.Errorf("Username %s: valid=%v, want %v", tt.username, valid, tt.valid)
		}
	}
}

// ===== Benchmarks =====

func BenchmarkHashPassword(b *testing.B) {
	password := "testPassword123"
	for i := 0; i < b.N; i++ {
		validation.HashPassword(password)
	}
}

func BenchmarkCheckPassword(b *testing.B) {
	password := "testPassword123"
	hash, _ := validation.HashPassword(password)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validation.CheckPassword(hash, password)
	}
}

func BenchmarkValidatePassword(b *testing.B) {
	password := "TestPassword123"
	for i := 0; i < b.N; i++ {
		validation.ValidatePassword(password)
	}
}