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
	"strings"
	"unicode/utf8"
)

const dimensions = 1024

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
	mux.HandleFunc("/embeddings", func(w http.ResponseWriter, r *http.Request) {
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
	})
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string `json:"model"`
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
		for _, message := range body.Messages {
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
		answer := "替身回答：材料中没有足够依据。"
		if excerpt != "" {
			// 确定性回答：复述第一条材料的开头并标注 [1]，用于验证引用与出处顺序。
			answer = fmt.Sprintf("依据材料[1]：%s 因此「%s」的答案以该片段为准。", firstLine(excerpt), truncate(question, 30))
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
	})
	log.Printf("gateway stub listening on %s", address)
	if err := http.ListenAndServe(address, mux); err != nil {
		log.Fatal(err)
	}
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

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
