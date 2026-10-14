package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
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

	"github.com/go-sql-driver/mysql"
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
	db              *sql.DB
	uploads, origin string
	secure          bool
	knowledge       bool
	vectors         *vectorStore
	gateway         *gatewayClient
	seedEnabled     bool
	seedDir         string
	seedClassName   string
	profileID       string
	mu              sync.Mutex
	attempts        map[string]attempt
}
type apiError struct {
	status  int
	message string
}

// rowScanner 同时适配 *sql.Row 与 *sql.Rows，便于共用一个扫描函数。
type rowScanner interface {
	Scan(dest ...any) error
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

// randomUUID 生成带连字符的 UUID 文本；切片标识要同时用作 Qdrant 点标识。
func randomUUID() string {
	raw := randomID(16)
	return raw[0:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:32]
}
func tokenHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func env(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}

// conflict 识别唯一键冲突（MySQL 1062）。
func conflict(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

// isAlreadyApplied 把重复对象错误视为「迁移已应用」。
// MySQL 的 DDL 会隐式提交，无法像 PostgreSQL 那样整体回滚，因此迁移脚本写成可重复执行的形式。
func isAlreadyApplied(err error) bool {
	var me *mysql.MySQLError
	if !errors.As(err, &me) {
		return false
	}
	switch me.Number {
	case 1050, // 表已存在
		1060, // 列已存在
		1061, // 索引已存在
		1826: // 外键名已存在
		return true
	}
	return false
}

type endpoint func(http.ResponseWriter, *http.Request, *User) error

// sameOrigin 判定写请求是否来自本站：
//   - 浏览器明确标注 cross-site 的直接拒绝；
//   - 没带 Origin（curl、同源表单）按同源处理；
//   - Origin 与请求自身的 Host 一致（localhost、127.0.0.1、局域网地址都算）通过；
//   - 否则必须命中 APP_ORIGIN（支持逗号分隔多个来源，用于反向代理或自定义域名）。
func (a *App) sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if parsed, err := url.Parse(origin); err == nil {
		host := r.Host
		// 有些反向代理会去掉端口，因此主机名一致也算同源（端口再由 APP_ORIGIN 兜底）。
		if strings.EqualFold(parsed.Host, host) ||
			(strings.EqualFold(parsed.Hostname(), hostname(host)) && parsed.Hostname() != "") {
			return true
		}
	}
	for _, allowed := range strings.Split(a.origin, ",") {
		if strings.TrimSpace(allowed) == origin {
			return true
		}
	}
	return false
}

// hostname 去掉 Host 里的端口，兼容 IPv6 字面量写法。
func hostname(host string) string {
	if strings.HasPrefix(host, "[") {
		if end := strings.Index(host, "]"); end > 0 {
			return host[1:end]
		}
	}
	if index := strings.LastIndex(host, ":"); index > 0 {
		return host[:index]
	}
	return host
}

// routeSpec 描述一个对外操作。路由注册与 `docs/openapi.json` 的一致性检查共用这一份数据，
// 因此新增接口时不会出现「实现了但文档没有」或反之的漂移。
type routeSpec struct {
	Method  string
	Pattern string
	Auth    bool
	Handler endpoint
}

func (a *App) mount(mux *http.ServeMux, spec routeSpec) {
	mux.HandleFunc(spec.Method+" "+spec.Pattern, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" && r.Method != "HEAD" {
			if !bearerOnly(r) && !a.sameOrigin(r) {
				writeJSON(w, 403, map[string]string{"error": "不允许跨站请求"})
				return
			}
		}
		var u *User
		var err error
		if spec.Auth {
			u, err = a.currentUser(r)
		}
		if err == nil {
			err = spec.Handler(w, r, u)
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
// requestToken 取出请求携带的凭证：优先 Authorization: Bearer，其次浏览器 Cookie。
func requestToken(r *http.Request) (string, string) {
	if header := r.Header.Get("Authorization"); len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:]), "bearer"
	}
	if c, err := r.Cookie("teaching_session"); err == nil {
		return c.Value, "cookie"
	}
	return "", ""
}

