# Funding Rate Config & Auto-Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立完整的 funding rate 计算参数配置体系，区分常量和变量，变量自动从交易所 API 同步。

**Architecture:** 将参数分为三层：(1) 全局常量写死在代码里，(2) 平台级规则（采样频率、平均方式等）写成平台 profile，(3) 动态变量（杠杆、利率、cap/floor 等）通过定时任务从 API 拉取并更新 config。

**Tech Stack:** Python, requests, InfluxDB, JSON config

---

## 参数分类分析

### 常量（hardcode in code，不需要配置）

| 参数 | 值 | 说明 |
|------|-----|------|
| Premium Index 公式 | `[Max(0, ImpBid-Idx) - Max(0, Idx-ImpAsk)] / Idx` | 三个平台完全一致 |
| 内层 Clamp 范围 | ±0.05% | Interest Rate 修正项的 clamp，三个平台一致 |
| IMN 计算公式 | `200 * max_leverage` | 三个平台本质一致（Binance: 200/IMR，OKX: 200×lever） |

### 平台级规则（per-exchange constants，写在 platform profile 里）

这些参数是**平台规则层面**的差异，不会随币种变化，但**不同平台之间不一样**。

| 参数 | Binance/Aster | OKX | 说明 |
|------|--------------|-----|------|
| 采样频率 | 5s | 60s | Binance 每5秒采一次，OKX 每分钟一次 |
| 平均方式 (1h interval) | **等权** | **时间加权** | 不一样！Binance 文档说 1h 用等权，OKX 始终时间加权 |
| 平均方式 (>1h interval) | 时间加权 | 时间加权 | 都用时间加权 |
| Interest Rate 来源 | API 返回固定值（通常 0.01%/期） | API 返回按期折算值（基于日利率 0.03%） | **不一样**，但都从 API 拿具体值，不需要客户端算公式 |
| Funding Rate 外层公式 | `[AvgP + clamp] / (8/N)`，**然后** 用 cap/floor 裁剪 | `clamp([AvgP + clamp], floor, cap)` **无 (8/N) 除法** | **不一样**，Binance 先除再 clamp，OKX 直接 clamp |
| Cap/Floor 计算方式 | 优先用 API 返回的 `adjustedFundingRateCap/Floor`；若无则 `±0.75 × maintMarginRatio` | API 直接返回 `maxFundingRate/minFundingRate` | 来源不同，但最终都是从 API 拿到具体数值 |

> **注意**：之前的分析说"三家公式一样"是不准确的。Premium Index 公式一样，但 Funding Rate 的最终计算公式（外层 clamp、(8/N) 除法、利率折算）在 Binance 和 OKX 之间有明确差异。

### 动态变量（per-exchange per-symbol，需要 API 自动同步）

| 参数 | 变化频率 | Binance/Aster API | OKX API |
|------|---------|-------------------|---------|
| **max_leverage** | 偶尔变（交易所调整风控） | `GET /fapi/v1/exchangeInfo` → `requiredMarginPercent` | `GET /api/v5/public/instruments` → `lever` |
| **funding_interval_hours** | 偶尔变 | `GET /fapi/v1/fundingInfo` → `fundingIntervalHours` | 从 `funding-rate` 的 `fundingTime` / `nextFundingTime` 差值推算 |
| **interest_rate** | 偶尔变 | `GET /fapi/v1/premiumIndex` → `interestRate` | `GET /api/v5/public/funding-rate` → `interestRate` |
| **funding_rate_cap** | 偶尔变 | `GET /fapi/v1/fundingInfo` → `adjustedFundingRateCap` | `GET /api/v5/public/funding-rate` → `maxFundingRate` |
| **funding_rate_floor** | 偶尔变 | `GET /fapi/v1/fundingInfo` → `adjustedFundingRateFloor` | `GET /api/v5/public/funding-rate` → `minFundingRate` |
| **maint_margin_ratio** | 偶尔变（Binance cap/floor 计算用） | `GET /fapi/v1/exchangeInfo` → `maintMarginPercent` | N/A（OKX 直接返回 cap/floor） |
| **impact_notional** | 派生值 = 200 * max_leverage | 不直接查，从 max_leverage 算 | API 返回 `impactValue` 可做校验 |

### 当前已知的具体值

| 参数 | Binance RAVEUSDT | Aster RAVEUSDT | OKX RAVEUSDT |
|------|-----------------|----------------|--------------|
| max_leverage | 20 | 5 | 20 |
| impact_notional | 4000 | 1000 | 4000 |
| funding_interval_hours | 1 | 1 | 1 |
| interest_rate | 0.0001 (0.01%) | 0.0001 (0.01%) | 0.0000125 (0.00125%) |
| funding_rate_cap | 0.02 (2%) | 未返回 | 0.015 (1.5%) |
| funding_rate_floor | -0.02 (-2%) | 未返回 | -0.015 (-1.5%) |
| maint_margin_ratio | 0.025 (2.5%) | 0.10 (10%) | N/A |
| sampling_interval | 5s | 5s | 60s |
| avg_method (1h) | equal | equal | time_weighted |

