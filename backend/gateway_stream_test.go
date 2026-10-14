package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func streamGateway(t *testing.T, handler http.HandlerFunc, timeout time.Duration) *gatewayClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &gatewayClient{baseURL: server.URL, embedModel: "embed-model", chatModel: "chat-model", dimensions: 4, timeout: timeout}
}

func sseDelta(w http.ResponseWriter, text string) {
	payload, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": text}}}})
	fmt.Fprintf(w, "data: %s\n\n", payload)
}

// 任务 4.2：正常流式读取、逐块回调、[DONE] 结束。
func TestChatStreamEmitsDeltas(t *testing.T) {
	client := streamGateway(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream   bool `json:"stream"`
			Messages []chatMessage `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !body.Stream {
			t.Errorf("流式调用必须带 stream=true")
		}
		if got := r.Header.Get("Accept"); !strings.Contains(got, "text/event-stream") {
			t.Errorf("应声明接受事件流，实际 %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, ": keep-alive\n\n")
		for _, chunk := range []string{"考核", "由平时", "作业组成"} {
			sseDelta(w, chunk)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}, 2*time.Second)

	var builder strings.Builder
	chunks := 0
	err := client.chatStream(context.Background(), []chatMessage{{Role: "user", Content: "问题"}}, func(text string) error {
		chunks++
		builder.WriteString(text)
		return nil
	})
	if err != nil {
		t.Fatalf("正常流式读取失败: %v", err)
	}
	if chunks != 3 || builder.String() != "考核由平时作业组成" {
		t.Fatalf("增量回调不符：chunks=%d text=%q", chunks, builder.String())
	}
}

// 任务 4.2：流被截断（没有 [DONE]）必须报错，不能当作完整回答。
func TestChatStreamRejectsTruncatedStream(t *testing.T) {
	client := streamGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		sseDelta(w, "只说了一半")
		flusher.Flush()
		// 直接结束，不发送 [DONE]。
	}, 2*time.Second)

	err := client.chatStream(context.Background(), []chatMessage{{Role: "user", Content: "问题"}}, func(string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "未正常结束") {
		t.Fatalf("截断的流应报错，实际 %v", err)
	}
}

// 任务 4.2：非 200、整段返回、空流、超时四种情形都要明确报错，且不静默降级。
func TestChatStreamFailures(t *testing.T) {
	cases := []struct {
		name    string
		timeout time.Duration
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "非 200", timeout: 2 * time.Second,
			handler: func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", http.StatusBadGateway) },
			want:    "502",
		},
		{
			name: "网关忽略 stream 参数", timeout: 2 * time.Second,
			handler: func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": "整段回答"}}}})
			},
			want: "未按流式返回",
		},
		{
			name: "只有结束标记", timeout: 2 * time.Second,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: [DONE]\n\n")
			},
			want: "未返回任何内容",
		},
		{
			name: "首字节超时", timeout: 150 * time.Millisecond,
			handler: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(600 * time.Millisecond)
				sseDelta(w, "迟到的回答")
			},
			want: "课程网关不可用",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := streamGateway(t, tc.handler, tc.timeout)
			err := client.chatStream(context.Background(), []chatMessage{{Role: "user", Content: "问题"}}, func(string) error { return nil })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("期望错误包含 %q，实际 %v", tc.want, err)
			}
		})
	}
}

// 任务 4.2：回调返回错误（客户端已断开）时立即停止读取上游。
func TestChatStreamStopsWhenEmitFails(t *testing.T) {
	closed := make(chan struct{})
	client := streamGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for i := 0; i < 50; i++ {
			sseDelta(w, "块")
			flusher.Flush()
			select {
			case <-r.Context().Done():
				close(closed)
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}, 5*time.Second)

	calls := 0
	stop := fmt.Errorf("客户端已断开")
	err := client.chatStream(context.Background(), []chatMessage{{Role: "user", Content: "问题"}}, func(string) error {
		calls++
		if calls >= 2 {
			return stop
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "客户端已断开") {
		t.Fatalf("应把回调错误原样返回，实际 %v", err)
	}
	if calls != 2 {
		t.Fatalf("回调应在第二次后停止，实际 %d 次", calls)
	}
}

// 未配置对话模型时明确报错。
func TestChatStreamRequiresConfiguration(t *testing.T) {
	client := &gatewayClient{dimensions: 4}
	if err := client.chatStream(context.Background(), []chatMessage{{Role: "user", Content: "问题"}}, func(string) error { return nil }); err == nil {
		t.Fatal("未配置对话模型时应报错")
	}
}
