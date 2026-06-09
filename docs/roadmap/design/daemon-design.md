# Daemon Design

> Status: RFC / committed direction
> Scope: 把現有「bash 背景 / monitor / subagent」三個獨立 store 砍掉，全部統一在
> 一個 `Daemon` 抽象 + 一個 `DaemonState` 之下。命名動機：對齊 ref/Claude Code 的
> 「Task」概念，但更名為 `daemon` 把 `task_*` 命名空間留給未來的 todo-v2
> （見 [`task-design.md`](task-design.md)）。
>
> 本文是 clean-sheet 設計，不保留任何舊形狀。`BgTaskStore`、`MonitorTaskStore`、
> `SpawnGroup`、`task_list/stop/output` 都會被刪除。

---

## 1. Problem（為什麼非重做不可）

現況有三個 **語意相同、實作各異** 的「背景單元」管理系統：

| Source | Store | ID prefix | Host interface |
|---|---|---|---|
| `bash run_in_background:true` | `shell.BgTaskStore` | `b…` | `shell.BgTaskHost` |
| `monitor` 工具 | `monitor.MonitorTaskStore` | `m…` | `monitor.MonitorHost` |
| subagent（async 路徑） | `meta.SpawnGroup` | agent id (`agent_…`) | 直接讀 `ToolState.AgentGroup()` |

三套各自有：observable domain、drain helper、TUI strip wiring、cancel 機制。
唯一的「跨 store 工具」`task_stop` 卻只看 `BgTaskStore`（`pkg/tools/shell/task_tools.go:178-200`），
於是：

- `task_stop("m…")` → not found → agent 改去 `bash kill <pid>`，繞過 monitor 的
  cancel→cleanup→Complete(Stopped) 流程。
- async subagent 沒有任何 stop 工具可用。
- 每新增一種背景單元就要 (store + host + drain + strip + tool dispatch) 五處
  動手，複雜度線性增長。

---

## 2. 設計原則（不可妥協）

1. **單一抽象** — Daemon 是「任何長時間運行的背景單元」的 polymorphic interface。
2. **單一狀態** — 每個 Agent 持有一個 `*DaemonState`。沒有其他平行 store。
3. **單一 signal queue** — Lifecycle 與 stream events 都走同一條 `Signals []Signal`。
4. **單一 drain** — agent loop 每 iter 一次 `drainDaemonSignals()`。
5. **單一 observable domain** — `"daemons"`；TUI 任何 strip 都訂閱它（前端各自 filter kind）。
6. **新增 kind = 新增一個 file** — 實作 `Daemon` interface、註冊一個 kind constant，
   工具 / drain / TUI 全部零改動。

---

## 3. 抽象核心

### 3.1 Kind 與 Status

```go
// pkg/tools/daemon/kind.go
package daemon

type DaemonKind string

const (
    KindLocalBash  DaemonKind = "local_bash"   // 本次實作
    KindLocalAgent DaemonKind = "local_agent"  // 本次實作（含 sync + async）
    KindMonitor    DaemonKind = "monitor"      // 本次實作

    // 預留，enum 先列上避免之後反覆改 schema
    KindRemoteAgent       DaemonKind = "remote_agent"
    KindInProcessTeammate DaemonKind = "in_process_teammate"
    KindLocalWorkflow     DaemonKind = "local_workflow"
    KindDream             DaemonKind = "dream"
)

// ID prefix — 對齊 ref 慣例（b/a/m/r/t/w/d）+ 8 char base36。
func GenerateID(kind DaemonKind) string
```

```go
// pkg/tools/daemon/status.go
type DaemonStatus string

const (
    StatusPending   DaemonStatus = "pending"
    StatusRunning   DaemonStatus = "running"
    StatusCompleted DaemonStatus = "completed"
    StatusFailed    DaemonStatus = "failed"
    StatusKilled    DaemonStatus = "killed"
)

func IsTerminal(s DaemonStatus) bool
```

