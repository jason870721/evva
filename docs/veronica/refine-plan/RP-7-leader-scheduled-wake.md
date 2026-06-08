# RP-7 — Leader 主導的排程喚醒（crontab）：工具化、可見、可控

> 狀態：**草案 / Draft（待 Johnny 拍板）** ｜ 日期：2026-06-06 ｜ 階段：**Phase 1**
> 觸發：希望 leader 能把某成員設成「定時主動執行任務」的模式，且喚醒提示詞可帶當下時間
> 與自訂內容；leader 也要能在成員清單看到各自的 crontab（防上下文壓縮後遺忘）。
> 上層設計：[`../veronica-design-v1.md`](../veronica-design-v1.md)（§5.5 三個喚醒源）
> 關聯：[RP-5](RP-5-member-prompt-env.md)（靜態提示詞不放時間，與本文喚醒時注入時間互補）

---

## 1. TL;DR

排程喚醒的**機械骨架其實已經存在**：`Schedule`（cron / interval 皆可，自帶 5 欄位 cron
解析）＋ supervisor 的 `timerTick`/`fireDue` 已能把「到期」變成一次喚醒。但它今天是個
**半成品**：

1. **只能在 `profile.yml` 宣告**，`evva-swarm.yml` 看不到、改不了。
2. **喚醒提示詞是寫死的**通用句（`scheduledDutyPrompt`）——**沒有時間、沒有自訂內容**。
3. **Leader 完全無法操控**——沒有對應工具，leader 不能把某成員設成定時模式。
4. **`list_members` 看不到 crontab**——leader 一旦被壓縮，就忘了誰被排了班。
5. **執行中觸發會「排隊」而非「跳過」**——buffered poke 會在當前 run 結束後補跑。

> **方向**：把這套排程**升級成 leader 的一級能力**——
> ① 新增 leader 預設工具 `schedule_set` / `schedule_clear`；
> ② 喚醒時注入 `<system-reminder>currenttime: YYYY-MM-DD HH:MM:SS, #{prompt}</system-reminder>`
>    （時間 = 觸發當下，`#{prompt}` = leader/User 指定的自訂提示）；
> ③ `evva-swarm.yml` 可宣告每個成員的 `schedule: {cron, prompt}`；
> ④ `list_members` **常駐顯示**每個成員的 crontab（防遺忘）；
> ⑤ **執行中觸發 → 跳過本輪**（不排隊）；
> ⑥ **一個成員至多一個 crontab**；**leader 不能撤銷/變更自己的**（只有 User 能，見
>    [RP-8](RP-8-web-agent-schedule-mgmt.md)）。

---

## 2. 現況盤點（file:line 證據）

| # | 事實 | 位置 | 對需求的意義 |
| --- | --- | --- | --- |
| S1 | `Schedule`（cron 或 every）＋自帶 5 欄位 cron 解析、`Next()` | `agentdef/schedule.go:19`（解析 `:98`、`Next` `:71`） | ✅ 可直接複用，免拉 cron 相依 |
| S2 | schedule **只從 `profile.yml` 載入** | `agentdef/loader.go:62`（`profileYml.Schedule`） | ❌ `evva-swarm.yml` 沒有此欄 |
| S3 | `evva-swarm.yml` schema **無 schedule** | `agentdef/manifest.go:34`（`manifestYml`）、`Member`（`:23`） | 需擴充 |
| S4 | 每空間的 live schedules map | `space.go:58` `sp.schedules`；`scheduleFor`（`:224`）；建構時填入（`:214`） | runtime 來源在此 |
| S5 | supervisor 把 schedule **複製進 `memberRun`，只在 loop 啟動時 seed 一次** | `scheduler.go:30-39`（`memberRun.schedule/nextDue`）、`startMemberLoop`（`:50-57`） | ❌ runtime 改 schedule 不會傳進跑著的 loop |
| S6 | 喚醒提示詞**寫死、無時間、無自訂** | `scheduler.go:214` `scheduledDutyPrompt`；timer 分支（`:94-101`） | 需改成 system-reminder 格式 |
| S7 | `fireDue` 用 buffered `poke` → 執行中會**排隊補跑** | `scheduler.go:281`（`fireDue`）、`poke`（`:219`，buffered(1)） | ❌ 需求是「跳過本輪」 |
| S8 | `list_members` 不含 schedule | `tools/messaging.go:81`（`newListMembers`）、`MemberView`（`roster.go:76`） | ❌ leader 看不到 crontab |
| S9 | leader 預設工具集無排程工具 | `tools/set.go:69`（`toolNamesForRole`）、`factories`（`:79`） | 需新增兩個工具 |
| S10 | swarm 協作工具走 permission 自動放行 | `tools/set.go:39-46`（`ReadOnlyOrSelfTools`） | 新工具沿用此自動放行 |
| S11 | runtime 持久化只寫 membership | `resume.go:32` `persistRuntime` | leader 設的 cron 需一併持久化才能撐過重啟 |

