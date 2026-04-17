# Spread Arbitrage

跨交易所永续合约价差收敛套利系统。监控同一合约在不同交易所的价格差异，当价差满足条件时自动开仓/平仓。

## 支持的交易所

- **Binance** — 普通账户 (fapi) 或统一账户/Portfolio Margin (papi)
- **Aster** — v3 API，EIP-712 钱包签名
- **OKX** — HMAC-SHA256 签名

## 环境要求

- Go 1.22+
- Node.js 18+

## 配置 API 密钥

```bash
cp .env.example .env
```

编辑 `.env`（只需配置你要用的交易所，未配置的会正常启动但无法交易）：

```env
# Binance
BINANCE_API_KEY=
BINANCE_API_SECRET=
BINANCE_UNIFIED=true

# Aster (v3 EIP-712)
ASTER_USER=
ASTER_SIGNER=
ASTER_PRIVATE_KEY=

# OKX
OKX_API_KEY=
OKX_API_SECRET=
OKX_PASSPHRASE=

# 服务端口
PORT=18527
```

### 如何获取各交易所 API Key

#### Binance

1. 登录 https://www.binance.com → 账户 → API 管理
2. 创建 API Key，勾选「Enable Futures」权限
3. 建议绑定 IP 白名单
4. `BINANCE_UNIFIED`：如果你的账户是统一账户 (Portfolio Margin)，设为 `true`；普通合约账户设为 `false`。在 Binance 网页端看到「Portfolio Margin」标识即为统一账户

#### Aster

Aster v3 API 使用 EIP-712 钱包签名认证，不需要传统 API Key。需要在 Aster 平台创建 Agent（API 钱包）：

1. 登录 https://app.asterdex.com，切换到 **Pro API** 模式
2. 创建一个 **Agent**（API 钱包），平台会生成一个专用的 signer 地址
3. 填写 `.env`：
   - `ASTER_USER` — 你的主账户钱包地址（登录 Aster 的钱包）
   - `ASTER_SIGNER` — 创建 Agent 后获得的 API 钱包地址
   - `ASTER_PRIVATE_KEY` — Agent 钱包的私钥（不带 `0x` 前缀）

API 文档：https://asterdex.github.io/aster-api-website/futures-v3/general-info/

#### OKX

1. 登录 https://www.okx.com → API 页面
2. 创建 API Key，设置 Passphrase，勾选「Trade」权限
3. 建议绑定 IP 白名单
4. 填入 `OKX_API_KEY`、`OKX_API_SECRET`、`OKX_PASSPHRASE`

## 本地开发

本地开发时，Go 后端和 React 前端分别运行。前端 Vite 开发服务器自动将 `/api` 和 `/ws` 代理到后端。

**终端 1 — Go 后端（端口 18527）：**

```bash
go run .
```

**终端 2 — 前端开发服务器（端口 18528）：**

```bash
cd web
npm install   # 首次运行
npm run dev
```

浏览器打开 **http://localhost:18528** 进行开发，前端代码修改后自动热更新。

> 注意：本地开发访问 18528（前端），不是 18527（后端）。18528 会自动代理 API 请求到 18527。

## 服务器部署

服务器上前端编译成静态文件，由 Go 服务直接托管，只需一个进程、一个端口。

### 1. 构建前端

```bash
cd web
npm install
npm run build
cd ..
```

### 2. 编译运行

```bash
go build -o spread-arbitrage .
./spread-arbitrage
```

浏览器打开 **http://服务器IP:18527**。

### 3. systemd 守护进程（推荐）

```bash
sudo tee /etc/systemd/system/spread-arbitrage.service > /dev/null <<'EOF'
[Unit]
Description=Spread Arbitrage
After=network.target

[Service]
Type=simple
WorkingDirectory=/path/to/spread-arbitrage
ExecStart=/path/to/spread-arbitrage/spread-arbitrage
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now spread-arbitrage
```

确保 `.env` 和 `web/dist/` 都在 `WorkingDirectory` 下。

```bash
sudo systemctl status spread-arbitrage    # 查看状态
sudo journalctl -u spread-arbitrage -f    # 实时日志
sudo systemctl restart spread-arbitrage   # 重启
```

## 使用流程

1. 打开 Web UI，选择币对和两个交易所
2. 在 Setup 页面点击「Test Trade」验证 API 连通性
3. 进入交易视图，创建任务并设置价差阈值
4. 启动自动交易，或使用手动开仓/平仓按钮

## 架构

```
main.go              入口，初始化所有组件
config.go            加载 .env 配置
internal/
  model/types.go     共享数据类型
  exchange/          交易所客户端（Binance/Aster/OKX）
  engine/            交易引擎、价差评估、任务管理
  wsmanager/         WebSocket 行情订阅管理
  api/               REST API + WebSocket 推送
web/                 React 前端（Vite + React）
```
