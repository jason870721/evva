# Veronica（evva swarm 子系統）— 設計藍圖 v0.1

> 狀態：**草案 / Draft（可行性探索階段）**
> 定位：**evva 專案內的子系統（代號 Veronica）**，提供「agent swarm 服務 + Web 工作站」。
> 不另開 repo —— 在 evva 裡以新子命令 `evva service` / `evva swarm` 實作。
> 本文目的：把初期想法收斂成一份「可以開始動工」的第一版架構藍圖，
> 並明確切出「哪些用現成的 evva `pkg/*` 蓋、哪些要動到 evva 的 internal/loop」。
>
> **v0.1 決策更新（2026-06-02，多輪收斂）：**
> ① 不拆獨立專案，folding 進 evva（`evva service` / `evva swarm` / `evva-swarm.yml`）；
> ② agent 佈局自寫 loader；③ push 派工、只有 Leader 能寫 task 狀態；
> ④ 訊息落 SQL、chan 只傳 uuid；⑤ event-driven 喚醒；⑥ leader/worker 全自訂 prompt；
> ⑦ 不支援同名/replicas（要多個取不同名）；⑧ 動態加入只給 User；⑨ 驗證可 spawn general agent；
> ⑩ **process 模型 A**（service host 一切，inter-agent bus 不出 process）；
> ⑪ **多 swarm space 是核心能力**——一個 service host 多個互相隔離的「子集團」，從 M0 起（§3.1）；
> ⑫ **timer 喚醒**（喚醒來源 = message / task / timer）+ 常駐值班 agent，task 狀態機 per-agent 可選（§5.5）。詳見各節與 §12。
>
> **定位：Veronica 是 evva 當前唯一核心開發目標**（其餘 v1.x 暫緩）；**品質大於速度**、長期。兩階段：**Phase 1** 蓋 swarm 本身、**Phase 2** 用 crypto trading team 驗證實用性。
> 文件集（皆在 `docs/veronica/`）：本設計 + [`roadmap.md`](roadmap.md)（兩階段、里程碑 gate、DoD）+ [`prd-phase1-swarm.md`](prd-phase1-swarm.md) + [`prd-phase2-trader-team.md`](prd-phase2-trader-team.md)；`CLAUDE.md` 已 reorient。

---

## 0. 一句話定義

**Veronica 是 evva 的一個子系統：`evva service start` 在背景開一個 `:8888` 的 swarm 工作站服務，
`evva swarm .` 把一個 agent 集群註冊進去。集群裡一個 Leader 對 User 負責、把工作拆成任務、
push 給一群各有專長的 Worker；所有成員都是 evva root agent、同處一個 process、用一份共享 SQLite
協作，User 從 vue.js 介面進入某個 swarm space 與 Leader 互動。**

---

## 1. Veronica 在 evva 裡的定位（同一個 repo）

Veronica 不是獨立專案，是 **evva 內的子系統**：

- 既有 `evva`（單 agent TUI）**不變**；新增 `evva service` / `evva swarm` 子命令（additive，符合 evva 的 phase 哲學）。
- 「evva 完全為了服務 Veronica」現在是字面意義：同一個 codebase，swarm 的需求直接塑形 evva。
- swarm 子系統屬 **Experimental tier**（如 `pkg/ui/bubbletea`、`pkg/observable`），不污染 v1.0 的 Stable `pkg/*` 承諾。

### 1.1 一條要守的紀律：swarm 仍只消費 `pkg/*`（multi-agent oracle）

swarm 子系統雖然放 repo 內的 `internal/swarm/`，**對 agent runtime 的使用仍應只走公開的 `pkg/*`**
（`agent.New`、`pkg/event`、`pkg/tools`、`pkg/skill`…），**不直接 import `internal/agent`**。理由：

- evva v2 花大力氣建立的「completeness oracle」（`cmd/evva` 零 `internal/` import、`examples/full-host`
  pkg-only）是 evva 的招牌；
- 讓 swarm 維持 pkg-pure，它就成為 evva 的**多 agent completeness oracle**：「如果 evva 自己的 swarm
  服務能只靠 `pkg/*` 蓋出來，第三方也能」—— 比 v2 north star 更強的版本；
- 唯一需要動 `internal` 的是需求②（loop 內 drain），而那本來就該做成**公開可插拔 seam**（加在 `pkg/agent`），
  不是私接。同 repo 反而讓「加公開 seam + 用它」能在一個 PR 完成。
- **強制**：加一條 dep-check（鏡像現有的 `go list -deps ./cmd/evva | grep internal` oracle），
  斷言 `internal/swarm` 對 agent 概念只 import `pkg/*`。

> 成本幾乎為零：swarm 需要的 bus / store / scheduler / webapi 全是新程式碼，本來就不碰 internal。

---

## 2. 核心架構決策（最重要的一節）

### 2.1 決策：swarm 是「一群平行的 root agent + Veronica 自己的訊息匯流排」，**不是** evva 的 subagent 樹

evva 今天的多 agent 模型與 Veronica 的願景**根本上不同**，後面所有設計都從這裡長出來：

| 面向 | evva 現況（subagent spawn） | Veronica 需要的 |
| --- | --- | --- |
| 拓撲 | 嚴格**兩層**：root → subagent，且 `subagents cannot spawn subagents`（`spawner.go:69`） | **扁平 mesh**：Leader 與多個 Worker 互為對等節點 |
| 生命週期 | subagent **用完即拋**（spawn → 跑完 → 回傳 final text） | Worker **長壽**，跨多個任務存活 |
| 通訊 | 只能 child **回傳結果**給 parent（blocking RPC） | 任意 agent 之間**非同步互發訊息**（辦公室收發信） |
| 狀態 | 無「在線目錄」，沒有 idle/busy | 每個 agent 有 idle/busy 狀態 + mailbox |