**結論**：cron 解析與 tick 引擎是現成的；本 RP 主要是**把宣告面向上接到 manifest、把控制面
開給 leader 工具、把可見面接進 list_members、把喚醒提示改成帶時間的 system-reminder、並修兩個
語意（跳過 vs 排隊、執行中保護）**。

---

## 3. 設計方向

### 3.1 Schedule 模型升級：帶 `prompt`

`Schedule` 目前只有 `Cron`/`Every`。新增 `Prompt string`（喚醒時要注入的自訂提示，即
`#{prompt}`）。喚醒提示**不放進系統提示詞**（呼應 [RP-5](RP-5-member-prompt-env.md)），只在
喚醒當下組成 run prompt。

```go
type Schedule struct {
    Cron   string        // 5 欄位 cron（leader 工具的主要形式）
    Every  time.Duration // 既有 interval 形式，yaml 仍可用
    Prompt string        // 自訂喚醒提示；空 → fallback 到標準站崗句
}
```

### 3.2 喚醒提示詞格式（帶時間 + 自訂）

`scheduler.go` 的 timer 分支（`:94`）改為：**喚醒當下**用觸發時間 `now` 組裝

```
<system-reminder>currenttime: 2026-06-06 14:30:00, #{prompt}</system-reminder>
```

- `currenttime` = `fireDue` 觸發時的時間，格式 `YYYY-MM-DD HH:MM:SS`。
- `#{prompt}` = 該成員 schedule 的 `Prompt`；若為空，fallback 到現有
  `scheduledDutyPrompt` 的站崗語意（保留「沒事就回報並 stand down，別硬找事做」）。
- 這是**唯一**會把時間放進對話的地方，且只在 run prompt（不污染系統提示詞快取）。

### 3.3 Leader 工具：`schedule_set` / `schedule_clear`

新增兩個工具，**預設加入 leader 工具集**（`set.go:69` `toolNamesForRole` 的 leader 分支），
factory 註冊進 `factories`（`:79`），並列入 permission 自動放行（`:39`，與其他協作工具同列
——這是團隊協調，不是檔案/shell 副作用）。

```
schedule_set   { member, cron, prompt }   # 設定/取代某成員的排程喚醒
schedule_clear { member }                 # 取消某成員的排程喚醒
```

**守則（exec 內檢查）**：

- `member` 必須是現役成員（複用 `rosterHas`，`set.go:138`）；否則回可糾正錯誤。
- **leader 不能對自己下 `schedule_set`/`schedule_clear`** —— 回明確錯誤：「leader 的排程只有
  操作者（User）能透過 Web 調整」。這實作使用者要求的「leader 不能撤銷自己的 crontab」。
- `cron` 用 `agentdef.parseCron` 驗證（壞 cron 當場回錯，不等到第一次 tick）。
- **一個成員至多一個 crontab**：`schedule_set` 直接**取代**舊的（map 以 name 為鍵，天然單一）。
- 描述（desc）依專案慣例，語氣對齊現有 `task_*` 工具（精簡、講清楚會發生什麼）。

