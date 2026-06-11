# RP-24 — Per-member permission_mode（RP-11 細規則之上的粗旋鈕）

> 狀態：**✅ 已實作（2026-06-11，feature/RP-24）** ｜ 階段：**第五波** ｜ 優先：**P2** ｜ 日期：2026-06-11
> 觸發：Sunday swarm 重整。Sunday 全 space 跑 `bypass`，唯一原因是 trader 的下單與 watchdog 的快照寫檔必須免審批——但這同時把**研究員們**的 bash/write 也全部 ungate 了。testnet 假錢可以接受；真金白銀的 swarm 不行。
> 關聯：[RP-11](RP-11-event-routing-and-scoped-lever.md)（已實作 per-member `permissions.json` 細規則——本文是它的粗粒度補集）、[RP-2](RP-2-permission-broker-routing.md)（審批路由，per-member 模式複用其管線）、`internal/swarm/tools/set.go:36-52`（init 註解自陳「worker 的 file/shell 才是真正權限邊界」）
> 請求者：Sunday。**無 Sunday-specific code。**

---

## 1. Problem（observed）

權限粒度目前是「space 級模式 + member 級規則」，中間缺一層：

| 層 | 已有 | 缺 |
| --- | --- | --- |
| space | `settings.permission_mode`（default/bypass/…） | — |
| member 粗 | **（無）** | **per-member mode 覆寫** |
| member 細 | RP-11 `agents/<name>/permissions.json`（method/url scoped rules） | — |

細規則理論上能模擬粗模式（給 trader 一串 allow 規則），但實務上：

1. 「這個成員全自主、那個成員全要審」是**編組決策**，自然的家是 manifest（團隊一份檔案講清楚），不是散在 N 份 permissions.json 裡用 glob 窮舉；
2. bypass 語義不只 allow-all——還包含「不彈框、不佔審批佇列」；用規則窮舉漏一條就卡一次（對無人值守的成員 = RP-14 才救得回來的 stall）；
3. `set.go` 的 init 註解自己說了：*"The actual permission boundary is a Worker's file/shell writes"*——既然邊界在成員身上，模式旋鈕就該能落在成員身上。

## 2. Proposal

1. **manifest `memberYml` 加 `permission_mode`**（leader 與 worker 皆可；值域同 settings）：

   ```yaml
   settings:
     permission_mode: default        # space 預設
   workers:
     - agent: trader
       permission_mode: bypass       # 執行台全自主
     - agent: analyst-news           # 省略 = 繼承 settings
   ```

2. **解析優先序**：member 欄位 > `settings.permission_mode`；非法值在 manifest load 時整份拒收（fail-fast，比照 effort 驗證 `space.go:226-229` 的先例）。
3. **落點**：`constructMember`（`internal/swarm/space.go`）把 resolved mode 設進該成員的 config clone——與 model/effort pin 同一 pattern，無新管線。
4. **與 RP-11 疊加語義（要寫進文件）**：mode 先裁決大方向，`permissions.json` 規則在 `default` 模式下開洞（allow）或在任何模式下封口（deny）。`bypass` + deny 規則 = deny 仍生效（deny 永遠最強）。
5. `list_members` / Web roster 顯示每成員的 effective mode——operator 一眼看清誰在自主跑。

## 3. Why evva（not Sunday）

信任分級是 swarm runtime 的權限模型。Sunday 今天的選擇（全 space bypass）是**被迫的二選一**；框架補上中間檔位，「analysts default、trader bypass-with-deny-rules」這類真實編組才表達得出來。

## 4. Acceptance

- bypass space 裡標 `default` 的成員：其 write/bash 走審批框（RP-2 管線），其他成員不受影響。
- default space 裡標 `bypass` 的成員：其工具免審批；deny 規則仍攔。
- 省略欄位的成員行為與今日完全相同（純繼承，零回歸）。
- 非法值（如 `permission_mode: yolo`）→ `evva swarm .` 註冊即報錯。
- `list_members` 顯示 effective mode；`-race` 綠。

## 5. Notes

