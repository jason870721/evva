# FE-3 — 即時資料層（Pinia stores ＋ WS ingest ＋ command 通道）

> 狀態：**草案 / Draft** ｜ 日期：2026-06-07 ｜ 類型：FE 實作 PRD（狀態架構）
> 相依：FE-1 ｜ 對應：RP-1（訊息可靠性）/ RP-2（gate 重放）/ RP-3（細狀態）的**消費端**
> 系列：[FE v2 總覽](README.md)

---

## 1. 目標

把 v1 散在 `SpaceView.vue`（662 行）裡的所有狀態與 IO——WS ingest、REST 輪詢對帳、gate 佇列、command 收送、reconnect 重放——抽成一組**型別化 Pinia store ＋一條乾淨的 ingest pipeline**。畫面元件（FE-4~7）只讀 store getter、只呼 store action，不再各自摸 `props.api` 與 socket。

> 後端契約**完全不動**——本層是 RP-1/2/3 的忠實消費端。v1 的純 reducer（`events.js`）已在 FE-1 port 成 `lib/events.ts`，本 PRD 把它接到 store。

---

## 2. v1 現況（被抽走的東西，附證據）

| v1 職責 | 證據 | 去處 |
| --- | --- | --- |
| WS 開連＋reconnect backoff | [`ws.js:10-61`](../../../../../web/src/ws.js) | `lib/ws.ts` ＋ `connectionStore` |
| 事件 → 相位/聊天 reduce | `SpaceView.vue:225-249`、`events.js` | `streamStore` ingest |
| 2.5s REST 對帳 | `SpaceView.vue:206-223, 410` | `spaceStore.poll` |
| live phase 疊加 polled roster | `SpaceView.vue:180-185`（`mergedRoster`） | `spaceStore.mergedRoster` getter |
| gate 佇列（de-dup） | `SpaceView.vue:42-43, 253-260` | `gateStore` |
| reconnect 重放 / command_error 補抓 | `SpaceView.vue:228-234, 363-370, 405-408` | `gateStore.hydrate` |
| command 收送 | `SpaceView.vue:262-301`、`api.js` | 各 store action |

---

## 3. Store 切分

```
stores/
  connection.ts   # wsStatus: 'connecting'|'open'|'closed'；單 socket 控制；reconnect
  space.ts        # active space、roster(MemberInfo[])、lifecycle；poll；mergedRoster getter
  stream.ts       # chat turns（per-agent demux/coalesce）、livePhases；ingest(ev)
  ledger.ts       # board snapshot(active+preview)、completedTotal、tasksPage（分頁，RP-6）
  mail.ts         # messages(MessageInfo[])、unread/claimed/read 衍生
  gate.ts         # approvals[]、questions[]（佇列）、hydrate()、resolve()
  ui.ts           # （FE-1）theme / gateMode / 偏好
```

每個 store 純 TS、可單元測試 ingest 邏輯（沿用 `events.ts` 的 `node --test` 風格）。

---

## 4. WS ingest pipeline（單一入口）

```ts
// 一個 ingest 函式分派一則 wire event 到對的 store（取代 SpaceView.onEvent）
function ingest(ev: WireEvent | CommandErrorFrame) {
  if (ev.type === 'command_error') {            // 服務層 frame，非事件（api.go:586-591）
    connection.lastError = ev.message
    gate.hydrate()                              // 路由失敗 → 重抓 pending，別讓成員卡死
    return
  }
  stream.foldPhase(ev)                           // reducePhase：先更新 livePhases（含 gate 事件）
  if (isApproval(ev)) return gate.enqueue('approval', approvalOf(ev))
  if (isQuestion(ev)) return gate.enqueue('question', questionOf(ev))
  stream.foldChat(ev)                            // reduceChat：text/thinking/tool/error 折進 turns
  if (touchesLedger(ev)) ledger.refresh()        // tool_use_result / store_update → board 對帳
}
```

- **demux/coalesce 不重寫**——直接用 FE-1 port 的 `reduceChat` / `agentOpenTurn`（多 agent 並發串流，各自折進自己的 open turn；`events.ts` 對應 `events.js:26-117`）。
- **phase 衍生不重寫**——`reducePhase` / `phaseFor` 是 Go `phaseDeriver` 的 JS 雙生子（`events.js:184-243`），保持 lockstep。
- `connection` 單一 socket：`onStatus('open')` → `gate.hydrate()`（補抓斷線期間升起的 gate，RP-2 §3.3）。

---

## 5. REST 對帳與 live 疊加