### 3.4 Runtime 套用：把改動推進跑著的 loop（修 S5）

目前 `memberRun.schedule` 只在 `startMemberLoop` seed 一次，runtime 改 `sp.schedules` 不會生效。
新增 supervisor 方法（leader 工具與 [RP-8](RP-8-web-agent-schedule-mgmt.md) 的 Web 都呼叫它）：

```go
func (s *Supervisor) SetSchedule(name string, sch agentdef.Schedule) error  // 更新 sp.schedules + 重算 memberRun.schedule/nextDue（s.mu 下）
func (s *Supervisor) ClearSchedule(name string) error                        // 刪 sp.schedules + 清 memberRun.schedule
```

- 在 `s.mu` 下同時更新 `sp.schedules`（持久來源）與 `members[name].schedule/nextDue`（跑著的
  loop 讀的），確保即時生效。
- 連同 `persistRuntime`（`resume.go:32`）擴充：runtime.json 一併寫入 live schedules，讓
  leader 設的 cron **撐過 service 重啟**（重啟 reconcile 時重建）。

### 3.5 可見性：`list_members` 常駐顯示 crontab（修 S8）

- `rosterEntry` / `MemberView`（`roster.go:61`/`:76`）新增 `Cron string` + `SchedulePrompt string`
  （或整個 `Schedule`）。Roster 在建構/`SetSchedule`/`ClearSchedule` 時同步。
- `newListMembers`（`messaging.go:96` 那行 `Fprintf`）追加 schedule 欄，例如：

  ```
  - qa [worker] active/ready — QA 工程師  ⏰ cron "*/30 * * * *": "巡檢測試套件，有紅燈就回報 lead"
  ```

- **WHY（使用者明說的理由）**：leader 的上下文可能被壓縮，壓縮後它會忘記自己幫誰排了班；
  把 crontab **放進每次 `list_members` 的常駐輸出**，等於把這份狀態釘在「隨時可重新取得」的
  地方，而不是只存在 leader 的易失記憶裡。

### 3.6 執行中觸發 → 跳過本輪（修 S7）

`fireDue`（`scheduler.go:281`）poke 之前**先看該成員 run 狀態**：若**非 idle**（busy/suspended），
**本輪直接跳過**（不 poke、不排隊），`nextDue` 照常往前推 → 下一個到期點再正常觸發。

- 已有先例：`rescanUnread`（`scheduler.go:251`）就是用 `mv.Run != RunIdle` 來判斷。本修改與它
  同一哲學。
- 語意：排程是「定時巡檢」，不是「一定要補跑的工作佇列」；忙就略過、下次再來，避免在長任務
  尾巴堆一串遲到的站崗喚醒。

### 3.7 `evva-swarm.yml` 宣告排程（修 S2/S3）

`manifestYml`（`manifest.go:34`）的 `workers[]`（與 `leader`）每項加：

```yaml
leader:
  agent: lead          # leader 也能被 User 在 yml 預先排班（leader 工具不能改自己的）
workers:
  - agent: qa
    schedule:
      cron: "*/30 * * * *"
      prompt: "巡檢測試套件，紅燈就 send_message 給 lead"
```

- `LoadManifest` 解析後，manifest 的 schedule **覆蓋/補上** `profile.yml` 的宣告（建議：manifest
  為權威，profile.yml 為 fallback；在 `BuildAll`/`constructMember` 合併）。
- 這讓「整團的定時行為」能寫在單一檔案版本控管，而非散落各 `profile.yml`。

---

## 4. 流程小圖

