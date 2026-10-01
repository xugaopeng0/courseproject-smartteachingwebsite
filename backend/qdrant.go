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

// vectorStore 是访问 Qdrant 的最小 REST 客户端：建集合、写点、按班级过滤检索、按讲义删除。
// 只保存标识类 payload；切片正文与授权始终以关系库为权威。
type vectorStore struct {
	baseURL    string
	apiKey     string
	collection string
	dimensions int
	http       *http.Client
}

type vectorPoint struct {
	ID      string         `json:"id"`
	Vector  []float32      `json:"vector"`
	Payload map[string]any `json:"payload"`
}

type vectorHit struct {
	ID      string
	Score   float64
	Payload map[string]any
}

func newVectorStore(dimensions int) *vectorStore {
	return &vectorStore{
		baseURL:    strings.TrimRight(env("QDRANT_URL", "http://qdrant:6333"), "/"),
		apiKey:     env("QDRANT_API_KEY", ""),
		collection: env("QDRANT_COLLECTION", "campusclaw_chunks"),
		dimensions: dimensions,
		http:       &http.Client{Timeout: 30 * time.Second},
	}
}

func (v *vectorStore) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, v.baseURL+path, reader)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if v.apiKey != "" {
		req.Header.Set("api-key", v.apiKey)
	}
	resp, err := v.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("向量库不可用: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("向量库返回状态 %d", resp.StatusCode)
	}
	if out != nil {
		var envelope struct {
			Result json.RawMessage `json:"result"`
		}
		if err = json.Unmarshal(payload, &envelope); err != nil {
			return resp.StatusCode, err
		}
		if len(envelope.Result) > 0 && string(envelope.Result) != "null" {
			if err = json.Unmarshal(envelope.Result, out); err != nil {
				return resp.StatusCode, err
			}
		}
	}
	return resp.StatusCode, nil
}

// ensureCollection 在集合缺失时按配置维度创建；维度不一致时拒绝启动，不覆盖已有向量。
func (v *vectorStore) ensureCollection(ctx context.Context) error {
	if v.dimensions <= 0 {
		return errors.New("嵌入维度必须为正整数")
	}
	var info struct {
		Config struct {
			Params struct {
				Vectors struct {
					Size     int    `json:"size"`
					Distance string `json:"distance"`
				} `json:"vectors"`
			} `json:"params"`
		} `json:"config"`
	}
	status, err := v.do(ctx, http.MethodGet, "/collections/"+v.collection, nil, &info)
	if err == nil {
		if info.Config.Params.Vectors.Size != v.dimensions {
			return fmt.Errorf("向量集合 %s 的维度为 %d，与配置的 %d 不一致；请改用匹配的嵌入模型或新集合", v.collection, info.Config.Params.Vectors.Size, v.dimensions)
		}
		if !strings.EqualFold(info.Config.Params.Vectors.Distance, "Cosine") {
			return fmt.Errorf("向量集合 %s 的度量方式为 %s，不是 Cosine", v.collection, info.Config.Params.Vectors.Distance)
		}
		return nil
	}
	if status != http.StatusNotFound {
		return err
	}
	body := map[string]any{"vectors": map[string]any{"size": v.dimensions, "distance": "Cosine"}}
	if _, err = v.do(ctx, http.MethodPut, "/collections/"+v.collection, body, nil); err != nil {
		return fmt.Errorf("创建向量集合失败: %w", err)
	}
	return nil
}

func (v *vectorStore) upsert(ctx context.Context, points []vectorPoint) error {
	if len(points) == 0 {
		return nil
	}
	for _, point := range points {
		if len(point.Vector) != v.dimensions {
			return fmt.Errorf("向量维度 %d 与配置的 %d 不一致", len(point.Vector), v.dimensions)
		}
	}
	body := map[string]any{"points": points}
	if _, err := v.do(ctx, http.MethodPut, "/collections/"+v.collection+"/points?wait=true", body, nil); err != nil {
		return fmt.Errorf("写入向量失败: %w", err)
	}
	return nil
}

// search 只按班级（可选再按讲义）过滤检索，返回主键与分数；正文一律回关系库读取。
func (v *vectorStore) search(ctx context.Context, vector []float32, classID string, handoutIDs []string, limit int, threshold float64) ([]vectorHit, error) {
	must := []any{map[string]any{"key": "class_id", "match": map[string]any{"value": classID}}}
	if len(handoutIDs) > 0 {
		must = append(must, map[string]any{"key": "handout_id", "match": map[string]any{"any": handoutIDs}})
	}
	body := map[string]any{
		"vector":          vector,
		"limit":           limit,
		"with_payload":    true,
		"score_threshold": threshold,
		"filter":          map[string]any{"must": must},
	}
	var hits []struct {
		ID      any            `json:"id"`
		Score   float64        `json:"score"`
		Payload map[string]any `json:"payload"`
	}
	if _, err := v.do(ctx, http.MethodPost, "/collections/"+v.collection+"/points/search", body, &hits); err != nil {
		return nil, fmt.Errorf("向量检索失败: %w", err)
	}
	out := make([]vectorHit, 0, len(hits))
	for _, hit := range hits {
		id, _ := hit.ID.(string)
		out = append(out, vectorHit{ID: id, Score: hit.Score, Payload: hit.Payload})
	}
	return out, nil
}

// deleteByHandout 清理某份讲义的全部向量点；失败只记录日志，不影响关系库已经提交的删除。
func (v *vectorStore) deleteByHandout(ctx context.Context, classID, handoutID string) error {
	body := map[string]any{"filter": map[string]any{"must": []any{
		map[string]any{"key": "class_id", "match": map[string]any{"value": classID}},
		map[string]any{"key": "handout_id", "match": map[string]any{"value": handoutID}},
	}}}
	_, err := v.do(ctx, http.MethodPost, "/collections/"+v.collection+"/points/delete?wait=true", body, nil)
	return err
}

// deleteStaleGenerations 删除同一讲义中不属于当前代次的向量点。
// 即使清理失败，检索侧仍会因代次校验而看不到这些点。
func (v *vectorStore) deleteStaleGenerations(ctx context.Context, classID, handoutID, keepGenerationID string) error {
	body := map[string]any{"filter": map[string]any{
		"must": []any{
			map[string]any{"key": "class_id", "match": map[string]any{"value": classID}},
			map[string]any{"key": "handout_id", "match": map[string]any{"value": handoutID}},
		},
		"must_not": []any{
			map[string]any{"key": "generation_id", "match": map[string]any{"value": keepGenerationID}},
		},
	}}
	_, err := v.do(ctx, http.MethodPost, "/collections/"+v.collection+"/points/delete?wait=true", body, nil)
	return err
}
