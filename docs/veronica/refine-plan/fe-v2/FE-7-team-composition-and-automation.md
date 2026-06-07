# FE-7 — 團隊編組 ＋ 自動化：Roster、成員增刪、排程、Skills、外部事件

> 狀態：**草案 / Draft** ｜ 日期：2026-06-07 ｜ 類型：FE 實作 PRD（編組台）
> 相依：FE-1、FE-2、FE-3 ｜ 對應：RP-7（排程喚醒）、RP-8（Web 編組）、RP-9（外部事件）、RP-10（skills）、RP-4 §4.4 H10
> 系列：[FE v2 總覽](README.md)
> 紀律：**leader 唯一**（不可增刪）；**agent 只能載入 skill、不能著作**（RP-10）。

---

## 1. 目標

把 RP-7~10 帶進來的「**User 的方向盤**」前端做成一個乾淨的編組台：看團隊、增減 worker、排班、配 skill、接外部事件——而不是把控制塞滿每張 roster 卡。

---

## 2. Roster v2（LEFT region）

降噪重做（RP-4 H10：v1 每卡常駐 active badge＋控制按鈕，`Roster.vue:68-88`）：

- **靜息乾淨**：卡片預設只顯 名(色點)／role／phase pill(含 elapsed)／`#task`／排程行；**控制收 hover 或 `⋯` overflow**（沿用 `Roster.vue:219-229` 的 hover 揭示，但更克制）。
- **色點 = per-agent 圖例**（`agentColor`）——roster 是全 app 配色的 legend（保留 v1 招牌，`colors.js`）。
- **異常才出 badge**：active 不顯、只顯 frozen/suspended（`Roster.vue:69`）。
- phase pill 用 `--phase-*` token（FE-1，對齊 TUI）＋形狀（FE-8 a11y）。
- 點卡 → INSPECTOR 開該成員 detail（FE-2 selected 語意）；`⋯` 內含：freeze/suspend/resume、schedule、skills、remove（非 leader）。
- `+ add agent` 在 roster 頭（`Roster.vue:52-54`）。

---

## 3. 成員增刪（RP-8）

### 3.1 Add agent（authoring flow）
重做 `NewAgentForm`（v1 `NewAgentForm.vue` 224 行單表單）為清楚的分段流程（`EvDialog` 內）：

1. **身分**：name、when_to_use（leader 用來決定何時找它）。
2. **人格**：system_prompt（多行；可給範本）。
3. **工具**：從 `api.tools`（`api.js:64`，協作工具已排除）勾 active / deferred；分類顯示。
4. **排程（選填）**：cron＋wake prompt（見 §4）。

送出 `createMember`（`api.js:60`）；只給 name 則掛載既有 on-disk dir（`MemberSpec`，`api.go:148-156`）。新增後系統發事件通知 leader（露 when_to_use，RP-8）——在 Timeline（FE-5）可見。

### 3.2 Remove
`removeMember`（`api.js:61-62`）走 FE-6 分級確認＋「也刪 on-disk 定義」checkbox（`SpaceView.vue:92-108`）。**leader 不可移除**（`Roster.vue:86-87`、RP-8 §3.E）。

---

## 4. 排程（RP-7 / RP-8）

操作者可為**任一成員（含 leader）**設排程（`api.js:66-68`；leader schedule 由 User 設、leader 不能改自己的，RP-7）：

- **cron 編輯器**：輸入框＋**人類可讀預覽**（「每 30 分鐘」「每天 09:00」）＋**下次觸發時間**預覽；非法 cron 即時提示（取代 v1 純輸入框 `Roster.vue:90-97`）。
- **wake prompt**（選填）：喚醒時注入 `<system-reminder>currenttime…, #{prompt}</system-reminder>`（RP-7 後端already）。
- 班表**常駐卡片**（`Roster.vue:75-78` 的 ⏰ 行）＋ Timeline 顯示喚醒事件。
- `clearSchedule`（`api.js:68`）清除。
- 執行中跳過本輪、leader 不可改自己的——這些是後端規則，前端**如實顯示**（如 leader 自排在 leader 視角唯讀）。

---

## 5. Agent Skills（RP-10）

重做 `SkillsPanel`（v1 213 行）：User-only 檢視/新增/刪除某成員的 skill（`api.js:71-74`）。

- **list**：name＋description（`SkillInfo`，`api.go:160-163`），即 prompt `# Skills` 區的那組 pair。
- **add**：name／description／body（首行成 `# <name> <description>`，其餘為指令；`SkillSpec`，`api.go:168-172`）；新增後**熱重載**該成員 prompt（接受 KV cache miss，RP-10 P2）。
- **delete**：FE-6 確認；提示「下次 run 起 prompt 重載、該成員不再看得到此 skill」（`SpaceView.vue:155-170`）。
- **紀律標語**：UI 明示「**skill 由 User 著作、agent 只載入不自建**」（RP-10）。

---

## 6. 外部事件可視化（RP-9）

讓操作者看見、並能接上外部 event 入口（`POST /api/swarm/{id}/event`，`api.go:307`，測試階段免 token / loopback）：

- **Event sources 小面板**（INSPECTOR 或 space menu）：列最近收到的外部事件（source/title/time/收件 leader）＋ idempotency。
- **接線指引**：顯示該 space 的 webhook URL ＋ 一段 `curl` 範例，讓使用者把外部 app（如 `localhost:7777` 交易 engine）接上（對齊 RP-9 用例）。
- 外部事件同時進 **Timeline**（FE-5）以 ⚡ 標記。
- **唯讀**：前端不偽造事件；只觀測與提供接線資訊。

---

## 7. 元件

```
roster/
  Roster.vue          # 靜息乾淨卡＋overflow 控制＋圖例
  MemberCard.vue      # 名/role/phase/task/排程行
  MemberMenu.vue      # ⋯ freeze/suspend/resume/schedule/skills/remove
compose/
  AddAgentDialog.vue  # 分段 authoring（身分/人格/工具/排程）
  ToolPicker.vue      # active/deferred 勾選（api.tools）
  ScheduleEditor.vue  # cron＋人類可讀＋下次觸發預覽
  SkillsPanel.vue     # list/add/delete＋熱重載＋紀律標語
  EventSources.vue    # 外部事件清單＋webhook 接線指引
```

---

## 8. 驗收

1. Roster 靜息乾淨（無常駐控制噪音）；控制在 hover/`⋯`；色點為全 app 圖例。
2. Add agent 走分段流程、可選工具/排程；leader 不可移除；新增通知 leader（Timeline 可見）。
3. 可為任一成員（含 leader）設排程；cron 有**人類可讀＋下次觸發**預覽與驗證；班表常駐。
4. Skills 可檢視/新增/刪除、熱重載；UI 明示「載入非著作」紀律。
5. 外部事件可見（清單＋Timeline ⚡），並提供 webhook 接線指引（URL＋curl）。

---

## 9. 子任務

| # | 子任務 |
| --- | --- |
| FE-7a | Roster v2＋MemberCard＋MemberMenu（降噪/overflow） |
| FE-7b | AddAgentDialog 分段 authoring＋ToolPicker |
| FE-7c | ScheduleEditor（cron 驗證＋人類可讀＋下次觸發） |
| FE-7d | SkillsPanel v2（list/add/delete＋熱重載＋紀律標語） |
| FE-7e | EventSources（外部事件清單＋webhook 接線指引） |
