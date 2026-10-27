package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// 工具调用编排：模型决定调用 → 宿主校验参数并执行 → 结果净化后回传 → 模型组织回答。
// 工具是增益：任何失败都退化为「用已有信息作答」，不让主路径失败。

const (
	maxToolResultRunes = 1200
	maxToolDetailRunes = 300
)

var instructionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)忽略(以上|之前|前面|上述)?(的)?(所有)?(指令|规则|约束|要求)`),
	regexp.MustCompile(`(?i)ignore\s+(all\s+|the\s+)?(previous|above|prior)`),
	regexp.MustCompile(`(?i)你现在是|现在起你是|from now on you are`),
	regexp.MustCompile(`(?i)(你必须|务必|必须)(输出|回复|回答|执行)`),
	regexp.MustCompile(`(?i)system\s*[:：]|assistant\s*[:：]`),
	regexp.MustCompile(`(?i)you\s+must\s+(output|reply|respond|do)`),
	regexp.MustCompile(`(?i)instead,?\s+(you\s+)?(must|should)`),
	regexp.MustCompile(`(?i)请(直接)?(输出|返回)(以下|如下)`),
}

// sanitizeToolText 丢弃命中指令式句式的整句，并按字符上限截断。
// 返回的是数据：其中的约束性语句不得进入后续提示词。
func sanitizeToolText(text string) string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	sentences := splitSentences(normalized)
	kept := make([]string, 0, len(sentences))
	for _, sentence := range sentences {
		trimmed := strings.TrimSpace(sentence)
		if trimmed == "" {
			continue
		}
		if matchesInstruction(trimmed) {
			continue
		}
		kept = append(kept, trimmed)
	}
	result := strings.Join(kept, " ")
	if strings.TrimSpace(result) == "" {
		return "（工具返回的内容已被过滤）"
	}
	runes := []rune(result)
	if len(runes) > maxToolResultRunes {
		return string(runes[:maxToolResultRunes]) + "…（已截断）"
	}
	return result
}

func splitSentences(text string) []string {
	var out []string
	var current strings.Builder
	for _, char := range text {
		current.WriteRune(char)
		switch char {
		case '。', '！', '？', '!', '?', '\n':
			out = append(out, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

func matchesInstruction(sentence string) bool {
	for _, pattern := range instructionPatterns {
		if pattern.MatchString(sentence) {
			return true
		}
	}
	return false
}

// toolCallLimits 读取轮次与累计调用上限（便于验收时调小）。
func toolCallLimits() (rounds int, calls int) {
	rounds = 3
	calls = 4
	if parsed, err := strconv.Atoi(strings.TrimSpace(os.Getenv("CHAT_MAX_TOOL_ROUNDS"))); err == nil && parsed > 0 {
		rounds = parsed
	}
	if parsed, err := strconv.Atoi(strings.TrimSpace(os.Getenv("CHAT_MAX_TOOL_CALLS"))); err == nil && parsed >= 0 {
		calls = parsed
	}
	return rounds, calls
}

// toolEvent 是一次工具调用的可见状态：running / ok / rejected / error。
type toolEvent struct {
	Tool   string `json:"tool"`
	Server string `json:"server"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// orchestration 是编排所需的上下文。
type orchestration struct {
	classID   string
	sessionID string
	ownerID   string
	triggerID string

	candidates []toolCandidate
	messages   []chatMessage
}