evva 的 `CLAUDE.md` 與 `docs/evva-sdk/sdk-v2.md:43` 都把 **「Multi-agent Teams / SendMessage」明確列為
out-of-scope**（理由是它需要 socket/JWT/跨機轉發的 bridge）。**Veronica 用「單 process 內的 mesh」迴避整個
bridge**：團隊每個成員（Leader、每個 Worker）都是**獨立的 `agent.New(...)` root agent**，全部活在**同一個
process**（SDK 保證「同 process 多 agent 無全域單例」，`docs/extending.md:128`），協作走 **Veronica 自己的
message bus**，而非 evva spawn。

### 2.2 但每個 root agent **仍保有 spawn 能力**（用在任務內部）

§2.1 是講「**團隊拓撲**不用 spawn 組」。但**每個成員（含 Leader）自己的工具集裡仍開著 evva 的 Agent(spawn)
工具**，用於**任務內部**的一次性子代理：

- Worker 要做大範圍 code 搜尋 → spawn 一個 `explore` subagent。
- **Leader 驗收任務（`verifying`）→ spawn 一個 general-purpose subagent 跑測試/客觀 review**（見 §7.1）。

兩層限制（subagent 不能再 spawn）只作用在**每個 root 各自的 spawn 樹內**；因為團隊成員都是 root，每個都有自己
一份兩層預算，驗證/搜尋這種一次性子任務綽綽有餘。**team = mesh + bus；intra-task = spawn。兩者並存。**

### 2.3 為什麼這個決策成立（而且划算）

- **繞過兩層限制**：團隊成員都是 root，不撞「subagent 不能再 spawn」的牆。
- **長壽 + 對等通訊**：mailbox + bus 是 Veronica 自己的，不受 evva spawn 語意約束。
- **單 process、單 SQLite、單 RWMutex** 全講得通，直接迴避所有 socket/JWT 複雜度。

---

## 3. 系統架構

`evva service` 是**一個背景 process**，host 一個或多個 **swarm space**（每個 space = 一個註冊進來的集群）。
**關鍵不變量：每個 swarm 的 inter-agent bus 永遠在記憶體、在該 swarm 的 process 內**——絕不退回跨機 bridge。
（單 process vs 每 swarm 一 process 的取捨見 §4.2。）

```
  Browser (vue.js SPA)        ┌─ evva service（背景 process, :8888）──────────────────────────┐
  ┌────────────────────┐      │  ┌──────────────────────────────────────────────────────┐    │
  │ swarm space 選單    │ WS/  │  │ Web Server (net/http + WS)                           │    │
  │ Team Board / Roster │◄REST─┼─►│  - 多 swarm space 分流；每 agent 一條 event 流(AgentID)│    │
  │ Leader Chat         │      │  └───────────────┬──────────────────────────────────────┘    │
  └────────────────────┘      │                  │                                            │
                              │  ┌───────────────▼──────────────┐  ┌────────────────────────┐ │
                              │  │ swarm space "my-eng-team"     │  │ swarm space "..."(另 wd)│ │
                              │  │  ┌──────────┐ roster/scheduler │  └────────────────────────┘ │
                              │  │  │Supervisor│ +動態加入/冷藏    │                             │
                              │  │  └──┬────┬──┘                  │                             │
                              │  │  ┌──▼─┐┌─▼──┐┌────┐ (都是 root) │                             │
                              │  │  │Lead││ WA ││ WB │… ←每個都可   │                             │
                              │  │  └──┬─┘└─┬──┘└─┬──┘   spawn 子代理│                            │
                              │  │  ┌──▼────▼─────▼──┐             │                             │
                              │  │  │ bus + .vero/vero.db (RWMutex)│ │                            │
                              │  │  └─────────────────┘            │                             │
                              │  └──────────────────────────────────┘                           │
                              │                  ▲ register (workdir)                            │
                              └──────────────────┼──────────────────────────────────────────────┘
                                                 │ `evva swarm .`  (控制面 CLI)
                                          User 在某個專案目錄裡執行
```

### 元件清單

| 元件 | 職責 | 蓋在哪 |
| --- | --- | --- |
| **Service host（:8888）** | 背景 process；管理多 swarm space；Web Server | `internal/swarm`（用 `pkg/event` sink） |
| **Supervisor / Scheduler**（每 swarm 一個） | 啟停/動態加入/冷藏 agent；維護在線 roster；「mailbox 有信 or 有指派」→ `Controller.Run()`；管 run context（suspend=cancel） | `internal/swarm`（純 `pkg/*`） |
| **Message Bus + Mailboxes** | 每 agent 一個 `chan`（只傳 msg-uuid）；`send_message` 投遞；訊息持久化 SQLite | `internal/swarm/bus` |
| **State Store（`.vero/` SQLite）** | 共享任務帳本 + 狀態機 + 訊息表 + 各 agent 自有 table；RWMutex | `internal/swarm/store` |
| **Agent 實例 ×N** | Leader + Workers，各是一個 `agent.New(...)` root | evva `pkg/agent` |
| **Custom Tools** | `send_message`、`list_members`、`task_*` | `internal/swarm/tools`（用 `pkg/tools`） |
| **vue.js SPA** | swarm space 選單、Team Board、Agent Roster、Leader 對話框 | `web/`（embed 進 evva binary） |