```
                       ┌─ evva-swarm.yml (schedule)
宣告來源 ──────────────┤
                       └─ profile.yml (fallback)
                                  │  載入
                                  ▼
控制面 ── leader: schedule_set/clear ──► Supervisor.SetSchedule/ClearSchedule
         (不能改自己) ─ User(Web, RP-8) ─┘            │ 更新 sp.schedules + memberRun
                                                      ▼
喚醒 ── timerTick ─► fireDue ── idle? ──否──► 跳過本輪（nextDue 照推）
                                  │是
                                  ▼ poke(wakeTimer)
                     serve: run "<system-reminder>currenttime: …, #{prompt}</system-reminder>"
可見面 ── list_members 常駐顯示每位成員 cron + prompt（防 leader 壓縮後遺忘）
```

---

## 5. Scope / Acceptance

**In**：`Schedule.Prompt`；system-reminder 喚醒格式（含 currenttime）；leader `schedule_set`/
`schedule_clear`（含 self 保護、單一 crontab、cron 驗證、permission 自動放行）；
supervisor `SetSchedule`/`ClearSchedule` + 即時套用；`list_members` 常駐顯示 crontab；
執行中跳過本輪；`evva-swarm.yml` schedule 欄；runtime 持久化 schedule；測試。

**Out**：Web 端管理 crontab（屬 [RP-8](RP-8-web-agent-schedule-mgmt.md) Phase 2）；秒級 cron（維持
分級 minute 解析度，`schedule.go` 不動）；跨機觸發（design 的 process-model A 不在此處）。

**Acceptance**：
1. Leader 用 `schedule_set { member:"qa", cron:"*/30 * * * *", prompt:"…" }` 後，qa 依排程被喚醒，
   喚醒輸入為 `<system-reminder>currenttime: <實際時間>, …</system-reminder>`。
2. `list_members` 的 qa 列顯示其 cron 與 prompt；**leader 上下文壓縮後再查仍看得到**。
3. cron 觸發時 qa 正在 run → **本輪跳過**（不在 run 結束後補跑）。
4. Leader 對**自己**呼叫 `schedule_set`/`schedule_clear` → 被拒（明確訊息指向 User/Web）。
5. `evva-swarm.yml` 宣告的 schedule 開機即生效；`schedule_clear` 後不再喚醒。
6. service 重啟後，leader 先前設定的 cron 仍在（runtime 持久化）。
7. 壞 cron 在 `schedule_set` 當下即回錯，不會延到 tick 才爆。

---

## 6. 落地任務（建議顆粒）

| # | 任務 | 落點 |
| --- | --- | --- |
| RP-7-1 | `Schedule` 加 `Prompt`；解析/驗證沿用 | `agentdef/schedule.go` |
| RP-7-2 | timer 喚醒改 system-reminder 格式（currenttime + #{prompt}，空則 fallback） | `swarm/scheduler.go`（`:94`、`:214`） |
| RP-7-3 | `fireDue` 加「非 idle 跳過本輪」 | `swarm/scheduler.go:281` |
| RP-7-4 | supervisor `SetSchedule`/`ClearSchedule`（即時套用 memberRun + sp.schedules） | `swarm/supervisor.go`、`scheduler.go` |
| RP-7-5 | leader 工具 `schedule_set`/`schedule_clear`（self 保護、單一、cron 驗證） | `swarm/tools/schedule.go`（新）、`set.go`（`toolNamesForRole`/`factories`/`init` 放行） |
| RP-7-6 | `MemberView`/`rosterEntry` 加 cron+prompt；`list_members` 常駐顯示 | `swarm/roster.go`、`tools/messaging.go` |
| RP-7-7 | `evva-swarm.yml` schedule 欄；manifest↔profile 合併 | `agentdef/manifest.go`、`loader.go` |
| RP-7-8 | runtime.json 持久化 live schedules + 重啟 reconcile | `swarm/resume.go` |
| RP-7-9 | 測試：喚醒格式、跳過本輪、self 保護、可見性、持久化、manifest schedule | 各 `*_test.go` |

> 北極星：讓 leader 像 docker/cron 一樣，把某個成員**宣告為定時主動執行的 worker**；而 leader
> 對自己的排班無權，那是 User 的方向盤（[RP-8](RP-8-web-agent-schedule-mgmt.md)）。