### 3.2 Snapshot + Metadata

```go
// pkg/tools/daemon/snapshot.go
type DaemonSnapshot struct {
    ID          string
    Kind        DaemonKind
    Status      DaemonStatus
    Description string
    AgentID     string         // 哪個 agent 起的
    StartedAt   time.Time
    EndedAt     time.Time      // zero 直到 terminal

    Metadata    DaemonMetadata // kind-specific，type-assert 取值
}

type DaemonMetadata interface{ daemonMetadata() }

// 三個 concrete metadata（每個 kind 一個檔）
type LocalBashMeta struct {
    Command  string
    ExitCode *int   // nil while running
}
type LocalAgentMeta struct {
    AgentType string
    Prompt    string
    Async     bool
    Summary   string  // 終結時填
}
type MonitorMeta struct {
    Command    string
    EventCount int
    Persistent bool
}
```

### 3.3 Daemon interface

```go
// pkg/tools/daemon/daemon.go
type Daemon interface {
    Snapshot() DaemonSnapshot       // copy，無鎖讀
    Kill(ctx context.Context) error // 多型中止
    Output() string                 // daemon_output 取得格式化文字
}
```

三個方法就是全部。Daemon 內部可以有任意 state；對 store / tool / drain 只露
這三個入口。

### 3.4 Signal

```go
// pkg/tools/daemon/signal.go
type Signal struct {
    DaemonID string
    Kind     DaemonKind
    At       time.Time
    Snapshot DaemonSnapshot   // 訊號當下的完整 snapshot

    // 二選一 — Go 沒有 sum type，用兩個 pointer 表達 variance
    Lifecycle *Lifecycle
    Event     *Event
}

type Lifecycle struct {
    Status DaemonStatus       // 進入此 status；只在 Transition 時發
}

type Event struct {
    Line    string             // 一行 stdout（monitor 主用）
    Closing bool                // process exit 前最後一個 event
}

// 建構 helper
func NewLifecycleSignal(d Daemon, status DaemonStatus) Signal
func NewEventSignal(d Daemon, line string, closing bool) Signal
```

### 3.5 DaemonState

唯一的狀態持有者。**取代 `BgTaskStore` + `MonitorTaskStore` + `SpawnGroup`。**

```go
// pkg/tools/daemon/state.go
type DaemonState struct {
    mu      sync.RWMutex
    daemons map[string]Daemon
    signals []Signal           // pending，drain 後清空

    *observable.Observable     // domain = "daemons"
    notify  func()             // 喚醒 agent loop（signal pump）
}

const Domain = "daemons"

func NewState(notify func()) *DaemonState

// ── CRUD ──────────────────────────────────────────────
func (s *DaemonState) Register(d Daemon)                       // + observable "added"
func (s *DaemonState) Evict(id string)                         // + observable "removed"
func (s *DaemonState) Get(id string) (Daemon, bool)
func (s *DaemonState) Snapshot() []DaemonSnapshot              // 排序：StartedAt
func (s *DaemonState) SnapshotByKind(k DaemonKind) []DaemonSnapshot

// ── 中止 ───────────────────────────────────────────────
// daemon_stop 的入口。lookup → daemon.Kill → 由 daemon goroutine 自己走 Emit
func (s *DaemonState) Stop(ctx context.Context, id string) (DaemonSnapshot, bool, error)

// ── Signal pump（daemon goroutine 呼叫）─────────────────
func (s *DaemonState) Emit(sig Signal)                         // queue + observable + notify

// ── Drain（agent loop 呼叫）─────────────────────────────
func (s *DaemonState) DrainSignals() []Signal                  // 全拉走、清空
func (s *DaemonState) HasPending() bool
```

`Emit` 是熱路徑，每個 daemon 的 goroutine 呼叫它：

