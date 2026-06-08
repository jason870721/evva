# Veronica — Roadmap（兩階段）

> 狀態：**草案 / Draft（方向已定，未進入實作）** ｜ 日期：2026-06-02
> 原則：**品質大於速度**、長期目標、finish before expand。
> 關聯文件：
> - 設計 / 架構：[`veronica-design-v1.md`](veronica-design-v1.md)
> - Phase 1 需求：[`prd-phase1-swarm.md`](prd-phase1-swarm.md)
> - Phase 2 需求：[`prd-phase2-trader-team.md`](prd-phase2-trader-team.md)

---

## 1. TL;DR — 這份 roadmap 是什麼

把 Veronica 從「初期想法」落成「可執行、有 gate 的兩階段計畫」：

- **Phase 1 — 蓋 swarm 基礎設施本身。** 判準：能不能穩定協調一群長壽 root agent（指派、訊息、持久化、重啟接續）。
- **Phase 2 — 用 crypto trading team 驗證 swarm 的實用性。** 判準：一個真實、連續、多角色的工作負載能不能在 swarm 上長時間自主運作。

**順序是硬的：Phase 1 的「完成定義」（§5 DoD）全綠之前，不開 Phase 2。** Phase 2 不得反過來「為了讓 trading 跑起來」在 swarm 層打補丁——若 Phase 2 發現 swarm 缺東西，回填 Phase 1 並補 DoD，而不是在 Phase 2 繞過。

---

## 2. 兩階段全景

| | Phase 1 — Swarm 基礎設施 | Phase 2 — Trading-Team 驗證 |
| --- | --- | --- |
| 目標 | 把「一群 root agent 協作」做成穩定可用的 runtime | 證明 swarm 在真實連續負載下實用 |
| 產出 | `evva service` / `evva swarm` + supervisor/bus/store/roster/webapi + 公開 inbox-drainer seam | friday/trader/analyst/risk-monitor/reviewer 五 agent + 域工具 + 安全護欄 |
| 完成判準 | §5 DoD 全綠 | §6 驗證準則達成（paper 模式） |
| 依賴 | 只 evva `pkg/*`（+ M4 加一個公開 seam） | Phase 1 DoD + 外部行情/交易所（testnet） |
| 不碰 | 真錢、交易邏輯 | swarm 內部機制（只當使用者） |

---

## 3. 排序原則

1. **finish before expand**：Phase 1 內 M0→M4 嚴格按依賴；不跳階段。
2. **純 `pkg/*` 優先**：唯一動 evva runtime 的（需求② inbox-drainer）排到 M4，且做成**公開 additive seam**（§1.1 紀律）。
3. **每個里程碑都要「能跑起來看到東西」**（walking-skeleton 哲學）——不存在「只有程式碼、跑不起來」的里程碑。
4. **paper-first**：Phase 2 一律先紙上交易 / testnet；真錢是 Phase 2 之後另開的決策，不在本 roadmap。
5. **測試與 dep-check 同里程碑交付**：不留「之後補測試」的尾巴。
6. **multi-space native**：service 從 M0 就是「一個 process 管多套互相隔離 swarm」的 multi-space host（設計 §3.1），不存在 single-space hardcode——這是核心能力，不是後補。

---

## 4. Phase 1 里程碑（M0–M4）

每個里程碑：**目標 / 交付 / Gate（可測 acceptance）/ 依賴**。Gate 沒過不進下一個。

### M0 — Walking skeleton（service 即 multi-space host）
- **目標**：證明「service 作為 **multi-space host** + 多 root agent + Web sink」主幹通。
- **交付**：`evva service start`（背景 :8888，內部含 **SwarmSpace registry**）；`evva swarm .` 註冊一個 `leader + 1 worker` 的 space；開 `.vero/vero.db`；Web 首頁是 **space 選單**、進去顯示 2 個 agent 在線；User 能在 Web 對 leader 送一句 prompt、看到回覆串流。
- **Gate**：① service 起得來、`evva service status` 正確；② `evva swarm .` 後 Web 看得到該 space 的 2 個 agent；③ 對 leader 的一輪對話能在 Web 即時看到 `event.Event` 串流；④ `go list -deps` 對 `internal/swarm` 不含 `internal/agent`；⑤ **在另一個目錄再跑 `evva swarm .` → Web space 選單出現第二個、與第一個完全獨立的 space（各自 db/roster/命名，互不可見）**。
- **依賴**：純 `pkg/*`。

