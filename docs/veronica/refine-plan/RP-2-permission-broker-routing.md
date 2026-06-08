# RP-2 — Permission broker 卡死：修審批路由、並發審批、斷線重放

> 狀態：**草案 / Draft（待拍板）** ｜ 日期：2026-06-05 ｜ 嚴重度：🔴 高（**deterministic**）
> 對應問題：smoke test #2 —— 「agents permission broker 會卡住，UI 顯示不出詢問框，
> agent 一直卡在 busy 不動」。
> Design：[`../veronica-design-v1.md`](../veronica-design-v1.md) §8.1、§8.3 ｜ 關聯：[RP-3](RP-3-agent-run-phase-states.md)

---

## 1. TL;DR

審批卡死的**頭號根因是一個 deterministic 路由 bug，不是 race**：

- agent 的 event 帶的 `AgentID` 是 `agent.New` 生成的**隨機 UUID**
  （`internal/agent/agent.go:242` `ID := common.GenUUID()`、`:480` `AgentID()` 回傳 `a.ID`），
  與成員名（`lead`/`qa`…）**不同**（`WithName` 設的是另一個欄位 `a.Name`，`options.go:40`）。
- 前端審批框抓的是 `ev.AgentID`（UUID，`events.js:approvalOf`），回傳時就把這個 UUID 當
  `agent` 送回（`ApprovalOverlay.vue:21` → `SpaceView.vue:onPermission` → `ws.js`）。
- **後端卻用成員名查 controller**：`service.RespondPermission`（`service.go:633`）→
  `controller(id, agent)`（`:684`）→ `Roster.Controller(agent)`（`roster.go:89`，以
  `entries[name]` 命中）。UUID 永遠不等於 name → 回 `"unknown space/agent"` →
  `dispatchInbound` 還把這個 error **丟掉**（`api.go:324` `_ = b.RespondPermission(...)`）
  → broker 永遠收不到回應 → tool goroutine 卡在 `Broker.Request` → **agent 永遠 busy**。

**也就是說：只要成員名 ≠ AgentID（永遠成立），每一次 web 審批都必然 hang。**
測試之所以全綠，是因為它們用**成員名**呼叫（`api_test.go:257` 送 `"agent":"leader"`、
`service_integration_test.go:206` 傳 `"leader"`），而**真實 UI 送的是 UUID** —— 整合測試
測到的路徑與 UI 走的路徑不同。

修整分五層：**§1 路由（deterministic，先修）→ §2 並發審批不互蓋 → §3 斷線/未連線重放
→ §4 WAITING_APPROVAL 觀測（接 RP-3）→ §5 pump head-of-line 防凍結**。

---

## 2. 根因清單（含證據）

| # | 根因 | 證據 | 類型 |
| --- | --- | --- | --- |
| **R1** | 回應路徑用 UUID 查「以名字為 key」的 roster → 必失敗、error 被吞 | `service.go:633/684` + `roster.go:89` + `api.go:324` | 🔴 deterministic |
| **R2** | 前端 `approval`/`question` 是**單槽 ref**，新審批覆蓋舊的 | `SpaceView.vue:23-24`、`:63-71` `onEvent` | 🟠 並發 |
| **R3** | 審批 event 只 live 扇出，無 REST 快照、無 reconnect 重放 | `ws.js` 重連、`webapi` 無 pending 端點 | 🟠 韌性 |
| **R4** | 卡審批時 roster 仍只顯示 `busy`，無「卡在哪」線索、無 timeout | `roster.go:24-30`、`scheduler.go:121` | 🟡 觀測 |
| **R5** | 單 goroutine pump + WS send 無 write deadline → 卡住的瀏覽器連線會凍住整個 space | `service.go:440` pump、`hub.go:77` `Message.Send` 無 deadline | 🟠 韌性 |

