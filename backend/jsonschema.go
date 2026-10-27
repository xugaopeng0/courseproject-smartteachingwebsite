package main

import (
	"encoding/json"
	"fmt"
	"sort"
)

// validateArguments 按工具声明的输入结构（JSON Schema 子集）校验参数。
// 支持 type / required / properties / enum / minimum / maximum / minLength / maxLength / items；
// 未在 properties 中声明的参数按忽略处理。
//
// mcp-server/schema.go 是本文件的镜像实现：api 与 mcp-server 是两个独立的构建上下文，
// 共用一个包需要改动既有 Dockerfile 与上下文，收益不抵风险。两处使用同一组测试用例表。
func validateArguments(schema map[string]any, args map[string]any) error {
	if args == nil {
		args = map[string]any{}
	}
	if typ, _ := schema["type"].(string); typ != "object" {
		return nil
	}
	if required, ok := schema["required"].([]any); ok {
		for _, item := range required {
			name, _ := item.(string)
			if name == "" {
				continue
			}
			if _, exists := args[name]; !exists {
				return fmt.Errorf("缺少必填参数 %s", name)
			}
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		return nil
	}
	names := make([]string, 0, len(args))
	for name := range args {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		property, ok := properties[name].(map[string]any)
		if !ok {
			continue
		}
		if err := validateValue(name, property, args[name]); err != nil {
			return err
		}
	}
	return nil
}

func validateValue(name string, schema map[string]any, value any) error {
	if value == nil {
		return fmt.Errorf("参数 %s 不能为空", name)
	}
	switch typ, _ := schema["type"].(string); typ {
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("参数 %s 应为字符串", name)
		}
		if err := checkEnum(name, schema, text); err != nil {
			return err
		}
		length := len([]rune(text))
		if min, ok := asFloat(schema["minLength"]); ok && float64(length) < min {
			return fmt.Errorf("参数 %s 至少需要 %d 个字符", name, int(min))
		}
		if max, ok := asFloat(schema["maxLength"]); ok && float64(length) > max {
			return fmt.Errorf("参数 %s 最多 %d 个字符", name, int(max))
		}
	case "number":
		number, ok := numberOf(value)
		if !ok {
			return fmt.Errorf("参数 %s 应为数字", name)
		}
		return checkRange(name, schema, number)
	case "integer":
		number, ok := numberOf(value)
		if !ok || number != float64(int64(number)) {
			return fmt.Errorf("参数 %s 应为整数", name)
		}
		return checkRange(name, schema, number)
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("参数 %s 应为布尔值", name)
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("参数 %s 应为数组", name)
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for index, item := range items {
				if err := validateValue(fmt.Sprintf("%s[%d]", name, index), itemSchema, item); err != nil {
					return err
				}
			}
		}
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("参数 %s 应为对象", name)
		}
		return validateArguments(schema, object)
	}
	return nil
}

func checkEnum(name string, schema map[string]any, value any) error {
	allowed, ok := schema["enum"].([]any)
	if !ok || len(allowed) == 0 {
		return nil
	}
	for _, item := range allowed {
		if item == value {
			return nil
		}
	}
	return fmt.Errorf("参数 %s 取值不合法，允许：%v", name, allowed)
}

func checkRange(name string, schema map[string]any, value float64) error {
	if min, ok := asFloat(schema["minimum"]); ok && value < min {
		return fmt.Errorf("参数 %s 不能小于 %v", name, min)
	}
	if max, ok := asFloat(schema["maximum"]); ok && value > max {
		return fmt.Errorf("参数 %s 不能大于 %v", name, max)
	}
	return nil
}

func asFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	}
	return 0, false
}

// numberOf 同时接受 JSON 解码得到的 float64 与 Go 调用方直接传入的整数。
func numberOf(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	}
	return 0, false
}
