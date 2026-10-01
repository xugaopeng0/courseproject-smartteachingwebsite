package main

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// 切片策略：默认窗口、自定义分隔符、Markdown 标题分章。
// 所有偏移都是「提取文本」中的 rune 下标，CharStart 为 0 起始，CharEnd 为开区间上界。
const (
	chunkerVersion = "chunks-v2"

	strategyWindow    = "window"
	strategySeparator = "separator"
	strategyHeading   = "heading"

	separatorBlank  = "blank"
	separatorNewline = "newline"
	separatorPeriod = "period"

	defaultChunkSize    = 800
	defaultChunkOverlap = 80
	maxChunkSize        = 4000
)

type chunkingPolicy struct {
	Strategy  string
	Separator string
	Size      int
	Overlap   int
}

func defaultChunkingPolicy() chunkingPolicy {
	return chunkingPolicy{Strategy: strategyWindow, Size: defaultChunkSize, Overlap: defaultChunkOverlap}
}

// normalize 校验并收敛策略参数；非法值返回错误而不是静默使用默认值。
func (p chunkingPolicy) normalize() (chunkingPolicy, error) {
	if p.Strategy == "" {
		p.Strategy = strategyWindow
	}
	switch p.Strategy {
	case strategyWindow, strategyHeading:
		p.Separator = ""
	case strategySeparator:
		if p.Separator == "" {
			p.Separator = separatorBlank
		}
		if p.Separator != separatorBlank && p.Separator != separatorNewline && p.Separator != separatorPeriod {
			return p, fail(400, "分隔符策略只支持空行、换行或句号")
		}
	default:
		return p, fail(400, "切片策略只支持 window、separator 或 heading")
	}
	if p.Size == 0 {
		p.Size = defaultChunkSize
	}
	if p.Size < 1 || p.Size > maxChunkSize {
		return p, fail(400, "切片窗口须为 1–4000 字")
	}
	if p.Overlap == 0 {
		p.Overlap = p.Size / 10
	}
	if p.Overlap < 0 || p.Overlap > p.Size/2 {
		return p, fail(400, "切片重叠须为 0 到窗口长度的一半")
	}
	return p, nil
}

type unitSpan struct {
	start, end int
	locator    locator
}

// composeText 把解析单元拼成「提取文本」，并记录每个单元覆盖的 rune 区间。
func composeText(units []sourceUnit) ([]rune, []unitSpan) {
	var b strings.Builder
	spans := make([]unitSpan, 0, len(units))
	total := 0
	for _, unit := range units {
		text := strings.TrimSpace(unit.Text)
		if text == "" {
			continue
		}
		if total > 0 {
			b.WriteString("\n")
			total++
		}
		start := total
		b.WriteString(text)
		total += utf8.RuneCountInString(text)
		spans = append(spans, unitSpan{start: start, end: total, locator: unit.Locator})
	}
	return []rune(b.String()), spans
}

// chunkingOutcome 记录实际生效的策略与回退原因，供索引状态接口向用户说明。
type chunkingOutcome struct {
	Strategy string
	Note     string
}

// chunkUnits 按策略切分提取文本，返回带序号、字符区间与位置映射的切片。
func chunkUnits(units []sourceUnit, policy chunkingPolicy, maxChunks int) ([]sourceChunk, error) {
	chunks, _, err := chunkDocument(units, policy, maxChunks)
	return chunks, err
}

// chunkDocument 在 chunkUnits 基础上返回实际生效的策略与回退说明。
func chunkDocument(units []sourceUnit, policy chunkingPolicy, maxChunks int) ([]sourceChunk, chunkingOutcome, error) {
	policy, err := policy.normalize()
	if err != nil {
		return nil, chunkingOutcome{}, err
	}
	outcome := chunkingOutcome{Strategy: policy.Strategy}
	runes, spans := composeText(units)
	if len(runes) == 0 {
		return nil, outcome, nil
	}
	segments, matched := strategySegments(runes, policy)
	if !matched {
		outcome.Strategy = strategyWindow
		if policy.Strategy == strategyHeading {
			outcome.Note = "未识别到 Markdown 标题，已回退为默认窗口策略"
		} else {
			outcome.Note = "未找到所选分隔符，已回退为默认窗口策略"
		}
	}
	out := []sourceChunk{}
	for _, segment := range segments {
		for _, piece := range windowPieces(runes, segment[0], segment[1], policy.Size, policy.Overlap) {
			start, end := trimRange(runes, piece[0], piece[1])
			if start >= end {
				continue
			}
			if len(out) >= maxChunks {
				return nil, outcome, &permanentError{"resource_limit", errors.New("too many chunks")}
			}
			loc := locateSpan(spans, start, end)
			out = append(out, sourceChunk{
				Text:      string(runes[start:end]),
				Locator:   loc,
				Ordinal:   len(out) + 1,
				CharStart: start,
				CharEnd:   end,
			})
		}
	}
	return out, outcome, nil
}