1. 把 signal 加進 `signals`（mu.Lock）
2. `Notify(observable.Change{Domain: "daemons", Op: ..., Payload: snap})` → TUI 立刻收到
3. `notify()` → 喚醒 agent signal pump（若 idle）

Agent loop 每 iter 起始呼叫 `DrainSignals()`，依 Lifecycle vs Event 折成
`<system-reminder>` 注入下一輪 prompt（細節見 §6）。

### 3.6 為什麼不需要 Host interface

舊架構每個 store 配一個 `*Host` interface（`BgTaskHost` / `MonitorHost`）。
Clean-sheet 設計裡，工具直接吃 `*DaemonState`：

```go
type DaemonListTool   struct{ state *DaemonState }
type DaemonStopTool   struct{ state *DaemonState }
type DaemonOutputTool struct{ state *DaemonState }
```

`ToolState` 暴露單一 `DaemonState()` 方法即可。Host interface 在「狀態本身就是
single source of truth」的前提下是多餘間接層。

---

## 4. 三個 kind 的 daemon 實作

每個 kind 一個 `.go` 檔，全部實作 `Daemon` interface。建構 daemon 時注入
`*DaemonState`（用來 Emit）。

### 4.1 `bashDaemon`（`pkg/tools/shell/bash_daemon.go`）

```go
type bashDaemon struct {
    mu      sync.Mutex
    snap    DaemonSnapshot
    meta    LocalBashMeta
    cancel  context.CancelFunc
    cmd     *exec.Cmd
    outBuf  *tailBuffer    // 64 KiB ring of stdout+stderr

    state *daemon.DaemonState
}

func newBashDaemon(parentCtx context.Context, state *DaemonState,
                   command, description, agentID string) *bashDaemon

func (d *bashDaemon) Snapshot() DaemonSnapshot { /* lock + copy */ }

func (d *bashDaemon) Kill(_ context.Context) error {
    d.cancel()    // ctx cancellation → process group SIGTERM (+ grace SIGKILL)
    return nil
}

func (d *bashDaemon) Output() string {
    snap := d.Snapshot()
    meta := snap.Metadata.(LocalBashMeta)
    head := fmt.Sprintf("daemon %s [%s/%s] exit=%v", snap.ID, snap.Kind, snap.Status, meta.ExitCode)
    return head + "\n---\n" + d.outBuf.String()
}

// goroutine：執行命令，process exit 後 Emit Lifecycle
func (d *bashDaemon) run(ctx context.Context) {
    err := d.cmd.Run()
    status := classifyExit(err, ctx)            // Completed | Failed | Killed
    d.setTerminal(status, exitCodeOf(err))
    d.state.Emit(daemon.NewLifecycleSignal(d, status))
}
```

注意 bash 在這個設計裡 **不發 Event signals** — bash 的 stdout 落到 outBuf
等 `daemon_output` 來讀就好；agent 不需要 line-by-line 通知（那是 monitor 的職責）。

### 4.2 `monitorDaemon`（`pkg/tools/monitor/monitor_daemon.go`）

```go
type monitorDaemon struct {
    mu      sync.Mutex
    snap    DaemonSnapshot
    meta    MonitorMeta
    cancel  context.CancelFunc
    cmd     *exec.Cmd
    ring    *eventRing      // 最近 N 條 events，給 daemon_output

    state *daemon.DaemonState
}

func (d *monitorDaemon) Kill(_ context.Context) error { d.cancel(); return nil }

func (d *monitorDaemon) Output() string {
    snap := d.Snapshot()
    meta := snap.Metadata.(MonitorMeta)
    head := fmt.Sprintf("daemon %s [%s/%s] events=%d", snap.ID, snap.Kind, snap.Status, meta.EventCount)
    return head + "\n---\n" + d.ring.JoinTail()  // 最近 N 條
}

func (d *monitorDaemon) run(ctx context.Context) {
    scanner := bufio.NewScanner(d.stdout)
    for scanner.Scan() {
        line := scanner.Text()
        d.appendEvent(line)
        d.state.Emit(daemon.NewEventSignal(d, line, false))
    }
    status := classifyMonitorExit(ctx, d.cmd.Wait())
    d.setTerminal(status)
    d.state.Emit(daemon.NewEventSignal(d, "", true))     // closing event
    d.state.Emit(daemon.NewLifecycleSignal(d, status))
}
```

