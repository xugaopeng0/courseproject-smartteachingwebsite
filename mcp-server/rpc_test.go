package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "test-token-1234567890"

func callRPC(t *testing.T, body string, token string, method string) *httptest.ResponseRecorder {
	t.Helper()
	mux := newMux(testToken)
	request := httptest.NewRequest(method, "/mcp", strings.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	return recorder
}

func decodeResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("响应不是合法 JSON：%v\n%s", err, recorder.Body.String())
	}
	return payload
}

func parseRPCError(t *testing.T, payload map[string]any) (int, string) {
	t.Helper()
	raw, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("期望 error 对象，实际 %v", payload)
	}
	code, _ := raw["code"].(float64)
	message, _ := raw["message"].(string)
	return int(code), message
}

// 任务 1.2：四种错误码与通知语义。
func TestProtocolErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
		code int
	}{
		{"非法 JSON", `{not json`, http.StatusBadRequest, codeParseError},
		{"未知方法", `{"jsonrpc":"2.0","id":1,"method":"tools/nope"}`, http.StatusOK, codeMethodNotFound},
		{"非法参数", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"unit_convert","arguments":{"value":"x","from":"m","to":"km"}}}`, http.StatusOK, codeInvalidParams},
		{"未知工具", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope"}}`, http.StatusOK, codeInvalidParams},
		{"缺少 jsonrpc", `{"id":4,"method":"initialize"}`, http.StatusBadRequest, codeInvalidRequest},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			recorder := callRPC(t, item.body, testToken, http.MethodPost)
			if recorder.Code != item.want {
				t.Fatalf("HTTP 状态应为 %d，实际 %d", item.want, recorder.Code)
			}
			code, message := parseRPCError(t, decodeResponse(t, recorder))
			if code != item.code {
				t.Fatalf("错误码应为 %d，实际 %d（%s）", item.code, code, message)
			}
			if strings.TrimSpace(message) == "" {
				t.Fatal("错误信息不能为空")
			}
		})
	}
}

// 任务 1.3：initialize 的结构。
func TestInitialize(t *testing.T) {
	recorder := callRPC(t, `{"jsonrpc":"2.0","id":"a","method":"initialize","params":{}}`, testToken, http.MethodPost)
	payload := decodeResponse(t, recorder)
	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 result：%v", payload)
	}
	if result["protocolVersion"] != "2025-06-18" {
		t.Fatalf("协议版本不符：%v", result["protocolVersion"])
	}
	capabilities, _ := result["capabilities"].(map[string]any)
	if _, ok := capabilities["tools"]; !ok {
		t.Fatalf("缺少 tools 能力：%v", capabilities)
	}
	serverInfo, _ := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != "campus-mcp-tools" || serverInfo["version"] == "" {
		t.Fatalf("serverInfo 不完整：%v", serverInfo)
	}
	if _, exists := result["tools"]; exists {
		t.Fatal("initialize 不应返回工具清单")
	}
}

// 任务 1.3：tools/list 的字段齐全、顺序稳定。
func TestToolsList(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	first := decodeResponse(t, callRPC(t, body, testToken, http.MethodPost))
	second := decodeResponse(t, callRPC(t, body, testToken, http.MethodPost))
	list := first["result"].(map[string]any)["tools"].([]any)
	if len(list) != 2 {
		t.Fatalf("应暴露 2 个工具，实际 %d", len(list))
	}
	names := make([]string, 0, len(list))
	for _, item := range list {
		entry := item.(map[string]any)
		for _, field := range []string{"name", "description", "inputSchema"} {
			if _, ok := entry[field]; !ok {
				t.Fatalf("工具缺少字段 %s：%v", field, entry)
			}
		}
		description := entry["description"].(string)
		if !strings.Contains(description, "不") {
			t.Fatalf("说明应写明不适用的边界：%s", description)
		}
		schema := entry["inputSchema"].(map[string]any)
		if schema["type"] != "object" {
			t.Fatalf("输入结构应为 object：%v", schema)
		}
		names = append(names, entry["name"].(string))
	}
	if names[0] != "formula_hint" || names[1] != "unit_convert" {
		t.Fatalf("工具顺序应为按名称排序，实际 %v", names)
	}
	if second["result"].(map[string]any)["tools"].([]any)[0].(map[string]any)["name"] != names[0] {
		t.Fatal("两次 tools/list 的顺序应一致")
	}
}

// 任务 2.1：令牌校验。
func TestAuthorization(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	if recorder := callRPC(t, body, "", http.MethodPost); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("无令牌应 401，实际 %d", recorder.Code)
	}
	if recorder := callRPC(t, body, "wrong-token", http.MethodPost); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("错误令牌应 401，实际 %d", recorder.Code)
	}
	recorder := callRPC(t, body, testToken, http.MethodPost)
	if recorder.Code != http.StatusOK {
		t.Fatalf("正确令牌应 200，实际 %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), testToken) {
		t.Fatal("响应体中不应出现令牌")
	}
	// 未授权的响应也不能泄露工具名。
	unauthorized := callRPC(t, body, "", http.MethodPost).Body.String()
	if strings.Contains(unauthorized, "formula_hint") || strings.Contains(unauthorized, "unit_convert") {
		t.Fatalf("未授权响应不应包含工具名：%s", unauthorized)
	}
}

// 任务 1.2：通知返回 202 空响应；非 POST 返回 405。
func TestTransportRules(t *testing.T) {
	recorder := callRPC(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, testToken, http.MethodPost)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("通知应返回 202，实际 %d", recorder.Code)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("通知不应返回响应体：%s", recorder.Body.String())
	}
	get := callRPC(t, "", testToken, http.MethodGet)
	if get.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp 应返回 405，实际 %d", get.Code)
	}
	if _, message := parseRPCError(t, decodeResponse(t, get)); !strings.Contains(message, "POST") {
		t.Fatalf("405 的错误信息应说明只接受 POST：%s", message)
	}
}

// 任务 1.1：/health 不需要令牌。
func TestHealth(t *testing.T) {
	mux := newMux(testToken)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("/health 应 200，实际 %d", recorder.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "ok" || payload["tools"].(float64) != 2 {
		t.Fatalf("/health 内容不符：%v", payload)
	}
}

// 任务 1.1：缺少令牌时拒绝启动。
func TestRunRefusesWithoutToken(t *testing.T) {
	t.Setenv("MCP_AUTH_TOKEN", "")
	if err := run(); err == nil || !strings.Contains(err.Error(), "MCP_AUTH_TOKEN") {
		t.Fatalf("缺少令牌时应拒绝启动并说明原因，实际 %v", err)
	}
}
