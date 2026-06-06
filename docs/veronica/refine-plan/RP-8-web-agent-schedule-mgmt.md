# RP-8 — Phase 2：Web 端的 Agent 與排程管理（User 的方向盤）

> 狀態：**草案 / Draft（待 Johnny 拍板）** ｜ 日期：2026-06-06 ｜ 階段：**Phase 2**
> 前置：依賴 [RP-7](RP-7-leader-scheduled-wake.md)（排程的後端 seam）＋ [RP-5](RP-5-member-prompt-env.md)/[RP-6](RP-6-completed-task-scaling.md) 落地。
> 觸發：把「排程管理」與「成員增刪」這兩個目前只有 CLI/手改檔的能力，搬到 Web，交給 User。
> 上層設計：[`../veronica-design-v1.md`](../veronica-design-v1.md)（§5.4 動態成員）｜ 關聯：[RP-4](RP-4-web-ui-ux.md)（operations console）

---

## 1. TL;DR

[RP-7](RP-7-leader-scheduled-wake.md) 讓 **leader** 能排組員的班；本 RP 把方向盤交給 **User**：

1. **排程管理（對齊 RP-7）**：Web 上 User 可**檢視 / 設定 / 修改 / 刪除任一成員**的 crontab 與
   system-reminder 提示——**包含 leader**（leader 自己不能改自己的，但 User 可以，這正是 RP-7
   的對稱補位）。
2. **成員增刪（透明且可控）**：Web 上 User 可**新增 / 移除 Agent**（**leader 唯一、不可增刪**）。
   新增表單填：名稱、system prompt、when to use、active/deferred 工具、（選）crontab + 提示；
   **團隊協作工具由角色自動注入、對 User 透明**（不出現在工具挑選器）。增刪後，**系統發事件給
   leader**（只透露 when to use），確保 leader 立即知道團隊組成變了、並能查詢與管控新成員。

> 心智模型：延續 [RP-4](RP-4-web-ui-ux.md) 的「swarm operations console」——User 不只是監看，而是能
> **編組團隊、排定班表**，且每個動作都讓 leader 與 roster 保持同步、可觀測。

---

## 2. 現況盤點（file:line 證據）

| # | 事實 | 位置 | 對需求的意義 |
| --- | --- | --- | --- |
| W1 | `AddMember` 只 hot-load **既有** `agents/sub/<name>/` 目錄 | `supervisor.go:103`；Web `POST /api/members {agent}`（`api.go:270`） | 新增需先「寫出目錄」再 AddMember |
| W2 | **沒有 `RemoveMember`** | 全 `internal/swarm/` grep 無 | 刪除要新做後端 + roster remove |
| W3 | `MemberInfo` 無 schedule 欄 | `webapi/api.go:87` | RP-7 已加 roster 欄，這裡上線到 wire |
| W4 | `Backend` 命令面已有 add/freeze/...，無 schedule/remove | `api.go:60-72` | 需擴充介面 |
| W5 | 系統→leader 投信 seam 已存在（sender `"user"`） | `service.go:919-942` `SendUserMessage` | 增刪通知可複用（建議以 `"system"` 區隔） |
| W6 | roster `add` 有，**無 remove** | `roster.go:121`；無 `remove` | 需加 `Roster.remove` |
| W7 | 角色自動注入協作工具（對使用者透明的前例） | `tools/set.go:57` `For`、`toolNamesForRole`（`:69`） | 新增表單**不列**這些工具 |
| W8 | 動態成員會 `persistRuntime` | `supervisor.go:121`、`resume.go:32` | 增刪/排程都要持久化 |

---

## 3. 設計方向

### 3.A 排程管理（Web，對齊 RP-7）

- **檢視**：RP-7 已把 `Cron`/`SchedulePrompt` 放上 `MemberView`；本 RP 將其上線到 `MemberInfo`
  （`api.go:87`）與 roster 快照，於成員卡（`Roster.vue`）顯示 ⏰ 班表。
