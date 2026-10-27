// 课程网关的本地确定性替身（仅用于功能验证，不用于正式验收）。
//
// 它同时提供兼容 OpenAI 的嵌入与对话接口，让「索引 → 检索 → 问答」整条链路在没有真实网关
// 的情况下也能跑通与断言。这里的向量由字符二元组做特征哈希得到，是个确定性的词面近似，
// 不是语义模型：它可以用来验证权限、隔离、状态、引用顺序与前端流程，不能用来证明召回质量。
//
// 构建（在宿主机上交叉编译成静态二进制）：
//
//	cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../.local/gateway-stub ./cmd/gateway-stub
package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const dimensions = 1024

// 调用计数：接口测试用它验证「无命中时不调用对话模型」这类外部可观测的行为。
var (
	embeddingCalls atomic.Int64
	chatCalls      atomic.Int64
)

// embed 把文本映射成固定维度的确定性向量：字符二元组特征哈希后归一化。
func embed(text string) []float32 {
	vector := make([]float32, dimensions)
	runes := []rune(strings.ToLower(text))
	if len(runes) == 1 {
		vector[hashIndex(string(runes[0]))]++
	}
	for i := 0; i+1 < len(runes); i++ {
		vector[hashIndex(string(runes[i:i+2]))]++
	}
	var norm float64
	for _, value := range vector {
		norm += float64(value * value)
	}
	if norm == 0 {
		vector[0] = 1
		return vector
	}
	scale := float32(1 / math.Sqrt(norm))
	for i := range vector {
		vector[i] *= scale
	}
	return vector
}

func hashIndex(gram string) int {
	sum := sha256.Sum256([]byte(gram))
	return int(binary.BigEndian.Uint32(sum[:4]) % dimensions)
}

func main() {
	address := ":" + env("PORT", "8080")
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})
	// 同一组处理器同时挂在根路径与 /v1 前缀下：真实网关两种写法都有。
	embeddings := func(w http.ResponseWriter, r *http.Request) {
		embeddingCalls.Add(1)
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		data := make([]map[string]any, 0, len(body.Input))
		for _, text := range body.Input {
			data = append(data, map[string]any{"embedding": embed(text), "index": len(data)})
		}
		writeJSON(w, map[string]any{"object": "list", "model": body.Model, "data": data})
	}
	mux.HandleFunc("/embeddings", embeddings)
	mux.HandleFunc("/v1/embeddings", embeddings)
	chat := func(w http.ResponseWriter, r *http.Request) {
		chatCalls.Add(1)
		var body struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
			Tools  []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		question := ""
		excerpt := ""
		toolResult := ""
		hasToolResult := false
		for _, message := range body.Messages {
			if message.Role == "tool" {
				toolResult = message.Content
				hasToolResult = true
			}
			if message.Role == "user" {
				question = message.Content
				if index := strings.Index(question, "问题："); index >= 0 {
					question = question[index+len("问题："):]
				}
				if start := strings.Index(message.Content, "[1] 材料："); start >= 0 {
					if end := strings.Index(message.Content[start:], "\n\n"); end > 0 {
						excerpt = strings.TrimSpace(message.Content[start : start+end])
					}
				}
			}
		}
		// 工具调用：只有在声明了 formula_hint 且问题含「公式」、且还没有工具结果时才调用。
		// 这样「无候选集」与「已有结果」两条路径都不会触发，行为可预期。
		wantsTool := false
		for _, item := range body.Tools {
			if item.Function.Name == "formula_hint" {
				wantsTool = true
			}
		}
		if wantsTool && !hasToolResult && strings.Contains(question, "公式") {
			if body.Stream {
				writeToolCallSSE(w, body.Model, "formula_hint", `{"topic":"总评计算"}`)
				return
			}
			writeJSON(w, map[string]any{
				"object": "chat.completion",
				"model":  body.Model,
				"choices": []map[string]any{{
					"index":         0,
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role":    "assistant",
						"content": "",
						"tool_calls": []map[string]any{{
							"id":       "call_stub_1",
							"type":     "function",
							"function": map[string]any{"name": "formula_hint", "arguments": `{"topic":"总评计算"}`},
						}},
					},
				}},
			})
			return
		}
		answer := "替身回答：材料中没有足够依据。"
		if hasToolResult && toolResult != "" {
			answer = fmt.Sprintf("依据工具结果：%s 因此「%s」的答案以该结果为准。", firstLine(toolResult), truncate(question, 30))
		} else if excerpt != "" {
			// 确定性回答：复述第一条材料的开头并标注 [1]，用于验证引用与出处顺序。
			answer = fmt.Sprintf("依据材料[1]：%s 因此「%s」的答案以该片段为准。", firstLine(excerpt), truncate(question, 30))
		}
		if body.Stream {
			writeSSE(w, body.Model, answer)
			return
		}
		writeJSON(w, map[string]any{
			"object": "chat.completion",
			"model":  body.Model,
			"choices": []map[string]any{{
				"index":         0,
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": answer},
			}},
		})
	}
	mux.HandleFunc("/chat/completions", chat)
	mux.HandleFunc("/v1/chat/completions", chat)
	// 调用计数：接口测试据此断言「无命中不调用对话模型」。
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"embeddings": embeddingCalls.Load(),
			"chat":       chatCalls.Load(),
		})
	})
	log.Printf("gateway stub listening on %s", address)
	if err := http.ListenAndServe(address, mux); err != nil {
		log.Fatal(err)
	}
}

// writeToolCallSSE 返回一次流式工具调用：与 OpenAI 的 delta.tool_calls 形状一致。
func writeToolCallSSE(w http.ResponseWriter, model, name, arguments string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	payload, _ := json.Marshal(map[string]any{
		"object": "chat.completion.chunk",
		"model":  model,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{"tool_calls": []map[string]any{{
				"index":    0,
				"id":       "call_stub_1",
				"type":     "function",
				"function": map[string]any{"name": name, "arguments": arguments},
			}}},
		}},
	})
	fmt.Fprintf(w, "data: %s\n\n", payload)
	flusher.Flush()
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func firstLine(text string) string {
	if index := strings.Index(text, "\n"); index > 0 {
		text = text[index+1:]
	}
	return truncate(strings.TrimSpace(text), 40)
}

func truncate(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(payload)
}

// writeSSE 按 OpenAI 的流式格式逐块返回：本地开发时用来验证逐字显示与中断处理。
// 分片间隔可以用 STUB_CHUNK_DELAY_MS 调大，便于人工观察或测试「停止生成」。
func writeSSE(w http.ResponseWriter, model, answer string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	delay := 40 * time.Millisecond
	if raw := os.Getenv("STUB_CHUNK_DELAY_MS"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			delay = time.Duration(parsed) * time.Millisecond
		}
	}
	runes := []rune(answer)
	const chunkSize = 6
	for start := 0; start < len(runes); start += chunkSize {
		end := start + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		payload, _ := json.Marshal(map[string]any{
			"object": "chat.completion.chunk",
			"model":  model,
			"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]any{"content": string(runes[start:end])},
			}},
		})
		fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
		time.Sleep(delay)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
