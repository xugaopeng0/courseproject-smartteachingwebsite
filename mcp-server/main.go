package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("%v", err)
	}
}

// run 读取配置、启动 HTTP 服务并等待退出信号。把启动逻辑拆出来是为了让
// 「未配置令牌必须拒绝启动」可以被单元测试覆盖。
func run() error {
	token := strings.TrimSpace(os.Getenv("MCP_AUTH_TOKEN"))
	if token == "" {
		return errMissingToken
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8090"
	}
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           newMux(token),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	shutdownErr := make(chan error, 1)

	go func() {
		// 日志只打印端口与工具数量，绝不打印令牌。
		log.Printf("mcp tool server listening on :%s（工具 %d 个）", port, len(tools()))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			shutdownErr <- err
		}
	}()

	select {
	case err := <-shutdownErr:
		return err
	case <-stop:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(ctx)
	}
}
