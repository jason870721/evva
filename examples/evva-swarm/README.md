# EVVA Swarm 範例集

evva swarm 讓多個 evva agent 組成一個**團隊 (space)** 協作:一位 **leader**
主持並對使用者負責,若干 **workers** 各帶自己的 system prompt、model 與
工具集。成員之間用 `send_message` 對話、用任務看板 (`task_create` /
`task_assign` / `task_verify`) 派工,你從 Web UI 觀戰或隨時插話。

這個資料夾收錄五套**開箱即用**的 swarm 範例,形狀刻意不同,
可以當成自己組隊的起點:

| 範例 | 成員 | 形狀 | 演示什麼 |
| --- | --- | --- | --- |
| [`starter/`](starter/) | 1 leader + 2 workers | 最小可跑團隊 | 入門模板:leader 派工、builder 做、reviewer 審 |
| [`tech-team/`](tech-team/) | 1 leader + 6 專家 | 完整交付團隊 | PM / 設計 / 前後端 / QA,真實工程隊形 |
| [`werewolf-swarm/`](werewolf-swarm/) | 1 上帝 + 12 玩家 | 回合制對話遊戲 | 嚴格回合控管、私訊資訊紀律、純 `send_message`(不用看板) |
| [`world-football/`](world-football/) | 1 總監 + 7 專家 | 六階段資料管線 | 任務看板派工、階段驗收、平行收集、多輪辯證 |
| [`code-review-swarm/`](code-review-swarm/) | 1 審查長 + 4 成員 | 平行扇出 + 對抗驗證 | 三路平行審查、去重、逐條對抗驗證守住報告品質 |
| [`fanout-refactor/`](fanout-refactor/) | 1 leader + 1 模板 worker（分身隨需） | 動態工作流（DWF） | 任務圖 `depends_on` 自動派工、`verify: auto` 無 leader 直流、`member_spawn` 臨時分身自生自滅 |

> `starter/` 與 `tech-team/` 原本放在 `docs/`,是搭配[群組使用指南](../../docs/user-guide/swarm/en.md)的
> 教學用範例;其餘三套是不同形狀的完整演示。

## 一個 swarm 專案長什麼樣

每套範例都是同一個極簡結構 — **一個資料夾就是一個 swarm**:

```
<swarm 專案>/
├── evva-swarm.yml          # 隊伍清單:name、leader、workers、settings
├── <公開知識>.md            # RULES.md / PIPELINE.md / REVIEW-GUIDE.md —
│                           #   所有成員可用 read 查閱的共同規則
└── agents/
    ├── main/<leader>/       # leader 一位
    │   ├── profile.yml      #   model、effort、when_to_use
    │   ├── system_prompt.md #   這個成員是誰、怎麼工作
    │   └── tools/active.yml #   一般工具清單(協作工具由角色自動注入)
    └── sub/<worker>/...     # workers 若干,結構同上
```

幾個關鍵點:

- **協作工具不用列**:`send_message`、任務看板等由 leader/worker 角色
  自動注入;`tools/active.yml` 只列一般工具(`read`、`write`、`bash`...)。
  工具給得越少,成員越不會亂跑 — 狼人殺的玩家只有 `read`。
- **`profile.yml` 在 space 建立時固定**:之後改 model/effort 要 **reset**
  space 才生效。
- **`settings.permission_mode: bypass`** 讓整場跑完不需逐一核准;想逐步
  監督改回 `default`。`max_iterations` 給足空間,避免長流程中途停下等推一把。
- 執行期產物(`.vero/`、各範例的狀態/產出資料夾)都在 `.gitignore` 裡 —
  範例本身只有純設定檔。

## 怎麼跑(三套通用)

```sh
# 1. 啟動 host(會印出 session token)
evva service start

# 2. 進入想跑的範例資料夾,註冊 swarm
cd werewolf-swarm        # 或 world-football / code-review-swarm
evva swarm .
#    → registered space <id>
```

打開 `http://127.0.0.1:8888`,貼上 token,進入對應的 space,
在 **Member Console** 對 leader 說第一句話:

