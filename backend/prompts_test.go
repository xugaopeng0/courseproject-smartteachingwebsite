package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// 任务 1.1：三张新表的形状（生成列、唯一键、检查约束）与迁移幂等。
func TestPromptAndSessionTablesShape(t *testing.T) {
	a, ctx := testApp(t)
	expected := map[string][]string{
		"prompts":       {"id", "scope", "class_id", "kind", "name", "body", "enabled", "scope_key", "created_by"},
		"chat_sessions": {"id", "class_id", "user_id", "title", "last_message_at"},
		"chat_messages": {"id", "session_id", "class_id", "seq", "role", "content", "status", "citations", "model", "latency_ms"},
	}
	for table, columns := range expected {
		for _, column := range columns {
			var count int
			err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name=?`, table, column).Scan(&count)
			if err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("表 %s 缺少列 %s", table, column)
			}
		}
	}
	// scope_key 必须是虚拟生成列：存储生成列的基列不能带 ON DELETE CASCADE 外键。
	var extra string
	if err := a.db.QueryRowContext(ctx, `SELECT extra FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='prompts' AND column_name='scope_key'`).Scan(&extra); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToUpper(extra), "VIRTUAL GENERATED") {
		t.Fatalf("scope_key 应为 VIRTUAL GENERATED，实际 %q", extra)
	}
}

// 任务 1.1：迁移可重复执行，重复应用不报错也不丢数据。
func TestMigrationIsIdempotent(t *testing.T) {
	a, ctx := testApp(t)
	f := seedFixture(t, a, ctx)
	if err := a.ensurePlatformPrompts(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.ExecContext(ctx, `INSERT INTO chat_sessions(id,class_id,user_id,title) VALUES(?,?,?,?)`,
		randomID(16), f.classID, f.userID, "迁移前写入"); err != nil {
		t.Fatal(err)
	}
	// 直接重放 DDL 与迁移器各一次：两者都必须幂等。
	ddl, err := migrationFiles.ReadFile("migrations/003_prompt_skills_and_sessions.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range splitStatements(string(ddl)) {
		if _, err = a.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("重复执行迁移语句失败: %v\n%s", err, statement)
		}
	}
	if err = applyMigrations(ctx, a.db); err != nil {
		t.Fatalf("重复应用迁移失败: %v", err)
	}
	var promptsCount, sessionsCount int
	if err = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM prompts`).Scan(&promptsCount); err != nil {
		t.Fatal(err)
	}
	if err = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chat_sessions WHERE title='迁移前写入'`).Scan(&sessionsCount); err != nil {
		t.Fatal(err)
	}
	if promptsCount == 0 || sessionsCount != 1 {
		t.Fatalf("重复迁移后数据不符：prompts=%d sessions=%d", promptsCount, sessionsCount)
	}
}

// 任务 1.2：内置提示词与技能幂等写入，已存在时不覆盖用户改动。
func TestPlatformPromptsSeededIdempotently(t *testing.T) {
	a, ctx := testApp(t)
	if err := a.ensurePlatformPrompts(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM prompts WHERE scope='platform'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("平台级应有默认提示词与「解题引导」技能各一条，实际 %d", count)
	}
	// 模拟管理员改过内容后再启动：不得被种子覆盖。
	if _, err := a.db.ExecContext(ctx, `UPDATE prompts SET body='管理员自定义' WHERE scope='platform' AND kind=?`, promptKindPrompt); err != nil {
		t.Fatal(err)
	}
	if err := a.ensurePlatformPrompts(ctx); err != nil {
		t.Fatal(err)
	}
	var body string
	if err := a.db.QueryRowContext(ctx, `SELECT body FROM prompts WHERE scope='platform' AND kind=?`, promptKindPrompt).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != "管理员自定义" {
		t.Fatalf("重复初始化不应覆盖已有内容，实际 %q", body)
	}
	// 生效提示词：没有班级行时回退平台行。
	effective, source, err := a.effectivePrompt(ctx, randomID(16))
	if err != nil {
		t.Fatal(err)
	}
	if source != "platform" || effective != "管理员自定义" {
		t.Fatalf("应回退平台提示词，实际 source=%s body=%q", source, effective)
	}
	// 内置技能默认启用。
	skills, err := a.effectiveSkills(ctx, randomID(16))
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != guidanceSkillName {
		t.Fatalf("应有一条启用中的「%s」技能，实际 %+v", guidanceSkillName, skills)
	}
}

// 任务 1.3：层级唯一性、班级覆盖、停用与级联清理。
func TestPromptScopeUniquenessAndCascade(t *testing.T) {
	a, ctx := testApp(t)
	f := seedFixture(t, a, ctx)
	if err := a.ensurePlatformPrompts(ctx); err != nil {
		t.Fatal(err)
	}
	// 平台级与班级级同名共存。
	if _, err := a.db.ExecContext(ctx, `INSERT INTO prompts(id,scope,class_id,kind,name,body,enabled) VALUES(?,?,?,?,?,?,1)`,
		randomID(16), "class", f.classID, promptKindSkill, guidanceSkillName, "班级自定义引导"); err != nil {
		t.Fatalf("班级级同名技能应能与平台级共存: %v", err)
	}
	// 同层级同名再插一次必须被唯一键拒绝。
	_, err := a.db.ExecContext(ctx, `INSERT INTO prompts(id,scope,class_id,kind,name,body,enabled) VALUES(?,?,?,?,?,?,1)`,
		randomID(16), "class", f.classID, promptKindSkill, guidanceSkillName, "重复")
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1062 {
		t.Fatalf("同层级同名应触发唯一键冲突（1062），实际 %v", err)
	}
	// 班级行覆盖平台行。
	skills, err := a.effectiveSkills(ctx, f.classID)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Body != "班级自定义引导" {
		t.Fatalf("班级行应覆盖平台行，实际 %+v", skills)
	}
	// 班级行 enabled=0 表示本班停用。
	if _, err = a.db.ExecContext(ctx, `UPDATE prompts SET enabled=0 WHERE class_id=? AND kind=? AND name=?`, f.classID, promptKindSkill, guidanceSkillName); err != nil {
		t.Fatal(err)
	}
	skills, err = a.effectiveSkills(ctx, f.classID)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("停用后不应有生效技能，实际 %+v", skills)
	}
	// 插入会话与消息，删除班级后级联清理。
	sessionID := randomID(16)
	if _, err = a.db.ExecContext(ctx, `INSERT INTO chat_sessions(id,class_id,user_id,title) VALUES(?,?,?,?)`, sessionID, f.classID, f.userID, "级联检查"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.ExecContext(ctx, `INSERT INTO chat_messages(id,session_id,class_id,seq,role,content) VALUES(?,?,?,?,?,?)`,
		randomID(16), sessionID, f.classID, 1, "user", "你好"); err != nil {
		t.Fatal(err)
	}
	// handouts.class_id 外键没有级联（既有设计），先删讲义再删班级，检查新表的级联。
	if _, err = a.db.ExecContext(ctx, `DELETE FROM handouts WHERE class_id=?`, f.classID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.ExecContext(ctx, `DELETE FROM classrooms WHERE id=?`, f.classID); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		args  []any
		want  int
	}{
		{`SELECT COUNT(*) FROM prompts WHERE class_id=?`, []any{f.classID}, 0},
		{`SELECT COUNT(*) FROM chat_sessions WHERE id=?`, []any{sessionID}, 0},
		{`SELECT COUNT(*) FROM chat_messages WHERE session_id=?`, []any{sessionID}, 0},
	} {
		var got int
		if err = a.db.QueryRowContext(ctx, check.query, check.args...).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Fatalf("级联清理不符：%s 期望 %d 实际 %d", check.query, check.want, got)
		}
	}
}

// 提示词正文校验：空内容与超长内容都被拒绝。
func TestValidatePromptBody(t *testing.T) {
	if _, err := validatePromptBody("   "); err == nil {
		t.Fatal("空白内容应被拒绝")
	}
	if _, err := validatePromptBody(strings.Repeat("字", maxPromptRunes+1)); err == nil {
		t.Fatal("超长内容应被拒绝")
	}
	body, err := validatePromptBody("  正常内容  ")
	if err != nil || body != "正常内容" {
		t.Fatalf("应接受并去除首尾空白，实际 %q %v", body, err)
	}
}

// buildSystemPrompt 必须包含固定约束、提示词与启用中的技能。
func TestBuildSystemPrompt(t *testing.T) {
	prompt := buildSystemPrompt("班级要求：只用中文", []promptRow{{Name: guidanceSkillName, Body: guidanceSkillBody}})
	for _, needle := range []string{"只依据", "资料中未找到相关内容", "班级要求：只用中文", guidanceSkillName, "不要直接给出最终答案"} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("系统提示应包含 %q，实际:\n%s", needle, prompt)
		}
	}
	plain := buildSystemPrompt("", nil)
	if strings.Contains(plain, "班级要求") {
		t.Fatalf("没有提示词时不应出现班级要求段：%s", plain)
	}
}
