# RP-3 — Agent run-phase 細狀態：從 event stream 推導，對齊 evva TUI

> 狀態：**草案 / Draft（待拍板）** ｜ 日期：2026-06-05 ｜ 嚴重度：🟡 中（觀測面，但放大了 #1/#2）
> 對應問題：smoke test #3 —— 「agent 的狀態不應只有 busy，應該學 evva TUI 用 RUNNING /
> EXECUTING / READY 等狀態，這樣卡住時 user 能知道卡在哪個點」。
> Design：[`../veronica-design-v1.md`](../veronica-design-v1.md) §5.1、§5.2 ｜ 關聯：[RP-2](RP-2-permission-broker-routing.md)

---

## 1. TL;DR

Roster 的 run 狀態只有粗粒度 `idle | busy | suspended`（`roster.go:24-30`），而且是由
supervisor 在 run 邊界**手動設定**（`scheduler.go:121` `setRun(RunBusy)` / `:132`
`setRun(RunIdle)`）。所以一個成員整段 run 都只顯示「busy」—— 卡住時 operator 看不出是
**在 thinking、在跑一個很久的 bash、卡在審批、還是在 draining**。

但 evva 自己的 TUI **早就**有一套從 event stream 推導的細狀態
（`pkg/ui/bubbletea/components/status/state.go`）：running / thinking / texting /
executing / draining / compacting / paused / error / ready。swarm 的 web 其實**收得到
一模一樣的 event**（WS 全扇出），卻把粒度丟掉了。

**修整方向**：把 run 維度從「手動設的粗狀態」改成「**從每個成員的 event stream 推導**的細
狀態」，移植 TUI 的 `State.Apply`，再補上 swarm 最需要的 **`WAITING_APPROVAL` /
`WAITING_INPUT`**（這正是讓 [RP-2](RP-2-permission-broker-routing.md) 的卡死「看得見」的
那一塊）。狀態寫回 Roster（單一真相），於是 **web 與 `list_members` 工具同時受惠**。

---

## 2. 現況與缺陷

### 2.1 粗狀態 + 手動設定的三個問題

| 缺陷 | 說明 | 證據 |
| --- | --- | --- |
| **粒度太粗** | 整段 run 都是 `busy`，看不出 thinking / executing / draining | `roster.go:24-30` |
| **手動設定、與路徑綁定** | 只有 supervisor `serve` 會 `setRun`；**web 直驅的 `Run` 端點完全不更新 roster** → 該路徑下成員實際在跑卻顯示 `idle` | `scheduler.go:121/132` vs `service.go:592 Run()`（無 setRun） |
| **缺「卡住」狀態** | 卡在 permission/question broker 時仍是 `busy`，最該有的診斷狀態反而沒有 | —— |

> 路徑相依的證據：`service.Run`（`service.go:592`）直接 `go ctl.Run(...)`，不碰 roster。
> 目前 live UI 走 flat-comms 寄信（會經 `serve` → 有 setRun）所以被掩蓋，但 `run` 端點仍
> 在線、不一致是潛在的。**改用 event 推導後，所有入口（serve / web Run / timer）都自動正確**
> —— 因為它們全都會 emit event。

### 2.2 evva TUI 已有的參考實作（直接借用）

`pkg/ui/bubbletea/components/status/state.go:163` `Apply(e event.Event)` 把 event 映射成
sub-phase：

```
KindRunStart/Resume/TurnStart/TurnEnd → Running
KindThinking(/Chunk)                  → Thinking
KindText(/Chunk)                      → Texting
KindToolUseStart                      → Executing
KindToolUseResult                     → Running
KindDrainingInfo                      → Draining
KindCompacting / End                  → Compacting / Running
KindRunEnd / KindIdle                 → Ready(Idle)
KindRunCancelled                      → Idle
KindIterLimit                         → Paused（sticky）
KindError                             → Error（sticky）
```

swarm 完全可以移植這套，並擴充兩個 event 對應 broker 阻塞（TUI 不需要，因為它用 modal
overlay；swarm 多成員則需要 per-agent 的阻塞指示）。

---

## 3. 修整方向：event-derived run phase 作為單一真相

### 3.1 新的 `RunPhase`（兩維模型不變，只豐富 run 維度）

維持 design §5.1 的兩個正交維度，只**豐富 run 維度**並**改成 event 推導**：

- **membership（第一維，不變）**：`active | frozen`。
- **run phase（第二維，豐富化）**：
  | Phase | 來源 event | 備註 |
  | --- | --- | --- |
  | `READY` | RunEnd / Idle | 原 `idle`；不燒 token |
  | `RUNNING` | RunStart/Resume/TurnStart/End、ToolUseResult | loop 活著的泛狀態 |
  | `THINKING` | Thinking(/Chunk) | 模型推理中 |
  | `EXECUTING` | ToolUseStart | **帶工具名**（`executing:bash`） |
  | `WAITING_APPROVAL` | **KindApprovalNeeded** | **新**；帶工具名；回答後下個 sub-phase event 清除 |
  | `WAITING_INPUT` | **KindQuestionNeeded** | **新**；AskUserQuestion 阻塞 |
  | `DRAINING` / `COMPACTING` | DrainingInfo / Compacting | 雜務 |
  | `PAUSED` / `ERROR` | IterLimit / Error | sticky（終態） |
  | `CANCELLED` | RunCancelled | |

  - `TEXTING`（emitting 回應文字）對 swarm 看板可能太吵，可折進 `RUNNING`（實作時決定，
    傾向 board 上 texting≈running，但 per-agent console 可細分）。
  - **`SUSPENDED`** 維持由 supervisor 命令權威（不是 event 推導）—— 見 §3.3 的優先序。

### 3.2 wiring：在 space teed 每個成員的 event，推導後寫回 Roster

