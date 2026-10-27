package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// tool 描述一个工具：名称、说明、输入结构与执行函数。
//
// 说明要写清「何时适用、何时不适用」——它决定模型会不会选这个工具；
// 输入结构用 JSON Schema 子集表达，宿主会按同一份结构校验模型填的参数。
type tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Call        func(ctx context.Context, args map[string]any) (string, error)
}

// tools 返回工具清单（按名称排序，保证 tools/list 顺序稳定）。
func tools() []tool {
	list := []tool{
		{
			Name: "formula_hint",
			Description: "当学生询问某个公式的构成、符号含义或解题思路时使用（教学场景）。" +
				"只给出公式、符号解释与可自行尝试的下一步，不做数值代入、不给最终数值答案。" +
				"不适用于查课程政策、讲义检索、非计算类问题；需要数值结果时应由学生自己代入计算。",
			InputSchema: mustSchema(`{
				"type": "object",
				"required": ["topic"],
				"properties": {
					"topic": {"type": "string", "minLength": 1, "maxLength": 200},
					"level": {"type": "string", "enum": ["beginner", "intermediate", "advanced"]}
				}
			}`),
			Call: formulaHint,
		},
		{
			Name: "unit_convert",
			Description: "当用户明确要求把一个带单位的值换算成另一个同类量的单位时使用。" +
				"只做同类量的纯单位换算（长度、质量、时间、容量），不做货币汇率、不做自定义单位，" +
				"也不判断数值的业务含义。跨类换算（例如长度换质量）会被拒绝。",
			InputSchema: mustSchema(`{
				"type": "object",
				"required": ["value", "from", "to"],
				"properties": {
					"value": {"type": "number"},
					"from": {"type": "string", "enum": ["m", "cm", "km", "kg", "g", "s", "min", "h", "L", "mL"]},
					"to": {"type": "string", "enum": ["m", "cm", "km", "kg", "g", "s", "min", "h", "L", "mL"]}
				}
			}`),
			Call: unitConvert,
		},
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

func mustSchema(raw string) map[string]any {
	var schema map[string]any
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		panic("工具输入结构不是合法 JSON: " + err.Error())
	}
	return schema
}

// formulaHint 按主题返回公式、符号说明与下一步提示。内容是可读的数据，
// 不含任何面向模型或宿主的指令式语句。
type hint struct {
	keywords []string
	formula  string
	symbols  string
	next     string
}

var hints = []hint{
	{
		keywords: []string{"总评", "加权", "权重", "成绩构成"},
		formula:  "总评 = 平时作业得分 × 40% + 期末项目得分 × 60%",
		symbols:  "40% 与 60% 是两项权重，二者相加为 1；两个得分都按满分 100 计。",
		next:     "先把两项得分分别乘以权重，再把两个结果相加；最后按题目要求决定是否取整。",
	},
	{
		keywords: []string{"平均", "均值", "平均分"},
		formula:  "平均分 = 总分 ÷ 门数",
		symbols:  "总分是所有参与计算的分值之和，门数是科目数量；若按学分加权，则分母改为学分之和。",
		next:     "先确认分母是「科目数」还是「学分之和」，再决定是否需要加权。",
	},
	{
		keywords: []string{"增长率", "增幅", "同比增长"},
		formula:  "增长率 = (本期值 − 上期值) ÷ 上期值 × 100%",
		symbols:  "本期值是当前周期的数值，上期值是作为基准的数值；基准为 0 时公式无意义。",
		next:     "先确认哪一期是基准，再决定分子里的减法顺序。",
	},
	{
		keywords: []string{"速度", "路程", "时间"},
		formula:  "速度 = 路程 ÷ 时间",
		symbols:  "路程与时间的单位要匹配（例如米配秒），否则需要先统一单位。",
		next:     "先检查已知量的单位是否一致，再代入。",
	},
	{
		keywords: []string{"密度", "质量", "体积"},
		formula:  "密度 = 质量 ÷ 体积",
		symbols:  "质量常用克或千克，体积常用立方厘米或升；单位不同需要先换算。",
		next:     "先统一质量的单位与体积的单位，再相除。",
	},
	{
		keywords: []string{"占比", "百分比", "比例", "百分比"},
		formula:  "占比 = 部分 ÷ 总量 × 100%",
		symbols:  "部分是被考察的那一份，总量是所有份之和；总量为 0 时公式无意义。",
		next:     "先确认「部分」与「总量」分别指什么，再决定是否需要乘以 100%。",
	},
}

func formulaHint(_ context.Context, args map[string]any) (string, error) {
	topic, _ := args["topic"].(string)
	topic = strings.TrimSpace(topic)
	level, _ := args["level"].(string)
	if level == "" {
		level = "beginner"
	}
	levelName := map[string]string{"beginner": "入门", "intermediate": "进阶", "advanced": "熟练"}[level]

	picked, matched := hint{}, false
	for _, candidate := range hints {
		for _, keyword := range candidate.keywords {
			if strings.Contains(topic, keyword) {
				picked, matched = candidate, true
				break
			}
		}
		if matched {
			break
		}
	}

	var builder strings.Builder
	builder.WriteString("主题：" + topic + "（" + levelName + "）\n")
	if matched {
		builder.WriteString("公式：" + picked.formula + "\n")
		builder.WriteString("符号：" + picked.symbols + "\n")
		builder.WriteString("下一步：" + picked.next + "\n")
	} else {
		builder.WriteString("未匹配到常见公式。通用思路：\n")
		builder.WriteString("1. 写出题目里的已知量与目标量，并标出单位；\n")
		builder.WriteString("2. 想清楚这几个量之间是加减、乘除还是比例关系；\n")
		builder.WriteString("3. 先把关系写成等式，再考虑代入数值。\n")
	}
	switch level {
	case "beginner":
		builder.WriteString("提示：先把已知量抄成一行，再逐个标上单位，能减少代错位置。\n")
	case "advanced":
		builder.WriteString("边界：若出现除数为 0、单位不一致或基准缺失，先处理这些条件再代入。\n")
	}
	return builder.String(), nil
}

// unit 记录一个单位的类别与换算到该类基准单位的系数。
type unit struct {
	category string
	factor   float64
}

var units = map[string]unit{
	"m":  {"length", 1},
	"cm": {"length", 0.01},
	"km": {"length", 1000},
	"g":  {"mass", 0.001},
	"kg": {"mass", 1},
	"s":  {"time", 1},
	"min": {"time", 60},
	"h":  {"time", 3600},
	"mL": {"volume", 0.001},
	"L":  {"volume", 1},
}

var categoryNames = map[string]string{"length": "长度", "mass": "质量", "time": "时间", "volume": "容量"}

func unitConvert(_ context.Context, args map[string]any) (string, error) {
	value, ok := args["value"].(float64)
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("参数 value 必须是有限数字")
	}
	from, _ := args["from"].(string)
	to, _ := args["to"].(string)
	source, ok := units[from]
	if !ok {
		return "", fmt.Errorf("不支持的单位 %s", from)
	}
	target, ok := units[to]
	if !ok {
		return "", fmt.Errorf("不支持的单位 %s", to)
	}
	if source.category != target.category {
		return "", fmt.Errorf("不能把%s换算成%s（%s → %s）", categoryNames[source.category], categoryNames[target.category], from, to)
	}
	result := value * source.factor / target.factor
	return fmt.Sprintf("%s %s = %s %s（%s换算）", trimNumber(value), from, trimNumber(result), to, categoryNames[source.category]), nil
}

func trimNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
