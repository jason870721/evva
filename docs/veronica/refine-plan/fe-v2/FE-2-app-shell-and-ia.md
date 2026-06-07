# FE-2 — 資訊架構 ＋ App 殼層

> 狀態：**草案 / Draft** ｜ 日期：2026-06-07 ｜ 類型：FE 實作 PRD（IA / 殼層）
> 相依：FE-1 ｜ 對應：RP-4 §4.2（資訊層級與版面）、H2/H7/H13
> 系列：[FE v2 總覽](README.md)

---

## 1. 目標

定義「**東西都放在哪**」——把 v1 的「token-gate →整頁 picker →單一 3 欄 SpaceView」升級為一個有持久 chrome、可深連結、響應式的 **operations console 殼層**。本 PRD 只做殼層與導航，**各區內容**由 FE-3~7 填。

---

## 2. v1 IA 的問題（證據）

| 問題 | 證據 | 後果 |
| --- | --- | --- |
| 殼層與內容糾纏 | `SpaceView.vue:421-557` header＋grid＋所有 overlay 全擠一支 | 加任何全域元素都要動 662 行的檔 |
| 破壞性操作在主區、零/不對稱確認 | `SpaceView.vue:434-435` `halt all` / `reset` 直接擺 header（RP-4 H2） | 誤觸半徑＝全團 |
| 無深連結 | 全 app 單一 `active` ref（`App.vue:13`），無 URL | 無法 bookmark「某 space 的某成員」 |
| focused vs selected 語意重疊 | `SpaceView.vue:53-54, 346-357` 一次點擊改兩者（RP-4 H7） | 不知道在看即時還是歷史 |
| 固定欄寬、無 RWD | `SpaceView.vue:600-609` `16rem｜1fr｜22rem`（RP-4 H13） | 小螢幕破版 |

---

## 3. IA：region 模型

```
┌─ TopBar（全域 chrome，恆顯）──────────────────────────────────────────────┐
│ evva·swarm ▸ [space ▾]   ● live   ◑ theme   ⟳   ⚙ space menu(halt/reset/stop) │
├─ AttentionStrip（FE-5 填，FE-2 留位）─────────────────────────────────────┤
│  ⏳ qa waiting-approval 2:41   ⚠ backend-b error            [全部 →]          │
├──────────────┬───────────────────────────────────────┬─────────────────────┤
│  LEFT        │  CENTER（workspace）                   │  INSPECTOR（contextual）
│  Roster      │  ┌ view switch ───────────────────┐    │  焦點成員 detail / task
│  (FE-7)      │  │ Board · Timeline · Stream · Done│    │  detail / gate detail
│              │  └─────────────────────────────────┘    │  （可收合）
│              │  ↑ 當前 view 給滿高度（不再 40/60 硬切） │
├──────────────┴───────────────────────────────────────┴─────────────────────┤
│  OVERLAY 層：gates(modal/tray, FE-6)、confirm(FE-6)、表單(FE-7)              │
│  GLOBAL 層：WS 斷線 banner（FE-3）、toast                                     │
└─────────────────────────────────────────────────────────────────────────────┘
```

- **TopBar**：品牌、**space 快切下拉**（不必回整頁 picker）、全域連線/健康燈、主題鈕、手動刷新、**`⚙ space menu`**——把 `halt / reset / stop / remove` 從主區挪進選單（降誤觸，RP-4 §4.3）。
- **AttentionStrip**：恆顯一行，內容由 FE-5 提供；FE-2 只保證它有固定的位置與骨架。
- **LEFT / Roster**：團隊目錄與控制（FE-7），桌機常駐、窄螢幕收抽屜。
- **CENTER / workspace**：主舞台，**view switch**（Board / Timeline / Stream / Completed）取代 v1 中欄上下硬切；當前 view 吃滿高度。
- **INSPECTOR**：**contextual** 右欄——依當前選取顯示 member detail（transcript＋mailbox）、task detail 或 gate detail；可收合（解 RP-4 H7：明確「即時在 CENTER、歷史在 INSPECTOR」）。

---

## 4. 導航與路由（深連結）

引入 **vue-router**，URL ＝ 狀態（ops console 值得 bookmark / 分享）：

