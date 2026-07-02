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
// 注意: 不应用于密码存储
func MD5(s string) string {
	return MD5Bytes([]byte(s))
}

// MD5Bytes 计算字节数组的 MD5 哈希值
func MD5Bytes(data []byte) string {
	h := md5.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// SHA1 计算字符串的 SHA1 哈希值
// 注意: 不应用于密码存储
func SHA1(s string) string {
	h := sha1.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// SHA256 计算字符串的 SHA256 哈希值
func SHA256(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// SHA256Bytes 计算字节数组的 SHA256 哈希值
func SHA256Bytes(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// HashFile 计算文件的哈希值
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
func Base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// Base64Decode Base64 解码
func Base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// Base64URLEncode URL 安全的 Base64 编码
func Base64URLEncode(data []byte) string {
	return base64.URLEncoding.EncodeToString(data)
}

// Base64URLDecode URL 安全的 Base64 解码
func Base64URLDecode(s string) ([]byte, error) {
	return base64.URLEncoding.DecodeString(s)
}

// Nl2br 将换行符替换为 <br> 标签
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
			// \n\r（罕见顺序）作为一个换行处理；普通 \n 单独处理。
			// （N4：原条件含 r == '\r' 半，但本 case 内 r 恒为 '\n'，该半恒假，已清理。）
			if i+1 < length && runes[i+1] == '\r' {
				buf.WriteString(br)
				continue
			}
			buf.WriteString(br)
		case '\r':
			// \r\n 由上面的 \n 分支处理（\r 在此跳过）；单独 \r 作为一个换行。
			if i+1 < length && runes[i+1] == '\n' {
				continue
			}
			buf.WriteString(br)
		default:
			buf.WriteRune(r)
		}
	}
	return buf.String()
}
