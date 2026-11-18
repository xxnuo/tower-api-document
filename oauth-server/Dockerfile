FROM node:22-alpine AS web-builder
WORKDIR /app/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.26-alpine AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go SKILL.md ./
COPY --from=web-builder /app/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /agents-tower .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=go-builder /agents-tower /app/agents-tower
ENV LISTEN_ADDR=[::]:8888
ENV DB_PATH=/app/data/agents-tower.db
VOLUME ["/app/data"]
EXPOSE 8888
CMD ["/app/agents-tower"]
