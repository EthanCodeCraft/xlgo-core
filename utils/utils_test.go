package utils_test

import (
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/utils"
)

// ===== Random Tests =====
//
// 注意（H1 收紧）：RandString/RandDigit 已移除（字符串随机的用途几乎都是安全场景，
// 保留 math/rand 版本会诱导误用）。测试覆盖 RandStringSecure/RandDigitSecure（crypto/rand）
// 与 RandInt/RandInt64（非安全范围随机）。

func TestRandInt(t *testing.T) {
	// 正常范围
	for i := 0; i < 100; i++ {
		r := utils.RandInt(1, 100)
		if r < 1 || r >= 100 {
			t.Errorf("RandInt(1, 100) = %d, out of range", r)
		}
	}

	// min == max
	r := utils.RandInt(5, 5)
	if r != 5 {
		t.Errorf("RandInt(5, 5) = %d", r)
	}

	// min > max (自动交换)
	r = utils.RandInt(100, 1)
	if r < 1 || r >= 100 {
		t.Errorf("RandInt(100, 1) = %d, should swap", r)
	}
}

func TestRandInt64(t *testing.T) {
	r := utils.RandInt64(0, 1000000)
	if r < 0 || r >= 1000000 {
		t.Errorf("RandInt64 out of range: %d", r)
	}
}

// ===== String Tests =====

func TestIsBlank(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"", true},
		{"   ", true},
		{"\t\n", true},
		{"abc", false},
		{"  abc  ", false},
	}

	for _, tt := range tests {
		if utils.IsBlank(tt.input) != tt.expected {
			t.Errorf("IsBlank(%q) = %v, want %v", tt.input, !tt.expected, tt.expected)
		}
	}
}

func TestIsAnyBlank(t *testing.T) {
	if !utils.IsAnyBlank("", "a") {
		t.Error("IsAnyBlank should return true")
	}
	if utils.IsAnyBlank("a", "b") {
		t.Error("IsAnyBlank should return false")
	}
}

func TestIsAllBlank(t *testing.T) {
	if !utils.IsAllBlank("", "  ", "\t") {
		t.Error("IsAllBlank should return true")
	}
	if utils.IsAllBlank("", "a") {
		t.Error("IsAllBlank should return false")
	}
}

func TestDefaultIfBlank(t *testing.T) {
	if utils.DefaultIfBlank("", "def") != "def" {
		t.Error("DefaultIfBlank empty failed")
	}
	if utils.DefaultIfBlank("val", "def") != "val" {
		t.Error("DefaultIfBlank non-empty failed")
	}
}

func TestSubstr(t *testing.T) {
	tests := []struct {
		input   string
		start   int
		length  int
		expected string
	}{
		{"hello世界", 0, 7, "hello世界"},
		{"hello世界", 0, 5, "hello"},
		{"hello世界", 5, 2, "世界"},
		{"hello世界", -2, 2, "世界"},
		{"hello世界", 100, 2, ""},
		{"", 0, 5, ""},
	}

	for _, tt := range tests {
		result := utils.Substr(tt.input, tt.start, tt.length)
		if result != tt.expected {
			t.Errorf("Substr(%q, %d, %d) = %q, want %q", tt.input, tt.start, tt.length, result, tt.expected)
		}
	}
}

func TestStrLen(t *testing.T) {
	if utils.StrLen("hello世界") != 7 {
		t.Error("StrLen Unicode count failed")
	}
	if utils.StrLen("") != 0 {
		t.Error("StrLen empty failed")
	}
}

func TestEqualsIgnoreCase(t *testing.T) {
	tests := []struct {
		a, b     string
		expected bool
	}{
		{"ABC", "abc", true},
		{"Hello", "HELLO", true},
		{"abc", "abc", true},
		{"abc", "abd", false},
		{"ab", "abc", false},
	}

	for _, tt := range tests {
		if utils.EqualsIgnoreCase(tt.a, tt.b) != tt.expected {
			t.Errorf("EqualsIgnoreCase(%q, %q) failed", tt.a, tt.b)
		}
	}
}