### M1 — 共享任務帳本 + roster
- **目標**：Leader 能拆派、Worker 能執行回報、任務狀態在 Web 看板正確流動。
- **交付**：`tasks` 表 + 5 狀態機（**Leader-only writes**）；`task_create/assign/update_status/verify/list`、`my_tasks/task_get`、`list_members` 工具；Web **Team Board**（kanban）+ **Agent Roster**。
- **Gate**：① Leader `task_create`→`assign`→worker 執行→回報→Leader `verifying`→`approve` 全程在 Web kanban 正確反映；② Worker 嘗試寫 task 狀態被拒（唯讀）；③ `list_members` 回傳正確 roster；④ store 單元測試綠（狀態機合法/非法轉移、單寫者）。
- **依賴**：M0。

### M2 — 訊息協作（drain 階段 A）
- **目標**：agent 之間能像收發信一樣協作；idle agent 收到信會醒來處理。
- **交付**：`send_message` 工具（每 agent 一份、烤入 sender）；mailbox `chan`（傳 msg-uuid）；訊息落 SQLite `messages`（標 sender/recipient/read_at）；Supervisor「有信就 Run」（drain A：回查 uuid→注入 prompt→標已讀）；Web 顯示信件往來。
- **Gate**：① A 寄信給 idle 的 B，B 自動醒來、prompt 含「來自 A 的信」、處理後該信 `read_at` 被標；② `to:"all"` 廣播到位；③ bus 單元測試綠（投遞/回查/標已讀/未讀重載）。
- **依賴**：M1。

### M3 — manifest + 計時器喚醒 + 動態成員 + suspend/resume + 重啟接續
- **目標**：把 swarm 做成「可宣告、可常駐、可擴編、可中止、可重啟接續」的完整 runtime。
- **交付**：`evva-swarm.yml` 完整解析；**timer 喚醒**（`profile.yml` 的 `schedule` → scheduler tick → Run，§5.5 設計）；`evva swarm add <name>` / Web 動態加入 + 冷藏（freeze）；suspend（cancel run ctx）/ resume；重啟接續（未讀訊息 reload + `.vero/sessions/` + `Agent.ResumeSession`）。
- **Gate**：① 宣告 `schedule: "*/1 * * * *"` 的 agent 每分鐘被喚醒一次（idle 時不燒 token，可由 log/usage 佐證）；② 動態加入的成員即刻可被 `list_members`/`send_message`/指派；③ freeze 後不再被派任務、可解凍；④ suspend 立即中止在飛 run、resume 能續；⑤ `kill -9` service 後重啟，未讀訊息重新入列、各 agent transcript 能 `ResumeSession` 接續。
- **依賴**：M2。

### M4 — 即時收信（drain 階段 B；唯一動 evva runtime 的里程碑）
- **目標**：忙碌中的 agent 也能即時把新信 fold 進當前推理，不必等 Run 結束。
- **交付**：在 **`pkg/agent` 開公開可插拔的 inbox-drainer seam**（推廣現有 `KindDrainBackgroundTask`，`pkg/event/event.go:140`），loop 每輪 iteration boundary 呼叫它、fold 回傳的 synthetic message；swarm 接上這個 seam。附測試 + `docs/extending.md` 文件 + `CHANGELOG` + 版本 bump（minor、additive）。
- **Gate**：① 對 busy 的 agent 寄「緊急停手」信，它在**當前 run 的下一輪**就看到並反應（不必等 run 結束）；② seam 是公開 API、有 downstream 編譯測試；③ 既有單 agent 行為不回歸（nil drainer noop）。
- **依賴**：M3。**這是 Phase 1 唯一需要改 evva 既有 runtime 的地方。**

---

## 5. Phase 1 完成定義（DoD）— 什麼叫「swarm 做好了」

**以下全綠，才視為 Phase 1 完成、才開 Phase 2：**

- [ ] 從 `evva-swarm.yml` 啟一個 **≥3 agent** 的 swarm；`evva service` 的 Web 正確顯示 roster（membership + run-status）。
- [ ] Leader push 任務、Worker 唯讀 + 回報、**5 狀態機**跑通；Web kanban 正確反映每次轉移。
- [ ] `send_message` 雙向、落 SQL、drain A 注入、標已讀；`to:"all"` 廣播可用。
- [ ] **timer 喚醒**可用（宣告 `schedule` 的 agent 會定時被 Run）。
- [ ] 動態加入 + 冷藏（freeze，不刪除）可用。
- [ ] suspend / resume + **重啟接續**（kill 後 reload 未讀 + `ResumeSession` 能續）。
- [ ] **drain B**（M4 inbox-drainer 公開 seam）落地，busy agent 即時收信。
- [ ] **全程零 `internal/agent` import**（dep-check 綠，§1.1 multi-agent oracle）。
- [ ] 測試綠：`store` / `bus` / `scheduler` 單元測試 + **一條 e2e**（起 swarm→指派→協作→重啟→接續）。
- [ ] 安全性底線：service 預設綁 `127.0.0.1` + session token；危險工具走 permission（§8.3）。

