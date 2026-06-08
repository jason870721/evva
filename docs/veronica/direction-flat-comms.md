# 方向規劃：扁平化溝通 — Operator ↔ 任一成員 直連對話 + 全員可觀測

> 狀態：**草案 / Draft（方向提案，未進入實作）** ｜ 日期：2026-06-04
> 關聯：Veronica swarm（Phase 1 已完成 — 見 [`roadmap.md`](roadmap.md) §5 DoD）
> 上層設計：[`veronica-design-v1.md`](veronica-design-v1.md) ｜ 使用者指南：[`user-guide-zh.md`](user-guide-zh.md)

---

## 1. TL;DR — 這份文件在規劃什麼

把 swarm web 從「**只能跟 leader 對話、只能旁觀 worker**」升級為「**扁平化管理**」：

- **看得到細節**：在 web 中看到**每一個 worker** 的工作細節與工具調用（text /
  thinking / tool-call / tool-result），體驗就跟現在的 Leader Chat 一樣。
- **講得到話**：User 可以**直接對任一成員（含基層 worker）發訊息**，不必透過
  leader 轉達 —— 扁平化、操作者可直達基層 agent。
- **不破壞工作流**：上述直連溝通**不得干擾** swarm 既有的協作 —— 任務帳本、agent
  之間的訊息往返、supervisor 的喚醒/排程照常運作。

**核心洞見**：swarm 早就有「**訊息總線 + drain A/B**」這層架構。只要把 **User 視為
總線上的一個一等 sender**，「操作者直連任一成員」就變成一個**附加、非侵入**的能力
—— 後端改動極小，主要工作在前端 UI。本文把這條路徑寫清楚。

---

## 2. 現況盤點 — 我們已經有什麼（不要重造）

| 已存在 | 說明 | 出處 |
| --- | --- | --- |
| **每個成員的事件已扇出到 web** | WS 以 `(spaceID, AgentID)` 路由，space 訂閱者會收到**全部成員**的 `event.Event`（text/tool/thinking…） | `internal/swarm/webapi` Hub + `internal/swarm/service` pump |
| **`Run` 本來就接受任意成員** | `Backend.Run(spaceID, agent, prompt)` 並非 leader 專用，只是前端 `LeaderChat.vue` 寫死了 leader | `internal/swarm/webapi/api.go` |
| **訊息總線、sender 為自由字串** | `Bus.Send(store.Message{Sender, Recipient, Body…})`；`to:"all"` 廣播給 active 成員 | `internal/swarm/bus` |
| **drain A（idle 喚醒）** | 成員 idle 時收到信會被 supervisor 喚醒、讀信、處理、標已讀 | `internal/swarm` scheduler/supervisor |
| **drain B（busy 中途折入）** | 成員忙碌中收到信，會在**當前 run 的下一輪**折入處理（SPRD-1-12 公開 seam） | `pkg/agent.WithInboxDrainer` + `internal/swarm/drain.go` |
| **唯讀 per-member transcript** | `/api/agents/:name/transcript` 已能回放任一成員歷史 | `internal/swarm/webapi` |
| **持久化 / 標已讀 / 重啟接續** | 任何總線訊息都是 durable row，自動納入重啟續跑 | `internal/swarm/store` + `resume.go` |

**缺口（本提案要補的）**：

1. **User → 成員** 的訊息入口（後端一個薄端點）。
2. **Per-member Console**：把混在一起的事件流，聚焦成「每個成員一個對話/檢視面板」，
   含工具調用細節 + 輸入框。
3.（選配）**成員 → User** 的回信通道（讓基層 agent 能直接回報操作者）。

> 結論：**80% 是前端，後端只新增一個「User 發訊息」端點**。非侵入性幾乎是**免費**
> 的 —— 因為 User 訊息走的是和 agent 間訊息**完全相同**的總線 + drain 路徑。

---

## 3. 設計

### 3.1 核心機制：把 User 變成總線上的一等 sender（mail-mode）

新增一個 webapi 端點，把操作者的話**當成一封信**投進目標成員的信箱：

```
POST /api/agents/{name}/message?space=<id>     body: { "body": "...", "subject": "..."? }
   → service: space.Bus.Send(store.Message{ Sender: "user", Recipient: name, Body, Subject })
廣播：name = "all"  → Bus 廣播給所有 active 成員
```

之後**什麼都不用做**，既有機制接手：

- 成員 **idle** → supervisor 的 **drain A** 喚醒它，prompt 內含「來自 user 的訊息」，
  處理完標已讀。
- 成員 **busy** → **drain B** 在當前 run 的下一輪把訊息折入，緊急指令（「先停下」）
  立刻被看到。
