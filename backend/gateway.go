package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// gatewayClient 通过课程网关调用模型。地址、模型名与密钥只存在于服务端配置，
// 不下发浏览器；失败时明确报错，不改发其他服务。
//
// 嵌入与对话是两个独立模块：各自可以配置不同的服务端地址与密钥（例如嵌入用本地模型、
// 对话用云端模型）。未单独配置时回退到共享的 GATEWAY_* 配置。
type gatewayClient struct {
	baseURL    string
	apiKey     string
	embedModel string
	chatModel  string
	chatURL    string
	chatKey    string
	dimensions int
	timeout    time.Duration
	http       *http.Client
	streamHTTP *http.Client
}

// chatMessage 是对话模块的消息；服务端只接受 user 与 assistant 两种角色。
// 工具调用沿用 OpenAI 兼容协议：assistant 消息带 tool_calls，工具结果以 tool 角色回传。
type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []toolCallWire `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// toolCallWire 是 assistant 消息里回传给模型的历史工具调用。
type toolCallWire struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// toolSpec 是随请求声明的工具：名称、说明与输入结构决定模型是否会选它。
type toolSpec struct {
	Type     string           `json:"type"`
	Function toolSpecFunction `json:"function"`
}

type toolSpecFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// toolCall 是模型提出的一次调用；arguments 是模型填的 JSON 字符串，需要宿主校验。
type toolCall struct {
	ID        string
	Name      string
	Arguments string
}

// streamRound 是一轮流式调用的结果：正文、工具调用与是否正常结束。
type streamRound struct {
	Content   string
	ToolCalls []toolCall
}

func newGatewayClient(dimensions int) *gatewayClient {
	timeout, err := time.ParseDuration(env("GATEWAY_TIMEOUT", "30s"))
	if err != nil || timeout <= 0 {
		timeout = 30 * time.Second
	}
	shared := strings.TrimRight(osEnv("GATEWAY_BASE_URL"), "/")
	sharedKey := osEnv("GATEWAY_API_KEY")
	embeddingURL := strings.TrimRight(env("EMBEDDING_BASE_URL", shared), "/")
	chatURL := strings.TrimRight(env("CHAT_BASE_URL", shared), "/")
	return &gatewayClient{
		baseURL:    embeddingURL,
		apiKey:     env("EMBEDDING_API_KEY", sharedKey),
		embedModel: osEnv("EMBEDDING_MODEL"),
		chatModel:  osEnv("CHAT_MODEL"),
		chatURL:    chatURL,
		chatKey:    env("CHAT_API_KEY", sharedKey),
		dimensions: dimensions,
		timeout:    timeout,
		http:       &http.Client{Timeout: timeout + 5*time.Second},
	}
}

func osEnv(key string) string { return env(key, "") }

// httpClient 延迟创建 HTTP 客户端，避免调用方漏填字段时出现 nil 解引用。
func (g *gatewayClient) httpClient() *http.Client {
	if g.http == nil {
		g.http = &http.Client{Timeout: g.timeout + 5*time.Second}
	}
	return g.http
}

func (g *gatewayClient) configured() bool {
	return g.baseURL != "" && g.embedModel != ""
}

// chatEndpoint 返回对话通道的地址与密钥；未单独配置时沿用嵌入通道的地址（同一个网关）。
func (g *gatewayClient) chatEndpoint() (string, string) {
	if g.chatURL != "" {
		return g.chatURL, g.chatKey
	}
	return g.baseURL, g.apiKey
}

// chatConfigured 判断对话通道是否可用；嵌入与对话是两个独立模块，配置互不代偿。
func (g *gatewayClient) chatConfigured() bool {
	base, _ := g.chatEndpoint()
	return base != "" && g.chatModel != ""
}

func (g *gatewayClient) post(ctx context.Context, baseURL, apiKey, path string, body any, out any) error {
	if baseURL == "" {
		return errors.New("课程网关未配置")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := g.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("课程网关不可用: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("课程网关返回状态 %d", resp.StatusCode)
	}
	if out != nil {
		if err = json.Unmarshal(payload, out); err != nil {
			return fmt.Errorf("课程网关响应无法解析: %w", err)
		}
	}
	return nil
}

// embed 把文本转换为向量。返回结果必须与请求条数、配置维度一致，且不含非有限值或零向量。
func (g *gatewayClient) embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	if !g.configured() {
		return nil, errors.New("课程网关未配置嵌入模型")
	}
	var response struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := g.post(ctx, g.baseURL, g.apiKey, "/embeddings", map[string]any{"model": g.embedModel, "input": inputs}, &response); err != nil {
		return nil, err
	}
	vectors := make([][]float32, len(response.Data))
	for i, item := range response.Data {
		vectors[i] = item.Embedding
	}
	if err := validateEmbeddings(vectors, len(inputs), g.dimensions); err != nil {
		return nil, err
	}
	return vectors, nil
}

// chat 调用对话接口撰写简短回答。这是与嵌入完全独立的第二次调用。
func (g *gatewayClient) chat(ctx context.Context, messages []chatMessage) (string, error) {
	if !g.chatConfigured() {
		return "", errors.New("课程网关未配置对话模型")
	}
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	body := map[string]any{"model": g.chatModel, "messages": messages, "stream": false}
	base, key := g.chatEndpoint()
	if err := g.post(ctx, base, key, "/chat/completions", body, &response); err != nil {
		return "", err
	}
	if len(response.Choices) == 0 {
		return "", errors.New("课程网关未返回任何回答")
	}
	answer := strings.TrimSpace(response.Choices[0].Message.Content)
	if answer == "" {
		return "", errors.New("课程网关返回了空回答")
	}
	return answer, nil
}

// streamClient 是流式专用客户端：只限制「首字节（响应头）超时」，整体时长交给请求上下文，
// 因此长回答不会被客户端的整体超时截断。
func (g *gatewayClient) streamClient() *http.Client {
	if g.streamHTTP == nil {
		g.streamHTTP = &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: g.timeout,
			IdleConnTimeout:       90 * time.Second,
		}}
	}
	return g.streamHTTP
}

// chatStream 以流式方式调用对话接口，把每个增量文本交给 emit。
// 客户端断开时通过 ctx 取消上游请求；网关不支持流式时明确报错，绝不静默降级成整段返回。
func (g *gatewayClient) chatStream(ctx context.Context, messages []chatMessage, emit func(string) error) error {
	if !g.chatConfigured() {
		return errors.New("课程网关未配置对话模型")
	}
	base, key := g.chatEndpoint()
	raw, err := json.Marshal(map[string]any{"model": g.chatModel, "messages": messages, "stream": true})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := g.streamClient().Do(req)
	if err != nil {
		return fmt.Errorf("课程网关不可用: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("课程网关返回状态 %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	emitted := false
	finished := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		// 网关忽略 stream 参数时会直接返回整段 JSON：明确报错而不是当作流式。
		if strings.HasPrefix(line, "{") {
			return errors.New("课程网关未按流式返回（收到整段响应）")
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			finished = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err = json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("课程网关流式分片无法解析: %w", err)
		}
		if chunk.Error != nil {
			return fmt.Errorf("课程网关返回错误: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		text := chunk.Choices[0].Delta.Content
		if text == "" {
			text = chunk.Choices[0].Message.Content
		}
		if text == "" {
			continue
		}
		if err = emit(text); err != nil {
			return err
		}
		emitted = true
	}
	if err = scanner.Err(); err != nil {
		return fmt.Errorf("读取流式响应失败: %w", err)
	}
	if !emitted {
		return errors.New("课程网关未返回任何内容")
	}
	// 没有收到 [DONE] 就结束，说明流被截断：按中断处理，绝不当作完整回答。
	if !finished {
		return errors.New("课程网关流式响应未正常结束")
	}
	return nil
}

// chatStreamRound 是带工具声明的流式调用：正文增量交给 emit，工具调用在整轮结束后返回。
//
// 与 chatStream 的区别只在于多解析 delta.tool_calls 并把它们累积成完整调用；
// 没有工具调用时行为与 chatStream 完全一致（同样拒绝截断与非流式响应）。
func (g *gatewayClient) chatStreamRound(ctx context.Context, messages []chatMessage, specs []toolSpec, emit func(string) error) (streamRound, error) {
	round := streamRound{}
	if !g.chatConfigured() {
		return round, errors.New("课程网关未配置对话模型")
	}
	body := map[string]any{"model": g.chatModel, "messages": messages, "stream": true}
	if len(specs) > 0 {
		body["tools"] = specs
		body["tool_choice"] = "auto"
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return round, err
	}
	base, key := g.chatEndpoint()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return round, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := g.streamClient().Do(req)
	if err != nil {
		return round, fmt.Errorf("课程网关不可用: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return round, fmt.Errorf("课程网关返回状态 %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}

	type partialTool struct {
		ID   string
		Name string
		Args strings.Builder
	}
	byIndex := map[int]*partialTool{}
	order := []int{}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	emitted := false
	finished := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "{") {
			return round, errors.New("课程网关未按流式返回（收到整段响应）")
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			finished = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err = json.Unmarshal([]byte(payload), &chunk); err != nil {
			return round, fmt.Errorf("课程网关流式分片无法解析: %w", err)
		}
		if chunk.Error != nil {
			return round, fmt.Errorf("课程网关返回错误: %s", chunk.Error.Message)
		}
		for _, choice := range chunk.Choices {
			text := choice.Delta.Content
			if text == "" {
				text = choice.Message.Content
			}
			if text != "" {
				round.Content += text
				if emit != nil {
					if err = emit(text); err != nil {
						return round, err
					}
				}
				emitted = true
			}
			for _, call := range choice.Delta.ToolCalls {
				slot, ok := byIndex[call.Index]
				if !ok {
					slot = &partialTool{}
					byIndex[call.Index] = slot
					order = append(order, call.Index)
				}
				if call.ID != "" {
					slot.ID = call.ID
				}
				slot.Name += call.Function.Name
				slot.Args.WriteString(call.Function.Arguments)
			}
		}
	}
	if err = scanner.Err(); err != nil {
		return round, fmt.Errorf("读取流式响应失败: %w", err)
	}
	for _, index := range order {
		slot := byIndex[index]
		round.ToolCalls = append(round.ToolCalls, toolCall{ID: slot.ID, Name: slot.Name, Arguments: slot.Args.String()})
	}
	if !emitted && len(round.ToolCalls) == 0 {
		return round, errors.New("课程网关未返回任何内容")
	}
	if !finished {
		return round, errors.New("课程网关流式响应未正常结束")
	}
	return round, nil
}