### 4.3 `agentDaemon`（`internal/agent/agent_daemon.go`）

```go
type agentDaemon struct {
    mu     sync.Mutex
    snap   daemon.DaemonSnapshot
    meta   daemon.LocalAgentMeta
    child  *Agent
    abort  func()                 // 對 child 發 KindAbort（cooperative cancel）

    state *daemon.DaemonState
}

func (d *agentDaemon) Kill(_ context.Context) error {
    d.abort()   // child loop 在下一輪 iter 看到 abort signal 退出
    return nil
}

func (d *agentDaemon) Output() string {
    snap := d.Snapshot()
    meta := snap.Metadata.(daemon.LocalAgentMeta)
    head := fmt.Sprintf("daemon %s [%s/%s] type=%s async=%v",
                        snap.ID, snap.Kind, snap.Status, meta.AgentType, meta.Async)
    body := meta.Summary
    if body == "" && meta.Prompt != "" {
        body = "prompt: " + truncate(meta.Prompt, 500)
    }
    return head + "\n---\n" + body
}

// Sync + Async 共用同一個 run；差別只在 caller 是否阻塞等回傳值
func (d *agentDaemon) run(ctx context.Context, prompt string) (string, error) {
    resp, err := d.child.Run(ctx, prompt)
    status, summary := classifyAgentExit(err, resp)
    d.setTerminal(status, summary)
    d.state.Emit(daemon.NewLifecycleSignal(d, status))
    return resp, err
}
```

Sync subagent 的呼叫端：

```go
// internal/agent/spawn.go（重寫後）
ad := newAgentDaemon(state, child, prompt, false /* async */)
state.Register(ad)
resp, err := ad.run(ctx, prompt)
state.Evict(ad.ID())            // sync 短命，立刻 evict
return resp, err
```

Async：

```go
ad := newAgentDaemon(state, child, prompt, true /* async */)
state.Register(ad)
go func() {
    _, _ = ad.run(ctx, prompt)  // Emit Lifecycle 之後 evict 由 drain 處理
}()
return fmt.Sprintf("subagent %s spawned", ad.ID()), nil
```

---

## 5. 工具層

三個工具，全部在 `pkg/tools/daemon/tools.go`，schema 共用簡單。

### 5.1 `daemon_list`

```jsonc
{
  "type":"object",
  "additionalProperties":false,
  "properties":{
    "kind":{
      "type":"string",
      "enum":["local_bash","local_agent","monitor"],
      "description":"Optional filter."
    },
    "include_terminal":{
      "type":"boolean", "default": false,
      "description":"Include completed/failed/killed daemons."
    }
  }
}
```

輸出每行格式：

```
<id> [<kind>/<status>] <description> started=<iso8601> (extras...)
```

`extras` 依 kind 帶：bash 帶 `exit=N`、monitor 帶 `events=N`、agent 帶 `type=…`。

### 5.2 `daemon_stop`

```jsonc
{
  "type":"object",
  "additionalProperties":false,
  "required":["daemon_id"],
  "properties":{
    "daemon_id":{"type":"string","description":"Daemon ID (b… / a… / m…)."}
  }
}
```

行為：

1. `state.Get(id)` — not found → IsError。
2. `IsTerminal(snapshot.Status)` → no-op 訊息。
3. `daemon.Kill(ctx)` — 同步呼叫；具體中止由 kind 實作決定（cancel ctx、agent abort signal …）。
4. 回 `daemon_stop: <id> terminating; you will receive a killed signal when it exits.`

