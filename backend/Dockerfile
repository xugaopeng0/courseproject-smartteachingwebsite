FROM golang:1.26-alpine AS build
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server .

FROM alpine:3.23
RUN apk add --no-cache libreoffice-writer libreoffice-impress poppler-utils font-noto-cjk && \
    addgroup -S app && adduser -S app -G app && mkdir -p /data/uploads /tmp && chown -R app:app /data /tmp
WORKDIR /app
COPY --from=build /server ./server
USER app
EXPOSE 8081
ENTRYPOINT ["/app/server"]
