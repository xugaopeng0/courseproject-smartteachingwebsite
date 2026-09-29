package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"errors"
	"github.com/jackc/pgx/v5"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxUpload int64 = 50 << 20

var mimeTypes = map[string]string{
	".pdf": "application/pdf", ".doc": "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".ppt":  "application/vnd.ms-powerpoint", ".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".txt": "text/plain; charset=utf-8", ".md": "text/markdown; charset=utf-8",
}

func validText(reader io.Reader) bool {
	buffer := bufio.NewReader(reader)
	first, hasContent := true, false
	for {
		character, size, err := buffer.ReadRune()
		if err == io.EOF {
			return hasContent
		}
		if err != nil || (character == utf8.RuneError && size == 1) {
			return false
		}
		if first && character == '\uFEFF' {
			first = false
			continue
		}
		first = false
		if unicode.IsControl(character) && character != '\t' && character != '\r' && character != '\n' {
			return false
		}
		hasContent = true
	}
}

type Handout struct {
	ID         string    `json:"id"`
	ClassID    string    `json:"class_id"`
	Title      string    `json:"title"`
	Filename   string    `json:"filename"`
	Size       int64     `json:"size"`
	Mime       string    `json:"mime"`
	UploadedAt time.Time `json:"uploaded_at"`
}

func validDocument(path, ext string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if ext == ".txt" || ext == ".md" {
		return validText(f)
	}
	buf := make([]byte, 8)
	n, _ := f.Read(buf)
	if ext == ".pdf" {
		return n >= 5 && bytes.Equal(buf[:5], []byte("%PDF-"))
	}
	if ext == ".doc" || ext == ".ppt" {
		return n == 8 && bytes.Equal(buf, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1})
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		return false
	}
	defer z.Close()
	required := "word/document.xml"
	if ext == ".pptx" {
		required = "ppt/presentation.xml"
	}
	hasType, hasDoc := false, false
	for _, entry := range z.File {
		if entry.Name == "[Content_Types].xml" {
			hasType = true
		}
		if entry.Name == required {
			hasDoc = true
		}
	}
	return hasType && hasDoc
}
func (a *App) upload(w http.ResponseWriter, r *http.Request, u *User) error {
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var owner string
	var archived bool
	err = tx.QueryRow(r.Context(), `SELECT owner_id,archived FROM classrooms WHERE id=$1 FOR UPDATE`, r.PathValue("id")).Scan(&owner, &archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return fail(404, "班级不存在")
	}
	if err != nil {
		return err
	}
	if owner != u.ID {
		return fail(403, "仅任课教师可以上传讲义")
	}
	if archived {
		return fail(409, "班级已归档，请先恢复")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+(1<<20))
	if err = r.ParseMultipartForm(2 << 20); err != nil {
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fail(413, "文件不能超过 50 MB")
		}
		return fail(400, "上传请求无效")
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		return fail(400, "请选择文件")
	}
	defer file.Close()
	filename := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	ext := strings.ToLower(filepath.Ext(filename))
	mt, ok := mimeTypes[ext]
	if !ok {
		return fail(400, "仅支持 PDF、Word、PowerPoint、TXT、MD 文档")
	}
	if header.Size > maxUpload {
		return fail(413, "文件不能超过 50 MB")
	}
	if len(filename) > 240 {
		return fail(400, "文件名过长")
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	if utf8.RuneCountInString(title) > 120 {
		return fail(400, "讲义标题不能超过 120 字")
	}
	stored := randomID(24) + ext
	path := filepath.Join(a.uploads, stored)
	dest, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path)
		}
	}()
	size, copyErr := io.Copy(dest, io.LimitReader(file, maxUpload+1))
	closeErr := dest.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if size > maxUpload {
		return fail(413, "文件不能超过 50 MB")
	}
	if size == 0 || !validDocument(path, ext) {
		if ext == ".txt" || ext == ".md" {
			return fail(400, "TXT/MD 须为非空 UTF-8 文本，且不能包含二进制控制字符；请另存为 UTF-8 后重试")
		}
		return fail(400, "文件内容与格式不匹配或文档已损坏")
	}
	id := randomID(16)
	_, err = tx.Exec(r.Context(), `INSERT INTO handouts(id,class_id,title,filename,stored_name,size,mime) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, r.PathValue("id"), title, filename, stored, size, mt)
	if err != nil {
		return err
	}
	if err = tx.Commit(r.Context()); err != nil {
		return err
	}
	keep = true
	writeJSON(w, 201, map[string]string{"id": id})
	return nil
}
func (a *App) listHandouts(r *http.Request, id string) ([]Handout, error) {
	rows, err := a.db.Query(r.Context(), `SELECT id,class_id,title,filename,size,mime,uploaded_at FROM handouts WHERE class_id=$1 ORDER BY uploaded_at DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Handout{}
	for rows.Next() {
		var h Handout
		if err = rows.Scan(&h.ID, &h.ClassID, &h.Title, &h.Filename, &h.Size, &h.Mime, &h.UploadedAt); err != nil {
			return nil, err
		}
		list = append(list, h)
	}
	return list, rows.Err()
}
func (a *App) download(w http.ResponseWriter, r *http.Request, u *User) error {
	var classID, filename, stored, mt string
	err := a.db.QueryRow(r.Context(), `SELECT class_id,filename,stored_name,mime FROM handouts WHERE id=$1`, r.PathValue("id")).Scan(&classID, &filename, &stored, &mt)
	if errors.Is(err, pgx.ErrNoRows) {
		return fail(404, "讲义不存在")
	}
	if err != nil {
		return err
	}
	if _, err = a.access(r, classID, u); err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(a.uploads, stored))
	if err != nil {
		return fail(404, "讲义文件暂不可用")
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	disposition := "attachment"
	if r.URL.Query().Get("preview") == "1" && mt == "application/pdf" {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", mt)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": filename}))
	w.Header().Set("Content-Security-Policy", "sandbox")
	http.ServeContent(w, r, filename, stat.ModTime(), f)
	return nil
}
func (a *App) deleteHandout(w http.ResponseWriter, r *http.Request, u *User) error {
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var stored, owner string
	var archived bool
	err = tx.QueryRow(r.Context(), `SELECT h.stored_name,c.owner_id,c.archived FROM handouts h JOIN classrooms c ON c.id=h.class_id WHERE h.id=$1 FOR UPDATE OF c,h`, r.PathValue("id")).Scan(&stored, &owner, &archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return fail(404, "讲义不存在")
	}
	if err != nil {
		return err
	}
	if owner != u.ID {
		return fail(403, "仅任课教师可删除讲义")
	}
	if archived {
		return fail(409, "请先恢复班级")
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM handouts WHERE id=$1`, r.PathValue("id")); err != nil {
		return err
	}
	if err = tx.Commit(r.Context()); err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(a.uploads, stored)); err != nil && !os.IsNotExist(err) {
		return err
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