### 3.1 多 swarm space：一個 service、多個「子集團」（first-class 核心能力）

**一個 `evva service`（一個 process、一個 :8888）能同時 host 多套互相獨立的 swarm**——像一間控股公司底下開很多**子集團**。這是**核心能力，不是附加選項**（Johnny 非常看重；絕不做成「一個 service 只能跑一套」）。結構是兩層：

```
Service（process 單例：:8888 + SwarmSpace registry + token + 生命週期）
  ├─ SwarmSpace "my-eng-team"   ← 一套 swarm = 一個子集團（完全隔離）
  │    ├─ workdir + .vero/vero.db（自己的 *sql.DB + RWMutex）
  │    ├─ Bus + mailboxes（自己的）
  │    ├─ Roster + Supervisor/Scheduler（自己的）
  │    └─ agents：leader / worker…（自己的 agent.New 實例）
  ├─ SwarmSpace "trading-team"  ← 另一套，跟上面互不可見
  └─ SwarmSpace …
```

**隔離保證（每個 space 是一個獨立子集團）：**

| 維度 | 隔離方式 |
| --- | --- |
| 任務 / 訊息 | **每 space 一個 `.vero/vero.db`**——task ledger、messages **絕不跨 space** |
| 訊息匯流排 | 每 space 一個 Bus；`send_message` 只在**寄件人所屬 space 內**解析收件人 |
| agent 命名 | **名字 per-space scoped**——space A 的 `leader` ≠ space B 的 `leader`；§4.4「不可同名」是**在同一 space 內**，engineer-1/2 唯一性也只在 space 內 |
| 生命週期 | 各 space 獨立啟停/重啟——停掉 trading-team 不影響 eng-team |
| 設定 / 額度 | 每 agent 各自的 `*config.Config` → space 之間可用**不同 provider / API key / 預算**（不同子集團不同帳） |
| 事件路由 | service 用 `(spaceID, AgentID)` 標每條 event 流，扇出到對應 Web space |

**共享（process 層）：** 一個 :8888 HTTP/WS server、一個 session token、一個 process、一套 evva provider 註冊。

**崩潰半徑（誠實說）：** 模型 A 下所有 space 共用 process —— 一個 agent panic 理論上波及全體。緩解：**每個 agent run 跑在 `recover()` 守護的 goroutine**，把 panic 收斂在「那一次 run」、不殺 process（這讓 A 也能安全地一對多）。要**真**崩潰隔離（每 space 一 process）就是模型 C（§4.2）——這正是「一個 process 管很多 swarm」（要 A）與「崩潰隔離」（要 C）的取捨點。

> CLI：`evva swarm .`（在某子集團目錄）→ 註冊成新 space；`evva swarm ls` 列所有 space；`evva swarm stop <name>` 停一個。Web 首頁就是 space 選單（子集團清單）。

---

## 4. 子命令、process 模型與 manifest

### 4.1 子命令

```bash
evva                      # （不變）單 agent TUI，今天的 evva
evva service start        # 背景啟動 :8888 swarm 服務（pidfile/log 於 ~/.evva/service/）
evva service stop|status
evva swarm .              # 讀 ./evva-swarm.yml，把這個集群「註冊」進在跑的 service
                          #   → 回一個 swarm space URL，User 進 Web 與 leader 互動
```

### 4.2 process 模型（⚠️ 命令設計逼出的關鍵岔路）

`evva swarm .` 的 agent 到底跑在哪個 process？這決定「單 process + 記憶體 bus + RWMutex」能否成立：

| | **A：service host 一切**（v1 採用） | **C：每 swarm 自己一個 process，service 當 web gateway**（演進路） |
| --- | --- | --- |
| agent 跑在 | service process 內 | `evva swarm .` 起的 process 內 |
| 跨 process 的東西 | 無 | **只有** event/command 串到 gateway（≈ 本來就要的 ws，多一跳） |
| inter-agent bus | service 內記憶體 | **各 swarm process 內記憶體**（一樣不破不變量！） |
| 取捨 | 最簡單、零 IPC；一個 agent crash 可能拖垮整個 service | crash/資源隔離；多一條 swarm↔gateway 協定 |

- **v1 採 A**：`evva swarm .` 把 `{workdir}` POST 給在跑的 service，service **在自己 process 內**用該 workdir
  建構這個 swarm 的 agents（evva agent 本就吃 `config.WorkDir`，per-agent 設定即可）。M0–M3 全程單 process、最簡。
- **C 是演進路**：需要多 swarm 隔離 / 穩定性時再升。**升級時 bus 仍在各 swarm process 內**，只把 event/command
  relay 搬到 gateway —— 不碰 inter-agent 通訊。

> 設計準則：把 **Supervisor ↔ Web** 的邊界做乾淨（事件出、指令入），A→C 才能平滑演進。

### 4.3 啟動時建立 `.vero/`

每個 swark（每個 workdir）有自己的 `.vero/`，**該 swarm 的所有持久化都在這**：

```
.vero/
├── vero.db            # 核心 SQLite（任務帳本、訊息、各 agent 自有 table）
├── sessions/          # 各 agent 的 evva session 快照（重啟接續用）
├── logs/              # 各 agent runtime log
└── runtime.json       # 執行期 metadata（在線/冷藏 agent…）
```

### 4.4 `evva-swarm.yml` 範例（草案；無 replicas、不允許同名）