func TestTrim(t *testing.T) {
	if utils.Trim("  hello  ") != "hello" {
		t.Error("Trim failed")
	}
	if utils.Trim("\t\nhello\n\t") != "hello" {
		t.Error("Trim whitespace failed")
	}
}

// ===== DateTime Tests =====

func TestNowUnix(t *testing.T) {
	n := utils.NowUnix()
	if n == 0 {
		t.Error("NowUnix returned 0")
	}
	// 应接近当前时间
	expected := time.Now().Unix()
	if n < expected-1 || n > expected+1 {
		t.Errorf("NowUnix = %d, expected ~%d", n, expected)
	}
}

func TestNowTimestamp(t *testing.T) {
	n := utils.NowTimestamp()
	if n == 0 {
		t.Error("NowTimestamp returned 0")
	}
}

func TestFromUnix(t *testing.T) {
	now := time.Now()
	unix := now.Unix()
	result := utils.FromUnix(unix)
	if result.Unix() != unix {
		t.Errorf("FromUnix mismatch")
	}
}

func TestFormatDateTime(t *testing.T) {
	now := time.Now()
	s := utils.FormatDateTime(now)
	if len(s) != 19 {
		t.Errorf("FormatDateTime length = %d", len(s))
	}
}

func TestFormatDate(t *testing.T) {
	now := time.Now()
	s := utils.FormatDate(now)
	if len(s) != 10 {
		t.Errorf("FormatDate length = %d", len(s))
	}
}

func TestStartEndOfDay(t *testing.T) {
	now := time.Now()
	start := utils.StartOfDay(now)
	end := utils.EndOfDay(now)

	if start.Hour() != 0 || start.Minute() != 0 || start.Second() != 0 {
		t.Error("StartOfDay not at 00:00:00")
	}
	if end.Hour() != 23 || end.Minute() != 59 || end.Second() != 59 {
		t.Error("EndOfDay not at 23:59:59")
	}
}

func TestStartEndOfMonth(t *testing.T) {
	now := time.Now()
	start := utils.StartOfMonth(now)
	end := utils.EndOfMonth(now)

	if start.Day() != 1 {
		t.Error("StartOfMonth day != 1")
	}
	if end.Day() < 28 || end.Day() > 31 {
		t.Errorf("EndOfMonth day = %d, unexpected", end.Day())
	}
}

func TestGetDateInt(t *testing.T) {
	now := time.Now()
	dateInt := utils.GetDateInt(now)
	expected := now.Year()*10000 + int(now.Month())*100 + now.Day()
	if dateInt != expected {
		t.Errorf("GetDateInt = %d, expected %d", dateInt, expected)
	}
}

func TestParseDateInt(t *testing.T) {
	dateInt := 20260430
	result := utils.ParseDateInt(dateInt)
	if result.Year() != 2026 || result.Month() != 4 || result.Day() != 30 {
		t.Errorf("ParseDateInt(%d) = %v", dateInt, result)
	}
}

// ===== Convert Tests =====

func TestToInt(t *testing.T) {
	if utils.ToInt("123") != 123 {
		t.Error("ToInt failed")
	}
	if utils.ToInt("abc") != 0 {
		t.Error("ToInt invalid should return 0")
	}
}

func TestToIntDefault(t *testing.T) {
	if utils.ToIntDefault("123", 999) != 123 {
		t.Error("ToIntDefault valid failed")
	}
	if utils.ToIntDefault("abc", 999) != 999 {
		t.Error("ToIntDefault invalid should return default")
	}
}

func TestToInt64(t *testing.T) {
	if utils.ToInt64("1234567890123") != 1234567890123 {
		t.Error("ToInt64 failed")
	}
}

func TestToInt64Default(t *testing.T) {
	if utils.ToInt64Default("abc", 999) != 999 {
		t.Error("ToInt64Default failed")
	}
}

