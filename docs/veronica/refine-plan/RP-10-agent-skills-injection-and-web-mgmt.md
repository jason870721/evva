# RP-10 — Agent Skills：啟動注入 system prompt ＋ Web 端動態增刪

> 狀態：**草案 / Draft（待 Johnny 拍板）** ｜ 日期：2026-06-06 ｜ 階段：**Phase 1（注入）＋ Phase 2（Web 管理）**
> 觸發：成員雖能在啟動時載入 skills，但 **skills 的 name+desc 沒被注入系統提示詞**（被一個旗標關掉了），
> 成員不知道自己有哪些 skill 可載；且缺一條「User 在 workflow 中發現某 agent 需要某 skill 就動態補上」的路徑。
> 上層設計：[`../veronica-design-v1.md`](../veronica-design-v1.md) ｜ 對照：evva `skillsSection`（`internal/agent/sysprompt/fragments.go:209`）｜ 關聯：[RP-8](RP-8-web-agent-schedule-mgmt.md)（Web agent CRUD，同屬動態 reconfigure）、[RP-5](RP-5-member-prompt-env.md)（提示詞組裝）

---

## 1. TL;DR

evva 早就有「把 skill 的 name+desc 列進系統提示詞」的機制（`skillsSection`），且 swarm 成員的
提示詞**確實會經過**那段組裝（`ComposeDiskMainPrompt`）。問題出在**它被一個旗標關掉了**：

- 成員 `profile.yml` 預設 `advertise_skills: false`（vero-tech-swarm 各成員亦然）→ `skillsSection`
  **不注入** → 成員系統提示詞裡**看不到自己有哪些 skill**，自然也不知道何時該用 `skill` 工具去載。
- 而且就算把旗標打開，**prompt 端的 skill 清單來源接錯了**：成員的 skill 工具讀的是該成員自己的
  `agents/sub/<name>/skills/`（經 `WithSkillRegistry`），但 prompt 端會 fallback 去載 **cfg 的全域
  skill 目錄**（兩個不同來源）。
- 此外，**成員預設沒有 `skill` 工具**（active.yml 沒列），所以即使提示詞列了 skill，也無從載入。

> **方向**：分兩半。
> **Part A（Phase 1，地基）**：① swarm 為每個成員**強制** `advertise_skills`；② 把 prompt 端的 skill
> 清單**接到成員自己的 registry**（與工具同源）；③ 每個成員**預設掛上 `skill` 工具**；④ swarm 版
> skills 段**移除「教 agent 自己建立 skill」那段引導**（見 §4.4 與下方紀律）。
> **Part B（Phase 2，Web）**：Web 上可**檢視 / 動態新增 / 刪除**每個 agent 的 skills；增刪後**reload
> 該 agent 的 system prompt**（會破 KV cache，但這種動態擴容很值得）。
> **紀律（Johnny 指示）**：**先不讓 agent 自己建立/修改 skill** —— 那會嚴重影響穩定運行中的 swarm 品質。
> skill 的作者權只在 **User（透過 Web）**；agent 只能「**載入**」既有 skill，不能「**著作**」。

---

## 2. 現況盤點（file:line 證據）

