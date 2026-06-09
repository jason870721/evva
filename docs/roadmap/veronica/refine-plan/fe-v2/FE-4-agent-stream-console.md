# FE-4 — Agent Stream Console（多 agent 串流交互）

> 狀態：**草案 / Draft** ｜ 日期：2026-06-07 ｜ 類型：FE 實作 PRD（招牌體驗）
> 相依：FE-1、FE-3 ｜ 對應：[扁平化溝通](../../direction-flat-comms.md)、RP-1 §3.6（inter-agent 可視化）、RP-3（細相位）
> 系列：[FE v2 總覽](README.md)

---

## 1. 目標

把「**看一支正在自主工作的團隊串流**」做成第一公民。這是使用者點名的重點（agent stream 交互設計）。v1 的 `MemberConsole` 只有：text / thinking / 一張 tool 名稱＋status＋result `<pre>`，且 autoscroll 是無條件強制捲底。v2 要做出一個**可讀、可控、可並發觀測**的串流體驗。

> 詞彙與配色全部對齊 TUI（thinking=亮藍、executing=銅、result=天藍、error=紅；see FE-1 §2.4），讓 TUI 用戶零學習成本。

---

## 2. v1 現況（證據）

| 現況 | 證據 | 缺口 |
| --- | --- | --- |
| tool 只渲染名稱＋status＋result `<pre>` | [`MemberConsole.vue:63-68`](../../../../../web/src/components/MemberConsole.vue) | 無 diff、無依工具家族分型、無 input 展開 |
| thinking 僅 italic 變灰 | `MemberConsole.vue:160-163` | 不可收合、串流中無指示 |
| autoscroll 無條件捲底 | `MemberConsole.vue:34-40` | 往上翻歷史會被拉回，無「跟隨/跳到最新」 |
| 只有單成員 console | `SpaceView.vue:491-499` | 無團隊 firehose（全員一條流） |
| 即時 vs 歷史混在一起 | console（live）與 `AgentTranscript`（history）分兩處但未標示 | RP-4 H7 |
| reduce 已正確 demux/coalesce | `events.js:26-117`（FE-1 已 port） | **保留**，本 PRD 只做 render/interaction |

---

## 3. 串流渲染：turn 類型

`stream` store 的 turn（FE-3）有四型，各給專屬呈現：

### 3.1 `assistant`（回覆文字）
- 串流逐字累積（已由 `appendChunk` coalesce 進該 agent 的 open turn）。
- markdown 輕量渲染（code fence / 清單 / 連結）；正文字級 `--fs-md`、行高舒適。
- 串流中尾端跟一個 caret（`--color-cursor` 電光青）。

### 3.2 `thinking`（推理）
- **可收合** block，預設展開、串流結束自動收合成一行摘要「💭 thought for 3.2s」。
- 色＝`--phase-thinking`（亮藍）、`--color-text-faint`；串流中 `EvSpinner`。
- 全域偏好「隱藏 thinking」（`uiStore`），對齊 TUI 的 displayThinking。

### 3.3 `tool`（工具呼叫）— 依家族分型卡片
一張 `ToolCard`，頭部恆有：`EvIcon`＋`tool 名`＋status pill（running spinner / done ✓ / error ✕，色用 `--status-*`）＋耗時。body 依工具家族切換 renderer：

| 家族 | renderer | 重點 |
| --- | --- | --- |
| `bash`/`shell` | 指令 + stdout/stderr | 指令 mono 高亮；輸出可摺、長則「show more」 |
| `fs` edit/write | **diff** | 用 `--diff-add-bg/--diff-del-bg` + `--diff-fg`（白字實心條，**完全對齊 TUI M2 視覺**，palette.go:37-49） |
| `fs` read | 檔案預覽 | 路徑可點（FE-7 連結）、行號 |
| `web`/`search` | 標題＋URL＋摘要 | URL 截斷、可展開 |
| 其他 | 泛型 | input（摺疊 JSON）＋ result `<pre>` |

- input 預設收合（一行摘要：`InputDescription` 或關鍵參數），點開看完整。
- result 截斷高度＋「展開」；error 狀態整卡描紅邊（`--status-error`）。

### 3.4 `error`
- 整段紅字卡（`--color-danger`），保留 agent 歸屬色點。

---

## 4. 兩種觀測視角

### 4.1 Focused member console（預設，URL `/s/:id/stream/:member`）
單一成員的流；head 顯示成員身分（per-agent 色點）＋ **live phase pill**（thinking / executing:bash / waiting-approval，含 elapsed，讀 `mergedRoster`）＋連線角標。leader 與 worker 共用同一元件（扁平化，對應 `MemberConsole` 設計意圖）。

### 4.2 Team firehose（新增，URL `/s/:id/stream`，不帶 member）
**全員一條時間流**，每則 turn 以 per-agent 色點＋名標記 sender；可用 chip 過濾成員。這收掉 RP-1 §3.6「inter-agent 可觀測」與 flat-comms 的「全員可觀測」缺口——不必逐成員點 console。

