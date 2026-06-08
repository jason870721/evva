# PRD — Veronica Phase 2：Crypto Trading-Team 驗證 — Plan

> 狀態：**草案 / Draft（方向已定，依賴 Phase 1 完成）** ｜ 日期：2026-06-02
> 上層：[`roadmap.md`](roadmap.md) ｜ 設計：[`veronica-design-v1.md`](veronica-design-v1.md) ｜ 前置：[`prd-phase1-swarm.md`](prd-phase1-swarm.md)
> 定位：**這是驗證實驗，不是交易產品。** 目的在證明 swarm 好不好用，不在賺錢。全程 **paper / testnet**。

---

## 1. TL;DR — 用 trader team 驗證 swarm

組一個 5 agent 的加密貨幣交易策略小組，當作 Phase 1 swarm 的**真實、連續、多角色**壓測：

- **friday**（Leader/CEO）：協調全員、對 User 負責、有 **kill switch**（叫停所有人）。
- **agent-trader**：交易決策者，取行情 → 決策 → （紙上）下單/開平倉 → 記 ledger（原因/根據/PnL）。
- **agent-analyst**：分析趨勢/風控/熱點，出報告給 trader。
- **agent-risk-monitor**：**定時**巡檢倉位，違規告警 trader、通報 friday。
- **agent-reviewer**：**每日固定時間**復盤 trader 當日操作 + PnL，總結建議交 friday，friday 據此可要 trader 改策略。

它剛好壓測 swarm 的每個機制（mesh / bus / task 帳本 / 共享讀域表 / roster / **timer 喚醒** / 重啟接續 / kill switch）。**「swarm 實用」的可測定義見 §3。**

---

## 2. Inventory — Phase 1 給了什麼 + 外部依賴

| 來自 Phase 1（直接用） | 用途 |
| --- | --- |
| `evva service` / `evva swarm .` + `evva-swarm.yml` | 啟動這支 5 agent 集群 |
| mesh + `send_message` + mailbox + drain A/B | analyst→trader、risk→trader/friday、reviewer→friday 的報告迴路 |
| task 帳本 + 5 狀態機（Leader-only） | friday 指派/驗收 trader、analyst 的任務 |
| **timer 喚醒**（`profile.yml schedule`） | risk-monitor 定時巡檢、reviewer 每日復盤 |
| 共享讀域表 | reviewer 讀 trader 的 trades/pnl、risk 讀 positions |
| `HaltAll`（supervisor）+ suspend | friday kill switch |
| 重啟接續（reload 未讀 + `ResumeSession`） | 連續運行的崩潰恢復 |
| permission broker → Web | 下單工具審批 |

| 外部依賴（Phase 2 自寫/接） | 備註 |
| --- | --- |
| 行情來源（OHLCV、ticker） | 交易所 public API 或資料商；唯讀 |
| 交易所帳戶（**testnet / paper**） | 下單/查倉；**v1 不接 mainnet 真錢** |
| 交易所 client（Go） | 自寫 custom tool 包裝（REST/WS）；不做成 evva 內建 |

---

## 3. Goal & 驗證準則（什麼叫「swarm 實用」被證明）

**Goal**：在 paper/testnet 上讓五 agent 連續自主協作，量測 swarm 的實用性，並把痛點回填 Phase 1。

**驗證準則（全達成 = 通過；全程 paper/testnet）：**

- **V1 — 連續自主**：五 agent 連續運作 **≥ 3 個交易日**，無需人工重啟/解卡。
- **V2 — 分工迴路運作**：analyst 報告 → trader 決策（紙上下單）→ risk-monitor 定時巡檢且違規時確實告警 → reviewer 每日復盤交 friday → friday 能據此要求 trader 調整，且 trader 收到並反映。**每一條箭頭都有 log/訊息佐證。**
- **V3 — kill switch**：friday 一聲令下，所有 agent 在飛 run 被中止、swarm 暫停（確定性，不靠 prompt 運氣）。
- **V4 — 重啟接續且對帳一致**：service 重啟後五 agent 接續；本地 ledger 與 testnet 交易所實際持倉 **reconcile 無偏差**。
- **V5 — 成本可觀測**：能報出每日 token / Run 次數；idle agent 不燒 token（timer/event 驅動生效）。
- **V6 — 安全護欄生效**：硬限額（單筆/總曝險/最大回撤）由 code/交易所層擋下，至少一次「LLM 想越線但被硬擋」的實證。