```yaml
# evva-swarm.yml
name: my-eng-team
workdir: .

leader:
  agent: leader              # 對應 agents/main/leader/

workers:
  - agent: backend-dev       # 對應 agents/sub/backend-dev/
  - agent: frontend-dev
  - agent: qa
  # 需要兩個工程師就取不同名：engineer-1 / engineer-2（不支援同名 / replicas）

settings:
  permission_mode: default   # 透過 Web UI 審批
  max_iterations: 50
```

### 4.5 agent 目錄佈局（✅ 自寫 loader）

**佈局：**
```
agents/main/{name}/  profile.yml · system_prompt.md · skills/{s}/SKILL.md · tools/active.yml · tools/deferr.yml
agents/sub/{name}/   （同上）
```

在 `internal/swarm/agentdef/` 寫薄 loader，把佈局**讀進來、map 成公開的 `agent.AgentDefinition` +
`skill.Registry`**，再用 SDK 的 **in-code 註冊**餵進去（全程 `pkg/*`）：

```go
// 偽碼：讀「一個 agent 目錄」→ 產出 live agent。動態加入(§5.4) = 再呼叫一次。
func (l *Loader) Build(dir string) (agent.AgentDefinition, *skill.Registry) {
    return agent.AgentDefinition{
        Name:          base(dir),
        As:            tierOf(dir),         // main/sub 只是角色標記；兩者都是 root（語意見下註）
        SystemPrompt:  read(dir, "system_prompt.md"),  // ← 全自訂的 leader/worker prompt
        ActiveTools:   parseYAML(dir, "tools/active.yml"),   // 含 Agent(spawn)、send_message、list_members…
        DeferredTools: parseYAML(dir, "tools/deferr.yml"),
        Model:         profile(dir).Model,
    }, skill.LoadRegistry(filepath.Join(dir, "skills"))
}
```

> ⚠️ **`As` 在 Veronica 重新定義**：evva 裡 `subagent`=「可被 Agent tool spawn」；Veronica 裡 `main`/`sub`
> 只是「Leader vs Worker」角色，**兩者都是 root agent**。別跟 evva spawn 語意搞混。
> **Leader/Worker 的 system_prompt 全部自訂** —— 不沿用 evva 的 coding persona（那是工程師人格，不是管理者）。
> Leader prompt 著重**團隊管理 / 任務拆解 / 驗收**；Worker prompt 著重各自領域實作。
>
> ✅ **自寫 loader 的紅利**：「讀目錄 → live agent」可重複呼叫，所以**動態加入成員（§5.4）= 再呼叫一次**、
> **重啟接續**也是用它把 `agents/` 重建一遍。

---

## 5. Agent 生命週期、狀態與成員管理

### 5.1 兩個正交的維度

```
  成員資格 (membership)            執行狀態 (run, 僅 active 時有意義)
  ┌────────┐  冷藏/解凍            ┌──────┐  有信/有指派   ┌──────┐
  │ active │◄──────────►│frozen│   │ idle │──────────────►│ busy │ (=Controller.Run 中)
  └────────┘            └──────┘   └──────┘◄──────────────└──────┘
   可派任務            不派任務        ▲   Run 結束           │
                       (不刪除)        │  resume(新 Run)      │ User/Leader 喊停
                                       │                ┌─────▼─────┐
                                       └────────────────│ suspended │ (=cancel run ctx)
                                                        └───────────┘
```

- **membership：`active` | `frozen`。** `frozen`（冷藏）= 成員還在、但 Supervisor 不派任務給牠。
  **v1 不做刪除**（刪除要處理在飛任務、懸空指派、訊息引用，複雜且危險）；冷藏是安全的「下線」，可再解凍。
- **run（僅 active）：`idle` | `busy` | `suspended`。**
  - `idle → busy`：Supervisor 的 scheduler 偵測到**三種喚醒來源任一**（① mailbox 有信、② 被指派 task、③ **計時器到點** —— agent 在 `profile.yml` 宣告 `schedule`）→ 組 prompt 呼叫 `Controller.Run(ctx, prompt)`。**idle 不燒 token。**（喚醒來源見 §5.5）
  - `busy → idle`：Run 回傳。
  - `busy → suspended`：User/Leader 喊停 → Supervisor cancel 該 run 的 `ctx`。
  - `suspended → busy`：resume = 開一段新的 Run。

### 5.2 在線 roster（Supervisor 的真相來源）

每個 swarm space 的 Supervisor 維護一份**在線目錄**：
`name → { handle: Controller, role, membership, run-status, currentTask, profile.when_to_use }`。
同時餵給 ① agent 的 `list_members` 工具；② Web 的 `/api/swarm`。**單一來源，兩個出口。**

### 5.3 `list_members` 工具（查當前 swarm 成員，方便寄信）

所有 agent 都拿這個唯讀工具，回傳每個成員：`name / role / specialty(when_to_use) /
membership / run-status / currentTask`。用途：寄信前先 `list_members` 看「誰在、誰是對的專家」再決定收件人。
（User 端在 Web Agent Roster 看同一份。）

### 5.4 動態新增成員（hot-load，不重啟；觸發者 = User）

1. 在 `agents/sub/{name}/` 放好描述檔（`profile.yml` + `system_prompt.md` + `tools/*` + `skills/*`）。
2. 觸發 Supervisor 的 `AddMember(name)`：loader `Build()` 讀目錄 → `agent.New(...)` runtime 建構 →
   掛 sink/mailbox/custom tools → 插入 roster（`active`+`idle`）。
3. 完成 —— 立刻可被 `list_members` 看到、可寄信、可被 Leader 指派。

> 全靠 evva 現成能力（同 process 多 agent）。**v1 觸發者僅 User**（CLI `evva swarm add <name>` 或 Web 按鈕）。
> 「給 Leader 一個 `hire_member` 工具自主擴編」**v1 先不做**，未來可能開放（§12）。

