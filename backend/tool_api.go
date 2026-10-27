package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	serverNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)
	toolNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type toolServerView struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	BaseURL     string           `json:"base_url"`
	Connected   bool             `json:"connected"`
	HasToken    bool             `json:"has_token"`
	LastError   string           `json:"last_error"`
	ConnectedAt string           `json:"connected_at,omitempty"`
	Tools       []discoveredTool `json:"tools"`
}

type toolServerInput struct {
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	AuthToken string `json:"auth_token"`
}

// requireTeacher 限定管理角色（教师）；学生与未登录都被拒绝。
func requireTeacher(u *User) error {
	if u == nil {
		return fail(401, "请先登录")
	}
	if u.Role != "teacher" {
		return fail(403, "只有教师可以管理工具服务")
	}
	return nil
}

func (a *App) toolServerView(ctx context.Context, row toolServerRow) (toolServerView, error) {
	tools, err := a.discoveredTools(ctx, row.ID)
	if err != nil {
		return toolServerView{}, err
	}
	view := toolServerView{
		ID: row.ID, Name: row.Name, BaseURL: row.BaseURL,
		Connected: row.Connected, HasToken: hasToken(row), LastError: row.LastError, Tools: tools,
	}
	if row.ConnectedAt.Valid {
		view.ConnectedAt = row.ConnectedAt.Time.UTC().Format(time.RFC3339)
	}
	return view, nil
}

// listToolServers 只返回当前教师自己的服务器。
func (a *App) listToolServers(ctx context.Context, ownerID string) ([]toolServerView, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id,owner_id,name,base_url,token_cipher,connected,last_error,connected_at
		FROM tool_servers WHERE owner_id=? ORDER BY created_at, name`, ownerID)
	if err != nil {
		return nil, err
	}
	var list []toolServerRow
	for rows.Next() {
		var row toolServerRow
		if err = rows.Scan(&row.ID, &row.OwnerID, &row.Name, &row.BaseURL, &row.TokenCipher, &row.Connected, &row.LastError, &row.ConnectedAt); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, row)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]toolServerView, 0, len(list))
	for _, row := range list {
		view, err := a.toolServerView(ctx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
}

func validateToolServerInput(input toolServerInput) (toolServerInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.BaseURL = strings.TrimSpace(input.BaseURL)
	input.AuthToken = strings.TrimSpace(input.AuthToken)
	if !serverNamePattern.MatchString(input.Name) {
		return input, fail(400, "服务器名称须以字母或数字开头，只含字母、数字与连字符，最长 64 字符")
	}
	parsed, err := url.Parse(input.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return input, fail(400, "Base URL 必须是 http 或 https 地址，例如 http://mcp-demo:8090")
	}
	return input, nil
}

// POST /api/tool-servers
func (a *App) createToolServer(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := requireTeacher(u); err != nil {
		return err
	}
	var input toolServerInput
	if err := decode(w, r, &input); err != nil {
		return err
	}
	input, err := validateToolServerInput(input)
	if err != nil {
		return err
	}
	cipher := sql.NullString{}
	if input.AuthToken != "" {
		sealed, err := encryptToolToken(input.AuthToken)
		if err != nil {
			return fail(400, err.Error())
		}
		cipher = sql.NullString{String: sealed, Valid: true}
	}
	var exists int
	if err = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM tool_servers WHERE owner_id=? AND name=?`, u.ID, input.Name).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return fail(409, "该名称已被使用")
	}
	id := newID()
	if _, err = a.db.ExecContext(r.Context(), `INSERT INTO tool_servers(id,owner_id,name,base_url,token_cipher) VALUES(?,?,?,?,?)`,
		id, u.ID, input.Name, input.BaseURL, cipher); err != nil {
		return err
	}
	row, err := a.loadToolServer(r.Context(), id, u.ID)
	if err != nil {
		return err
	}
	view, err := a.toolServerView(r.Context(), row)
	if err != nil {
		return err
	}
	writeJSON(w, 201, view)
	return nil
}