> R2 會獨立造成卡死：swarm 裡多個 worker 同時要審批很常見，第二個 `approval_needed`
> 直接覆蓋第一個 —— 第一個成員的 tool 仍阻塞，但 UI 已無任何入口回答它。

---

## 3. 修整方向

### 3.1（R1，先修）審批/回應路由：以 event 帶的同一身分解析

回應路徑必須能解析「event 帶回來的那個身分」。event 全鏈都以 `AgentID` 為 key
（WS 扇出、console demux 都是），所以**讓 `AgentID` 成為 roster 的一等查詢鍵**最一致：

- `Roster` 增加 `byAgentID map[string]*rosterEntry`（與 `entries`（by name）並存、同步維護）。
- `service.controller(id, ref)` 改為 **ref 先當 AgentID 查、查不到再當 name 查**，
  讓 `RespondPermission` / `RespondQuestion` / `Run` / `Suspend` … 任何一個都吃兩種 ref。
- `dispatchInbound` **不要再吞 error**（`api.go:324`）—— 至少 log，最好把失敗沿 WS 回送，
  讓前端知道「這個審批沒送達」而不是無聲卡死。

> 替代方案：在 `wireEvent` publish 時反查補上成員 `name`，前端改送 name。可行但要在邊界
> 做轉換；既然全鏈已是 AgentID，把 AgentID 變成可查鍵更省事、面積更小。

**最關鍵的測試補強**：加一個**走 UUID 路徑**的端到端回歸測試（取 `ctl.AgentID()` 當
`agent` 參數呼叫 `RespondPermission`，斷言 broker 真的被 unblock）—— 正是現有測試漏掉的路徑。

### 3.2（R2）並發審批佇列，不再互蓋

前端把 `approval`/`question` 從**單槽**改成**佇列**（以 `(agentId, requestId)` 為 key 的
map / list）：

- `onEvent` 收到 gate event → **push 進佇列**，不覆蓋。
- overlay 一次顯示一筆（或堆疊顯示），回答後**只移除那一筆**，自動顯示下一筆。
- 顯示待辦數 badge（「3 件待審批」），operator 知道還有幾個成員在等。

### 3.3（R3）pending 審批可查、可重放

broker 本來就持有 pending requests；把它**暴露出來**即可讓審批「在 session 內 durable」：

- broker 加 `Pending()`；`Controller` 加 `PendingApprovals()` / `PendingQuestions()`
  （小幅 additive surface）。
- 新增 `GET /api/swarm/:id/pending`（或併進 roster 快照）：列出每個成員未決的 approval/question。
- 前端在 **WS connect / reconnect 時 hydrate** 這份 pending —— 於是「在進 space 前就觸發的
  審批」「reconnect 空窗期觸發的審批」都會在連上後補顯示，不再無聲卡死。

> 範圍：止於 *session 內*（process 活著就找得回）。跨 process 重啟不持久化 —— 重啟後 run
> 會重跑、重新 emit，本就會重新要求審批。

### 3.4（R4）WAITING_APPROVAL 觀測（細節見 [RP-3](RP-3-agent-run-phase-states.md)）

卡審批時 roster 仍是泛泛 `busy`，operator 看不出卡在哪。RP-3 會引入由 event 推導的細
狀態；其中 `WAITING_APPROVAL` / `WAITING_INPUT`（由 `KindApprovalNeeded` /
`KindQuestionNeeded` 觸發、回答後下一個 sub-phase event 清除）**正是診斷本問題的關鍵**：
operator 一眼看到「`qa` 卡在 WAITING_APPROVAL: bash 2:41」而不是一片 busy。

- 可選的 broker timeout / 「拒絕該成員所有待決審批」操作：避免被遺忘的審批無限 hang。
  傾向**先給觀測 + 手動操作**，timeout 設為 opt-in（無聲 auto-deny 容易讓人意外）。

### 3.5（R5）pump 不被單一壞連線凍住