### 5.5 喚醒來源 = {message, task, timer}；task-driven vs 常駐值班

scheduler 的喚醒來源有三種，任一觸發都讓 idle→busy：

| 來源 | 觸發 | 典型 agent |
| --- | --- | --- |
| **message** | mailbox 收到信 | 所有 agent |
| **task** | 被 Leader 指派 task | task-driven worker（走 §7 狀態機） |
| **timer** | `profile.yml` 宣告的 `schedule`（cron / interval）到點 | **常駐值班**（定期巡檢、定時復盤） |

由此分出兩種 worker：
- **task-driven**：靠 Leader 指派驅動，走 task 狀態機（§7）。
- **常駐值班（standing-duty）**：靠 timer + message 驅動，**不一定有 task row**（如風控監控、每日復盤）。

→ **task 狀態機是 per-agent 可選**，不是所有 worker 都套。timer 喚醒純 `pkg/*`：scheduler 在 tick 時用 synthetic
prompt（如「定時巡檢：檢查倉位是否違反風控」）呼叫 `Controller.Run`，idle 時不燒 token。這是 trading 實驗（§Phase 2）逼出的一般化。

---

## 6. Message Bus 與 `send_message`

### 6.1 `send_message`（每個 agent 各建一份的 custom tool）

```jsonc
{
  "to":       "backend-dev",      // agent 名 | "leader" | "all"（有效值用 list_members 查）
  "subject":  "需要 schema 確認",  // 選填
  "body":     "User 改了需求，orders 表要加 status 欄位，你的 migration 要跟著改。",
  "ref_task": 42                  // 選填，關聯任務
}
```

- 用 `pkg/tools.Tool` 介面寫。
- **寄件人身份**：`Execute` 拿不到「我是誰」，所以每個 agent 各建一份（`WithCustomTool("send_message", factory)`，
  closure 烤進該 agent 名字）。多 agent 協作下**收件人必須知道是誰寄的**，來源寫進 `messages.sender`。
- Execute：寫一筆 `messages`（UUID、sender、recipient、body…）到 SQLite → 把 **UUID** 推進收件人 mailbox `chan` → 回「已送達」。

### 6.2 Mailbox 與訊息持久化（落 SQL、可回看、可重啟接續）

- **SQLite `messages` 表是訊息唯一真相來源（durable）。** 每封信一個 **UUID**，標明 `sender`/`recipient`/`read_at`（NULL=未讀）。
- **記憶體 mailbox `chan` 只傳 message-UUID**，不傳整包 —— chan 純當「有新信、照順序」的通知 + 排隊。
- **drain 時用 UUID 回查**：`SELECT * FROM messages WHERE id=?` → 取完整內容 → 標來源 → inject 進 system prompt →
  `UPDATE messages SET read_at=? WHERE id=?`（**標記已讀**）。
- **重啟接續**：重啟時 Supervisor 對每個 agent 跑 `SELECT id FROM messages WHERE recipient=? AND read_at IS NULL
  ORDER BY created_at`，把未讀 UUID 重塞回 chan —— **沒處理完的信不會掉**。搭配持久 task 帳本（§7）+ 各 agent 的
  `.vero/sessions/` 快照 + `Agent.ResumeSession`，swarm 重啟後接續工作。

### 6.3 Drain 的兩階段（唯一會「戳進 evva loop」的地方，分期做）

| 階段 | 時機 | 做法 | 依賴 |
| --- | --- | --- | --- |
| **A. run 之間投遞**（先做） | agent idle 時 | Supervisor 看到 chan 有 UUID，下一次 `Run()` 前用 UUID 回查、組「來自 X 的信：…」塞進 prompt、標已讀 | **純 `pkg/*`，零 evva 改動** |
| **B. run 之中 drain**（後做） | 每輪 LLM 結束 | evva loop 在 iteration boundary 把 mailbox UUID 回查、fold 成 synthetic user message | **要 evva 加公開 seam**（§9-需求②） |

> 階段 B **不是新發明** —— evva 內部早有一模一樣機制：`KindDrainBackgroundTask` / `KindDrainMonitorEvents`
> （`pkg/event/event.go:140`）就是「在 iteration boundary 把背景事件 fold 成 synthetic user message」。
> Veronica 要的 mailbox drain 是同一個 seam 的推廣。loop 在 `internal/` 且不可配置，所以這個推廣**必須由 evva
> 做成公開可插拔 seam**（§1.1）。**v1 先只做階段 A** 就能跑出會互相發信協作的團隊；B 是「忙碌中即時收信」的體驗升級。

---

## 7. Task 狀態機 + `.vero` SQLite

### 7.1 狀態機（5 狀態；**只有 Leader 能寫狀態**、push 派工、驗證可 spawn）

**主動 push**（Leader 指派給特定 Worker），**只有 main agent（Leader）能修改 task 狀態**。Worker 全程**唯讀**任務
（`my_tasks`/`task_get`）+ 用 `send_message` 回報；**所有狀態轉移都是 Leader** 根據回報（或 User 指令）執行。