// PATCH /api/tool-servers/{id}
func (a *App) updateToolServer(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := requireTeacher(u); err != nil {
		return err
	}
	row, err := a.loadToolServer(r.Context(), r.PathValue("id"), u.ID)
	if err != nil {
		return err
	}
	var input toolServerInput
	if err = decode(w, r, &input); err != nil {
		return err
	}
	input, err = validateToolServerInput(input)
	if err != nil {
		return err
	}
	cipher := row.TokenCipher
	if input.AuthToken != "" {
		sealed, err := encryptToolToken(input.AuthToken)
		if err != nil {
			return fail(400, err.Error())
		}
		cipher = sql.NullString{String: sealed, Valid: true}
	}
	var duplicate int
	if err = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM tool_servers WHERE owner_id=? AND name=? AND id<>?`, u.ID, input.Name, row.ID).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate > 0 {
		return fail(409, "该名称已被使用")
	}
	if _, err = a.db.ExecContext(r.Context(), `UPDATE tool_servers SET name=?, base_url=?, token_cipher=? WHERE id=?`,
		input.Name, input.BaseURL, cipher, row.ID); err != nil {
		return err
	}
	updated, err := a.loadToolServer(r.Context(), row.ID, u.ID)
	if err != nil {
		return err
	}
	view, err := a.toolServerView(r.Context(), updated)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

// DELETE /api/tool-servers/{id}
func (a *App) deleteToolServer(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := requireTeacher(u); err != nil {
		return err
	}
	row, err := a.loadToolServer(r.Context(), r.PathValue("id"), u.ID)
	if err != nil {
		return err
	}
	if _, err = a.db.ExecContext(r.Context(), `DELETE FROM tool_servers WHERE id=?`, row.ID); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

// POST /api/tool-servers/{id}/connect：握手 + 工具发现，两步都成功才替换清单。
func (a *App) connectToolServer(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := requireTeacher(u); err != nil {
		return err
	}
	row, err := a.loadToolServer(r.Context(), r.PathValue("id"), u.ID)
	if err != nil {
		return err
	}
	tools, connectErr := connectTools(r.Context(), row)
	if connectErr != nil {
		// 任一步失败：停在未连接、清空清单、记录可读原因。
		_, _ = a.db.ExecContext(r.Context(), `DELETE FROM tool_server_tools WHERE server_id=?`, row.ID)
		_, _ = a.db.ExecContext(r.Context(), `UPDATE tool_servers SET connected=0, connected_at=NULL, last_error=? WHERE id=?`,
			truncateRunes(connectErr.Error(), 500), row.ID)
		return fail(502, connectErr.Error())
	}
	if err = a.replaceTools(r.Context(), row.ID, tools); err != nil {
		return err
	}
	updated, err := a.loadToolServer(r.Context(), row.ID, u.ID)
	if err != nil {
		return err
	}
	view, err := a.toolServerView(r.Context(), updated)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

// POST /api/tool-servers/{id}/disconnect：清空清单且不可再调用。
func (a *App) disconnectToolServer(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := requireTeacher(u); err != nil {
		return err
	}
	row, err := a.loadToolServer(r.Context(), r.PathValue("id"), u.ID)
	if err != nil {
		return err
	}
	if err = a.clearTools(r.Context(), row.ID); err != nil {
		return err
	}
	updated, err := a.loadToolServer(r.Context(), row.ID, u.ID)
	if err != nil {
		return err
	}
	view, err := a.toolServerView(r.Context(), updated)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

type toolCallView struct {
	ID          string `json:"id"`
	ServerID    string `json:"server_id,omitempty"`
	ServerName  string `json:"server_name"`
	Tool        string `json:"tool"`
	Outcome     string `json:"outcome"`
	Detail      string `json:"detail"`
	ClassID     string `json:"class_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	Turn        int    `json:"turn"`
	TriggerID   string `json:"trigger_user_id,omitempty"`
	TriggerName string `json:"trigger_user"`
	CreatedAt   string `json:"created_at"`
}

// GET /api/tool-calls：只返回当前教师范围内的记录，默认带条数上限。
func (a *App) listToolCalls(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := requireTeacher(u); err != nil {
		return err
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return fail(400, "limit 须为正整数")
		}
		limit = min(parsed, 200)
	}
	query := `SELECT c.id,IFNULL(c.server_id,''),c.server_name,c.tool_name,c.outcome,c.detail,
		IFNULL(c.class_id,''),IFNULL(c.session_id,''),c.turn,IFNULL(c.trigger_user_id,''),IFNULL(u.name,''),c.created_at
		FROM tool_calls c LEFT JOIN users u ON u.id=c.trigger_user_id WHERE c.owner_id=?`
	args := []any{u.ID}
	if outcome := strings.TrimSpace(r.URL.Query().Get("outcome")); outcome != "" {
		if outcome != "success" && outcome != "rejected" && outcome != "error" {
			return fail(400, "outcome 只能是 success、rejected 或 error")
		}
		query += ` AND c.outcome=?`
		args = append(args, outcome)
	}
	if sessionID := strings.TrimSpace(r.URL.Query().Get("session_id")); sessionID != "" {
		query += ` AND c.session_id=?`
		args = append(args, sessionID)
	}
	if serverID := strings.TrimSpace(r.URL.Query().Get("server_id")); serverID != "" {
		query += ` AND c.server_id=?`
		args = append(args, serverID)
	}
	query += ` ORDER BY c.created_at DESC, c.id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := a.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	calls := []toolCallView{}
	for rows.Next() {
		var item toolCallView
		var created time.Time
		if err = rows.Scan(&item.ID, &item.ServerID, &item.ServerName, &item.Tool, &item.Outcome, &item.Detail,
			&item.ClassID, &item.SessionID, &item.Turn, &item.TriggerID, &item.TriggerName, &created); err != nil {
			return err
		}
		item.CreatedAt = created.UTC().Format(time.RFC3339)
		calls = append(calls, item)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	hasMore := false
	if len(calls) > limit {
		hasMore = true
		calls = calls[:limit]
	}
	writeJSON(w, 200, map[string]any{"calls": calls, "has_more": hasMore, "limit": limit})
	return nil
}

// truncateRunes 按字符截断，避免多字节字符被切坏。
func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

var errNoTools = errors.New("没有可用的工具")