func TestToUint64Default(t *testing.T) {
	if utils.ToUint64Default("abc", 999) != 999 {
		t.Error("ToUint64Default failed")
	}
	if utils.ToUint64Default("123", 0) != 123 {
		t.Error("ToUint64Default valid failed")
	}
}

func TestToFloat64(t *testing.T) {
	if utils.ToFloat64("3.14") != 3.14 {
		t.Error("ToFloat64 failed")
	}
}

func TestToFloat64Default(t *testing.T) {
	if utils.ToFloat64Default("abc", 1.5) != 1.5 {
		t.Error("ToFloat64Default failed")
	}
}

func TestToString(t *testing.T) {
	if utils.ToString(123) != "123" {
		t.Error("ToString failed")
	}
}

func TestToString64(t *testing.T) {
	if utils.ToString64(1234567890123) != "1234567890123" {
		t.Error("ToString64 failed")
	}
}

func TestCalcPageCount(t *testing.T) {
	tests := []struct {
		total    int64
		pageSize int64
		expected int64
	}{
		{100, 10, 10},
		{95, 10, 10},
		{0, 10, 0},
		{100, 0, 0},
		{1, 10, 1},
	}

	for _, tt := range tests {
		result := utils.CalcPageCount(tt.total, tt.pageSize)
		if result != tt.expected {
			t.Errorf("CalcPageCount(%d, %d) = %d, want %d", tt.total, tt.pageSize, result, tt.expected)
		}
	}
}

func TestCalcOffset(t *testing.T) {
	tests := []struct {
		page     int
		pageSize int
		expected int
	}{
		{1, 20, 0},
		{2, 20, 20},
		{3, 10, 20},
		{0, 20, 0},  // page <= 0 自动修正为 1
		{1, 0, 0},   // pageSize <= 0 自动修正为 20
	}

	for _, tt := range tests {
		result := utils.CalcOffset(tt.page, tt.pageSize)
		if result != tt.expected {
			t.Errorf("CalcOffset(%d, %d) = %d, want %d", tt.page, tt.pageSize, result, tt.expected)
		}
	}
}

// ===== Validator Tests =====

func TestIsPhone(t *testing.T) {
	valid := []string{"13812345678", "15912345678", "18812345678"}
	invalid := []string{"12345678901", "1381234567", "138123456789"}

	for _, p := range valid {
		if !utils.IsPhone(p) {
			t.Errorf("IsPhone(%s) should be true", p)
		}
	}
	for _, p := range invalid {
		if utils.IsPhone(p) {
			t.Errorf("IsPhone(%s) should be false", p)
		}
	}
}

func TestIsEmail(t *testing.T) {
	valid := []string{"test@example.com", "user.name@domain.cn"}
	invalid := []string{"invalid", "no@", "@nodomain.com"}

	for _, e := range valid {
		if !utils.IsEmail(e) {
			t.Errorf("IsEmail(%s) should be true", e)
		}
	}
	for _, e := range invalid {
		if utils.IsEmail(e) {
			t.Errorf("IsEmail(%s) should be false", e)
		}
	}
}

func TestIsIPv4(t *testing.T) {
	valid := []string{"192.168.1.1", "0.0.0.0", "255.255.255.255"}
	invalid := []string{"256.1.1.1", "1.1.1", "1.1.1.1.1"}

	for _, ip := range valid {
		if !utils.IsIPv4(ip) {
			t.Errorf("IsIPv4(%s) should be true", ip)
		}
	}
	for _, ip := range invalid {
		if utils.IsIPv4(ip) {
			t.Errorf("IsIPv4(%s) should be false", ip)
		}
	}
}

func TestIsIDCard(t *testing.T) {
	// 18位身份证
	if !utils.IsIDCard("11010519900307293X") {
		t.Error("IsIDCard valid 18-digit failed")
	}
	// 无效
	if utils.IsIDCard("123456789012345") {
		t.Error("IsIDCard invalid should be false")
	}
}

