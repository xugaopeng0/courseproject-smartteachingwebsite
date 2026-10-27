package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// 技能的工具项：内置知识库检索（knowledge_search）或 `工具名@服务器名` 引用。

// parseToolRef 解析 `工具名@服务器名`。内置检索不是这种格式，由调用方单独处理。
func parseToolRef(ref string) (string, string, error) {
	trimmed := strings.TrimSpace(ref)
	at := strings.Index(trimmed, "@")
	if at <= 0 || at != strings.LastIndex(trimmed, "@") || at == len(trimmed)-1 {
		return "", "", fmt.Errorf("工具引用 %q 格式不正确，应为 工具名@服务器名", trimmed)
	}
	tool, server := trimmed[:at], trimmed[at+1:]
	if !toolNamePattern.MatchString(tool) {
		return "", "", fmt.Errorf("工具名 %q 不合法：须以小写字母开头，只含小写字母、数字与下划线", tool)
	}
	if !serverNamePattern.MatchString(server) {
		return "", "", fmt.Errorf("服务器名 %q 不合法：须以字母或数字开头，只含字母、数字与连字符", server)
	}
	return tool, server, nil
}

// normalizeSkillTools 校验并去重技能的工具项：
// 格式 → 服务器属于请求者 → 已连接 → 工具在发现清单内。平台技能只允许内置检索。
func (a *App) normalizeSkillTools(ctx context.Context, ownerID string, refs []string, allowExternal bool) ([]string, error) {
	if len(refs) > maxSkillTools {
		return nil, fail(400, fmt.Sprintf("一个技能最多引用 %d 个工具", maxSkillTools))
	}
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range refs {
		ref := strings.TrimSpace(raw)
		if ref == "" || seen[ref] {
			continue
		}
		if ref == builtinSearchTool {
			seen[ref] = true
			out = append(out, ref)
			continue
		}
		if !allowExternal {
			return nil, fail(400, "平台技能只能使用内置知识库检索，不能引用外部工具服务")
		}
		tool, serverName, err := parseToolRef(ref)
		if err != nil {
			return nil, fail(400, err.Error())
		}
		var serverID string
		var connected bool
		err = a.db.QueryRowContext(ctx, `SELECT id,connected FROM tool_servers WHERE owner_id=? AND name=?`, ownerID, serverName).
			Scan(&serverID, &connected)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fail(400, fmt.Sprintf("技能引用的工具 %s 未找到：没有名为 %s 的工具服务", ref, serverName))
		}
		if err != nil {
			return nil, err
		}
		if !connected {
			return nil, fail(400, fmt.Sprintf("工具服务 %s 尚未连接，请先在工具页连接后再引用 %s", serverName, ref))
		}
		var exists int
		if err = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tool_server_tools WHERE server_id=? AND name=?`, serverID, tool).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			return nil, fail(400, fmt.Sprintf("工具 %s 不在 %s 的发现清单内，请先重新连接该服务器", tool, serverName))
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out, nil
}

// toolCandidate 是本次请求候选项：一个已连接服务器上的一个工具。
type toolCandidate struct {
	Ref         string
	Tool        string
	Server      toolServerRow
	Description string
	InputSchema map[string]any
}

// candidateTools 计算候选集：只取自生效且启用的技能 → 去掉内置检索 → 只保留已连接服务器上
// 存在于发现清单中的工具 → 去重 → 按上限裁剪（顺序稳定，先按技能顺序）。
func (a *App) candidateTools(ctx context.Context, skills []promptRow) ([]toolCandidate, error) {
	seen := map[string]bool{}
	out := []toolCandidate{}
	for _, skill := range skills {
		for _, ref := range skill.Tools {
			ref = strings.TrimSpace(ref)
			if ref == "" || ref == builtinSearchTool || seen[ref] {
				continue
			}
			tool, serverName, err := parseToolRef(ref)
			if err != nil {
				continue
			}
			var server toolServerRow
			err = a.db.QueryRowContext(ctx, `SELECT id,owner_id,name,base_url,token_cipher,connected,last_error,connected_at
				FROM tool_servers WHERE name=? AND connected=1 ORDER BY created_at LIMIT 1`, serverName).
				Scan(&server.ID, &server.OwnerID, &server.Name, &server.BaseURL, &server.TokenCipher, &server.Connected, &server.LastError, &server.ConnectedAt)
			if err != nil {
				// 未连接或已删除：不进候选集（调用会被记为被拒）。
				continue
			}
			item := discoveredTool{}
			var schemaRaw sql.NullString
			err = a.db.QueryRowContext(ctx, `SELECT name,description,input_schema FROM tool_server_tools WHERE server_id=? AND name=?`, server.ID, tool).
				Scan(&item.Name, &item.Description, &schemaRaw)
			if err != nil {
				continue
			}
			schema := map[string]any{}
			if schemaRaw.Valid && schemaRaw.String != "" {
				_ = jsonUnmarshal(schemaRaw.String, &schema)
			}
			seen[ref] = true
			out = append(out, toolCandidate{Ref: ref, Tool: tool, Server: server, Description: item.Description, InputSchema: schema})
			if len(out) >= maxCandidateTools {
				return out, nil
			}
		}
	}
	return out, nil
}

// specsFromCandidates 把候选集转成随请求声明的工具列表。
func specsFromCandidates(candidates []toolCandidate) []toolSpec {
	specs := make([]toolSpec, 0, len(candidates))
	for _, item := range candidates {
		description := item.Description
		if !strings.Contains(description, "@") {
			// 让模型知道这是本班材料之外的一次确定性工具调用。
			description = item.Tool + "（来自 " + item.Server.Name + "）" + description
		}
		specs = append(specs, toolSpec{
			Type: "function",
			Function: toolSpecFunction{
				Name:        item.Tool,
				Description: description,
				Parameters:  item.InputSchema,
			},
		})
	}
	return specs
}
