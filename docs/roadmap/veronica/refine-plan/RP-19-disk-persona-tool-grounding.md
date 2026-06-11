# RP-19 — Disk persona 的工具系統接地（mechanics 注入 + deferred 公告 + tool_search 自動掛）

> 狀態：**✅ 已完成（2026-06-11，feature/RP-19-disk-persona-tool-grounding）** ｜ 階段：**第五波（swarm 即框架的成熟度）** ｜ 日期：2026-06-11
> 落地註記：Part B-2 的 `tool_search` 自動掛**沒有**做在 `registerDef`（swarm-policy seam），而是做在
> `mainProfileFromDiskAgent`（`internal/agent/profiles.go`）——所有 disk main persona（swarm 成員與
> `/profile` persona）共用的 seam，swarm 成員經 `agent.New` 流經此處，非 swarm persona 一併受益；
> registry 裡的 def 維持作者原樣（profile 層效果，不改 persona 目錄）。curated 表 + 逐工具 gate 落在
> `internal/agent/sysprompt/disk_tools_guide.go`；link test（AST 解析 `pkg/tools/name.go`）保證新增
> builtin 工具必補準則。已知鄰接缺口（本 RP 不處理）：disk **subagent** 路徑（`spawn.go
> profileFromDiskAgent`）沒有 compose、也沒有 tool_search 自動掛——deferr.yml 對 subagent 仍是死資料。
> 觸發：Sunday swarm 重整（2026-06-11：friday/trader 決策執行分離 + 全員 prompt 重寫）。重寫過程發現：**8 個 agent 的 system_prompt.md 每一份都得手寫一節「工具櫃」**——教 tool_search 協議、平行呼叫、repl vs calc、todo_write 協議——這些是 harness 事實，不是 persona 個性，卻全落在 operator 肩上。
> 關聯：[RP-10A](RP-10A-subtickets.md)（`registerDef` 強制設定的先例：`ensureTool(SKILL)`）、[RP-5](RP-5-member-prompt-env.md)（提示詞前綴位元穩定，本文所有注入都必須遵守）
> 請求者：Sunday（swarm 的*使用者*）。**無 Sunday-specific code**——是 disk persona prompt 組裝的通用缺口。

---

## 1. Problem（observed，含 file:line 證據）

`ComposeDiskMainPrompt`（`internal/agent/sysprompt/fragments.go:323`）的註解明言：

> *"Disk personas DELIBERATELY skip the ref-ported sections … The persona supplies its own conduct rules in body."*

這個理由把兩件事混在一起了：

- **persona / conduct**（這個 agent 是誰、怎麼說話、何時警示）——確實是 operator 的事；
- **harness mechanics**（tool_search 怎麼用、deferred tool 是什麼、平行 tool call、todo_write 的狀態協議、repl 是無狀態子行程）——這是 **evva 的事實**，不隨 persona 變，operator 不寫 agent 就不會。

被跳過的不只 conduct 區塊，還包括 `mainToolsGuideSection`（`main_agent.go:120`）。三個具體後果：

1. **工具教學整段缺席**：swarm 成員拿到工具 schema 但沒有使用準則——Sunday 第一輪 swarm 的「agent 不會靈活用工具」直接源於此。
2. **deferred tools 隱形**：`mainProfileFromDiskAgent` 有組 `ctx.DeferredTools`（`profiles.go:313-314`），但 `ComposeDiskMainPrompt` 從不渲染 `mainDeferredToolsSection`（`main_agent.go:82`）——`deferr.yml` 裡的工具**可被 tool_search 載入，但模型永遠不知道名字**。
3. **`tool_search` 不自動掛**：`registerDef`（`internal/swarm/space.go:188`）已有 `ensureTool(SKILL)` 先例，卻沒有對 deferred 非空的成員 ensure `TOOL_SEARCH`——active.yml 漏列它，deferr.yml 整份變死資料。

Sunday 的 workaround（每份 prompt 手寫工具櫃 + 點名深櫃工具 + active.yml 手放 tool_search）證明這套內容**可以從成員的工具清單機械式生成**——那它就該由框架生成。

## 2. Proposal（Part A 生成、Part B 兩個機械修補）

**Part B（先做，小時級）**：

1. `ComposeDiskMainPrompt` 納入現成的 `mainDeferredToolsSection(ctx.DeferredTools)`（函式已存在，只是沒被呼叫）。
2. `registerDef`：`if len(def.DeferredTools) > 0 { def.ActiveTools = ensureTool(def.ActiveTools, tools.TOOL_SEARCH) }`——比照 RP-10-1 的 SKILL 強制。

**Part A（設計 + 實作，天級）**：新增 sysprompt fragment `diskToolsGuideSection(active, deferred []tools.ToolName)`，由 `ComposeDiskMainPrompt` 在 persona body **之前**渲染（harness 事實先於人設，與 main agent 的 section ordering 哲學一致）：

- **只教成員真的有的工具**：從 active/deferred 清單查一張 curated 的「工具 → 一句使用準則」表（read/write/edit/bash/grep/glob/tree/web_*/http_request/calc/repl/json_query/todo_write/excel/skill…）；沒有的工具一字不提（避免幻覺呼叫）。
- 固定收錄三條通用 mechanics：平行 tool call、deferred + tool_search 協議（僅當 deferred 非空）、todo_write 狀態協議（僅當有 todo_write）。
- **位元穩定**（RP-5 不變量）：內容只依賴工具清單，無日期、無計數器；同一份 tools.yml 永遠生成同一段文字。

## 3. Why evva（not Sunday）

工具使用準則描述的是 evva 的 harness 行為。讓每個 swarm operator 手抄一遍，等於要求他們讀 `main_agent.go` 自行還原——Sunday 這次做了，下一個 swarm 不會做。修在 `ComposeDiskMainPrompt` 一個 seam，所有 disk persona（swarm 與否）一起受益。

## 4. Acceptance

- 有 `deferr.yml` 的成員：prompt 含 `<available-deferred-tools>` 區塊列出名字；active tools 自動含 `tool_search`（active.yml 不必手列）。
- 成員 prompt 含 mechanics 區塊，且**只**提及其 active/deferred 清單內的工具；無 todo_write 的成員看不到 todo 協議。
- 同一 tools.yml 重複組裝 → 區塊逐位元相同（prompt-cache 前綴穩定）。
- persona body 的內容與順序不受影響（人設仍領頭其後的 swarm 協議注入不變）。
- Sunday 回歸：刪掉 agents/*/system_prompt.md 手寫的 tool_search／deferred 點名段後，行為不退化。

## 5. Notes

- curated 表放 sysprompt 包內、以 `tools.ToolName` 為 key——新增 builtin 工具時 link test 應提醒補一句準則（比照 `toolnames.go` 的 rename 防護哲學）。
- 不要把 main agent 的完整 `mainToolsGuideSection` 原樣搬來——它含 plan-mode/worktree/subagent 等 swarm 成員多半沒有的工具；逐工具 gate 是本 RP 的核心。
- conduct 類區塊（doing-tasks / actions / tone）**維持不注入**——那確實是 persona 的事，本 RP 不翻案。
