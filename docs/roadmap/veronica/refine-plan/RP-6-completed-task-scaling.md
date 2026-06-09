# RP-6 — Completed-task 規模化：Leader 查詢分頁 ＋ Web UI 收斂

> 狀態：**草案 / Draft（待 Johnny 拍板）** ｜ 日期：2026-06-06 ｜ 階段：**Phase 1**
> 觸發：長時間運行後 `completed` 任務堆積 →（A）leader 每次查 ledger 都把整串灌進上下文；
> （B）Web 看板 completed 欄變成一面「已完成卡牆」。
> 上層設計：[`../veronica-design-v1.md`](../veronica-design-v1.md) ｜ 關聯：[RP-4](RP-4-web-ui-ux.md)（看板/分頁）

---

## 1. TL;DR

一個 swarm 跑得久，`completed` 是**單調累積的終態**——會越堆越多、永不縮減。但目前
**查詢沒有任何上限或分頁**：

- **Leader 上下文膨脹（🔴 核心）**：`task_list` 把符合條件的**所有**任務（含全部 completed）
  逐筆格式化塞進工具結果，而 leader 會反覆查 ledger → 每次 poll 都把同一坨 completed
  再灌一次，上下文線性膨脹。
- **Web 看板擁擠（🟡）**：看板 completed 欄渲染**全部**已完成卡片，越跑越長。

> **方向**：在 **store 層**加一個共用的「分頁 ＋ 計數」原語，讓兩邊都吃它：
> ① Leader 的 `task_list` 預設**只回最近 N 筆 + 總數**，並提供 `offset` 做**漸進 reload
> （分頁查詢）**，尤其針對 completed；② Web 看板 completed 欄**只顯示最近 5 筆**，另開
> 一個 **Completed 分頁**讓 User 翻頁查歷史。

---

## 2. 現況盤點（file:line 證據）

| # | 問題 | 嚴重度 | 證據 |
| --- | --- | --- | --- |
| C1 | `ListTasks` **無 LIMIT/OFFSET**，回傳所有符合列，`ORDER BY id`（最舊在前） | 🔴 | `internal/swarm/store/tasks.go:167`；`q += " ORDER BY id"`（`:185`） |
| C2 | `TaskFilter` 只有 `Status`/`Assignee`，無分頁欄位 | 🔴 | `store/tasks.go:55-59` |
| C3 | `task_list` 把整串逐筆灌進結果（title+spec+result+note） | 🔴 | `tools/tasks.go:225` `newTaskList` → `formatTasks`（`:50`） |
| C4 | 沒有「總數 / 還有更多」的概念，模型無從得知該翻頁 | 🟠 | 同上，`formatTasks` 只印 `len(tasks)` |
| C5 | Web 看板渲染每欄**全部**卡片 | 🟡 | `web/src/components/TeamBoard.vue:34`（`v-for="t in columns[s]"`） |
| C6 | `groupTasks` 把所有任務全分桶、不截斷 | 🟡 | `web/src/events.js:121`；`TASK_STATES`（`:11`） |
| C7 | `GET /api/tasks` 一次回整個 ledger、無 query 參數 | 🟠 | `webapi/api.go:210`；`Tasks(spaceID)`（`:43`） |

---

## 3. 設計方向

### 3.1 Store 共用原語：分頁 ＋ 計數（兩邊都吃它）

擴充 `TaskFilter` 與 `ListTasks`，並新增計數：

```go
type TaskFilter struct {
    Status   Status
    Assignee string
    Limit    int   // 0 = 用呼叫端預設上限（不等於無限）
    Offset   int   // 漸進 reload / 分頁
    Newest   bool  // true: ORDER BY id DESC（completed 要「最近的」）
}

func (s *Store) ListTasks(f TaskFilter) ([]Task, error)   // 加 LIMIT ? OFFSET ?，排序依 Newest
func (s *Store) CountTasks(f TaskFilter) (int, error)      // 同 WHERE、回總數，給「N of TOTAL」
```

- **排序**：terminal 的 `completed` 想看「最近完成」→ `Newest=true`（`ORDER BY id DESC`）；
  其餘狀態維持現有最舊在前（不破壞看板既有順序）。
- **計數**：`CountTasks` 與 `ListTasks` 共用 WHERE，回總數，讓上層能說「顯示 20／共 348」。

### 3.2 Leader 工具：`task_list` 加分頁（漸進 reload）

對 `tools/tasks.go:225` 的 `newTaskList`：

- 新增輸入 `limit`（預設 ~20、**硬上限 ~50**）、`offset`（預設 0）。
- **completed 預設 newest-first ＋ 截斷**；活躍狀態（pending/running/verifying/suspended）
  數量天然小，仍套用同一個合理上限以防極端。