// runChatWithTools 执行编排循环，正文增量交给 emit，工具状态通过 onTool 实时上报。
// 返回组装出的回答正文与本次用到的工具列表。
func (a *App) runChatWithTools(ctx context.Context, o orchestration, emit func(string) error, onTool func(toolEvent)) (string, []toolEvent, error) {
	maxRounds, maxCalls := toolCallLimits()
	specs := specsFromCandidates(o.candidates)
	messages := append([]chatMessage{}, o.messages...)
	used := []toolEvent{}
	callsLeft := maxCalls
	pending := map[string]toolCandidate{}
	for _, item := range o.candidates {
		pending[item.Tool] = item
	}
	var answer strings.Builder

	for round := 1; ; round++ {
		useTools := len(specs) > 0 && callsLeft > 0 && round <= maxRounds
		var declared []toolSpec
		if useTools {
			declared = specs
		}
		result, err := a.gateway.chatStreamRound(ctx, messages, declared, emit)
		if err != nil {
			return answer.String(), used, err
		}
		if result.Content != "" {
			answer.WriteString(result.Content)
		}
		if len(result.ToolCalls) == 0 {
			return answer.String(), used, nil
		}

		// 把模型的调用意图作为 assistant 消息写回对话，随后逐条追加工具结果。
		assistant := chatMessage{Role: "assistant"}
		wire := make([]toolCallWire, 0, len(result.ToolCalls))
		for _, call := range result.ToolCalls {
			item := toolCallWire{ID: call.ID, Type: "function"}
			item.Function.Name = call.Name
			item.Function.Arguments = call.Arguments
			wire = append(wire, item)
		}
		assistant.ToolCalls = wire
		messages = append(messages, assistant)

		for _, call := range result.ToolCalls {
			if callsLeft <= 0 {
				break
			}
			callsLeft--
			event := a.executeToolCall(ctx, o, call, pending, round, &used)
			onTool(event.toolEvent)
			messages = append(messages, chatMessage{Role: "tool", ToolCallID: call.ID, Content: event.contentForModel})
		}
		if len(result.ToolCalls) == 0 {
			return answer.String(), used, nil
		}
		if callsLeft <= 0 || round >= maxRounds {
			// 达到上限：不再声明工具，让模型基于已有信息收尾。
			specs = nil
			messages = append(messages, chatMessage{Role: "user", Content: "（工具调用已达上限。请基于已经获得的信息直接作答，不要再调用工具。）"})
			result, err = a.gateway.chatStreamRound(ctx, messages, nil, emit)
			if err != nil {
				return answer.String(), used, err
			}
			if result.Content != "" {
				answer.WriteString(result.Content)
			}
			if strings.TrimSpace(answer.String()) == "" {
				fallback := "已到工具调用上限，以下是已获得的信息：\n" + summariseToolResults(used)
				answer.WriteString(fallback)
				_ = emit(fallback)
			}
			return answer.String(), used, nil
		}
	}
}

// toolCallOutcome 是 executeToolCall 的内部结果，带一条事件与回传给模型的内容。
type toolCallOutcome struct {
	toolEvent
	contentForModel string
	outcome         string
}

func (a *App) executeToolCall(ctx context.Context, o orchestration, call toolCall, pending map[string]toolCandidate, round int, used *[]toolEvent) toolCallOutcome {
	candidate, ok := pending[call.Name]
	if !ok {
		return a.rejectToolCall(ctx, o, call, round, "该工具不在本次候选集内", used)
	}
	var args map[string]any
	if strings.TrimSpace(call.Arguments) != "" {
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			return a.rejectToolCall(ctx, o, call, round, "参数不是合法 JSON", used)
		}
	}
	if err := validateArguments(candidate.InputSchema, args); err != nil {
		return a.rejectToolCall(ctx, o, call, round, err.Error(), used)
	}
	// 调用开始后服务器可能被改名或删除：写记录前会重新核对，不匹配则跳过记录。
	text, toolFailed, err := callTool(ctx, candidate.Server, candidate.Tool, args)
	if err != nil {
		outcome := toolCallOutcome{
			toolEvent:       toolEvent{Tool: candidate.Tool, Server: candidate.Server.Name, Status: "error", Detail: truncateRunes(err.Error(), maxToolDetailRunes)},
			contentForModel: "工具调用失败：" + err.Error(),
			outcome:         "error",
		}
		a.recordToolCall(ctx, o, candidate, round, "error", err.Error())
		*used = append(*used, outcome.toolEvent)
		return outcome
	}
	clean := sanitizeToolText(text)
	status := "ok"
	outcome := "success"
	detail := ""
	if toolFailed {
		status = "error"
		outcome = "error"
		detail = truncateRunes(text, maxToolDetailRunes)
	}
	event := toolCallOutcome{
		toolEvent:       toolEvent{Tool: candidate.Tool, Server: candidate.Server.Name, Status: status, Detail: detail},
		contentForModel: clean,
		outcome:         outcome,
	}
	a.recordToolCall(ctx, o, candidate, round, outcome, detail)
	*used = append(*used, event.toolEvent)
	return event
}

