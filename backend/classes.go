package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type Classroom struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	OwnerID     string    `json:"owner_id"`
	Teacher     string    `json:"teacher"`
	InviteCode  string    `json:"invite_code,omitempty"`
	Archived    bool      `json:"archived"`
	CreatedAt   time.Time `json:"created_at"`
	Members     int       `json:"member_count"`
	Handouts    int       `json:"handout_count"`
}

const classSelect = `SELECT c.id,c.name,c.description,c.owner_id,u.name,c.invite_code,c.archived,c.created_at,(SELECT count(*) FROM memberships m WHERE m.class_id=c.id),(SELECT count(*) FROM handouts h WHERE h.class_id=c.id) FROM classrooms c JOIN users u ON u.id=c.owner_id `

func scanClass(row rowScanner) (Classroom, error) {
	var c Classroom
	err := row.Scan(&c.ID, &c.Name, &c.Description, &c.OwnerID, &c.Teacher, &c.InviteCode, &c.Archived, &c.CreatedAt, &c.Members, &c.Handouts)
	return c, err
}

func (a *App) access(r *http.Request, id string, u *User) (Classroom, error) {
	c, err := scanClass(a.db.QueryRowContext(r.Context(), classSelect+`WHERE c.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, fail(404, "班级不存在")
	}
	if err != nil {
		return c, err
	}
	if c.OwnerID != u.ID {
		var member bool
		err = a.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM memberships WHERE class_id=? AND user_id=?)`, id, u.ID).Scan(&member)
		if err != nil {
			return c, err
		}
		if !member {
			return c, fail(403, "你不是该班级的成员")
		}
		c.InviteCode = ""
	}
	return c, nil
}
func (a *App) classes(w http.ResponseWriter, r *http.Request, u *User) error {
	rows, err := a.db.QueryContext(r.Context(), classSelect+`WHERE c.owner_id=? OR EXISTS(SELECT 1 FROM memberships m WHERE m.class_id=c.id AND m.user_id=?) ORDER BY c.created_at DESC`, u.ID, u.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	list := []Classroom{}
	for rows.Next() {
		c, err := scanClass(rows)
		if err != nil {
			return err
		}
		if c.OwnerID != u.ID {
			c.InviteCode = ""
		}
		list = append(list, c)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, list)
	return nil
}

type classInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func validClass(c *classInput) error {
	c.Name = strings.TrimSpace(c.Name)
	c.Description = strings.TrimSpace(c.Description)
	if utf8.RuneCountInString(c.Name) < 1 || utf8.RuneCountInString(c.Name) > 80 || utf8.RuneCountInString(c.Description) > 1000 {
		return fail(400, "班级名称须为 1–80 字，描述不超过 1000 字")
	}
	return nil
}
func (a *App) createClass(w http.ResponseWriter, r *http.Request, u *User) error {
	if u.Role != "teacher" {
		return fail(403, "只有教师可以创建班级")
	}
	var input classInput
	if err := decode(w, r, &input); err != nil {
		return err
	}
	if err := validClass(&input); err != nil {
		return err
	}
	id := randomID(16)
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO classrooms(id,name,description,owner_id,invite_code) VALUES(?,?,?,?,?)`, id, input.Name, input.Description, u.ID, strings.ToUpper(randomID(5)))
	if err != nil {
		return err
	}
	c, err := a.access(r, id, u)
	if err != nil {
		return err
	}
	writeJSON(w, 201, c)
	return nil
}
func (a *App) updateClass(w http.ResponseWriter, r *http.Request, u *User) error {
	var input classInput
	if err := decode(w, r, &input); err != nil {
		return err
	}
	if err := validClass(&input); err != nil {
		return err
	}
	tag, err := a.db.ExecContext(r.Context(), `UPDATE classrooms SET name=?,description=? WHERE id=? AND owner_id=? AND archived=0`, input.Name, input.Description, r.PathValue("id"), u.ID)
	if err != nil {
		return err
	}
	if affected, _ := tag.RowsAffected(); affected == 0 {
		return fail(403, "无权编辑，或班级已归档")
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *App) archiveClass(w http.ResponseWriter, r *http.Request, u *User) error {
	var input struct {
		Archived bool `json:"archived"`
	}
	if err := decode(w, r, &input); err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner string
	err = tx.QueryRowContext(r.Context(), `SELECT owner_id FROM classrooms WHERE id=? FOR UPDATE`, r.PathValue("id")).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(404, "班级不存在")
	}
	if err != nil {
		return err
	}
	if owner != u.ID {
		return fail(403, "无权管理此班级")
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE classrooms SET archived=? WHERE id=?`, input.Archived, r.PathValue("id")); err != nil {
		return err
	}
	if a.knowledge {
		if input.Archived {
			_, err = tx.ExecContext(r.Context(), `UPDATE knowledge_jobs SET status='paused',lease_until=NULL,lease_token=NULL,updated_at=NOW(6) WHERE class_id=? AND status IN ('queued','running')`, r.PathValue("id"))
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `UPDATE knowledge_documents SET status='paused',updated_at=NOW(6) WHERE class_id=? AND status IN ('queued','extracting','embedding')`, r.PathValue("id"))
			}
		} else {
			_, err = tx.ExecContext(r.Context(), `UPDATE knowledge_jobs SET status='queued',next_attempt_at=NOW(6),updated_at=NOW(6) WHERE class_id=? AND status='paused'`, r.PathValue("id"))
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `UPDATE knowledge_documents d SET status='queued',updated_at=NOW(6) WHERE d.class_id=? AND d.status='paused' AND EXISTS(SELECT 1 FROM knowledge_jobs j WHERE j.handout_id=d.handout_id AND j.status='queued')`, r.PathValue("id"))
			}
		}
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *App) joinClass(w http.ResponseWriter, r *http.Request, u *User) error {
	if u.Role != "student" {
		return fail(403, "仅学生可通过邀请码加入班级")
	}
	var input struct {
		Code string `json:"code"`
	}
	if err := decode(w, r, &input); err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	var archived bool
	err = tx.QueryRowContext(r.Context(), `SELECT id,archived FROM classrooms WHERE invite_code=? FOR UPDATE`, strings.ToUpper(strings.TrimSpace(input.Code))).Scan(&id, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(404, "邀请码无效")
	}
	if err != nil {
		return err
	}
	if archived {
		return fail(409, "班级已归档，无法加入")
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO memberships(class_id,user_id) VALUES(?,?)`, id, u.ID)
	if conflict(err) {
		return fail(409, "你已加入该班级")
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]string{"id": id})
	return nil
}
func (a *App) removeMember(w http.ResponseWriter, r *http.Request, u *User) error {
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner string
	var archived bool
	err = tx.QueryRowContext(r.Context(), `SELECT owner_id,archived FROM classrooms WHERE id=? FOR UPDATE`, r.PathValue("id")).Scan(&owner, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(404, "班级不存在")
	}
	if err != nil {
		return err
	}
	target := r.PathValue("userID")
	if u.ID != owner && u.ID != target {
		return fail(403, "不能移除其他同学")
	}
	if archived && u.ID == owner {
		return fail(409, "请先恢复班级再管理成员")
	}
	tag, err := tx.ExecContext(r.Context(), `DELETE FROM memberships WHERE class_id=? AND user_id=?`, r.PathValue("id"), target)
	if err != nil {
		return err
	}
	if affected, _ := tag.RowsAffected(); affected == 0 {
		return fail(404, "成员不存在")
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *App) classDetail(w http.ResponseWriter, r *http.Request, u *User) error {
	c, err := a.access(r, r.PathValue("id"), u)
	if err != nil {
		return err
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT u.id,u.username,u.name,u.role FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.class_id=? ORDER BY m.joined_at`, c.ID)
	if err != nil {
		return err
	}
	members := []User{}
	for rows.Next() {
		var m User
		if err = rows.Scan(&m.ID, &m.Username, &m.Name, &m.Role); err != nil {
			rows.Close()
			return err
		}
		if u.ID != c.OwnerID {
			m.Username = ""
		}
		members = append(members, m)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	handouts, err := a.listHandouts(r, c.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"classroom": c, "members": members, "handouts": handouts})
	return nil
}
