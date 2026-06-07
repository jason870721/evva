# FE-5 — 態勢感知：Attention 層 ＋ 5 態看板 ＋ Team Timeline

> 狀態：**草案 / Draft** ｜ 日期：2026-06-07 ｜ 類型：FE 實作 PRD（監看面）
> 相依：FE-1、FE-3 ｜ 對應：RP-4 §4.1（態勢感知，最高優先）、RP-6（completed 規模化）、RP-3（細相位）
> 系列：[FE v2 總覽](README.md)
> 紀律：**task store 為 agent 擁有**——操作者面看板是 **read-mostly**，不提供 user 側 clear/cancel（見專案 memory）。

---

## 1. 目標

回答操作者打開 console 的兩個問題：「**有什麼需要我？**」（Attention）與「**團隊在幹嘛？**」（Board ＋ Timeline）。v1 已落地 AttentionBar / TeamBoard / Timeline（RP-4 UX-1/UX-2），v2 在 token 化 ＋ 規模化 ＋ 可觀測性上補齊。

---

## 2. 三塊監看面

### 2.1 Attention 層（恆顯，AttentionStrip）

聚合「需要我」，most-urgent first（沿用 `attentionItems`，`events.js:289-305`）：

- **act**：`waiting-approval` / `waiting-input`（紫 `--phase-waiting`）——點 chip 直接開該 gate（FE-6）。
- **warn**：`error` / `paused`（紅/黃）——點 chip 聚焦該成員 stream。
- **stall（新增）**：任一 phase 停留超過門檻（如 executing > 5m、thinking > 3m）即升 warn——接 RP-3 細相位＋`phaseSince`，補 RP-4 H1 提到的「卡很久」偵測。門檻可設（`uiStore`）。
- 每 chip：glyph（形狀，非純色）＋成員名＋phase:tool＋**live elapsed**（`elapsed`，`events.js:274-284`）。
- 安靜態：無事顯一行淡色「✓ all clear」（保留 `AttentionBar.vue` 的 quiet 設計）。
- **新 attention 提示**：可選的瀏覽器 title badge「(2) evva·swarm」＋ 可選音效（`uiStore` 偏好，預設關）——讓背景分頁也察覺。
- 鍵盤：`!` 跳到第一個待辦、`Esc` 收起。

```
┌─ Attention ─────────────────────────────────────────────  ● 2 need you ─┐
│ ⏳ qa  waiting-approval:bash  2:41 [審→]   ⚠ fe  error  0:12 [看→]        │
└──────────────────────────────────────────────────────────────────────────┘
```

### 2.2 5 態看板（Board view）

對齊 task 狀態機 `pending / running / suspended / verifying / completed`（`events.js:11`），欄色用 `--status-*`（FE-1，對齊 TUI）。卡片加料（延續 RP-4 UX-2、`TeamBoard.vue`）：

- **assignee per-agent 色點**＋名、**相對時間**（`relTime`）、`#id`。
- 點開 detail：`spec / result / verifyNote / createdBy`；**parent/child** 關係（`TaskInfo.parentId`，`api.go:184`）以縮排或連結呈現（v1 未用 parentId）。
- **規模化（RP-6）**：board 的 completed 欄只顯最近數筆＋總數，`view all N →` 切 Completed 分頁；active 欄多 task 時欄內捲動，欄首常駐計數。
- **過濾**：依 assignee 篩（多成員時定位快）。
- detail 也可開到 INSPECTOR（FE-2 `/s/:id/t/:taskId`），帶**關聯訊息**（`MessageInfo.refTask`，`api.go:220`）——把「這個 task 牽動哪些對話」串起來。
- **read-mostly**：不提供操作者改 task 狀態/刪除（agent 擁有）；只有檢視＋（必要時）對 leader 發訊息請求調整（走 stream）。

### 2.3 Completed 分頁（Completed view）

獨立分頁，`tasksPage(status=completed, limit, offset)`（RP-6，`api.js:32-38`）。無限捲動或頁碼；顯「N of TOTAL」。沿用 `CompletedTasks.vue` 行為，token 化重做。

