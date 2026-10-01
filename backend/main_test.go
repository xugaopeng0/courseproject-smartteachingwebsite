package main

import (
	"net/http"
	"testing"
)

// 回归：用 127.0.0.1 打开站点而 APP_ORIGIN 写的是 localhost 时，登录不能被判成跨站。
func TestSameOrigin(t *testing.T) {
	a := &App{origin: "http://localhost:8080"}
	cases := []struct {
		name   string
		host   string
		origin string
		fetch  string
		want   bool
	}{
		{"配置的来源", "localhost:8080", "http://localhost:8080", "", true},
		{"本机回环地址", "127.0.0.1:8080", "http://127.0.0.1:8080", "", true},
		{"IPv6 回环地址", "[::1]:8080", "http://[::1]:8080", "", true},
		{"局域网地址", "192.168.1.20:8080", "http://192.168.1.20:8080", "", true},
		{"没有 Origin（curl 或同源表单）", "localhost:8080", "", "", true},
		{"同源请求头", "localhost:8080", "http://localhost:8080", "same-origin", true},
		{"他站来源", "localhost:8080", "http://evil.example", "", false},
		{"跨站标注", "localhost:8080", "http://localhost:8080", "cross-site", false},
		{"主机不同", "localhost:8080", "http://127.0.0.1:8080", "", false},
		{"代理去掉端口", "127.0.0.1", "http://127.0.0.1:8080", "", true},
		{"端口不同但主机相同", "127.0.0.1:9090", "http://127.0.0.1:8080", "", true},
		{"Origin 无法解析", "localhost:8080", "::::", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &http.Request{Host: tc.host, Header: http.Header{}}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.fetch != "" {
				r.Header.Set("Sec-Fetch-Site", tc.fetch)
			}
			if got := a.sameOrigin(r); got != tc.want {
				t.Fatalf("sameOrigin(host=%q origin=%q fetch=%q) = %v，期望 %v", tc.host, tc.origin, tc.fetch, got, tc.want)
			}
		})
	}
}

// 配置里可以列出多个来源（反向代理或自定义域名）。
func TestSameOriginAllowsListedOrigins(t *testing.T) {
	a := &App{origin: "http://localhost:8080, https://zhixu.example"}
	for _, origin := range []string{"http://localhost:8080", "https://zhixu.example"} {
		r := &http.Request{Host: "api.internal:8081", Header: http.Header{}}
		r.Header.Set("Origin", origin)
		if !a.sameOrigin(r) {
			t.Fatalf("配置中的来源 %s 应被接受", origin)
		}
	}
	r := &http.Request{Host: "api.internal:8081", Header: http.Header{}}
	r.Header.Set("Origin", "https://other.example")
	if a.sameOrigin(r) {
		t.Fatal("未列出的来源应被拒绝")
	}
}