func (a *App) rejectToolCall(ctx context.Context, o orchestration, call toolCall, round int, reason string, used *[]toolEvent) toolCallOutcome {
	serverName := ""
	for _, candidate := range o.candidates {
		if candidate.Tool == call.Name {
			serverName = candidate.Server.Name
		}
	}
	event := toolCallOutcome{
		toolEvent:       toolEvent{Tool: call.Name, Server: serverName, Status: "rejected", Detail: truncateRunes(reason, maxToolDetailRunes)},
		contentForModel: "本次调用被拒绝：" + reason + "。请基于已有信息作答。",
		outcome:         "rejected",
	}
	snapshot := toolCandidate{Tool: call.Name, Server: toolServerRow{Name: serverName, ID: ""}}
	a.recordToolCall(ctx, o, snapshot, round, "rejected", reason)
	*used = append(*used, event.toolEvent)
	return event
}

// recordToolCall 写一条只增不改的调用记录。
// 写前重新核对服务器：存在时名称必须与调用开始时一致（被改名则不写）；服务器已被删除时
// 若本次调用本来就不知道归属（例如模型点名了候选集之外的工具）仍如实记一条被拒记录。
func (a *App) recordToolCall(ctx context.Context, o orchestration, candidate toolCandidate, round int, outcome, detail string) {
	serverID := candidate.Server.ID
	serverName := candidate.Server.Name
	ownerID := candidate.Server.OwnerID
	if ownerID == "" {
		ownerID = o.ownerID
	}
	var serverIDValue any
	if serverID != "" {
		var currentName string
		if err := a.db.QueryRowContext(ctx, `SELECT name FROM tool_servers WHERE id=?`, serverID).Scan(&currentName); err != nil {
			// 服务器已被删除：本次调用无处归属，不写记录。
			return
		}
		if currentName != serverName {
			return
		}
		serverIDValue = serverID
	}
	_, err := a.db.ExecContext(ctx, `INSERT INTO tool_calls(id,owner_id,class_id,session_id,turn,trigger_user_id,server_id,server_name,tool_name,outcome,detail)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		newID(), ownerID, o.classID, o.sessionID, round, o.triggerID, serverIDValue, serverName, candidate.Tool, outcome, truncateRunes(detail, 500))
	if err != nil {
		log.Printf("工具调用记录写入失败: %v", err)
	}
}

// summariseToolResults 在模型无法收尾时，用已获得的工具结果拼出兜底回答。
func summariseToolResults(events []toolEvent) string {
	if len(events) == 0 {
		return "本次没有获得可用的工具结果。"
	}
	var builder strings.Builder
	for _, event := range events {
		builder.WriteString("- " + event.Tool + "@" + event.Server + "：" + statusText(event.Status))
		if event.Detail != "" {
			builder.WriteString("（" + event.Detail + "）")
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

func statusText(status string) string {
	switch status {
	case "ok":
		return "成功"
	case "rejected":
		return "被拒"
	case "error":
		return "出错"
	}
	return status
}

func jsonUnmarshal(raw string, out any) error {
	return json.Unmarshal([]byte(raw), out)
}

var errToolLimit = errors.New("工具调用已达上限")