| 範例 | 對誰說 | 說什麼 |
| --- | --- | --- |
| werewolf-swarm | `god` | 「開始遊戲」 |
| world-football | `director` | 「預測 2026-06-15 阿根廷 vs 法國(世界盃小組賽)」 |
| code-review-swarm | `lead` | 「審查 /path/to/repo 的 feature-x 對 main 的 diff」 |

**觀戰**:用**時間軸 / firehose stream** 看全場訊息流;點 roster 裡的成員
進入 ta 的視角;用看板的範例(football / code-review)看**任務看板**最快 —
未結案的任務就是流程走到哪了。你可以隨時私訊任何成員 — 訊息走同一條 bus,
不會中斷流程。

**常用操作**:

```sh
evva swarm ls            # 列出 space(找 id)
evva swarm stop <id>     # 停掉 space
rm -rf .vero <產出資料夾>  # 清掉狀態重來(各範例 README 有確切清單)
evva swarm .             # 重新註冊
```

## 三套範例

### 🐺 werewolf-swarm — 12 人狼人殺

一場完整的 12 人標準局(預女獵守):上帝用 `bash` 真隨機抽籤發牌、私訊
推動夜晚(守衛→狼人→預言家→女巫)、公開主持白天(發言→投票→放逐),
直到屠邊分出勝負。玩家沒有預設身分,每局重抽;牌局內幕在
`game/game-state.md`(上帝視角,劇透注意)。

最值得看的是**回合制紀律**:上帝一次只向一位玩家徵詢、收到回覆才走下一步 —
13 個 agent 的對話遊戲不靠這條會直接失控。詳見
[`werewolf-swarm/README.md`](werewolf-swarm/README.md)。

### ⚽ world-football — 足球賽果預測管線

一條六階段資料管線:企劃定義資料規格 → 三位收集者平行抓資料(`data/raw/`)
→ 品管清洗合併成 `data/dataset.xlsx`(關鍵缺口可觸發一輪補抓)→ 分析師
特徵工程 + Poisson 建模 → 預測專家與分析師多輪辯證 → 最終預測
`work/prediction.md`。

最值得看的是**任務看板當管線狀態機**:總監每階段開任務、親自 `read` 產出
檔案驗收後才結案、上一階段不清不開下一階段。需要專案 `.venv`(openpyxl /
pandas / scikit-learn),詳見
[`world-football/README.md`](world-football/README.md)。

### 🔍 code-review-swarm — 程式碼審查小組

對任一本機 repo 的指定範圍做審查:正確性 / 安全 / 品質三路**平行**審查 →
審查長去重 → **對抗驗證者**逐條重讀程式碼、以推翻為目標驗證,推翻不了的
才進最終報告 `review/report.md`(refuted 的也列出,讓你知道哪些「看起來
像問題」其實不是)。整個 swarm 對目標 repo 一律唯讀。

最值得看的是**對抗驗證**這一段:發現靠廣度、可信度靠對抗 — 單一 reviewer
的誤報在這裡被擋下來。詳見
[`code-review-swarm/README.md`](code-review-swarm/README.md)。

## 自己組一隊

從形狀最接近的範例複製整個資料夾改起:

1. `evva-swarm.yml`:改 `name`、leader、workers 清單。
2. `agents/main/<leader>/system_prompt.md`:leader 的主持邏輯是整個 swarm
   的骨架 — 通訊協定(誰可以跟誰說話)、階段紀律(什麼條件才走下一步)、
   狀態檔格式(leader 唯一可靠的記憶)三件事寫清楚,worker 再簡單都能跑。
3. 每位 worker:`system_prompt.md` 寫清楚「你只負責什麼、產出寫到哪、
   完成後恰好回一則給 leader」;`tools/active.yml` 給最小工具集。
4. 公開知識(規則、流程、格式定義)抽成根目錄一份 `.md`,讓所有成員
   `read` 同一份,不要在每個 system prompt 裡各抄一遍。
5. `evva swarm .` 註冊開跑;改了 `profile.yml` 記得 reset space。
