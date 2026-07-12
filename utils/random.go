package utils

import (
	"crypto/rand"
	"errors"
	"math/big"
	mrand "math/rand"
	"sync"
	"time"
)

// 本文件提供两类随机数：
//   - 密码学安全（RandStringSecure/RandDigitSecure/RandIntSecure/RandInt64Secure）：基于
//     crypto/rand，不可预测，适用于 token/OTP/重置码/会话 ID/安全 nonce 范围等。
//   - 非密码学（RandInt/RandInt64）：基于 math/rand + sync.Pool，高性能但可预测，
//     仅用于非安全场景（负载均衡、游戏、A/B 分桶等）。
//
// 注意（H1 收紧）：RandString/RandDigit 已移除——字符串随机的用途几乎都是安全场景
// （token/ID/验证码），保留 math/rand 版本会诱导误用。请用 RandStringSecure/RandDigitSecure。

var (
	// #nosec G404 -- math/rand 用于 RandInt/RandInt64 等非密码学函数（性能优先，仅非安全场景：
	// 负载均衡、游戏、A/B 分桶等）。安全场景（token/OTP/重置码）用 RandStringSecure/RandDigitSecure。
	randPool = sync.Pool{
		New: func() any {
			return mrand.New(mrand.NewSource(time.Now().UnixNano()))
		},
	}
)

const (
	letterBytes = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digitBytes  = "0123456789"
)

// ErrRandInvalidLength 长度过大（超过 1<<20，保护熵池）。n<=0 时返回空串而非此错误。
var ErrRandInvalidLength = errors.New("invalid random length")

// RandInt 返回 [min, max) 范围内的随机整数。min==max 时返回 min；max<min 自动交换。
//
// 非密码学安全（math/rand + sync.Pool），仅用于非安全场景（负载均衡、游戏、A/B 分桶等）；
// 不适用于 token/OTP 等安全场景。
func RandInt(min, max int) int {
	if min == max {
		return min
	}
	if max < min {
		min, max = max, min
	}
	r := randPool.Get().(*mrand.Rand)
	defer randPool.Put(r)
	return min + r.Intn(max-min)
}

// RandInt64 返回 [min, max) 范围内的随机 int64（非密码学安全）。min==max 返回 min；max<min 自动交换。
func RandInt64(min, max int64) int64 {
	if min == max {
		return min
	}
	if max < min {
		min, max = max, min
	}
	r := randPool.Get().(*mrand.Rand)
	defer randPool.Put(r)
	return min + r.Int63n(max-min)
}

// RandStringSecure 生成指定长度的密码学安全随机字符串（字母+数字）。
// 基于 crypto/rand，不可预测，适用于 token、会话 ID、API key 等安全场景。
// n<=0 返回空，n 过大（>1<<20）返回错误避免过度消耗熵池。
func RandStringSecure(n int) (string, error) {
	if n <= 0 {
		return "", nil
	}
	if n > 1<<20 {
		return "", ErrRandInvalidLength
	}
	b := make([]byte, n)
	for i := range b {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(letterBytes))))
		if err != nil {
			return "", err
		}
		b[i] = letterBytes[idx.Int64()]
	}
	return string(b), nil
}

// RandDigitSecure 生成指定长度的密码学安全随机数字字符串。
// 基于 crypto/rand，不可预测，适用于 OTP 验证码、密码重置码等安全场景。
// n<=0 返回空，n 过大（>1<<20）返回错误。
func RandDigitSecure(n int) (string, error) {
	if n <= 0 {
		return "", nil
	}
	if n > 1<<20 {
		return "", ErrRandInvalidLength
	}
	b := make([]byte, n)
	for i := range b {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(digitBytes))))
		if err != nil {
			return "", err
		}
		b[i] = digitBytes[idx.Int64()]
	}
	return string(b), nil
}

// RandIntSecure 返回 [min, max) 范围内的密码学安全随机整数。
// 基于 crypto/rand + big.Int 拒绝采样（无偏），适用于安全 nonce 范围、防猜抽奖、密钥分桶等。
// min==max 返回 min；max<min 自动交换。crypto/rand 读取失败时返回错误。
func RandIntSecure(min, max int) (int, error) {
	if min == max {
		return min, nil
	}
	if max < min {
		min, max = max, min
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max-min)))
	if err != nil {
		return 0, err
	}
	return min + int(n.Int64()), nil
}

// RandInt64Secure 返回 [min, max) 范围内的密码学安全随机 int64。
// 基于 crypto/rand + big.Int 拒绝采样（无偏）。min==max 返回 min；max<min 自动交换。
func RandInt64Secure(min, max int64) (int64, error) {
	if min == max {
		return min, nil
	}
	if max < min {
		min, max = max, min
	}
	n, err := rand.Int(rand.Reader, big.NewInt(max-min))
	if err != nil {
		return 0, err
	}
	return min + n.Int64(), nil
}