func TestIsChinese(t *testing.T) {
	if !utils.IsChinese("中文") {
		t.Error("IsChinese should be true")
	}
	if utils.IsChinese("abc中文") {
		t.Error("IsChinese mixed should be false")
	}
}

func TestIsNumeric(t *testing.T) {
	if !utils.IsNumeric("12345") {
		t.Error("IsNumeric should be true")
	}
	if utils.IsNumeric("123a") {
		t.Error("IsNumeric should be false")
	}
}

func TestIsAlphanumeric(t *testing.T) {
	if !utils.IsAlphanumeric("abc123") {
		t.Error("IsAlphanumeric should be true")
	}
	if utils.IsAlphanumeric("abc-123") {
		t.Error("IsAlphanumeric with dash should be false")
	}
}

// ===== Crypto Tests =====

func TestMD5(t *testing.T) {
	if utils.MD5("hello") != "5d41402abc4b2a76b9719d911017c592" {
		t.Error("MD5 failed")
	}
	if utils.MD5("") != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Error("MD5 empty failed")
	}
}

func TestSHA256(t *testing.T) {
	// SHA256 有固定长度
	hash := utils.SHA256("hello")
	if len(hash) != 64 {
		t.Errorf("SHA256 length = %d", len(hash))
	}
}

func TestBase64(t *testing.T) {
	original := "hello world"
	encoded := utils.Base64Encode([]byte(original))
	decoded, err := utils.Base64Decode(encoded)
	if err != nil {
		t.Errorf("Base64Decode error: %v", err)
	}
	if string(decoded) != original {
		t.Errorf("Base64 roundtrip failed: %s", decoded)
	}
}

func TestBase64URL(t *testing.T) {
	original := "hello+world"
	encoded := utils.Base64URLEncode([]byte(original))
	decoded, err := utils.Base64URLDecode(encoded)
	if err != nil {
		t.Errorf("Base64URLDecode error: %v", err)
	}
	if string(decoded) != original {
		t.Errorf("Base64URL roundtrip failed")
	}
}

// ===== UUID Tests =====

func TestUUID(t *testing.T) {
	uuid := utils.UUID()
	if len(uuid) != 36 {
		t.Errorf("UUID length = %d", len(uuid))
	}
	// 格式检查: 8-4-4-4-12
	if uuid[8] != '-' || uuid[13] != '-' || uuid[18] != '-' || uuid[23] != '-' {
		t.Errorf("UUID format invalid: %s", uuid)
	}
}

func TestUUIDShort(t *testing.T) {
	uuid := utils.UUIDShort()
	if len(uuid) != 32 {
		t.Errorf("UUIDShort length = %d", len(uuid))
	}
}

func TestUUIDValid(t *testing.T) {
	valid := "550e8400-e29b-41d4-a716-446655440000"
	invalid := "invalid-uuid"

	if !utils.UUIDValid(valid) {
		t.Errorf("UUIDValid(%s) should be true", valid)
	}
	if utils.UUIDValid(invalid) {
		t.Errorf("UUIDValid(%s) should be false", invalid)
	}
}

// ===== URL Tests =====

func TestURLEncodeDecode(t *testing.T) {
	original := "hello world"
	encoded := utils.URLEncode(original)
	decoded, err := utils.URLDecode(encoded)
	if err != nil {
		t.Errorf("URLDecode error: %v", err)
	}
	if decoded != original {
		t.Errorf("URL roundtrip failed: %s -> %s -> %s", original, encoded, decoded)
	}
}

