package main

import (
	"embed"
	"net/http"
	"strings"
)

// 对外契约与文档页面的唯一来源是 docs/openapi.json 与 frontend/node_modules/swagger-ui-dist；
// scripts/sync-openapi.mjs 把它们同步到 backend/ 下，openapi_test.go 每次都会校验副本没有过期。
//
//go:embed openapi.json
var openapiContract []byte

//go:embed swaggerui
var swaggerAssets embed.FS

// openapiDocument 提供机器可读契约（GET /api/openapi.json）。
func (a *App) openapiDocument(w http.ResponseWriter, r *http.Request, _ *User) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, err := w.Write(openapiContract)
	return err
}

// apiDocsPage 提供可直接打开的接口文档页面（GET /api/docs）。
// 页面只引用本站资源，离线环境同样可用；填入 Bearer 令牌即可试调受保护接口。
func (a *App) apiDocsPage(w http.ResponseWriter, r *http.Request, _ *User) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := w.Write([]byte(apiDocsHTML))
	return err
}

// apiDocsAsset 提供文档页面的静态资源（GET /api/docs/{asset}）。
func (a *App) apiDocsAsset(w http.ResponseWriter, r *http.Request, _ *User) error {
	name := r.PathValue("asset")
	if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return fail(404, "资源不存在")
	}
	raw, err := swaggerAssets.ReadFile("swaggerui/" + name)
	if err != nil {
		return fail(404, "资源不存在")
	}
	switch {
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".png"):
		w.Header().Set("Content-Type", "image/png")
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, err = w.Write(raw)
	return err
}

const apiDocsHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>知序 API 文档</title>
<link rel="stylesheet" href="/api/docs/swagger-ui.css" />
<link rel="icon" type="image/png" href="/api/docs/favicon-32x32.png" sizes="32x32" />
<style>
  body { margin: 0; background: #fff; }
  .docs-hint { font: 13px/1.7 "Microsoft YaHei", system-ui, sans-serif; color: #62636d; padding: 14px 20px 0; }
  .docs-hint strong { color: #b82e3c; }
</style>
</head>
<body>
<p class="docs-hint">
  先在 <strong>POST /api/auth/token</strong> 用账号密码换一个令牌，再点右上角 <strong>Authorize</strong> 填入令牌，即可试调需要登录的接口。
  浏览器本身的 Cookie 会话也可直接使用（页面同源访问 <code>/api</code>）。
</p>
<div id="swagger-ui"></div>
<script src="/api/docs/swagger-ui-bundle.js" crossorigin></script>
<script src="/api/docs/swagger-ui-standalone-preset.js" crossorigin></script>
<script>
  window.ui = SwaggerUIBundle({
    url: "/api/openapi.json",
    dom_id: "#swagger-ui",
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
    layout: "BaseLayout",
    deepLinking: true,
    persistAuthorization: true,
    docExpansion: "list",
    tryItOutEnabled: true,
    requestInterceptor: function (request) {
      request.credentials = "same-origin";
      return request;
    },
  });
</script>
</body>
</html>
`