- `space.poll()`：每 2.5s 拉 `roster / tasks / messages`（沿用 `SpaceView.refreshSnapshots`，`api.js:27-44`）。輪詢是**結構真相**（成員、role、coarse run、task）；WS 是**即時相位真相**。
- `space.mergedRoster`（getter）：把 `stream.livePhases[agentId]` 疊到 polled roster 上（phase/tool/phaseSince），讓 pill（尤其 sky 藍 thinking）即時跟動而非慢一個 poll（對應 `SpaceView.vue:180-185`）。
- `ledger`：`tasks` 回 board snapshot `{tasks, total}`（RP-6，`api.js:30`）；Completed 分頁走 `tasksPage`（`api.js:32-38`），交 FE-5。
- 對帳節流：tool_use_result 高頻，`ledger.refresh()` 做 trailing debounce（~300ms）避免每顆 token 都打 REST。**（v1 是每個 tool result 都 refresh，`events.js:343-353`；v2 收斂為 debounce。）**

---

## 6. Command 通道（型別化 action ＋樂觀更新）

| action | 後端 | 樂觀策略 |
| --- | --- | --- |
| `stream.sendMessage(to, text)` | `POST …/message`（`api.js:50-51`） | 立即把 user turn 推進 console（`SpaceView.vue:262-274`），失敗 toast |
| `gate.respondPermission(d)` | WS `respond_permission`（`api.go:573`） | 立即出列該 gate，下一個自動浮上（`SpaceView.vue:276-288`） |
| `gate.respondQuestion(d)` | WS `respond_question`（`api.go:575`） | 同上 |
| `space.memberCmd(verb, name)` | `POST …/{verb}`（`api.js:52-55`） | 送出後 `poll()` 收斂 |
| `space.run(agent, prompt)` | WS `run`（`api.go:571`） | — |
| `ledger / mail / schedule / skills` | 各 REST | 交 FE-5/FE-7，仍經此層 |

- **錯誤統一出口**：任何 action 失敗 → `connection.lastError` ＋ toast；gate 類失敗 → 額外 `gate.hydrate()` 重放（RP-2 §3.3、RP-4 UX-3，避免無聲卡住）。
- **gate 佇列**：`enqueue` 以 `(agentId, requestId)` de-dup（重連重放 / 雙 WS 不重複；`SpaceView.vue:253-257`）；`resolve` 只移除已答那筆，head 自動換下一個。

---

## 7. Console / mailbox 復原（reconnect / reload / stop→run）

沿用 v1 的 best-effort hydrate，移進 store：

- `stream.hydrateFromTranscripts()`：逐成員拉 transcript（`api.js:40-41`），把 assistant turns 種回空 console，**只在 console 為空時**（不蓋掉 live 已送達的 turns）——對應 `SpaceView.vue:378-396`。
- `gate.hydrate()`：`GET …/pending`（`api.js:44`）重抓未決 gate。
- reset 後清流：`stream / gate` 清空再 `poll`（對應 `SpaceView.vue:330-344`）。

---

## 8. 連線韌性（first-class）

- `connectionStore.wsStatus` 為一級狀態；FE-2 的全域 banner 與各 console 角標都讀它（取代 v1 散在多處的小字，RP-4 H8）。
- reconnect backoff 留在 `lib/ws.ts`（500ms→5s，`ws.js:43-47`）；open 時觸發 `gate.hydrate()` ＋ `space.poll()` 立即補一拍。

---

## 9. 驗收

1. 業務元件（FE-4~7）**零** 直接觸碰 socket / `fetch`——全經 store。
2. 多 agent 並發串流時，各成員 turn 不互相截斷（`reduceChat` 測試 port 後綠）。
3. roster pill 的 thinking/executing 在事件抵達當下即更新，不等 2.5s poll。
4. 斷線→重連：未決 gate 自動重現可答；console 不變空白；command_error 觸發重放。
5. `tool_use_result` 洪流下 REST 對帳被 debounce，board 仍最終一致。
6. store ingest 邏輯有單元測試（接 FE-1 的純函式）。

---

## 10. 子任務

| # | 子任務 |
| --- | --- |
| FE-3a | connectionStore＋單 socket＋reconnect｜全域 banner 接線 |
| FE-3b | streamStore：ingest pipeline（foldChat/foldPhase）＋demux |
| FE-3c | spaceStore：poll＋mergedRoster＋memberCmd |
| FE-3d | gateStore：佇列 de-dup＋hydrate＋resolve＋command_error 重放 |
| FE-3e | ledgerStore（board snapshot＋分頁原語）＋mailStore |
| FE-3f | console/gate hydrate（transcript / pending / reset 清流）＋對帳 debounce |