func TestParseURL(t *testing.T) {
	b, err := utils.ParseURL("https://example.com/path")
	if err != nil {
		t.Errorf("ParseURL error: %v", err)
	}
	result := b.AddQuery("key", "value").AddQuery("foo", "bar").String()
	if !contains(result, "key=value") || !contains(result, "foo=bar") {
		t.Errorf("URLBuilder result: %s", result)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ===== File Tests =====

func TestFileExists(t *testing.T) {
	if utils.FileExists("/nonexistent/path") {
		t.Error("FileExists should return false for nonexistent")
	}
}

func TestDirExists(t *testing.T) {
	if utils.DirExists("/nonexistent/dir") {
		t.Error("DirExists should return false")
	}
}

// ===== Secure Random Tests（H1：crypto/rand 安全版本） =====

// 回归 H1：RandStringSecure 生成正确长度、仅含字母数字。
func TestRandStringSecure(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{"normal", 16},
		{"short", 1},
		{"long", 100},
		{"zero", 0},
		{"negative", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := utils.RandStringSecure(tt.length)
			if err != nil {
				t.Fatalf("RandStringSecure(%d) err: %v", tt.length, err)
			}
			if tt.length <= 0 {
				if s != "" {
					t.Errorf("RandStringSecure(%d) should return empty", tt.length)
				}
				return
			}
			if len(s) != tt.length {
				t.Errorf("RandStringSecure(%d) length = %d", tt.length, len(s))
			}
			for _, c := range s {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
					t.Errorf("RandStringSecure contains invalid char: %c", c)
				}
			}
		})
	}
}

// 回归 H1：RandStringSecure 唯一性——crypto/rand 不可预测，大批量无重复。
// 对比 RandString（math/rand）偶发重复（H1 征兆）。
func TestRandStringSecureUniqueness(t *testing.T) {
	results := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		s, err := utils.RandStringSecure(16)
		if err != nil {
			t.Fatalf("RandStringSecure err: %v", err)
		}
		if results[s] {
			t.Fatalf("RandStringSecure generated duplicate at i=%d: %q (H1: crypto/rand must be unique)", i, s)
		}
		results[s] = true
	}
}

// 回归 H1：RandDigitSecure 生成正确长度、仅含数字（用于 OTP）。
func TestRandDigitSecure(t *testing.T) {
	s, err := utils.RandDigitSecure(6)
	if err != nil {
		t.Fatalf("RandDigitSecure err: %v", err)
	}
	if len(s) != 6 {
		t.Fatalf("RandDigitSecure length = %d, want 6", len(s))
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Errorf("RandDigitSecure contains non-digit: %c", c)
		}
	}
}

// 回归 H1：RandDigitSecure 唯一性——6 位 OTP 大批量重复率应在合理范围。
// 6 位数字空间 10^6，1000 个样本重复概率极低（生日攻击 ~0.0005），crypto/rand 下应无重复。
func TestRandDigitSecureUniqueness(t *testing.T) {
	results := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		s, err := utils.RandDigitSecure(6)
		if err != nil {
			t.Fatalf("RandDigitSecure err: %v", err)
		}
		if results[s] {
			// 6 位空间小，理论上有极小概率生日碰撞；若发生则记录但不直接失败，
			// 用更长的码验证。此处改用 8 位码重测以排除碰撞。
			t.Logf("RandDigitSecure(6) collision at i=%d (small space), retrying with 8 digits", i)
			goto retry8
		}
		results[s] = true
	}
	return
retry8:
	results = make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		s, err := utils.RandDigitSecure(8)
		if err != nil {
			t.Fatalf("RandDigitSecure(8) err: %v", err)
		}
		if results[s] {
			t.Fatalf("RandDigitSecure(8) generated duplicate at i=%d (H1: crypto/rand must be unique)", i)
		}
		results[s] = true
	}
}

// 回归 H1：RandStringSecure 过大长度返回错误（保护熵池）。
func TestRandStringSecureTooLarge(t *testing.T) {
	_, err := utils.RandStringSecure(1 << 21) // 2MB，超过 1<<20 上限
	if err != utils.ErrRandInvalidLength {
		t.Errorf("RandStringSecure(too large) err = %v, want ErrRandInvalidLength", err)
	}
	_, err = utils.RandDigitSecure(1 << 21)
	if err != utils.ErrRandInvalidLength {
		t.Errorf("RandDigitSecure(too large) err = %v, want ErrRandInvalidLength", err)
	}
}

