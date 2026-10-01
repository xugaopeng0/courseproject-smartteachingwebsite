package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// importSeeds 把课程提供的种子材料导入内置示例班级。
// 种子材料就是普通讲义：它们走同一套上传卷、索引任务与班级授权规则，不享受任何特殊通道。
func (a *App) importSeeds(ctx context.Context) error {
	entries, err := os.ReadDir(a.seedDir)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("seed import: %s 不存在，跳过", a.seedDir)
			return nil
		}
		return err
	}
	files := []string{}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		// 目录说明文件不当作种子材料。
		if strings.HasPrefix(strings.ToUpper(entry.Name()), "README") {
			continue
		}
		if _, ok := mimeTypes[strings.ToLower(filepath.Ext(entry.Name()))]; !ok {
			continue
		}
		files = append(files, entry.Name())
	}
	if len(files) == 0 {
		log.Printf("seed import: %s 中没有可导入的讲义，跳过", a.seedDir)
		return nil
	}
	classID, err := a.ensureSeedClass(ctx)
	if err != nil {
		return err
	}
	imported := 0
	for _, name := range files {
		changed, err := a.importSeedFile(ctx, classID, name)
		if err != nil {
			log.Printf("seed import: %s 失败: %v", name, err)
			continue
		}
		if changed {
			imported++
		}
	}
	log.Printf("seed import: %s 已同步 %d 份种子材料（本次新增或更新 %d 份）", a.seedClassName, len(files), imported)
	return nil
}

// ensureSeedClass 幂等地取得或建立示例班级；所有者取首个管理员账号，学生通过邀请码加入。
func (a *App) ensureSeedClass(ctx context.Context) (string, error) {
	var id string
	err := a.db.QueryRowContext(ctx, `SELECT id FROM classrooms WHERE name=? ORDER BY created_at LIMIT 1`, a.seedClassName).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var owner string
	if err = a.db.QueryRowContext(ctx, `SELECT id FROM users WHERE role='admin' ORDER BY created_at LIMIT 1`).Scan(&owner); err != nil {
		return "", err
	}
	id = randomID(16)
	_, err = a.db.ExecContext(ctx, `INSERT INTO classrooms(id,name,description,owner_id,invite_code) VALUES(?,?,?,?,?)`,
		id, a.seedClassName, "由课程种子材料自动建立的示例班级，学生可通过邀请码加入后检索。", owner, strings.ToUpper(randomID(5)))
	if err != nil {
		// 并发启动时可能已被另一个进程创建。
		if reselect := a.db.QueryRowContext(ctx, `SELECT id FROM classrooms WHERE name=? ORDER BY created_at LIMIT 1`, a.seedClassName).Scan(&id); reselect == nil {
			return id, nil
		}
		return "", err
	}
	return id, nil
}

// importSeedFile 按内容哈希幂等地导入一份种子材料；返回是否发生了新增或更新。
func (a *App) importSeedFile(ctx context.Context, classID, name string) (bool, error) {
	source := filepath.Join(a.seedDir, name)
	info, err := os.Stat(source)
	if err != nil {
		return false, err
	}
	ext := strings.ToLower(filepath.Ext(name))
	mimeType := mimeTypes[ext]
	hash, err := fileHash(source)
	if err != nil {
		return false, err
	}
	stored := "seed-" + shortHash(name) + ext
	destination := filepath.Join(a.uploads, stored)
	title := strings.TrimSuffix(name, filepath.Ext(name))
	if len([]rune(title)) > 120 {
		title = string([]rune(title)[:120])
	}
	var handoutID, indexedHash string
	err = a.db.QueryRowContext(ctx, `SELECT h.id,COALESCE(d.content_hash,'') FROM handouts h LEFT JOIN knowledge_documents d ON d.handout_id=h.id WHERE h.stored_name=?`, stored).Scan(&handoutID, &indexedHash)
	switch {
	case err == nil && indexedHash == hash:
		return false, nil
	case err == nil:
		if err = copySeed(source, destination); err != nil {
			return false, err
		}
		tx, beginErr := a.db.BeginTx(ctx, nil)
		if beginErr != nil {
			return false, beginErr
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, `UPDATE handouts SET title=?,filename=?,size=?,mime=? WHERE id=?`, title, name, info.Size(), mimeType, handoutID); err != nil {
			return false, err
		}
		if err = a.queueKnowledgeForUpload(ctx, tx, handoutID, classID, hash); err != nil {
			return false, err
		}
		return true, tx.Commit()
	case errors.Is(err, sql.ErrNoRows):
		if err = copySeed(source, destination); err != nil {
			return false, err
		}
		tx, beginErr := a.db.BeginTx(ctx, nil)
		if beginErr != nil {
			return false, beginErr
		}
		defer tx.Rollback()
		handoutID = randomID(16)
		if _, err = tx.ExecContext(ctx, `INSERT INTO handouts(id,class_id,title,filename,stored_name,size,mime) VALUES(?,?,?,?,?,?,?)`,
			handoutID, classID, title, name, stored, info.Size(), mimeType); err != nil {
			return false, err
		}
		if err = a.queueKnowledgeForUpload(ctx, tx, handoutID, classID, hash); err != nil {
			return false, err
		}
		return true, tx.Commit()
	default:
		return false, err
	}
}

func copySeed(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}