---

## File Structure

```
data-collector/
├── config.json                  # 修改: 新增 platform_profiles + 动态变量
├── collector.py                 # 修改: 无（已有 depth 采集）
├── sync_config.py               # 新建: 从 API 同步动态变量到 config.json
calc_funding_rate.py             # 修改: 读取新 config，按平台 profile 分别计算
```

---

### Task 1: 重构 config.json 结构

**Files:**
- Modify: `data-collector/config.json`

- [ ] **Step 1: 设计新的 config.json 结构**

```json
{
  "influxdb": {
    "url": "http://localhost:8101",
    "token": "funding-fee-monitor-token",
    "org": "funding",
    "bucket": "prices"
  },
  "pairs": [
    {
      "symbol": "RAVEUSDT",
      "exchanges": ["aster", "binance", "okx"]
    }
  ],
  "platform_profiles": {
    "binance": {
      "sampling_interval_sec": 5,
      "avg_method_1h": "equal",
      "avg_method_gt1h": "time_weighted",
      "interest_rate_formula": "fixed",
      "cap_floor_source": "maint_margin_ratio",
      "cap_floor_factor": 0.75
    },
    "aster": {
      "sampling_interval_sec": 5,
      "avg_method_1h": "equal",
      "avg_method_gt1h": "time_weighted",
      "interest_rate_formula": "fixed",
      "cap_floor_source": "maint_margin_ratio",
      "cap_floor_factor": 0.75
    },
    "okx": {
      "sampling_interval_sec": 60,
      "avg_method_1h": "time_weighted",
      "avg_method_gt1h": "time_weighted",
      "interest_rate_formula": "daily_rate",
      "daily_interest_rate": 0.0003,
      "cap_floor_source": "api_direct"
    }
  },
  "symbol_params": {
    "RAVEUSDT": {
      "binance": {
        "max_leverage": 20,
        "impact_notional": 4000,
        "funding_interval_hours": 1,
        "interest_rate": 0.0001,
        "funding_rate_cap": 0.02,
        "funding_rate_floor": -0.02,
        "maint_margin_ratio": 0.025,
        "_last_synced": "2026-04-16T07:00:00Z"
      },
      "aster": {
        "max_leverage": 5,
        "impact_notional": 1000,
        "funding_interval_hours": 1,
        "interest_rate": 0.0001,
        "funding_rate_cap": null,
        "funding_rate_floor": null,
        "maint_margin_ratio": 0.10,
        "_last_synced": "2026-04-16T07:00:00Z"
      },
      "okx": {
        "max_leverage": 20,
        "impact_notional": 4000,
        "funding_interval_hours": 1,
        "interest_rate": 0.0000125,
        "funding_rate_cap": 0.015,
        "funding_rate_floor": -0.015,
        "_last_synced": "2026-04-16T07:00:00Z"
      }
    }
  }
}
```

- [ ] **Step 2: 写入新 config.json**

- [ ] **Step 3: 验证 collector.py 仍然能正常启动**

因为 collector 只读 `influxdb`, `pairs`, `impact_config` 字段。`impact_config` 被替换为 `symbol_params`，需要同步更新 collector.py 中的读取逻辑。

- [ ] **Step 4: 更新 collector.py 读取 impact_notional 的路径**

从 `config["impact_config"][symbol][exchange]["impact_notional"]` 改为 `config["symbol_params"][symbol][exchange]["impact_notional"]`。

- [ ] **Step 5: 启动 collector 验证**

```bash
cd data-collector && timeout 10 ../venv/bin/python3 collector.py
```

Expected: 所有 9 个 WebSocket 连接成功，数据写入正常。

- [ ] **Step 6: Commit**

```bash
git add data-collector/config.json data-collector/collector.py
git commit -m "refactor: restructure config with platform profiles and symbol params"
```

---

### Task 2: 创建 sync_config.py — 自动同步动态变量

**Files:**
- Create: `data-collector/sync_config.py`

- [ ] **Step 1: 实现 Binance/Aster 参数同步**

```python
def sync_binance_params(symbol: str, base_url: str, verify_ssl: bool = True) -> dict:
    """
    从 Binance 兼容 API 拉取动态参数:
    - GET /fapi/v1/exchangeInfo → max_leverage, maint_margin_ratio
    - GET /fapi/v1/fundingInfo → funding_interval_hours, cap, floor
    - GET /fapi/v1/premiumIndex → interest_rate
    返回 symbol_params 字典。
    """
```

- [ ] **Step 2: 实现 OKX 参数同步**

