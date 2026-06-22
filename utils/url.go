package utils

import (
	"net/url"
)

// URLBuilder URL 构建器
type URLBuilder struct {
	u     *url.URL
	query url.Values
}

// ParseURL 解析 URL 字符串
func ParseURL(rawURL string) (*URLBuilder, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	return &URLBuilder{
		u:     u,
		query: u.Query(),
	}, nil
}

// AddQuery 添加单个查询参数
func (b *URLBuilder) AddQuery(key, value string) *URLBuilder {
	b.query.Add(key, value)
	return b
}

// AddQueries 批量添加查询参数
func (b *URLBuilder) AddQueries(params map[string]string) *URLBuilder {
	for k, v := range params {
		b.query.Set(k, v)
	}
	return b
}

// SetQuery 设置查询参数（覆盖同名参数）
func (b *URLBuilder) SetQuery(key, value string) *URLBuilder {
	b.query.Set(key, value)
	return b
}

// SetPath 设置路径
func (b *URLBuilder) SetPath(path string) *URLBuilder {
	b.u.Path = path
	return b
}

// SetScheme 设置协议（http/https）
func (b *URLBuilder) SetScheme(scheme string) *URLBuilder {
	b.u.Scheme = scheme
	return b
}

// SetHost 设置主机
func (b *URLBuilder) SetHost(host string) *URLBuilder {
	b.u.Host = host
	return b
}

// Build 构建最终的 URL
func (b *URLBuilder) Build() *url.URL {
	b.u.RawQuery = b.query.Encode()
	return b.u
}

// String 返回 URL 字符串
func (b *URLBuilder) String() string {
	return b.Build().String()
}

// URLEncode URL 编码
func URLEncode(s string) string {
	return url.QueryEscape(s)
}

// URLDecode URL 解码
func URLDecode(s string) (string, error) {
	return url.QueryUnescape(s)
}
