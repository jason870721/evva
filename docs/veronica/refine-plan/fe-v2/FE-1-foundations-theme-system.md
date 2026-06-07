# FE-1 — 地基：專案骨架 ＋ 設計 token ＋ NEON TOKYO 主題系統

> 狀態：**草案 / Draft** ｜ 日期：2026-06-07 ｜ 類型：FE 實作 PRD（地基）
> 相依：無（本系列地基，FE-2~8 全部依賴它）
> 系列：[FE v2 總覽](README.md)

---

## 1. 目標

把 Web UI 2.0 的地基一次做對，讓 FE-2~8 都站在型別與設計系統之上：

1. **專案骨架**：`web2/`，Vue 3 + TypeScript + Pinia + Vite，自帶 `embed.go`。
2. **設計 token 系統**：三層（primitive → semantic → component）CSS 變數架構。
3. **NEON TOKYO 旗艦主題**：移植 TUI [`palette.go`](../../../../pkg/ui/bubbletea/theme/palette.go)；＋第二主題證明可切換 seam。
4. **純邏輯層 port**：把 v1 已測、與框架無關的 `events.js` / `colors.js`（＋ `api.js` / `ws.js`）移植成型別化 `.ts`，沿用 `node --test`。
5. **型別契約**：一個 `types/` 模組，鏡射後端 `webapi` 的 `*Info` 與 `pkg/event` 的 `Kind`。
6. **基礎元件原子**：Button / Icon / Badge / Pill / Panel / Dialog 等 design-system atoms。

> 本 PRD **不做任何業務畫面**（roster/board/console 等在 FE-2 之後）。完成後應能跑起一個套用 NEON TOKYO、可切主題的空殼，且 `tsc` 與 `node --test` 全綠。

---

## 2. 範圍

- **In**：建置工具鏈、token 系統、主題切換、邏輯層 port、型別、原子元件、`embed.go`。
- **Out**：任何 swarm 業務畫面與 store（FE-2/FE-3 起）。

---

## 3. 專案骨架

```
web2/
├── embed.go                # package web2; //go:embed all:dist （新 embed，cutover 在 FE-8）
├── index.html
├── package.json            # vue ^3.5 / pinia / typescript / vite / @vitejs/plugin-vue / vue-tsc
├── tsconfig.json
├── vite.config.ts          # base:'./' + 穩定 un-hashed 資產名（沿用 v1 vite.config.js 策略）
└── src/
    ├── main.ts             # createApp + createPinia + 掛 theme boot
    ├── App.vue             # 殼層（FE-2 接手）；FE-1 階段只放 ThemeProbe 展示頁
    ├── lib/                # ← 純邏輯層（port 自 v1，可 node --test）
    │   ├── events.ts  events.test.ts
    │   ├── colors.ts  colors.test.ts
    │   ├── api.ts
    │   └── ws.ts
    ├── types/              # ← 型別契約（鏡射後端）
    │   ├── wire.ts         # MemberInfo / TaskInfo / MessageInfo / SkillInfo / SpaceInfo …
    │   └── events.ts       # EventKind union + payload 介面
    ├── stores/             # Pinia（FE-3 起；FE-1 只放 ui.ts）
    │   └── ui.ts           # theme / gateMode / 偏好
    ├── components/base/    # ← design-system 原子
    │   ├── EvButton.vue  EvIcon.vue  EvBadge.vue  EvPill.vue
    │   ├── EvPanel.vue   EvDialog.vue EvTooltip.vue EvSpinner.vue
    └── styles/             # ← token 系統
        ├── reset.css
        ├── tokens.primitive.neon-tokyo.css   # 主題 A：原色
        ├── tokens.primitive.midnight.css     # 主題 B：原色（證明 seam）
        ├── tokens.semantic.css               # primitive → semantic 對照（與主題無關的語意名）
        ├── tokens.component.css              # 元件級 token
        └── base.css                          # typography / spacing / radius / motion scale
```

> 沿用 v1 的「**dist 入庫、un-hashed 資產名**」策略（[`web/vite.config.js:9-24`](../../../../web/vite.config.js)、[`web/README.md`](../../../../web/README.md)），讓 `go build` 無 node step 也能 embed 一份可用 UI。

---

## 4. 設計 token 系統（核心）

