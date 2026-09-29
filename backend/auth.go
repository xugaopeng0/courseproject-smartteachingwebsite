package main

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-only-placeholder"), bcrypt.DefaultCost)

func normalize(c *credentials) error {
	c.Username = strings.ToLower(strings.TrimSpace(c.Username))
	c.Name = strings.TrimSpace(c.Name)
	if !usernamePattern.MatchString(c.Username) {
		return fail(400, "账号须为 3–32 位字母、数字或下划线")
	}
	if utf8.RuneCountInString(c.Name) < 1 || utf8.RuneCountInString(c.Name) > 40 {
		return fail(400, "姓名须为 1–40 个字符")
	}
	if len(c.Password) < 8 || len(c.Password) > 72 {
		return fail(400, "密码长度须为 8–72 字节")
	}
	return nil
}
func (a *App) addUser(r *http.Request, c credentials, role string) (User, error) {
	u := User{ID: randomID(16), Username: c.Username, Name: c.Name, Role: role}
	hash, err := bcrypt.GenerateFromPassword([]byte(c.Password), bcrypt.DefaultCost)
	if err != nil {
		return u, err
	}
	_, err = a.db.Exec(r.Context(), `INSERT INTO users(id,username,name,password_hash,role) VALUES($1,$2,$3,$4,$5)`, u.ID, u.Username, u.Name, string(hash), role)
	if conflict(err) {
		return u, fail(409, "该账号已被使用")
	}
	return u, err
}
func (a *App) register(w http.ResponseWriter, r *http.Request, _ *User) error {
	if a.limited(r) {
		return fail(429, "操作过于频繁，请 10 分钟后重试")
	}
	var c credentials
	if err := decode(w, r, &c); err != nil {
		return err
	}
	if err := normalize(&c); err != nil {
		return err
	}
	u, err := a.addUser(r, c, "student")
	if err != nil {
		return err
	}
	return a.session(w, r, u)
}
func (a *App) login(w http.ResponseWriter, r *http.Request, _ *User) error {
	if a.limited(r) {
		return fail(429, "操作过于频繁，请 10 分钟后重试")
	}
	var c credentials
	if err := decode(w, r, &c); err != nil {
		return err
	}
	u := User{}
	var hash string
	err := a.db.QueryRow(r.Context(), `SELECT id,username,name,role,password_hash FROM users WHERE username=$1`, strings.ToLower(strings.TrimSpace(c.Username))).Scan(&u.ID, &u.Username, &u.Name, &u.Role, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(c.Password))
		return fail(401, "账号或密码错误")
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(c.Password)) != nil {
		return fail(401, "账号或密码错误")
	}
	return a.session(w, r, u)
}
func (a *App) createTeacher(w http.ResponseWriter, r *http.Request, u *User) error {
	if u.Role != "admin" {
		return fail(403, "只有管理员可以创建教师账号")
	}
	var c credentials
	if err := decode(w, r, &c); err != nil {
		return err
	}
	if err := normalize(&c); err != nil {
		return err
	}
	teacher, err := a.addUser(r, c, "teacher")
	if err != nil {
		return err
	}
	writeJSON(w, 201, teacher)
	return nil
}
func (a *App) teachers(w http.ResponseWriter, r *http.Request, u *User) error {
	if u.Role != "admin" {
		return fail(403, "无权访问")
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,username,name,role FROM users WHERE role='teacher' ORDER BY created_at DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	list := []User{}
	for rows.Next() {
		var t User
		if err = rows.Scan(&t.ID, &t.Username, &t.Name, &t.Role); err != nil {
			return err
		}
		list = append(list, t)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, list)
	return nil
}
