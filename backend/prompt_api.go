package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

// promptView 是对外的提示词与技能状态。PromptSource 说明当前提示词来自班级、平台还是都没有。
type promptView struct {
	PromptBody   string       `json:"prompt_body"`
	PromptSource string       `json:"prompt_source"`
	Skills       []skillState `json:"skills"`
	CanEdit      bool         `json:"can_edit"`
	Archived     bool         `json:"archived"`
}

type promptInput struct {
	Body string `json:"body"`
}

type skillInput struct {
	Body    *string   `json:"body"`
	Enabled *bool     `json:"enabled"`
	Tools   *[]string `json:"tools"`
}

// ownerEditable 取班级并要求「所属教师 + 未归档」；学生与其他教师都被拒绝。
func (a *App) ownerEditable(r *http.Request, u *User) (Classroom, error) {
	c, err := a.access(r, r.PathValue("id"), u)
	if err != nil {
		return c, err
	}
	if c.OwnerID != u.ID {
		return c, fail(403, "只有所属教师可以修改")
	}
	if c.Archived {
		return c, fail(403, "班级已归档，不能修改")
	}
	return c, nil
}

// classPromptView 组装班级提示词状态；修改接口复用它作为响应。
func (a *App) classPromptView(r *http.Request, c Classroom, u *User) (promptView, error) {
	body, source, err := a.effectivePrompt(r.Context(), c.ID)
	if err != nil {
		return promptView{}, err
	}
	skills, err := a.skillsState(r.Context(), c.ID)
	if err != nil {
		return promptView{}, err
	}
	return promptView{PromptBody: body, PromptSource: source, Skills: skills, CanEdit: c.OwnerID == u.ID && !c.Archived, Archived: c.Archived}, nil
}