### 4.1 三層與鐵律

```
primitive   主題專屬原色      tokens.primitive.<theme>.css   （切主題＝換這層）
   ▼
semantic    語意名（與主題無關） tokens.semantic.css           （元件只讀這層 + component）
   ▼
component   元件級別名         tokens.component.css
```

**鐵律**：`components/**` 與業務 `.vue` 的 CSS **只能引用 semantic / component token**——不得寫死 hex、不得直接引用 `--neon-*` primitive。FE-8 用 lint/grep 抽查把關（驗收 §8）。

### 4.2 primitive（NEON TOKYO，逐一對齊 `palette.go`）

```css
/* tokens.primitive.neon-tokyo.css — 移植自 pkg/ui/bubbletea/theme/palette.go */
:root[data-theme='neon-tokyo'] {
  --neon-bg:          #0A0A14; /* palette.go bg  — abyssal navy            */
  --neon-surface:     #1B1B2F; /* palette.go dim — panel                  */
  --neon-fg:          #E2E2FF; /* palette.go fg  — cool fog white          */
  --neon-muted:       #7A7E94; /* palette.go muted                        */
  --neon-think:       #5E627A; /* palette.go think — quiet aside           */
  --neon-cyan:        #05D9E8; /* palette.go cyan  — primary               */
  --neon-violet:      #B967FF; /* palette.go purple — secondary            */
  --neon-copper:      #B87333; /* palette.go brown — tool / executing      */
  --neon-sky:         #69B4FF; /* palette.go sky   — tool result           */
  --neon-yellow:      #FAFC4E; /* palette.go yellow — compacting / paused  */
  --neon-green:       #39FF14; /* palette.go green  — success / diff+       */
  --neon-red:         #FF003C; /* palette.go red    — error / diff−         */
  --neon-light-blue:  #7DF9FF; /* palette.go lightBlue — thinking          */
  --neon-blue:        #5D5FEF; /* palette.go blue   — info                  */
  --neon-magenta:     #FF2A6D; /* palette.go magenta — flourish            */
  --neon-white:       #FFFFFF;
  --neon-diff-add-bg: #1a3a1a; /* palette.go diffAddBg                      */
  --neon-diff-del-bg: #3a1a1a; /* palette.go diffRemoveBg                   */
}
```

### 4.3 semantic（元件實際引用的層；與主題無關的名字）

```css
/* tokens.semantic.css — 同一份語意名，每個主題的 primitive 各自映射 */
:root {
  /* 表面 / 文字 */
  --color-bg: var(--neon-bg);
  --color-surface: var(--neon-surface);
  --color-surface-2: color-mix(in srgb, var(--neon-surface) 70%, var(--neon-bg));
  --color-line: color-mix(in srgb, var(--neon-fg) 14%, transparent);
  --color-text: var(--neon-fg);
  --color-text-muted: var(--neon-muted);
  --color-text-faint: var(--neon-think);

  /* 強調 */
  --color-accent: var(--neon-cyan);
  --color-accent-2: var(--neon-violet);
  --color-info: var(--neon-blue);
  --color-danger: var(--neon-red);
  --color-flourish: var(--neon-magenta);

  /* phase 詞彙（對齊 TUI；FE-3 的 phaseClass 消費） */
  --phase-thinking: var(--neon-light-blue);
  --phase-executing: var(--neon-copper);
  --phase-waiting: var(--neon-violet);   /* 需要你 — 最醒目 */
  --phase-error: var(--neon-red);
  --phase-paused: var(--neon-yellow);
  --phase-idle: var(--neon-muted);
  --color-tool-result: var(--neon-sky);

  /* 任務狀態（5 態看板，對齊 TUI 語意色） */
  --status-pending: var(--neon-muted);
  --status-running: var(--neon-cyan);
  --status-suspended: var(--neon-yellow);
  --status-verifying: var(--neon-violet);
  --status-completed: var(--neon-green);

  /* diff */
  --diff-add-bg: var(--neon-diff-add-bg);
  --diff-del-bg: var(--neon-diff-del-bg);
  --diff-fg: var(--neon-white);
}
```

> `color-mix()` 讓 `--color-line` / surface-2 從主色衍生，換主題自動跟著走，不必逐主題重定。