```python
def sync_okx_params(symbol: str) -> dict:
    """
    从 OKX API 拉取动态参数:
    - GET /api/v5/public/instruments → max_leverage
    - GET /api/v5/public/funding-rate → interest_rate, cap, floor, interval, impact_value
    返回 symbol_params 字典。
    """
```

- [ ] **Step 3: 实现 config.json 更新逻辑**

```python
def sync_all(config_path: str = "config.json"):
    """
    读取 config.json，遍历所有 pairs，对每个 exchange 调用对应的 sync 函数，
    更新 symbol_params 并写回 config.json。
    """
```

- [ ] **Step 4: 运行并验证**

```bash
cd data-collector && ../venv/bin/python3 sync_config.py
cat config.json  # 验证 symbol_params 已更新，_last_synced 已刷新
```

- [ ] **Step 5: Commit**

```bash
git add data-collector/sync_config.py
git commit -m "feat: add sync_config.py to auto-sync exchange params"
```

---

### Task 3: 重写 calc_funding_rate.py — 按平台规则分别计算

**Files:**
- Modify: `calc_funding_rate.py`

- [ ] **Step 1: 重构为读取新 config 结构**

从 `data-collector/config.json` 读取 `platform_profiles` 和 `symbol_params`，不再 hardcode 任何动态参数。

- [ ] **Step 2: 实现 Premium Index 采样逻辑**

根据平台 profile 的 `sampling_interval_sec`，用对应的 InfluxDB aggregateWindow：
- Binance/Aster: `every: 5s`
- OKX: `every: 1m`

- [ ] **Step 3: 实现两种平均方式**

```python
def equal_avg(premiums: list) -> float:
    return sum(premiums) / len(premiums)

def time_weighted_avg(premiums: list) -> float:
    n = len(premiums)
    return sum((i + 1) * p for i, p in enumerate(premiums)) / (n * (n + 1) / 2)
```

根据 `funding_interval_hours` 和 profile 的 `avg_method_1h` / `avg_method_gt1h` 选择。

- [ ] **Step 4: 实现 Interest Rate 获取逻辑**

- Binance/Aster (`interest_rate_formula: "fixed"`): 直接从 `symbol_params` 读 `interest_rate`
- OKX (`interest_rate_formula: "daily_rate"`): 从 `symbol_params` 读 `interest_rate`（API 已经算好了每期利率）

- [ ] **Step 5: 实现 Cap/Floor 逻辑**

- Binance/Aster (`cap_floor_source: "maint_margin_ratio"`):
  ```python
  cap = cap_floor_factor * maint_margin_ratio   # 0.75 * 0.025 = 0.01875
  floor = -cap
  ```
  如果 `adjustedFundingRateCap` 不为 null，优先使用。
- OKX (`cap_floor_source: "api_direct"`): 直接用 `funding_rate_cap` / `funding_rate_floor`

- [ ] **Step 6: 实现完整的 Funding Rate 计算**

```python
def calc_funding_rate(avg_premium, interest_rate, interval_hours, cap, floor):
    clamp_val = max(-0.0005, min(0.0005, interest_rate - avg_premium))
    raw_fr = (avg_premium + clamp_val) / (8 / interval_hours)
    if cap is not None and floor is not None:
        raw_fr = max(floor, min(cap, raw_fr))
    return raw_fr
```

- [ ] **Step 7: 用 impact_price measurement 替换 best bid/ask**

改为从 InfluxDB 的 `impact_price` measurement 读取 `impact_bid_price` / `impact_ask_price`，而不是 `price` measurement 的 `bid` / `ask`。

- [ ] **Step 8: 运行并对比**

```bash
python3 calc_funding_rate.py
```

对比我们算出的 FR 与交易所报告的 FR，验证数值更接近。

- [ ] **Step 9: Commit**

```bash
git add calc_funding_rate.py
git commit -m "refactor: calc_funding_rate reads config, supports per-platform rules"
```

---

### Task 4: 集成 — sync 定时运行 + collector 启动时 sync

**Files:**
- Modify: `data-collector/collector.py` — 启动时调用 sync

- [ ] **Step 1: collector.py 启动时先 sync 一次**

在 `PriceCollector.__init__` 中调用 `sync_config.sync_all()`，确保启动时参数最新。

- [ ] **Step 2: 添加定时 sync 线程**

每小时调用一次 `sync_all()` 并重新加载 config 中的 `symbol_params`。

- [ ] **Step 3: 测试完整流程**

```bash
cd data-collector && timeout 30 ../venv/bin/python3 collector.py
# 验证启动日志中有 sync 输出
# 验证 config.json 已更新
# 验证 impact_price 数据正常写入
```

- [ ] **Step 4: Commit**

```bash
git add data-collector/collector.py
git commit -m "feat: auto-sync exchange params on startup and hourly"
```