```
        task_create (Leader)
              │
              ▼
         ┌─────────┐  指派+啟動  ┌─────────┐  worker回報完成 ┌───────────┐  approve  ┌───────────┐
         │ pending │ ──────────►│ running │ ──────────────►│ verifying │ ─────────►│ completed │
         └─────────┘  (Leader)  └─────────┘   (Leader設)   └─────┬─────┘ (Leader)  └───────────┘
              ▲                   │     ▲                        │  ▲ Leader 可 spawn general
   reject:reopen(Leader)     stop │     │ resume                 │  │ subagent 跑測試/客觀驗收
              └───────────────────┼─────┘                        │  └───────────────
                                  ▼                              │ reject:rework(Leader)
                            ┌───────────┐                        ▼
                            │ suspended │◄───────────── 回 running 或 pending
                            └───────────┘
```

| 轉移 | 觸發 | 機制（**狀態欄一律由 Leader 寫**） |
| --- | --- | --- |
| → `pending` | Leader | `task_create`（push：建立時即指定 `assignee`） |
| `pending → running` | Leader | `task_assign`：發 message 推給該 Worker，Leader 設 `running` |
| `running → suspended` | Leader（含代 User） | Leader 設 `suspended` + Supervisor cancel 該 Worker run |
| `suspended → running` | Leader | resume：Leader 設 `running` + 重發 message |
| `running → verifying` | Leader | Worker 完成 → `send_message` 回報 → Leader 設 `verifying` |
| `verifying → completed` | Leader | `task_verify approve`（**Leader 可先 spawn 一個 general subagent 跑測試/review 做客觀驗收**） |
| `verifying → running` | Leader | `task_verify reject` + `send_message` 給 Worker 說明 rework |

> **角色與工具**
> Leader（可寫狀態）：`task_create / task_assign / task_update_status / task_verify / task_list / send_message / list_members / Agent(spawn)`
> Worker（唯讀任務）：`my_tasks / task_get + send_message / list_members + Agent(spawn) + 該領域實作工具`
> —— **每個 agent 都開著 `Agent(spawn)`**，用於任務內部一次性子代理（§2.2）。

### 7.2 Schema 草案

```sql
CREATE TABLE tasks (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  title       TEXT    NOT NULL,
  spec        TEXT    NOT NULL,                       -- 任務說明 + 驗收標準
  status      TEXT    NOT NULL DEFAULT 'pending',     -- pending|running|suspended|verifying|completed
  assignee    TEXT    NOT NULL,                        -- push：建立即指派
  created_by  TEXT    NOT NULL,                        -- 通常是 leader
  result      TEXT,                                   -- Leader 寫入待驗證的產出摘要
  verify_note TEXT,                                   -- Leader 的 approve/reject 理由
  parent_id   INTEGER,                                -- 任務拆解樹（選填）
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE messages (
  id        TEXT    PRIMARY KEY,        -- UUID；chan 只傳這個
  sender    TEXT    NOT NULL,
  recipient TEXT    NOT NULL,           -- agent 名 | 'all'
  subject   TEXT,
  body      TEXT    NOT NULL,
  ref_task  INTEGER,
  read_at   INTEGER,                    -- NULL = 未讀；drain 後標記
  created_at INTEGER NOT NULL
);
CREATE INDEX idx_msg_inbox ON messages(recipient, read_at, created_at);

-- 各 agent 自有 table：依約定命名 agent_<name>_<purpose>，由該 agent 自行 CREATE/讀寫。
```

### 7.3 並行模型（回應 RWMutex 設想）

push + Leader-only-writes 讓並行**大幅簡化**：

- **`tasks` 表 = 單一寫者（Leader）+ 多讀者（Workers、Web）** → **不需要原子認領**（無 pull、無搶單）。
- **`messages` 表 = 多寫者（所有 agent `send_message`）+ 多讀者** → 這才是 RWMutex 真正保護的地方。
- 實作：**單一 `*sql.DB` + `sync.RWMutex`** 包 DAO（寫 `Lock()`、讀 `RLock()`）；開 **WAL + `busy_timeout`**。
- ⚠️ **誠實限制**：SQLite 任一時刻只允許一個 writer，「各 agent 寫自己 table」在**寫入**仍序列化 —— RWMutex
  保證 Go 端**安全**，不是真平行。對 Veronica 負載沒問題。
- **「各 agent 只能開自己的 table」是邏輯約定**（SQLite 不強制）：每 agent 拿只暴露自己 table 的 DAO handle 軟性隔離。
- **共享讀域表**：跨 agent 報告場景（復盤 agent 讀交易 agent 的 PnL、風控讀倉位）需要「**部分域表明確共享可讀**」——trades/positions/pnl 設成共享讀、純私有 scratch 才完全私有；**寫入仍只有 owner**。
- 升級路：**single-writer actor**（一 goroutine 獨佔 DB、別人丟 op 進 channel），對齊「發訊息請求」模型。v1 用 RWMutex。

> 📌 Veronica task ledger ≠ evva 內建 `todo` store：後者 agent 私有/暫存/全綠自動收摺；前者**跨 agent、持久、
> User/Leader 可見、有驗證關卡**。別混 —— agent 仍可用 evva todo 做私人規劃。

---

## 8. Web 層（`:8888` + vue.js）

### 8.1 後端

- Go `net/http`（或輕量 router）開 `:8888`，host 多 swarm space。
- **事件出站**：每個 agent 掛一個 Veronica 寫的 `event.Sink`，把 `event.Event`（已 JSON-tag、`omitempty`）
  **依 swarm space + `AgentID` 分流**推到對應 WebSocket。`event.go` 註解早就預期「a JSON-over-websocket bridge」。
  扇出用 `event.Multi`（同時餵 Web + 落 log）。
