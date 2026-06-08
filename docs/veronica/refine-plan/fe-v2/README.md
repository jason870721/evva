# Veronica — FE v2：Swarm Web UI 2.0 PRD 系列

> 狀態：**草案 / Draft（待 Johnny 拍板）** ｜ 日期：2026-06-07 ｜ 類型：FE 設計＋實作 PRD 系列
> 視角：以資深 FE/UX 視角，把 swarm web 從「邊長功能邊補 UI 的 v1」重設計成一套**全新的 2.0 工作站**。
> 接續：第二波 refine（[RP-5 ~ RP-10](../README.md#第二波-refine--優化rp-5--rp-10)）收尾 **之後** 的 FE v2 track。
> 母體：[RP-4 — Web UI/UX 檢討](../RP-4-web-ui-ux.md)（北極星來源，本系列承接並徹底落實）
> 上層：[設計總綱](../../veronica-design-v1.md)｜[扁平化溝通](../../direction-flat-comms.md)｜[roadmap](../../roadmap.md)

---

## 0. TL;DR

- **背景**：swarm 功能從 RP-1 一路長到 RP-10（roster / 5 態看板 / 審批 gates / timeline / 排程 / webhook / skills…）。v1 的 web 是「功能長一個、UI 補一塊」，已撞到**資訊密度與可維護性的天花板**——不是缺功能，是**缺結構**。
- **做什麼**：不再補丁式改 v1，而是**平行新建一套 Web UI 2.0**（Vue 3 + TypeScript + Pinia），達到功能 parity 後**汰換 v1**。
- **北極星**：沿用 RP-4 的重定義——**swarm operations console（監看＋介入）**，但這次用一套**設計系統＋型別化狀態層**從地基做對，而不是再分階段補。
- **三個招牌差異**：
  1. **NEON TOKYO 設計語言**——對齊 evva TUI 配色（[`pkg/ui/bubbletea/theme/palette.go`](../../../../pkg/ui/bubbletea/theme/palette.go)）＋**可切換主題**＋三層 token 化 CSS（未來擴充一個主題 = 新增一個 css 檔，零元件改動）。
  2. **agent stream 交互重設計**——多 agent 並發串流、thinking / tool-call / diff / 結果的可讀呈現，把「看一支正在工作的團隊」做成第一公民。
  3. **正式狀態層**——Pinia store + TS 型別，收掉目前 662 行的 `SpaceView` god-component。

---

## 1. 為什麼是「重寫」而不是「再補一輪」

v1 現況盤點（附 file:line 證據，延續 RP 系列的紀律）：

| 證據 | 位置 | 問題 |
| --- | --- | --- |
| `SpaceView.vue` **662 行** god-component | [`web/src/components/SpaceView.vue`](../../../../web/src/components/SpaceView.vue) | 一支元件同時扛：WS ingest、15 個子元件協調、11 種 REST command、gate 佇列、confirm、schedule、skills、reset/halt…難以再加功能而不增複雜度。 |
| 配色散落、半套 token | `App.vue:84-99`（`:root` 只定義了 `--bg/--accent/--fs-*`）+ 各元件寫死 `#22c55e/#f59e0b/#a855f7`（`Roster.vue:188-198`、`TeamBoard.vue:107-111`） | 沒有設計系統；RP-4 UX-4 只導入了 type-scale 子集。**完全不是 TUI 的 neon 配色**（v1 bg = `#0e1116`，TUI bg = `#0A0A14`）。 |
| 純 JS、無型別 | `web/src/events.js`、`api.js` | 與 Go 事件/REST 契約的對齊**只靠註解**維持（`events.js:6-10`）；契約一漂移就靜默壞掉。 |
| 主題不可切換 | 全 app | 使用者明確要的「可切換主題色、未來擴充 css 方便」——v1 沒有這個 seam。 |

> **結論**：瓶頸已從「缺功能」轉成「缺地基」。重寫**設計系統＋型別狀態層**這兩塊地基，比在 god-component 上繼續補丁更省、更快、更穩。已測且與框架無關的純邏輯層（`events.js` / `colors.js` 的 reducer 與 hash 配色）**保留並 port 成 `.ts`**，不浪費。

---

## 2. 設計語言：NEON TOKYO ＋可切換主題（本系列的招牌）

對應使用者核心訴求：「採用跟 evva tui 一樣的 tokyo neon 配色（可切換主題色、未來擴充 css 方便使用）」。完整規格見 **[FE-1](FE-1-foundations-theme-system.md)**，此處只給總綱。

### 2.1 三層 token 架構

```
Primitive（原色，主題專屬）   --neon-cyan: #05D9E8 ; --neon-violet: #B967FF ; …
        │  一張表 = 一個主題
        ▼
Semantic（語意，元件只讀這層） --color-accent ; --color-bg ; --phase-executing ; --status-error ; …
        │
        ▼
Component（元件級，選用）      --console-bg ; --card-border ; --pill-waiting-fg ; …
```

**鐵律**：元件 CSS **只引用 semantic / component token**——永不寫死 hex、永不直接引用 primitive。換主題 = 換掉 primitive→semantic 那張對照表，元件零改動。

### 2.2 NEON TOKYO（旗艦主題，移植自 TUI [`palette.go:15-60`](../../../../pkg/ui/bubbletea/theme/palette.go)）

| 語意 token | 取自 TUI | hex | 用途 |
| --- | --- | --- | --- |
| `--color-bg` | `bg` | `#0A0A14` | 終端深藍底 |
| `--color-surface` | `dim` | `#1B1B2F` | 面板 / 卡片 |
| `--color-text` | `fg` | `#E2E2FF` | 主文字（冷霧白） |
| `--color-text-muted` | `muted` | `#7A7E94` | 次要 chrome |
| `--color-accent` | `cyan` | `#05D9E8` | 主強調（連結 / 選取 / 焦點） |
| `--color-accent-2` | `purple` | `#B967FF` | 次強調 ＋ `--phase-waiting`（需要你） |
| `--phase-thinking` | `lightBlue` | `#7DF9FF` | LLM 生成中 |
| `--phase-executing` | `brown` | `#B87333` | 執行工具（銅） |
| `--color-tool-result` | `sky` | `#69B4FF` | 工具結果 |
| `--status-success` | `green` | `#39FF14` | 完成 / diff＋ |
| `--status-error` / `--color-danger` | `red` | `#FF003C` | 錯誤 / diff− |
| `--status-paused` | `yellow` | `#FAFC4E` | compacting / paused |
| `--color-info` | `blue` | `#5D5FEF` | 中性 info |
| `--color-flourish` | `magenta` | `#FF2A6D` | 點綴（謹用） |

> **跨 TUI/Web 色彩詞彙一致**：phase→色完全對齊 TUI（thinking=亮藍、executing=銅、result=天藍、waiting=violet、error=紅、paused=黃、success=綠）。在 TUI 與 Web 間切換的人，看到「**同一個狀態 = 同一個顏色**」。

### 2.3 主題切換機制

- `<html data-theme="neon-tokyo">`；切換 = 改 `data-theme` 值。
- `uiStore.theme`（Pinia）+ `localStorage` 持久化；首次以 `prefers-color-scheme` 為初值；以 inline boot script 避免 FOUC（無閃爍）。
- **新增主題 = 新增一個 `themes/<name>.css`**（只定義 primitive→semantic），在 store 註冊一個名字即可——零元件改動，這就是「未來擴充 css 方便使用」。
- 出貨即附**兩個主題**證明 seam：`neon-tokyo`（旗艦）＋ 一個對照主題（柔和暗色 `midnight`，或淺色），逼出 token 抽象的正確性。

---

## 3. 架構決策（已拍板 2026-06-07）

| 決策 | 選擇 | 一句話理由 |
| --- | --- | --- |
| Stack | **Vue 3 + TypeScript + Pinia + Vite** | 功能集持續長大，型別＋正式 store 提供結構與安全；收掉 god-component。 |
| 落地 | **平行新建（`web2/`）→ parity 後汰換 v1** | 「全新 2.0」不打斷正在運行的 v1；移植已測純邏輯層，達功能對等再 cutover。 |
| 廣度 | **完整 8 份 arc** | 覆蓋所有現有 swarm 功能的 v2 重設計，每份含 wireframe ＋驗收。 |

> `go:embed` 路徑不能用 `..`（見 [`web/embed.go:5-9`](../../../../web/embed.go)），故 `web2/` 需自帶 `web2/embed.go`（新 package `web2`）；**cutover = service 改 import `web2` ＋刪舊 `web/`**（細節見 [FE-8](FE-8-a11y-rwd-and-migration.md)）。

---

## 4. PRD 系列總覽

| # | 主題 | 一句話 | 相依 | 對應 RP |
| --- | --- | --- | --- | --- |
| [FE-1](FE-1-foundations-theme-system.md) | 地基：骨架＋設計 token＋主題系統 | Vue3+TS+Pinia 專案骨架、三層 token、NEON TOKYO 旗艦＋可切換主題、port 純邏輯層為 `.ts` | — | RP-4 UX-4 |
| [FE-2](FE-2-app-shell-and-ia.md) | 資訊架構＋ App 殼層 | 全域 chrome（topbar / space 切換 / 連線狀態 / 主題鈕）、版面區塊系統、entry/auth、space 生命週期安全位 | FE-1 | RP-4 §4.2 |
| [FE-3](FE-3-realtime-data-layer.md) | 即時資料層 | Pinia stores、WS ingest pipeline、REST 對帳、gate 佇列＋reconnect 重放/hydrate、command 通道＋樂觀更新 | FE-1 | RP-1/2/3 消費端 |
| [FE-4](FE-4-agent-stream-console.md) | **Agent stream console** | 多 agent 並發串流、thinking/tool/diff/result 可讀呈現、autoscroll/jump、member inspector、firehose vs focused | FE-1,3 | flat-comms、RP-1 §3.6 |
| [FE-5](FE-5-situational-awareness.md) | 態勢感知 | Attention 層 v2、5 態看板 v2（豐富卡片＋分頁）、Team Timeline v2（跨成員事件流） | FE-1,3 | RP-4 §4.1、RP-6 |
| [FE-6](FE-6-intervention-and-gates.md) | 介入＋安全 | 審批/問答 gate v2（modal/tray、佇列、鍵盤、**multi-select 補洞**、per-gate 錯誤）、破壞性操作分級確認、成員控制 | FE-1,3 | RP-2、RP-4 §4.3 |
| [FE-7](FE-7-team-composition-and-automation.md) | 團隊編組＋自動化 | Roster v2、成員增刪（authoring flow）、排程（cron 編輯/預覽）、skills 管理、外部事件可視化 | FE-1,2,3 | RP-7/8/9/10 |
| [FE-8](FE-8-a11y-rwd-and-migration.md) | 無障礙／RWD／收尾＋遷移 | WCAG AA、鍵盤/ARIA、RWD 斷點、四態（load/empty/error/skeleton）、效能/虛擬化、i18n scaffold、**cutover plan＋parity checklist** | 全部 | RP-4 §4.5 |

---

## 5. 落地順序與相依

```
            FE-1  地基（token / 主題 / 骨架 / 邏輯層 port）
              │
      ┌───────┴───────┐
      ▼               ▼
   FE-2 殼層/IA     FE-3 即時資料層
      │               │
      │      ┌────────┼────────┐
      │      ▼        ▼        ▼
      │   FE-4 stream FE-5 態勢 FE-6 介入/gates
      │      console
      └──────┴────────┴────────┘
              ▼
           FE-7 編組/自動化
              ▼
           FE-8 a11y / RWD / cutover
```

**建議序**：FE-1 →（FE-2 ∥ FE-3）→（FE-4 ∥ FE-5 ∥ FE-6）→ FE-7 → FE-8。
**紀律（finish before expand）**：先把地基（1–3）做對，再做功能面（4–7），最後才收尾與 cutover（8）。FE-8 的 parity checklist 是「能不能刪 v1」的唯一閘門。

---

## 6. 全域驗收（系列層級）

1. **Parity**：v1 能做的，v2 全能做（逐項 checklist 落在 FE-8）。
2. **體驗**：打開任一 space，3 秒內回答「**有什麼需要我**」；主題**一鍵切換、無閃爍、無寫死色**（抽查元件 CSS 無 hex）。
3. **工程**：`tsc` 零 error；純邏輯層的 `node --test` 全綠（沿用 v1 的 `events.test` / `colors.test`）；bundle 仍為單一 embed dist。
4. **無障礙**：主要文字 ≥ 12px、狀態非純色編碼、WS 斷線有全域提示、鍵盤可達、對比 ≥ AA。
5. **Cutover**：`service` embed 切到 v2、CI `web` job 綠、舊 `web/` 移除。

---

## 7. 與既有 RP 的關係

- **RP-4 是方向母體**：本系列把它從「在 v1 上分階段補丁（UX-1~4）」升級為「**設計系統重寫**」。RP-4 UX-1~4 已落地的成果（AttentionBar、ApprovalTray、Timeline、ConfirmDialog、type-scale…）視為 **v1 能力基線，v2 必須 ≥ 之**。
- **RP-1/2/3**（訊息可靠性／審批路由／細狀態）後端不動，v2 是其**消費端**（沿用 `phaseDeriver` 的 JS 雙生子、gate 重放 API）。
- **RP-6/7/8/9/10**（分頁／排程／編組／webhook／skills）後端 API 不動，v2 **重做其前端**。

---

## 8. 範圍邊界（系列）

- **In**：web 前端的全部（骨架、設計系統、狀態層、所有畫面、a11y、遷移）。
- **Out**：
  - 後端 API 變更——除非是 parity 缺口（如 multi-select 問答的後端支援若不足），另立 ticket，不夾帶。
  - 認證強化——維持 `root` 固定 token（資安議題，另案；見 RP-4 §6）。
  - i18n 完整翻譯——本系列只做 **scaffolding**（FE-8），實際 zh/en 字典另做。
  - 換掉 Go service / embed 模型——維持單一 binary embed dist。
