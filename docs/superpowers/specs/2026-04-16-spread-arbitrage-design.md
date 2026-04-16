# 价差收敛自动交易套利工具 - 设计文档

## 概述

一个独立的跨交易所永续合约价差收敛自动套利系统。监控同一币对在不同交易所的价格差异，当价差满足条件时自动开仓/平仓，赚取价差收敛利润。提供 Web 界面供用户查看持仓、交易记录、管理任务。

## 策略逻辑

### 交易模型

- **标的：** 同一永续合约在不同交易所之间的价差（架构预留现货支持）
- **交易所：** Aster / Binance / OKX，支持任意两个所的组合
- **每个任务指定 ExchangeA 和 ExchangeB，以及 Spread 方向（做多/做空）**

### 价差计算

使用盘口价格，阈值为绝对价差：

```
Spread = ExchangeA价格 - ExchangeB价格
```

其中价格取盘口价，具体取 bid 还是 ask 取决于实际成交方向（见下方开平仓条件）。

### 方向：做空 Spread vs 做多 Spread

每个任务配置一个方向（`Direction`）：

**做空 Spread (`short_spread`)**：预期价差会缩小
- 开仓条件：`Spread（A.bid - B.ask）> OpenThreshold`，连续 `ConfirmCount` 次满足
- 开仓动作：卖出 A + 买入 B
- 平仓条件：`Spread（A.ask - B.bid）< CloseThreshold`，连续 `ConfirmCount` 次满足
- 平仓动作：买入 A + 卖出 B
- 约束：`OpenThreshold > CloseThreshold`

**做多 Spread (`long_spread`)**：预期价差会扩大
- 开仓条件：`Spread（A.ask - B.bid）< OpenThreshold`，连续 `ConfirmCount` 次满足
- 开仓动作：买入 A + 卖出 B
- 平仓条件：`Spread（A.bid - B.ask）> CloseThreshold`，连续 `ConfirmCount` 次满足
- 平仓动作：卖出 A + 买入 B
- 约束：`OpenThreshold < CloseThreshold`

**连续确认机制：** 每次收到新行情数据时检查条件，只有连续 `ConfirmCount` 次都满足才触发动作。任何一次不满足则计数器归零。开仓和平仓各自维护独立的计数器。

### 仓位管理

- 每次开仓/平仓数量固定（按标的个数，非 USDT 金额）
- 设置最大持仓上限，未达上限时可持续开仓
- 下单方式：市价单

## 技术选型

| 层面 | 选择 | 理由 |
|------|------|------|
| 后端语言 | Go | 高性能，天然并发模型适合管理多 WS 连接 |
| 前端框架 | React | |
| 架构 | 单进程 + Goroutine | 部署简单，goroutine 管理并发 |
| 数据存储 | 无（全内存） | 任务状态在内存中，持仓/交易记录从交易所 API 实时查询 |
| 行情数据源 | WebSocket | 低延迟实时行情 |
| 下单接口 | REST API | 交易所标准下单方式 |
| API Key 管理 | `.env` 文件 + `godotenv` | 简单安全，`.gitignore` 排除 |

## 系统架构

```
┌─────────────────────────────────────────────┐
│                 Go 单进程                     │
│                                              │
│  ┌──────────┐  ┌──────────┐  ┌───────────┐  │
│  │ WS 行情   │  │ 策略引擎  │  │ HTTP/WS   │  │
│  │ Manager   │→│ Engine    │←│ API Server │  │
│  └──────────┘  └──────────┘  └───────────┘  │
│       ↑              ↑↓             ↑        │
│  各交易所WS      任务管理器      React前端    │
│  (Aster/BN/OKX)  (内存状态)                  │
└─────────────────────────────────────────────┘
```

### 核心组件

1. **WS Manager** — 管理到各交易所的 WebSocket 连接，接收实时行情，校验数据时效性（超过阈值的数据丢弃）
2. **Engine（策略引擎）** — 接收有效行情数据，计算价差，与阈值比较，触发开仓/平仓信号，调用交易所下单 API 执行
3. **Task Manager（任务管理器）** — 内存中管理所有任务的配置和状态（running / stopped），供引擎和 API 使用
4. **API Server** — 提供 HTTP REST API + WebSocket 推送，供 React 前端调用

### 数据流

- **行情：** 交易所 WS → WS Manager（时效校验）→ Engine（价差计算 + 信号判断）→ 交易所 REST API（下单）
- **前端：** React ←→ API Server ←→ Task Manager / Engine

## 任务模型