- **指令入站**：瀏覽器經 WS/REST 打到對應 agent 的 `Controller`：
  - Leader 對話框送訊息 → `leader.Controller().Run(ctx, msg)`
  - 審批 permission → `Controller.RespondPermission(id, decision)`；回答問題 → `RespondQuestion(id, resp)`
  - 喊停任務 → Supervisor cancel 該 run；加入/冷藏成員 → `AddMember`/`FreezeMember`
- **REST 快照**：`GET /api/swarms`（space 列表）、`/api/swarm/:id`（roster）、`/api/tasks`（看板）、
  `/api/agents/:name/transcript`、`/api/messages` …

### 8.2 前端（vue.js SPA）

1. **swarm space 選單**：列出註冊進來的集群，進入某個 space。
2. **Team Board**：5 狀態 kanban（pending/running/suspended/verifying/completed）。
3. **Agent Roster**：成員 active/frozen + idle/busy/suspended、目前任務；提供「加入/冷藏」。
4. **Leader Chat**：User 與 Leader 主對話框（也是 permission/question 審批彈窗所在）。可點進任一 agent 看 transcript/信件。

> SPA build 後靜態檔用 `embed.FS` 打進 `evva` binary，單一執行檔分發。

### 8.3 安全性（v1 就正視）

agent 會跑 shell / 改檔案 = 等同 RCE。所以：

- **預設只綁 `127.0.0.1:8888`**，不對外；遠端走 SSH tunnel / 自加反代認證。
- 最少一個 **session token**（首啟產生、印在 terminal）擋同機其他程式亂連。
- **permission broker 接 Web UI**：危險操作前端跳審批由 User 點准（`Controller.RespondPermission`）。headless 全自動只在受信任沙箱。

---

## 9. 建構於 evva `pkg/*` vs 需動到 evva internal

### 9.1 ✅ 用現成 `pkg/*` 就能蓋（零 evva 改動）

| Veronica 需求 | 用 evva 的什麼 |
| --- | --- |
| 同 process 跑 N 個 agent | `agent.New(Config,...)`，「無全域單例」保證（`extending.md:128`） |
| 動態新增成員（hot-load） | runtime 再 `agent.New()` 一個、插進 roster |
| 每個 agent 任務內部 spawn 子代理（含 Leader 驗證） | evva 既有的 Agent(task) 工具，列入該 agent ActiveTools |
| 各 agent 獨立設定 | 各自 `*config.Config` + `CustomConfig` |
| 自家 persona 佈局 / 全自訂 prompt | in-code `AgentRegistry.Register(AgentDefinition{SystemPrompt:...})` |
| `send_message` / `list_members` / `task_*` 工具 | `pkg/tools.Tool` + `WithCustomTool` / `toolset.DefaultRegistry()` |
| 事件推到瀏覽器 | 自寫 `event.Sink`，`event.Event` 直接 JSON；`event.Multi` 扇出 |
| 驅動 / 審批 / 提問 | `Controller`：`Run / Continue / RespondPermission / RespondQuestion` |
| suspend/resume 任務 | 持有傳給 `Run` 的 `ctx`，cancel 即 suspend |
| 重啟接續 | 持久 task 帳本 + 未讀訊息 reload + `.vero/sessions/` 快照 + `Agent.ResumeSession` |
| per-agent skills | `skill.LoadRegistry()` + `WithSkillRegistry` |

### 9.2 🛠 需要動到 evva internal/loop 的（在同 repo 內做成公開 seam）

| # | 需求 | 為什麼 `pkg/*` 給不了 | 做法 |
| --- | --- | --- | --- |
| **②** | **loop 在 iteration boundary drain 外部 mailbox**（drain 階段 B） | loop 在 `internal/`、不可配置 | 推廣現有 `KindDrainBackgroundTask` 機制 → 在 `pkg/agent` 開**公開可插拔的 inbox-drainer seam**，loop 每輪 fold 它回傳的 synthetic message。同 repo → 加 seam + 用 seam 同一個 PR。**有現成 seam 可抄，成本低。** |

> 同 repo 後不再有「跨 repo 提需求」的儀式 —— 但**仍維持 §1.1 紀律**：swarm 用公開 seam，不私接 internal。
> 需求②是唯一動 loop 的點；它本身就該做成公開 seam（讓第三方多 agent host 也受惠）。

---

## 10. 在 evva repo 內的佈局（不另開 repo）

```
evva/
├── cmd/evva/
│   ├── main.go              # 既有：單 agent TUI（不變）+ 子命令分派
│   ├── service.go           # evva service start/stop/status
│   └── swarm.go             # evva swarm . （控制面 CLI：把 workdir 註冊進 service）
├── internal/
│   ├── swarm/               # ★ 子系統（Experimental）；對 agent 概念只 import pkg/*
│   │   ├── service.go       #   :8888 host：多 swarm space 管理
│   │   ├── supervisor.go    #   單一 swarm：啟停/動態加入/冷藏、roster、lifecycle
│   │   ├── scheduler.go     #   mailbox/任務 → Controller.Run 派工
│   │   ├── bus/             #   message bus + mailboxes（chan: msg-uuid）
│   │   ├── store/           #   .vero SQLite：tasks 狀態機 / messages / RWMutex DAO
│   │   ├── agentdef/        #   agents/{main,sub}/ 佈局 → pkg/agent.AgentDefinition + skill.Registry
│   │   ├── tools/           #   send_message / list_members / task_* （用 pkg/tools）
│   │   └── webapi/          #   REST + websocket；pkg/event.Sink 實作
│   └── ...                  # 既有 evva internal（swarm 不直接 import internal/agent）
├── pkg/agent/               # 唯一新增 = 需求② 的 inbox-drainer 公開 seam（Experimental，additive）
├── web/                     # vue.js 原始碼（vite）；build 後 embed
└── （pkg/* Stable 承諾不受影響）
```

