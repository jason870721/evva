# RP-1 — 訊息投遞可靠性：消滅 lost-wakeup、以 DB 為唯一真相

> 狀態：**草案 / Draft（待拍板）** ｜ 日期：2026-06-05 ｜ 嚴重度：🔴 高
> 對應問題：smoke test #1 —— 「A 送訊息給 B，B 有時收不到（UI 無訊息、收件 agent 無反應）；
> 部分訊息卡在 mailbox 一直 unread」。
> Design：[`../veronica-design-v1.md`](../veronica-design-v1.md) §6、§6.2 ｜ Ticket 源：SPRD-1-5 / 1-6 / 1-12

---

## 1. TL;DR

訊息其實**已經 durable 落地 SQLite**（`PutMessage` 先寫、再 signal），所以「漏收」不是
資料掉了，而是 **agent 沒被喚醒去讀**。根因有兩層：

1. **`drainStaleHints` 過度清空 mailbox channel** —— 它在組完 prompt 後把 channel 裡
   **所有** buffered hint 清掉，但其中可能含「在 `UnreadFor` 快照之後、清空之前才到」的
   新訊息 hint。那封訊息**沒被折進 prompt、也沒被標已讀**，而 hint 又被清掉 → 永遠不會
   再有人來叫醒 B。→ 「卡在 unread、agent 無反應」。
2. **喚醒只靠 chan hint，從不回頭對帳 DB** —— 設計說「DB 是真相、chan 只是 hint」，但
   這條原則只實作在**內容**（`composePrompt` 讀 DB），沒實作在**喚醒**（run 結束後沒有
   重新檢查 `UnreadFor`）。任何 hint 掉了（buffer 滿、被過度清空、投遞 race 到 frozen）
   都是一次**永久 lost wakeup**。

**修整方向（允許大動作）**：把 mailbox channel 降格成**純粹的 best-effort 喚醒提示**，
讓 SQLite 同時是**內容**與**liveness** 的唯一真相 —— 改成 **level-triggered**：每次 run
結束都重查 `UnreadFor`，非空就立刻再跑；刪掉 `drainStaleHints`；把 drain A / drain B
的「標已讀」語意統一；再加一個低頻 safety re-scan 兜底。

---

## 2. 現況與根因（含證據）

### 2.1 投遞鏈是對的，喚醒鏈是錯的

| 環節 | 程式 | 狀態 |
| --- | --- | --- |
| 寫 durable row → signal hint | `bus/bus.go:118` `deliver()`（persist-before-signal） | ✅ 正確 |
| hint 滿/無 inbox 就丟（row 仍在） | `bus/bus.go:132` `signal()` | ✅ 故意非阻塞 |
| idle 喚醒讀信（drain A） | `scheduler.go:83` `serve()` → `composePrompt()` 讀 `UnreadFor` | ⚠️ 內容對、喚醒不對帳 |
| busy 中途折信（drain B） | `drain.go:32` `inboxDrainer.Drain()` | ⚠️ 標已讀語意與 A 不一致 |
| 清掉「已折入批次」的殘留 hint | `scheduler.go:208` `drainStaleHints()` | 🔴 **過度清空 → lost wakeup** |
| run 結束後重查未讀 | （無） | 🔴 **缺這一步 = 沒有兜底** |

### 2.2 lost-wakeup 的精確時序

`serve()`（`scheduler.go:83-153`）的順序是：① `composePrompt` 用 `UnreadFor` 取快照 →
② `drainStaleHints` 清空 channel → ③ 跑 run → ④ clean 結束才 `MarkRead`。考慮：

```
B idle，runLoop 阻塞在 select{ case <-inbox }
1. Msg1 到：PutMessage(U1) → signal 推 U1 → chan=[U1]
2. runLoop 消費 U1 → serve(wakeMessage)
3. composePrompt: UnreadFor(B)=[U1]（快照）→ prompt 只含 Msg1
   ── 競態窗口 ──
4. Msg2 到：PutMessage(U2)（DB: U2 未讀）→ signal 推 U2 → chan=[U2]
5. drainStaleHints: 清空 chan → 連 U2 的 hint 一起清掉！ chan=[]
6. run（只處理 Msg1）→ 期間 drain B select chan 為空 → 不折 Msg2
7. clean 結束：MarkRead(U1)。U2 仍未讀、且 chan 無 hint
8. runLoop 回 select → 永遠阻塞（直到下一封信偶然來叫醒）
```

→ **Msg2 卡死 unread、B 永不反應**，與回報症狀完全一致。`drainStaleHints` 無法分辨
「已折入批次的殘留 hint」與「快照之後才到的新 hint」，所以必然會誤清。

> 補充：`Deregister` 目前**無人呼叫**（dead code），freeze 不換 channel，所以
> 「runLoop 抓舊 channel」不是本問題來源；本問題純粹是 hint 被誤清 + 無 DB 對帳。

### 2.3 drain A / drain B 標已讀語意不一致（會造成相反的失敗：訊息被吃掉）