> 注意：**驗證準則不含「是否獲利」。** 這是 swarm 機制驗證，不是策略績效評比。

---

## 4. The team — 5 agents 規格

| agent | 角色 | 喚醒來源 | 主要工具 | prompt 重點 | 讀寫的表 |
| --- | --- | --- | --- | --- | --- |
| **friday** | Leader（main） | message（含 user prompt）、risk 告警 | `task_*`、`send_message`、`list_members`、**`halt_all`**、`Agent(spawn)`（驗收用） | CEO / 團隊管理 / 統籌 / 風險最終決策；**非交易員人格** | 寫 `tasks`；讀全部 |
| **agent-trader** | Worker（task-driven） | 被指派、收 analyst 報告、收 risk 告警 | `market_data`、`place_order`、`open/close_position`、`record_trade`、`send_message`、`my_tasks` | 交易決策；每次操作必寫「原因/根據/本次 PnL」 | 寫 `agent_trader_trades/positions/pnl`（共享讀）；讀 analyst 報告 |
| **agent-analyst** | Worker（task + timer） | 被指派、定時掃市場 | `market_data`、`web`(新聞/熱點)、`send_message` | 趨勢方向 / 風控視角 / 熱點；產出結構化報告 | 寫 `agent_analyst_reports`（共享讀）；或直接寄信給 trader |
| **agent-risk-monitor** | Worker（常駐值班） | **timer（定期）** + 倉位變動 | `read_positions`、`risk_check`、`send_message` | 對照風控標準；違規即告警 trader、通報 friday | 讀 trader `positions`；寫 `agent_risk_alerts` |
| **agent-reviewer** | Worker（常駐值班） | **timer（每日固定時間）** | `read_trades_pnl`、`send_message` | 復盤當日操作 + PnL，給經驗總結與策略建議 | 讀 trader `trades/pnl`；寫 `agent_reviewer_daily` |

每個成員一個目錄：`agents/main/friday/` 與 `agents/sub/{trader,analyst,risk-monitor,reviewer}/`，各含全自訂 `system_prompt.md` + `profile.yml`（含 model tier 與 `schedule`）。

**model tier 建議（成本）**：trader/analyst 用強模型；risk-monitor/reviewer 可用較便宜模型（規則性巡檢/總結）。

---

## 5. Domain tools 規格（自寫 custom tools）

全部用 `pkg/tools.Tool` 介面；下單/開平倉類**預設綁 permission gate**（§6）。

| 工具 | 輸入 | 行為 | 唯讀? | permission |
| --- | --- | --- | --- | --- |
| `market_data` | `{symbol, timeframe, limit}` | 取 OHLCV / ticker | 是 | auto-allow |
| `read_positions` | `{}` | 查當前（paper）持倉 | 是 | auto-allow |
| `read_trades_pnl` | `{since}` | 讀 trade ledger + PnL | 是 | auto-allow |
| `risk_check` | `{}` | 對照風控標準回違規清單 | 是 | auto-allow |
| `place_order` | `{symbol, side, type, qty, price?}` | （紙上/testnet）下單 | **否** | **ask（gate）** |
| `open_position` / `close_position` | `{symbol, side, qty, ...}` | 開/平倉 | **否** | **ask（gate）** |
| `record_trade` | `{reason, basis, symbol, side, qty, entry, pnl?}` | 寫 trade ledger（trader 自有表） | 否(自有) | auto-allow |
| `halt_all` | `{reason}` | friday 觸發 supervisor `HaltAll`（kill switch） | 否 | auto-allow（僅 friday 持有） |

> **硬限額不在這些工具的 LLM 判斷裡**——見 §6。這些工具是「手」，限額是「保險絲」，分開。

---

## 6. 安全護欄（即使 paper 也照做，養成正確架構）

