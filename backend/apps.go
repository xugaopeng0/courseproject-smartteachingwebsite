package main

import (
	"fmt"
	"strings"
	"time"
)

// 内置 AI 应用：同一套「检索 → 材料 → 出处」管线，用不同任务模板得到不同用途。
// 模板里支持变量，由 renderTemplate 渲染；未知变量原样保留，便于排查拼写。
type appPreset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Task 会追加到 system 提示词末尾；为空表示沿用默认的「回答问题」任务。
	Task string `json:"-"`
}

const (
	appQA   = "qa"
	appQuiz = "quiz"
)

var appPresets = []appPreset{
	{
		ID:          appQA,
		Name:        "知识库问答",
		Description: "依据本班讲义回答一个问题，并给出可核对的出处。",
	},
	{
		ID:          appQuiz,
		Name:        "出题练习",
		Description: "依据本班讲义出 3 道由易到难的练习题，逐题给出参考答案并标注出处。",
		Task: "任务：基于下面提供的材料出 3 道由易到难的练习题，逐题给出参考答案，并在每题后标注用到的材料编号 [n]。" +
			"题目必须能从材料推出；材料不足以出 3 道时，只出能出的题并说明原因。主题或要求：{{question}}",
	},
}

// findApp 按标识取应用预设；未知标识返回 false。
func findApp(id string) (appPreset, bool) {
	for _, preset := range appPresets {
		if preset.ID == id {
			return preset, true
		}
	}
	return appPreset{}, false
}

// renderTemplate 替换模板变量：{{question}}、{{class_name}}、{{skill_names}}、{{date}}。
// 不认识的占位符保持原样，便于使用者发现拼写问题。
func renderTemplate(text string, vars map[string]string) string {
	if !strings.Contains(text, "{{") {
		return text
	}
	replacer := strings.NewReplacer(
		"{{question}}", vars["question"],
		"{{class_name}}", vars["class_name"],
		"{{skill_names}}", vars["skill_names"],
		"{{date}}", vars["date"],
	)
	return replacer.Replace(text)
}

// promptVariables 组装渲染提示词模板所用的变量。
func (a *App) promptVariables(classID, question string, skills []promptRow) map[string]string {
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	return map[string]string{
		"question":    strings.TrimSpace(question),
		"class_name":  a.className(classID),
		"skill_names": strings.Join(names, "、"),
		"date":        time.Now().Format("2006-01-02"),
	}
}

// className 读班级名；读不到时返回空串（模板里就显示为空）。
func (a *App) className(classID string) string {
	var name string
	if err := a.db.QueryRow(`SELECT name FROM classrooms WHERE id=?`, classID).Scan(&name); err != nil {
		return ""
	}
	return name
}

// buildTaskPrompt 组装本次应用的任务段：应用模板渲染后追加到 system 提示词末尾。
func (a *App) buildTaskPrompt(appID, classID, question string, skills []promptRow) (string, error) {
	preset, ok := findApp(appID)
	if !ok {
		return "", fail(400, fmt.Sprintf("未知应用 %q：可用应用为 qa、quiz", appID))
	}
	if strings.TrimSpace(preset.Task) == "" {
		return "", nil
	}
	return renderTemplate(preset.Task, a.promptVariables(classID, question, skills)), nil
}