### 4.4 typography / spacing / radius / motion scale（`base.css`）

- **type scale**：`--fs-xs .75rem / --fs-sm .8125rem / --fs-md .9rem / --fs-lg 1.05rem / --fs-xl 1.3rem`（floor 12px，延續 RP-4 UX-4 但收斂為單一階梯）。
- **mono**：`--font-mono`（id / tool / 時間戳）。
- **space**：`--sp-1…6`（4/8/12/16/24/32）。
- **radius**：`--r-sm 4 / --r-md 6 / --r-lg 8 / --r-pill 999`。
- **motion**：`--ease-out` / `--dur-fast 120ms / --dur-base 200ms`；尊重 `prefers-reduced-motion`（FE-8 收尾）。

---

## 5. 主題切換 seam

```ts
// stores/ui.ts
export const THEMES = ['neon-tokyo', 'midnight'] as const
export type ThemeName = (typeof THEMES)[number]

export const useUiStore = defineStore('ui', {
  state: () => ({ theme: readInitialTheme() as ThemeName }),
  actions: {
    setTheme(t: ThemeName) {
      this.theme = t
      document.documentElement.dataset.theme = t
      localStorage.setItem('evva-theme', t)
    },
  },
})
```

- 初值：`localStorage('evva-theme')` → 否則 `prefers-color-scheme` → 預設 `neon-tokyo`。
- **無 FOUC**：`index.html` `<head>` 內嵌一段極短 inline script，在 Vue 掛載前就把 `data-theme` 寫上 `<html>`。
- **新增主題的代價**＝新增一個 `tokens.primitive.<name>.css`（`:root[data-theme='<name>']{…}`）＋在 `THEMES` 加一個名字。**元件零改動**——這就是「未來擴充 css 方便使用」。
- 第二主題 `midnight`：刻意換掉整組 primitive（柔和暗色，降低 neon 飽和）以**逼出抽象正確性**（若有元件偷寫 primitive 或 hex，換到 midnight 就會露餡）。

---

## 6. 純邏輯層 port（保留 v1 的驗證資產）

把 v1 這些**與 Vue 無關、已測**的純函式原樣 port 成 `.ts`，補上型別，沿用 `node --test`：

| v1 | → web2 | 內容 |
| --- | --- | --- |
| [`web/src/events.js`](../../../../web/src/events.js) | `lib/events.ts` | `reduceChat` / `reducePhase` / `phaseFor` / `displayPhase` / `phaseClass` / `attentionItems` / `relTime` / `elapsed` / `mailState` …（**這是與 Go `phaseDeriver` 對齊的 JS 雙生子，務必逐函式保形**） |
| [`web/src/colors.js`](../../../../web/src/colors.js) | `lib/colors.ts` | `agentColor`（FNV-1a 穩定 per-agent 色）；PALETTE 重新針對 `#0A0A14` 微調對比（FE-4 驗證） |
| [`web/src/api.js`](../../../../web/src/api.js) | `lib/api.ts` | REST client，回傳型別化 `types/wire.ts` |
| [`web/src/ws.js`](../../../../web/src/ws.js) | `lib/ws.ts` | WS bridge，`onEvent` 帶 `WireEvent` 型別 |

> `events.test.js` / `colors.test.js` 一起 port（`node --test` 對 `.ts` 用 `--experimental-strip-types` 或先 build）。**移植驗收 = 兩份測試全綠**，確保語意零漂移。

---

## 7. 型別契約（單一真相）

`types/wire.ts` 逐欄鏡射 [`internal/swarm/webapi/api.go`](../../../../internal/swarm/webapi/api.go) 的 `*Info`：

```ts
// types/wire.ts — 鏡射 webapi.MemberInfo (api.go:127-143) 等
export interface MemberInfo {
  name: string; agentId: string; role: string; membership: string
  run: 'idle' | 'busy' | 'suspended'
  phase?: string; tool?: string; phaseSince?: number
  currentTask: number; whenToUse?: string
  cron?: string; schedulePrompt?: string
}
export interface TaskInfo { id: number; title: string; spec: string; status: TaskStatus; assignee: string; createdBy: string; result?: string; verifyNote?: string; parentId?: number; createdAt: number; updatedAt: number }
export type TaskStatus = 'pending' | 'running' | 'suspended' | 'verifying' | 'completed'
export interface MessageInfo { id: string; sender: string; recipient: string; subject?: string; body: string; refTask?: number; readAt?: number; claimedAt?: number; createdAt: number }
export interface SkillInfo { name: string; description: string }
export interface SpaceInfo { id: string; name: string; workdir: string; status: 'running' | 'stopped'; members: number }
```