- 與 agent 間訊息**同一條路徑、同一序列化（每成員 run 串行）、同一持久化 / 標已讀 /
  重啟接續**。
- **不碰任務帳本**：帳本仍然 **leader-only 寫者**；User 訊息只是訊息，不是任務狀態
  變更。

> 這就是非侵入性的來源：User 不是一個新的控制面，而是「總線上多了一位寄件人」。
> 系統既有的不變量（每成員 run 串行、DB 為真相、帳本單寫者）原封不動。

### 3.2 為什麼用 mail-mode，而不是直接 `Run`（drive-mode）

- 直接對 **busy** 成員呼叫 `Run` 會和 supervisor 的喚醒**競爭**（同一成員的第二個並行
  Run → 衝突 / 「run in progress」）。
- mail-mode 透過總線**天然序列化**，零競爭，且自動享有 drain B 的「中途折入」。
- **統一**：連 leader 的開場（kickoff）都可以走 mail-mode —— leader idle 時 drain A
  喚醒它，照常編排。於是「跟 leader 對話」和「跟 worker 對話」變成**同一個動作**。
- `Run`（drive-mode）保留為**進階動作**（「立刻逼一輪」）；若日後提供，busy 時改走
  `Controller.EnqueueUserPrompt`（在迭代邊界排入，而非開第二個 Run），同樣非侵入。

### 3.3 Per-member Console（可觀測 + 對話）

前端把「已經收到的全員事件流」**依 `AgentID` 拆開**呈現：

- **Roster 點擊成員 → Member Console**：
  - 以該成員的 `AgentID` 過濾事件流，渲染它的 turn（沿用既有 `reduceChat`），
    **完整顯示工具調用細節**：工具名、輸入摘要、狀態（running/done/error）、結果。
  - 底部輸入框「**訊息給 \<成員\>**」→ 呼叫 §3.1 端點（mail-mode）。
  - 開啟時用既有 `/api/agents/:name/transcript` **回填歷史**，再接 WS 即時流。
- **Leader Chat 收斂**為「leader 的 Member Console」——同一個元件，去除特例。
- 支援**多開 / 分頁切換**不同成員，像同時跟多位 agent 對話（一條 WS 連線，client 端
  依 `AgentID` demux 即可；Hub 也支援 `?agent=` 聚焦訂閱，視效能再選）。

### 3.4（選配，M2）成員 → User 回信

讓基層 agent 能**主動回報操作者**，完成雙向扁平溝通：

- 把 `"user"` 設為合法 recipient；成員 `send_message {to:"user", body:…}` 落入一個
  **User 收件匣**。
- web 顯示「**來自 \<成員\> 的訊息 / 通知**」（一個 DM / 通知串）。
- `list_members` 可把 `user` 列為可定址對象，讓 agent 知道「可以直接回報操作者」。

> M1 只做**正向**（User→成員）+ 可觀測；反向（成員→User DM）放 M2，避免一次擴張過大。

### 3.5 「不干擾工作流」的逐條保證

| 顧慮 | 為何不被干擾 |
| --- | --- |
| 並行 run 衝突 | User 訊息走總線 → 每成員 run **串行**；不會和任務驅動的 run 並行 |
| 中止在飛工作 | drain A/B 是**折入**（fold），不是中止；agent 間在飛的協作照跑 |
| 任務帳本被亂改 | User 訊息**不寫帳本**；leader-only 寫者規則不變 |
| 重啟掉訊息 | User 訊息是 durable row，重啟自動 reload + 標已讀（沿用 1-11） |
| 觀測影響行為 | 事件扇出是**唯讀**，不改變任何 agent 的執行 |
| 安全面 | 沿用 `127.0.0.1` + session token；一般訊息非危險工具，無需額外 gate；成員據訊息**調用**的危險工具仍走既有 permission 審批 |

---

## 4. 工作分解

### 後端（小）
- `internal/swarm/webapi`：新增 `Backend.SendUserMessage(spaceID, to, subject, body)` +
  `POST /api/agents/{name}/message`（與 `all` 廣播）。
- `internal/swarm/service`：實作之 —— 解析 space → `space.Bus.Send(Sender:"user", …)`。
- （M2）User 收件匣：`"user"` recipient 落點 + `GET /api/user/messages` + `list_members`
  顯示 user。

### 前端（主）
- `web/src/components/MemberConsole.vue`（由 `LeaderChat.vue` 演化）：props 帶
  `member`，turn 以 `agentId` 過濾，工具調用細節渲染，輸入框 → `/message` 端點。
- `SpaceView.vue`：Roster 點擊聚焦、管理多個 console / 分頁；Leader Chat 收斂為
  leader console。
- `api.js`：`sendMessage(space, to, body)`；（M2）`userMessages(space)`。
- （M2）`UserInbox.vue`：成員→User 的 DM / 通知串。

