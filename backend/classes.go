package main

import (
	"errors"
	"github.com/jackc/pgx/v5"
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

func scanClass(row pgx.Row) (Classroom, error) {
	var c Classroom
	err := row.Scan(&c.ID, &c.Name, &c.Description, &c.OwnerID, &c.Teacher, &c.InviteCode, &c.Archived, &c.CreatedAt, &c.Members, &c.Handouts)
	return c, err
}
func (a *App) access(r *http.Request, id string, u *User) (Classroom, error) {
	c, err := scanClass(a.db.QueryRow(r.Context(), classSelect+`WHERE c.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, fail(404, "班级不存在")
	}
	if err != nil {
		return c, err
	}
	if c.OwnerID != u.ID {
		var member bool
		err = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM memberships WHERE class_id=$1 AND user_id=$2)`, id, u.ID).Scan(&member)
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
	rows, err := a.db.Query(r.Context(), classSelect+`WHERE c.owner_id=$1 OR EXISTS(SELECT 1 FROM memberships m WHERE m.class_id=c.id AND m.user_id=$1) ORDER BY c.created_at DESC`, u.ID)
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
	_, err := a.db.Exec(r.Context(), `INSERT INTO classrooms(id,name,description,owner_id,invite_code) VALUES($1,$2,$3,$4,$5)`, id, input.Name, input.Description, u.ID, strings.ToUpper(randomID(5)))
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
	tag, err := a.db.Exec(r.Context(), `UPDATE classrooms SET name=$1,description=$2 WHERE id=$3 AND owner_id=$4 AND NOT archived`, input.Name, input.Description, r.PathValue("id"), u.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
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
	tag, err := a.db.Exec(r.Context(), `UPDATE classrooms SET archived=$1 WHERE id=$2 AND owner_id=$3`, input.Archived, r.PathValue("id"), u.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fail(403, "无权管理此班级")
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
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var id string
	var archived bool
	err = tx.QueryRow(r.Context(), `SELECT id,archived FROM classrooms WHERE invite_code=$1 FOR UPDATE`, strings.ToUpper(strings.TrimSpace(input.Code))).Scan(&id, &archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return fail(404, "邀请码无效")
	}
	if err != nil {
		return err
	}
	if archived {
		return fail(409, "班级已归档，无法加入")
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO memberships(class_id,user_id) VALUES($1,$2)`, id, u.ID)
	if conflict(err) {
		return fail(409, "你已加入该班级")
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]string{"id": id})
	return nil
}
func (a *App) removeMember(w http.ResponseWriter, r *http.Request, u *User) error {
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
	target := r.PathValue("userID")
	if u.ID != owner && u.ID != target {
		return fail(403, "不能移除其他同学")
	}
	if archived && u.ID == owner {
		return fail(409, "请先恢复班级再管理成员")
	}
	tag, err := tx.Exec(r.Context(), `DELETE FROM memberships WHERE class_id=$1 AND user_id=$2`, r.PathValue("id"), target)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fail(404, "成员不存在")
	}
	if err = tx.Commit(r.Context()); err != nil {
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
	rows, err := a.db.Query(r.Context(), `SELECT u.id,u.username,u.name,u.role FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.class_id=$1 ORDER BY m.joined_at`, c.ID)
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