| URL | 畫面 |
| --- | --- |
| `/` | landing：space picker（無 active space 時） |
| `/s/:spaceId` | 工作站，預設 view = board |
| `/s/:spaceId/board` `…/timeline` `…/stream` `…/completed` | 指定 CENTER view |
| `/s/:spaceId/stream/:member` | stream 聚焦某成員（focused） |
| `/s/:spaceId/m/:member` | INSPECTOR 開某成員 detail（selected） |
| `/s/:spaceId/t/:taskId` | INSPECTOR 開某 task detail |

- **focused（CENTER stream 聚焦）** 與 **selected（INSPECTOR 打開）** 由不同 URL 段表達，**語意分離**（解 H7）——點 roster 成員預設只 `selected`（開 inspector）；點「在 stream 看」才 `focused`。
- 路由守衛：`stopped` 的 space 不可進工作站（沿用 `SpacePicker.vue:10-13` 規則），導回 landing 並提示 `evva swarm run`。

---

## 5. Entry / Auth / Space 生命週期

- **Token gate**：保留（service 印出的 session token，`App.vue:50-63`）；維持 `root` 預設（RP-4 §6 範圍外）。做成 `EvDialog` 而非整頁，連線後進 landing。
- **Space picker（landing）**：沿用 `SpacePicker.vue` 的卡片＋空狀態指引（`evva swarm .`），但卡片加「members / phase 摘要 / workdir」；running 可進、stopped 給 run 指令提示。
- **Space 快切**：TopBar 下拉列出所有 running space，點即 router 切換，不回 landing。
- **生命週期動作**（`run / stop / remove / reset / halt`，API 見 [`api.js:75-83`](../../../../web/src/api.js)）：全部收進 `⚙ space menu`，**破壞性者一律走 FE-6 的分級確認**（halt all＝二次確認）。

---

## 6. 響應式 region 收合（骨架，FE-8 收尾）

| 斷點 | 行為 |
| --- | --- |
| ≥ 1200px | 三 region 並列（LEFT｜CENTER｜INSPECTOR） |
| 860–1200px | INSPECTOR 浮層化（點成員才滑出）；LEFT｜CENTER 兩欄 |
| < 860px | 單欄；Roster 收 TopBar 抽屜；view switch 仍在；INSPECTOR 全幅蓋層 |

> 用 CSS grid + container query；region 寬度走 `--layout-*` token，不寫死（解 H13）。

---

## 7. 元件

```
App.vue → RouterView
 shell/
  TopBar.vue          # 品牌 / SpaceSwitcher / 連線燈 / ThemeToggle / SpaceMenu
  SpaceSwitcher.vue   # running space 下拉快切
  ThemeToggle.vue     # 切 uiStore.theme（FE-1）
  SpaceMenu.vue       # halt/reset/stop/remove（→ FE-6 確認）
  AppLayout.vue       # region grid（LEFT/CENTER/INSPECTOR）＋ RWD 收合
  Inspector.vue       # contextual 右欄殼（內容槽：member/task/gate）
views/
  LandingView.vue     # picker（含 TokenGate）
  WorkspaceView.vue   # 殼內主視圖，掛 view switch + <RouterView> 子層
  BoardView / TimelineView / StreamView / CompletedView   # FE-4/5 填內容
```

---

## 8. 驗收

1. TopBar 恆顯；space 快切不回 landing；連線燈、主題鈕、`⚙ menu` 可用。
2. 破壞性動作**只**在 `⚙ space menu`，不在主區可誤觸位置。
3. URL 深連結：直接打 `/s/:id/stream/:member` 能還原該畫面；重整不丟狀態。
4. focused 與 selected 語意分離（點成員 → inspector；明確動作才聚焦 stream）。
5. 三斷點不破版；INSPECTOR 在中斷點浮層化、窄屏 Roster 抽屜化。
6. 殼層不含任何業務資料抓取（資料層在 FE-3）。

---

## 9. 子任務

| # | 子任務 |
| --- | --- |
| FE-2a | vue-router＋URL 狀態模型＋守衛 |
| FE-2b | TopBar（含 SpaceSwitcher / ThemeToggle / SpaceMenu） |
| FE-2c | AppLayout region grid＋RWD 收合骨架 |
| FE-2d | Inspector 殼（contextual 內容槽） |
| FE-2e | Landing（TokenGate＋picker）＋空狀態 |