`types/events.ts` 鏡射 [`pkg/event/event.go:34-155`](../../../../pkg/event/event.go) 的 `Kind` 為 union：

```ts
export type EventKind =
  | 'idle' | 'run_start' | 'run_resume' | 'run_end' | 'run_cancelled' | 'iter_limit'
  | 'turn_start' | 'turn_end' | 'thinking' | 'thinking_chunk' | 'text' | 'text_chunk'
  | 'tool_use_start' | 'tool_use_result' | 'approval_needed' | 'question_needed'
  | 'compacting' | 'compacting_end' | 'error' | 'store_update' | 'usage'
  | 'mode_changed' | 'bg_result' | 'monitor_event'
  | 'drain_background_task' | 'drain_monitor_events' | 'drain_inbox'
export interface WireEvent { Kind: EventKind; AgentID: string; Time: string; /* + 各 payload 指標 */ }
```

> 後端契約變了，TS 編譯就會紅 → 把 RP 系列「靠註解對齊」的脆弱點換成編譯期保證。

---

## 8. 基礎元件原子（design-system atoms）

無業務邏輯、只吃 token 的可重用件，供 FE-2~7 組裝：

- **`EvButton`**：`variant=primary|ghost|danger`、`size`、`icon?`、`loading?`（取代 v1 散落的 `.primary/.ghost/.danger` 規則，見 `App.vue:116-139`）。
- **`EvIcon`**：精簡 inline SVG set（freeze ❄ / suspend ⏸ / resume ▶ / halt ■ / approval 🛡 / executing ▶ / thinking … / waiting ⏳ / schedule ⏰），對應 RP-4 §4.4 的 icon 願景；icon＋文字並存（a11y）。
- **`EvBadge` / `EvPill`**：狀態徽章與 phase pill；吃 `--phase-*` / `--status-*` token，**形狀＋文字雙編碼**（非純色，為 FE-8 a11y 鋪路）。
- **`EvPanel`**：卡片/面板容器（surface + line + radius）。
- **`EvDialog`**：modal/dialog 基座（focus-trap、Esc 關、scrim）；FE-6 的 gate / confirm 與 FE-7 的表單都基於它。
- **`EvTooltip` / `EvSpinner`**：tooltip 與 neon 載入指示（thinking spinner 用 `--phase-thinking`）。

---

## 9. 驗收

1. `cd web2 && npm i && npm run build` 產出 `web2/dist`，`tsc`/`vue-tsc` **零 error**。
2. 啟動空殼套用 **NEON TOKYO**：bg 為 `#0A0A14`、文字冷霧白、強調電光青——與 TUI 同調。
3. 主題鈕在 `neon-tokyo` ↔ `midnight` **一鍵切換、無閃爍**；重整後記住選擇。
4. `grep -RInE '#[0-9a-fA-F]{3,6}' src/components` **僅命中** `styles/tokens.primitive.*`（元件零寫死色）。
5. `node --test`（port 後的 `events.test` / `colors.test`）**全綠**。
6. `EvButton/EvPill/EvDialog` 等原子可在 demo 頁渲染，換主題時色彩跟著走。

---

## 10. 子任務

| # | 子任務 | 產出 |
| --- | --- | --- |
| FE-1a | 專案骨架＋`embed.go`＋vite/tsconfig | `web2/` 可 build、可 dev |
| FE-1b | 純邏輯層 port（events/colors/api/ws → .ts）＋測試 | `node --test` 綠 |
| FE-1c | 型別契約（wire.ts / events.ts） | `tsc` 綠、鏡射後端 |
| FE-1d | token 系統三層＋NEON TOKYO primitive | 套用旗艦主題 |
| FE-1e | 主題切換 store＋第二主題＋無 FOUC boot | 一鍵切換驗收 |
| FE-1f | 原子元件（Button/Icon/Badge/Pill/Panel/Dialog/Tooltip/Spinner） | demo 頁 |
