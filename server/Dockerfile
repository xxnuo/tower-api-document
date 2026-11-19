FROM node:22-alpine AS web-builder
WORKDIR /app/web
RUN corepack enable && corepack install --global pnpm@12.21.0
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
ARG VERSION
RUN test -z "$VERSION" || test "$VERSION" = "$(node -p 'require("./package.json").version')"
RUN pnpm build

FROM golang:1.26-alpine AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go SKILL.md ./
COPY --from=web-builder /app/web/package.json ./web/
COPY --from=web-builder /app/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /agents-tower .

FROM alpine:3.23
ARG VERSION
LABEL org.opencontainers.image.version=$VERSION
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=go-builder /agents-tower /app/agents-tower
ENV LISTEN_ADDR=[::]:8888
ENV DB_PATH=/app/data/data.db
VOLUME ["/app/data"]
EXPOSE 8888
ENTRYPOINT ["/app/agents-tower"]
