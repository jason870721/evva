# RP-4 — Web UI/UX 設計檢討與改版方向

> 狀態：**草案 / Draft（設計方向，待拍板）** ｜ 日期：2026-06-05 ｜ 類型：UI/UX review
> 視角：以一位資深 UI/UX 設計師的角度，對目前 swarm web 工作站做啟發式評估（heuristic
> evaluation）＋提出改版方向。**本文是方向提案，不含像素級規格、也尚未實作。**
> 關聯：[`../direction-flat-comms.md`](../direction-flat-comms.md)（扁平化溝通）、[RP-3](RP-3-agent-run-phase-states.md)（細狀態，已落地）、[RP-2](RP-2-permission-broker-routing.md)（審批佇列/重放，已落地）

---

## 1. TL;DR — 一句話與北極星

目前的 web 把 swarm 當成一個「**多分頁的聊天 App**」來呈現：登入 → 選 space → 跟某個成員
聊天。但 swarm 的本質是「**一支正在自主工作的團隊**」，操作者的核心任務不是聊天，而是
**監看（situational awareness）＋必要時介入（intervene）**。

> **北極星重定義：把它從「a chat app for agents」改設計成「a swarm operations console」**
> —— 心智模型接近 **航管 / SRE 戰情室 / CI 儀表板**：一眼看出「誰在做什麼、哪裡卡住、
> 什麼需要我」，再一鍵切進細節處理。

現有實作其實**地基不錯**（穩定 per-agent 配色、扁平化 demux、剛補上的 phase pill 與審批
佇列），問題集中在**資訊層級、注意力導引、危險操作防呆、密度/可讀性與無障礙**。本文用
**啟發式評估**列出問題（附嚴重度），再給**分主題、分階段**的改版方向與兩張 wireframe 草圖。

---

## 2. 現況盤點（先肯定可取之處）

| 元件 | 角色 | 做得好的地方 |
| --- | --- | --- |
| `App.vue` | token gate → picker → space | 流程清晰；token 存 localStorage 只輸入一次 |
| `SpacePicker.vue` | 子集團清單 | 卡片式、空狀態有指引（`evva swarm .`） |
| `SpaceView.vue` | 主工作區（3 欄） | 單一 WS 流 + 2.5s REST 輪詢；reset 有 confirm |
| `Roster.vue` | 成員目錄（左） | membership/phase/task 一覽 + 內聯控制 |
| `TeamBoard.vue` | 5 態看板（中上） | 對齊 task 狀態機；欄首有計數 |
| `MemberConsole.vue` | 焦點成員串流（中下） | 扁平化（對 leader/worker 一致）；自動捲動 |
| `AgentTranscript.vue` | 成員 transcript + mailbox（右） | sender→recipient 配色路由可掃視 |
| `colors.js` | 穩定 per-agent 配色 | FNV 雜湊穩定色、user/all 固定色，**全 app 一致**（招牌優點） |

**整體基調**：dark theme（bg `#0e1116`）、技術 id 用 mono、狀態色（amber/green/red/purple）
使用一致。這些是好底子，改版應**保留並系統化**，而非推倒。

---

## 3. 啟發式評估（Nielsen heuristics × 嚴重度）

> 嚴重度：🔴 高（傷害核心任務 / 有風險）｜🟠 中（明顯摩擦）｜🟡 低（打磨）

