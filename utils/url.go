package utils

import (
	"net/url"
)

// URLBuilder URL 构建器
// 评分: ⭐⭐⭐⭐⭐
// 理由: 链式调用设计优雅，便于构建复杂 URL
type URLBuilder struct {
	u     *url.URL
	query url.Values
}

// ParseURL 解析 URL 字符串
// 评分: ⭐⭐⭐⭐⭐
// 理由: 创建 URLBuilder 的入口函数
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
// 评分: ⭐⭐⭐⭐⭐
// 理由: 链式调用，支持多次添加
func (b *URLBuilder) AddQuery(key, value string) *URLBuilder {
	b.query.Add(key, value)
	return b
}

// AddQueries 批量添加查询参数
// 评分: ⭐⭐⭐⭐⭐
// 理由: 批量添加更高效
func (b *URLBuilder) AddQueries(params map[string]string) *URLBuilder {
	for k, v := range params {
		b.query.Set(k, v)
	}
	return b
}

// SetQuery 设置查询参数（覆盖同名参数）
// 评分: ⭐⭐⭐⭐⭐
// 理由: 区别于 Add，覆盖同名参数
func (b *URLBuilder) SetQuery(key, value string) *URLBuilder {
	b.query.Set(key, value)
	return b
}

// SetPath 设置路径
// 评分: ⭐⭐⭐⭐
// 理由: 动态修改路径
func (b *URLBuilder) SetPath(path string) *URLBuilder {
	b.u.Path = path
	return b
}

// SetScheme 设置协议（http/https）
// 评分: ⭐⭐⭐⭐
// 理由: 协议切换
func (b *URLBuilder) SetScheme(scheme string) *URLBuilder {
	b.u.Scheme = scheme
	return b
}

// SetHost 设置主机
// 评分: ⭐⭐⭐⭐
// 理由: 域名切换
func (b *URLBuilder) SetHost(host string) *URLBuilder {
	b.u.Host = host
	return b
}

// Build 构建最终的 URL
// 评分: ⭐⭐⭐⭐⭐
// 理由: 返回标准库 url.URL
func (b *URLBuilder) Build() *url.URL {
	b.u.RawQuery = b.query.Encode()
	return b.u
}

// String 返回 URL 字符串
// 评分: ⭐⭐⭐⭐⭐
// 理由: 最常用的输出方法
func (b *URLBuilder) String() string {
	return b.Build().String()
}

// URLEncode URL 编码
// 评分: ⭐⭐⭐⭐⭐
// 理由: 常用编码函数
func URLEncode(s string) string {
	return url.QueryEscape(s)
}

// URLDecode URL 解码
// 评分: ⭐⭐⭐⭐⭐
// 理由: 常用解码函数
func URLDecode(s string) (string, error) {
	return url.QueryUnescape(s)
}