// strategySegments 返回按策略划分的粗分段，以及所选策略的边界是否真的存在。
// 窗口策略下整篇为一段。
func strategySegments(runes []rune, policy chunkingPolicy) ([][2]int, bool) {
	switch policy.Strategy {
	case strategySeparator:
		return separatorSegments(runes, policy.Separator)
	case strategyHeading:
		if segments := headingSegments(runes); len(segments) > 0 {
			return segments, true
		}
		return [][2]int{{0, len(runes)}}, false
	default:
		return [][2]int{{0, len(runes)}}, true
	}
}

// separatorSegments 按分隔符切分，并返回是否真的出现过该分隔符。
func separatorSegments(runes []rune, separator string) ([][2]int, bool) {
	marker := []rune("\n\n")
	includeMarker := false
	switch separator {
	case separatorNewline:
		marker = []rune("\n")
	case separatorPeriod:
		marker = []rune("。")
		includeMarker = true
	}
	segments := [][2]int{}
	start := 0
	matched := false
	for i := 0; i+len(marker) <= len(runes); {
		if !matchRunes(runes[i:i+len(marker)], marker) {
			i++
			continue
		}
		matched = true
		end := i
		if includeMarker {
			end = i + len(marker)
		}
		if end > start {
			segments = append(segments, [2]int{start, end})
		}
		start = i + len(marker)
		i = start
	}
	if start < len(runes) {
		segments = append(segments, [2]int{start, len(runes)})
	}
	if !matched || len(segments) == 0 {
		return [][2]int{{0, len(runes)}}, false
	}
	return segments, true
}

func matchRunes(window, marker []rune) bool {
	for i := range marker {
		if window[i] != marker[i] {
			return false
		}
	}
	return true
}

// headingSegments 以 Markdown 标题行为边界分章；标题行归入其后的章节。
func headingSegments(runes []rune) [][2]int {
	starts := []int{}
	lineStart := 0
	for i := 0; i <= len(runes); i++ {
		if i == len(runes) || runes[i] == '\n' {
			line := strings.TrimSpace(string(runes[lineStart:i]))
			if strings.HasPrefix(line, "#") {
				starts = append(starts, lineStart)
			}
			lineStart = i + 1
		}
	}
	if len(starts) == 0 {
		return nil
	}
	segments := [][2]int{}
	for i, start := range starts {
		end := len(runes)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		if end > start {
			segments = append(segments, [2]int{start, end})
		}
	}
	return segments
}

// windowPieces 在 [start,end) 内按窗口切分，相邻切片保留 overlap 个字符的重叠，
// 并优先在换行、空行或句号处断开。
func windowPieces(runes []rune, start, end, size, overlap int) [][2]int {
	if end > len(runes) {
		end = len(runes)
	}
	pieces := [][2]int{}
	position := start
	for position < end {
		limit := position + size
		if limit >= end {
			pieces = append(pieces, [2]int{position, end})
			break
		}
		cut := breakPoint(runes, position, limit)
		pieces = append(pieces, [2]int{position, cut})
		next := cut - overlap
		if next <= position {
			next = cut
		}
		position = next
	}
	return pieces
}

// breakPoint 在窗口后半段向前寻找自然边界，找不到就用窗口上界。
func breakPoint(runes []rune, start, limit int) int {
	floor := start + (limit-start)/2
	for i := limit; i > floor; i-- {
		switch runes[i-1] {
		case '\n', '。', '；', '！', '？', '.', ';', '!', '?':
			return i
		}
	}
	return limit
}

func trimRange(runes []rune, start, end int) (int, int) {
	for start < end && isSpaceRune(runes[start]) {
		start++
	}
	for end > start && isSpaceRune(runes[end-1]) {
		end--
	}
	return start, end
}

func isSpaceRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\u3000' || r == '\uFEFF'
}

// locateSpan 把切片的字符区间映射回解析定位，跨多个单元时给出范围。
func locateSpan(spans []unitSpan, start, end int) locator {
	var first, last *unitSpan
	for i := range spans {
		span := &spans[i]
		if span.end <= start || span.start >= end {
			continue
		}
		if first == nil {
			first = span
		}
		last = span
	}
	if first == nil {
		return locator{Type: "document", Start: 1, End: 1, Label: "提取文本"}
	}
	if first == last {
		return first.locator
	}
	merged := first.locator
	merged.End = last.locator.End
	merged.Label = rangeLabel(merged.Type, merged.Start, merged.End)
	return merged
}

func rangeLabel(kind string, start, end int) string {
	if start >= end {
		end = start
	}
	switch kind {
	case "line":
		if start == end {
			return fmt.Sprintf("第 %d 行", start)
		}
		return fmt.Sprintf("第 %d–%d 行", start, end)
	case "page":
		if start == end {
			return fmt.Sprintf("第 %d 页", start)
		}
		return fmt.Sprintf("第 %d–%d 页", start, end)
	case "slide":
		if start == end {
			return fmt.Sprintf("第 %d 张幻灯片", start)
		}
		return fmt.Sprintf("第 %d–%d 张幻灯片", start, end)
	case "paragraph":
		if start == end {
			return fmt.Sprintf("提取正文第 %d 段", start)
		}
		return fmt.Sprintf("提取正文第 %d–%d 段", start, end)
	default:
		return fmt.Sprintf("第 %d–%d 字", start+1, end)
	}
}