event 本來就走 `spaceSink` → `sp.out`（`space.go:249`）。做法（推薦）：

- 在 space 把每個成員的 event **tee 成兩支**：一支照舊進 `sp.out`（web/log，不動）；
  一支進 **per-member phase deriver**（直接移植 `status.State.Apply` + §3.1 兩個 waiting
  phase），由它更新 `rosterEntry.run`。
- Roster 維持後端權威 → **web 的 `/api/swarm/:id` 與 agent 的 `list_members` 工具同時**
  看到細狀態（agent 之間互看也從「busy」升級成「qa 在 executing」）。

> 替代方案：只在前端推導（像 TUI 那樣消費 WS）。後端改動小，但 `list_members`、REST 快照
> 仍是粗的，且 reconnect 後 phase 會丟。**不採** —— 會失去 design 看重的「Roster 單一真相」。

### 3.3 與 supervisor 生命週期的優先序（乾淨組合）

顯示狀態的合成優先序：

```
frozen（membership）  >  suspended（supervisor 命令中）  >  event-derived run phase
```

- supervisor 仍獨佔 suspend/resume/freeze；deriver 獨佔 ready/running/…/waiting。
- `KindRunEnd` / `KindIdle` 把 phase 收回 `READY`。
- **移除 `serve` 裡手動的 `setRun(RunBusy/RunIdle)`**（deriver 接手）—— 順帶把 §2.1 的
  web-`Run` 路徑不一致一起修掉（那條路徑也會 emit event）。

### 3.4 surface：UI 與工具一致的詞彙 + 每段耗時

- web roster pill 用與 TUI **同一套詞彙/顏色**顯示 phase，並加**每段 phase 的 elapsed 計時**
  —— 「EXECUTING bash 0:03」與「WAITING_APPROVAL 2:41」一眼可辨，直接回答「卡在哪個點」。
- `list_members` 輸出含 phase（`tools/messaging.go` 的 roster 視圖跟著升級）。

### 3.5（可選）stall 偵測

有了 per-phase entry-time，supervisor 可在成員停在某非-READY phase 超過閾值時 log/emit
警告（EXECUTING > N 分、WAITING_APPROVAL > M 分）。把無聲 hang 變成可見告警，成本極低
（phase 進入時間本就要記）。與 RP-2 的「卡審批」相互強化。

---

## 4. Scope

**In：**
- `roster.go`：`RunStatus` → `RunPhase`（豐富列舉）；`MemberView` / `MemberInfo` 帶 phase
  + 可選 elapsed/tool 名。
- 新增 per-member phase deriver（移植 `status.State.Apply` + 兩個 waiting phase）。
- `space.go`：tee 成員 event 進 deriver。
- `scheduler.go`：移除手動 `setRun`，改由 deriver 推導（suspended/frozen 仍命令權威）。
- `tools/messaging.go`：`list_members` 顯示 phase。
- `web/`：roster pill 詞彙/顏色對齊 TUI、每段 phase 計時。
- （可選）supervisor stall 偵測。

**Out：**
- 審批路由/並發/重放本身 → [RP-2](RP-2-permission-broker-routing.md)（本計畫只提供其觀測面）。
- TUI 端任何改動（純借用其 `status` 邏輯，不改它）。

---

## 5. Acceptance Criteria

1. roster/board 顯示**逐成員、細粒度**的即時 phase，與成員實際動作相符
   （thinking / executing-tool / draining / ready），精神等同 TUI pill。
2. 卡審批的成員顯示 `WAITING_APPROVAL: <tool>`（**非** `busy`）；卡 AskUserQuestion 顯示
   `WAITING_INPUT`。
3. 跑很久的工具顯示 `EXECUTING: <tool>` 且帶 elapsed 計時。
4. `suspended` / `frozen` 仍正確優先於 event phase。
5. `list_members` 反映同一套 phase（agent 互看一致）。
6. `serve` 中不再有手動 `setRun`；web-`Run` 路徑下的成員狀態也正確（不再假性 idle）。
7. 單元測試移植 TUI `state_test.go` 表，並補兩個 waiting phase + 優先序規則。

---

## 6. Definition of Done

- [ ] `RunPhase` 豐富列舉；deriver 移植 + 兩個 waiting phase；寫回 Roster（單一真相）。
- [ ] space tee event 進 deriver；`serve` 移除手動 setRun；suspended/frozen 優先序測試綠燈。
- [ ] web roster pill 對齊 TUI 詞彙 + 每段計時；`list_members` 帶 phase。
- [ ] phase 推導單元測試（含 waiting / 優先序）綠燈；`-race` clean。

---

## 7. 風險與取捨

- **event 多但便宜**：deriver 是純記憶體 switch（移植自既有 TUI 邏輯），每 event O(1)，
  與既有 sink 扇出同源，幾乎零成本。
- **waiting phase 的清除時機**：`KindApprovalNeeded` 設 WAITING，回答後 broker 解鎖、loop
  續跑會 emit 下一個 sub-phase event（ToolUseResult/Thinking…）→ 自然清除。需測「審批被
  拒 → 該成員回到 RUNNING/READY 而非卡在 WAITING」。
- **與 RP-2 的相依**：WAITING_APPROVAL 顯示的價值要 RP-2 §3.1 修好路由後才完整（否則
  顯示得到、卻仍回答不了）。但**RP-3 可先獨立上線**作為觀測 —— 反而能讓 RP-2 的 bug 在 UI
  上一眼現形，建議兩者相鄰排程。
- **`RunStatus`→`RunPhase` 的相容**：屬 Experimental tier 內部型別，可直接改；webapi DTO
  的 `run` 欄位字串值會擴充（前端同步），無對外 Stable 承諾受影響。