- **順手修一個語義洞**（同屬 manifest 解析，落在同一 PR 最划算）：`settings.daily_budget_tokens` 的**負值語義未定義**——member 級 `budget_tokens: -1` 文件明定為「豁免」，settings 級文件只說「0/省略 = 不限」。實際就有 operator 在 settings 級寫了 `-1`（Sunday manifest，2026-06-11）。提案：settings 級 `<= 0` 一律視為「不限」並在 load 時 normalize + 文件補一句；或直接拒收負值。二擇一，別留 undefined。
- 不做 per-tool mode（如「bash ask、write bypass」）——那就是 RP-11 規則的職權，重複造輪。

---

## 6. 落地註記（2026-06-11）

照提案落地，但 §2.4 的疊加語義揭穿了一件事：**「deny 永遠最強」當時不是事實，是願望**——
`pkg/permission.Decide` 第 1 步就對 bypass 短路（`bypass means bypass`），deny 規則排第 2 步、
根本輪不到。所以本票不只「寫進文件」，動了裁決順序本身：

1. **`Decide` 重排（pkg/permission/decision.go）**：deny 查核移到 bypass 短路**之前**。
   deny 成為唯一穿透 bypass 的東西；ask 規則**刻意不穿透**（bypass 的存在理由就是無人值守
   不卡彈窗——ask 在 bypass 下視同 allow）。這是全域語義變更（solo evva 的 TUI bypass 也適用），
   與參考 harness（Claude Code：deny rules apply in all modes）對齊，CHANGELOG 記在 ### Changed。
   舊測試 `TestDecide_BypassAllowsEverything`（斷言 bypass 無視 deny）反轉重寫，
   新增 deny-pierces-bypass 與 ask-does-not-pierce-bypass 兩用例。
2. **manifest knob**：`memberYml.permission_mode`（leader 與 worker 皆可），值域驗證在
   `LoadManifest` 用 `parsePermissionMode`（"" = 繼承；非法值整份拒收，含 settings 級——
   settings 級先前也是未驗證的，`yolo` 會靜默落回 default）。`WriteManifest` round-trip。
   programmatic manifest（測試/SDK 直構）在 `constructMember` 二次把關（effort pin 同款）。
3. **解析與顯示分離**：constructMember 解析 member > settings 後交給 `agent.Config`；
   roster 存的是 `ag.PermissionModeName()` **讀回來的真實生效檔位**——它吃完整條 fallback 鏈
   （member > settings > app config > "default"），全空時顯示的也是真值。存於 rosterEntry
   （store-don't-query 精例），`MemberView.PermissionMode` → list_members（`· perm bypass`，
   永遠顯示——bypass 隊友可 fire-and-forget、default 隊友會卡審批佇列，這改變 leader 的
   委派策略）→ webapi `MemberInfo.permissionMode`。**FE 渲染留給 FE 線**（wire 欄位已就緒）。
4. **§5 budget 語義洞**：選 normalize 不選拒收——settings 級 `<0` 在 load 時歸 0（=不限）。
   breaker 與 list_members 本來就把 `<=0` 當不限（scheduler.go `budget <= 0` guard），
   normalize 只是把既成事實寫成保證，且 Sunday 現有 manifest（settings `-1`）不會在升級後
   突然拒收。member 級 `-1` = 豁免的帶號語義不動。
5. **範圍註**：knob 限 manifest（編組決策一份檔講清楚），profile.yml 刻意不收；
   web 動態加入的成員（AddWorker 只寫 agent 名）= 繼承 settings；`plan` 也是合法成員檔位
  （唯讀觀察員是真實編組）。順手修了 user-guide 一個誤導：profile.yml 範例裡註解的
   `budget_tokens`（loader 靜默忽略該欄位）改為指回 manifest 成員條目。
6. **驗收對照**：bypass space 標 default 的成員 → 其 agent 以 default 構建（審批走 RP-2
   既有管線）；default space 標 bypass → 免審批、deny 仍攔（Decide 重排保證）；省略 = 零回歸
  （`TestNewSpaceConstructsRoster` 等全綠）；非法值 register 拒收（manifest_test）+
   programmatic 拒收（space_test）；list_members 顯示（tools_test）+ wire 欄位
  （example_swarm_test 斷言三成員皆非空）。全套 `go test ./...` 綠、觸及包 `-race` 綠。
