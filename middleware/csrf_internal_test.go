package middleware

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/gin-gonic/gin"
)

// 回归 C6b：TTL 过期分支。直接向包级存储注入一个“已过期”的 token，
// 断言 CSRFForAPI 拒绝它（即使该 token 从未被消费过）。
// 这条分支（time.Since(issuedAt) > apiTokenTTL）无法靠单次消费用例覆盖，
// 必须单独构造过期时间戳。
func TestCSRFForAPITTLExpiry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CSRFForAPI())
	r.POST("/action", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	expiredToken := "expired-test-token"
	apiTokensMu.Lock()
	// 注入一个早于 TTL 的颁发时间，模拟 token 已过期。
	apiTokens[expiredToken] = time.Now().Add(-(apiTokenTTL + time.Second))
	apiTokensMu.Unlock()
	defer func() {
		apiTokensMu.Lock()
		delete(apiTokens, expiredToken)
		apiTokensMu.Unlock()
	}()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/action", nil)
	req.Header.Set("X-CSRF-Token", expiredToken)
	r.ServeHTTP(w, req)

	var resp response.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", w.Body.String(), err)
	}
	if resp.Code == response.CodeSuccess {
		t.Errorf("expired token must be rejected, got code=%d (success)", resp.Code)
	}
}