- **設定/修改/刪除**：成員卡上一個「排程」編輯器（cron 輸入 + prompt 文字框 + 清除鈕）。新 REST：

  ```
  POST   /api/agents/{name}/schedule   { cron, prompt }   # 設定/取代
  DELETE /api/agents/{name}/schedule                       # 清除
  ```

  後端走 [RP-7](RP-7-leader-scheduled-wake.md) 的 `Supervisor.SetSchedule`/`ClearSchedule`（同一個即時
  套用 seam），差別是 **User 路徑沒有 self 限制**——User **可以**設定/修改/刪除 **leader** 的班表。
- **對稱補位**：leader 工具（RP-7 §3.3）拒絕改自己的 crontab；本路徑就是「唯一能改 leader 班表
  的入口」，與 RP-7 的守則一致。
- cron 驗證沿用 `agentdef.parseCron`；壞 cron 回 400。

### 3.B 新增 Agent（寫目錄 → 熱載入 → 通知 leader）

新增是「先把 agent 定義寫到磁碟，再走既有 `AddMember` 熱載入」的兩段式：

1. **表單（`web/`）**：name、system_prompt（textarea）、when_to_use、active tools（多選）、
   deferred tools（多選）、（選）cron + prompt。
   - 工具清單來源：可列舉的工具目錄（active/deferred 候選）。**團隊協作工具（send_message /
     list_members / my_tasks / task_get …）不出現在挑選器**——它們由角色自動注入
     （`set.go:57`），對 User 透明（W7）。
2. **後端 `CreateMember`（新）**：在 `<workdir>/agents/sub/<name>/` 寫出
   `system_prompt.md`、`profile.yml`（含 when_to_use + 選配 schedule）、`tools/active.yml`、
   `tools/deferr.yml`，**再呼叫 `Supervisor.AddMember(name)`**（`supervisor.go:103`，沿用既有
   register→construct→startLoop→persist 全路徑）。
   - 名稱衝突檢查（per-space 唯一，invariant #2）；非法名稱（路徑跳脫）拒絕。
   - 寫檔失敗則不掛入 roster（保持原子性：要嘛上線、要嘛乾淨退回）。
3. **API**：擴充 `POST /api/members`（`api.go:270`，目前只收 `{agent}`）改收完整 spec；
   `Backend.AddMember` → `Backend.CreateMember(spaceID string, spec MemberSpec)`。

### 3.C 移除 Agent（新後端，leader 不可刪）

`RemoveMember` 今天不存在（W2），需新做：

```go
func (s *Supervisor) RemoveMember(name string) error
```

- **守則**：`role == leader` → 拒絕（leader 唯一、不可移除）。
- 動作：停該成員 run loop（cancel + 從 `members` 移除）、`Roster.remove(name)`（W6 新增）、
  停止對其投信（bus 取消註冊）、`persistRuntime`。
- **資料保留**：`.vero` 的歷史與既有 tasks **不硬刪**（v1 永不刪哲學，見 [RP-6](RP-6-completed-task-scaling.md)）；
  該成員名下未完成 task 的處置 → 建議**留給 leader 重新指派**（移除通知裡點明，見 §3.D）。
  在席目錄是否一併刪除 → 建議**預設保留**（可日後 re-add，類似 freeze 的可逆性）；提供
  「連目錄一起刪」為進階選項。
- **API**：`DELETE /api/agents/{name}`（leader 回 400/403）。

> 注意 freeze（`supervisor.go:128`）與 remove 的區別：freeze = 暫時離線、保留席位；remove =
> 退出團隊。UI 要把兩者講清楚，避免誤把「想暫停」做成「想刪人」。

### 3.D 增刪後通知 leader（只透露 when to use）

User 增刪成員後，**系統主動投信給 leader**（複用 `SendUserMessage` 的 bus 路徑，W5；建議以
sender `"system"` 與一般 user 訊息區隔）：

- 新增：`「新成員 \"qa2\" 已加入團隊。When to use：<when_to_use>。需要時可指派任務給它。」`
- 移除：`「成員 \"qa2\" 已移出團隊。其未完成任務需要你重新指派。」`
- **只透露 when to use**（不洩漏 system_prompt / 工具細節）——leader 要的是「這個人何時派得上
  用場」，不是它的內部設定。
