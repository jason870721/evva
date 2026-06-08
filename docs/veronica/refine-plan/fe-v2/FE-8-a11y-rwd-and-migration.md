# FE-8 — 無障礙 ／ RWD ／ 打磨 ＋ 遷移 cutover

> 狀態：**草案 / Draft** ｜ 日期：2026-06-07 ｜ 類型：FE 實作 PRD（收尾＋遷移）
> 相依：FE-1~7（全部）｜ 對應：RP-4 §4.5（無障礙與韌性）、H4/H5/H8/H13、`web/embed.go`、`web/README.md`
> 系列：[FE v2 總覽](README.md)

---

## 1. 目標

把 v2 從「功能齊」推到「**可交付**」：無障礙達標、響應式不破版、四態齊全、效能可接受、i18n 留好 seam，最後**汰換 v1**。FE-8 的 parity checklist（§7）是「**能不能刪 `web/`**」的唯一閘門。

---

## 2. 無障礙（WCAG AA）

- **對比稽核**：neon 主色在 `#0A0A14` 上多半高對比，但需逐項驗 `--color-text-muted`(#7A7E94)、faint(#5E627A)、各 phase 色於小字情境 ≥ 4.5:1；不足者在 semantic 層調（不動 primitive 名）。midnight 主題同樣過稽核。
- **不只用顏色**（H5）：phase pill / board dot / attention chip / mail 狀態一律**形狀或文字雙編碼**（FE-4/5/7 已預留，FE-8 全盤驗收）。
- **鍵盤可達**：roster 上下鍵切換、stream `j/k`、gate `A/D/數字/Enter/Esc`（FE-6）、space 快切、view 切換皆可鍵盤操作；`:focus-visible` 全域（FE-1 token）。
- **ARIA / 語義**：roster=list/listitem、gate=dialog（focus-trap）、AttentionStrip=status、**stream=log（aria-live=polite）**讓螢幕報讀跟得上串流但不洗版；斷線 banner=alert。
- **skip link** 到主 workspace；`prefers-reduced-motion` 關掉 spinner/過場（用 `--dur-*` token 一次切）。

---

## 3. 響應式（最終斷點）

承 FE-2 的 region 骨架，收斂為：

| 斷點 | 版面 |
| --- | --- |
| ≥ 1200px | LEFT｜CENTER｜INSPECTOR 三欄（container query，非固定 px） |
| 860–1200px | 兩欄；INSPECTOR 浮層；Roster 可收 |
| < 860px | 單欄；Roster→TopBar 抽屜；view switch 保留；gate/inspector 全幅蓋層 |

- 寬度全走 `--layout-*` token（不寫死 `16rem/22rem`，解 H13）。
- 觸控目標 ≥ 44px；overflow 選單在窄屏改 bottom-sheet。

---

## 4. 四態齊全（H8）

每個資料面（roster/board/timeline/stream/completed/skills…）都備：

- **loading**：REST 未回前 **skeleton**（取代 v1 空白靠 2.5s 補，`SpaceView.vue:206-223`）。
- **empty**：有指引的空狀態（如 picker 的 `evva swarm .`，`SpacePicker.vue:28-31`）。
- **error**：區域級錯誤＋重試，不只全域紅字。
- **disconnected**：WS 斷線**全域 banner**＋自動重連倒數（升級 v1 角落小字，`SpaceView.vue:438-440`、RP-4 §4.5）。

---

## 5. 效能

- **stream 虛擬化**落地（FE-4 seam）：千則 turn 順捲；ToolCard result/diff 預設摺疊降 DOM。
- REST 對帳 debounce（FE-3）；firehose 過濾在 getter。
- bundle 預算：維持單一 embed dist；檢查 vue-router/pinia/markdown/i18n 後的體積，必要時 code-split（dynamic import view）。但 base `./`＋un-hashed 名須維持（embed 相容，`vite.config.js`）。

---

## 6. i18n scaffolding（不做完整翻譯）

- 導入 vue-i18n，字串外化為 key；預設 locale 跟隨 `uiStore`／瀏覽器。
- 出貨 `zh-TW`（主）＋ `en` 骨架兩本字典；對齊既有 [`user-guide-zh`](../../user-guide-zh.md) / [`user-guide-en`](../../user-guide-en.md) 的術語。
- **完整翻譯內容**留待另案（README §8）；本 PRD 只保證**抽字串完成、切語言可運作**。

---

## 7. Parity checklist（cutover 閘門）

v1 每個能力 → v2 對應，全綠才可刪 `web/`：

| v1 能力 | v1 來源 | v2 落點 | ✅ |
| --- | --- | --- | --- |
| token gate / space picker / 快切 | App/SpacePicker | FE-2 | ☐ |
| space 生命週期（run/stop/remove/reset/halt） | api.js:75-83 | FE-2＋FE-6 | ☐ |
| roster＋membership＋freeze/suspend/resume | Roster | FE-7＋FE-6 | ☐ |
| 5 態看板＋卡片展開 | TeamBoard | FE-5 | ☐ |
| completed 分頁（RP-6） | CompletedTasks | FE-5 | ☐ |
| Timeline（訊息→多源） | Timeline | FE-5 | ☐ |
| 串流 console（demux/coalesce） | MemberConsole+events | FE-4 | ☐ |
| transcript＋mailbox（color 路由/mailState） | AgentTranscript | FE-4 | ☐ |
| Attention（act/warn＋elapsed） | AttentionBar | FE-5 | ☐ |
| phase pill（RP-3 細相位/live 疊加） | events+Roster | FE-3＋FE-4 | ☐ |
| 審批 modal/tray＋佇列＋重放（RP-2） | ApprovalOverlay/Tray/Gate | FE-6 | ☐ |
| 問答（**+ multi-select 補洞**） | GateCard | FE-6 | ☐ |
| 確認對話（halt/reset/remove） | ConfirmDialog | FE-6 | ☐ |
| 成員增刪（RP-8） | NewAgentForm | FE-7 | ☐ |
| 排程 CRUD（RP-7/8） | Roster sched | FE-7 | ☐ |
| skills 檢視/增刪（RP-10） | SkillsPanel | FE-7 | ☐ |
| 外部事件（RP-9） | —（v1 無 UI） | FE-7＋FE-5 | ☐ |
| 穩定 per-agent 配色 | colors.js | FE-1 port | ☐ |

> 並補 v2 新增驗收（README §6）：主題切換無閃爍/無寫死色、`tsc` 綠、a11y AA、深連結。

---

## 8. Cutover 計畫

`go:embed` 不能用 `..`（`web/embed.go:5-9`），故 v2 於 `web2/` 自帶 `embed.go`（package `web2`）。步驟：

1. **共存期**：`web2/` 與 `web/` 並存；dev 用 `web2` vite，service 仍 embed `web`。
2. **切 import**：`internal/swarm/service` / `internal/swarm/webapi` 把 `web.Dist` 改成 `web2.Dist`（FE serve 來源換成 v2）。
3. **建置/CI**：CI `web` job 改 build `web2`、commit `web2/dist`（沿用「dist 入庫、un-hashed 名」紀律，`web/README.md`）；release 無 node step 仍能 embed。
4. **驗收 gate**：§7 parity 全綠 ＋ smoke test（`evva service start` → 打開 → 跑一輪 swarm，gate/board/stream/編組皆正常）。
5. **退役 v1**：刪 `web/`（或保留一版 tag 後刪）；文件指向 `web2/`。
6. **回滾**：cutover 前的 commit 可一鍵切回 `web.Dist`（import 單點切換）。

---

## 9. 驗收

1. axe / 手動稽核：對比、ARIA、鍵盤、reduced-motion 全過；兩個主題皆 AA。
2. 三斷點不破版；窄屏抽屜/bottom-sheet 可用。
3. 每個資料面四態齊全；WS 斷線全域 banner＋倒數。
4. 千則 turn stream 順捲（虛擬化生效）。
5. 切語言可運作（zh-TW/en 骨架）。
6. **Parity checklist 全綠** → service embed 切 `web2`、CI 綠、`web/` 退役。

---

## 10. 子任務

| # | 子任務 |
| --- | --- |
| FE-8a | 對比/ARIA/鍵盤/reduced-motion 稽核與修補（兩主題） |
| FE-8b | RWD 最終斷點＋抽屜/bottom-sheet＋觸控目標 |
| FE-8c | 四態（skeleton/empty/error/disconnected）全面鋪滿 |
| FE-8d | stream 虛擬化落地＋bundle 預算/code-split |
| FE-8e | vue-i18n scaffold＋zh-TW/en 骨架字典 |
| FE-8f | parity checklist 驗收＋cutover（embed 切 web2＋CI＋退役 v1） |