| # | 事實 | 位置 | 意義 |
| --- | --- | --- | --- |
| K1 | `skillsSection` 把 skill name+desc 列進提示詞——機制**已存在** | `sysprompt/fragments.go:209` | 不必新做注入，是被關掉 |
| K2 | 成員提示詞**會**經過該段（disk-main 組裝） | `profiles.go:295` `ComposeDiskMainPrompt` → `fragments.go:268-270`（`if def.AdvertiseSkills { skillsList = skillsSection(ctx.Skills) }`） | 注入點現成 |
| K3 | 注入被 `AdvertiseSkills` 把關 | `profiles.go:280-281`（`if def.AdvertiseSkills { ctx.Skills = skills }`） | 成員預設關 → 不注入 |
| K4 | 成員預設 `advertise_skills: false` | `agentdef/loader.go:61` `profileYml.AdvertiseSkills`；vero-tech-swarm 各 `profile.yml` | 要 swarm 層強制開 |
| K5 | 成員 skill 工具讀**自己的** registry（per-agent 目錄） | `space.go:178` `WithSkillRegistry(ld.Skills)`；載入於 `loader.go:111`（`agents/sub/<name>/skills/`） | 工具端正確 |
| K6 | 但 prompt 端 skillRefs 會 fallback 去載 **cfg 全域 skills**（來源錯位） | `agent.go:321-329`（ToolState 已被 `WithSkillRegistry` 設過 → 此塊跳過 → `a.skillRefs` 仍 nil）→ `profiles.go:226-227`（nil → `loadDiskSkillRegistry(cfg)`） | prompt 與工具的 skill 來源不一致，需修 |
| K7 | 成員 active.yml **沒有 `skill` 工具** | vero-tech-swarm `lead`/`qa` 的 `tools/active.yml`；swarm ToolSet 只注入協作工具（`tools/set.go:69`） | 需預設掛上 skill 工具 |
| K8 | `skill.Registry` 有 `Add`/`AddBundled`，**無 `Remove`** | `pkg/skill/registry.go:108`/`:143`（無 remove） | 動態刪除需新做（或從磁碟 reload） |
| K9 | runtime **重建並重套 system prompt 的 seam 已存在** | `agent.go:1174-1179`（re-resolve → `a.profile.SystemPrompt = …` → `a.llm.Apply(llm.WithSystem(...))`） | 動態 reload 沿用此 pattern |
| K10 | skills 段尾**教模型「如何建立 skill」** | `fragments.go:229`（"How to create a skill: …"） | 與「禁止 agent 自建」相違，swarm 版要拿掉 |
| K11 | `skill` 工具是 **load-only**（讀 `LoadBody`），本就無著作能力 | `pkg/skill/registry.go:330` `LoadBody` | 紀律的另一半天然成立（只要別給著作工具/引導） |

**結論**：注入機制、per-agent skill 載入、runtime 重套 prompt 的 seam **全是現成的**。本 RP 是
「**打開被關掉的注入 + 接對來源 + 掛上 skill 工具 + 開一條 Web 的增刪/reload 路徑 + 拿掉自建引導**」。

---

## 3. 為什麼值得（與 KV cache 的取捨）

動態增刪 skill → reload system prompt → **必然使該成員下一輪的 prompt 前綴 cache miss**。Johnny 已
明確接受：**這種「workflow 中發現缺能力就即時補上」的動態擴容，價值遠大於一次 cache 重置**。緩解：

- **只重載受影響的那一個 agent**（不動整個 swarm）。
- **只在 User 明確操作時**才 reload（不是每輪、不是自動）。
- 重套在**成員下一個 run 邊界**生效（忙碌中不打斷 in-flight 呼叫；參照 [RP-7](RP-7-leader-scheduled-wake.md)
  「忙就延到邊界」的做法）。

---

## 4. Part A — 啟動注入（Phase 1，地基）

> **可開工 sub-tickets**：本節已細化為三張票（RP-10-1/2/3，含 file:line 級改動、AC、測試、風險）—— 見
> [`RP-10A-subtickets.md`](RP-10A-subtickets.md)。

### 4.1 強制 `advertise_skills`（K3/K4）

swarm 在組裝成員時，把 `def.AdvertiseSkills` **強制設為 true**（不論 `profile.yml` 寫什麼）——
這是使用者要求「**每一個 agent 都要具備 skill name+desc injection**」的直接落實，不該是 per-agent
可關的選項。落點：`space.go` 的 `registerDef`（注入團隊協定的同一個 chokepoint）。

### 4.2 接對 prompt 端的 skill 來源（K5/K6）