// bearerOnly 判断请求是否「只带 Bearer 令牌」。令牌不会被浏览器自动携带，
// 因此这类请求不做同源校验；同时带 Cookie 的请求仍按 Cookie 规则校验，防 CSRF。
func bearerOnly(r *http.Request) bool {
	value, kind := requestToken(r)
	if kind != "bearer" || value == "" {
		return false
	}
	_, err := r.Cookie("teaching_session")
	return err != nil
}

func (a *App) currentUser(r *http.Request) (*User, error) {
	value, kind := requestToken(r)
	if value == "" {
		return nil, fail(401, "请先登录")
	}
	u := &User{}
	err := a.db.QueryRowContext(r.Context(), `SELECT u.id,u.username,u.name,u.role FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>NOW(6)`, tokenHash(value)).Scan(&u.ID, &u.Username, &u.Name, &u.Role)
	if errors.Is(err, sql.ErrNoRows) {
		if kind == "bearer" {
			return nil, fail(401, "令牌无效或已过期")
		}
		return nil, fail(401, "登录已过期，请重新登录")
	}
	return u, err
}
func (a *App) session(w http.ResponseWriter, r *http.Request, u User) error {
	token := randomID(32)
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES(?,?,?)`, tokenHash(token), u.ID, time.Now().UTC().Add(sessionLifetime))
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "teaching_session", Value: token, Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode, MaxAge: int(sessionLifetime.Seconds())})
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
	for _, spec := range a.routes() {
		a.mount(mux, spec)
	}
	return mux
}

//go:embed migrations/*.sql
var migrationFiles embed.FS

// splitStatements 按行尾分号切分脚本；迁移文件与建表脚本都由本仓库维护，不含分号字面量。
func splitStatements(script string) []string {
	out := []string{}
	var b strings.Builder
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
		if strings.HasSuffix(trimmed, ";") {
			statement := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(b.String()), ";"))
			if statement != "" {
				out = append(out, statement)
			}
			b.Reset()
		}
	}
	if rest := strings.TrimSpace(b.String()); rest != "" {
		out = append(out, rest)
	}
	return out
}

func applyMigrations(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked sql.NullInt64
	if err = conn.QueryRowContext(ctx, `SELECT GET_LOCK('teaching-platform-migrations', 30)`).Scan(&locked); err != nil {
		return err
	}
	if !locked.Valid || locked.Int64 != 1 {
		return errors.New("could not acquire migration lock")
	}
	defer conn.ExecContext(context.Background(), `SELECT RELEASE_LOCK('teaching-platform-migrations')`)
	if _, err = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version varchar(190) NOT NULL PRIMARY KEY, applied_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		return err
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		version := entry.Name()
		var exists bool
		if err = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=?)`, version).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		body, readErr := migrationFiles.ReadFile("migrations/" + version)
		if readErr != nil {
			return readErr
		}
		for _, statement := range splitStatements(string(body)) {
			if _, err = conn.ExecContext(ctx, statement); err != nil && !isAlreadyApplied(err) {
				return fmt.Errorf("migration %s: %w", version, err)
			}
		}
		if _, err = conn.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES(?)`, version); err != nil {
			return err
		}
	}
	return nil
}

func openDatabase(ctx context.Context) (*sql.DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// checkServerVariables 拒绝在 ngram_token_size 不为 2 的实例上写入索引：
// 该变量是只读启动参数，改动后必须重建全文索引。
func checkServerVariables(ctx context.Context, db *sql.DB) error {
	var ngram int
	if err := db.QueryRowContext(ctx, `SELECT @@ngram_token_size`).Scan(&ngram); err != nil {
		return err
	}
	if ngram != 2 {
		return fmt.Errorf("ngram_token_size must be 2, got %d", ngram)
	}
	var charset string
	if err := db.QueryRowContext(ctx, `SELECT @@character_set_server`).Scan(&charset); err != nil {
		return err
	}
	if charset != "utf8mb4" {
		return fmt.Errorf("character_set_server must be utf8mb4, got %s", charset)
	}
	return nil
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
	if len(os.Args) > 1 && os.Args[1] == "worker-healthcheck" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		db, err := openDatabase(ctx)
		if err != nil {
			os.Exit(1)
		}
		defer db.Close()
		var ok int
		err = db.QueryRowContext(ctx, `SELECT IF(EXISTS(SELECT 1 FROM knowledge_jobs WHERE status='running' AND lease_until < NOW(6) - INTERVAL 1 MINUTE),0,1)`).Scan(&ok)
		if err != nil || ok != 1 {
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := openDatabase(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	for _, statement := range splitStatements(schema) {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			log.Fatal(err)
		}
	}
	knowledge := env("KNOWLEDGE_ENABLED", "false") == "true"
	if knowledge {
		if err = checkServerVariables(ctx, db); err != nil {
			log.Fatal(err)
		}
		if err = applyMigrations(ctx, db); err != nil {
			log.Fatal(err)
		}
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
	a := &App{db: db, uploads: uploads, origin: origin, secure: env("COOKIE_SECURE", "false") == "true", knowledge: knowledge, attempts: map[string]attempt{}}
	if knowledge {
		dimensions, dimErr := envInt("EMBEDDING_DIMENSIONS", 1024)
		if dimErr != nil {
			log.Fatal(dimErr)
		}
		a.gateway = newGatewayClient(dimensions)
		a.profileID = env("EMBEDDING_PROFILE_ID", "gateway-embedding-v1")
		if len(a.profileID) > 100 {
			log.Fatal("EMBEDDING_PROFILE_ID must not exceed 100 characters")
		}
		a.vectors = newVectorStore(dimensions)
		if err = a.vectors.ensureCollection(ctx); err != nil {
			log.Fatal(err)
		}
		if a.gateway.configured() {
			if err = a.ensureKnowledgeProfile(ctx); err != nil {
				log.Fatal(err)
			}
		} else {
			// 网关未配置时不注册嵌入配置，避免写入空模型名后与后续真实配置冲突。
			log.Print("课程网关未配置（GATEWAY_BASE_URL 与 EMBEDDING_MODEL），知识库索引与检索在配置完成前不可用")
		}
		a.seedEnabled = env("SEED_ENABLED", "false") == "true"
		a.seedDir = env("SEED_DIR", "./seeds")
		a.seedClassName = env("SEED_CLASS_NAME", "示例班级")
		// 提示词与内置技能不依赖网关，只要知识库启用就保证存在（幂等，不覆盖已有内容）。
		if err = a.ensurePlatformPrompts(ctx); err != nil {
			log.Fatal(err)
		}
	}
	if err = a.bootstrap(ctx); err != nil {
		log.Fatal(err)
	}
	if a.seedEnabled {
		if err = a.importSeeds(ctx); err != nil {
			log.Printf("seed import: %v", err)
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "worker" {
		if !knowledge {
			log.Fatal("KNOWLEDGE_ENABLED must be true for worker")
		}
		if err = a.runKnowledgeWorker(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatal(err)
		}
		return
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
				if _, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at<NOW(6)`); err != nil {
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

func envInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	var value int
	if _, err := fmt.Sscanf(raw, "%d", &value); err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
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
	_, err = a.db.ExecContext(ctx, `INSERT INTO users(id,username,name,password_hash,role) VALUES(?,?,?,?,'admin') ON DUPLICATE KEY UPDATE id=id`, randomID(16), username, "平台管理员", string(hash))
	if err != nil {
		return err
	}
	var role string
	if err = a.db.QueryRowContext(ctx, `SELECT role FROM users WHERE username=?`, username).Scan(&role); err != nil {
		return err
	}
	if role != "admin" {
		return fmt.Errorf("ADMIN_USERNAME conflicts with an existing non-administrator account")
	}
	return nil
}
