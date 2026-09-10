# GKD-API

一个 OpenAI 格式兼容的 LLM 网关（类 New-API 的轻量实现）。管理员在后台把若干「真实模型」挂到同一个「虚拟模型」名下；客户端请求虚拟模型时，GKD-API 并发/分批地向真实模型发起请求，**赛马选出最先（或权重最高）可用者**，把响应中继给客户端，同时立即取消所有落选的上游请求以节省开销。

- 客户端只需支持 **OpenAI 格式**（`/v1/chat/completions`、`/v1/models`）
- 上游真实模型支持 **OpenAI 格式**（透传）与 **Gemini Generate Content**（自动双向翻译）
- 支持流式（SSE）与非流式，多模态图片（base64 / URL）
- 单二进制部署（前端已内嵌），SQLite 存储，无需外部依赖

## 路由规则（重头戏）

每个虚拟模型有三个可调参数：

| 参数 | 默认 | 说明 |
|---|---|---|
| `batch_size` 每轮并发数 | 0（一次全部） | 真实模型按**权重降序**分批发起请求 |
| `batch_wait_ms` 每轮等待窗口 | 3000ms | 窗口内无人响应则取消本轮，进入下一批 |
| `pick_mode` 选择策略 | `fastest` | 见下 |
| `max_wait_ms` 兜底超时 | 60000ms | 整场竞赛的总上限，超时返回 503 |

「已响应」的定义：上游返回 HTTP 200 且收到第一个响应字节（流式 = 第一个 SSE chunk）。非 2xx / 连接失败 / 超时的竞争者立即淘汰；批内全军覆没则立刻进入下一批。

**fastest —— 不择手段的快**：批内谁先响应谁立刻胜出，取消其余。等价于原「规则 A」：`batch_size=0 + fastest + 窗口=超时(3s)`，3s 内无人响应立即 503。

**weight —— 有择手段的快**：整场并发全部（`batch_size=0`），`batch_wait_ms` 即宽限时间；窗口结束时取**已响应中权重最高**者；若窗口结束时无人响应，继续等待首个响应（受 `max_wait_ms` 约束）。等价于原「规则 B」。

**省着点的快**：`batch_size=3`（可自定义）+ `fastest`。即原「规则 C」：每轮只请求权重最高的 N 个，轮内赛马；无人响应进入下一轮；全部轮次耗尽仍无响应则 503。

## 快速开始

```bash
make build          # 构建前端 + 单二进制 gkd-api
./gkd-api           # 默认 :8787，SQLite: ./gkd-api.db
```

首次启动会在日志中打印自动生成的管理员密码（可用 `GKD_ADMIN_PASSWORD` 指定）。浏览器打开 `http://127.0.0.1:8787/` 进入管理后台。

环境变量：

| 变量 | 默认 | 说明 |
|---|---|---|
| `GKD_PORT` | 8787 | 监听端口 |
| `GKD_DB` | gkd-api.db | SQLite 路径 |
| `GKD_ADMIN_PASSWORD` | 随机生成 | 首次启动的管理员密码 |

## Docker

镜像由 GitHub Actions 自动构建并推送（多架构 amd64/arm64）：

- 推送到 `main` 分支 → `ghcr.io/2ertwo6/gkd-api:latest` / `:main`
- 推送 `v*` 标签（如 `v1.2.0`）→ `ghcr.io/2ertwo6/gkd-api:1.2.0`、`:1.2`

```bash
docker run -d --name gkd-api -p 8787:8787 -v gkd-data:/data ghcr.io/2ertwo6/gkd-api:latest

# 或使用 compose
docker compose up -d
```

本地构建（国内网络）：

```bash
podman build --build-arg GOPROXY=https://goproxy.cn,direct \
  --build-arg NPM_REGISTRY=https://registry.npmmirror.com \
  -t gkd-api:local .
```

## 客户端调用

```bash
curl http://127.0.0.1:8787/v1/chat/completions \
  -H "Authorization: Bearer sk-gkd-xxxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"free","messages":[{"role":"user","content":"你好"}]}'
```

`Authorization: Bearer` 或 `x-goog-api-key` 头均可用于鉴权；密钥在管理后台「API 密钥」页创建。

## 管理 API（`/api/admin`，Bearer JWT）

- `POST /login`、`GET /me`、`POST /change-password`
- `GET|POST /virtual-models`、`GET|PUT|DELETE /virtual-models/:id`
- `POST /virtual-models/:id/real-models`、`PUT|DELETE /real-models/:id`
- `GET|POST /api-keys`、`PUT|DELETE /api-keys/:id`
- `GET /logs?page=&page_size=&virtual_model=`

## 参数覆写（JSON Merge Patch / RFC 7386）

每个真实模型可配置 `override_json`，在替换 model 名之后、协议翻译之前对 OpenAI 格式请求体合并覆写；值为 `null` 可删除字段：

```json
{ "temperature": 0.3, "reasoning_effort": null, "max_tokens": 2048 }
```

## 开发

```bash
make test          # 后端测试（含竞速引擎、翻译层、全链路 e2e）
cd web && npm run dev   # 前端开发（代理到 :8787）
```

目录结构：

```
cmd/server        入口
cmd/mockup        本地假上游（端到端演示用）
Dockerfile        多阶段构建（node → golang → distroless）
.github/workflows GitHub Actions：测试 + 自动构建多架构镜像到 GHCR
internal/config   环境变量配置
internal/db       GORM 模型与迁移
internal/auth     JWT 签发/校验
internal/admin    管理 API
internal/relay    客户端端点、请求准备、响应中继
internal/engine   竞速引擎（分批/窗口/双策略/取消）
internal/translate OpenAI ↔ Gemini 翻译
web               React + Vite + AntD 管理后台（go:embed 内嵌）
```

## 已知限制（v1）

- 跨协议（OpenAI 客户端 → Gemini 上游）暂不支持 tools/function calling；同协议透传不受影响
- Gemini 上游图片 URL 会由网关下载后转 base64（生成请求的额外延迟）
- 竞速落选请求会在胜者确定后立即取消，但上游计费取决于各家对中断请求的策略
