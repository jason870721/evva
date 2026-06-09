# PRD — Veronica Phase 1：Swarm 基礎設施 — Implementation Plan

> 狀態：**草案 / Draft（方向已定，未進入實作）** ｜ 日期：2026-06-02
> 上層：[`roadmap.md`](roadmap.md) ｜ 設計：[`veronica-design-v1.md`](veronica-design-v1.md)
> 範圍：**只蓋 swarm 基礎設施**，不含任何交易邏輯（那是 Phase 2）。

---

## 1. TL;DR — 這個 phase 到底是什麼

把 evva 從「單 agent runtime」擴成「多 agent swarm 工作站」，新增兩個子命令：

- `evva service start` — 背景 process，開 `:8888`，**作為 multi-space host 同時 host 多套互相隔離的 swarm（子集團）**——「一個 service 跑多套 swarm」是核心能力（§4 元件 6、§5.6、設計 §3.1），不是單 swarm 服務。
- `evva swarm .` — 讀 `./evva-swarm.yml`，把集群註冊進在跑的 service（process 模型 **A**：service 在自己 process 內建構 agents）。

集群 = **一群獨立的 `agent.New(...)` root agent**（Leader + Workers），活在同一 process，靠 **Veronica 自寫的 message bus + `.vero/` SQLite** 協作。**對 agent runtime 只用公開 `pkg/*`**，不碰 `internal/agent`——使 swarm 成為 evva 的 multi-agent completeness oracle。唯一動 evva 既有 runtime 的是 M4 的 inbox-drainer 公開 seam。

**本 phase 完成 = [`roadmap.md`](roadmap.md) §5 的 DoD 全綠。**

---

## 2. Inventory — evva 已經給了什麼（不要重造）

| 已存在 | 用途 | 出處 |
| --- | --- | --- |
| `agent.New(Config, ...Option)` | 一通呼叫建一個 root agent（persona/memory/skills/permission/broker 全 wire） | `pkg/agent` |
| 「同 process 多 agent、無全域單例」 | 一個 process 開 N 個 agent 的根本保證 | `docs/extending.md:128` |
| `agent.AgentDefinition` + `AgentRegistry.Register` | in-code 註冊自訂 persona（全自訂 prompt） | `pkg/agent` |
| `Controller`（`Run/Continue/RespondPermission/RespondQuestion/...`） | 驅動單個 agent 的窄介面 | `pkg/agent/types.go` |
| `event.Sink`（`Emit(Event)`）+ `event.Event`（JSON-tag、`AgentID/ParentID`）+ `event.Multi` | 把 agent 事件推到 Web / log；多 sink 扇出 | `pkg/event`（註解明言預期 ws bridge） |
| `pkg/tools.Tool`（`Name/Description/Schema/Execute`）+ `WithCustomTool` / `toolset.DefaultRegistry()` | 自寫 `send_message`/`task_*`/`list_members` | `pkg/tools` |
| `skill.LoadRegistry` + `WithSkillRegistry` | per-agent skills | `pkg/skill` |
| `Agent.ResumeSession` / session 快照 / `/compact` | 重啟接續、長壽 agent context 控管 | `pkg/agent` |
| `pkg/permission`（Store/Broker/Mode）+ `RespondPermission` | 危險工具審批（接 Web） | `pkg/permission` |
| 既有 Agent(spawn) tool | 每個 root agent 任務內部一次性子代理 | evva 內建（列入 ActiveTools 即可） |
| `KindDrainBackgroundTask` / `KindDrainMonitorEvents` | M4 inbox-drainer 的**現成可抄 seam** | `pkg/event/event.go:140` |

> **重造警戒**：task 帳本 ≠ evva 內建 `todo` store（後者私有/暫存/自動收摺）；Veronica 的是跨 agent/持久/有驗證關卡的共享帳本。別混用。

---

## 3. Goal & acceptance criteria

