package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// promptClient 在 authHarness 基础上提供角色化的请求助手。
type promptClient struct {
	h       *authHarness
	headers map[string]string
	user    User
}

func (h *authHarness) newUser(t *testing.T, role string, join bool) promptClient {
	t.Helper()
	name := map[string]string{"admin": "ad", "teacher": "te", "student": "st"}[role]
	password := name + "-pass-123456"
	request := httptest.NewRequest(http.MethodPost, "/api/auth/register", nil)
	user, err := h.app.addUser(request, credentials{Username: name + h.f.classID[:6], Name: "测试" + role, Password: password}, role)
	if err != nil {
		t.Fatal(err)
	}
	if join && role == "student" {
		if _, err = h.app.db.ExecContext(h.ctx, `INSERT INTO memberships(class_id,user_id) VALUES(?,?)`, h.f.classID, user.ID); err != nil {
			t.Fatal(err)
		}
	}
	resp, body := h.do(t, http.MethodPost, "/api/auth/token", h.credentials(user.Username, password), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("为 %s 签发令牌失败：%d %s", role, resp.StatusCode, body)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err = json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	return promptClient{h: h, headers: map[string]string{"Authorization": "Bearer " + payload.Token}, user: user}
}

func (c promptClient) request(t *testing.T, method, path, body string) (*http.Response, string) {
	t.Helper()
	return c.h.do(t, method, path, body, c.headers)
}

func decodeView(t *testing.T, body string) promptView {
	t.Helper()
	var view promptView
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("解析提示词视图失败: %v\n%s", err, body)
	}
	return view
}

// 任务 3.1：角色与归档边界。
func TestPromptApiRoles(t *testing.T) {
	h := newAuthHarness(t)
	owner := promptClient{h: h, headers: map[string]string{"Authorization": "Bearer " + h.issueToken(t)}, user: h.user}
	student := h.newUser(t, "student", true)
	stranger := h.newUser(t, "teacher", false)
	admin := h.newUser(t, "admin", false)
	base := "/api/classes/" + h.f.classID + "/prompts"

	if resp, body := owner.request(t, http.MethodGet, base, ""); resp.StatusCode != 200 || !decodeView(t, body).CanEdit {
		t.Fatalf("所属教师应可读且可编辑：%d %s", resp.StatusCode, body)
	}
	if resp, body := student.request(t, http.MethodGet, base, ""); resp.StatusCode != 200 || decodeView(t, body).CanEdit {
		t.Fatalf("学生应可读但不可编辑：%d %s", resp.StatusCode, body)
	}
	if resp, _ := student.request(t, http.MethodPut, base, `{"body":"学生不能改"}`); resp.StatusCode != 403 {
		t.Fatalf("学生修改应被拒绝，实际 %d", resp.StatusCode)
	}
	if resp, _ := stranger.request(t, http.MethodGet, base, ""); resp.StatusCode != 403 {
		t.Fatalf("其他教师读取应被拒绝，实际 %d", resp.StatusCode)
	}
	if resp, _ := stranger.request(t, http.MethodPut, base, `{"body":"越权"}`); resp.StatusCode != 403 {
		t.Fatalf("其他教师修改应被拒绝，实际 %d", resp.StatusCode)
	}
	if resp, _ := admin.request(t, http.MethodPut, base, `{"body":"管理员也不能改班级"}`); resp.StatusCode != 403 {
		t.Fatalf("管理员改班级提示词应被拒绝，实际 %d", resp.StatusCode)
	}
	if resp, body := admin.request(t, http.MethodGet, "/api/admin/prompts", ""); resp.StatusCode != 200 || !decodeView(t, body).CanEdit {
		t.Fatalf("管理员应可读平台提示词：%d %s", resp.StatusCode, body)
	}
	if resp, _ := owner.request(t, http.MethodGet, "/api/admin/prompts", ""); resp.StatusCode != 403 {
		t.Fatalf("教师读平台提示词应被拒绝，实际 %d", resp.StatusCode)
	}
	if resp, _ := owner.request(t, http.MethodPut, "/api/admin/skills/"+guidanceSkillName, `{"enabled":false}`); resp.StatusCode != 403 {
		t.Fatalf("教师改平台技能应被拒绝，实际 %d", resp.StatusCode)
	}
	if resp, _ := admin.request(t, http.MethodDelete, "/api/admin/skills/"+guidanceSkillName, ""); resp.StatusCode != 400 {
		t.Fatalf("内置技能不允许删除，实际 %d", resp.StatusCode)
	}
	// 归档后只读。
	if _, err := h.app.db.ExecContext(h.ctx, `UPDATE classrooms SET archived=1 WHERE id=?`, h.f.classID); err != nil {
		t.Fatal(err)
	}
	if resp, body := owner.request(t, http.MethodGet, base, ""); resp.StatusCode != 200 || decodeView(t, body).CanEdit {
		t.Fatalf("归档后应可读但不可编辑：%d %s", resp.StatusCode, body)
	}
	if resp, _ := owner.request(t, http.MethodPut, base, `{"body":"归档后不能改"}`); resp.StatusCode != 403 {
		t.Fatalf("归档后修改应被拒绝，实际 %d", resp.StatusCode)
	}
}

