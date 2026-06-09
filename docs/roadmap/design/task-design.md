# Task Design — Todo-v2

> Status: deferred；todo-v1 仍可用，daemon refactor 優先。
> 本文只記錄「為什麼要做」與「目標形狀」，不細寫實作。
> 硬依賴：[`daemon-design.md`](daemon-design.md) 必須先落地，把 `task_*`
> 命名空間從現有 daemon 工具收走。

---

## 1. 為什麼要取代 todo-v1

todo-v1 是一個 `TodoWrite` 工具 + flat list（每筆 `{content, status, activeForm}`）。
夠用，但有幾個結構性問題：

- **沒有依賴語意**：refactor / multi-step migration 常需要「B 阻擋 A」表達，
  v1 只能靠人類自律。
- **沒有 owner**：未來 evva → nono 跨 persona 委派時無法表達「這 task 誰在做」。
- **無法 resume**：v1 是 in-memory transient；agent 退出即遺失。
- **沒有歷史**：「為什麼這 task 從 in_progress 退回 pending」沒有蛛絲馬跡。
- **整批覆寫易失誤**：model 漏寫一筆 = 該筆遺失。增量 CRUD 更穩。

---

## 2. 目標形狀（對齊 ref/Claude Code）

四個工具：

| 工具 | 用途 |
|---|---|
| `TaskCreate` | 新增 `{subject, description, activeForm, blockedBy?, metadata?}` |
| `TaskUpdate` | 改 status / owner / blockedBy / append comment |
| `TaskList`   | 列出全部，可 filter status / owner |
| `TaskGet`    | 取單一 task 完整內容（含 comments） |

Task schema：

```go
type Task struct {
    ID          string           // base36
    Subject     string           // 一行標題
    Description string           // markdown
    ActiveForm  string           // in_progress 時的 spinner 文字
    Status      TaskStatus       // pending | in_progress | completed
    Owner       string           // agent / teammate id；空 = 未認領
    Blocks      []string         // 阻擋哪些 task
    BlockedBy   []string         // 被哪些 task 阻擋
    Comments    []TaskComment    // append-only 變更紀錄
    Metadata    map[string]any   // 自由欄位（PR url、issue id 等）
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

Status 只有三檔（對齊 v1，避免 model 困惑）；blocked 由 `BlockedBy` 推導，
不是獨立狀態。

落地路徑：`<EVVA_HOME>/task-lists/<id>.jsonl`，append-only；resume 時 replay
重建狀態。

---

## 3. 改善方向（v1 → v2）

| 主題 | v1 | v2 |
|---|---|---|
| 表達 | 整批覆寫 | 增量 CRUD |
| 依賴 | 無 | `blockedBy` / `blocks` |
| 認領 | 無 | `owner` |
| 歷史 | 無 | `comments` append-only |
| 持久化 | in-memory | jsonl on disk |
| Resume | 遺失 | replay 重建 |
| Hook 整合 | 無 | `TaskCreated` / `TaskUpdated`（對齊 ref `executeTaskCreatedHooks`） |

---

## 4. 暫不做

- Teammate / Swarm 搶 task 模式（ref 的 `agentSwarmsEnabled`）— evva 還沒有
  second agent process。
- `TaskDelete` — 一律走 `TaskUpdate(status=...)`。
- TUI 重寫 — 沿用 v1 的 panel layout，只換後端。

---

## 5. 參考

- `ref/src/tools/TaskCreateTool/` / `TaskUpdateTool/` / `TaskListTool/` / `TaskGetTool/`
- `ref/src/utils/tasks.ts` — schema + 磁碟 storage helpers
- `ref/src/utils/hooks.ts` — `executeTaskCreatedHooks`