讓成員 prompt 的 skill 清單來自**該成員自己的 registry**（`ld.Skills`），與 `skill` 工具同源——
而不是 fallback 去載 cfg 全域 skills。做法：swarm 組裝時把 `refsFromRegistry(ld.Skills)` 當成該成員
的 prompt skillRefs 傳入（對應 `internal/agent` 既有的 skillRefs 注入點；必要時在 `pkg/agent` 暴露
一個「同時設定 skill 工具 registry ＋ prompt skillRefs」的選項，避免兩處來源分叉）。

> 驗收要點：成員提示詞列出的 skills == `agents/sub/<name>/skills/` 裡的 skills == `skill` 工具能載的
> skills（三者一致）。

### 4.3 每個成員預設掛 `skill` 工具（K7）

`skill` 是 evva **內建工具**（wire name `skill`），不是 swarm custom tool，所以不走 ToolSet，而是
**在組裝時強制把 `skill` 併入每個成員的 `ActiveTools`**（與 4.1 同一 chokepoint）。對 User 透明、
不需寫進 active.yml——呼應使用者「每一個 agent 都要預設具備 skill tool 供 load skill 使用」。

### 4.4 swarm 版 skills 段：拿掉「自建 skill」引導（K10/紀律）

`skillsSection` 結尾有一段教模型「如何建立 skill（去某目錄寫 SKILL.md）」。**swarm 成員不該被鼓勵
自建 skill**（會破壞穩定 swarm 的品質）。做法：給 `skillsSection` 一個「精簡模式」旗標（只列
name+desc + 「用 `skill` 工具載入」一句，**不含**建立教學），swarm 啟用之。`skill` 工具本身 load-only
（K11），所以只要不給著作工具、不給著作引導，紀律即成立。

---

## 5. Part B — Web 端動態增刪 ＋ reload（Phase 2）

### 5.1 檢視

```
GET /api/agents/{name}/skills?space=<ref>   → [{ name, description, source }]
```

後端讀該成員的 skill registry（`MemberContext` / roster → agent 的 ToolState registry，或重掃其
skills 目錄）。Web 在成員卡/詳情面板列出。

### 5.2 新增 / 刪除（User 著作，agent 不可）

```
POST   /api/agents/{name}/skills           { name, description, body }   # 寫 SKILL.md
DELETE /api/agents/{name}/skills/{skill}                                  # 移除
```

- **新增**：在 `<workdir>/agents/sub/<name>/skills/<skill-name>/SKILL.md` 寫出（第一行為 description
  標題、其後為 body，依 `skill` 套件慣例 `ParseTitleLine`），名稱衝突/非法名稱拒絕。
- **刪除**：移除該 skill 目錄（或標記）。
- 兩者皆**只由 User 經 Web 觸發**；無對應的 agent-facing 工具（紀律）。

### 5.3 reload：重掃 registry ＋ 重套 system prompt（K8/K9）

增刪後走一條新的 supervisor/service seam（概念）：

```go
func (s *Supervisor) ReloadMemberSkills(name string) error
// 1) 重掃 agents/sub/<name>/skills/ → 新 *skill.Registry（避開 K8「無 Remove」：直接從磁碟重建）
// 2) 換掉該成員 agent 的 ToolState registry（SetSkillRegistry）+ 重算 prompt skillRefs（同 4.2 來源）
// 3) 觸發既有的 profile re-resolve → 重套 WithSystem（沿用 agent.go:1174-1179 的 pattern）
// 4) 忙碌則延到下一個 run 邊界；persist
```

- 避開 `skill.Registry` 無 `Remove` 的限制：**從磁碟整包重建** registry，而非原地刪。
- 重套 system prompt = KV cache miss（已接受，§3）；只影響該成員、只在此刻。

### 5.4 Web UI

成員卡/詳情面板一個 **Skills 區**：列出 name+desc（標 source）＋「＋ 新增 skill」表單（name /
description / body textarea）＋ 每項的刪除鈕（複用 [RP-8](RP-8-web-agent-schedule-mgmt.md) 的
`ConfirmDialog`）。與 RP-8 的 agent CRUD 並列，構成「Web 動態 reconfigure agent」的一組能力。

