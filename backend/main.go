package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

//go:embed schema.sql
var schema string

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Role     string `json:"role"`
}
type attempt struct {
	count int
	until time.Time
}
type App struct {
	db              *pgxpool.Pool
	uploads, origin string
	secure          bool
	mu              sync.Mutex
	attempts        map[string]attempt
}
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string           { return e.message }
func fail(status int, message string) error { return &apiError{status, message} }
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fail(400, "请求内容不正确")
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return fail(400, "请求只能包含一个 JSON 对象")
	}
	return nil
}
func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func tokenHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func env(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
func conflict(err error) bool { var e *pgconn.PgError; return errors.As(err, &e) && e.Code == "23505" }

type endpoint func(http.ResponseWriter, *http.Request, *User) error

func (a *App) route(mux *http.ServeMux, pattern string, auth bool, fn endpoint) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" && r.Method != "HEAD" {
			origin := r.Header.Get("Origin")
			if (origin != "" && origin != a.origin) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				writeJSON(w, 403, map[string]string{"error": "不允许跨站请求"})
				return
			}
		}
		var u *User
		var err error
		if auth {
			u, err = a.currentUser(r)
		}
		if err == nil {
			err = fn(w, r, u)
		}
		if err != nil {
			var e *apiError
			if errors.As(err, &e) {
				writeJSON(w, e.status, map[string]string{"error": e.message})
			} else {
				log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
				writeJSON(w, 500, map[string]string{"error": "服务暂时不可用，请稍后重试"})
			}
		}
	})
}
func (a *App) currentUser(r *http.Request) (*User, error) {
	c, err := r.Cookie("teaching_session")
	if err != nil {
		return nil, fail(401, "请先登录")
	}
	u := &User{}
	err = a.db.QueryRow(r.Context(), `SELECT u.id,u.username,u.name,u.role FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>now()`, tokenHash(c.Value)).Scan(&u.ID, &u.Username, &u.Name, &u.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fail(401, "登录已过期，请重新登录")
	}
	return u, err
}
func (a *App) session(w http.ResponseWriter, r *http.Request, u User) error {
	token := randomID(32)
	_, err := a.db.Exec(r.Context(), `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)`, tokenHash(token), u.ID, time.Now().Add(8*time.Hour))
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "teaching_session", Value: token, Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode, MaxAge: 28800})
	writeJSON(w, 200, u)
	return nil
}
func (a *App) limited(r *http.Request) bool {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for k, v := range a.attempts {
		if now.After(v.until) {
			delete(a.attempts, k)
		}
	}
	v := a.attempts[ip]
	if v.until.IsZero() {
		v.until = now.Add(10 * time.Minute)
	}
	v.count++
	a.attempts[ip] = v
	return v.count > 40
}
func (a *App) handler() http.Handler {
	mux := http.NewServeMux()
	a.route(mux, "GET /api/health", false, func(w http.ResponseWriter, r *http.Request, _ *User) error {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := a.db.Ping(ctx); err != nil {
			return fail(503, "数据库未就绪")
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
		return nil
	})
	a.route(mux, "POST /api/auth/register", false, a.register)
	a.route(mux, "POST /api/auth/login", false, a.login)
	a.route(mux, "POST /api/auth/logout", false, func(w http.ResponseWriter, r *http.Request, _ *User) error {
		if c, err := r.Cookie("teaching_session"); err == nil {
			if _, err = a.db.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash=$1`, tokenHash(c.Value)); err != nil {
				return err
			}
		}
		http.SetCookie(w, &http.Cookie{Name: "teaching_session", Value: "", Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	})
	a.route(mux, "GET /api/auth/me", true, func(w http.ResponseWriter, r *http.Request, u *User) error { writeJSON(w, 200, u); return nil })
	a.route(mux, "GET /api/admin/teachers", true, a.teachers)
	a.route(mux, "POST /api/admin/teachers", true, a.createTeacher)
	a.route(mux, "GET /api/classes", true, a.classes)
	a.route(mux, "POST /api/classes", true, a.createClass)
	a.route(mux, "POST /api/classes/join", true, a.joinClass)
	a.route(mux, "GET /api/classes/{id}", true, a.classDetail)
	a.route(mux, "PATCH /api/classes/{id}", true, a.updateClass)
	a.route(mux, "POST /api/classes/{id}/archive", true, a.archiveClass)
	a.route(mux, "DELETE /api/classes/{id}/members/{userID}", true, a.removeMember)
	a.route(mux, "POST /api/classes/{id}/handouts", true, a.upload)
	a.route(mux, "GET /api/handouts/{id}/file", true, a.download)
	a.route(mux, "DELETE /api/handouts/{id}", true, a.deleteHandout)
	return mux
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		client := http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://127.0.0.1:8081/api/health")
		if err != nil || resp.StatusCode != 200 {
			os.Exit(1)
		}
		resp.Body.Close()
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, schema); err != nil {
		log.Fatal(err)
	}
	uploads, err := filepath.Abs(env("UPLOAD_DIR", "./uploads"))
	if err != nil {
		log.Fatal(err)
	}
	if err = os.MkdirAll(uploads, 0700); err != nil {
		log.Fatal(err)
	}
	origin := env("APP_ORIGIN", "http://localhost:8080")
	if u, err := url.Parse(origin); err != nil || u.Host == "" {
		log.Fatal("APP_ORIGIN must be an absolute origin")
	}
	a := &App{db: pool, uploads: uploads, origin: origin, secure: env("COOKIE_SECURE", "false") == "true", attempts: map[string]attempt{}}
	if err = a.bootstrap(ctx); err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Addr: env("LISTEN_ADDR", ":8081"), Handler: a.handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second}
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at<now()`); err != nil {
					log.Print(err)
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	fmt.Printf("Teaching API listening on %s\n", srv.Addr)
	if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
func (a *App) bootstrap(ctx context.Context) error {
	username := strings.ToLower(env("ADMIN_USERNAME", "admin"))
	if !usernamePattern.MatchString(username) {
		return fmt.Errorf("ADMIN_USERNAME must contain 3–32 lowercase letters, digits or underscores")
	}
	password := os.Getenv("ADMIN_PASSWORD")
	if len(password) < 8 || len(password) > 72 {
		return fmt.Errorf("ADMIN_PASSWORD must contain 8–72 bytes; configure .env before startup")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(ctx, `INSERT INTO users(id,username,name,password_hash,role) VALUES($1,$2,$3,$4,'admin') ON CONFLICT(username) DO NOTHING`, randomID(16), username, "平台管理员", string(hash))
	if err != nil {
		return err
	}
	var role string
	if err = a.db.QueryRow(ctx, `SELECT role FROM users WHERE username=$1`, username).Scan(&role); err != nil {
		return err
	}
	if role != "admin" {
		return fmt.Errorf("ADMIN_USERNAME conflicts with an existing non-administrator account")
	}
	return nil
}
