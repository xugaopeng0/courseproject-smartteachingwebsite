package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentValidation(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, ext, content string
		valid              bool
	}{
		{"pdf", ".pdf", "%PDF-1.7\nexample", true},
		{"html-disguised-as-pdf", ".pdf", "<html>bad</html>", false},
		{"empty", ".pdf", "", false},
		{"renamed-pdf", ".docx", "%PDF-1.7", false},
		{"old-word", ".doc", string([]byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}), true},
		{"chinese-text", ".txt", "中文讲义\r\n第一节\t内容 😀", true},
		{"markdown", ".md", "# 教学目标\n\n- 学习 Go\n<script>只作为文本下载</script>", true},
		{"bom", ".txt", "\uFEFF带 BOM 文本", true},
		{"bom-only", ".md", "\uFEFF", false},
		{"empty-text", ".txt", "", false},
		{"invalid-utf8", ".md", string([]byte{0xc3, 0x28}), false},
		{"gbk", ".txt", string([]byte{0xd6, 0xd0, 0xce, 0xc4}), false},
		{"utf16", ".txt", string([]byte{0xff, 0xfe, 0x41, 0}), false},
		{"nul", ".txt", "text\x00binary", false},
		{"escape", ".md", "text\x1b[31m", false},
		{"unicode-control", ".txt", "text\u0085", false},
		{"literal-replacement-character", ".txt", "合法字符�", true},
		{"buffer-boundary", ".md", strings.Repeat("a", 4095) + "中文", true},
		{"invalid-tail", ".txt", strings.Repeat("a", 8192) + "\xff", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, tc.name)
			if err := os.WriteFile(p, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			if got := validDocument(p, tc.ext); got != tc.valid {
				t.Fatalf("validDocument = %v, want %v", got, tc.valid)
			}
		})
	}
	for _, ext := range []string{".docx", ".pptx"} {
		t.Run(ext, func(t *testing.T) {
			p := filepath.Join(dir, "office"+ext)
			f, err := os.Create(p)
			if err != nil {
				t.Fatal(err)
			}
			z := zip.NewWriter(f)
			for _, name := range []string{"[Content_Types].xml", "word/document.xml"} {
				w, _ := z.Create(name)
				_, _ = w.Write([]byte("<xml/>"))
			}
			_ = z.Close()
			_ = f.Close()
			want := ext == ".docx"
			if validDocument(p, ext) != want {
				t.Fatalf("Office format distinction failed: %s", ext)
			}
		})
	}
}
func TestCredentialsCannotBeBlankOrOversized(t *testing.T) {
	for _, c := range []credentials{{Username: "ab", Name: "学生", Password: "abcdefgh"}, {Username: "valid", Name: " ", Password: "abcdefgh"}, {Username: "valid", Name: "学生", Password: "short"}} {
		if normalize(&c) == nil {
			t.Fatal("invalid credentials accepted")
		}
	}
}
