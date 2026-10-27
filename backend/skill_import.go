package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
)

// 技能上传：支持 .md（可选 front-matter）与 .json 两种格式，解析后走与表单保存相同的校验。

const maxSkillUploadBytes = 32 << 10

// parsedSkill 是上传文件解析出的技能定义。
type parsedSkill struct {
	Name    string
	Body    string
	Enabled *bool
	Tools   []string
}

// parseSkillFile 按扩展名解析技能定义。
func parseSkillFile(filename string, raw []byte) (parsedSkill, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	text := strings.TrimPrefix(string(raw), "\uFEFF")
	switch ext {
	case ".json":
		var payload struct {
			Name    string   `json:"name"`
			Body    string   `json:"body"`
			Enabled *bool    `json:"enabled"`
			Tools   []string `json:"tools"`
		}
		if err := json.Unmarshal([]byte(text), &payload); err != nil {
			return parsedSkill{}, errors.New("技能文件不是合法 JSON")
		}
		return parsedSkill{Name: payload.Name, Body: payload.Body, Enabled: payload.Enabled, Tools: payload.Tools}, nil
	case ".md", ".markdown", ".txt":
		return parseMarkdownSkill(text)
	default:
		return parsedSkill{}, fmt.Errorf("不支持的文件类型 %s：请使用 .md 或 .json", ext)
	}
}

// parseMarkdownSkill 解析可选的 front-matter：
//
//	---
//	name: 逐步提示
//	enabled: true
//	tools: formula_hint@campus-tools, knowledge_search
//	---
//	技能正文……
func parseMarkdownSkill(text string) (parsedSkill, error) {
	out := parsedSkill{}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	if strings.HasPrefix(strings.TrimSpace(normalized), "---") {
		trimmed := strings.TrimPrefix(strings.TrimSpace(normalized), "---")
		end := strings.Index(trimmed, "\n---")
		if end < 0 {
			return parsedSkill{}, errors.New("front-matter 未闭合：需要以 --- 开始并以 --- 结束")
		}
		header := trimmed[:end]
		out.Body = strings.TrimSpace(trimmed[end+len("\n---"):])
		for _, line := range strings.Split(header, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || !strings.Contains(line, ":") {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			key := strings.ToLower(strings.TrimSpace(parts[0]))
			value := ""
			if len(parts) > 1 {
				value = strings.TrimSpace(strings.Trim(parts[1], `"'`))
			}
			switch key {
			case "name":
				out.Name = value
			case "body":
				out.Body = value
			case "enabled":
				enabled := !strings.EqualFold(value, "false") && value != "0"
				out.Enabled = &enabled
			case "tools":
				for _, item := range strings.Split(value, ",") {
					if item = strings.TrimSpace(item); item != "" {
						out.Tools = append(out.Tools, item)
					}
				}
			}
		}
		return out, nil
	}
	out.Body = strings.TrimSpace(normalized)
	return out, nil
}

// importClassSkill 处理 POST /api/classes/{id}/skills/import。
func (a *App) importClassSkill(w http.ResponseWriter, r *http.Request, u *User) error {
	c, err := a.ownerEditable(r, u)
	if err != nil {
		return err
	}
	if err = r.ParseMultipartForm(maxSkillUploadBytes); err != nil {
		return fail(400, "请用 multipart/form-data 上传技能文件")
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return fail(400, "缺少文件字段 file")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxSkillUploadBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxSkillUploadBytes {
		return fail(413, "技能文件不能超过 32 KB")
	}
	parsed, err := parseSkillFile(header.Filename, raw)
	if err != nil {
		return fail(400, err.Error())
	}
	name := strings.TrimSpace(parsed.Name)
	if name == "" {
		// 没写名称时用文件名（去掉扩展名），再按技能名规则校验。
		name = strings.TrimSpace(strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename)))
	}
	if _, err = skillName(name); err != nil {
		return err
	}
	body, err := validatePromptBody(parsed.Body)
	if err != nil {
		return fail(400, "技能正文不合法："+err.Error())
	}
	tools, err := a.normalizeSkillTools(r.Context(), u.ID, parsed.Tools, true)
	if err != nil {
		return err
	}
	enabled := true
	if parsed.Enabled != nil {
		enabled = *parsed.Enabled
	}
	encodedTools, _ := json.Marshal(tools)
	if _, err = a.db.ExecContext(r.Context(), `
		INSERT INTO prompts(id,scope,class_id,kind,name,body,enabled,created_by,tools) VALUES(?,?,?,?,?,?,?,?,?) AS new
		ON DUPLICATE KEY UPDATE body=new.body,enabled=new.enabled,tools=new.tools,updated_at=NOW(6)`,
		newID(), "class", c.ID, promptKindSkill, name, body, enabled, u.ID, string(encodedTools)); err != nil {
		return err
	}
	view, err := a.classPromptView(r, c, u)
	if err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"skill": name, "view": view})
	return nil
}

// listApps 处理 GET /api/classes/{id}/apps：返回内置应用清单（成员可读）。
func (a *App) listApps(w http.ResponseWriter, r *http.Request, u *User) error {
	if _, err := a.access(r, r.PathValue("id"), u); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"apps": appPresets})
	return nil
}