- **drain A**：clean run 結束**才** `MarkRead`（`scheduler.go:148-152`）—— run 失敗/取消
  就留 unread 重試。✅ 對 crash recovery 友善。
- **drain B**：折信當下**立即** `MarkRead`（`drain.go:46`）—— 若該 run 隨後被
  suspend / cancel / error，這封信**已標已讀但從未被處理** → 靜默丟失。

兩者規則相反，是潛在的「另一種漏訊息」。

### 2.4 UI 可見性缺口（放大了「看不到訊息」的體感）

- inter-agent 訊息只出現在 ① 輪詢的 `/api/messages`（每 2.5s）② 點進成員才看得到的
  mailbox（`AgentTranscript` 右欄）。**`MemberConsole` 不會把它當 turn 顯示**
  （console 只顯示 `agentId` 命中的 event + operator 對它的 user 訊息，見
  `events.js:consoleTurns`）。
- B 折信是「合成 LLM prompt」，**不產生 chat-stream event**，所以 operator 在 console
  完全看不到「B 收到 A 的信」。配上 lost-wakeup，體感就是「我送了，什麼都沒發生」。

### 2.5 addressing 陷阱（次要，但 smoke test 很容易踩）

worker 直覺寄 `to:"leader"`，但 leader 的**成員名**可能叫 `lead` / `pm`
（見 `vero-tech-swarm`）。`rosterHas`（`tools/messaging.go`）會擋下未知名字、回錯誤給
model。守規矩的 model 會 `list_members` 重試，不守的就放生 → 看起來像「訊息沒送到」。

### 2.6 測試為何沒抓到

`supervisor_test.go:124 TestMessageWakeRunsAndMarksRead` 只測「單封、無競態」的 happy
path；drain 測試（`drain_test.go`）只單獨測 drain B。**沒有任何測試覆蓋「訊息在
compose 窗口期到達」的競態**，所以 lost-wakeup 從未被觸發。

---

## 3. 修整方向：mailbox = 純喚醒提示；SQLite = 內容 + liveness 的唯一真相

核心原則一句話：**supervisor 永遠不可依賴某個 hint 還在 channel 裡**。

### 3.1 （主修）level-triggered serve —— run 結束必對帳 DB

把 per-member run 改成「醒來後 drain 到 DB 無未讀為止」：

```text
runLoop:
  for {
    select { <-ctx.Done: return; <-inbox: ; <-wake r ; <-safetyTick: }
    for {                                 // 關鍵：drain 到空
      progressed := serveOnce(ctx, name, m, reason)
      if !progressed { break }            // UnreadFor 空（或 frozen/suspended）→ 回 select 阻塞
    }
  }

serveOnce:
  if !isActive: return false
  ids := UnreadFor(name)                  // ← liveness 也以 DB 為準
  if len(ids)==0: return false
  prompt := fold(ids)
  run(prompt)
  if clean: MarkRead(ids ∪ 本 run drain B 折入的 ids)   // 見 §3.3
  return true                             // 跑過一輪 → 外層再對帳一次
```

**這一步單獨就消滅整個 lost-wakeup 類別**：只要成員至少被叫醒一次，它就會把 DB 裡
所有未讀清乾淨，不管多少 hint 在中途掉了。

### 3.2 刪掉 `drainStaleHints`

有了 §3.1 + drain B 以 `read_at` 去重（`drain.go:43` 已會跳過已讀），殘留 hint 變成
無害：指向已折入訊息的 hint 被 drain B 跳過；指向新訊息的 hint 觸發一次**正確的**額外
serve。殘留 hint 的成本只剩「一次便宜的 `UnreadFor` 空查詢」。→ 不再有 blanket drain、
不再誤清。

### 3.3 統一 drain A / drain B 的標已讀（消滅 §2.3 不一致）

定一條規則：**標已讀只發生在 clean run 結束，對象是「起始批次 ids ∪ 本 run 期間
drain B 折入的 ids」之聯集**。

- drain B **不再自己標已讀**，改成把折入的 id 記進「本 run 折入集合」並對該集合去重
  （避免把起始批次重折一次）。
- 因為 drain B 與 serve **跑在同一條 goroutine**（drain B 是 `ctl.Run` 內部、同步呼叫），
  這個集合**無併發**，實作極簡（一個 per-run set，serve 開跑前建、結束清）。
- 非 clean 結束（cancel / error / suspend / panic）→ 整個聯集都不標 → 全部重試；crash
  則靠重啟 `Reload` 的 `UnreadFor` requeue 兜回（`resume.go:81`）。

