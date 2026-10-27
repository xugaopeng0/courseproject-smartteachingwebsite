package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// 工具服务客户端：只做三件事——拿令牌、握手、发现/调用。协议细节见 mcp-server/。

const (
	toolCallTimeout    = 8 * time.Second
	toolConnectTimeout = 5 * time.Second
	maxCandidateTools  = 4
	maxSkillTools      = 8
	builtinSearchTool  = "knowledge_search"
)

// toolHTTP 是连接工具服务用的客户端：连接与调用都是短请求，统一超时。
var toolHTTP = &http.Client{Timeout: toolCallTimeout}

type toolServerRow struct {
	ID          string
	OwnerID     string
	Name        string
	BaseURL     string
	TokenCipher sql.NullString
	Connected   bool
	LastError   string
	ConnectedAt sql.NullTime
}

type discoveredTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// jsonRPCError 是对方返回的 JSON-RPC 错误。
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error"`
}

// toolTokenKey 由 TOOL_SECRET_KEY 派生 AES-256 密钥；未配置时返回 nil。
func toolTokenKey() []byte {
	secret := strings.TrimSpace(os.Getenv("TOOL_SECRET_KEY"))
	if secret == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// encryptToolToken 用 AES-256-GCM 加密服务器级令牌，密文以 base64 保存。
func encryptToolToken(token string) (string, error) {
	key := toolTokenKey()
	if key == nil {
		return "", errors.New("未配置 TOOL_SECRET_KEY，无法保存服务器级令牌")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(token), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptToolToken 还原服务器级令牌；解不开时返回空串（按未配置处理）。
func decryptToolToken(cipherText string) string {
	key := toolTokenKey()
	if key == nil || cipherText == "" {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(cipherText)
	if err != nil {
		return ""
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(raw) < gcm.NonceSize() {
		return ""
	}
	nonce, payload := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, payload, nil)
	if err != nil {
		return ""
	}
	return string(plain)
}

// serverToken 解析某台服务器实际使用的令牌：服务器密文优先，其次环境变量。
func serverToken(server toolServerRow) string {
	if plain := decryptToolToken(server.TokenCipher.String); plain != "" {
		return plain
	}
	return strings.TrimSpace(os.Getenv("MCP_AUTH_TOKEN"))
}

// hasToken 只用于对外返回「是否已配置令牌」，绝不返回令牌本身。
func hasToken(server toolServerRow) bool {
	return serverToken(server) != ""
}

// mcpCall 向工具服务发一次 JSON-RPC 调用，返回 result 原文。
func mcpCall(ctx context.Context, baseURL, token, method string, params any) (json.RawMessage, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, errors.New("工具服务地址为空")
	}
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/mcp", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := toolHTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("无法连接工具服务：%v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取工具服务响应失败：%v", err)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("工具服务拒绝了访问令牌（HTTP 401）")
	}
	var envelope jsonRPCResponse
	if err = json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("工具服务返回的不是 JSON-RPC 报文（HTTP %d）", response.StatusCode)
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("工具服务返回错误 %d：%s", envelope.Error.Code, envelope.Error.Message)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("工具服务返回状态 %d", response.StatusCode)
	}
	if len(envelope.Result) == 0 {
		return nil, errors.New("工具服务没有返回 result")
	}
	return envelope.Result, nil
}

// connectTools 完成握手与工具发现两步；任何一步失败都返回可读原因。
func connectTools(ctx context.Context, server toolServerRow) ([]discoveredTool, error) {
	token := serverToken(server)
	if token == "" {
		return nil, errors.New("未配置访问令牌：请先填写令牌或设置 MCP_AUTH_TOKEN")
	}
	dialCtx, cancel := context.WithTimeout(ctx, toolConnectTimeout)
	defer cancel()
	if _, err := mcpCall(dialCtx, server.BaseURL, token, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"clientInfo":      map[string]any{"name": "zhixu-teaching", "version": "1.0.0"},
	}); err != nil {
		return nil, fmt.Errorf("握手失败：%v", err)
	}
	raw, err := mcpCall(dialCtx, server.BaseURL, token, "tools/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("工具发现失败：%v", err)
	}
	var payload struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err = json.Unmarshal(raw, &payload); err != nil || len(payload.Tools) == 0 {
		return nil, errors.New("工具清单格式不合法：需要非空的 tools 数组")
	}
	out := make([]discoveredTool, 0, len(payload.Tools))
	seen := map[string]bool{}
	for _, item := range payload.Tools {
		if strings.TrimSpace(item.Name) == "" || seen[item.Name] {
			return nil, errors.New("工具清单格式不合法：存在空名称或重复名称")
		}
		seen[item.Name] = true
		out = append(out, discoveredTool{Name: item.Name, Description: item.Description, InputSchema: item.InputSchema})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// callTool 执行一次工具调用；返回文本内容与是否为工具侧失败。
func callTool(ctx context.Context, server toolServerRow, name string, args map[string]any) (string, bool, error) {
	token := serverToken(server)
	if token == "" {
		return "", false, errors.New("未配置访问令牌")
	}
	callCtx, cancel := context.WithTimeout(ctx, toolCallTimeout)
	defer cancel()
	raw, err := mcpCall(callCtx, server.BaseURL, token, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", false, err
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return "", false, fmt.Errorf("工具返回格式不合法：%v", err)
	}
	texts := make([]string, 0, len(result.Content))
	for _, item := range result.Content {
		if item.Type == "text" && strings.TrimSpace(item.Text) != "" {
			texts = append(texts, item.Text)
		}
	}
	if len(texts) == 0 {
		return "", result.IsError, errors.New("工具没有返回文本内容")
	}
	return strings.Join(texts, "\n"), result.IsError, nil
}

// loadToolServer 读取一台服务器；不存在或不属于该所有者时按「不存在」返回 404。
func (a *App) loadToolServer(ctx context.Context, id, ownerID string) (toolServerRow, error) {
	var row toolServerRow
	err := a.db.QueryRowContext(ctx, `SELECT id,owner_id,name,base_url,token_cipher,connected,last_error,connected_at
		FROM tool_servers WHERE id=? AND owner_id=?`, id, ownerID).
		Scan(&row.ID, &row.OwnerID, &row.Name, &row.BaseURL, &row.TokenCipher, &row.Connected, &row.LastError, &row.ConnectedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return row, fail(404, "工具服务不存在")
	}
	return row, err
}

// discoveredTools 读取某台服务器上次连接时发现的工具。
func (a *App) discoveredTools(ctx context.Context, serverID string) ([]discoveredTool, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT name,description,input_schema FROM tool_server_tools WHERE server_id=? ORDER BY name`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []discoveredTool{}
	for rows.Next() {
		item := discoveredTool{}
		var schema sql.NullString
		if err = rows.Scan(&item.Name, &item.Description, &schema); err != nil {
			return nil, err
		}
		if schema.Valid && schema.String != "" {
			_ = json.Unmarshal([]byte(schema.String), &item.InputSchema)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// replaceTools 在一次事务里整体替换工具清单：因此连续连接两次不会翻倍。
func (a *App) replaceTools(ctx context.Context, serverID string, tools []discoveredTool) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM tool_server_tools WHERE server_id=?`, serverID); err != nil {
		return err
	}
	for _, item := range tools {
		schema := "{}"
		if item.InputSchema != nil {
			if encoded, err := json.Marshal(item.InputSchema); err == nil {
				schema = string(encoded)
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO tool_server_tools(server_id,name,description,input_schema) VALUES(?,?,?,?)`,
			serverID, item.Name, item.Description, schema); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tool_servers SET connected=1, connected_at=NOW(6), last_error='' WHERE id=?`, serverID); err != nil {
		return err
	}
	return tx.Commit()
}

// clearTools 断开连接：清空清单并把状态置为未连接。
func (a *App) clearTools(ctx context.Context, serverID string) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM tool_server_tools WHERE server_id=?`, serverID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tool_servers SET connected=0, connected_at=NULL WHERE id=?`, serverID); err != nil {
		return err
	}
	return tx.Commit()
}
