# RP-10 Part A — Sub-tickets（可開工）

> Parent: [`RP-10-agent-skills-injection-and-web-mgmt.md`](RP-10-agent-skills-injection-and-web-mgmt.md) §4（Part A）
> 階段：**Phase 1（地基）** ｜ 狀態：**Ready to build（待認領）** ｜ 日期：2026-06-06
> 範圍：只含 Part A（啟動注入）。Part B（Web 增刪 + reload）另開。

Part A 的目標：**讓每個 swarm 成員啟動時，系統提示詞列出「自己」的 skill（name+desc），並預設具備
`skill` 工具去載入它們**——而且不鼓勵 agent 自建 skill。注入機制本就存在（`skillsSection` →
`ComposeDiskMainPrompt`），三張票分別「打開它 / 接對來源 / 收掉自建引導」。

## 共用背景（三票都要懂的兩個事實）

1. **唯一 chokepoint**：每個成員（leader + workers）組裝時都會經過 `internal/swarm/space.go`
   `registerDef`（`:142-152`）——它已經 `def.As = ensureMain(...)`、`def.SystemPrompt =
   injectTeamProtocol(...)`、然後 `sp.reg.Register(def)`。**A1/A3 對 def 的強制設定都加在這裡**
   （注入團隊協定的同一個地方）。
2. **建構路徑**：成員是 main-tier persona → `agent.New` → `mainProfileFromDiskAgent`
   （`internal/agent/profiles.go:278`）→ `ComposeDiskMainPrompt`（`fragments.go:261`）。其中
   `if def.AdvertiseSkills { ctx.Skills = skills }`（`profiles.go:280-281`）、`skillsList =
   skillsSection(ctx.Skills)`（`fragments.go:269`）。

**建議順序 / PR 切法**：A1 + A2 一起（合起來才看得到「成員列出自己的 skill」），A3 接著（措辭紀律）。
三張都小，可單一 PR 收完 Part A。

---

## RP-10-1 — 每個成員強制 advertise skills ＋ 預設掛 `skill` 工具

> Status: Ready ｜ Depends on: — ｜ Unblocks: RP-10-2 的可觀測性、整個 Part B

### Goal
swarm 的**每一個**成員（leader + workers）都 ① `AdvertiseSkills=true`（提示詞會列 skill 段）、
② 工具集含內建 `skill` 工具（wire name `skill`，`pkg/tools/name.go:36` `tools.SKILL`）。

### Files & change
- `internal/swarm/space.go` `registerDef`（`:142-152`）：在 `sp.reg.Register(def)` **之前**，對本地
  `def` 加兩行強制設定：

  ```go
  def.AdvertiseSkills = true                 // 提示詞列出 skill name+desc（RP-10-1）
  def.ActiveTools = ensureTool(def.ActiveTools, tools.SKILL) // 每個成員都能 load skill
  ```

  - 新增小 helper `ensureTool(list, name)`（append-if-absent，去重）——或直接 inline 去重。
  - import `github.com/johnny1110/evva/pkg/tools`。
- 不動 `profile.yml` / loader：強制在 swarm 層，不是 per-agent 可關的選項（呼應「每一個 agent 都要
  預設具備」）。`advertise_skills:` 在 `profile.yml` 寫什麼都被覆蓋為 true。

### Why this works
`mainProfileFromDiskAgent` 用 registry 裡那份 `def` 的 `AdvertiseSkills`（`profiles.go:280`）與
`ActiveTools`（`profiles.go:300`）；`skill` 工具 factory 已註冊於 `internal/toolset/builtins.go:100`
（讀 `ts.SkillRegistry`，而成員的 registry 由 `space.go:178` `WithSkillRegistry(ld.Skills)` 設好）。

### Acceptance
1. 任一成員（含 leader）建構後，其 active 工具集**包含 `skill`**。
2. 該成員 resolved profile 的 `AdvertiseSkills == true`。
3. `profile.yml` 即使寫 `advertise_skills: false`，成員仍 advertise（被 swarm 強制覆蓋）。

### Tests
- `internal/swarm/space_test.go`：建一個成員，斷言 active 工具含 `skill`、且 def.AdvertiseSkills 為真。
- 邊界：成員 active.yml 已含 `skill` → 不重複（去重）。

### Risk / rollback
極低：純粹多開一個旗標 + 多掛一個 read-mostly 的工具。回滾 = 移除那兩行。

---

## RP-10-2 — Prompt 的 skill 來源接成員自己的 registry（與工具同源）

> Status: Ready ｜ Depends on: RP-10-1（要 advertise 才看得到效果）｜ 類型：internal/agent 修正（**惠及所有 host**）