- 透過 bus + drain B（`space.go:192` inbox-drainer），leader **就算正在跑也會把通知折進當前 run**，
  即時更新它的團隊心智模型；之後 `list_members` 也查得到（透明且可控）。

### 3.E Leader 唯一性（貫穿 B/C）

- 增：只新增 worker（`agents/sub/`，`RoleWorker`）；UI 不提供「新增 leader」。
- 刪：`role == leader` 一律拒；UI 對 leader 卡隱藏刪除鈕。
- 與既有 manifest 驗證一致——manifest 要求恰一個 leader（`manifest.go:79` `validate`）。

---

## 4. Scope / Acceptance

**In**：Web 排程 CRUD（含 leader，由 User 操作）；Web 新增/移除 worker（寫目錄 + AddMember /
RemoveMember + roster remove）；協作工具對 User 透明；增刪後系統通知 leader（露 when_to_use）；
相關 REST + DTO + 持久化 + 測試。

**Out**：新增/刪除 leader（明確禁止）；硬刪 `.vero` 歷史/任務（保留）；Web 端編輯**既有**成員的
system_prompt/工具（本 RP 只做新增、移除、排程；改既有定義另議）；跨機/權限帳號系統（沿用
[RP-4](RP-4-web-ui-ux.md) §6 的 root token 現況）。

**Acceptance**：
1. User 在 Web 對任一成員（**含 leader**）設定/修改/刪除 cron 與 system-reminder 提示，即時生效、
   重啟後仍在。
2. User 透過 Web 表單新增一個 worker（name/prompt/when_to_use/工具/選配 cron）→ 目錄被寫出、
   成員上線、出現在 roster；**leader 收到「新成員 + when_to_use」通知**並能 `task_assign` 給它。
3. User 移除一個非 leader 成員 → 其退出 roster、停止被投信；**leader 收到移除通知**並被提示重指派。
4. 對 leader 的新增/刪除一律被拒（UI 不給入口、API 回錯）。
5. 新增表單**不出現**協作工具；新成員仍自動擁有 send_message/list_members/my_tasks/task_get。
6. 名稱衝突 / 非法名稱被乾淨拒絕，不會留下半掛載狀態。

---

## 5. 落地任務（建議顆粒）

| # | 任務 | 落點 |
| --- | --- | --- |
| RP-8-1 | `MemberInfo` 上線 cron+prompt；roster 卡顯示 ⏰ | `webapi/api.go`、`web/src/components/Roster.vue` |
| RP-8-2 | `POST/DELETE /api/agents/{name}/schedule` → `Set/ClearSchedule`（User 可改 leader） | `webapi/api.go`、`service/service.go` |
| RP-8-3 | Web 排程編輯器（cron + prompt + 清除） | `web/src/components/`（成員卡或面板） |
| RP-8-4 | `Supervisor`/service `CreateMember(spec)`：寫目錄 → `AddMember` | `swarm/supervisor.go`、`service/service.go`、`agentdef`（寫出 helper） |
| RP-8-5 | `Supervisor.RemoveMember` + `Roster.remove` + bus 取消註冊 + persist | `swarm/supervisor.go`、`roster.go`、`bus/` |
| RP-8-6 | `POST /api/members`（完整 spec）+ `DELETE /api/agents/{name}`（leader 拒絕） | `webapi/api.go`、`service/service.go` |
| RP-8-7 | 新增 Agent 表單（工具多選；**不含**協作工具）；移除確認（複用 `ConfirmDialog`） | `web/src/components/`（新表單） |
| RP-8-8 | 增/刪後系統投信 leader（sender `"system"`，只露 when_to_use） | `service/service.go`（複用 `SendUserMessage` 路徑） |
| RP-8-9 | 測試：排程 CRUD（含 leader）、create/remove member、leader 守則、通知、持久化 | 各 `*_test.go`、`web` 測試 |

> 一句話：[RP-7](RP-7-leader-scheduled-wake.md) 給 leader 排別人的班、但管不到自己；本 RP 讓 **User** 既能
> 校正 leader 的班，也能**動態編組整支團隊**——而且每次改動都讓 leader 立刻知情、roster 立刻同步。