// 任务 3.2：班级覆盖、回退平台、长度上限与「拒绝时不破坏已有内容」。
func TestPromptOverrideAndValidation(t *testing.T) {
	h := newAuthHarness(t)
	owner := promptClient{h: h, headers: map[string]string{"Authorization": "Bearer " + h.issueToken(t)}, user: h.user}
	base := "/api/classes/" + h.f.classID + "/prompts"

	resp, body := owner.request(t, http.MethodGet, base, "")
	if view := decodeView(t, body); resp.StatusCode != 200 || view.PromptSource != "platform" {
		t.Fatalf("初始应回退平台提示词：%d %s", resp.StatusCode, body)
	}
	resp, body = owner.request(t, http.MethodPut, base, `{"body":"只用中文回答，并先给结论"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("保存班级提示词失败：%d %s", resp.StatusCode, body)
	}
	if view := decodeView(t, body); view.PromptSource != "class" || !strings.Contains(view.PromptBody, "先给结论") {
		t.Fatalf("保存后应以班级为准：%s", body)
	}
	var stored string
	if err := h.app.db.QueryRowContext(h.ctx, `SELECT body FROM prompts WHERE class_id=? AND kind=? AND name=?`, h.f.classID, promptKindPrompt, defaultPromptName).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, "先给结论") {
		t.Fatalf("数据库中的班级提示词不符：%q", stored)
	}
	// 超长内容被拒绝且不破坏已有内容。
	long := strings.Repeat("字", maxPromptRunes+1)
	if resp, _ := owner.request(t, http.MethodPut, base, `{"body":"`+long+`"}`); resp.StatusCode != 400 {
		t.Fatalf("超长内容应被拒绝，实际 %d", resp.StatusCode)
	}
	if err := h.app.db.QueryRowContext(h.ctx, `SELECT body FROM prompts WHERE class_id=? AND kind=? AND name=?`, h.f.classID, promptKindPrompt, defaultPromptName).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, "先给结论") {
		t.Fatalf("拒绝后不应改动原内容：%q", stored)
	}
	// 空内容被拒绝。
	if resp, _ := owner.request(t, http.MethodPut, base, `{"body":"   "}`); resp.StatusCode != 400 {
		t.Fatalf("空内容应被拒绝，实际 %d", resp.StatusCode)
	}
	// 删除班级覆盖后回到平台默认。
	resp, body = owner.request(t, http.MethodDelete, base, "")
	if resp.StatusCode != 200 || decodeView(t, body).PromptSource != "platform" {
		t.Fatalf("删除覆盖后应回退平台：%d %s", resp.StatusCode, body)
	}
}

// 接口测试发现的缺陷回归：班级自建技能在只改启用状态时，不能要求重发正文。
// 之前的实现只看平台是否有同名技能，因此「停用我班自建技能」会被 400 拒绝。
func TestSkillToggleKeepsClassBodyWithoutResending(t *testing.T) {
	h := newAuthHarness(t)
	owner := promptClient{h: h, headers: map[string]string{"Authorization": "Bearer " + h.issueToken(t)}, user: h.user}
	base := "/api/classes/" + h.f.classID
	if err := h.app.ensurePlatformPrompts(h.ctx); err != nil {
		t.Fatal(err)
	}

	resp, body := owner.request(t, http.MethodPut, base+"/skills/逐步提示", `{"body":"每次只提示一个小步骤。","enabled":true}`)
	if resp.StatusCode != 200 {
		t.Fatalf("新建班级技能失败：%d %s", resp.StatusCode, body)
	}

	// 只发 enabled：正文应沿用班级行已有内容，而不是被清空或拒绝。
	resp, body = owner.request(t, http.MethodPut, base+"/skills/逐步提示", `{"enabled":false}`)
	if resp.StatusCode != 200 {
		t.Fatalf("停用班级自建技能失败：%d %s", resp.StatusCode, body)
	}
	view := decodeView(t, body)
	var found *skillState
	for index := range view.Skills {
		if view.Skills[index].Name == "逐步提示" {
			found = &view.Skills[index]
		}
	}
	if found == nil {
		t.Fatalf("班级技能应从状态中消失：%+v", view.Skills)
	}
	if found.Enabled {
		t.Fatalf("技能应已停用：%+v", found)
	}
	if found.Body != "每次只提示一个小步骤。" {
		t.Fatalf("停用后正文应保留，实际 %q", found.Body)
	}

	// 平台技能同样允许只改启用状态（需要管理员）。
	admin := h.newUser(t, "admin", false)
	resp, body = admin.request(t, http.MethodPut, "/api/admin/skills/平台自建技能", `{"body":"平台技能正文。"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("新建平台技能失败：%d %s", resp.StatusCode, body)
	}
	resp, body = admin.request(t, http.MethodPut, "/api/admin/skills/平台自建技能", `{"enabled":false}`)
	if resp.StatusCode != 200 {
		t.Fatalf("停用平台技能失败：%d %s", resp.StatusCode, body)
	}
	var existing string
	if err := h.app.db.QueryRowContext(h.ctx, `SELECT body FROM prompts WHERE scope='platform' AND kind=? AND name=?`, promptKindSkill, "平台自建技能").Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if existing != "平台技能正文。" {
		t.Fatalf("平台技能正文应保留，实际 %q", existing)
	}

	// 新建技能仍然必须提供正文。
	resp, body = owner.request(t, http.MethodPut, base+"/skills/全新技能", `{"enabled":true}`)
	if resp.StatusCode != 400 {
		t.Fatalf("新建技能缺少正文应 400，实际 %d %s", resp.StatusCode, body)
	}
}

// 任务 3.3：技能启用／停用进入组装结果，班级正文为空时沿用平台正文。
func TestSkillToggleAffectsSystemPrompt(t *testing.T) {
	h := newAuthHarness(t)
	owner := promptClient{h: h, headers: map[string]string{"Authorization": "Bearer " + h.issueToken(t)}, user: h.user}
	base := "/api/classes/" + h.f.classID
	if err := h.app.ensurePlatformPrompts(h.ctx); err != nil {
		t.Fatal(err)
	}

	skills, err := h.app.effectiveSkills(h.ctx, h.f.classID)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || !strings.Contains(buildSystemPrompt("", skills), "不要直接给出最终答案") {
		t.Fatalf("默认应启用内置引导技能：%+v", skills)
	}

	// 停用：班级行只改启用状态，正文沿用平台。
	resp, body := owner.request(t, http.MethodPut, base+"/skills/"+guidanceSkillName, `{"enabled":false}`)
	if resp.StatusCode != 200 {
		t.Fatalf("停用技能失败：%d %s", resp.StatusCode, body)
	}
	view := decodeView(t, body)
	found := false
	for _, skill := range view.Skills {
		if skill.Name == guidanceSkillName {
			found = true
			if skill.Enabled {
				t.Fatalf("停用后视图应显示 enabled=false：%s", body)
			}
			if !strings.Contains(skill.Body, "解题引导") && !strings.Contains(skill.Body, "引导") {
				t.Fatalf("班级行正文为空时应沿用平台正文：%s", body)
			}
		}
	}
	if !found {
		t.Fatalf("视图中应仍能看到被停用的技能：%s", body)
	}
	skills, err = h.app.effectiveSkills(h.ctx, h.f.classID)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 || strings.Contains(buildSystemPrompt("", skills), guidanceSkillName) {
		t.Fatalf("停用后不应进入组装结果：%+v", skills)
	}

	// 恢复启用并新增一个自定义技能。
	if resp, body = owner.request(t, http.MethodPut, base+"/skills/"+guidanceSkillName, `{"enabled":true}`); resp.StatusCode != 200 {
		t.Fatalf("恢复技能失败：%d %s", resp.StatusCode, body)
	}
	if resp, body = owner.request(t, http.MethodPut, base+"/skills/逐步提示", `{"body":"每次只提示一个小步骤"}`); resp.StatusCode != 200 {
		t.Fatalf("新增技能失败：%d %s", resp.StatusCode, body)
	}
	skills, err = h.app.effectiveSkills(h.ctx, h.f.classID)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 {
		t.Fatalf("应有两个启用技能，实际 %+v", skills)
	}
	built := buildSystemPrompt("", skills)
	if !strings.Contains(built, guidanceSkillName) || !strings.Contains(built, "每次只提示一个小步骤") {
		t.Fatalf("组装结果应包含两个技能：%s", built)
	}
	// 新建技能不给正文会被拒绝。
	if resp, _ = owner.request(t, http.MethodPut, base+"/skills/空技能", `{"enabled":true}`); resp.StatusCode != 400 {
		t.Fatalf("新建技能缺正文应 400，实际 %d", resp.StatusCode)
	}
	// 删除班级行后回到平台状态（只剩内置技能）。
	resp, body = owner.request(t, http.MethodDelete, base+"/skills/逐步提示", "")
	if resp.StatusCode != 200 {
		t.Fatalf("删除班级技能失败：%d %s", resp.StatusCode, body)
	}
	skills, err = h.app.effectiveSkills(h.ctx, h.f.classID)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != guidanceSkillName {
		t.Fatalf("删除后应只剩平台内置技能：%+v", skills)
	}
}