實際的 status 變更是 daemon 自己的 goroutine 透過 `Emit(NewLifecycleSignal)` 完成
—— 跟自然結束走同一條路徑。

### 5.3 `daemon_output`

```jsonc
{
  "type":"object",
  "additionalProperties":false,
  "required":["daemon_id"],
  "properties":{
    "daemon_id":{"type":"string"},
    "tail":{"type":"number","minimum":1,"description":"Last N lines only."}
  }
}
```

```go
d, ok := state.Get(id)
text := d.Output()    // 多型；kind-specific 格式化已在 daemon 內處理
if tail != nil { text = tailLines(text, *tail) }
return tools.Result{Content: text}
```

---

## 6. Drain — Agent loop 整合

```go
// internal/agent/drain_daemons.go
func (a *Agent) drainDaemonSignals() string {
    signals := a.daemonState.DrainSignals()
    if len(signals) == 0 {
        return ""
    }

    // 同一 daemon 的多筆 events 折成一段；lifecycle 各自一段。
    var sb strings.Builder
    eventBuckets := map[string][]Signal{} // daemonID → events
    var lifecycles []Signal

    for _, sig := range signals {
        switch {
        case sig.Lifecycle != nil:
            lifecycles = append(lifecycles, sig)
        case sig.Event != nil:
            eventBuckets[sig.DaemonID] = append(eventBuckets[sig.DaemonID], sig)
        }
    }

    for id, evs := range eventBuckets {
        kind := evs[0].Kind
        fmt.Fprintf(&sb, "<system-reminder>daemon %s [%s] events:\n", id, kind)
        for _, ev := range evs {
            if ev.Event.Closing { continue }
            fmt.Fprintf(&sb, "%s\n", ev.Event.Line)
        }
        sb.WriteString("</system-reminder>\n")
    }

    for _, sig := range lifecycles {
        fmt.Fprintf(&sb, "<system-reminder>daemon %s [%s] %s: %s</system-reminder>\n",
                    sig.DaemonID, sig.Kind, sig.Lifecycle.Status, sig.Snapshot.Description)
    }

    // Terminal lifecycle 的 daemon 從 state 中 evict（lifecycle 已送進 prompt）
    for _, sig := range lifecycles {
        if daemon.IsTerminal(sig.Lifecycle.Status) {
            a.daemonState.Evict(sig.DaemonID)
        }
    }

    return sb.String()
}
```

每次 agent loop iter 起始呼叫，回傳值作為 `<system-reminder>` user message 注入。
Iter 結束時若 `daemonState.HasPending()` 為 true → 立刻 re-iter（不出去等 LLM
新 prompt）。

---

## 7. TUI

三條 strip 視覺保留（bg-tasks / monitor / subagent 各一條），全部訂閱
`daemon.Domain = "daemons"`，但前端各自 filter `Kind`：

```go
// internal/ui/bubbletea_v2/components/bg_tasks/strip.go
sub := observable.Subscribe(daemonState, func(c observable.Change) {
    snap := c.Payload.(DaemonSnapshot)
    if snap.Kind != daemon.KindLocalBash { return }
    ...
})

// monitor strip — filter Kind == KindMonitor
// agents strip   — filter Kind == KindLocalAgent
```

`Op` 來自 observable：`"added"` / `"removed"` / `"transition:<status>"` / `"event"`。
每個 strip 自己定義對 op 的反應。

`SpawnGroup` 整個刪除；subagent strip 改成 `DaemonState` 的 view，把
`Metadata.(LocalAgentMeta)` 解出 phase / prompt / summary 等做 render。

---

## 8. Wiring（誰持有誰）

```
*Agent
  └── ToolState (per-agent)
        └── daemonState *daemon.DaemonState     ← 唯一狀態
              ├── daemons map[id]Daemon
              ├── signals []Signal              ← 統一 queue
              └── Observable (domain "daemons") ← TUI fan-out
```