// GET /api/classes/{id}/prompts：班级成员可读（学生只读，CanEdit=false）。
func (a *App) classPrompts(w http.ResponseWriter, r *http.Request, u *User) error {
	c, err := a.access(r, r.PathValue("id"), u)
	if err != nil {
		return err
	}
	view, err := a.classPromptView(r, c, u)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

// PUT /api/classes/{id}/prompts：所属教师保存班级提示词，覆盖平台默认。
func (a *App) updateClassPrompt(w http.ResponseWriter, r *http.Request, u *User) error {
	c, err := a.ownerEditable(r, u)
	if err != nil {
		return err
	}
	var input promptInput
	if err = decode(w, r, &input); err != nil {
		return err
	}
	body, err := validatePromptBody(input.Body)
	if err != nil {
		return fail(400, err.Error())
	}
	if _, err = a.db.ExecContext(r.Context(), `
		INSERT INTO prompts(id,scope,class_id,kind,name,body,enabled,created_by) VALUES(?,?,?,?,?,?,1,?) AS new
		ON DUPLICATE KEY UPDATE body=new.body,enabled=1,updated_at=NOW(6)`,
		newID(), "class", c.ID, promptKindPrompt, defaultPromptName, body, u.ID); err != nil {
		return err
	}
	view, err := a.classPromptView(r, c, u)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

// DELETE /api/classes/{id}/prompts：删除班级覆盖，回到平台默认。
func (a *App) deleteClassPrompt(w http.ResponseWriter, r *http.Request, u *User) error {
	c, err := a.ownerEditable(r, u)
	if err != nil {
		return err
	}
	if _, err = a.db.ExecContext(r.Context(), `DELETE FROM prompts WHERE class_id=? AND kind=? AND name=?`, c.ID, promptKindPrompt, defaultPromptName); err != nil {
		return err
	}
	view, err := a.classPromptView(r, c, u)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

// skillName 校验技能名：非空、长度受限、不含控制字符。
func skillName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fail(400, "技能名不能为空")
	}
	if utf8.RuneCountInString(name) > 60 {
		return "", fail(400, "技能名不能超过 60 个字符")
	}
	for _, symbol := range name {
		if symbol < 0x20 || symbol == 0x7f {
			return "", fail(400, "技能名不能包含控制字符")
		}
	}
	return name, nil
}

// PUT /api/classes/{id}/skills/{name}：所属教师覆盖或停用某个技能。
// body 省略或为空表示沿用平台同名技能的正文，只调整启用状态。
func (a *App) updateClassSkill(w http.ResponseWriter, r *http.Request, u *User) error {
	c, err := a.ownerEditable(r, u)
	if err != nil {
		return err
	}
	name, err := skillName(r.PathValue("name"))
	if err != nil {
		return err
	}
	var input skillInput
	if err = decode(w, r, &input); err != nil {
		return err
	}
	body := ""
	if input.Body != nil {
		if body, err = validatePromptBody(*input.Body); err != nil {
			return fail(400, err.Error())
		}
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	// 省略正文时沿用既有内容：先看本班是否已有该技能，再看平台同名技能。
	// 这样「只停用／只启用」不需要把正文再发一遍。
	if body == "" {
		var existing string
		err = a.db.QueryRowContext(r.Context(), `SELECT body FROM prompts WHERE scope='class' AND class_id=? AND kind=? AND name=?`, c.ID, promptKindSkill, name).Scan(&existing)
		switch {
		case err == nil:
			body = existing
		case errors.Is(err, sql.ErrNoRows):
			var platformBody string
			if err = a.db.QueryRowContext(r.Context(), `SELECT body FROM prompts WHERE scope='platform' AND kind=? AND name=?`, promptKindSkill, name).Scan(&platformBody); err == nil {
				body = platformBody
			} else if errors.Is(err, sql.ErrNoRows) {
				return fail(400, "新建技能必须提供正文")
			} else {
				return err
			}
		default:
			return err
		}
	}
	// 工具项：省略时沿用既有（本班行 → 平台行），提供时按归属、连接状态与发现清单校验。
	toolsJSON := sql.NullString{}
	{
		var existingTools sql.NullString
		err = a.db.QueryRowContext(r.Context(), `SELECT tools FROM prompts WHERE scope='class' AND class_id=? AND kind=? AND name=?`, c.ID, promptKindSkill, name).Scan(&existingTools)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if input.Tools != nil {
			normalized, err := a.normalizeSkillTools(r.Context(), u.ID, *input.Tools, true)
			if err != nil {
				return err
			}
			encoded, _ := json.Marshal(normalized)
			toolsJSON = sql.NullString{String: string(encoded), Valid: true}
		} else if existingTools.Valid {
			toolsJSON = existingTools
		}
	}
	if _, err = a.db.ExecContext(r.Context(), `
		INSERT INTO prompts(id,scope,class_id,kind,name,body,enabled,created_by,tools) VALUES(?,?,?,?,?,?,?,?,?) AS new
		ON DUPLICATE KEY UPDATE body=new.body,enabled=new.enabled,tools=IF(new.tools IS NULL,prompts.tools,new.tools),updated_at=NOW(6)`,
		newID(), "class", c.ID, promptKindSkill, name, body, enabled, u.ID, toolsJSON); err != nil {
		return err
	}
	view, err := a.classPromptView(r, c, u)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

// DELETE /api/classes/{id}/skills/{name}：删除班级行，回到平台同名技能的状态。
func (a *App) deleteClassSkill(w http.ResponseWriter, r *http.Request, u *User) error {
	c, err := a.ownerEditable(r, u)
	if err != nil {
		return err
	}
	name, err := skillName(r.PathValue("name"))
	if err != nil {
		return err
	}
	if _, err = a.db.ExecContext(r.Context(), `DELETE FROM prompts WHERE class_id=? AND kind=? AND name=?`, c.ID, promptKindSkill, name); err != nil {
		return err
	}
	view, err := a.classPromptView(r, c, u)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

// platformPromptView 组装平台级状态：管理员可编辑。
func (a *App) platformPromptView(r *http.Request) (promptView, error) {
	var body string
	err := a.db.QueryRowContext(r.Context(), `SELECT body FROM prompts WHERE scope='platform' AND class_id IS NULL AND kind=? AND name=?`, promptKindPrompt, defaultPromptName).Scan(&body)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return promptView{}, err
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT name,body,enabled FROM prompts WHERE scope='platform' AND class_id IS NULL AND kind=? ORDER BY name`, promptKindSkill)
	if err != nil {
		return promptView{}, err
	}
	defer rows.Close()
	skills := []skillState{}
	for rows.Next() {
		item := skillState{Scope: "platform"}
		if err = rows.Scan(&item.Name, &item.Body, &item.Enabled); err != nil {
			return promptView{}, err
		}
		skills = append(skills, item)
	}
	if err = rows.Err(); err != nil {
		return promptView{}, err
	}
	source := "platform"
	if body == "" {
		source = "none"
	}
	return promptView{PromptBody: body, PromptSource: source, Skills: skills, CanEdit: true}, nil
}

// GET /api/admin/prompts：管理员读取平台默认提示词与平台技能。
func (a *App) adminPrompts(w http.ResponseWriter, r *http.Request, u *User) error {
	if u.Role != "admin" {
		return fail(403, "只有管理员可以查看平台提示词")
	}
	view, err := a.platformPromptView(r)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

// PUT /api/admin/prompts：管理员修改平台默认提示词。
func (a *App) updateAdminPrompt(w http.ResponseWriter, r *http.Request, u *User) error {
	if u.Role != "admin" {
		return fail(403, "只有管理员可以修改平台提示词")
	}
	var input promptInput
	if err := decode(w, r, &input); err != nil {
		return err
	}
	body, err := validatePromptBody(input.Body)
	if err != nil {
		return fail(400, err.Error())
	}
	if _, err = a.db.ExecContext(r.Context(), `
		INSERT INTO prompts(id,scope,class_id,kind,name,body,enabled,created_by) VALUES(?,?,NULL,?,?,?,1,?) AS new
		ON DUPLICATE KEY UPDATE body=new.body,enabled=1,updated_at=NOW(6)`,
		newID(), "platform", promptKindPrompt, defaultPromptName, body, u.ID); err != nil {
		return err
	}
	view, err := a.platformPromptView(r)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

// PUT /api/admin/skills/{name}：管理员维护平台技能（含内置「解题引导」）。
func (a *App) updateAdminSkill(w http.ResponseWriter, r *http.Request, u *User) error {
	if u.Role != "admin" {
		return fail(403, "只有管理员可以修改平台技能")
	}
	name, err := skillName(r.PathValue("name"))
	if err != nil {
		return err
	}
	var input skillInput
	if err = decode(w, r, &input); err != nil {
		return err
	}
	body := ""
	if input.Body != nil {
		if body, err = validatePromptBody(*input.Body); err != nil {
			return fail(400, err.Error())
		}
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	// 内置「解题引导」必须始终存在：允许停用，但正文不能被清空。
	if name == guidanceSkillName && body == "" && input.Body != nil {
		return fail(400, "内置技能的正文不能为空")
	}
	// 省略正文时沿用平台既有正文，因此「只停用／只启用」不需要重发正文。
	if body == "" {
		var existing string
		if err = a.db.QueryRowContext(r.Context(), `SELECT body FROM prompts WHERE scope='platform' AND kind=? AND name=?`, promptKindSkill, name).Scan(&existing); err == nil {
			body = existing
		} else if errors.Is(err, sql.ErrNoRows) {
			return fail(400, "新建技能必须提供正文")
		} else {
			return err
		}
	}
	// 平台技能只允许内置知识库检索：管理员没有工具服务器归属，跨用户引用会破坏隔离。
	toolsJSON := sql.NullString{}
	if input.Tools != nil {
		normalized, err := a.normalizeSkillTools(r.Context(), u.ID, *input.Tools, false)
		if err != nil {
			return err
		}
		encoded, _ := json.Marshal(normalized)
		toolsJSON = sql.NullString{String: string(encoded), Valid: true}
	}
	if _, err = a.db.ExecContext(r.Context(), `
		INSERT INTO prompts(id,scope,class_id,kind,name,body,enabled,created_by,tools) VALUES(?,?,NULL,?,?,?,?,?,?) AS new
		ON DUPLICATE KEY UPDATE body=IF(new.body='',prompts.body,new.body),enabled=new.enabled,tools=IF(new.tools IS NULL,prompts.tools,new.tools),updated_at=NOW(6)`,
		newID(), "platform", promptKindSkill, name, body, enabled, u.ID, toolsJSON); err != nil {
		return err
	}
	view, err := a.platformPromptView(r)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

// DELETE /api/admin/skills/{name}：删除平台技能；内置「解题引导」不允许删除。
func (a *App) deleteAdminSkill(w http.ResponseWriter, r *http.Request, u *User) error {
	if u.Role != "admin" {
		return fail(403, "只有管理员可以删除平台技能")
	}
	name, err := skillName(r.PathValue("name"))
	if err != nil {
		return err
	}
	if name == guidanceSkillName {
		return fail(400, "内置「"+guidanceSkillName+"」技能不能删除，只能停用")
	}
	if _, err = a.db.ExecContext(r.Context(), `DELETE FROM prompts WHERE scope='platform' AND class_id IS NULL AND kind=? AND name=?`, promptKindSkill, name); err != nil {
		return err
	}
	view, err := a.platformPromptView(r)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}
