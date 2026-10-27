package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func callToolResult(t *testing.T, args map[string]any, name string) (map[string]any, bool) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      9,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := callRPC(t, string(body), testToken, "POST")
	payload := decodeResponse(t, recorder)
	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s 没有返回 result：%v", name, payload)
	}
	isError, _ := result["isError"].(bool)
	return result, isError
}

// 任务 3.1：formula_hint 返回公式与思路，不做数值代入。
func TestFormulaHint(t *testing.T) {
	result, isError := callToolResult(t, map[string]any{"topic": "总评计算", "level": "beginner"}, "formula_hint")
	if isError {
		t.Fatalf("合法参数不应报错：%v", result)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	for _, want := range []string{"公式：", "符号：", "下一步："} {
		if !strings.Contains(text, want) {
			t.Fatalf("返回内容缺少 %q：%s", want, text)
		}
	}
	if !strings.Contains(text, "40%") || !strings.Contains(text, "60%") {
		t.Fatalf("应给出权重而非数值结果：%s", text)
	}
	// 不给最终数值答案：不应出现具体得分结果的写法。
	if strings.Contains(text, "= 88") || strings.Contains(text, "总分是 88") {
		t.Fatalf("不应直接给出最终数值答案：%s", text)
	}
	if strings.Contains(text, "忽略") || strings.Contains(text, "你必须") {
		t.Fatalf("返回内容不应包含指令式语句：%s", text)
	}

	// 未匹配主题给通用思路，而不是报错。
	fallback, isError := callToolResult(t, map[string]any{"topic": "太阳系行星轨道"}, "formula_hint")
	if isError {
		t.Fatalf("未匹配主题应给通用思路：%v", fallback)
	}
	if !strings.Contains(fallback["content"].([]any)[0].(map[string]any)["text"].(string), "通用思路") {
		t.Fatal("未匹配主题应说明走通用思路")
	}
}

// 任务 3.1：缺少必填参数走协议错误。
func TestFormulaHintMissingTopic(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"formula_hint","arguments":{}}}`
	payload := decodeResponse(t, callRPC(t, body, testToken, "POST"))
	code, message := parseRPCError(t, payload)
	if code != codeInvalidParams || !strings.Contains(message, "topic") {
		t.Fatalf("应返回参数错误并指出 topic：%d %s", code, message)
	}
}

// 任务 3.2：unit_convert 覆盖四类换算。
func TestUnitConvertCategories(t *testing.T) {
	cases := []struct {
		value    float64
		from, to string
		want     string
	}{
		{1.2, "km", "m", "1200 m"},
		{500, "g", "kg", "0.5 kg"},
		{90, "min", "h", "1.5 h"},
		{2, "L", "mL", "2000 mL"},
	}
	for _, item := range cases {
		result, isError := callToolResult(t, map[string]any{"value": item.value, "from": item.from, "to": item.to}, "unit_convert")
		if isError {
			t.Fatalf("%v %s→%s 不应失败：%v", item.value, item.from, item.to, result)
		}
		text := result["content"].([]any)[0].(map[string]any)["text"].(string)
		if !strings.Contains(text, item.want) {
			t.Fatalf("期望包含 %q，实际 %q", item.want, text)
		}
	}
}

// 任务 3.3：跨类换算属于工具执行失败（result.isError），不是协议错误。
func TestUnitConvertCrossCategory(t *testing.T) {
	result, isError := callToolResult(t, map[string]any{"value": 1, "from": "cm", "to": "kg"}, "unit_convert")
	if !isError {
		t.Fatalf("跨类换算应标记 isError：%v", result)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "不能把长度换算成质量") {
		t.Fatalf("应说明不能跨类换算：%s", text)
	}
}

// 任务 3.3：未知单位与非法类型也是参数错误。
func TestUnitConvertInvalidInput(t *testing.T) {
	for _, args := range []map[string]any{
		{"value": 1, "from": "parsec", "to": "m"},
		{"value": "abc", "from": "m", "to": "km"},
	} {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "unit_convert", "arguments": args}})
		payload := decodeResponse(t, callRPC(t, string(body), testToken, "POST"))
		if code, _ := parseRPCError(t, payload); code != codeInvalidParams {
			t.Fatalf("%v 应返回参数错误，实际 %d", args, code)
		}
	}
}

// 工具函数本身可直接调用（不经过 RPC），便于宿主侧复用同一套语义。
func TestToolsCallableDirectly(t *testing.T) {
	list := tools()
	if len(list) != 2 {
		t.Fatalf("工具数量应为 2，实际 %d", len(list))
	}
	text, err := list[1].Call(context.Background(), map[string]any{"value": 1.0, "from": "m", "to": "cm"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "100 cm") {
		t.Fatalf("直接调用结果不符：%s", text)
	}
}