### 測試
- 端點注入 → idle 成員被喚醒、folds「來自 user 的訊息」、標已讀（service e2e）。
- busy 成員「緊急」訊息**當前 run 下一輪**即見（drain B e2e，沿用 1-12 模式）。
- 全程 leader↔worker 任務迴路**不中斷**（並行 e2e：跑任務迴路的同時對 worker 發訊息，
  斷言任務仍走到 completed）。
- 前端：多成員 console 依 `agentId` 過濾的 reducer 單元測試（`node --test`）。

### 文件
- 更新 [`user-guide-zh.md`](user-guide-zh.md) / [`user-guide-en.md`](user-guide-en.md)
  新增「扁平化溝通：直接對任一成員發訊息」章節。

---

## 5. 里程碑 / Gate

- **M1 — 正向 + 可觀測（核心）** ✅ **已交付（2026-06-04）**
  - Gate：① web 可對**任一成員**發訊息，該成員（idle 喚醒 / busy 折入）確實收到並
    處理、標已讀；② Member Console 即時顯示該成員的 turn + **工具調用細節**；③ 並行
    e2e 證明 leader↔worker 任務迴路**不受影響**。
  - 實作：`Backend.SendUserMessage` + `POST /api/agents/{name}/message`（sender
    `"user"`，`all` 廣播）走既有 `Bus.Send`；`MemberInfo.AgentID` 讓前端 demux 事件
    流；前端 `MemberConsole.vue`（取代 `LeaderChat`，依 `AgentID` 過濾、含工具調用
    細節、輸入即 mail-mode）+ Roster 點擊聚焦。測試：webapi 路由、service「user 訊息
    喚醒 idle 成員→drain→標已讀、不動帳本」、前端 `consoleTurns` demux 單元測試。
- **M2 — 雙向**
  - Gate：① 成員 `send_message {to:"user"}` → web User 收件匣可見；② `list_members`
    顯示 user 為可定址對象。

---

## 6. 風險與緩解

| 風險 | 緩解 |
| --- | --- |
| **語意衝突**：User 指令與 leader 規劃矛盾（例如叫 worker 別做 leader 指派的任務） | 系統層仍一致（帳本不變、無資料競爭）；協調責任在操作者。UI 在 Member Console 顯示該成員「當前任務 #N」提醒情境，避免誤操作 |
| **廣播噪音 / 濫用** | `to:"all"` 前 UI 二次確認；必要時加速率提示 |
| **觀測資訊過載**（多成員事件混雜） | 預設 per-member 分頁；提供「全部活動」與「單一成員」兩種視圖 |
| **drive-mode 競爭**（若日後開放直接 Run） | busy 時改走 `EnqueueUserPrompt`（迭代邊界排入），不開第二個 Run |
| **權限**：User 透過訊息誘導危險操作 | 成員實際調用的危險工具仍走 permission 審批（default 模式）；訊息本身非工具，不需 gate |

---

## 7. 不在範圍（本方向）

- 跨機 / 多進程的操作者通道（維持 `127.0.0.1` 單進程；見設計 §4.2 的演進路）。
- 變更「**帳本 leader-only 寫者**」規則 —— User 不直接寫任務帳本；要動帳本仍透過
  leader（或未來另開的專屬 operator 工具，另案）。
- 把 User 做成一個會「跑 loop」的 agent —— User 是總線上的**寄件人 / 收件人**，不是
  一個被 supervisor 排程的成員。

---

## 8. 驗收準則（對應 §5 Gate）

1. 對 **idle** worker 發訊息 → 它被喚醒、prompt 含「來自 user 的訊息」、處理後該訊息
   `read_at` 被標；web 即時看到它的 turn 與工具調用。
2. 對 **busy** worker 發「緊急」訊息 → 它在**當前 run 的下一輪**就看到（drain B）。
3. 在 1–2 進行的同時，leader↔worker 的任務迴路（assign→回報→verify→complete）**全程
   不中斷**（並行 e2e 斷言任務仍走到 completed）。
4. Member Console 對每個成員都能呈現「像跟 leader 對話一樣」的細節（text / thinking /
   tool-call 名稱+輸入+結果）。
5.（M2）worker `send_message {to:"user"}` → web 的 User 收件匣可見；`list_members` 含
   user。

---

## 9. 一句話總結

swarm 的「總線 + drain」架構讓**扁平化溝通**幾乎是免費的：把 **User 當成總線上的一位
寄件人**，操作者就能像跟 leader 一樣，直接看見並指揮任一基層 agent —— 而整個 swarm 的
協作工作流，因為走的是同一條既有、已序列化、已持久化的路徑，**原封不動**。
