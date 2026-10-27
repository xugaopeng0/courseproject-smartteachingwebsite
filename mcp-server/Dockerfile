FROM golang:1.26-alpine AS build
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mcp-server .

FROM alpine:3.23
RUN addgroup -S app && adduser -S app -G app
COPY --from=build /mcp-server /usr/local/bin/mcp-server
USER app
EXPOSE 8090
# 令牌只经环境变量注入：镜像内不含任何令牌。
HEALTHCHECK --interval=15s --timeout=3s --retries=5 CMD wget -q -O- http://127.0.0.1:8090/health || exit 1
ENTRYPOINT ["/usr/local/bin/mcp-server"]
