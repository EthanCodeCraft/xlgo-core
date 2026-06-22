package utils

import (
	"math/rand"
	"sync"
	"time"
)

var (
	randPool = sync.Pool{
		New: func() any {
			return rand.New(rand.NewSource(time.Now().UnixNano()))
		},
	}
)

const (
	letterBytes   = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digitBytes    = "0123456789"
	letterIdxBits = 6                    // 6 bits to represent a letter index (0-63)
	letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax  = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
)

// RandString 生成指定长度的随机字符串（字母+数字）
func RandString(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	r := randPool.Get().(*rand.Rand)
	defer randPool.Put(r)

	for i, cache, remain := n-1, r.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = r.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterBytes) {
			b[i] = letterBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}
	return string(b)
}

// RandDigit 生成指定长度的随机数字字符串
func RandDigit(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	r := randPool.Get().(*rand.Rand)
	defer randPool.Put(r)

	for i, cache, remain := n-1, r.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = r.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(digitBytes) {
			b[i] = digitBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}
	return string(b)
}

// RandInt 返回 [min, max) 范围内的随机整数
func RandInt(min, max int) int {
	if min == max {
		return min
	}
	if max < min {
		min, max = max, min
	}
	r := randPool.Get().(*rand.Rand)
	defer randPool.Put(r)
	return min + r.Intn(max-min)
}

// RandInt64 返回 [min, max) 范围内的随机 int64
func RandInt64(min, max int64) int64 {
	if min == max {
		return min
	}
	if max < min {
		min, max = max, min
	}
	r := randPool.Get().(*rand.Rand)
	defer randPool.Put(r)
	return min + r.Int63n(max-min)
}