- 結果尾端附**翻頁提示**：`顯示 #N–#M，共 TOTAL 筆；要看更多用 offset=<next>`。這就是
  使用者要的「針對 completed 的漸進 reload（類似分頁查詢）」—— leader 的上下文不再因
  查 ledger 而線性膨脹，需要更多時自己翻頁。
- `formatTasks`（`:50`）對應印出 `label (showing N of TOTAL)`。

> 設計選擇：**預設就安全**（不傳 limit 也只回最近一頁），讓既有 leader 行為自動收斂，不必
> 依賴模型「記得加 limit」。

### 3.3 Web：看板「最近 5」＋ 獨立 Completed 分頁

- **看板 completed 欄收斂**：`TeamBoard.vue` 對 completed 欄只渲染**最近 5 筆**（newest），
  欄首顯示總數，並放一個 **「查看全部 →」** 入口跳到 Completed 分頁。其餘四欄不變。
- **新增 Completed 分頁**：沿用 `SpaceView.vue` 既有的中欄 tab 機制（`centerTab`：目前
  `board | timeline | console`，見 `SpaceView.vue:49`），加第四個 tab `completed`。內容是一個
  可翻頁的已完成清單（每頁 N 筆，前/後翻頁），可點開看 spec/result/verify（複用看板卡片
  的展開）。
- **API 支援分頁**：`GET /api/tasks` 增加 `status`、`limit`、`offset` query；回傳改成帶總數的
  包裝（或加 `X-Total-Count` header），讓分頁 UI 知道總筆數。`webapi/api.go:210` 的 handler
  與 `Backend.Tasks`（`api.go:43`）對應擴充為 `Tasks(spaceID string, f TaskQuery)`。

> 看板輪詢（2.5s）仍抓「活躍欄 + completed 最近 5」這種小集合；Completed 分頁是**按需**
> 查詢，不進輪詢，避免把大量歷史塞進每次 poll。

---

## 4. Scope / Acceptance

**In**：store 分頁+計數原語；`task_list` 分頁（completed newest-first + 上限 + 翻頁提示）；
Web 看板 completed「最近 5 + 查看全部」；新增 Completed 分頁 + `/api/tasks` 分頁參數；測試。

**Out**：改任務狀態機（5 態不動，見 [RP-3](RP-3-agent-run-phase-states.md) / design §7.1）；completed
的歸檔/刪除（v1 仍永不刪，只分頁顯示）；全文檢索（之後再說）。

**Acceptance**：
1. 一個有 500 筆 completed 的空間，leader 跑 `task_list` 預設只回**一頁（≤上限）+ 總數**，
   且可用 `offset` 漸進往回翻；leader 上下文不再因查 ledger 線性膨脹。
2. Web 看板 completed 欄**只顯示最近 5 筆**並標總數，提供「查看全部」。
3. Completed 分頁能前/後翻頁瀏覽全部已完成任務，可展開看細節。
4. `/api/tasks` 接受 `status/limit/offset` 並回總數；分頁 UI 正確。
5. 既有四欄（pending/running/verifying/suspended）行為與順序不變。

---

## 5. 落地任務（建議顆粒）

| # | 任務 | 落點 |
| --- | --- | --- |
| RP-6-1 | `TaskFilter` 加 `Limit/Offset/Newest`；`ListTasks` 加 `LIMIT/OFFSET`+排序；新增 `CountTasks` | `store/tasks.go` |
| RP-6-2 | `task_list` 加 `limit`(預設20/上限50)/`offset`；completed newest-first；結果附「N of TOTAL + 翻頁提示」 | `tools/tasks.go` |
| RP-6-3 | `GET /api/tasks` 加 `status/limit/offset` query + 總數；`Backend.Tasks` 簽名擴充 | `webapi/api.go`、`service/service.go` |
| RP-6-4 | 看板 completed 欄「最近 5 + 查看全部」 | `web/src/components/TeamBoard.vue`、`events.js` |
| RP-6-5 | 新增 Completed 分頁（翻頁 + 可展開），接 `centerTab` | `web/src/components/SpaceView.vue`（+ 新元件） |
| RP-6-6 | 測試：store 分頁/計數、`task_list` 上限與翻頁、api 分頁 | `store/*_test.go`、`tools/*_test.go`、`webapi/api_test.go` |

> 一句話：**completed 只增不減，所以查詢必須有界**。store 一個分頁原語，leader 與 Web 各取
> 所需，根除「上下文/介面隨時間膨脹」。
