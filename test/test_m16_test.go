package test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestExecuteReturnsResponse(t *testing.T) {
	router := SetupRouter()
	router.POST("/users", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"name": "张三"}})
	})

	resp := POST(router, "/users").WithJSON(gin.H{"name": "张三"}).Execute()
	resp.AssertOK(t)
	resp.AssertJSONContains(t, "data.name", "张三")
}

func TestRequestExecuteRecorderKeepsRawRecorder(t *testing.T) {
	router := SetupRouter()
	router.GET("/ping", func(c *gin.Context) {
		c.String(http.StatusCreated, "pong")
	})

	recorder := GET(router, "/ping").ExecuteRecorder()
	if _, ok := any(recorder).(*httptest.ResponseRecorder); !ok {
		t.Fatalf("ExecuteRecorder 应返回原始 ResponseRecorder")
	}
	if recorder.Code != http.StatusCreated {
		t.Fatalf("状态码错误: 期望 %d, 实际 %d", http.StatusCreated, recorder.Code)
	}
}

func TestMockCacheCopiesBytes(t *testing.T) {
	cache := NewMockCache()
	input := []byte("abc")
	cache.Set("k", input)
	input[0] = 'x'

	got, ok := cache.Get("k")
	if !ok {
		t.Fatalf("缓存不存在")
	}
	if string(got) != "abc" {
		t.Fatalf("缓存被外部输入污染: %q", got)
	}

	got[0] = 'y'
	gotAgain, _ := cache.Get("k")
	if string(gotAgain) != "abc" {
		t.Fatalf("缓存被返回值污染: %q", gotAgain)
	}
}

func TestMocksConcurrentAccess(t *testing.T) {
	db := NewMockDB()
	cache := NewMockCache()

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			db.Set(i, i)
			_, _ = db.Get(i)
			cache.Set("k", []byte{byte(i)})
			_, _ = cache.Get("k")
		}(i)
	}
	wg.Wait()
}

func TestMockStorageRejectsNilAndLargeInput(t *testing.T) {
	storage := NewMockStorage()
	if _, err := storage.Upload(nil, "avatars"); err == nil {
		t.Fatalf("nil 文件应返回错误")
	}

	large := bytes.Repeat([]byte{'x'}, int(mockStorageMaxBytes)+1)
	if _, err := storage.UploadFromBytes(large, "large.bin", "files"); !errors.Is(err, ErrMockStorageTooLarge) {
		t.Fatalf("超大文件错误不符合预期: %v", err)
	}
}