| # | 問題 | Heuristic | 嚴重度 | 證據 |
| --- | --- | --- | --- | --- |
| H1 | **沒有「需要我關注」的總覽**：哪個成員 `waiting-approval`、哪個 error、哪個卡很久，只能自己掃 roster 或等 modal 跳 | Visibility of system status | 🔴 | 無 attention 區；phase 只在 roster pill |
| H2 | **「Halt all」零確認**，一鍵停掉整團；reset 卻有確認 → 危險不對稱 | Error prevention | 🔴 | `SpaceView.vue` `@click="memberCmd('halt')"` vs `resetSpace()` 有 confirm |
| H3 | **審批是全螢幕 hard modal scrim**，多 agent 監看時被打斷；佇列雖修了互蓋，但「邊看邊決定」做不到 | Flexibility & efficiency | 🟠 | `ApprovalOverlay.vue` `.scrim{position:fixed;inset:0}` |
| H4 | **字級過小**（多處 0.62–0.7rem ≈ 10–11px）、密度高 → 掃視吃力、低於無障礙舒適線 | Aesthetic / accessibility | 🟠 | 各 `.vue` font-size |
| H5 | **狀態幾乎只靠顏色編碼**，色盲使用者難分（phase/board dot/ws）；雖多半有文字輔助但 board dot 純色 | Accessibility | 🟠 | `TeamBoard.vue` `.dot.*`、`Roster` badge |
| H6 | **沒有時間軸 / team activity feed**：看不到「leader 指派了 #3 給 qa」「qa→leader：done」這種團隊事件流；只能逐成員 console 看 | Match system & real world | 🟠 | 無 timeline；inter-agent 訊息只在右欄 mailbox |
| H7 | **focused（console）vs selected（transcript）語意重疊**：一次點擊同時改兩者，且 transcript 與 console 內容部分重複、未標示「歷史 vs 即時」 | Consistency / recognition | 🟠 | `selectMember()` 同時 set `focused`+`selected` |
| H8 | **缺 loading / skeleton**：REST 未回前空白，靠 2.5s 輪詢補；WS 狀態只在 console 角落小字 | Visibility of system status | 🟡 | `refreshSnapshots()` 無 loading 態 |
| H9 | **board 資訊太薄**：卡片無 assignee 配色、無時間、不可點看細節；5 欄擠在 40% 高 → task 一多就很擠 | Recognition / scalability | 🟠 | `TeamBoard.vue` grid 5×、card 無互動 |
| H10 | **roster 噪音**：每張卡常駐 `active` badge（多數成員都 active）＋常駐 freeze/suspend 按鈕 → 視覺雜訊 | Aesthetic & minimalist | 🟡 | `Roster.vue` line2 + `.ctl` 常駐 |
| H11 | **全文字、無 icon**：freeze/suspend/halt/close… 都是文字標籤，掃視與國際化成本高、佔空間 | Recognition / efficiency | 🟡 | 各元件 |
| H12 | **window.confirm 原生彈窗**（reset）風格突兀、與 app 視覺斷裂；大小寫不一致（"Allow once" vs "freeze"/"halt all"） | Consistency & standards | 🟡 | `resetSpace()`、按鈕標籤 |
| H13 | **無 RWD**：固定 `16rem｜1fr｜22rem` + board 5 固定欄，小螢幕直接破版；無 keyboard 操作（除 Enter 送出） | Flexibility / accessibility | 🟡 | `SpaceView.vue` `.grid` 固定欄寬 |
| H14 | **錯誤呈現薄弱**：`err` 一行紅字；`command_error`（RP-2 新增）目前只塞進同一個 `err`，無 per-gate 對應 | Help users recognize errors | 🟡 | `SpaceView.vue` `err` |

---

## 4. 改版方向（分主題、附 wireframe）

### 4.1 主題一：注意力與態勢感知（最高優先 —— 對應 H1/H3/H6）

**核心理念**：操作者打開 web 的第一個問題永遠是「**有什麼需要我？**」。要有一個**恆定可見的
attention 層**，把「需要人介入」的東西聚合上浮。

**(a) 頂部 Attention Bar（恆顯）** —— 聚合三類「需要我」：① `waiting-approval`/`waiting-input`
的成員數（可點開審批）② `error`/卡住（某 phase 停留過久，接 RP-3 的 stall 偵測）③ 待辦提示。
平時安靜（一行淡色「all clear」），有事才變色＋計數＋可點跳轉。

**(b) 審批改「可選非阻斷」** —— 保留 modal 作為「安全強制」的預設，但提供一個**側邊 approvals
tray**：多 agent 同時要審批時，操作者能邊看團隊邊逐一決定，而不是被全螢幕 scrim 鎖死。
（佇列資料已具備，見 RP-2 §3.2。）modal vs tray 可做成偏好設定。

**(c) Team Activity Timeline（新檢視）** —— 一條**跨成員的事件流**：任務指派/狀態轉移、
inter-agent 訊息、審批、成員上下線，依時間排序、用 per-agent 色標記 sender。這正是
[`direction-flat-comms.md`](../direction-flat-comms.md)「全員可觀測」缺的那塊，也順手收掉
RP-1 §3.6（inter-agent 訊息可見化）。

