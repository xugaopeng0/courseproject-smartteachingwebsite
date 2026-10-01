package main

import (
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
}

// chatMessage 是对话模块的消息；服务端只接受 user 与 assistant 两种角色。
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