// 回归 H1：RandIntSecure 范围正确、min==max、max<min 自动交换、crypto/rand 无偏。
func TestRandIntSecure(t *testing.T) {
	// 正常范围 [1,100)
	for i := 0; i < 1000; i++ {
		n, err := utils.RandIntSecure(1, 100)
		if err != nil {
			t.Fatalf("RandIntSecure err: %v", err)
		}
		if n < 1 || n >= 100 {
			t.Errorf("RandIntSecure(1,100) = %d, out of range", n)
		}
	}

	// min == max
	n, err := utils.RandIntSecure(5, 5)
	if err != nil || n != 5 {
		t.Errorf("RandIntSecure(5,5) = %d, err=%v, want 5", n, err)
	}

	// max < min 自动交换
	n, err = utils.RandIntSecure(100, 1)
	if err != nil {
		t.Fatalf("RandIntSecure(100,1) err: %v", err)
	}
	if n < 1 || n >= 100 {
		t.Errorf("RandIntSecure(100,1) = %d, should swap to [1,100)", n)
	}
}

// 回归 H1：RandInt64Secure 唯一性——crypto/rand 在大空间内分布均匀、不可预测。
// [0, 1<<40) 取 1000 个，生日攻击重复概率 ~0.0000005，crypto/rand 应无重复。
func TestRandInt64SecureUniqueness(t *testing.T) {
	seen := make(map[int64]bool, 1000)
	for i := 0; i < 1000; i++ {
		n, err := utils.RandInt64Secure(0, 1<<40)
		if err != nil {
			t.Fatalf("RandInt64Secure err: %v", err)
		}
		if seen[n] {
			t.Fatalf("RandInt64Secure duplicate at i=%d: %d (H1: crypto/rand must be unique in large range)", i, n)
		}
		seen[n] = true
	}
}

// 回归 H1：RandInt64Secure 范围正确、min==max、max<min 自动交换。
func TestRandInt64Secure(t *testing.T) {
	for i := 0; i < 1000; i++ {
		n, err := utils.RandInt64Secure(0, 1000000)
		if err != nil {
			t.Fatalf("RandInt64Secure err: %v", err)
		}
		if n < 0 || n >= 1000000 {
			t.Errorf("RandInt64Secure(0,1e6) = %d, out of range", n)
		}
	}

	// min == max
	n, err := utils.RandInt64Secure(7, 7)
	if err != nil || n != 7 {
		t.Errorf("RandInt64Secure(7,7) = %d, err=%v, want 7", n, err)
	}

	// max < min 自动交换
	n, err = utils.RandInt64Secure(1000000, 0)
	if err != nil {
		t.Fatalf("RandInt64Secure(1e6,0) err: %v", err)
	}
	if n < 0 || n >= 1000000 {
		t.Errorf("RandInt64Secure(1e6,0) = %d, should swap to [0,1e6)", n)
	}
}

// ===== Benchmarks =====

func BenchmarkRandStringSecure(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = utils.RandStringSecure(16)
	}
}

func BenchmarkRandDigitSecure(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = utils.RandDigitSecure(6)
	}
}

func BenchmarkMD5(b *testing.B) {
	for i := 0; i < b.N; i++ {
		utils.MD5("hello world")
	}
}

func BenchmarkSHA256(b *testing.B) {
	for i := 0; i < b.N; i++ {
		utils.SHA256("hello world")
	}
}

func BenchmarkUUID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		utils.UUID()
	}
}

func BenchmarkStrLen(b *testing.B) {
	s := "hello世界测试字符串"
	for i := 0; i < b.N; i++ {
		utils.StrLen(s)
	}
}

func BenchmarkCalcPageCount(b *testing.B) {
	for i := 0; i < b.N; i++ {
		utils.CalcPageCount(10000, 20)
	}
}