**Goal**：交付一個可宣告（manifest）、可常駐、可擴編、可中止、可重啟接續的 in-process swarm runtime + Web 工作站，**只靠 `pkg/*`（+ 一個 M4 公開 seam）**。

驗收（對應 [`roadmap.md`](roadmap.md) §5 DoD，編號便於 PR gate）：

- **A1** `evva service start/stop/status` 正常；pidfile/log 落 `~/.evva/service/`。
- **A2** `evva swarm .` 註冊一個 ≥3 agent 的 space；Web 顯示 roster（membership + run-status）。
- **A2b（多 space，核心）** 兩個不同 workdir 各 `evva swarm .` → 兩個**完全獨立**的 space：各自 `.vero/vero.db`、bus、roster、**per-space 命名**（兩 space 可都有 `leader` 不衝突）；訊息/任務不跨 space；停一個不影響另一個。
- **A3** Leader push 任務、Worker 唯讀+回報、5 狀態機跑通；Web kanban 正確反映每次轉移；**Worker 寫 task 狀態被拒**。
- **A4** `send_message` 雙向 + `to:"all"`；落 SQL；drain A 注入並標已讀。
- **A5** timer 喚醒：宣告 `schedule` 的 agent 定時被 Run；idle 不燒 token（log/usage 佐證）。
- **A6** 動態加入即刻可定址；freeze 後不派任務、可解凍；**不提供刪除**。
- **A7** suspend 立即中止在飛 run；resume 能續；`kill -9` 後重啟，未讀訊息重入列 + `ResumeSession` 接續。
- **A8** drain B（M4）：busy agent 在**當前 run 下一輪**即看到緊急信。
- **A9** dep-check：`go list -deps ./internal/swarm/...` 不含 `internal/agent`。
- **A10** 測試：`store`/`bus`/`scheduler` 單元 + 一條 e2e（起→指派→協作→重啟→接續）全綠。
- **A11** 安全底線：service 預設綁 `127.0.0.1` + session token；危險工具走 permission。

---

## 4. Work breakdown（按元件；括號標對應里程碑）

> 介面為**草案簽名**，非最終實作；file-by-file 細節在各里程碑開工時再定（避免提前過度規格化導致 drift）。

### 元件 0 — 模組 skeleton + dep-check（M0）
- 建 `internal/swarm/`（service/supervisor/scheduler/bus/store/agentdef/tools/webapi 子包）、`cmd/evva` 子命令分派、`web/`（vite 專案）。
- CI dep-check：斷言 `internal/swarm` 對 agent 概念只 import `pkg/*`（A9）。

### 元件 1 — `store`（`.vero/vero.db`）（M1，messages 於 M2）
- schema：`tasks`（單寫者=Leader）、`messages`（UUID、多寫者）、各 agent 自有/共享讀域表（見設計 §7）。WAL + `busy_timeout`。
- DAO：`sync.RWMutex` 包一層；寫 `Lock()`、讀 `RLock()`。
- **task 狀態機**：合法轉移表（pending→running→verifying→completed；suspended；reject 回 running/pending），非法轉移回錯。
- 介面草案：
  ```go
  type Store struct { /* *sql.DB + RWMutex */ }
  func (s *Store) CreateTask(t Task) (int64, error)               // Leader
  func (s *Store) TransitionTask(id int64, to Status, by string) error // Leader-only；驗證合法轉移
  func (s *Store) ListTasks(filter) ([]Task, error)               // 任何人讀
  func (s *Store) PutMessage(m Message) error                     // 任何 agent；含 UUID
  func (s *Store) GetMessage(id string) (Message, error)
  func (s *Store) MarkRead(id string) error
  func (s *Store) UnreadFor(recipient string) ([]string, error)   // 重啟 reload
  ```

### 元件 2 — `bus` + mailbox（M2）
- 每 agent 一個 `chan string`（傳 msg-uuid）；`Bus.Send(to, msgUUID)`、`Bus.Inbox(name) <-chan string`。
- `to:"all"` 廣播給所有 active 成員。
- 投遞先寫 `messages`（store）再推 uuid 進 chan（順序保證）。

