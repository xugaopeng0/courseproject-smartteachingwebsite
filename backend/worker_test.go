package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tempFile(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func officeFile(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for filename, content := range files {
		w, e := z.Create(filename)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write([]byte(content)); e != nil {
			t.Fatal(e)
		}
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func lineUnits(text string) []sourceUnit {
	units := []sourceUnit{}
	for i, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		units = append(units, sourceUnit{line, locator{"line", i + 1, i + 1, "第 " + itoa(i+1) + " 行", false}})
	}
	return units
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func TestTextExtractionAndChunkLocations(t *testing.T) {
	path := tempFile(t, "lesson.md", []byte("\ufeff# 标题\r\n\r\n第一段内容\n第二段内容"))
	units, err := extractText(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 3 || units[0].Locator.Start != 1 || units[1].Locator.Start != 3 {
		t.Fatalf("unexpected units: %#v", units)
	}
	chunks, err := chunkUnits(units, chunkingPolicy{Strategy: strategyWindow, Size: 12, Overlap: 2}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 || chunks[0].Locator.Type != "line" {
		t.Fatalf("location was not preserved: %#v", chunks)
	}
	// 跨越多个解析单元时定位必须给出范围，而不是只标第一行。
	spanning := false
	for _, chunk := range chunks {
		if chunk.Locator.Start != chunk.Locator.End {
			spanning = true
			if !strings.Contains(chunk.Locator.Label, "–") {
				t.Fatalf("multi-line locator label missing range: %q", chunk.Locator.Label)
			}
		}
	}
	if !spanning {
		t.Fatalf("expected at least one chunk to span multiple lines: %#v", chunks)
	}
}

func TestModernOfficeExtraction(t *testing.T) {
	docx := officeFile(t, "lesson.docx", map[string]string{"word/document.xml": `<w:document xmlns:w="w"><w:body><w:p><w:r><w:t>中文段落一</w:t></w:r></w:p><w:p><w:r><w:t>表格内容</w:t></w:r></w:p></w:body></w:document>`})
	docUnits, err := extractDOCX(docx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(docUnits) != 2 || docUnits[1].Locator.Label != "提取正文第 2 段" {
		t.Fatalf("unexpected DOCX: %#v", docUnits)
	}
	pptx := officeFile(t, "lesson.pptx", map[string]string{
		"ppt/slides/slide2.xml": `<p:sld xmlns:p="p" xmlns:a="a"><a:p><a:r><a:t>第二张</a:t></a:r></a:p></p:sld>`,
		"ppt/slides/slide1.xml": `<p:sld xmlns:p="p" xmlns:a="a"><a:p><a:r><a:t>第一张</a:t></a:r></a:p></p:sld>`,
	})
	pptUnits, err := extractPPTX(pptx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(pptUnits) != 2 || pptUnits[0].Text != "第一张" || pptUnits[1].Locator.Start != 2 {
		t.Fatalf("unexpected PPTX: %#v", pptUnits)
	}
}

func TestChunkLimit(t *testing.T) {
	_, err := chunkUnits([]sourceUnit{{Text: strings.Repeat("内容。", 20), Locator: locator{Type: "paragraph", Start: 1, End: 1}}}, chunkingPolicy{Strategy: strategyWindow, Size: 4}, 2)
	if err == nil || !strings.Contains(err.Error(), "too many") || !errorsAs(err, &permanentError{}) {
		t.Fatalf("expected permanent limit error, got %v", err)
	}
}

func errorsAs(err error, target *permanentError) bool {
	p, ok := err.(*permanentError)
	if ok {
		*target = *p
	}
	return ok
}

// 任务 3.1：默认窗口约 800 字、相邻约 10% 重叠，并优先在句末断开。
func TestDefaultWindowChunking(t *testing.T) {
	text := strings.Repeat("这是一句用于测试切片窗口的中文内容。", 100)
	chunks, err := chunkUnits([]sourceUnit{{Text: text, Locator: locator{Type: "paragraph", Start: 1, End: 1, Label: "提取正文第 1 段"}}}, defaultChunkingPolicy(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 3 {
		t.Fatalf("expected several chunks, got %d", len(chunks))
	}
	runes := []rune(text)
	for i, chunk := range chunks {
		if chunk.Ordinal != i+1 {
			t.Fatalf("chunk %d ordinal = %d", i, chunk.Ordinal)
		}
		if size := chunk.CharEnd - chunk.CharStart; size > defaultChunkSize {
			t.Fatalf("chunk %d exceeds window: %d runes", i, size)
		}
		if got := string(runes[chunk.CharStart:chunk.CharEnd]); got != chunk.Text {
			t.Fatalf("chunk %d text does not match its char range", i)
		}
		if i < len(chunks)-1 {
			if !strings.HasSuffix(chunk.Text, "。") {
				t.Fatalf("chunk %d does not end on a sentence boundary: %q", i, tail(chunk.Text))
			}
			if overlap := chunk.CharEnd - chunks[i+1].CharStart; overlap != defaultChunkOverlap {
				t.Fatalf("overlap between %d and %d is %d, want %d", i, i+1, overlap, defaultChunkOverlap)
			}
		}
	}
}

func tail(text string) string {
	runes := []rune(text)
	if len(runes) <= 12 {
		return text
	}
	return string(runes[len(runes)-12:])
}

// 任务 3.1：短正文只产生一个切片，不产生空切片或重复切片。
func TestShortDocumentProducesSingleChunk(t *testing.T) {
	chunks, err := chunkUnits(lineUnits("只有一行很短的正文"), defaultChunkingPolicy(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].Text != "只有一行很短的正文" {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
	if chunks[0].CharStart != 0 || chunks[0].CharEnd != len([]rune("只有一行很短的正文")) {
		t.Fatalf("unexpected char range: %#v", chunks[0])
	}
}

// 任务 3.1：跨页切片的定位必须保留全部范围。
func TestCrossPageChunkKeepsRange(t *testing.T) {
	page1 := strings.Repeat("第一页内容。", 120)
	page2 := strings.Repeat("第二页内容。", 120)
	units := []sourceUnit{
		{Text: page1, Locator: locator{Type: "page", Start: 1, End: 1, Label: "第 1 页"}},
		{Text: page2, Locator: locator{Type: "page", Start: 2, End: 2, Label: "第 2 页"}},
	}
	chunks, err := chunkUnits(units, defaultChunkingPolicy(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	if chunks[0].Locator.Start != 1 || chunks[0].Locator.End != 2 {
		t.Fatalf("expected first chunk to span pages 1-2, got %#v", chunks[0].Locator)
	}
	if !strings.Contains(chunks[0].Locator.Label, "1–2") {
		t.Fatalf("unexpected locator label: %q", chunks[0].Locator.Label)
	}
}

// 任务 3.2：分隔符策略按空行分段，超长段落继续按窗口切分。
func TestSeparatorStrategy(t *testing.T) {
	text := "第一段内容。\n\n第二段内容。\n\n" + strings.Repeat("很长的第三段内容。", 200)
	chunks, err := chunkUnits([]sourceUnit{{Text: text, Locator: locator{Type: "line", Start: 1, End: 1, Label: "第 1 行"}}}, chunkingPolicy{Strategy: strategySeparator, Separator: separatorBlank}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 3 {
		t.Fatalf("expected paragraph chunks plus windowed long paragraph, got %d", len(chunks))
	}
	if chunks[0].Text != "第一段内容。" || chunks[1].Text != "第二段内容。" {
		t.Fatalf("unexpected paragraph chunks: %#v %#v", chunks[0].Text, chunks[1].Text)
	}
	if size := chunks[2].CharEnd - chunks[2].CharStart; size > defaultChunkSize {
		t.Fatalf("long paragraph was not windowed: %d", size)
	}
}

// 任务 3.2：标题分章以标题层级为边界。
func TestHeadingStrategy(t *testing.T) {
	text := "# 第一章\n第一章的内容。\n## 第一节\n第一节的内容。\n# 第二章\n第二章的内容。"
	chunks, err := chunkUnits([]sourceUnit{{Text: text, Locator: locator{Type: "line", Start: 1, End: 1, Label: "第 1 行"}}}, chunkingPolicy{Strategy: strategyHeading}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 3 {
		t.Fatalf("expected 3 sections, got %d: %#v", len(chunks), chunks)
	}
	if !strings.HasPrefix(chunks[0].Text, "# 第一章") || !strings.HasPrefix(chunks[1].Text, "## 第一节") || !strings.HasPrefix(chunks[2].Text, "# 第二章") {
		t.Fatalf("section boundaries are wrong: %#v", chunks)
	}
}

// 任务 3.2：不含标题的文档回退到默认窗口，而不是产生空索引。
func TestHeadingStrategyFallsBackToWindow(t *testing.T) {
	text := strings.Repeat("没有标题的正文内容。", 200)
	chunks, err := chunkUnits([]sourceUnit{{Text: text, Locator: locator{Type: "line", Start: 1, End: 1, Label: "第 1 行"}}}, chunkingPolicy{Strategy: strategyHeading}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected windowed chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if size := chunk.CharEnd - chunk.CharStart; size > defaultChunkSize {
			t.Fatalf("fallback did not use the default window: %d", size)
		}
	}
}

// 任务 3.3：同内容同策略重复索引得到一致的序号与字符区间。
func TestChunkingIsReproducible(t *testing.T) {
	text := strings.Repeat("可复现的正文内容。", 300)
	units := []sourceUnit{{Text: text, Locator: locator{Type: "paragraph", Start: 1, End: 1, Label: "提取正文第 1 段"}}}
	first, err := chunkUnits(units, defaultChunkingPolicy(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	second, err := chunkUnits(units, defaultChunkingPolicy(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("chunk count differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Ordinal != second[i].Ordinal || first[i].CharStart != second[i].CharStart || first[i].CharEnd != second[i].CharEnd || first[i].Text != second[i].Text {
			t.Fatalf("chunk %d is not reproducible", i)
		}
	}
}

// 容器内解析与定位验收：设置 FORMAT_CHECK_DIR 指向七种格式的样例目录后运行。
// 该测试需要 poppler 与 LibreOffice，因此在后端镜像里执行（宿主机通常没有这两个工具）。
func TestExtractionInContainer(t *testing.T) {
	dir := os.Getenv("FORMAT_CHECK_DIR")
	if dir == "" {
		t.Skip("FORMAT_CHECK_DIR 未设置，跳过容器内解析验收")
	}
	cases := []struct {
		name        string
		locatorType string
		converted   bool
	}{
		{"format.pdf", "page", false},
		{"format.docx", "paragraph", false},
		{"format.doc", "paragraph", true},
		{"format.pptx", "slide", false},
		{"format.ppt", "slide", true},
		{"format.txt", "line", false},
		{"format.md", "line", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			if _, err := os.Stat(path); err != nil {
				t.Skipf("样例文件不存在: %s", path)
			}
			units, err := extractDocument(context.Background(), path, tc.name)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if len(units) == 0 {
				t.Fatal("解析结果为空")
			}
			for _, unit := range units {
				if unit.Locator.Type != tc.locatorType {
					t.Fatalf("定位类型应为 %s，实际 %s", tc.locatorType, unit.Locator.Type)
				}
				if unit.Locator.Converted != tc.converted {
					t.Fatalf("转换标记应为 %v: %#v", tc.converted, unit.Locator)
				}
				if unit.Locator.Start < 1 || unit.Locator.End < unit.Locator.Start {
					t.Fatalf("定位范围不合法: %#v", unit.Locator)
				}
				if strings.TrimSpace(unit.Text) == "" {
					t.Fatal("解析出空文本单元")
				}
			}
			// 每个格式都必须能继续按默认窗口切片。
			chunks, outcome, err := chunkDocument(units, defaultChunkingPolicy(), 10000)
			if err != nil {
				t.Fatalf("切片失败: %v", err)
			}
			if len(chunks) == 0 || outcome.Strategy != strategyWindow {
				t.Fatalf("默认窗口切片结果不正确: %d %#v", len(chunks), outcome)
			}
			t.Logf("%s：%d 个解析单元，%d 个切片，首个定位 %s", tc.name, len(units), len(chunks), chunks[0].Locator.Label)
		})
	}
}

// 任务 3.2：策略不适用时必须回退并给出可展示的原因说明。
func TestChunkingFallbackNote(t *testing.T) {
	plain := []sourceUnit{{Text: strings.Repeat("没有标题的正文内容。", 100), Locator: locator{Type: "line", Start: 1, End: 1, Label: "第 1 行"}}}
	chunks, outcome, err := chunkDocument(plain, chunkingPolicy{Strategy: strategyHeading}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Strategy != strategyWindow || !strings.Contains(outcome.Note, "回退") {
		t.Fatalf("无标题文档应回退并说明原因: %#v", outcome)
	}
	if len(chunks) == 0 {
		t.Fatal("回退后仍应产生切片")
	}

	headed := []sourceUnit{{Text: "# 第一章\n内容\n# 第二章\n内容", Locator: locator{Type: "line", Start: 1, End: 1, Label: "第 1 行"}}}
	_, headedOutcome, err := chunkDocument(headed, chunkingPolicy{Strategy: strategyHeading}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if headedOutcome.Strategy != strategyHeading || headedOutcome.Note != "" {
		t.Fatalf("有标题时不应回退: %#v", headedOutcome)
	}

	separated := []sourceUnit{{Text: strings.Repeat("连续文本内容", 300), Locator: locator{Type: "line", Start: 1, End: 1, Label: "第 1 行"}}}
	_, separatedOutcome, err := chunkDocument(separated, chunkingPolicy{Strategy: strategySeparator, Separator: separatorBlank}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if separatedOutcome.Strategy != strategyWindow || !strings.Contains(separatedOutcome.Note, "分隔符") {
		t.Fatalf("缺少所选分隔符时应回退并说明: %#v", separatedOutcome)
	}
}

func TestChunkingPolicyValidation(t *testing.T) {
	cases := []chunkingPolicy{
		{Strategy: "unknown"},
		{Strategy: strategySeparator, Separator: "逗号"},
		{Strategy: strategyWindow, Size: maxChunkSize + 1},
		{Strategy: strategyWindow, Size: 200, Overlap: 150},
	}
	for _, policy := range cases {
		if _, err := policy.normalize(); err == nil {
			t.Fatalf("invalid policy accepted: %#v", policy)
		}
	}
}

func gatewayStub(t *testing.T, handler http.HandlerFunc) (*gatewayClient, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	client := &gatewayClient{baseURL: server.URL, apiKey: "test-key", embedModel: "test-model", dimensions: 1024, timeout: 5 * time.Second, http: server.Client()}
	return client, server
}

func openAIEmbedding(vector []float32) map[string]any {
	return map[string]any{"data": []map[string]any{{"embedding": vector}}}
}

func validVector() []float32 {
	vector := make([]float32, 1024)
	vector[0] = 1
	return vector
}

func TestGatewayEmbedding(t *testing.T) {
	client, server := gatewayStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing gateway credentials on the server side")
		}
		var input map[string]any
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input["model"] != "test-model" {
			t.Errorf("model not forwarded: %v", input["model"])
		}
		_ = json.NewEncoder(w).Encode(openAIEmbedding(validVector()))
	})
	defer server.Close()
	vectors, err := client.embed(context.Background(), []string{"中文查询"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 1 || len(vectors[0]) != 1024 {
		t.Fatal("unexpected embedding")
	}
}

func TestGatewayEmbeddingRejectsZeroVector(t *testing.T) {
	client, server := gatewayStub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(openAIEmbedding(make([]float32, 1024)))
	})
	defer server.Close()
	_, err := client.embed(context.Background(), []string{"x"})
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("zero")) {
		t.Fatalf("expected zero vector rejection, got %v", err)
	}
}

func TestGatewayEmbeddingRejectsInvalidResults(t *testing.T) {
	valid := validVector()
	cases := []struct {
		name    string
		vectors [][]float32
		count   int
		want    string
	}{
		{"count", [][]float32{valid}, 2, "count"},
		{"dimension", [][]float32{{1}}, 1, "dimension"},
		{"zero", [][]float32{make([]float32, 1024)}, 1, "zero"},
		{"nan", [][]float32{append([]float32{float32(math.NaN())}, make([]float32, 1023)...)}, 1, "non-finite"},
		{"infinity", [][]float32{append([]float32{float32(math.Inf(1))}, make([]float32, 1023)...)}, 1, "non-finite"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEmbeddings(tc.vectors, tc.count, 1024)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %s error, got %v", tc.want, err)
			}
		})
	}
}

func TestGatewayEmbeddingErrors(t *testing.T) {
	t.Run("non-200", func(t *testing.T) {
		client, server := gatewayStub(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		})
		defer server.Close()
		if _, err := client.embed(context.Background(), []string{"x"}); err == nil {
			t.Fatal("expected error for non-200 response")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			time.Sleep(80 * time.Millisecond)
		}))
		defer server.Close()
		client := &gatewayClient{baseURL: server.URL, embedModel: "test-model", dimensions: 1024, timeout: 5 * time.Millisecond, http: server.Client()}
		if _, err := client.embed(context.Background(), []string{"x"}); err == nil {
			t.Fatal("expected timeout error")
		}
		if requests != 1 {
			t.Fatalf("expected exactly one request without external fallback, got %d", requests)
		}
	})
	t.Run("not-configured", func(t *testing.T) {
		client := &gatewayClient{dimensions: 1024}
		if _, err := client.embed(context.Background(), []string{"x"}); err == nil || !strings.Contains(err.Error(), "未配置") {
			t.Fatalf("expected configuration error, got %v", err)
		}
	})
}
