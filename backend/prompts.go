package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// 提示词与技能：平台级给默认值，班级级覆盖；生效内容决定回答的语气与边界。
//
// 生效规则（与 openspec/changes/solver-assistant 的 prompt-skills 规约一致）：
//   - 提示词：班级行（kind=prompt、enabled=1）优先，其次平台行（kind=prompt、enabled=1），都没有则只用固定约束。
//   - 技能：同名技能以班级行为准（班级行 enabled=0 表示本班停用），没有班级行时用平台行。
const (
	promptKindPrompt = "prompt"
	promptKindSkill  = "skill"

	// 提示词正文上限：与对话历史上限一起约束交给模型的输入长度。
	maxPromptRunes = 2000

	// 平台提示词固定用这个名字，唯一键 (scope_key, kind, name) 因此保证每个层级只有一条提示词。
	defaultPromptName = "default"
	defaultPromptBody = "你是班级学习助手。只依据下面提供的本班材料回答：材料不足时直接说明未找到相关内容，不要用自己的知识补充；" +
		"回答使用简体中文，先给结论再给依据，并保留材料编号 [n]。"
	guidanceSkillName = "解题引导"
	guidanceSkillBody = "只做解题引导：说明思路、给出下一步可以尝试的步骤，必要时提出追问，帮助学生自己想出答案。" +
		"不要直接给出最终答案、完整解答或可直接提交的结果；即使学生要求公布答案，也只提供思路与自查方法。"
)

type promptRow struct {
	ID        string `json:"id"`
	Scope     string `json:"scope"`
	ClassID   string `json:"class_id,omitempty"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Body      string `json:"body"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func newID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw)
}

// ensurePlatformPrompts 幂等写入平台默认提示词与内置「解题引导」技能：
// 已存在则完全不改动（教师或管理员可能已经改过内容），只补缺失行。
func (a *App) ensurePlatformPrompts(ctx context.Context) error {
	seed := []struct {
		kind, name, body string
	}{
		{promptKindPrompt, defaultPromptName, defaultPromptBody},
		{promptKindSkill, guidanceSkillName, guidanceSkillBody},
	}
	for _, item := range seed {
		var exists int
		err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM prompts WHERE scope='platform' AND class_id IS NULL AND kind=? AND name=?`, item.kind, item.name).Scan(&exists)
		if err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		if _, err = a.db.ExecContext(ctx, `INSERT INTO prompts(id,scope,class_id,kind,name,body,enabled,created_by) VALUES(?,?,NULL,?,?,?,1,NULL)`,
			newID(), "platform", item.kind, item.name, item.body); err != nil {
			return err
		}
	}
	return nil
}

// effectivePrompt 返回班级生效的提示词正文与来源（class、platform 或 none）。
func (a *App) effectivePrompt(ctx context.Context, classID string) (string, string, error) {
	var body string
	err := a.db.QueryRowContext(ctx, `SELECT body FROM prompts WHERE scope='class' AND class_id=? AND kind=? AND name=? AND enabled=1`,
		classID, promptKindPrompt, defaultPromptName).Scan(&body)
	if err == nil {
		return strings.TrimSpace(body), "class", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}
	err = a.db.QueryRowContext(ctx, `SELECT body FROM prompts WHERE scope='platform' AND class_id IS NULL AND kind=? AND name=? AND enabled=1`,
		promptKindPrompt, defaultPromptName).Scan(&body)
	if err == nil {
		return strings.TrimSpace(body), "platform", nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", "none", nil
	}
	return "", "", err
}

// skillState 是一个技能名在某个班级下的生效状态：
// 班级行存在时以班级为准（enabled 与正文都算），班级行正文为空表示沿用平台正文。
type skillState struct {
	Name    string `json:"name"`
	Body    string `json:"body"`
	Enabled bool   `json:"enabled"`
	Scope   string `json:"scope"`
}

// skillsState 返回「平台技能 + 该班班级技能」合并后的完整状态（含已停用的，供管理界面展示）。
func (a *App) skillsState(ctx context.Context, classID string) ([]skillState, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT IFNULL(class_id,''),name,body,enabled FROM prompts
		WHERE kind=? AND (class_id IS NULL OR class_id=?)`, promptKindSkill, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type pair struct {
		platform, class *skillState
	}
	byName := map[string]*pair{}
	order := []string{}
	for rows.Next() {
		var classIDValue, name, body string
		var enabled bool
		if err = rows.Scan(&classIDValue, &name, &body, &enabled); err != nil {
			return nil, err
		}
		item, seen := byName[name]
		if !seen {
			item = &pair{}
			byName[name] = item
			order = append(order, name)
		}
		state := &skillState{Name: name, Body: body, Enabled: enabled, Scope: "platform"}
		if classIDValue == "" {
			item.platform = state
		} else {
			state.Scope = "class"
			item.class = state
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]skillState, 0, len(order))
	for _, name := range order {
		item := byName[name]
		merged := skillState{Name: name, Scope: "platform"}
		switch {
		case item.class != nil:
			merged.Enabled = item.class.Enabled
			merged.Scope = "class"
			merged.Body = item.class.Body
			if merged.Body == "" && item.platform != nil {
				merged.Body = item.platform.Body
			}
		case item.platform != nil:
			merged.Enabled = item.platform.Enabled
			merged.Body = item.platform.Body
		}
		out = append(out, merged)
	}
	return out, nil
}

// effectiveSkills 只保留启用中的技能，用于组装对话输入。
func (a *App) effectiveSkills(ctx context.Context, classID string) ([]promptRow, error) {
	states, err := a.skillsState(ctx, classID)
	if err != nil {
		return nil, err
	}
	out := make([]promptRow, 0, len(states))
	for _, state := range states {
		if state.Enabled {
			out = append(out, promptRow{Name: state.Name, Body: state.Body, Kind: promptKindSkill, Enabled: true})
		}
	}
	return out, nil
}

// validatePromptBody 统一校验提示词与技能正文：去空白后非空、长度受限。
func validatePromptBody(body string) (string, error) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "", errors.New("内容不能为空")
	}
	if utf8.RuneCountInString(trimmed) > maxPromptRunes {
		return "", fmt.Errorf("内容不能超过 %d 字", maxPromptRunes)
	}
	return trimmed, nil
}

// buildSystemPrompt 组装交给对话模块的固定约束、提示词与启用中的技能。
func buildSystemPrompt(promptBody string, skills []promptRow) string {
	var builder strings.Builder
	builder.WriteString("约束：只依据下面「材料」小节的内容回答，不得使用你自己的知识补充；")
	builder.WriteString("材料没有覆盖的问题，回答「资料中未找到相关内容」；回答保留材料编号 [n]。")
	if strings.TrimSpace(promptBody) != "" {
		builder.WriteString("\n\n班级要求：")
		builder.WriteString(strings.TrimSpace(promptBody))
	}
	for _, skill := range skills {
		builder.WriteString("\n\n技能「")
		builder.WriteString(skill.Name)
		builder.WriteString("」：")
		builder.WriteString(strings.TrimSpace(skill.Body))
	}
	return builder.String()
}