### Goal
成員提示詞列出的 skill == 該成員 `agents/sub/<name>/skills/` 的 skill（== `skill` 工具能載入的那組）。
**根除來源錯位**：目前若打開 advertise，prompt 端會 fallback 去載 **cfg 全域 skills**，而非成員自己的。

### Root cause（file:line）
- `WithSkillRegistry`（`internal/agent/options.go:69-71`）只 `SetSkillRegistry`（工具端），**不設**
  `a.skillRefs`（prompt 端）。
- `agent.New` 的自動載入塊只在「registry 為 nil」時派生 skillRefs（`agent.go:321-329`）；成員已被
  `WithSkillRegistry` 設過 registry → 此塊跳過 → `a.skillRefs` 仍 nil。
- `resolveMainProfileWithExtra` 對 nil skills 會 fallback：`if skills == nil { skills =
  refsFromRegistry(loadDiskSkillRegistry(cfg)) }`（`profiles.go:226-227`）→ 載入 **cfg 全域 skills**。
- 而 `refsFromRegistry` 對空 registry 回 **nil**（`internal/agent/skills.go:48-49`）→ 一個**沒有 skills
  目錄**的成員也會 nil → 同樣誤 fallback 到全域。

### Change
- `internal/agent/agent.go`：在自動載入塊（`:321-329`）**之後**補一段——當 skillRefs 仍為 nil 但
  ToolState 已有 registry（即被 `WithSkillRegistry` 注入），就從該 registry 派生 prompt refs，並**把
  空 registry 強制成 non-nil 空 slice**，杜絕 fallback 到全域：

  ```go
  // 顯式注入的 registry 必須同時驅動「prompt 的 # Skills 段」，而不只是 SKILL 工具——
  // 否則 prompt 會 fallback 去載 cfg 全域 skills（不同的 catalog）。空 registry 也要 advertise
  // 「無」，而不是繼承全域。
  if a.skillRefs == nil {
      if reg := a.toolState.SkillRegistry(); reg != nil {
          refs := refsFromRegistry(reg)
          if refs == nil {
              refs = []sysprompt.SkillRef{} // 顯式空：列零個，且抑制 cfg 全域 fallback
          }
          a.skillRefs = refs
      }
  }
  ```

- **不需要動 swarm 程式**：swarm 早已傳 `WithSkillRegistry(ld.Skills)`（`space.go:178`），此修正生效後
  成員 prompt 自動列出自己的 skills。

### Scope note（對其他 host 的影響）
此修正讓「**顯式注入 registry**」同時餵 prompt 與工具——這是直覺正確的對齊，且仍受 `AdvertiseSkills`
把關（沒開 advertise 的 persona 完全不受影響）。內建 evva / disk persona 走的是「自動載入」分支
（registry 原為 nil），其 skillRefs 多半已由原塊設好，本段為 no-op。需以測試鎖住 evva 行為不變。

### Acceptance
1. 一個 `agents/sub/x/skills/` 有 `foo`、`bar` 的成員，系統提示詞 `# Skills` **只列 foo、bar**
   （不混入 cfg 全域 skills），且 == `skill` 工具能載入的集合。
2. 一個**沒有** skills 目錄的成員，提示詞**無 `# Skills` 段**（不繼承全域 skills）。
3. 內建 evva（非 swarm）的 skills 行為**不變**（回歸測試）。

### Tests
- `internal/agent/*_test.go`：`agent.New(... WithSkillRegistry(custom) ...)` + AdvertiseSkills →
  prompt 含 custom 的 skill、不含 disk 全域；注入**空** registry → 無 # Skills 段、且不 fallback。
- `internal/swarm/space_test.go`：成員 prompt 列出其 per-agent skills。
- 回歸：內建 evva prompt 的 skills 段不變。

### Risk / rollback
低—中：碰到共用建構路徑。風險集中在「是否誤改 evva/disk 行為」，用回歸測試框住。回滾 = 移除新增段。

---

## RP-10-3 — Swarm 版 skills 段：移除「如何建立 skill」引導

> Status: Ready ｜ Depends on: RP-10-1（要有 skills 段才需要收措辭）｜ 紀律：agent 只載入、不著作

### Goal
swarm 成員的 `# Skills` 段**只列 name+desc ＋「用 `skill` 載入」一句**，**不含**「How to create a
skill…」教學（`fragments.go:229`）。Worker 本就有 `write`/`bash`，那行等於鼓勵它自建 SKILL.md，違反
「先不讓 agent 自己建立/修改 skill」的紀律。evva 主 agent 維持原樣（仍含教學）。