`*daemon.DaemonState` 由 `NewState(notify func())` 構造，`notify` 就是 agent 的
signal pump 喚醒函式（現有 `signal.go` 已有）。

工具持有 `*DaemonState` 即可，不需要 host interface。

---

## 9. Migration Plan（要刪什麼、要加什麼）

### Phase D0 — 新 package（純加，不刪）

`pkg/tools/daemon/`：
- `kind.go` / `status.go` / `snapshot.go` / `daemon.go` / `signal.go` / `state.go`
- `state_test.go` — Register / Get / Stop / Emit / DrainSignals / observable fan-out

驗收：`go test ./pkg/tools/daemon/...` 全綠。

### Phase D1 — 工具與 ToolState

- 新檔 `pkg/tools/daemon/tools.go`：`DaemonListTool` / `DaemonStopTool` / `DaemonOutputTool`
- `pkg/tools/tools.go`：加 `DAEMON_LIST` / `DAEMON_STOP` / `DAEMON_OUTPUT` 常數
- `internal/toolset/toolset.go`：加 `daemonState *daemon.DaemonState`、`DaemonState()` 方法
- `internal/toolset/builtins.go`：daemon_* factory wiring
- `internal/agent/profiles.go`：deferred tools 表中加三個 daemon_*

此階段三個工具能跑但 state 還沒有 daemon 進駐。

### Phase D2 — 砍掉 bash 舊路徑、實作 `bashDaemon`

**刪除：**
- `pkg/tools/shell/bgtask.go` 整檔
- `pkg/tools/shell/task_tools.go` 整檔
- `internal/toolset/toolset.go` 的 `bgTaskStore` 欄位、`BgTaskStore()` / `HasBgTaskStore()` 方法
- `pkg/tools/shell/` 中所有 `BgTaskHost` 參照

**新增：**
- `pkg/tools/shell/bash_daemon.go` — `bashDaemon` 實作 `Daemon`
- 改寫 `pkg/tools/shell/bash.go` 的 `run_in_background:true` 路徑為
  `state.Register(newBashDaemon(...))` + `go d.run()`

測試遷移：`pkg/tools/shell/bash_bg_test.go` 改 assert on `DaemonState`。
`bgtask_test.go` 改名 `bash_daemon_test.go` 並重寫。

### Phase D3 — 砍掉 monitor 舊路徑、實作 `monitorDaemon`

**刪除：**
- `pkg/tools/monitor/store.go` 整檔（`MonitorTaskStore` / `MonitorEventQueue` /
  `MonitorTask` / `MonitorSnapshot` / `MonitorHost`）
- `internal/toolset/toolset.go` 的 `monitorTaskStore` + `monitorEventQueue` 欄位、
  相關 method group

**新增：**
- `pkg/tools/monitor/monitor_daemon.go` — `monitorDaemon` 實作 `Daemon`
- 改寫 `pkg/tools/monitor/monitor.go`：spawn 路徑改成
  `state.Register(newMonitorDaemon(...))` + `go d.run()`

`MonitorEvent` 概念被 `daemon.Signal` 的 `Event` variant 取代 — 整個型別刪掉。
測試遷移：`monitor_test.go` 對齊新介面。

### Phase D4 — 砍掉 SpawnGroup、實作 `agentDaemon`

**刪除：**
- `internal/tools/meta/agent.go` 的 `SpawnGroup`、`SubagentSnapshot`、
  `spawnedAgent` 等型別與相關 method
- `internal/toolset/toolset.go` 的 `AgentGroup()` / `agentGroup` 欄位

**新增：**
- `internal/agent/agent_daemon.go` — `agentDaemon` 實作 `Daemon`
- 改寫 `internal/agent/spawn.go`：sync + async 路徑都用 `agentDaemon`
  - sync：`Register → run → Evict`，回傳 child response
  - async：`Register → go run()`（drain helper 看到 terminal lifecycle 後 evict）

