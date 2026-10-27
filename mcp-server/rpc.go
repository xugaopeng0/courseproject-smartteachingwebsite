package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// JSON-RPC 2.0 错误码。任务要求的四类错误与标准错误码一致。
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
	codeUnauthorized   = -32001
)

const maxBodyBytes = 1 << 20 // 1 MiB 足够，防止超大请求体

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// toolResult 是 tools/call 的成功结果。工具自身失败用 IsError 标记，
// 与协议层错误（error 对象）区分开。
type toolResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// server 持有令牌与工具清单。
type server struct {
	token string
	tools []tool
}

func newServer(token string) *server {
	return &server{token: token, tools: tools()}
}

func newMux(token string) *http.ServeMux {
	s := newServer(token)
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleRPC)
	mux.HandleFunc("/health", s.handleHealth)
	return mux
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	// 健康检查不返回工具信息，只回计数，供 Compose 健康检查与编排探活。
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "tools": len(s.tools)})
}

func (s *server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeRPC(w, http.StatusMethodNotAllowed, nil, nil, &rpcError{codeInvalidRequest, "RPC 入口只接受 POST 请求"})
		return
	}
	if !s.authorized(r) {
		writeRPC(w, http.StatusUnauthorized, nil, nil, &rpcError{codeUnauthorized, "未授权：缺少或错误的访问令牌"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeRPC(w, http.StatusBadRequest, nil, nil, &rpcError{codeParseError, "读取请求体失败"})
		return
	}
	var request rpcRequest
	if err = json.Unmarshal(body, &request); err != nil {
		writeRPC(w, http.StatusBadRequest, nil, nil, &rpcError{codeParseError, "请求体不是合法 JSON"})
		return
	}
	if request.JSONRPC != "2.0" || strings.TrimSpace(request.Method) == "" {
		writeRPC(w, http.StatusBadRequest, request.ID, nil, &rpcError{codeInvalidRequest, "请求不符合 JSON-RPC 2.0：需要 jsonrpc 2.0 与 method"})
		return
	}
	// 通知（没有 id）不需要回包。
	if len(request.ID) == 0 || string(request.ID) == "null" {
		if request.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeRPC(w, http.StatusBadRequest, nil, nil, &rpcError{codeInvalidRequest, "通知只支持 notifications/initialized"})
		return
	}
	result, rpcErr := s.dispatch(r.Context(), request)
	if rpcErr != nil {
		// JSON-RPC 规定协议层错误仍以 HTTP 200 返回，错误信息在报文的 error 字段里。
		writeRPC(w, http.StatusOK, request.ID, nil, rpcErr)
		return
	}
	writeRPC(w, http.StatusOK, request.ID, result, nil)
}

func (s *server) dispatch(ctx context.Context, request rpcRequest) (any, *rpcError) {
	switch request.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "campus-mcp-tools", "version": "1.0.0"},
		}, nil
	case "tools/list":
		list := make([]map[string]any, 0, len(s.tools))
		for _, item := range s.tools {
			list = append(list, map[string]any{
				"name":        item.Name,
				"description": item.Description,
				"inputSchema": item.InputSchema,
			})
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		return s.callTool(ctx, request.Params)
	default:
		return nil, &rpcError{codeMethodNotFound, fmt.Sprintf("不支持的方法 %q：只提供 initialize、tools/list、tools/call", request.Method)}
	}
}

func (s *server) callTool(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, &rpcError{codeInvalidParams, "params 不是合法对象"}
		}
	}
	name := strings.TrimSpace(params.Name)
	var selected *tool
	for index := range s.tools {
		if s.tools[index].Name == name {
			selected = &s.tools[index]
			break
		}
	}
	if selected == nil {
		return nil, &rpcError{codeInvalidParams, fmt.Sprintf("未知工具 %q：可用工具为 %s", name, toolNames(s.tools))}
	}
	if err := validateArguments(selected.InputSchema, params.Arguments); err != nil {
		return nil, &rpcError{codeInvalidParams, fmt.Sprintf("工具 %s 的参数不合法：%v", name, err)}
	}
	text, err := selected.Call(ctx, params.Arguments)
	if err != nil {
		// 工具执行失败是业务结果，不是协议错误。
		return toolResult{Content: []contentItem{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}
	return toolResult{Content: []contentItem{{Type: "text", Text: text}}, IsError: false}, nil
}

func toolNames(list []tool) string {
	names := make([]string, 0, len(list))
	for _, item := range list {
		names = append(names, item.Name)
	}
	return strings.Join(names, "、")
}

// authorized 校验 Authorization: Bearer <token>，使用恒定时间比较。
func (s *server) authorized(r *http.Request) bool {
	header := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1
}

func writeRPC(w http.ResponseWriter, status int, id json.RawMessage, result any, rpcErr *rpcError) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	response := rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr}
	writeJSON(w, status, response)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("写响应失败: %v", err)
	}
}

var errMissingToken = errors.New("缺少 MCP_AUTH_TOKEN：工具服务拒绝在无令牌的情况下启动（令牌只经环境变量注入）")
