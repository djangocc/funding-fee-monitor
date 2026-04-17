# Curitiba — 加密货币资金费率监控与套利系统

本项目包含多个独立程序，围绕永续合约资金费率和跨交易所价差进行数据采集、监控和自动化交易。

## 程序一览

| 程序 | 语言 | 说明 |
|------|------|------|
| `data-collector/` | Python | 实时行情数据采集，写入 InfluxDB |
| `web/` | Python (Flask) | 资金费率可视化 Web 仪表盘 |
| `monitor.py` | Python (Tkinter) | 桌面端实时价差监控报警 |
| `spread-arbitrage/` | Go + React | 跨交易所价差套利自动交易系统 |

---

### data-collector — 行情数据采集

通过 WebSocket 实时采集多交易所行情数据并写入 InfluxDB。

- 采集内容：买一/卖一价格 (bookTicker)、指数价/标记价、盘口深度
- 支持交易所：Binance、Aster、OKX
- 100ms 聚合写入，自动重连
- 每小时从交易所 API 同步杠杆倍数、资金费率上限等配置

```bash
cd data-collector
python collector.py
```

配置文件：`data-collector/config.json`（InfluxDB 连接、交易对、交易所参数）

---

### web — 资金费率可视化仪表盘

Flask Web 服务，从 InfluxDB 读取历史数据，展示价格走势和资金费率图表。

- 合约价格、指数价格、标记价格折线图
- 溢价率 (Premium%) 与资金费率 (Funding Rate%) 双轴图
- 跨交易所价差 K 线图
- 支持 1s ~ 1h 多种时间粒度

```bash
cd web
python app.py
```

访问 `http://localhost:5000`

---

### monitor.py — 桌面端价差监控

Tkinter 桌面 GUI 程序，实时显示各交易所价格和资金费率，价差超阈值时声音报警。

- WebSocket 实时价格 + 15s 轮询资金费率
- 可配置开仓/平仓报警阈值
- 窗口置顶、可拖拽、按交易所/方向独立静音

```bash
python monitor.py
```

---

### spread-arbitrage — 跨交易所价差套利

Go 后端 + React 前端的自动化套利交易系统。详细部署说明见 [spread-arbitrage/README.md](spread-arbitrage/README.md)。

- 监控同一合约在两个交易所的价差，满足条件自动开仓/平仓
- 支持做空价差和做多价差两种策略
- 连续确认机制防止瞬间波动误触发
- 单边失败自动反向恢复，最多重试 3 次
- Web UI：订单簿、资金费率、持仓、委托记录，配对订单悬浮显示价差

```bash
cd spread-arbitrage
cp .env.example .env   # 配置 API 密钥
cd web && npm install && npm run build && cd ..
go build -o spread-arbitrage .
./spread-arbitrage
```

访问 `http://localhost:18527`

---

## 风险提示与安全建议

**免责声明：本项目仅供学习和研究目的，不构成任何投资建议。加密货币交易具有极高风险，包括但不限于市场波动、交易所故障、网络延迟、程序 Bug 等均可能导致资金损失。使用本系统进行实盘交易造成的任何损失，作者不承担任何责任。**

### 建议

- **使用子账户 + 小资金试运行。** 所有交易所都支持创建子账户，先用少量资金验证策略逻辑和系统稳定性，确认无误后再逐步增加资金
- **API 权限遵循最小原则。** 只开启必要的权限（合约交易），不要开启提币权限；务必绑定 IP 白名单，限制为运行本程序的服务器 IP
- **不要在公网暴露 Web UI。** 本系统没有用户认证，如需远程访问请通过 SSH 隧道或 VPN
- **妥善保管 `.env` 文件。** 包含交易所密钥和私钥，不要提交到 Git，不要分享给他人