per-space pump 是單 goroutine，`hub.Publish` → `conn.send` 是同步
`websocket.Message.Send`、**無 write deadline**（`hub.go:77`）。一條 half-open 的瀏覽器
連線會卡住 send → 卡住 pump → `sp.out`（1024）填滿後**每個成員的 `spaceSink.Emit` 全部
阻塞 → 整個 space 凍結**（諷刺的是連「解鎖用的審批 event」都送不出去）。

- WS send 設 **write deadline**，逾時就**踢掉該連線**（瀏覽器會自動重連）而非阻塞 pump。
- 可給每連線一個 outbound buffer + 「慢消費者就丟棄/斷線」策略。屬 defense-in-depth，
  但正對應「整個 space 一起卡住」的觀察。

---

## 4. Scope

**In：**
- `roster.go`：`byAgentID` 索引 + 維護（add/構造/AddMember 同步）。
- `service.go`：`controller(id, ref)` 接受 AgentID 或 name；相關指令受惠。
- `webapi/api.go`：`dispatchInbound` 不吞 error；新增 `GET .../pending`。
- `pkg/permission` broker + `pkg/ui.Controller`：`Pending*()` 唯讀存取（additive）。
- `web/`：審批佇列、reconnect hydrate pending、待辦 badge。
- `webapi/hub.go`：WS write deadline + 慢連線淘汰。

**Out：**
- 細狀態本身（RUNNING/EXECUTING/…）→ [RP-3](RP-3-agent-run-phase-states.md)。
- 跨 process 持久化審批（重啟自會重 emit）。
- 認證/授權強化（§8.3 既有 token 機制不在本計畫範圍）。

---

## 5. Acceptance Criteria

1. **R1 回歸（核心）**：以 `ctl.AgentID()`（UUID）為 `agent` 參數從 web 路徑送
   approve/deny，broker **確實被 unblock**、agent 由 busy 回到 READY。（明確覆蓋 UI 真走的
   UUID 路徑，而非僅 name 路徑。）
2. **並發審批**：兩個成員同時要審批，兩個 overlay 都可回答、兩個都解鎖，無互蓋。
3. **重放**：成員在前端未連線/重連空窗期觸發審批，連上後經 pending 快照補顯示並可回答。
4. **觀測**：被審批阻塞的成員在 roster 顯示 `waiting-approval` + 工具名（依賴 RP-3）。
5. **韌性**：一條卡死的 WS 連線**不會**讓同 space 其他成員停擺（pump 不被凍住）。
6. `dispatchInbound` 的失敗會被 log / 回報，不再無聲吞掉。

---

## 6. Definition of Done

- [ ] Roster 以 AgentID 可查；`controller(id, ref)` 雙鍵解析；**UUID 路徑回歸測試**綠燈。
- [ ] 前端審批/問題改佇列、不互蓋；待辦 badge。
- [ ] broker `Pending*()` + `/api/swarm/:id/pending` + reconnect hydrate。
- [ ] WS send write deadline + 慢連線淘汰；pump 不再可被單連線凍住。
- [ ] `dispatchInbound` 不吞 error；`go test -race ./internal/swarm/...` clean。

---

## 7. 風險與取捨

- **雙鍵解析的歧義**：UUID 與 name 命名空間不重疊（UUID 是隨機 hex、name 是人取的），
  「先 AgentID 再 name」不會誤命中。仍建議 name 唯一性檢查維持（roster 已做，`roster.go:73`）。
- **broker `Pending*()` 公開面**：屬 Experimental tier（`pkg/ui`/`pkg/permission` 尚在
  演進），additive 唯讀，風險低；需與 RP-3 對 Controller 的擴充一起審。
- **WS write deadline 值**：太短會誤踢正常慢網路、太長失去防凍效果；建議可設定，預設
  數秒級，搭配前端自動重連。
- **先後**：§3.1 可獨立、最小、立即解掉 deterministic hang，建議**先單獨出一個小 PR**
  讓 demo 能跑；§3.2–§3.5 隨後。