### 元件 3 — `supervisor` / `scheduler` / `roster`（M0 起，逐步補）
- **每個 SwarmSpace 一組** supervisor/scheduler/roster（space 之間不共享；見元件 6 的兩層結構與設計 §3.1）。
- **roster**：`name → { Controller, role, membership(active|frozen), runStatus(idle|busy|suspended), currentTask, whenToUse }`。**per-space**、單一真相，餵 `list_members` + Web。
- **scheduler**：喚醒來源 = {message, task, timer}（設計 §5.5）；任一觸發且 agent idle → 組 synthetic prompt → `Controller.Run(ctx, prompt)`。timer 由 `profile.yml` 的 `schedule` 驅動。
- **supervisor**：`AddMember(name)`（hot-load，M3）、`FreezeMember/Unfreeze`、`Suspend(name)`（cancel run ctx）/`Resume`、`HaltAll`（Phase 2 friday kill switch 用）、重啟 reload（M3）。
- 介面草案：
  ```go
  func (sp *Supervisor) AddMember(name string) error      // 呼叫 agentdef.Build → agent.New → 入 roster
  func (sp *Supervisor) Freeze(name string) error
  func (sp *Supervisor) Suspend(name string) error        // cancel 該 agent run ctx
  func (sp *Supervisor) HaltAll() error
  func (sp *Supervisor) wake(name, reason, prompt string)  // scheduler 內部：idle→Run
  ```

### 元件 4 — `agentdef` loader（M3，M0 先硬寫最小版）
- 讀 `agents/{main,sub}/{name}/`（`profile.yml`/`system_prompt.md`/`tools/{active,deferr}.yml`/`skills/`）→ `agent.AgentDefinition` + `skill.Registry`。
- **可重複呼叫**（= hot-load 與重啟重建共用同一函式）。
- `profile.yml` 解析 `model` / `effort` / **`schedule`**（timer 喚醒）。

### 元件 5 — custom tools（M1/M2）
- `task_create/assign/update_status/verify/list`（Leader）、`my_tasks/task_get`（Worker 唯讀）、`send_message`（每 agent 一份、烤 sender）、`list_members`。
- 全部用 `pkg/tools.Tool`；經 `WithCustomTool` 掛到對應 agent。Leader 與 Worker 工具集不同（權限差異即工具差異）。

### 元件 6 — `service`（multi-space host）+ `webapi`（M0 起）
- **兩層結構（核心）**：`Service`（process 單例：:8888 + **`SwarmSpace` registry** `map[id]*SwarmSpace` + token + 生命週期）→ 每個 `SwarmSpace`（自己的 workdir / db / bus / roster / supervisor / agents，**完全隔離**，見設計 §3.1）。Service 從第一天就是「多 space 容器」，不存在 single-space 寫死。
- HTTP（:8888）+ WebSocket；每 agent 一個 `event.Sink`，依 **`(spaceID, AgentID)`** 分流推 ws。
- REST：`/api/swarms`(列所有 space)、`/api/swarm/:id`(roster)、`/api/tasks`、`/api/agents/:name/transcript`、`/api/messages`。
- 入站：Web → `Controller`（Run/RespondPermission/RespondQuestion）、supervisor（suspend/add/freeze/halt）。
- CLI：`evva swarm .`(註冊 space) / `evva swarm ls` / `evva swarm stop <name>`。
- vue.js SPA：**space 選單（子集團清單）** / Team Board / Roster / Leader Chat；build 後 `embed.FS`。

### 元件 7 — `cmd/evva` 子命令（M0）
- `service start|stop|status`（背景化 + pidfile）；`swarm .`（POST workdir 進 service）；`swarm add <name>`（M3）。
- 既有 `evva`（TUI）不變。

