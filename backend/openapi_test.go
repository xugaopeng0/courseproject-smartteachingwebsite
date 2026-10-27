package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 契约结构：只解析一致性检查用得到的字段。
type openAPIDoc struct {
	OpenAPI string `json:"openapi"`
	Info    struct {
		Title string `json:"title"`
	} `json:"info"`
	Paths map[string]map[string]struct {
		Security []map[string][]string `json:"security"`
		Summary  string                `json:"summary"`
		Params   []map[string]any      `json:"parameters"`
	} `json:"paths"`
	Components struct {
		Schemas         map[string]any            `json:"schemas"`
		SecuritySchemes map[string]any            `json:"securitySchemes"`
		Parameters      map[string]map[string]any `json:"parameters"`
	} `json:"components"`
}

// resolveParameter 解析参数里的本地 $ref（`#/components/parameters/<name>`）。
func (doc openAPIDoc) resolveParameter(t *testing.T, raw map[string]any) map[string]any {
	t.Helper()
	ref, ok := raw["$ref"].(string)
	if !ok {
		return raw
	}
	name := strings.TrimPrefix(ref, "#/components/parameters/")
	resolved, ok := doc.Components.Parameters[name]
	if !ok {
		t.Fatalf("契约引用了不存在的参数组件 %s", ref)
	}
	return resolved
}

func loadContract(t *testing.T) openAPIDoc {
	t.Helper()
	var doc openAPIDoc
	if err := json.Unmarshal(openapiContract, &doc); err != nil {
		t.Fatalf("嵌入的契约无法解析: %v", err)
	}
	return doc
}

// 任务 3.1：实现与契约的操作集合必须双向一致。
func TestOpenAPIMatchesRoutes(t *testing.T) {
	a, _ := testApp(t)
	doc := loadContract(t)
	if !strings.HasPrefix(doc.OpenAPI, "3.1") {
		t.Fatalf("应为 OpenAPI 3.1，实际 %q", doc.OpenAPI)
	}
	implemented := map[string]bool{}
	for _, spec := range a.routes() {
		implemented[spec.Method+" "+spec.Pattern] = true
	}
	documented := map[string]bool{}
	for path, operations := range doc.Paths {
		for method := range operations {
			documented[strings.ToUpper(method)+" "+path] = true
		}
	}
	missing, extra := []string{}, []string{}
	for key := range implemented {
		if !documented[key] {
			missing = append(missing, key)
		}
	}
	for key := range documented {
		if !implemented[key] {
			extra = append(extra, key)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("契约与路由不一致\n  实现有而文档缺: %v\n  文档有而实现无: %v", missing, extra)
	}
	if len(implemented) != 48 {
		t.Fatalf("操作数量变化时应同步更新本断言：实际 %d 个", len(implemented))
	}
}

// 任务 3.2：受保护操作必须标注 security，匿名操作必须没有。
func TestOpenAPISecurityMatchesRoutes(t *testing.T) {
	a, _ := testApp(t)
	doc := loadContract(t)
	if len(doc.Components.SecuritySchemes) == 0 {
		t.Fatal("契约必须声明安全方案")
	}
	for _, spec := range a.routes() {
		operations, ok := doc.Paths[spec.Pattern]
		if !ok {
			t.Fatalf("契约缺少路径 %s", spec.Pattern)
		}
		operation, ok := operations[strings.ToLower(spec.Method)]
		if !ok {
			t.Fatalf("契约缺少操作 %s %s", spec.Method, spec.Pattern)
		}
		key := spec.Method + " " + spec.Pattern
		switch {
		case spec.Auth && len(operation.Security) == 0:
			t.Fatalf("%s 需要登录，但契约没有标注 security", key)
		case !spec.Auth && len(operation.Security) > 0:
			t.Fatalf("%s 允许匿名访问，但契约标注了 security", key)
		}
		if strings.TrimSpace(operation.Summary) == "" {
			t.Fatalf("%s 缺少 summary", key)
		}
	}
}

// 任务 3.3：契约里的路径参数必须与实现中的占位符一致。
func TestOpenAPIPathParametersExist(t *testing.T) {
	a, _ := testApp(t)
	doc := loadContract(t)
	checked := 0
	for _, spec := range a.routes() {
		operation := doc.Paths[spec.Pattern][strings.ToLower(spec.Method)]
		inPattern := map[string]bool{}
		for _, segment := range strings.Split(spec.Pattern, "/") {
			if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
				inPattern[strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}")] = true
			}
		}
		declared := map[string]bool{}
		for _, raw := range operation.Params {
			param := doc.resolveParameter(t, raw)
			if param["in"] != "path" {
				continue
			}
			name, _ := param["name"].(string)
			declared[name] = true
			if required, _ := param["required"].(bool); !required {
				t.Fatalf("%s %s 的路径参数 %s 必须标记 required", spec.Method, spec.Pattern, name)
			}
		}
		for name := range inPattern {
			if !declared[name] {
				t.Fatalf("%s %s 未声明路径参数 %s", spec.Method, spec.Pattern, name)
			}
		}
		for name := range declared {
			if !inPattern[name] {
				t.Fatalf("%s %s 声明的路径参数 %s 在实现中不存在", spec.Method, spec.Pattern, name)
			}
		}
		checked++
	}
	if checked != len(a.routes()) {
		t.Fatalf("检查数量不符：%d", checked)
	}
}

// 契约副本不得过期：docs/openapi.json 是唯一来源，backend/openapi.json 是字节级副本。
func TestOpenAPICopyIsCurrent(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "docs", "openapi.json"))
	if err != nil {
		t.Fatalf("读取 docs/openapi.json 失败: %v", err)
	}
	if string(source) != string(openapiContract) {
		t.Fatal("backend/openapi.json 与 docs/openapi.json 不一致，请运行 node scripts/sync-openapi.mjs")
	}
}

// 任务 2.1 / 2.4：契约与文档页面由本服务提供，且页面不依赖外网资源。
func TestApiDocumentationEndpoints(t *testing.T) {
	a, _ := testApp(t)
	handler := a.handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil))
	if recorder.Code != 200 {
		t.Fatalf("契约端点应 200，实际 %d", recorder.Code)
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("契约端点 Content-Type 不符：%s", recorder.Header().Get("Content-Type"))
	}
	if recorder.Body.String() != string(openapiContract) {
		t.Fatal("契约端点返回的内容与嵌入的契约不一致")
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "SwaggerUIBundle") {
		t.Fatalf("文档页面应可打开：%d", recorder.Code)
	}
	page := recorder.Body.String()
	if strings.Contains(page, "http://") || strings.Contains(page, "https://") {
		t.Fatalf("文档页面不得引用外部地址：%s", page)
	}

	for _, asset := range []string{"swagger-ui.css", "swagger-ui-bundle.js"} {
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/docs/"+asset, nil))
		if recorder.Code != 200 || recorder.Body.Len() == 0 {
			t.Fatalf("资源 %s 应可读取：%d", asset, recorder.Code)
		}
	}

	// 目录穿越与不存在的资源都按未找到处理。
	for _, path := range []string{"/api/docs/..%2Fopenapi.json", "/api/docs/nope.js"} {
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != 404 {
			t.Fatalf("%s 应 404，实际 %d", path, recorder.Code)
		}
	}
}
