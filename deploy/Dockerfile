# Multi-stage build producing one image per Go binary via `--target`.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 GOOS=linux go build -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 GOOS=linux go build -o /out/cli ./cmd/cli

FROM alpine:3.20 AS api
RUN apk add --no-cache ca-certificates
COPY --from=build /out/api /usr/local/bin/api
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]

# worker shells out to ffmpeg/ffprobe (internal/infra/executor/local) — the
# only image that needs anything beyond the static binary.
FROM alpine:3.20 AS worker
RUN apk add --no-cache ca-certificates ffmpeg
COPY --from=build /out/worker /usr/local/bin/worker
ENTRYPOINT ["/usr/local/bin/worker"]

FROM alpine:3.20 AS migrate
RUN apk add --no-cache ca-certificates
COPY --from=build /out/migrate /usr/local/bin/migrate
ENTRYPOINT ["/usr/local/bin/migrate"]

FROM alpine:3.20 AS cli
RUN apk add --no-cache ca-certificates
COPY --from=build /out/cli /usr/local/bin/cli
ENTRYPOINT ["/usr/local/bin/cli"]

# ---- Frontend: Vite build served by nginx, fronting the whole stack ----
FROM node:20-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
# Relative path — nginx proxies /api/ to the api service on the same
# origin, so the browser never needs an absolute backend URL baked in.
ARG VITE_API_BASE=/api/v1
ENV VITE_API_BASE=$VITE_API_BASE
RUN npm run build

FROM nginx:1.27-alpine AS web
COPY --from=web-build /src/web/dist /usr/share/nginx/html
COPY deploy/nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
