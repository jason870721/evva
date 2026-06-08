# EVVAgent — 使用手冊

## 目錄

- [1. 總覽 — TUI 介面一覽](#1-總覽--tui-介面一覽)
- [2. Slash 指令](#2-slash-指令)
  - [/config — 即時設定](#config--即時設定)
  - [/model — 切換提供者/模型](#model--切換提供者模型)
  - [/profile — 切換人格](#profile--切換人格)
  - [/effort — 思考強度](#effort--思考強度)
  - [/resume — 還原先前的工作階段](#resume--還原先前的工作階段)
  - [內建技能](#內建技能)
- [3. 快捷鍵](#3-快捷鍵)
- [4. Yank 模式 — 從對話紀錄複製](#4-yank-模式--從對話紀錄複製)
- [5. 對話紀錄搜尋](#5-對話紀錄搜尋)
- [6. 權限系統](#6-權限系統)
  - [權限模式](#權限模式)
  - [計畫模式（`enter_plan_mode` / `exit_plan_mode`)](#計畫模式enter_plan_mode--exit_plan_mode)
  - [工作樹（`enter_worktree` / `exit_worktree`)](#工作樹enter_worktree--exit_worktree)
  - [核准提示](#核准提示)
  - [權限規則](#權限規則)
- [7. 子代理與人格](#7-子代理與人格)
- [8. Hooks 鉤子](#8-hooks-鉤子)
  - [Hook 設定檔位置](#hook-設定檔位置)
  - [檔案格式](#檔案格式)
  - [事件](#事件)
  - [Payload 與 Decision](#payload-與-decision)
- [9. MCP 伺服器](#9-mcp-伺服器)
  - [設定伺服器](#設定伺服器)
  - [使用 MCP 工具](#使用-mcp-工具)
  - [資源（Resources）](#資源resources)
  - [需 OAuth 授權的伺服器](#需-oauth-授權的伺服器)
- [10. 設定參考](#10-設定參考)
  - [evva-config.yml](#evva-configyml)
  - [.env（選用）](#env選用)
  - [CLI 參數](#cli-參數)
- [11. 執行模式 — TUI vs CLI](#11-執行模式--tui-vs-cli)
- [12. 日誌](#12-日誌)
- [13. LSP — 語言伺服器協定支援](#13-lsp--語言伺服器協定支援)
  - [逐步設定（以 Go 為例）](#逐步設定以-go-為例)
  - [驗證 LSP 是否正常運作](#驗證-lsp-是否正常運作)
  - [其他語言設定](#其他語言設定)
  - [手動設定參考](#手動設定參考)
  - [使用方式](#使用方式)
  - [疑難排解](#疑難排解)
- [14. 以 evva 開發 — SDK（開發者指南）](#14-以-evva-開發--sdk開發者指南)
  - [快速開始 — 約 40 行的完整宿主程式](#快速開始--約-40-行的完整宿主程式)
  - [擴充點一覽](#擴充點一覽)
  - [穩定性與延伸閱讀](#穩定性與延伸閱讀)

---

## 1. 總覽 — TUI 介面一覽

```
┌──────────────────────────────────────────────────────────────┐
│ banner box / transcript                                      │
│                                                              │
│  ▶ user prompt                                               │
│  assistant text…                                             │
│                                                              │
├──────────────────────────────────────────────────────────────┤
│ ▰ TODOS         (only when non-empty)                        │
│   ▶ wire migration                                           │
├──────────────────────────────────────────────────────────────┤
│ ‹⠹ explorer› ‹▶ writer› ‹✔ reviewer›   ← active sub-agents   │
├──────────────────────────────────────────────────────────────┤
│ overlay panels: /config · /model · /profile · approval · …   │
├──────────────────────────────────────────────────────────────┤
│ > input                                                      │
├──────────────────────────────────────────────────────────────┤
│ ‹⠋ RUN› ◆ EVVA ◆ ▸ model ◆ in N out M ◆ CTX ▰▰▱…▱ 12%       │
└──────────────────────────────────────────────────────────────┘
```

面板在空白時會折疊至零高度。狀態列始終顯示在底部；`EVVA` 格顯示目前人格的名稱（已轉為大寫）——`/profile` 切換後會變成 `NONO`、`MY-PERSONA` 等。

---

## 2. Slash 指令

在輸入框開頭輸入 `/`，畫面會顯示建議面板。隨著你輸入更多字元，列表會依大小寫不敏感的 prefix 比對進行過濾。當輸入內容與某個指令**完全相符**時，該列會變為綠色並顯示 `✓`——按下 Enter 即可執行。

| 按鍵 | 效果 |
| --- | --- |
| `Tab` | 自動補全為高亮的建議選項 |
| `↑` / `↓` | 移動高亮建議選項 |
| `Enter` | 送出當前輸入（若為有效指令則執行） |
| `Esc` | 在此輸入階段關閉建議面板 |

可用指令：

| 指令 | 功能 |
| --- | --- |
| `/config` | 開啟設定表單 |
| `/model` | 切換 LLM 提供者/模型 — **會清除對話歷史** |
| `/profile` | 切換代理人格（evva、nono…）— **會清除對話歷史** |
| `/effort` | 設定思考強度（low / medium / high / ultra） |
| `/compact` | 壓縮對話紀錄 — 可選 micro 或 full |
| `/resume` | 還原此工作目錄下先前的工作階段 |
| `/clear` | 清除對話紀錄（保留 banner） |
| `/exit`、`/quit` | 離開 |

使用者安裝的技能（skills）也會出現在此清單中——任何放在 `~/.evva/skills/<name>/SKILL.md` 或 `<workdir>/.evva/skills/<name>/SKILL.md` 的技能都會以 `/<name>` 的形式出現在同一個建議面板。

### /config — 即時設定

開啟一個帶邊框的表單，列出所有可編輯的設定：

```
┌─ /CONFIG ────────────────────────────────────────┐
│ ▶ max_iterations           30                    │
│   max_tokens               4096                  │
│   auto_compact_threshold   0.8                   │
│   display_thinking         true                  │
│   fetch_max_bytes          100000                │
│   tavily_api_key           ****wxyz              │
│   anthropic.api_key        (empty)               │
│   …                                              │
│ [↑↓] navigate · [Enter] edit/toggle · [Esc] close│
└──────────────────────────────────────────────────┘
```

| 按鍵 | 效果 |
| --- | --- |
| `↑` / `↓` | 移動游標 |
| `Enter` | 編輯聚焦的欄位（布林值直接切換） |
| `Enter`（編輯器中） | 套用並儲存 |
| `Esc` | 取消編輯（或在列表模式關閉面板） |

API 金鑰欄位會開啟密碼遮罩編輯器；貼上功能照常運作（顯示維持遮罩狀態）。

**即時生效**（立即套用）：

- `max_iterations` — 迴圈安全上限
- `display_thinking` — 切換對話紀錄中的思考區塊顯示
- `auto_compact_threshold` — 上下文壓縮的觸發時機

**已儲存但需重新啟動**（需要重建 client / web 工具）：

- `max_tokens`、`fetch_max_bytes`、`tavily_api_key`、所有 `<provider>.api_key`、所有 `<provider>.api_url`

每次編輯都會立即寫入 `~/.evva/config/evva-config.yml`。

#### 在對話中變更設定

你不必自己打開表單 —— 可以直接用自然語言請 evva 讀取或變更設定（「我的
display_thinking 設定是什麼？」「把 auto-memory 關掉」「將 max_iterations
設為 40」）。在底層，模型會使用一個 `config` 工具，它暴露與此表單相同的設定鍵
（`max_iterations`、`display_thinking`、`default_effort`、
`<provider>.api_key` 等），再加上表單沒有的幾個（`default_effort`、
`default_profile`）。

讀取會直接完成、不會跳出提示。寫入則會經過權限提示，顯示為
`Set <key> to <value>`，因此未經你核准不會有任何變更。祕密值讀回時會被遮罩
（`****wxyz`）。此工具無法切換使用中的模型（請用 `/model`）或變更權限模式
（請用 Shift+Tab）。若想停止針對特定設定的詢問，輸入 `/permissions` 並為
`config` 新增一條 allow 規則。

### /model — 切換提供者/模型

開啟一個清單，顯示程式已知的所有 `(provider, model)` 組合，游標預設停在目前使用中的項目上：

```
┌─ /MODEL ─────────────────────────────────────────────────────┐
│ Swapping clears the conversation — provider-specific state   │
│ (thinking signatures) can't carry across providers.          │
│                                                              │
│   ollama / qwen3.6                                           │
│   anthropic / claude-sonnet-4-6                              │
│   anthropic / claude-opus-4-7                                │
│ ▶ deepseek / deepseek-v4-pro  (current)                      │
│   deepseek / deepseek-v4-flash                               │
│   openai / gpt-5.4-mini                                      │
│   openai / gpt-5.5                                           │
│                                                              │
│ [↑↓] navigate · [Enter] switch · [Esc] cancel                │
└──────────────────────────────────────────────────────────────┘
```

| 按鍵 | 效果 |
| --- | --- |
| `↑` / `↓` | 瀏覽清單 |
| `Enter` | 切換至高亮的模型 |
| `Esc` | 取消 |

**重要：** 切換模型必定會清除對話。Anthropic 的 `ThinkingSignature` 綁定特定提供者——若帶著舊對話紀錄跨提供者切換，下一次請求會回傳 400 錯誤。新的選擇也會儲存為 `default_provider` + `default_model`，讓下次啟動時直接沿用。

若有執行中的任務則無法切換；請先按 Esc 取消任務，再輸入 `/model`。

### /profile — 切換人格

切換代理的人格——不同的身份、系統提示詞與工具集。內建 `evva`（完整工具包的軟體工程師人格）隨二進位檔附帶；你可以在 `~/.evva/` 底下建立 `agents/<name>/` 目錄來新增其他人格：

```
~/.evva/agents/nono/
├── system_prompt.md   # 人格本體（必要）
├── tools.yml          # { active: [...], deferred: [...] }
└── meta.yml           # { as: [main|subagent|both], when_to_use, inject_memory, advertise_skills }
```

`meta.yml` 欄位：

| 欄位 | 意義 |
| --- | --- |
| `as` | `[main]`、`[subagent]` 或 `[main, subagent]` 之一。`main` 讓人格出現在 `/profile`；`subagent` 讓人格可透過 Agent 工具的 `subagent_type` 列舉呼叫 |
| `when_to_use` | 在選單中顯示於名稱旁邊的一句簡述 |
| `inject_memory` | 為 `true` 時，人格的系統提示詞會收到 `EVVA.md` + `~/.evva/memory/` 索引（以及型別化記憶指引與召回）。預設 `false` |
| `advertise_skills` | 為 `true` 時，人格的提示詞會列出已安裝的技能目錄。預設 `false` |

選單會列出所有 `as:` 包含 `main` 的人格：

```
┌─ /PROFILE ───────────────────────────────────────────────────┐
│ Switching clears the conversation — each persona has its own │
│ system prompt and tool surface.                              │
│                                                              │
│ ▶ evva  (current)  — full-kit software-engineer              │
│   nono             — finance / numbers persona               │
│                                                              │
│ [↑↓] navigate · [Enter] switch · [Esc] cancel                │
└──────────────────────────────────────────────────────────────┘
```

切換後對話紀錄會清空、狀態列的人格名稱會更新為新人格的大寫形式，並把新人格儲存為 `default_profile`，讓下次啟動就以該人格開啟。

宣告為 `as: [main, subagent]` 的人格**同時**可從執行中的根代理透過 Agent 工具呼叫——這就是跨人格委派（例如 `evva` 在不離開階段的情況下，將財務問題委派給 `nono`）。

若有執行中的任務則無法切換；請先按 Esc 取消任務，再輸入 `/profile`。

### /effort — 思考強度

調整模型的推理深度。四個等級：

| 等級 | 使用時機 |
| --- | --- |
| `low` | 快速查找、「X 的語法是什麼」 |
| `medium` | 預設——大多數的撰碼任務 |
| `high` | 非簡單的推理、多步驟重構 |
| `ultra` | 架構性決策、難以察覺的 bug 排查 |

各提供者會把這四個等級對應到自己的旋鈕——Anthropic 的 effort 等級、DeepSeek 的 thinking 開關 + 等級、OpenAI 的 reasoning effort 等。對於只有粗略開/關開關的提供者，`low` → 關閉，其餘 → 開啟。所選的等級會儲存為 `default_effort`，並顯示在狀態列上（`▸ model · ⚡high`）。

### /resume — 還原先前的工作階段

從目前的工作目錄還原先前的工作階段。每次迭代的狀態都會持久化到 `~/.evva/sessions/<workdir-slug>/<session-id>.json`，所以關閉 TUI 再重新開啟並不會遺失工作——`/resume` 會把對話帶回你離開時的狀態。

選單以每頁 10 筆、依最後寫入時間遞減排序的方式列出最近活動的工作階段。每一列以一行預覽顯示該階段的第一個使用者提示，並附上人格、訊息數量與模型：

```
┌─ /RESUME ────────────────────────────────────────────────────┐
│ 還原先前的工作階段 — 僅限同一工作目錄，依最近寫入時間遞減。  │
│ 還原會清除目前的對話畫面，並以儲存的版本取代。               │
│                                                              │
│ ▶ 串接 /resume slash 指令與 overlay                          │
│     5m ago · evva · 42 msgs · claude-opus-4-7                │
│   移植型別化記憶目錄 + 相關性召回                            │
│     2h ago · evva · 87 msgs · claude-opus-4-7                │
│   驗證跨平台 release 工作流                                  │
│     1d ago · evva · 18 msgs · deepseek-v4-pro                │
│   …                                                          │
│                                                              │
│ page 1 / 3                                                   │
│ [↑↓] 游標 · [←→] 翻頁 · [Enter] 還原 · [Esc] 取消             │
└──────────────────────────────────────────────────────────────┘
```

| 按鍵 | 效果 |
| --- | --- |
| `↑` / `↓` | 在當前頁面移動游標 |
| `←` / `→` | 切換到前一頁／下一頁（每頁 10 筆） |
| `Enter` | 還原所選的工作階段 |
| `Esc` | 取消 |

**還原時會還原什麼：**

- 完整的訊息歷史——每個使用者提示、助理回應、思考區塊、工具呼叫與工具結果都會重新放入對話畫面，你可以往上捲動查看先前的工作內容。
- 該階段使用的人格、提供者與模型。若這些已不存在（人格被刪除、目前的 build 沒有該模型），則會回退到 `evva` 或目前的預設，並在日誌中記錄警告。
- session-id——後續儲存會覆蓋同一個檔案而非新增，所以還原後的階段在選單中仍維持單一條目。
- 狀態列上的累計用量（usage）與 context 條。

**作用範圍：** 工作階段以發起時的工作目錄為界。在不同目錄下執行 `evva` 會顯示該目錄的工作階段；全部的工作階段儲存在 `~/.evva/sessions/`，並依 workdir slug 分類（例如 `-Users-alice-lab-myrepo`）。

**儲存頻率：** 每次迴圈迭代（即每次工具來回）後都會重寫檔案，所以即使 evva 崩潰，最多只會遺失一次 LLM 呼叫的工作量。

**壓縮行為：** 執行完整的 `/compact` 會以壓縮後的摘要覆蓋同一個工作階段檔案——選單依然只顯示一筆，但內容變成摘要而非原始對話。

**子代理：** 只有根代理的工作階段會被持久化。透過 Agent 工具產生的子代理依設計為短暫的，永遠不會出現在 `/resume` 中。

若有執行中的任務則無法還原；請先按 Esc 取消任務，再輸入 `/resume`。

### 內建技能

evva 內建五個**內建技能（bundled skills）**——由官方提供、代理可呼叫的指令文件。當請求符合時模型會自動使用它們，你也可以自行輸入 `/<name>` 來呼叫：

| 技能 | 用途 |
| --- | --- |
| `/commit` | 為目前的變更草擬並建立 git commit，以 evva 作為作者。 |
| `/review` | 審查 GitHub pull request（使用 `gh`）。 |
| `/security-review` | 針對分支待提交變更進行聚焦式安全審查。 |
| `/simplify` | 三位審查者並行清理（重用／品質／效率），接著套用修正。 |
| `/setup-hooks` | 引導你在 `.evva/settings.json` 中撰寫生命週期掛鉤（見第 8 節）。 |

內建技能是**最低優先序**的層級：在 `~/.evva/skills/<name>/SKILL.md` 或 `<workdir>/.evva/skills/<name>/SKILL.md` 放置**同名**的 `SKILL.md`，即可無聲覆蓋內建內容。技能在啟動時載入——新增或編輯後請重新啟動 evva。關於撰寫自訂技能與 SDK 方式，請參見[以 evva 開發](#13-以-evva-開發--sdk開發者指南)與 `docs/extending.md`。

---

## 3. 快捷鍵

| 按鍵 | 效果 |
| --- | --- |
| `Enter` | 送出 |
| `Ctrl+J` / `Alt+Enter` | 插入換行（多行輸入） |
| `↑` / `↓` | 瀏覽提示歷史（輸入框為空或已在瀏覽時） |
| `Esc` | 取消執行中的任務 / 關閉面板 |
| `Ctrl+C` | 按一次：取消執行中任務 · 閒置時：離開 |
| `Ctrl+D` | 離開（輸入框為空時） |
| `Ctrl+O` | 切換展開所有工具結果（折疊/展開較長的 bash 與 read 輸出） |
| `Ctrl+Y` | 開啟 **yank 模式** — 選取區塊並複製其乾淨內容 |
| `Ctrl+F` | 開啟 **對話紀錄搜尋** — 輸入查詢字串，`Enter`/`n` 循環跳轉 |
| `Shift+Tab` | 循環切換 **權限模式** — `default → accept_edits → plan → bypass → …` |
| `PgUp` / `PgDown` / `Home` / `End` | 捲動對話紀錄 |
| 滑鼠滾輪 | 捲動對話紀錄 |

---

## 4. Yank 模式 — 從對話紀錄複製

對話紀錄中的每個區塊都會在左側繪製時間軸裝飾線（`│`、`├─` 等），讓對話以結構化方式呈現。缺點是：一般終端機的拖曳選取會複製畫面上所有可見內容——包含這些裝飾符號。貼到其他視窗後會得到像這樣的結果：

```
▶ who are you?
│
│ I'm evva — an interactive coding assistant…
│
```

要複製不含裝飾的乾淨內容，evva 內建了 **yank 模式**，能夠辨識區塊邊界。這是標準的乾淨複製途徑；在終端機不完整支援剪貼簿逸出序列時，也是唯一可用的方式。

**使用 `Ctrl+Y` 開啟。** 一次只會在一個區塊上顯示青色粗體的邊欄提示；狀態列上方的提示文字會顯示當前游標位置（`yank 3/5`）與按鍵對照。

| 按鍵 | 效果 |
| --- | --- |
| `j` / `↓` | 下一個區塊（較新） |
| `k` / `↑` | 上一個區塊（較舊） |
| `g` | 跳到第一個區塊 |
| `G` | 跳到最後一個區塊 |
| `Enter` / `c` | 將聚焦區塊的乾淨文字複製到系統剪貼簿 |
| `e` | 僅切換此區塊的展開/折疊（在複製長工具輸出前很實用） |
| `q` / `Esc` | 離開 yank 模式（清除邊欄提示） |
| `Ctrl+C` | 離開 + 退出 evva |

**複製了什麼。** 每個區塊提供一個 `PlainText()` 視圖，會移除 ANSI 控制碼與裝飾符號。使用者提示區塊對應提示文字，助手文字區塊對應 markdown 原始碼（非渲染後輸出），工具區塊則為呼叫標頭（`◢ name(...)`）加上結果內文。成功時狀態列會閃爍 `copied N chars`。

**技術細節 — OSC52。** Yank 模式使用 [OSC52](https://wezfurlong.org/wezterm/escape-sequences.html#operating-system-command-sequences) 終端機逸出序列將內容寫入剪貼簿。不需外部函式庫，也不依賴 `pbcopy`。終端機會將逸出序列轉發至作業系統剪貼簿。

| 終端機 | 是否預設可用？ |
| --- | --- |
| **iTerm2** | 是（預設） |
| **kitty** | 是 |
| **WezTerm** | 是 |
| **Alacritty** | 是 |
| **Ghostty** | 是 |
| **Apple Terminal.app** | 預設不可用 — 需啟用 `編輯 → 允許剪貼簿存取` 或更換終端機 |
| **tmux** | 需設定 `set -g set-clipboard on` |
| **GNU screen** | 大多無法使用；請改用 `Ctrl+Y` 從宿主終端機操作 |

若寫入失敗（內容超過 100 KB、終端機阻擋），狀態列會顯示 `clipboard: <error>`，yank 模式保持開啟，讓你可以嘗試其他區塊。

**為什麼不用原生拖曳選取？** evva 啟用滑鼠捕捉是為了讓滾輪能夠捲動對話紀錄。這項取捨使得拖放複製無法以原生方式運作——即使現代終端機支援 `Shift`/`Alt`+拖曳的繞過機制，選取結果仍然包含渲染後的裝飾符號（因為它們本就是畫在螢幕上的內容）。Yank 模式是將乾淨內容從程式內帶出的正式流程。

---

## 5. 對話紀錄搜尋

按下 `Ctrl+F` 開啟搜尋列。輸入查詢字串後按 `Enter` 跳到第一個匹配項。按 `n` 向前循環匹配項，或按 `N`（Shift+n）向後循環。按 `Esc` 關閉搜尋列。

---

## 6. 權限系統

### 權限模式

evva 透過**權限模式**對每個工具呼叫進行把關。共有四種模式，使用 `Shift+Tab` 循環切換：

| 模式 | 不需詢問即自動允許 | 適合情境 |
| --- | --- | --- |
| **`default`** | 唯讀工具（`read`、`tree`、`grep`、`glob`、`web_*`、`json_query`、`calc`、`daemon_list`、`daemon_output`）、代理自協調工具（`agent`、`todo_write`、`skill`、`tool_search`、`ask_user_question`），以及**唯讀 bash 指令**（`ls`、`cat`、`head`、`grep`、`git status`、`git log`、…）。檔案寫入與其他 bash 指令**會詢問**。 | 初學者、敏感工作、預設姿態 |
| **`accept_edits`** | 同 `default` + 檔案編輯（`edit`、`write`、`notebook_edit`）+ 常見檔案系統 bash 指令（`mkdir`、`touch`、`mv`、`cp`、`rmdir`、`ln`、`chmod`、`chown`）。 | 審閱中的程式碼迭代 |
| **`plan`** | 與 `default` 相同的唯讀安全清單。清單外的任何操作**直接拒絕**（不顯示提示）。 | 在決定修改前先探索程式碼庫 |
| **`bypass`** | 全部允許。危險指令分類仍會在背景記錄，但絕不阻擋。 | **僅限隔離容器與虛擬機使用** — 會傳遞至子代理 |

當前模式在狀態列中以彩色標籤顯示（`⛨ plan`、`⛨ bypass`、…）。`default` 會折疊此欄位以保持介面簡潔。

**以指定模式啟動：**

```bash
evva -permission-mode=plan                # 最安全：先調查
evva -permission-mode=accept_edits        # 自動套用編輯 + 安全的檔案系統指令
evva -permission-mode=bypass              # 無提示；僅限沙箱環境
```

CLI 參數優先；持久性預設值可寫入 `evva-config.yml`：

```yaml
permission_mode: default     # default | accept_edits | plan | bypass
```

### 計畫模式（`enter_plan_mode` / `exit_plan_mode`）

計畫模式是 `permission_mode: plan` 搭配兩個模型可呼叫的工具自動化整個流程。模型在處理非平凡任務（新功能、架構決策、跨多檔重構）時可自行切入計畫模式；你也能透過 `Shift+Tab` 手動進入。

**完整流程：**

1. **進入** — 模型呼叫 `enter_plan_mode`（或你用 `Shift+Tab` 切到 `plan`）。狀態列顯示 `⛨ plan`。除了一個專用的計畫檔之外，所有寫入都會被拒絕。
2. **計畫檔** — `<workdir>/.evva/plans/current.md`。每個 session 一份。`enter_plan_mode` 會建立或清空此檔；模型用一般的 `write` / `edit` 將計畫以 markdown 寫入此處。權限把關僅對這個確切路徑開放；其他任何寫入目標仍會被硬性拒絕，並顯示 *「plan mode forbids writes — Shift+Tab to exit plan mode」*。
3. **探索** — `read`、`grep`、`glob`、`tree`、`agent`（派生 `explore` 子代理）全部自動允許。模型藉此調查程式碼庫、起草計畫並反覆修改。
4. **退出** — 計畫完成後，模型呼叫 `exit_plan_mode`。evva 從磁碟讀取計畫檔並彈出 **Plan Approval** 覆蓋層，將 markdown 內容顯示出來：

```
┌─ PLAN APPROVAL ────────────────────────────────────┐
│ tool: exit_plan_mode                               │
│ mode: plan                                         │
│ reason: Plan approval — review and approve to exit │
│                                                    │
│ plan:                                              │
│   # Phase 7 — Plan Mode                            │
│   ## Context                                       │
│   …                                                │
│   ## Design                                        │
│   …                                                │
│                                                    │
│ ▶ [1] Allow once     (核准計畫並退出模式)          │
│   [2] Allow for…     (計畫場景幾乎用不到)          │
│   [3] Deny           (退回 — 模型會迭代)           │
└────────────────────────────────────────────────────┘
```

- **核准**（`1` / Enter）— 退出計畫模式，還原為先前的模式（`default` / `accept_edits` / 進入 `enter_plan_mode` 前的任何模式），模型開始實作。
- **拒絕**（`3` / Esc）— 鍵入一行原因；模型會收到 `"User requested changes: <原因>"`，留在計畫模式繼續修改計畫檔。

**注意事項：**

- 系統提示已告知模型：`exit_plan_mode` 就是核准信號，絕不能用 `ask_user_question` 問「這個計畫可以嗎？」。
- 子代理無法翻轉父 session 的計畫模式 — `enter_plan_mode` / `exit_plan_mode` 僅限根代理使用。
- 計畫檔在退出後仍會保留；下一次 `enter_plan_mode` 會清空它。若想保留某份計畫，請在重新進入計畫模式前先把 `current.md` 複製出 `.evva/plans/`。

### 工作樹（`enter_worktree` / `exit_worktree`）

工作樹（worktree）是同一個 git 倉庫在另一個分支上的平行 checkout，存放於獨立目錄。在你想要一個隔離的沙箱時使用：高風險的重構、會破壞性的實驗、想隨時可以丟棄的平行 feature 分支。

模型**只**會在你明確說出「worktree」時才呼叫這對工具 — 像是「開個 worktree」、「在叫 demo 的 worktree 裡做」、「離開 worktree」。比較模糊的說法（「切個分支」、「重構這段」）會讓 session 留在原本的 workdir。

**工作流程：**

1. **進入** — 模型呼叫 `enter_worktree`（可選擇傳入 `name`）。evva 執行 `git worktree add -b worktree-<slug> <repo>/.evva/worktrees/<slug>/ HEAD` 並將 session 的工作目錄切到新工作樹。之後的 `read` / `edit` / `write` / `bash` 都在工作樹中執行 — 原本目錄完全不會被動到。
2. **工作** — 正常驅動 session。讀檔、編輯、提交都發生在工作樹的獨立分支上。
3. **退出** — 完成後，模型呼叫 `exit_worktree`，搭配 `action: "keep"` 或 `action: "remove"`：
   - `"keep"` — 工作樹目錄與分支留在磁碟上。想之後回來繼續或合併時用這個。
   - `"remove"` — 執行 `git worktree remove --force` 並刪除分支。若工作樹中有未提交的變更，除非你明確說「移除並丟棄變更」（模型會以 `discard_changes: true` 重新呼叫），否則工具會拒絕。
4. Session 還原至原始目錄；EVVA.md 與系統提示會以原 workdir 重建。

**子代理隔離** — `agent` 工具接受 `isolation: "worktree"`。以該旗標 spawn 一個子代理時，會在 `.evva/worktrees/agent-<id>/` 下建立屬於該子代理的工作樹，子代理整個生命週期都跑在裡面。若乾淨退出（沒有檔案變動、沒有新提交），evva 會自動移除工作樹；否則保留在磁碟，子代理結果裡會回報 `worktree_path:` / `worktree_branch:`，方便你檢視或合併。

**注意事項：**

- 工作樹位於 `<repo>/.evva/worktrees/<slug>/`。如果還沒把 `.evva/` 加進 `.gitignore`，建議加上。
- 計畫模式會拒絕 `enter_worktree` / `exit_worktree`（它們不在唯讀白名單裡）。需要新建工作樹時請先退出計畫模式。
- 子代理無法在中途自行進入工作樹 — 僅根代理可以呼叫工具對。AgentTool 的 `isolation` 參數才是讓子代理跑在工作樹裡的官方做法。
- v1 沒有 `.worktreeinclude` 支援 — 被 gitignore 的檔案（`.env`、本機設定）不會自動複製到新工作樹。需要時請在工作樹裡手動建立。

### 核准提示

在 `default` / `accept_edits` / `plan` 模式下，任何需要核准的操作都會彈出模態對話框：

```
┌─ APPROVAL ─────────────────────────────────────────┐
│ tool: bash                                         │
│ mode: default  risk: dangerous (sudo)              │
│ reason: matches dangerous prefix                   │
│                                                    │
│ input: sudo rm /tmp/evil-file                      │
│                                                    │
│ ▶ [1] Allow once                                   │
│   [2] Allow for this session                       │
│   [3] Deny                                         │
│                                                    │
│ [↑↓] choose · [Enter] confirm · [Esc] deny         │
└────────────────────────────────────────────────────┘
```

| 按鍵 | 效果 |
| --- | --- |
| `↑` / `↓` | 在按鈕間移動 |
| `1` / `a` | 允許一次 — 僅執行本次呼叫 |
| `2` / `s` | 允許此工作階段 — 同時新增記憶體規則，後續類似呼叫不再提示 |
| `3` / `d` | 拒絕 — 再按 Enter 可輸入提供給模型的拒絕原因 |
| `Enter` | 確認高亮選項（或送出拒絕原因） |
| `Esc` | 等同拒絕 |
| `Ctrl+C` | 拒絕 + 退出 |

**「允許此工作階段」** 會根據呼叫內容選擇合適的規則形式：對 `bash` 儲存第一個 token（因此核准 `git status` 後，後續 `git …` 呼叫都會放行，而非任意指令）；對 `read`/`write`/`edit` 儲存檔案路徑；其他工具則為工具層級的放行。工作階段規則在退出後消失；若要持久化，請手動編輯 `permissions.json`。

平行核准（代理在同一回合發出兩個 `bash` 呼叫）會堆疊 — 處理完最上層後，下一個會自動浮現。

### 權限規則

規則讓核准持久化，跨執行不會重複看到相同提示。有兩個作用範圍：

- `<workdir>/.evva/permissions.json` — **專案級**：跟隨 repo，可透過 git 分享
- `~/.evva/permissions.json` — **使用者級**：在所有工作目錄生效

格式：

```json
{
  "permissions": {
    "allow": [
      "bash(git:*)",
      "bash(npm:*)",
      "read(src/**)",
      "edit",
      "tree"
    ],
    "deny": [
      "bash(sudo:*)",
      "bash(rm -rf /)"
    ],
    "ask": [
      "bash(npm publish)"
    ]
  }
}
```

**規則語法**：`ToolName` 匹配該工具的所有呼叫。`ToolName(content)` 加入內容匹配：

| 工具 | 內容語法 | 範例 |
| --- | --- | --- |
| `bash` | `prefix:*`、`pattern *`、`git *` 或精確指令 | `bash(git:*)`、`bash(npm install *)`、`bash(make build)` |
| `read`、`write`、`edit`、`notebook_edit` | 針對 `file_path` 的 doublestar glob | `read(src/**)`、`write(./tmp/*.txt)`、`edit(**/*.go)` |
| 其他 | 對原始輸入的精確字串比對 | 少用；建議使用工具層級規則 |

**優先順序：**

1. `bypass` 模式 — 一律允許，忽略規則。
2. **deny 規則** — 最先檢查，在所有非 bypass 模式中優先於 allow。
3. **ask 規則** — 強制顯示提示，即使有更廣泛的 allow（或模式安全清單）匹配。
4. `plan` 模式 + 工具不在唯讀安全清單 → **拒絕**（無提示）。
5. 唯讀 / 自協調安全清單 → 允許。
6. Bash + 分類器判定為唯讀（`ls`、`cat`、`git status`、…）→ 允許。
7. 僅 `accept_edits`：`edit`/`write`/`notebook_edit` → 允許；bash 常見檔案系統指令（`mkdir`/`mv`/`cp`/…）→ 允許。
8. **allow 規則** — 匹配 → 執行。
9. 最終回退 — 詢問。

各行為（deny/ask/allow）內的來源優先順序為 `session > project > user`，因此工作階段的「允許此工作階段」會覆蓋使用者範圍規則，但永遠不會覆蓋 deny。

---

## 7. 子代理與人格

根代理可以生成子代理。內建兩種：

- **`explore`** — 唯讀檢查。工具僅限 `read`、`grep`、`tree`、`glob`、`web_search`、`json_query`。模型在進行「X 定義在哪／哪些檔案參照 Y」這類查詢時使用，沒有變更風險。
- **`general-purpose`** — 具寫入能力。攜帶 fs + shell + web + util 工具集。

執行中的子代理會在輸入框上方以水平橫列晶片顯示。非同步子代理在背景完成——其摘要會在下一次迭代中以模擬使用者訊息的形式出現在頂端，對話會自動接收。架構嚴格為兩層：子代理無法再生成子代理。

**使用者自訂子代理。** 在 `~/.evva/` 底下放入 `agents/<name>/` 目錄（與 `/profile` 同樣的格式——見上文），並在 `meta.yml` 設定 `as: [subagent]`。從磁碟載入的代理會自動出現在 Agent 工具的 `subagent_type` 列舉中，無須重新編譯。

**跨人格委派。** 宣告為 `as: [main, subagent]` 的人格既可在 `/profile` 中選擇，也可從執行中的根代理呼叫。這就是讓內建 `evva` 能將財務問題委派給使用者自訂 `nono` 人格的機制——根代理呼叫 Agent 工具並指定 `subagent_type: "nono"`，spawner 以 `nono` 的系統提示詞與工具建立子代理、執行一次，再把摘要送回 evva 的對話紀錄。

你不需要手動驅動子代理；模型會自行決定何時生成。

---

## 8. Hooks 鉤子

Hooks 是使用者自訂的 shell 指令或 HTTP webhook，在代理迴圈中的六個明確時機觸發。用途包括：工具呼叫前驗證、編輯後自動格式化、自訂日誌、阻擋已知不良指令，或在長時間執行的核准上把通知送到 Slack／桌面通知器。

### Hook 設定檔位置

兩個檔案，皆為選用，於啟動時合併：

- `<workdir>/.evva/settings.json` — **專案** hook。隨 repo 存在，可選擇透過 git 分享。
- `~/.evva/settings.json` — **使用者** hook。在每個工作目錄都會套用。

專案 hook 先觸發；專案 hook 回傳 `"continue": false` 會中斷該次觸發的使用者 hook。格式不正確的條目會在啟動時於 stderr 顯示為警告——其餘檔案內容仍會載入。

### 檔案格式

JSON 格式（與 Claude Code 的 `settings.json` `hooks` 區塊相容，兩邊的檔案可互通）：

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "bash",
        "hooks": [
          { "type": "command", "command": "/path/to/check.sh", "timeout": 30 }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "edit|write",
        "hooks": [
          { "type": "command", "command": "goimports -w \"$EVVA_TOOL_INPUT_PATH\"" }
        ]
      }
    ],
    "Notification": [
      {
        "hooks": [
          { "type": "http", "url": "https://hooks.slack.com/...", "method": "POST", "async": true }
        ]
      }
    ]
  }
}
```

**Matcher**：對工具名稱進行 doublestar glob 比對。空 matcher = 全部符合。支援交集（`bash|grep`）與萬用字元（`tool_*`）。不附帶工具名稱的事件（SessionStart、Stop、Notification）會忽略 matcher。

**Hook 條目欄位**：

| 欄位 | 適用 | 意義 |
| --- | --- | --- |
| `type` | 兩者 | `"command"`（shell 子行程）或 `"http"`（HTTP 請求） |
| `command` | command | shell 指令。stdin 為 JSON payload；stdout 為可選的 decision |
| `url` | http | 接收 payload 的 endpoint |
| `method` | http | HTTP method，預設 `POST` |
| `headers` | http | 選用的 headers 對應 |
| `timeout` | 兩者 | 秒（1–600）。預設視事件而定 |
| `async` | 兩者 | 觸發後不等待。command 預設 `false`，http 預設 `true` |

子行程 hook 的環境變數會包含 `EVVA_PROJECT_DIR`。

### 事件

| 事件 | 觸發時機 | 典型用途 |
| --- | --- | --- |
| `SessionStart` | 代理啟動一次 | 預熱快取、為第一個 prompt 注入額外脈絡 |
| `UserPromptSubmit` | 使用者 prompt 加入 session 之前 | prompt 驗證、機密遮蔽 |
| `PreToolUse` | 權限閘門執行之前 | 阻擋不良呼叫、改寫參數、覆蓋閘門 |
| `PostToolUse` | 工具回傳之後 | 自動格式化、保留日誌、為下一輪附加脈絡 |
| `Stop` | 主代理進入終止輪（沒有更多工具呼叫） | 摘要匯出、稽核日誌 |
| `Notification` | 迭代上限、內部錯誤、需要核准 | 在長時間核准時送 Slack 通知、桌面通知 |

### Payload 與 Decision

每個 hook 都會收到一個 JSON payload（command 從 stdin、webhook 從 HTTP body）。共同信封：

```json
{
  "session_id": "...",
  "transcript_path": "...",
  "cwd": "/abs/working/dir",
  "permission_mode": "default",
  "agent_id": "uuid",
  "agent_type": "main",
  "hook_event_name": "PreToolUse"
}
```

事件特有欄位：

- `SessionStart`：`source`（`"startup"`）、`model`
- `UserPromptSubmit`：`prompt`
- `PreToolUse`：`tool_name`、`tool_input`（模型送出的原始 JSON）、`tool_use_id`
- `PostToolUse`：`tool_name`、`tool_input`、`tool_use_id`、`tool_response`、`is_error`
- `Stop`：`last_assistant_message`、`stop_hook_active`
- `Notification`：`message`、`title`、`notification_type`

Command hook 可以把一個 JSON 物件寫到 stdout 來影響迴圈：

```json
{
  "continue": false,
  "decision": "block",
  "reason": "lint failed: see stderr",
  "systemMessage": "ran golint, found 3 issues",
  "hookSpecificOutput": {
    "permissionDecision": "deny",
    "permissionDecisionReason": "vendor directory is read-only",
    "additionalContext": "the next turn should retry the edit elsewhere",
    "updatedInput": { "file_path": "/safer/path.go" }
  }
}
```

各事件的效果：

- **PreToolUse**：`hookSpecificOutput.permissionDecision`（`"allow"` / `"deny"` / `"ask"`）覆蓋閘門。`updatedInput` 在閘門檢查前改寫工具參數。`decision: "block"` 或 `continue: false` 以給定的 `reason` 直接阻擋該呼叫。
- **PostToolUse**：`additionalContext` 會附加到 LLM 下一輪看到的工具結果。`block` / `continue` 會被忽略——post-tool hook 無法把已執行的工具收回。
- **UserPromptSubmit**：`additionalContext` 會附加到使用者 prompt。`block` / `continue: false` 會完全丟棄該 prompt。
- **Stop**：`block` / `continue: false` 會再次進入迴圈一次（`stop_hook_active` 旗標可防止無限重入）。
- **SessionStart**：`additionalContext` 與 `hookSpecificOutput.initialUserMessage` 會插入第一個使用者 prompt 的前面。
- **Notification**：stdout 會被忽略——純粹是側通道訊號。

stdout 為空（或非 JSON）的 hook 表示「沒有意見、直接通過」。Command hook 回傳結束碼 2 會被視為硬性阻擋，訊息從 stderr 讀取。

超過 `timeout` 的子行程會被強制終止，其 decision 也會被丟棄。HTTP hook 預設為非同步觸發即忘——失敗會記錄日誌但永遠不會阻擋迴圈。

---

## 9. MCP 伺服器

evva 可以消費任何 [Model Context Protocol](https://modelcontextprotocol.io) 伺服器（檔案系統、GitHub、Slack、Notion，或你自己的內部伺服器）所提供的工具與資源，完全不需為每個伺服器寫程式。已設定的伺服器會在啟動時連線，其工具會以 `mcp__<server>__<tool>` 的名稱出現在 evva 的延遲工具（deferred tool）目錄中，並在需要時透過 `tool_search` 載入。

### 設定伺服器

MCP 伺服器設定放在 **與 hooks 相同的 `settings.json` 檔案**裡的 `mcpServers` 區塊——所有 evva 擴充設定都集中於一處：

- **專案層級：** `<workdir>/.evva/settings.json`
- **使用者層級：** `~/.evva/settings.json`（`<APP_HOME>/settings.json`）

同名時，專案層級會覆蓋使用者層級。

```json
{
  "mcpServers": {
    "filesystem": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "${HOME}/work"]
    },
    "github": {
      "type": "http",
      "url": "https://api.githubcopilot.com/mcp/",
      "headers": {"Authorization": "Bearer ${GITHUB_MCP_TOKEN}"}
    }
  }
}
```

各伺服器欄位：

| 欄位 | 適用於 | 說明 |
| --- | --- | --- |
| `type` | 兩者 | `"stdio"` 或 `"http"`。省略時由 `command`→stdio／`url`→http 自動推斷。 |
| `command`、`args`、`env` | stdio | 啟動子行程。`${VAR}` 與 `${VAR:-default}` 會在載入時從環境變數展開。 |
| `url`、`headers` | http | Streamable HTTP 傳輸（2025-03-26 規格）。header 值同樣會展開環境變數。 |
| `timeout` | 兩者 | 連線逾時秒數。預設 30，最大 600。 |
| `disabled` | 兩者 | `true` 會完全略過該伺服器——不啟動子行程、不發送請求。 |

設定錯誤的伺服器永遠不會阻擋啟動：失敗會記錄日誌（在[日誌](#12-日誌)中尋找 `mcp: connect` 行），該伺服器被略過，其餘伺服器照常連線。編輯 `settings.json` 會在下次啟動時生效（不支援熱重載）。

### 使用 MCP 工具

被發現的工具屬於**延遲工具**——它們的名稱會列在系統提示的 `<available-deferred-tools>` 區塊，但 schema 不會預先載入。evva 第一次需要時會用 `tool_search` 取得工具 schema，與其他延遲工具一樣。你不需要做任何特別的事：直接請 evva 完成任務，它會自行尋找並呼叫 MCP 工具。

MCP 工具呼叫與內建工具走相同的流程：

- **權限**（[§6](#6-權限系統)）：第一次呼叫未知的 MCP 工具會要求核准。若要永久允許，請以完整名稱新增規則——`mcp__filesystem__read_file`——或使用萬用字元如 `mcp__filesystem__*` / `mcp__*`。deny 規則會阻擋。
- **Hooks**（[§8](#8-hooks-鉤子)）：像 `mcp__**__write_*` 這樣的 `PreToolUse` matcher 會在符合的 MCP 呼叫前觸發，可阻擋、改寫輸入或覆寫權限決策——用一條規則就能稽核所有 MCP 寫入。
- **子代理**（[§7](#7-子代理與人格)）：子代理共用父代理的 MCP 連線，因此被委派的任務不需重新連線即可使用相同的伺服器。

### 資源（Resources）

部分 MCP 伺服器除了工具之外還會提供**資源**（檔案、紀錄、文件）。有兩個延遲工具可跨所有已連線伺服器運作：

- `list_mcp_resources`——列出可用資源（每筆都標記其來源 `server`）。可用 `server` 參數過濾單一伺服器。
- `read_mcp_resource`——以 `{server, uri}` 讀取單一資源。文字直接內嵌回傳；二進位內容會存到 `~/.evva/mcp-blobs/` 並回傳路徑（需要原始位元組時可用 `read` 工具讀回）。

### 需 OAuth 授權的伺服器

若 HTTP 伺服器在首次連線時回傳 `401 Unauthorized`，evva 會將它標記為 `needs-auth`，並提供一個一次性的 `mcp__<server>__authenticate` 工具（而非該伺服器的真正工具）。當 evva 呼叫它時，你會看到一個帶有授權 URL 的 `ask_user_question` 提示：

1. 在瀏覽器開啟該 URL 並完成登入。
2. 在提示中選擇 **「I'm done」**。

evva 接著會重新連線，該伺服器的真正工具會在本次工作階段內變為可用。（本版本的 token 僅保存在記憶體中——重啟 evva 後需重新授權。）

---

## 10. 設定參考

### evva-config.yml

路徑：`~/.evva/config/evva-config.yml`。首次啟動時自動建立。可透過 TUI 的 `/config` 即時編輯，或手動修改：

```yaml
# Agent loop
max_iterations: 30
max_tokens: 4096
auto_compact_threshold: 0.8
display_thinking: true

# Default model used at startup (overwritten by /model swap)
default_provider: deepseek
default_model: deepseek-v4-pro

# Default thinking effort: low | medium | high | ultra. Overwritten by /effort.
default_effort: medium

# Default persona that boots — must match an agent name in the registry
# (built-in "evva" or a user-authored agent under ~/.evva/agents/<name>/).
# Overwritten by /profile. Empty falls back to "evva".
default_profile: evva

# Permission stance at startup. Cycle at runtime with Shift+Tab; -permission-mode CLI flag overrides.
permission_mode: default     # default | accept_edits | plan | bypass

# Web tooling
fetch_max_bytes: 100000
tavily_api_key: ""

# 記憶（位於 ~/.evva/memory/ 的型別化記憶目錄）
enable_auto_memory: true     # 記憶指引 + MEMORY.md 索引 + 寫入豁免 + 召回
enable_memory_recall: true   # 每回合相關性側查詢（成本開關；設為 false 只保留索引）
memory_recall_model: ""      # 留空 = 當前供應商中較便宜的模型（anthropic→sonnet、deepseek→flash、openai→gpt-5.4-mini @ medium；ollama→當前模型+effort）

# Per-provider credentials. Empty api_url falls back to the constant's default.
providers:
  anthropic: { api_key: "", api_url: "" }
  deepseek:  { api_key: "", api_url: "" }
  openai:    { api_key: "", api_url: "" }
  ollama:    { api_url: "" }
```

### 記憶

evva 在 `~/.evva/memory/` 維護單一全域、以檔案為基礎的記憶。每則記憶是一個帶有
`name` / `description` / `type` frontmatter 的 Markdown 檔（四種型別為 `user`、
`feedback`、`project`、`reference`），目錄中的 `MEMORY.md` 為其索引。代理會用它
平常的檔案工具自行寫入與更新這些檔案；寫入限定在記憶目錄內者會自動核准，因此不會
為每則筆記提示你。

- **永遠載入的內容**：僅 `MEMORY.md` 索引（一份目錄），讓提示詞保持精簡。
- **相關性召回**：每回合開始時，一次廉價的側查詢會拉入與你訊息相關的少數記憶，
  它們會以 `<system-reminder>` 出現在對話紀錄／日誌中。設定
  `enable_memory_recall: false` 可只保留索引而略過此額外呼叫。預設使用當前供應商中
  較便宜的模型——Anthropic → Sonnet、DeepSeek → v4-flash、OpenAI → gpt-5.4-mini
  （皆為 medium effort）；Ollama 則沿用當前模型與 effort——亦可用 `memory_recall_model` 指定特定模型。
- **新鮮度**：召回時超過一天的記憶會在前面附上其年齡與「在當作事實前先對照現有
  程式碼驗證」的提醒。
- **關閉方式**：`enable_auto_memory: false`（或 `EVVA_AUTO_MEMORY=0`）會停用整個
  子系統——不建立目錄、不召回、提示詞中也沒有記憶區段。

> 舊的雙檔模型（`USER_PROFILE.md` + 各專案的 `projects/<key>/MEMORY.md`）以及
> `update_user_profile` / `update_project_memory` 工具已移除。舊檔仍保留在磁碟上但
> 不再讀取——若有值得保留的內容，請複製到新的記憶中。

### .env（選用）

放置於工作目錄或 `~/.evva/.env`。僅用於部署 / 日誌控制——絕非使用者偏好設定：

```bash
APP_ENV=dev            # dev | prod
LOG_LEVEL=info         # debug | info | warn | error
LOG_FORMAT=text        # text | json
LOG_DIR=               # 未設定 → $EVVA_HOME/logs（預設）；填寫路徑 → 自訂目錄；明確設為空 → 改用 stdout
SKILLS_DIR=skills      # ~/.evva/ 下的子路徑
USER_PROFILE=user_profile.md
```

### CLI 參數

```bash
evva                                # 互動式 TUI（stdout 為 TTY 時預設）
evva -temp 0.7                      # 取樣溫度（預設不設定）
evva -max-tokens 2048               # 每次 completion 輸出上限（覆蓋 YAML）
evva -max-iters 40                  # 迴圈迭代上限（覆蓋 YAML）
evva -permission-mode=plan          # 以 plan 模式啟動（唯讀）
evva -permission-mode=bypass        # 以關閉權限閘門啟動
evva -no-tui "explain loop.go"      # 單次純文字模式
echo "list files in /tmp" | evva -no-tui   # 管線輸入提示
```

---

## 11. 執行模式 — TUI vs CLI

**互動式 TUI**（stdout 為 TTY 時預設）。包含對話紀錄、面板、狀態列等完整功能。

**純文字 CLI**（`-no-tui`，或 stdout 被管線重定向時）。單次流程：從參數/stdin 讀取提示 → 執行代理 → 以純文字串流事件 → 退出。CLI 模式沒有互動式核准介面——任何需要提示的呼叫都會**自動拒絕**，並提示可傳入 `-permission-mode=bypass` 或在 `permissions.json` 中新增規則。適用於腳本與 CI 環境。

---

## 12. 日誌

每個代理的純文字日誌預設存放於 `$EVVA_HOME/logs/<agent-id>/<agent-id>.log`——`make install` 之後不需要額外設定即可找到。若要改寫到其他目錄，在 `.env` 中設定 `LOG_DIR=/your/path`。若要回到舊的 stdout-only 開發模式(日誌打到終端而非寫檔)，將 `LOG_DIR=` 明確設為空字串。`LOG_LEVEL=debug` 會揭露每次迭代的 `turn.start` / `llm.call` / `tool.dispatch` / `tool.result` 行——在除錯代理卡住或無限迴圈時非常實用。

---

## 13. LSP — 語言伺服器協定支援

evva 整合了語言伺服器（Language Server），讓終端機裡的程式碼代理能夠直接查詢語意資訊。

### 支援的操作

`lsp_request` 工具可讓代理查詢語言伺服器：

| 操作 | 說明 |
|---|---|
| `go_to_definition` | 跳至符號的定義位置 |
| `find_references` | 找出所有引用該符號的位置 |
| `hover` | 取得該位置的型別資訊與文件 |
| `document_symbols` | 列出檔案中的所有符號 |
| `workspace_symbol` | 以名稱搜尋整個工作區的符號 |
| `go_to_implementation` | 找出介面或型別的實作 |
| `call_hierarchy` | 追蹤呼叫圖（傳入／傳出呼叫） |

此外，LSP 伺服器會**自動推送診斷訊息**（錯誤、警告）——它們會以系統提醒的形式出現在對話中，代理無需主動請求。

---

### 逐步設定（以 Go 為例）

此範例使用 Go 與 gopls。同樣的模式適用於 TypeScript、Rust 或任何有 LSP 伺服器的語言。

#### 1. 安裝 LSP 伺服器

```bash
go install golang.org/x/tools/gopls@latest
```

確認已安裝在 PATH 上：

```bash
which gopls
# /Users/you/go/bin/gopls

gopls version
# golang.org/x/tools/gopls v0.21.1
```

#### 2. 在專案中啟動 evva

進入任何 Go 專案目錄（含有 `go.mod` 的目錄）並啟動 evva：

```bash
cd /path/to/your-go-project
evva
```

evva 會自動偵測 `go.mod` 與 PATH 上的 `gopls` — 無需撰寫設定檔。

若自動偵測未生效（少見情況），可建立最小設定檔：

```yaml
# .evva/lsp_servers.yml
servers:
  gopls:
    command: gopls
    extensions:
      ".go": "go"
    startupTimeout: "120s"
    maxRestarts: 3
```

#### 3. 驗證 LSP 是否正常運作

在 evva 對話中，請代理使用 LSP：

```
找出 server.go 中 Server 型別的定義
```

代理會以 `operation: "go_to_definition"` 呼叫 `lsp_request`。第一次請求會啟動 gopls（初始索引可能需要 30–60 秒）。後續請求即時回應。

手動檢查 LSP 伺服器狀態：

```
daemon_list
```

應該會看到 LSP 守護程序條目：

```
daemon l1 [lsp/running] server=gopls state=running restarts=0/3
```

#### 4. 測試常用操作

在 evva 中嘗試以下提示來測試不同的 LSP 功能：

- **定義：**「找出 `tool.go` 第 22 行 `Manager` 的定義」
- **引用：**「找出專案中所有引用 `Daemon` 的地方」
- **懸停：**「`tool.go` 第 22 行的 `ctx` 是什麼型別？」
- **符號：**「列出 `agent.go` 中的所有符號」
- **工作區搜尋：**「在工作區中搜尋與 'Agent' 匹配的符號」
- **呼叫階層：**「顯示 `NewTool` 的呼叫階層」

---

### 其他語言設定

#### TypeScript / JavaScript

```bash
npm install -g typescript-language-server typescript
```

當 `package.json` 存在且有 `.ts`/`.tsx` 檔案時自動偵測。

#### Rust

```bash
rustup component add rust-analyzer
```

當 `Cargo.toml` 存在時自動偵測。

#### 其他語言

建立 `.evva/lsp_servers.yml` 並填入對應語言的伺服器。常見伺服器：

| 語言 | 伺服器 | 安裝指令 |
|---|---|---|
| Python | pyright | `pip install pyright` |
| Zig | zls | [zigtools.org/zls](https://zigtools.org/zls/) |
| C/C++ | clangd | `apt install clangd` / `brew install llvm` |

Python 設定範例：

```yaml
servers:
  pyright:
    command: pyright-langserver
    args: ["--stdio"]
    extensions:
      ".py": "python"
    startupTimeout: "60s"
```

---

### 手動設定參考

在專案根目錄建立 `.evva/lsp_servers.yml`（專案層級），或放在 `~/.evva/lsp_servers.yml`（使用者層級，套用至所有專案）。相同伺服器名稱下，專案層級設定會覆蓋使用者層級。

完整設定格式：

```yaml
servers:
  gopls:
    command: gopls                    # 必要：二進位檔名稱或路徑
    args: []                          # 選用：CLI 參數
    extensions:                       # 必要：副檔名 → 語言 ID
      ".go": "go"
    env:                              # 選用：環境變數
      GOPATH: "${HOME}/go"
    startupTimeout: "120s"            # 選用：等待初始化的最長時間（預設 30s）
    maxRestarts: 3                    # 選用：崩潰復原上限（預設 3）
```

`command`、`args` 與 `env` 的值支援環境變數展開（`${VAR}`、`${HOME}`）。

---

### 使用方式

`lsp_request` 工具為**延遲載入** — 代理在需要 LSP 功能時會透過 `tool_search` 發現它。你可以直接向代理提出類似以下的問題：

- 「`UserService` 定義在哪裡？」
- 「找出所有引用 `authenticate` 的地方」
- 「這個變數是什麼型別？」
- 「列出 `handler.go` 中的所有符號」
- 「誰呼叫了 `processRequest`？」

代理會在適當時機自動使用 `lsp_request`。

---

### 疑難排解

**「gopls not found in PATH」**
安裝缺少的伺服器（見上方安裝指令），重新啟動 evva 後再試。

**「No LSP server configured for extension .py」**
在 `.evva/lsp_servers.yml` 中新增該語言的伺服器設定。錯誤訊息中的提示會建議應安裝的伺服器名稱。

**伺服器已啟動但請求無回應**
gopls 首次啟動時需要時間為專案建立索引。大型專案可能需要 60–120 秒。請在設定中提高 `startupTimeout`，並在第一次 `lsp_request` 後等待 — 後續請求就會很快。

**沒有出現診斷訊息**
診斷訊息會在 `lsp_request` 開啟檔案後出現。如果你用 `write`/`edit`/`bash` 編輯了檔案，請對該檔案呼叫 `lsp_request` 以重新整理診斷訊息。

**殘留的 gopls 程序**
執行 `pkill gopls` 清理。evva 在關閉時會終止伺服器，但若 evva 崩潰，伺服器程序可能殘留。

---

## 14. 以 evva 開發 — SDK（開發者指南）

前面把 evva 當作應用程式介紹；evva 同時也是可嵌入的 Go SDK：另一支程式可以 `import "github.com/johnny1110/evva/pkg/agent"`，組出自己的 ReAct agent — 自訂 LLM 提供者、自訂工具、自己的人格、權限政策與 UI — 不需 fork，也不必碰 agent 迴圈。

整個公開介面都在 `pkg/*` 底下。Go 的 `internal/` 規則在編譯期強制這條界線：下游模組若不小心引用 `evva/internal/...` 會無法編譯。自 `v1.0.0` 起，旗艦的 `cmd/evva` 本身也完全建構在 `pkg/*` 之上，所以內建應用程式做得到的事，你的程式也做得到。

```bash
go get github.com/johnny1110/evva@v1.0.0
```

### 快速開始 — 約 40 行的完整宿主程式

一個宣告式的 `agent.Config` 搭配幾個選項，就能得到完整體驗 — 內建終端機 UI、人格 `/profile` 切換、權限提示、`/resume` 與 `/compact`。`agent.New` 會吸收整個啟動流程：解析人格（找不到時退回 `evva`）、自動載入 `EVVA.md` 與 `~/.evva/memory/` 目錄及技能目錄、載入權限規則庫，並安裝核准／提問 broker。

```go
package main

import (
    "context"
    "os"
    "os/signal"
    "syscall"

    "github.com/johnny1110/evva/pkg/agent"
    "github.com/johnny1110/evva/pkg/config"
    _ "github.com/johnny1110/evva/pkg/llm/builtins" // 註冊 anthropic/deepseek/openai/ollama
    "github.com/johnny1110/evva/pkg/ui/bubbletea"
)

func main() {
    cfg := config.Get() // 或 config.Load(config.LoadOptions{AppName: "myapp", AppHome: ...})

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    tui := bubbletea.New(cfg.AppHome) // 內建參考 TUI（實作 ui.UI）

    ag, err := agent.New(agent.Config{AppConfig: cfg},
        agent.WithSink(tui),        // agent 將事件送進 UI
        agent.WithRootContext(ctx), // Ctrl-C 會關閉所有背景工作
    )
    if err != nil {
        panic(err)
    }
    defer ag.Shutdown()

    tui.Attach(ag.Controller()) // 把 agent 的 controller 檢視交給 UI
    _ = tui.Run(ctx)
}
```

不需要 TUI？拿掉它即可：用 `agent.New(agent.Config{AppConfig: cfg, PermissionMode: "bypass"})` 建構，再呼叫 `ag.Run(ctx, "你的提示")`。沒有 sink 時，agent 會自動拒絕核准請求（避免卡住）；`"bypass"` 則在可信任／CI 環境中全部自動放行。

### 擴充點一覽

每個部分都能透過 `pkg/*` 的接縫替換：

| 想要… | 使用 |
| --- | --- |
| 新增 LLM 提供者 | 在 `llm.DefaultRegistry()` 註冊工廠函式；你的 `llm.Client` 需實作 `Name` / `Model` / `SupportsDeferLoading` / `Complete` / `Stream` / `Apply`。 |
| 新增工具 | 實作 `tools.Tool`；以 `agent.WithCustomTool(name, factory)` 傳入，或註冊到 `toolset.DefaultRegistry()`。 |
| 新增人格 | `agent.BuildAgentRegistry` + `reg.Register(agent.AgentDefinition{...})`（或在 `<AppHome>/agents/<name>/` 放置檔案）；以 `Config.Personas` + `Config.Persona` 傳入。會驅動 `/profile` 與子代理。 |
| 控制核准 | `Config.PermissionMode`、`Config.PermissionStore`，或自訂 `agent.WithPermissionBroker`（以 `permission.NewBroker` + `SetOnRequest` 建立）。 |
| 自製 UI | 實作 `ui.UI`；透過完全公開的 `ui.Controller` 驅動 agent。或直接嵌入 `pkg/ui/bubbletea`。 |
| 提供技能（skill） | `skill.NewRegistry()` + `Add(...)`（程式碼）或放置 `SKILL.md` 檔案；以 `agent.WithSkillRegistry` 傳入。 |
| 加入生命週期掛鉤 | 在 `.evva/settings.json` 加入 `hooks` 區塊；掛鉤會在 SessionStart、UserPromptSubmit、PreToolUse、PostToolUse、Stop、Notification 事件觸發。詳見[生命週期掛鉤](#生命週期掛鉤)。 |
| 使用自訂家目錄 | `config.Load(config.LoadOptions{AppName, AppHome, ...})` → `Config.AppConfig`。 |

### 穩定性與延伸閱讀

`v1.0.0` 讓 **Stable** 等級套件受主版號承諾保護：`pkg/agent`、`pkg/config`、`pkg/event`、`pkg/llm`、`pkg/tools`、`pkg/toolset`、`pkg/permission`、`pkg/ui`、`pkg/skill`、`pkg/constant`。Experimental 等級套件（`pkg/ui/bubbletea`、`pkg/tools/lsp`、`pkg/observable`、`pkg/tools/kits`）在次版號仍可能變動。

- [`integration.md`](../en/integration.md)（英文）— 逐步整合教學。
- [`docs/extending.md`](../../extending.md)（英文）— 完整參考：每個公開套件、每個擴充點，以及無法覆寫的部分。
- [`docs/sdk-stability.md`](../../sdk-stability.md)（英文）— 各套件穩定性等級與如何在 `go.mod` 釘選 evva。
- [`examples/full-host/`](../../../examples/full-host/main.go) — 可執行的完整宿主（TUI + 人格 + 權限），獨立模組。

### 生命週期掛鉤

掛鉤（hook）是使用者編寫的 shell 指令或 HTTP webhook，會在 agent 迴圈的六個時機點觸發。在 `.evva/settings.json` 中設定：

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "bash",
        "hooks": [
          {
            "type": "command",
            "command": "jq '.tool_input' | grep -q dangerous && exit 2 || exit 0",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

**事件：**
- `SessionStart` — agent 首次執行時觸發一次
- `UserPromptSubmit` — 每次使用者輸入送出前觸發
- `PreToolUse` — 每次工具執行前觸發；可阻擋、變更輸入或覆寫權限
- `PostToolUse` — 工具執行後觸發；可將額外內容附加到結果中
- `Stop` — agent 到達終止回合時觸發；可重新進入迴圈一次
- `Notification` — 頻外事件觸發（如達到迭代上限）

**掛鉤類型：**
- `type: "command"` — shell 指令，stdin 接收 JSON 承載。exit 0 → 解析 stdout 為決策；exit 1 → 非阻塞錯誤（記錄日誌）；exit 2 → 阻擋。
- `type: "http"` — HTTP POST，預設為非同步。

專案掛鉤（`.evva/settings.json`）比使用者掛鉤（`<APP_HOME>/settings.json`）先觸發。格式錯誤的設定檔會產生啟動警告，agent 仍會正常啟動。
- [`examples/minimal-host/`](../../../examples/minimal-host/main.go) — 可執行的精簡宿主（自訂提供者 + 工具 + 技能）。