1. **paper-first / testnet only（v1）**：`place_order`/`open/close_position` 一律走紙上或交易所 testnet。**上 mainnet 真錢是 Phase 2 之後的獨立決策，不在本 PRD。**
2. **下單走 permission gate**：交易類工具預設 `ask`，由 friday 或 User 在 Web 審批（`Controller.RespondPermission`）。可設「testnet 自動 allow、mainnet 必審」的規則。
3. **硬限額 = code/交易所層，不靠 LLM**：單筆上限、總曝險、最大槓桿、最大回撤做成 **deterministic circuit breaker**（在 `place_order` 工具入口檢查並拒絕）或**交易所原生 stop order**。risk-monitor agent 負責「策略級判斷」，不負責毫秒級硬停。**V6 要實證一次「想越線被硬擋」。**
4. **friday kill switch = 確定性路徑**：`halt_all` → supervisor `HaltAll`（cancel 全部 run + 暫停 swarm），可選串一個 code 層「市價平倉/撤單」動作（不靠 prompt）。
5. **重啟對帳以交易所為準**：本地 `.vero` ledger 是協作紀錄；**倉位真相在交易所**。重啟時先 reconcile（拉 testnet 實際持倉）再續（V4）。
6. **稽核**：所有下單/狀態轉移/告警寫 SQLite，Web 可回看（誰、何時、為何）。

---

## 7. Work breakdown（ordered）

### P2-M0 — paper skeleton（最小決策迴路）
- 只 friday + trader 兩角；`market_data`（紙上行情）+ `place_order`（紙上）+ `record_trade`。
- friday 指派一個「依當前行情做一次紙上交易決策」的 task；trader 執行、記 ledger、回報；friday 驗收。
- **Gate**：最小「指派→取行情→決策→紙上下單→記帳→回報→驗收」迴路在 Web 看得到。

### P2-M1 — full team（報告迴路成形）
- 補 analyst（出報告給 trader）、risk-monitor（timer 巡檢 + 告警）、reviewer（每日復盤交 friday）。
- 接 permission gate、硬限額 circuit breaker、kill switch。
- **Gate**：V2（分工迴路每條箭頭有佐證）+ V3（kill switch）+ V6（硬限額擋下一次）。

### P2-M2 — endurance run（連續壓測 + 評估報告）
- 連續多日 paper run；量測 V1（≥3 日自主）、V4（重啟對帳）、V5（成本/idle 不燒 token）。
- **產出**：一份 **「swarm 實用性評估」報告**——哪些機制好用、哪些是痛點、哪些要**回填 Phase 1**（補 DoD）。
- **Gate**：V1–V6 全達成；評估報告完成。

---

## 8. Design decisions & risks

- **8.1 trader/analyst 走 task 狀態機；risk-monitor/reviewer 是常駐值班（timer 驅動、無 task row）**——驗證 §5.5 的「task 狀態機 per-agent 可選」。
- **8.2 報告傳遞二選一**：`send_message`（即時、會喚醒收件人）vs 寫共享表（被動、收件人下次醒來才讀）。建議：**即時性需求用 send_message（如 risk 告警），週期性產物用共享表 + 一封通知信**。
- **8.3 LLM 非確定性**：交易「決策」由 LLM 出，必然不穩定——**這是研究平台**，V 準則只看協作機制，不看績效。
- **風險**：① 連續運行成本（緩解：便宜 model tier + 調 `schedule` 頻率 + idle 不燒 token）；② 行情 API rate limit（緩解：快取 + 共享 `market_data` 結果）；③ reviewer/risk 讀 trader 表的時序一致性（緩解：讀走 store 的 `RLock`）；④ 真錢誘惑（紀律：本 PRD 硬性 paper/testnet）。

---

## 9. Out of scope（Phase 2）

- **mainnet 真錢交易**（獨立決策，Phase 2 之後）。
- 任何 swarm 內部機制的修改——Phase 2 只當 swarm 的**使用者**；缺東西就回填 Phase 1。
- 策略績效最佳化 / 回測框架（這裡只驗協作，不做量化研究平台）。
- bundled 交易所整合（交易所 client 是本實驗自寫的 custom tool）。
- 多策略 / 多市場擴張。

---

## 10. Verification checklist (gate)

- [ ] V1 連續自主 ≥3 交易日（paper）。
- [ ] V2 分工迴路每條箭頭有 log/訊息佐證。
- [ ] V3 friday kill switch 確定性生效。
- [ ] V4 重啟接續 + 與 testnet 交易所對帳無偏差。
- [ ] V5 成本可觀測 + idle agent 不燒 token。
- [ ] V6 硬限額至少擋下一次「想越線」。
- [ ] 下單類工具走 permission gate；硬限額在 code/交易所層。
- [ ] 產出「swarm 實用性評估」報告（含回填 Phase 1 的痛點清單）。
