package main

import (
	"net/http"
	"strings"
	"testing"
)

// 任务 3.2：改名生效、他人的服务器按不存在处理。
func TestToolServerRenameAndIsolation(t *testing.T) {
	th := newToolHarness(t, true)
	path := "/api/tool-servers/" + th.row.ID
	resp, body := th.owner.request(t, http.MethodPatch, path, `{"name":"campus-tools-2","base_url":"`+th.mcp.server.URL+`"}`)
	if resp.StatusCode != 200 || !strings.Contains(body, "campus-tools-2") {
		t.Fatalf("改名应成功：%d %s", resp.StatusCode, body)
	}
	// 改名后再连接：连接状态保持，工具清单仍可替换。
	if resp, body = th.owner.request(t, http.MethodPost, path+"/connect", ""); resp.StatusCode != 200 {
		t.Fatalf("改名后连接应成功：%d %s", resp.StatusCode, body)
	}
	// 重名 409
	if _, body = th.owner.request(t, http.MethodPost, "/api/tool-servers", `{"name":"campus-tools-2","base_url":"`+th.mcp.server.URL+`"}`); !strings.Contains(body, "已被使用") {
		t.Fatalf("重名应提示已被使用：%s", body)
	}
	other := th.server.newUser(t, "teacher", false)
	if resp, _ = other.request(t, http.MethodPatch, path, `{"name":"x","base_url":"http://127.0.0.1:9"}`); resp.StatusCode != 404 {
		t.Fatalf("他人改名应 404，实际 %d", resp.StatusCode)
	}
	if resp, _ = other.request(t, http.MethodDelete, path, ""); resp.StatusCode != 404 {
		t.Fatalf("他人删除应 404，实际 %d", resp.StatusCode)
	}
}

// 任务 3.4：连接失败不影响检索与流式对话主路径。
func TestConnectFailureKeepsChatPath(t *testing.T) {
	th := newToolHarness(t, false)
	dead := toolServerRow{ID: newID(), OwnerID: th.server.user.ID, Name: "dead-tools", BaseURL: "http://127.0.0.1:1"}
	if _, err := th.app.db.ExecContext(th.server.ctx, `INSERT INTO tool_servers(id,owner_id,name,base_url) VALUES(?,?,?,?)`,
		dead.ID, dead.OwnerID, dead.Name, dead.BaseURL); err != nil {
		t.Fatal(err)
	}
	if resp, _ := th.owner.request(t, http.MethodPost, "/api/tool-servers/"+dead.ID+"/connect", ""); resp.StatusCode != 502 {
		t.Fatalf("连接失败应 502，实际 %d", resp.StatusCode)
	}
	// 主路径：检索与提问仍正常（用第 5 课的替身网关）。
	th.server.seedChunks(t)
	stub := newChatStub(t, chatStubConfig{chunks: []string{"考核由平时作业", "与期末项目组成。[1]"}})
	th.app.gateway = stub.client
	sessionID := th.server.createSession(t, ownerHeaders(t, th.server))
	resp, events := th.server.chatRequest(t, http.MethodPost, "/api/classes/"+th.server.f.classID+"/sessions/"+sessionID+"/messages",
		`{"content":"考核方式由什么组成"}`, ownerHeaders(t, th.server))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("连接失败后提问应 200，实际 %d", resp.StatusCode)
	}
	if len(events) < 3 {
		t.Fatalf("仍应有完整的流式事件：%+v", events)
	}
}

// 任务 4.2：校验失败的保存不破坏既有内容。
func TestSkillToolSaveKeepsPreviousContent(t *testing.T) {
	th := newToolHarness(t, true)
	path := th.classPath("/skills/带工具技能")
	if resp, body := th.owner.request(t, http.MethodPut, path, `{"body":"原文","enabled":true,"tools":["formula_hint@campus-tools"]}`); resp.StatusCode != 200 {
		t.Fatalf("首次保存应成功：%d %s", resp.StatusCode, body)
	}
	if resp, _ := th.owner.request(t, http.MethodPut, path, `{"body":"改后","enabled":true,"tools":["nope@campus-tools"]}`); resp.StatusCode != 400 {
		t.Fatalf("非法工具应 400，实际 %d", resp.StatusCode)
	}
	var body string
	var tools string
	if err := th.app.db.QueryRowContext(th.server.ctx, `SELECT body,IFNULL(tools,'') FROM prompts WHERE class_id=? AND kind=? AND name=?`,
		th.server.f.classID, promptKindSkill, "带工具技能").Scan(&body, &tools); err != nil {
		t.Fatal(err)
	}
	if body != "原文" || !strings.Contains(tools, "formula_hint") {
		t.Fatalf("失败后不应改动既有内容：body=%q tools=%q", body, tools)
	}
}

// 任务 6.2：工具结果只作为 tool 角色回传，不进入 system 提示词。
func TestToolResultStaysOutOfSystemPrompt(t *testing.T) {
	th := newToolHarness(t, true)
	if _, err := th.app.db.ExecContext(th.server.ctx, `INSERT INTO prompts(id,scope,class_id,kind,name,body,enabled,tools)
		VALUES(?,?,?,?,?,?,1,?)`, newID(), "class", th.server.f.classID, promptKindSkill, "带工具技能", "按工具提示回答。",
		`["formula_hint@campus-tools"]`); err != nil {
		t.Fatal(err)
	}
	sessionID := th.server.createSession(t, ownerHeaders(t, th.server))
	input, _ := th.orchestration(t, sessionID)
	script := &scriptedGateway{toolCalls: 1, callName: "formula_hint", args: `{"topic":"总评"}`, content: []string{"回答。"}}
	gateway := newScriptedGateway(t, script)
	th.app.gateway = gateway
	var captured []chatMessage
	script.onRequest = func(messages []chatMessage) { captured = messages }
	if _, _, err := th.app.runChatWithTools(th.server.ctx, input, func(string) error { return nil }, func(toolEvent) {}); err != nil {
		t.Fatal(err)
	}
	if len(captured) == 0 {
		t.Fatal("应至少有一次请求被捕获")
	}
	for _, message := range captured {
		if message.Role == "system" || (message.Role == "user" && strings.Contains(message.Content, "公式：总评")) {
			t.Fatalf("工具结果不应进入 system 或用户消息：%+v", message)
		}
	}
	foundTool := false
	for _, message := range captured {
		if message.Role == "tool" && strings.Contains(message.Content, "40%") {
			foundTool = true
		}
	}
	if !foundTool {
		t.Fatalf("工具结果应作为 tool 消息回传：%+v", captured)
	}
}