```
┌─ Stream ────────────────  ◉firehose ○focused  filter:[lead][qa][fe]  ⤓follow ─┐
│ ● lead    assistant  指派 #7 給 qa…                                      0:02 │
│ ● qa      thinking   💭 …                                                0:01 │
│ ● qa      ▶ bash     go test ./swarm/...            ✓ done               0:08 │
│ ● fe      ▶ edit     web2/src/App.vue   +12 −3   [diff ▾]                0:01 │
│ ● lead    🛡 approval bash: rm -rf dist        ⏳ waiting 0:11   [審 →]       │
│ ──────────────────────────────────────────────────────── ↓ 3 new · 跳到最新 │
│ [ Message qa… ⏎ 送出 · ⇧⏎ 換行 ]                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

## 5. Autoscroll：follow-tail，不綁架

- 預設 **follow-tail**；使用者一旦往上捲離底，**暫停自動捲動** ＋ 浮出「**↓ N new · 跳到最新**」pill；點它或捲回底恢復 follow。
- 取代 v1 的無條件 `scrollTop = scrollHeight`（`MemberConsole.vue:34-40`）——盯歷史時不再被拉走。
- 串流抵達只在 follow 模式自動捲；否則只增 pill 計數。

---

## 6. Operator 輸入（mail-mode 扁平溝通）

- 輸入框送訊息給**當前 focused 成員**，走 `stream.sendMessage`（FE-3）→ bus + drain：idle 成員被喚醒、busy 成員 mid-run fold，回覆串回同一 console（語意同 `SpaceView.vue:262-274`）。
- **Enter 送出、Shift+Enter 換行**，且 **IME 組字中不送**（保留 RP 的 `isComposing` 修正，`MemberConsole.vue:29-32`）。
- 樂觀：送出立刻把 user turn 推進流（per-agent `user` 色，`colors.js` FIXED.user）。

---

## 7. Member Inspector（INSPECTOR region）：明確區分 live vs history

右欄（FE-2 的 INSPECTOR）開某成員時，分三明確區塊（解 RP-4 H7）：

1. **Live**：當前 run 的即時相位＋最近 turn 摘要（指回 CENTER 的 focused stream）。
2. **History（transcript）**：持久 transcript（`api.transcript`），標「歷史」、相對時間。
3. **Mailbox**：該成員的 inter-agent 訊息，sender→recipient **per-agent 色路由**（保留 `AgentTranscript.vue` 的可掃視優點）＋ unread/claimed/read 狀態（`mailState`，含「reading…」claimed 態，`events.js:363-368`）。

---

## 8. 效能（seam，FE-8 收尾）

- 長串流 **虛擬化**（windowing）：只渲染可視範圍 turn；ToolCard result 預設摺疊降 DOM。
- turn 以穩定 key；markdown / diff 渲染 memo 化。
- firehose 過濾在 store getter 層，不在模板重算。

---

## 9. 互動細節（affordances）

- 每則 turn：hover 顯示「複製 / 展開」；tool result「複製輸出」。
- 「全部展開 / 收合」toggle（thinking 與 tool input）。
- 鍵盤：`j/k` 在 turn 間移動（FE-8 a11y 一併）、`gg`/`G` 到頂/底。
- 空狀態：「尚無活動，送 {member} 一則訊息開始。」（沿用 `MemberConsole.vue:70-72`）。

---

## 10. 元件

```
stream/
  StreamView.vue        # firehose ↔ focused 切換、filter、follow pill
  MemberStream.vue      # 單成員流（focused）＋ head phase pill ＋ 輸入框
  TurnList.vue          # 虛擬化 turn 列表
  turns/
    AssistantTurn.vue  ThinkingTurn.vue  ErrorTurn.vue
    ToolCard.vue         # 殼：icon/status/耗時 + body slot
    tools/ BashRender.vue  DiffRender.vue  ReadRender.vue  WebRender.vue  GenericRender.vue
  Composer.vue          # 輸入框（IME-safe Enter）
inspector/
  MemberInspector.vue   # Live / History / Mailbox 三區
  MailboxList.vue       # per-agent 色路由 + mailState
```

---

## 11. 驗收

1. tool 呼叫依家族正確分型；`fs` edit/write 顯示 **diff（白字 + 綠/紅實心條，視覺對齊 TUI）**。
2. thinking 可收合、串流中有 spinner、結束收成摘要。
3. 多成員並發串流互不截斷；firehose 一條流可依成員過濾。
4. 往上捲歷史**不被拉回**；有「↓ N new · 跳到最新」。
5. focused console 的 phase pill 即時反映 thinking/executing/waiting（讀 FE-3 mergedRoster）。
6. Inspector 明確標示 Live / History / Mailbox；mailbox 有 unread/claimed(reading)/read。
7. 千則 turn 下捲動順暢（虛擬化生效）。

---

## 12. 子任務

| # | 子任務 |
| --- | --- |
| FE-4a | TurnList 虛擬化＋AssistantTurn/ThinkingTurn/ErrorTurn |
| FE-4b | ToolCard 殼＋status/耗時＋家族 renderer（bash/diff/read/web/generic） |
| FE-4c | MemberStream（focused）＋head phase pill＋Composer（IME-safe） |
| FE-4d | StreamView firehose＋成員過濾 |
| FE-4e | follow-tail / 跳到最新 pill |
| FE-4f | MemberInspector（Live/History/Mailbox）＋MailboxList |