### 元件 8 — inbox-drainer 公開 seam（M4，唯一動 evva runtime）
- 在 `pkg/agent` 開可插拔介面（推廣 `KindDrainBackgroundTask`）：loop 每輪 iteration boundary 呼叫 drainer，fold 回傳的 synthetic message。
- swarm 提供一個 drainer：回查 mailbox uuid → 組「來自 X 的信」→ 標已讀。
- 附測試 + `docs/extending.md` + `CHANGELOG` + 版本（minor、additive）。nil drainer 必須 noop（不回歸單 agent）。

---

## 5. Design decisions & risks（開工前讀）

- **5.1 process 模型 A（v1）**：service 在自己 process 內 host 所有 swarm；`evva swarm .` 只 POST workdir。最簡、零 IPC。**不變量：inter-agent bus 永遠在 process 內。** crash 隔離需求 → 演進模型 C（設計 §4.2），非本 phase。
- **5.2 task 表單寫者**：push + 只有 Leader 寫狀態 → 無搶單競爭、無需原子認領；RWMutex 重點在 `messages`（多寫）。
- **5.3 訊息 DB 為真相、chan 傳 uuid**：天生可回看、可重啟接續（reload 未讀）；別把 payload 放 chan。
- **5.4 timer 喚醒是 scheduler 的事，不是 agent 的事**：用 Supervisor 層 timer 驅動 `Controller.Run`，不靠 agent 自行 `sleep`（會卡 run）。
- **5.5 §1.1 紀律**：swarm 對 agent 只用 `pkg/*`；dep-check 強制。唯一例外是 M4 的公開 seam（本來就該公開）。
- **5.6 多 space 隔離 + 崩潰半徑（核心）**：每個 `SwarmSpace` 一套 db/bus/roster/agents，互不可見；**agent 名 per-space scoped**；event 以 `(spaceID, AgentID)` 路由。模型 A 下所有 space 共用 process → **每個 agent run 必須跑在 `recover()` 守護的 goroutine**，把 panic 收斂在單次 run、不殺 process（讓「一 process 多 swarm」安全可靠）。真崩潰隔離 = 模型 C（每 space 一 process，演進路）。
- **風險**：① 長壽 agent context 膨脹 → e2e 要涵蓋 compaction；② Web 是 RCE 面 → 預設 localhost + token + permission；③ scheduler 與 run 生命週期競態（suspend 正在 wake 的 agent）→ 用 per-agent 狀態鎖 + ctx 收斂測試覆蓋。

---

## 6. Out of scope（Phase 1）

- 任何交易/金融邏輯、外部行情/交易所串接（Phase 2）。
- process 模型 C、跨機 bridge。
- Leader 自主 `hire_member`、成員刪除（只 freeze）。
- 多 swarm space 的進階管理（配額/跨 space 通訊）；M3 只做基本多 space。
- bundled 第三方整合。

---

## 7. Verification checklist (PR gate)

- [ ] A1–A11 全綠（§3）。
- [ ] `internal/swarm` dep-check 不含 `internal/agent`（A9）。
- [ ] `store`/`bus`/`scheduler` 單元測試 + 一條 e2e（A10）。
- [ ] M4 seam 有 downstream 編譯測試；nil drainer noop（單 agent 不回歸）。
- [ ] service 預設 `127.0.0.1` + token；危險工具 permission gate（A11）。
- [ ] 文件：`docs/extending.md` 新增 inbox-drainer 章節；`CHANGELOG` + 版本 bump。

---

## 8. Package/file change list（在 evva repo 內，cheat sheet）

```
cmd/evva/
  service.go            # evva service start/stop/status（新）
  swarm.go              # evva swarm . / add（新）
internal/swarm/         # 新子系統（Experimental）；對 agent 概念只 import pkg/*
  service.go  supervisor.go  scheduler.go  roster.go
  bus/        store/{store.go,tasks.go,messages.go,migrations/}
  agentdef/   tools/    webapi/
web/                    # vue.js（vite）；build 後 embed
pkg/agent/              # M4 唯一新增：inbox-drainer 公開 seam（additive）
docs/extending.md       # M4：inbox-drainer 章節
CHANGELOG.md            # M4：版本條目
```