```
┌─ Attention ───────────────────────────────────────────────── ● 2 need you ─┐
│  ⚠ qa waiting-approval: bash · 2:41    ⚠ backend-b error    [review →]      │
├──────────────┬──────────────────────────────────────┬──────────────────────┤
│  ROSTER      │  ▸ Board    ▸ Timeline    ▸ Console   │  DETAIL (qa)         │
│  ● lead  …   │  （分頁，不再上下硬切 40/60）          │  transcript          │
│  ● qa  ⚠wait │                                       │  mailbox             │
│  ● fe  ▶exec │                                       │                      │
└──────────────┴──────────────────────────────────────┴──────────────────────┘
```

### 4.2 主題二：資訊層級與版面（H7/H9/H13）

- **中欄改「分頁/可切」而非上下硬切 40/60**：Board｜Timeline｜Console 三個 tab，把整個高度
  讓給當前關注的東西（看板要看時給滿、盯某成員時 console 給滿）。預設 Board。
- **釐清 focused vs selected**：一個明確的「**目前聚焦成員**」概念貫穿（roster 高亮、console、
  detail 同步），右欄 detail 明確分區「**Live**（即時串流）｜**History**（transcript）｜
  **Mailbox**」，並標示哪段是歷史、哪段是即時。
- **Board 卡片加料**：assignee 用 per-agent 色點、顯示相對時間（updated 3m ago）、可點開
  task detail（spec / result / verify_note / 關聯訊息）。欄可橫向捲動以容納多 task。
- **RWD**：≥3 欄（桌機）→ 2 欄（roster 抽屜化）→ 單欄（tab 切換）。至少加斷點不破版。

### 4.3 主題三：互動與安全防呆（H2/H12）

- **危險操作一律確認且分級**：`halt all`、`reset`、`freeze` 用**自訂 confirm 對話框**（複用
  overlay 元件，取代原生 `window.confirm`），文案講清後果；`halt all` 必須二次確認（破壞半徑＝
  全團）。把 halt/reset 從 header 主區挪到「⋯ space 選單」降低誤觸。
- **審批/問題支援鍵盤**：A=allow、D=deny、Enter=submit；roster 上下鍵切換聚焦成員。
- **per-gate 錯誤回饋**：`command_error`（RP-2）對應到該審批卡上「送出失敗，重試」，而非全域紅字。

### 4.4 主題四：視覺系統與可讀性（H4/H10/H11）

- **建立 type scale**：最小體型 ≥ 12px（0.75rem）；用 2–3 級層次取代現在 0.62/0.65/0.68/0.7 的
  零碎尺寸。維持 dark theme 與既有狀態色，但**收斂成 design token**（顏色/間距/圓角/字級各一張表），
  讓元件一致。
- **降噪**：roster 卡只在「非預設」時才顯 badge（`active` 不顯、只顯 `frozen`）；freeze/suspend
  控制改 **hover 顯示或 ⋯ 選單**，平時乾淨。
- **導入精簡 icon set**（狀態/動作）：freeze=❄、suspend=⏸、resume=▶、halt=■、approval=🛡、
  executing=▶、thinking=…、waiting=⏳。icon＋文字並存（無障礙），但讓掃視更快、更省空間。
- **phase pill 系統化**（接 RP-3）：每個 phase 一個固定 icon＋色＋（執行中）計時，`waiting-approval`
  最醒目（已用紫色加粗，建議再加常駐計時「⏳2:41」凸顯卡多久）。

### 4.5 主題五：無障礙與韌性（H5/H8/H14）

- **不只用顏色**：所有狀態都有文字/icon 雙編碼；board dot 加形狀或標籤；確保 dim 文字對 bg
  ≥ WCAG AA（4.5:1）。
- **loading / skeleton / empty / error 四態齊全**：REST 載入時 skeleton；WS 斷線時頂部明確橫幅
  （非只 console 角落小字）＋自動重連倒數。
- **focus-visible 樣式 + ARIA roles**（list/listitem/dialog/status）讓鍵盤與螢幕報讀可用。

---

## 5. 建議分階段（finish before expand；先觀測、再美化）