### Change（threading 一個旗標）
1. `internal/agent/sysprompt/fragments.go`：`skillsSection(skills []SkillRef, omitAuthoring bool)`——
   `omitAuthoring` 為真時略過 `:229` 那行（保留「列表 + 用 skill 工具載入」）。更新兩個 caller：
   - `ComposeDiskMainPrompt`（`:269`）：傳 `def.LongRunning`（見下）。
   - `main_agent.go:65`（evva）：傳 `false`。
2. 新增旗標 `LongRunning bool`，沿 def 傳遞（swarm 設、evva 不設）：
   - `pkg/agent/persona.go` `AgentDefinition` 加 `LongRunning bool`（additive，Stable 允許）；`toSpec`
     /`definitionFromSpec`（`:70-96`）帶上。
   - `internal/agent/registry.go` `AgentSpec`（`:15-26`）加欄；`internal/agent/sysprompt/agent_def.go`
     `AgentDefinition`（`:31` 附近）加欄。
   - `internal/swarm/space.go` `registerDef`：`def.LongRunning = true`（與 RP-10-1 同一處）。
3. `mainProfileFromDiskAgent`（`profiles.go:278`）把 `def.LongRunning` 帶進 `ComposeDiskMainPrompt`
   （該函式已收 `def`，直接讀 `def.LongRunning` 傳給 `skillsSection`）。

### 與 RP-5 的協調（✅ 已拍板：共用一個 `LongRunning` 旗標）

> **決策（Johnny 2026-06-06）**：RP-5 與 RP-10-3 **共用同一個 `LongRunning` 旗標**，不各立旗標。

`LongRunning` 是「**長命/swarm persona ⇒ 去掉會漂移或不該有的 prompt 片段**」的 umbrella 旗標：
- **本票（RP-10-3）負責「引入」該旗標**：在 `pkg/agent.AgentDefinition` / `AgentSpec` /
  `sysprompt.AgentDefinition` 加 `LongRunning bool`，於 `registerDef` 設 `def.LongRunning = true`，
  並用它關掉 skill-authoring 引導。
- **[RP-5](RP-5-member-prompt-env.md) 「複用」該旗標**：新增 `PromptContext.OmitDate`，由
  `mainProfileFromDiskAgent` 以 `ctx.OmitDate = def.LongRunning` 驅動，`environmentSection` 據此略過
  `- Today:`。RP-5 **不**再自立旗標，只加「消費端」。

> 落地序：RP-10-3 先落（引入 `LongRunning` 欄 + registerDef 設定）→ RP-5 接上消費端即可。若 RP-5 反而
> 先落，則由 RP-5 引入該欄、RP-10-3 改為純消費。無論誰先，**只有一個 `LongRunning`**。

### Acceptance
1. swarm 成員的 `# Skills` 段**不含**「How to create a skill」文字（仍列 name+desc ＋ 載入提示）。
2. 內建 evva 的 skills 段**仍含**建立教學（不變）。
3. 無任何 agent-facing 的 skill 著作工具（`skill` 工具是 load-only，`pkg/skill` `LoadBody`）。

### Tests
- `sysprompt` 單測：`skillsSection(refs, true)` 無 authoring 行、`(refs, false)` 有。
- `internal/swarm/space_test.go`：成員 prompt 不含 authoring 行。
- 回歸：evva prompt 仍含。

### Risk / rollback
低：措辭 + 一個 additive 旗標。回滾 = 旗標恆 false。

---

## Part A — DoD（驗收整包）

1. 起一個 vero-tech-swarm，任一成員的系統提示詞含 `# Skills`，列出**該成員自己**的 skills，且該清單
   與 `skill` 工具能載入的集合一致（RP-10-1 + RP-10-2）。
2. 每個成員（含 leader）都能呼叫 `skill` 載入上述任一 skill 的 body（RP-10-1）。
3. 沒有 skills 目錄的成員無 `# Skills` 段、不繼承全域 skills（RP-10-2）。
4. 成員 prompt 不含「如何建立 skill」引導；evva 不受影響（RP-10-3）。
5. `go build ./... && go vet ./...`、swarm/agent 套件 `-race` 測試綠燈；內建 evva skills 回歸通過。

## 開工前決策（已拍板）
- ✅ **`LongRunning` 旗標共用**（Johnny 2026-06-06）：單一 `LongRunning` 旗標同時服務 RP-5（去日期）與
  RP-10-3（去 authoring 引導），由 RP-10-3 引入、RP-5 複用消費端。其餘皆為照著 file:line 直改、無歧義。
  **無待決項，可開工。**