`group.Add / Status / Crush / Report / Remove` 全部消失 — phase / summary 等
metadata 直接寫進 `agentDaemon.snap.Metadata.(LocalAgentMeta)`，TUI 從 observable
拿。

### Phase D5 — Drain helper 統一

**刪除：**
- `internal/agent/drain_signals.go` 的 `drainBackgroundTaskResult` 與
  `drainMonitorEvents`（保留檔，內容重寫）

**新增：**
- `internal/agent/drain_daemons.go`（或在原檔內）`drainDaemonSignals()`

Agent loop 的 iter 起始與 re-iter check 改成只認 `daemonState.HasPending()`。

### Phase D6 — TUI strips 改訂閱

- `internal/ui/bubbletea_v2/components/bg_tasks/`：改訂閱 `daemon.Domain`，
  filter `KindLocalBash`
- `internal/ui/bubbletea_v2/components/monitor/`：filter `KindMonitor`
- `internal/ui/bubbletea_v2/components/agents/`：filter `KindLocalAgent`，從
  `Metadata.(LocalAgentMeta)` 取 phase / prompt / summary

三個 strip 的訂閱程式碼長相應該幾乎一致 — 抽一個 helper `subscribeDaemonsByKind`。

### Phase D7 — sysprompt

- `internal/agent/sysprompt/toolnames.go`：加 `daemon_list / stop / output`，
  移除 `task_list / stop / output`
- `docs/sys-prompt/` 對應檔同步更新
- main_agent + general-purpose 兩個 persona 的系統 prompt 中對舊 task_* 的提示
  全部換成 daemon_*，並補上 cross-kind 說明

---

## 10. 驗收

- [ ] `go test ./...` 全綠（新增的 daemon package 測試 + 三個 kind 的 daemon 測試）
- [ ] `daemon_list` 同時列出 bash / monitor / subagent
- [ ] `daemon_stop("m…")` 正確中止 monitor（agent 不再 fallback `bash kill`）
- [ ] `daemon_stop("a…")` 正確中止 async subagent（child 退出、drain 折出
      `<system-reminder>`）
- [ ] `daemon_output` 對三種 kind 都能回 sensible 內容
- [ ] 以下 symbol 在整個 codebase 中已不存在：`BgTaskStore`、`MonitorTaskStore`、
      `SpawnGroup`、`task_list`、`task_stop`、`task_output`、`BgTaskHost`、`MonitorHost`
- [ ] TUI 三條 strip 仍能正確 render（bg-tasks / monitor / subagent）
- [ ] 預留 kind（`remote_agent` / `in_process_teammate` / `local_workflow` / `dream`）
      已在 `kind.go` 列出但未實作 daemon，新增時零改動工具與 drain

---

## 11. 預留 kind 怎麼上線

未來新增任何一種 daemon（例 `dream`）：

1. `pkg/tools/daemon/snapshot.go` 加 `DreamMeta` struct 實作 `daemonMetadata()`
2. 新檔 `internal/agent/dream_daemon.go` 實作 `Daemon` interface
3. 上層工具呼叫 `state.Register(newDreamDaemon(...))`
4. （選擇性）TUI 加一條 strip 訂閱 `KindDream`

`daemon_list / stop / output`、drain、signal pump — 零行改動。這是 clean-sheet
設計唯一要保證的事。

---

## 12. 參考

- ref/ 對應：
  - `ref/src/Task.ts`（kind enum / status / base state / ID 規則）
  - `ref/src/tasks.ts`（`getTaskByType` dispatch）
  - `ref/src/tasks/stopTask.ts`（多型 stop）
  - `ref/src/tasks/LocalShellTask/`、`LocalAgentTask/`（kind 實作範本）
  - `ref/src/utils/task/framework.ts`（register / poll / drain pattern）