| 階段 | 內容 | 主題 | 價值 |
| --- | --- | --- | --- |
| **UX-1 態勢感知** | Attention Bar + 審批 tray 選項 + per-phase 計時 | 4.1 | 直接回答「有什麼需要我」，承接 RP-2/RP-3 |
| └ UX-1a ✅ | **已落地**：`AttentionBar.vue`（聚合 waiting-approval/input + error/paused、可點聚焦、live elapsed）+ roster pill 顯示 phase 計時（後端 `roster.phaseSince` → `MemberInfo.phaseSince`） | — | 已實作、測試＋smoke 驗證 |
| └ UX-1b ✅ | **已落地**：審批改可選**非阻斷 tray**（`ApprovalTray.vue` 浮動側欄列出整個佇列）＋ modal↔tray 偏好（header toggle，localStorage 持久化，預設 modal）；抽出共用 `GateCard.vue`（modal 與 tray 共用一份 allow/deny/問答邏輯，不重複） | — | 已實作、build 驗證 |
| **UX-2 版面重整** ✅ | **已落地**：中欄 Board/Timeline/Console **分頁化**；`Timeline.vue` 全 space 訊息活動流（收 RP-1 §3.6）；board 卡片加 assignee 配色/相對時間/可點展開 spec·result·verify；detail 改標 history·transcript / mailbox + 相對時間 | 4.2 | 監看效率與可觀測性 |
| **UX-3 安全與互動** ✅ | **已落地**：`ConfirmDialog.vue` 取代原生 confirm（Enter/Esc）；reset + **halt all 皆需確認**；審批 modal 鍵盤 A=allow/D=deny；失敗的 gate 回應帶 reqId 並 re-hydrate（不再無聲卡住） | 4.3 | 防呆、效率 |
| **UX-4 設計系統** ✅ | **已落地（核心子集）**：type-scale token（`--fs-xs/sm/md/lg`，可讀性 floor ~11.5px，掃掉散落的 ~10px）；roster 降噪（隱藏 active badge、控制 hover 顯示）；WS 斷線全域 banner；RWD 斷點（窄螢幕收合）；全域 `:focus-visible` + 關鍵容器 ARIA role。**未做（後續）：完整 icon set、Figma 高保真、深度 a11y（roster li 鍵盤化）** | 4.4/4.5 | 一致性、可讀性、無障礙 |

> 順序刻意把**態勢感知（UX-1）擺第一** —— 它與剛落地的 RP-2/RP-3 直接相乘，投入小、感受大。
> 設計系統（UX-4）擺後面：先把「看得懂、不出錯」做對，再系統化美化。

---

## 6. Scope / Out-of-scope / Acceptance

**In（本 RP 涵蓋的方向）**：上述 5 主題的**設計方向與優先序**；wireframe 草圖層級（非高保真）。

**Out（暫不在此 RP）**：
- 完整 design system 的 Figma 高保真稿（待方向拍板後另開）。
- 換框架 / UI library（維持 vue + 手寫 CSS 的輕量路線；只導入 token，不導入重型 UI kit）。
- i18n（中英切換）—— 之後可做，本 RP 只在 icon/文案層預留。
- 認證/登入安全強化 —— 目前刻意以 `root` 固定 token 方便測試（見 `service.DefaultToken`，
  已標 TODO），屬資安議題、不在 UI/UX RP；成熟後另案處理。

**Acceptance（方向被採納後，逐階段驗收）**：
1. 打開任一 space，3 秒內能回答「**有什麼需要我**」（Attention Bar 命中）。
2. 多個成員同時 `waiting-approval` 時，操作者能**邊監看邊逐一審批**，無互蓋、無被鎖死。
3. `halt all` / `reset` 等破壞性操作**皆需明確確認**，且不在主區易誤觸位置。
4. 主要文字 ≥ 12px、狀態非純色編碼、WS 斷線有明確全域提示。
5. 視覺 token 化後，元件間字級/色/間距一致（抽查無零碎硬寫值）。

---

## 7. 一頁總結

- **重定義**：chat app → **swarm operations console**（監看＋介入）。
- **最痛點**：沒有「需要我關注」的聚合層（H1）＋ halt 零確認的安全不對稱（H2）。
- **最划算**：Attention Bar + 審批 tray + per-phase 計時（UX-1）—— 與剛落地的 RP-2/RP-3 相乘。
- **補完觀測**：Team Activity Timeline 順手收掉 RP-1 §3.6 與 flat-comms 的「全員可觀測」。
- **底子保留**：穩定 per-agent 配色、dark theme、扁平化 demux 是優點，改版圍繞它**系統化**，不推倒。
- **紀律**：先「看得懂、不出錯」（UX-1~3），再「系統化美化」（UX-4）；維持輕量手寫 CSS，只導入 token。