> **建議的結構性重構**：把「未讀 → 折入 → 結算」的整段生命週期，從目前散落在
> `scheduler.go`（compose + drainStaleHints + markread）與 `drain.go`（eager markread）
> 兩處，收斂成**單一 per-member `MailboxSession` 物件**，drain A 與 drain B 都呼叫它。
> 這同時解掉 §2.2 與 §2.3，且讓訊息生命週期只有一個 owner。
>
> *（可選的 durability 強化）* 若想讓 web 看得到「claimed（折入但 run 未結束）」狀態、
> 或想免去重啟 reset，可把 `read_at` 二態升成 `unread → claimed_at → read_at` 三態。
> 非必要 —— 上面的「聯集 + 重啟 requeue」已足夠正確；列為後續選項。

### 3.4 safety re-scan tick（兜底，防「從未被叫醒」）

每個 space 加一個低頻（5–10s）reconcile：對每個 `active` 且 `READY` 的成員查
`UnreadFor`，非空就 `poke`。這是「成員完全沒收到任何 hint」這種極端角落（投遞 race 到
剛 freeze/unfreeze、或 register 前就投遞）的最終保險。成本是每成員每 tick 一次 indexed
查詢，極低；有了 §3.1 幾乎用不到，但它把「永久卡死」降級成「最多卡 ≤10s」—— 這是
hang 與 hiccup 的差別。

### 3.5 role-addressing（解 §2.5 陷阱）

`send_message` / `SendUserMessage` 解析 `to` 時：先看是不是 role token
（`leader`/`reviewer`…），是就解析成該 role 的**唯一 active 成員**；否則才走名字精確比對。
移除 dead-letter 陷阱，同時保留對真打錯字的友善錯誤。

### 3.6 UI：把 inter-agent 訊息變成 console 一等 turn（解 §2.4）

讓 `MemberConsole` 把「寄給 / 寄自該成員」的訊息以 `✉ A → B: …` inline 顯示在串流裡，
B 折信時也顯示一筆可見的 incoming turn。後端訊息本來就 durable，這是純前端工作，與
[`../direction-flat-comms.md`](../direction-flat-comms.md)「全員可觀測」同向。可搭配一個
輕量「message delivered」WS 通知降低 2.5s 輪詢延遲。

---

## 4. Scope

**In：**
- `scheduler.go`：`runLoop` / `serve` 改 level-triggered；刪 `drainStaleHints`；統一 markread。
- `drain.go`：drain B 不再 eager markread，改記入 per-run 折入集合 + `read_at` 去重。
- （建議）抽出 `MailboxSession` 收斂訊息生命週期。
- `supervisor.go` / `space.go`：加 safety re-scan tick。
- `tools/messaging.go` + `service.go`：role-addressing 解析。
- `web/`：inter-agent 訊息在 console 的可見化（前端）。

**Out：**
- 訊息 schema 三態化（§3.3 選項）—— 列為後續，非本計畫必須。
- drain B（M4 即時收信）本身的存廢 —— 保留；本計畫只修它的正確性，不移除這個體驗升級。
- 跨 space 投遞（永不 —— §3.1 不變量）。

---

## 5. Acceptance Criteria

1. **訊息壓力測試**：A 對 B 連發 N 封、每封帶隨機 jitter（含刻意落在 B compose/run 窗口期），
   結束後 `UnreadFor(B)` 必為空、B 的 transcript 反映全部 N 封 —— **零漏收**。
2. **§2.2 競態回歸測試**：精確重現「訊息在 `UnreadFor` 快照之後到達」，斷言它**不會**
   lost（最終被處理、標讀）。
3. **drain-B-then-cancel**：折入後 run 被 suspend/cancel，該訊息**回到未讀並於 resume 重試**
   （不得被靜默吃掉）。
4. **safety re-scan**：投遞時序刻意讓 hint 全掉，成員仍於一個 tick 內被叫醒處理。
5. **role-addressing**：`to:"leader"` 在 leader 成員名為 `lead` 時仍正確投遞。
6. `go test -race ./internal/swarm/...` clean；無 `internal/agent` import（維持 oracle 紀律）。

---

## 6. Definition of Done

- [ ] `serve` 改 level-triggered、run 結束對帳 `UnreadFor`；`drainStaleHints` 移除。
- [ ] drain A / drain B 標已讀語意統一（clean-end 結算聯集；非 clean 全重試）。
- [ ] safety re-scan tick 上線；§2.2 競態回歸測試 + 壓力測試綠燈。
- [ ] role-addressing；inter-agent 訊息在 `MemberConsole` 可見。
- [ ] `-race` clean；happy-path 既有測試不退化。

---

## 7. 風險與取捨

- **level-triggered 會不會 busy-loop？** 不會 —— 內層迴圈只在「DB 真有未讀」時續跑；
  清空即回 select 阻塞（idle 不燒 token 的承諾不變）。
- **safety tick 的成本**：每成員每 tick 一次 indexed `UnreadFor`，O(成員數)，可忽略；
  頻率可設定。
- **MailboxSession 重構的範圍**：屬中型重構，但收斂了三處散落的訊息邏輯，長期維護划算；
  若要小步走，可先只做 §3.1 + §3.2（最小且足以解 smoke test 症狀），§3.3 的 owner 收斂
  作為 follow-up。