---

## 6. Scope / Acceptance

**In**：Part A —— swarm 強制 `advertise_skills` ＋ 接對 prompt skill 來源（與工具同源）＋ 每成員預設掛
`skill` 工具 ＋ swarm 版 skills 段去掉自建引導。Part B —— Web 檢視/新增/刪除 agent skills ＋
`ReloadMemberSkills`（重掃 registry ＋ 重套 prompt，忙碌延到邊界）＋ Web Skills 面板 ＋ 持久化 ＋ 測試。

**Out**：讓 agent 自己建立/修改 skill（**明確禁止**，紀律）；改 evva 主 agent 的 `AdvertiseSkills`
預設（不動主線）；skill body 的版本控管/diff（之後再說）；跨 agent 共用 skill 的中央 catalog（v1 以
per-agent 目錄為準；共用另議）。

**Acceptance**：
1. 啟動後，任一成員的系統提示詞**列出自己 `agents/sub/<name>/skills/` 的 skill name+desc**，且該清單
   與 `skill` 工具能載入的清單**一致**（同源）。
2. 每個成員**預設具備 `skill` 工具**，能載入上述任一 skill 的 body。
3. 成員提示詞**不含**「如何建立 skill」的教學；無任何 agent-facing 的 skill 著作工具。
4. User 在 Web 對某 agent **新增** 一個 skill → 寫出 SKILL.md → 該 agent 的 system prompt **reload**
   後列出新 skill、且可被 `skill` 工具載入（成員下一輪即生效）。
5. User 在 Web **刪除** 某 agent 的 skill → reload 後提示詞不再列出、工具無法載入。
6. reload 只影響該成員、其餘成員提示詞與 cache 不受波及；忙碌中的成員在下一個 run 邊界才套用。
7. 重啟後，Web 增刪的 skills 仍在（落磁碟，隨成員 skills 目錄被重載）。

---

## 7. 落地任務（建議顆粒）

| # | 任務 | 階段 | 落點 |
| --- | --- | --- | --- |
| RP-10-1 | swarm 組裝時強制 `AdvertiseSkills=true` + 強制併入 `skill` 工具 | P1 | `internal/swarm/space.go`（`registerDef`/`constructMember`） |
| RP-10-2 | prompt skillRefs 接成員自己的 registry（與工具同源）；必要時 `pkg/agent` 暴露對應選項 | P1 | `internal/swarm/space.go`、`pkg/agent`、`internal/agent` |
| RP-10-3 | `skillsSection` 精簡模式（去「自建 skill」引導）；swarm 啟用 | P1 | `internal/agent/sysprompt/fragments.go` |
| RP-10-4 | `Supervisor.ReloadMemberSkills`：重掃 registry + 重套 prompt（忙延邊界）+ persist | P2 | `internal/swarm/supervisor.go`、`internal/agent`（暴露 refresh seam） |
| RP-10-5 | `GET/POST/DELETE /api/agents/{name}/skills`；`Backend` 方法 + service 實作（寫/刪 SKILL.md） | P2 | `webapi/api.go`、`service/service.go`、`pkg/skill`/`agentdef`（寫出 helper） |
| RP-10-6 | Web Skills 面板（列出 + 新增表單 + 刪除確認） | P2 | `web/src/components/`（成員卡/詳情） |
| RP-10-7 | 測試：注入同源、預設 skill 工具、無自建引導、Web 增刪→reload→生效、忙延邊界、重啟保留 | P1+P2 | 各 `*_test.go`、`web` 測試 |

> 一句話：注入機制本就存在、只是被旗標關著；本 RP **打開它並接對來源**，再把「**User 在 workflow 中
> 即時替 agent 補課**」做成一條 Web 路徑——而著作權留給 User、agent 只管載入，守住穩定 swarm 的品質。
