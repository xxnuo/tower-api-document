FROM node:22-alpine AS web-builder
WORKDIR /app/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.24-alpine AS go-builder
WORKDIR /app
COPY go.mod ./
COPY main.go ./
COPY --from=web-builder /app/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /tower-oauth .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=go-builder /tower-oauth /app/tower-oauth
ENV LISTEN_ADDR=[::]:8888
EXPOSE 8888
CMD ["/app/tower-oauth"]
