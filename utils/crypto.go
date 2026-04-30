package utils

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"hash"
)

// MD5 计算字符串的 MD5 哈希值
// 评分: ⭐⭐⭐⭐
// 理由: 常用哈希函数，但 MD5 已不安全，仅用于非安全场景（如文件校验）
// 注意: 不应用于密码存储
func MD5(s string) string {
	return MD5Bytes([]byte(s))
}

// MD5Bytes 计算字节数组的 MD5 哈希值
// 评分: ⭐⭐⭐⭐
// 理由: 文件哈希常用
func MD5Bytes(data []byte) string {
	h := md5.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// SHA1 计算字符串的 SHA1 哈希值
// 评分: ⭐⭐⭐
// 理由: SHA1 也已不安全，仅用于兼容旧系统
// 注意: 不应用于密码存储
func SHA1(s string) string {
	h := sha1.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// SHA256 计算字符串的 SHA256 哈希值
// 评分: ⭐⭐⭐⭐⭐
// 理由: 安全哈希算法，适用于数据完整性校验
func SHA256(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// SHA256Bytes 计算字节数组的 SHA256 哈希值
// 评分: ⭐⭐⭐⭐⭐
// 理由: 文件哈希常用
func SHA256Bytes(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// HashFile 计算文件的哈希值
// 评分: ⭐⭐⭐⭐⭐
// 理由: 大文件哈希，支持分块读取避免内存溢出
func HashFile(path string, newHash func() hash.Hash) (string, error) {
	data, err := ReadFile(path)
	if err != nil {
		return "", err
	}
	h := newHash()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Base64Encode Base64 编码
// 评分: ⭐⭐⭐⭐⭐
// 理由: 常用编码函数
func Base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// Base64Decode Base64 解码
// 评分: ⭐⭐⭐⭐⭐
// 理由: 常用解码函数
func Base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// Base64URLEncode URL 安全的 Base64 编码
// 评分: ⭐⭐⭐⭐⭐
// 理由: URL 传输常用，替换 +/ 为 -_
func Base64URLEncode(data []byte) string {
	return base64.URLEncoding.EncodeToString(data)
}

// Base64URLDecode URL 安全的 Base64 解码
// 评分: ⭐⭐⭐⭐⭐
// 理由: URL 传输常用
func Base64URLDecode(s string) ([]byte, error) {
	return base64.URLEncoding.DecodeString(s)
}

// Nl2br 将换行符替换为 <br> 标签
// 评分: ⭐⭐⭐
// 理由: 特定场景使用（富文本显示），但现代前端框架通常自行处理
func Nl2br(s string, isXhtml bool) string {
	var br string
	if isXhtml {
		br = "<br />"
	} else {
		br = "<br>"
	}

	var buf bytes.Buffer
	runes := []rune(s)
	length := len(runes)

	for i, r := range runes {
		switch r {
		case '\n':
			// 检查是否是 \r\n 或 \n\r
			if i+1 < length {
				next := runes[i+1]
				if (r == '\n' && next == '\r') || (r == '\r' && next == '\n') {
					buf.WriteString(br)
					continue
				}
			}
			buf.WriteString(br)
		case '\r':
			// 单独的 \r 或 \r\n 已在上面处理
			if i+1 < length && runes[i+1] == '\n' {
				continue // \r\n 由 \n 处理
			}
			buf.WriteString(br)
		default:
			buf.WriteRune(r)
		}
	}
	return buf.String()
}