---

## 6. Phase 2 里程碑 + 驗證準則

> 細節見 [`prd-phase2-trader-team.md`](prd-phase2-trader-team.md)。這裡只列里程碑與「實用」的可測定義。

### 驗證準則 —「swarm 實用」被證明 = 以下皆達成（全程 **paper / testnet**）
- 五 agent（friday/trader/analyst/risk-monitor/reviewer）能**連續自主運作 ≥ 3 個交易日**而不需人工重啟/解卡。
- 分工迴路確實運作：analyst 報告 → trader 決策下單（紙上）→ risk-monitor 定時巡檢並在違規時告警 → reviewer 每日復盤交給 friday → friday 能據此要求 trader 調整。
- **friday kill switch 有效**：一聲令下，所有 agent 在飛 run 被中止、swarm 暫停。
- **重啟接續且對帳一致**：service 重啟後，五 agent 接續，且本地 ledger 與（testnet）交易所實際持倉 reconcile 無偏差。
- 成本可觀測：能報出每日 token / 次數，且 idle agent 不燒 token（timer/event 驅動生效）。

### 里程碑
- **P2-M0 — paper skeleton**：friday + trader 兩角，紙上行情 → 紙上下單 → 記 ledger。跑通最小決策迴路。
- **P2-M1 — full team**：補齊 analyst / risk-monitor / reviewer，定時巡檢 + 每日復盤 + 報告迴路成形。
- **P2-M2 — endurance run**：連續多日 paper run，量測上面驗證準則；產出一份「swarm 實用性評估」報告（含痛點回填 Phase 1 的清單）。

---

## 7. 需求②（inbox-drainer）排程

- **只在 M4 動 evva runtime**；其餘里程碑全在 `internal/swarm` + `web/` + 新 custom tools。
- 做成 **`pkg/agent` 公開 seam**（不是私接），附：單元測試、downstream 編譯測試、`docs/extending.md` 章節、`CHANGELOG`、版本 bump（minor）。
- 在此之前（M0–M3），busy agent 的新信走 drain 階段 A（下一次 Run 才看到）——**功能完整、只是即時性稍弱**，不阻擋 Phase 1 其他進度。

---

## 8. 風險與緩解（跨階段）

| 風險 | 緩解 |
| --- | --- |
| 連續運行 **token 成本** 高 | per-agent model tier（`profile.yml`）；event/timer 驅動使 idle 不燒 token；可調 `schedule` 頻率 |
| 長壽 agent **context 膨脹** | 靠 evva `/compact` + session 快照；e2e 要涵蓋一次 compaction |
| **真錢安全**（Phase 2） | paper/testnet first；下單工具走 permission gate；硬限額做成交易所 stop order / code circuit breaker；friday kill switch 確定性；重啟對帳以交易所為準 |
| LLM **非確定性** | Phase 2 定位為策略研究平台，不承諾獲利；驗證準則只看「協作機制是否運作」 |
| 單 process **crash 連坐** | v1 接受（模型 A）；演進到模型 C（每 swarm 一 process + web gateway）做 crash 隔離，bus 仍不出 process |
| swarm 設計缺漏在 Phase 2 才暴露 | Phase 2 痛點**回填 Phase 1**（補 DoD），不在 Phase 2 打補丁 |

---

## 9. 不在這份 roadmap 內（out of scope）

- **process 模型 C**（每 swarm 一 process + web gateway）——演進路，非 v1（§4.2 設計）。
- **跨機 / 多 process bridge**（socket/JWT/跨機轉發）——維持 evva out-of-scope。
- **跨 space 通訊 / 資源配額 / per-space 權限**等**進階** multi-space 管理——留後。（基本的「一 process 多隔離 space（子集團）」是 **M0 起的核心能力**，不在此 out-of-scope 列。）
- **bundled 交易所整合**——交易所 client 由 Phase 2 自寫 custom tool，不做成 evva 內建。
- **Leader 自主 `hire_member`**——擴編 v1 只給 User，未來可能開放。
- **真錢交易**——Phase 2 一律 paper/testnet；上真錢是另一個獨立決策。