```go
Task {
    ID               string
    Symbol           string     // e.g. "RAVEUSDT"
    ExchangeA        string     // 交易所 A
    ExchangeB        string     // 交易所 B
    Direction        string     // "long_spread" | "short_spread"
    Status           string     // "running" | "stopped"

    // 策略参数
    OpenThreshold    float64    // 开仓阈值（绝对价差）
    CloseThreshold   float64    // 平仓阈值（绝对价差）
    ConfirmCount     int        // 连续满足条件的次数才触发（开仓/平仓共用）
    QuantityPerOrder float64    // 每次下单数量（标的个数）
    MaxPositionQty   float64    // 最大持仓上限（标的个数）
    DataMaxLatencyMs int64      // 行情数据最大允许延迟 (ms)
}
```

## API 接口

### REST API

| 方法 | 路径 | 功能 |
|------|------|------|
| `GET` | `/api/tasks` | 获取所有任务列表及状态 |
| `POST` | `/api/tasks` | 创建新任务 |
| `PUT` | `/api/tasks/:id` | 修改任务参数 |
| `POST` | `/api/tasks/:id/start` | 启动任务 |
| `POST` | `/api/tasks/:id/stop` | 停止任务 |
| `DELETE` | `/api/tasks/:id` | 删除任务 |
| `POST` | `/api/tasks/:id/open` | 手动触发开仓（一笔固定数量） |
| `POST` | `/api/tasks/:id/close` | 手动触发平仓（一笔固定数量） |
| `GET` | `/api/tasks/:id/positions` | 查询持仓（从交易所实时拉取） |
| `GET` | `/api/tasks/:id/trades` | 查询交易记录（从交易所实时拉取） |

### WebSocket 推送

- `ws://host/ws` — 推送实时数据给前端：当前价差、任务状态变化、开平仓事件

## 前端页面

单页面布局，任务详情默认展开：

### 任务管理区

- 任务列表表格：Symbol、交易所对、阈值参数、当前价差、状态
- 操作按钮：启动、停止、编辑参数、手动开仓、手动平仓、删除
- 创建新任务表单

### 任务详情（默认展开）

- 当前持仓信息：两个交易所各自的持仓方向、数量、开仓均价、未实现盈亏
- 交易记录列表：时间、方向（开仓/平仓）、数量、价格、两端价差

### 实时更新

通过 WebSocket 接收后端推送，价差和状态实时刷新。

## 错误处理

### 单边成交

两边同时市价下单，如果一边成功另一边失败：
- 立即尝试平掉已成交的那一边
- 停止该任务
- 通过 WebSocket 推送错误事件给前端

### WebSocket 断线

- 立即标记该交易所数据为无效
- 暂停涉及该交易所的所有任务的自动交易
- 自动重连，恢复后继续

### 数据延迟

延迟判断：交易所消息中的时间戳 vs 本地收到时间，差值超过 `DataMaxLatencyMs` 则丢弃该数据。两端数据都必须在有效期内才允许交易决策。

### 进程崩溃

全内存设计，进程重启后所有任务状态丢失。用户需手动重新创建任务。已开仓位不受影响（在交易所侧），用户可通过交易所查看并手动处理。

## 项目目录结构

```
spread-arbitrage/
├── .env                        # 交易所 API Key（gitignore）
├── .env.example                # .env 模板（提交到 git）
├── go.mod
├── go.sum
├── main.go                     # 程序入口
├── config.go                   # 配置加载（读取 .env）
├── internal/
│   ├── engine/
│   │   ├── engine.go           # 策略引擎：价差计算、信号判断、下单执行
│   │   └── task.go             # 任务模型与任务管理器
│   ├── exchange/
│   │   ├── client.go           # 交易所统一接口定义
│   │   ├── binance.go          # Binance 实现（WS + REST）
│   │   ├── aster.go            # Aster 实现
│   │   └── okx.go              # OKX 实现
│   └── api/
│       ├── server.go           # HTTP + WS 服务
│       ├── routes.go           # REST 路由注册
│       └── ws.go               # WebSocket 推送管理
├── web/                        # React 前端
│   ├── package.json
│   ├── src/
│   │   ├── App.tsx
│   │   ├── components/
│   │   │   ├── TaskList.tsx     # 任务列表
│   │   │   ├── TaskDetail.tsx   # 任务详情（持仓 + 交易记录）
│   │   │   └── TaskForm.tsx     # 创建/编辑任务表单
│   │   └── hooks/
│   │       └── useWebSocket.ts  # WS 连接 hook
│   └── index.html
└── README.md
```