### 2.4 Team Timeline（Timeline view）

**跨成員事件流**（RP-4 §4.1c，收 RP-1 §3.6 與 flat-comms「全員可觀測」）。v1 `Timeline.vue` 只放 messages；v2 擴成真正的 activity feed，依時間排序、per-agent 色標 sender：

| 事件源 | 來源 | 呈現 |
| --- | --- | --- |
| inter-agent / operator 訊息 | `mailStore`（`api.messages`） | sender→recipient 色路由＋unread/claimed/read |
| 任務指派 / 狀態轉移 | `ledgerStore` diff（poll 對帳） | 「lead 指派 #7 → qa」「qa 完成 #7」 |
| 審批 / 問答 gate | `gateStore` 事件 | 「qa 請求 bash 審批」（可點回放/聚焦） |
| 成員上/下線、freeze/suspend | roster diff | 「+ designer 加入」「fe 凍結」 |
| 排程喚醒（RP-7）/ 外部事件（RP-9） | event/webhook | 「⏰ qa 定時喚醒」「⚡ 外部事件 from trader-engine」 |

- 過濾：依類型 / 成員。
- 每列可點：訊息→開 inspector mailbox；gate→開 FE-6；task→開 task detail。

```
┌─ Timeline ──────────────────────  filter: ◉all ○msg ○task ○gate ○member ──┐
│ 09:41:02 ● lead → ● qa     指派 #7「跑迴歸測試」                            │
│ 09:41:10 ● qa              ▶ 開始 #7（running）                              │
│ 09:43:55 ● qa  🛡 請求審批 bash                              [審 →]          │
│ 09:44:20 ⚡ external · trader-engine：BTC 波動 > 3%          → leader          │
│ 09:45:01 ● qa → ● lead     #7 done ✓                                         │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

## 3. 不只用顏色（a11y 前置，FE-8 收尾）

所有狀態**形狀/文字雙編碼**：board dot 加形狀或標籤；phase chip 帶 glyph；attention act/warn 用不同 glyph 而非只靠色（對應 RP-4 H5）。

---

## 4. 元件

```
attention/
  AttentionStrip.vue   # 聚合 chip（act/warn/stall）＋ all-clear ＋ title/sound 偏好
board/
  TeamBoard.vue        # 5 欄、status 色、過濾
  TaskCard.vue         # assignee 色、相對時間、展開 spec/result/verify、parent/child
  CompletedView.vue    # 分頁（tasksPage）
  TaskDetail.vue       # inspector：完整欄位＋關聯訊息（refTask）
timeline/
  Timeline.vue         # 多源 activity feed＋type/member 過濾
  TimelineRow.vue      # 依事件型別 render，可點跳轉
```

---

## 5. 驗收

1. 打開任一 space，**3 秒內**從 Attention 回答「有什麼需要我」；stall（卡太久）也會升上來。
2. Board 卡片有 assignee 色、相對時間、可展開 spec/result/verify；completed 受 RP-6 規模化（最近數筆＋總數＋分頁）。
3. Timeline 是**多源** activity feed（訊息＋任務＋gate＋成員＋排程/外部事件），可過濾、可點跳轉。
4. 操作者**無法**直接改/刪 task（agent 擁有）；只能檢視與經 stream 請 leader 調整。
5. 狀態非純色編碼（dot/chip 帶形狀或文字）。

---

## 6. 子任務

| # | 子任務 |
| --- | --- |
| FE-5a | AttentionStrip（act/warn/**stall**）＋ title/sound 偏好＋鍵盤跳轉 |
| FE-5b | TeamBoard＋TaskCard（assignee 色/相對時間/展開/parent-child）＋assignee 過濾 |
| FE-5c | CompletedView 分頁（tasksPage） |
| FE-5d | TaskDetail（inspector）＋關聯訊息 refTask |
| FE-5e | Timeline 多源 feed＋型別/成員過濾＋可點跳轉 |
