package main

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// sessionLifetime 同时用于浏览器会话与 Bearer 令牌；令牌不自动续期。
const sessionLifetime = 8 * time.Hour

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
	_, err = a.db.ExecContext(r.Context(), `INSERT INTO users(id,username,name,password_hash,role) VALUES(?,?,?,?,?)`, u.ID, u.Username, u.Name, string(hash), role)
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
	u, err := a.authenticate(r, c)
	if err != nil {
		return err
	}
	return a.session(w, r, u)
}

// token 用账号密码换取不透明 Bearer 令牌。令牌与浏览器会话共用 sessions 表，
// 因此撤销路径一致；签发过程不写 Cookie（令牌由调用方自行保存）。
func (a *App) token(w http.ResponseWriter, r *http.Request, _ *User) error {
	if a.limited(r) {
		return fail(429, "操作过于频繁，请 10 分钟后重试")
	}
	var c credentials
	if err := decode(w, r, &c); err != nil {
		return err
	}
	u, err := a.authenticate(r, c)
	if err != nil {
		return err
	}
	value := randomID(32)
	expires := time.Now().UTC().Add(sessionLifetime)
	if _, err = a.db.ExecContext(r.Context(), `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES(?,?,?)`, tokenHash(value), u.ID, expires); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"token": value, "expires_at": expires.Format(time.RFC3339), "user": u})
	return nil
}

// authenticate 校验账号密码。错误凭证统一返回 401 且不区分账号是否存在。
func (a *App) authenticate(r *http.Request, c credentials) (User, error) {
	u := User{}
	var hash string
	err := a.db.QueryRowContext(r.Context(), `SELECT id,username,name,role,password_hash FROM users WHERE username=?`, strings.ToLower(strings.TrimSpace(c.Username))).Scan(&u.ID, &u.Username, &u.Name, &u.Role, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(c.Password))
		return u, fail(401, "账号或密码错误")
	}
	if err != nil {
		return u, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(c.Password)) != nil {
		return u, fail(401, "账号或密码错误")
	}
	return u, nil
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
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,username,name,role FROM users WHERE role='teacher' ORDER BY created_at DESC`)
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
