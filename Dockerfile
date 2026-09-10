# ---------- 前端构建 ----------
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
ARG NPM_REGISTRY=https://registry.npmjs.org
RUN npm ci --registry=${NPM_REGISTRY}
COPY web/ ./
RUN npm run build

# ---------- 后端构建 ----------
FROM golang:1.26.8-alpine AS build
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} GOTOOLCHAIN=local CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN go build -trimpath -ldflags="-s -w" -o /out/gkd-api ./cmd/server \
    && mkdir -p /out/data

# ---------- 运行镜像（distroless，含 CA 证书） ----------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gkd-api /app/gkd-api
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV GKD_PORT=8787 \
    GKD_DB=/data/gkd-api.db
VOLUME /data
EXPOSE 8787
WORKDIR /app
ENTRYPOINT ["/app/gkd-api"]
