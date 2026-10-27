package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// 任务 3.1 / 3.2 的校验器用例表：与 backend/jsonschema.go 的测试保持同一份用例，
// 避免两份镜像实现分叉。
func schema(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestValidateArguments(t *testing.T) {
	objectSchema := schema(t, `{
		"type": "object",
		"required": ["topic"],
		"properties": {
			"topic": {"type": "string", "minLength": 1, "maxLength": 5},
			"level": {"type": "string", "enum": ["a", "b"]},
			"value": {"type": "number", "minimum": 1, "maximum": 10},
			"count": {"type": "integer", "minimum": 0},
			"flag": {"type": "boolean"},
			"items": {"type": "array", "items": {"type": "string"}}
		}
	}`)

	cases := []struct {
		name    string
		args    map[string]any
		wantErr string
	}{
		{"合法", map[string]any{"topic": "abc"}, ""},
		{"缺必填", map[string]any{}, "缺少必填参数 topic"},
		{"字符串超长", map[string]any{"topic": "abcdef"}, "最多 5 个字符"},
		{"类型错误", map[string]any{"topic": 1}, "应为字符串"},
		{"枚举越界", map[string]any{"topic": "abc", "level": "c"}, "取值不合法"},
		{"数值过小", map[string]any{"topic": "abc", "value": 0.5}, "不能小于 1"},
		{"数值过大", map[string]any{"topic": "abc", "value": 11}, "不能大于 10"},
		{"整数校验", map[string]any{"topic": "abc", "count": 1.5}, "应为整数"},
		{"布尔校验", map[string]any{"topic": "abc", "flag": "yes"}, "应为布尔值"},
		{"数组元素", map[string]any{"topic": "abc", "items": []any{"ok", 1}}, "items[1] 应为字符串"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			err := validateArguments(objectSchema, item.args)
			if item.wantErr == "" {
				if err != nil {
					t.Fatalf("不应报错：%v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), item.wantErr) {
				t.Fatalf("期望错误包含 %q，实际 %v", item.wantErr, err)
			}
		})
	}
}

func TestValidateArgumentsIgnoresUndeclared(t *testing.T) {
	objectSchema := schema(t, `{"type":"object","properties":{"topic":{"type":"string"}}}`)
	if err := validateArguments(objectSchema, map[string]any{"topic": "ok", "extra": 1}); err != nil {
		t.Fatalf("未声明参数应被忽略：%v", err)
	}
}