> dep-check（CI）：斷言 `go list -deps ./internal/swarm/... | grep internal/agent` 為空（modulo 明列例外），
> 維持 swarm 的 multi-agent oracle 性質（§1.1）。

---

## 11. 分階段路線圖（finish before expand）

| 里程碑 | 目標（每階段都要能跑起來看到東西） | 依賴 |
| --- | --- | --- |
| **M0 — Walking skeleton** | `evva service start` 起 :8888；`evva swarm .` 註冊一個 leader+1 worker 的 space；開 `.vero/vero.db`；Web 出極簡頁顯示成員在線。**證明「service host 多 root agent + Web sink」主幹通。** | 純 `pkg/*` |
| **M1 — 共享任務帳本 + roster** | tasks 表 + 5 狀態機（Leader-only writes）+ `task_*`/`list_members`；Web Team Board + Agent Roster。Leader 建/指派/驗證；Worker 唯讀+回報。 | 純 `pkg/*` |
| **M2 — 訊息協作（drain 階段 A）** | `send_message` + mailbox（chan 傳 uuid、訊息落 SQL、標已讀）+ Supervisor「有信就 Run」；Web 顯示信件。會互相發信 cowork 的團隊成形。 | 純 `pkg/*` |
| **M3 — manifest + 動態成員 + 重啟接續** | `evva-swarm.yml` 完整解析；`evva swarm add` / Web 動態加入 + 冷藏；suspend/resume；未讀訊息 reload + `ResumeSession`；驗證 spawn general subagent；permission 審批接 Web。 | 純 `pkg/*` |
| **M4 — 即時收信（drain 階段 B）** | 在 `pkg/agent` 開 inbox-drainer 公開 seam + swarm 接上，忙碌中也能即時 fold 訊息。 | **需求②（同 repo）** |
| **M5+ — 打磨 / 演進** | vue UI 體驗、各 agent transcript 視圖、binary embed、安全強化；視需要從 process 模型 A → C（§4.2）。 | — |

> M0–M3 **完全不需要改 evva 既有程式**就能做出可用 swarm。M4 才在 `pkg/agent` 加一個 additive 公開 seam。

---

## 12. 待決問題

### ✅ 已定案（2026-06-02）
1. **不拆專案** → folding 進 evva：`evva service` / `evva swarm` / `evva-swarm.yml`（§1、§4）。
2. **process 模型** → **v1 採 A**（service host 一切），C（每 swarm 一 process）當演進路；bus 永遠在 swarm process 內（§4.2）。✅ Johnny 2026-06-02 確認。
3. **swarm 維持 pkg-pure**（multi-agent oracle）+ dep-check 強制（§1.1）。【我的建議】
4. **agent 佈局** → `agents/{main,sub}/` + 自寫 loader（§4.5）。
5. **Leader/Worker prompt** → **全自訂**，不沿用 evva coding persona；Leader 著重團隊管理（§4.5）。
6. **派工** → push，**只有 Leader 能改 task 狀態**；Worker 唯讀 + 回報（§7.1）。
7. **訊息持久化** → 落 SQL；chan 只傳 msg-uuid；drain 回查/標已讀/標來源；支援重啟接續（§6.2）。
8. **Worker 喚醒** → event-driven（§5.1）。
9. **同名/replicas** → 不支援；要多個就取不同名（engineer-1/2）（§4.4）。
10. **動態加入觸發者** → v1 僅 User（CLI/Web），不給 Leader `hire_member`（§5.4）。
11. **驗證** → 每個 agent 都可 spawn；`verifying` 時 Leader 可 spawn general subagent 跑測試/客觀驗收（§7.1）。

### ✅ 已定案（追加 2026-06-02）
12. **CLAUDE.md 已更新**：新增 Core direction 區塊 + Roadmap PAUSED 橫幅 + Teams out-of-scope 收窄為「僅跨機」。
13. **多 swarm space = 核心能力**：一個 service host 多個互相隔離的 space（子集團），**從 M0 就是 multi-space host**（§3.1）。**不採「v1 先單 space」**。

### ⏳ 待 Johnny 拍板
（目前無重大待決項；其餘為各里程碑開工時的實作細節。）

---

## 13. 一頁總結

- **Veronica = evva 的 swarm 子系統**：`evva service`（背景 :8888）host 一/多個 swarm space，`evva swarm .` 註冊集群。
- **團隊用 mesh + bus 組（不用 subagent spawn）**；但**每個 root agent 仍開著 spawn**，用在任務內部（含 Leader 驗收）。
- **v1 單 process（模型 A）**，bus/SQLite/RWMutex 全在 process 內；crash 隔離需求再演進到模型 C，bus 仍不出 process。
- **push 派工、Leader 獨佔 task 狀態寫入** → tasks 表單一寫者、無搶單；RWMutex 重點在 messages 表。
- **訊息 DB 為真相、chan 只傳 uuid、標已讀** → 天生可回看、可重啟接續。
- **roster + 動態成員（冷藏不刪除）、leader/worker 全自訂 prompt** —— 全靠 evva「同 process 多 agent」現成能力。
- **M0–M3 純靠現成 `pkg/*`**；唯一動 evva 的是需求②（loop 內 drain），做成 `pkg/agent` 的公開 seam。
- **紀律**：swarm 只消費 `pkg/*` → 成為 evva 的**多 agent completeness oracle**。evva 管「單 agent 怎麼想」，swarm 管「一群 agent 怎麼一起工作」。
```
