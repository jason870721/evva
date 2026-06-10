# Veronica 健康檢查 2026-06-10 — 第四波 refine（RP-13~18）＋ Explore track（EX-1~6）

> 狀態：**草案 / Draft（待 Johnny 拍板）** ｜ 日期：2026-06-10
> 觸發：Phase 2（Sunday trading team）上線運營一週後的全面 code / 架構健康檢查。
> 上層設計：[`veronica-design-v1.md`](veronica-design-v1.md) ｜ 前三波：[`refine-plan/README.md`](refine-plan/README.md)

---

## 健康檢查結論（摘要）

**地基是穩的**：13k 行、150 個測試函式、`go vet` 乾淨、`-race` 全綠、覆蓋率 71.7%–90.6%。
訊息可靠性（persist-before-signal、DB-is-truth、level-triggered drain + rescan 後盾、
claim→settle 單點）、重啟恢復（vero.db + runtime.json + SDK session resume + durable alarms）、
上下文管理（兩級 auto-compact + RP-5 cache 紀律）、事件管線防凍結（WS write deadline）——
**第一、二、三波 refine 的成果都驗證有效，無紅色（correctness）問題。**

風險集中在三類「還沒被運營逼出來」的維度：

1. **運營可觀測性**——特別是 token 成本完全沒有儀表（24/7 集群在燒什麼看不到）。
2. **長期運行的退化路徑**——卡死的 run 無告警、ledger 只增不刪、事件流不落地。
3. **安全邊界的已知 TODO**——webhook 免 token（loopback 信任）尚未兌現 minted token。

由此產生兩條 track：

- **第四波 refine（RP-13~18）**：運營硬化。讓一個跑了幾週的 swarm「看得到、停得下、瘦得了」。
- **Explore track（EX-1~6）**：把 Phase 2 運營中自然長出來的模式（外部記憶、事件驅動、
  便宜哨兵）回收成 swarm 原生能力，並驗證「one runtime, many personas」的跨進程版本。

---

## 第四波 refine — 運營硬化（RP-13~18）

| # | 計畫 | 優先 | 主題 | 一句話 |
| --- | --- | --- | --- | --- |
| [RP-13](refine-plan/RP-13-member-usage-metering.md) | 成員用量儀表＋預算熔斷 | **P0** | 成本可觀測 | `ui.Controller` 已有 `Usage()` 出口、swarm 層完全沒接——`list_members`/web 顯示 per-member tokens，超日預算自動 Freeze＋通知。**✅ 已實作 2026-06-10。** |
| [RP-14](refine-plan/RP-14-stuck-run-watchdog.md) | Stuck-run watchdog | **P0** | 卡死可見 | run 無時限、busy 成員無人盯——busy 超閾值發 stall 事件＋通知，第二閾值可選自動 cancel（Suspend 的 ctx-cancel seam 現成）。**✅ 已實作 2026-06-10。** |
| [RP-15](refine-plan/RP-15-webapi-auth-hardening.md) | WebAPI 認證硬化 | P1 | 安全邊界 | 兌現 `service.go` 的 minted-token TODO：loopback 預設不變，非 loopback bind 顯式 opt-in＋強制 token，webhook 可選 secret。**✅ 已實作 2026-06-10。** |
| [RP-16](refine-plan/RP-16-ledger-retention.md) | Ledger retention | P1 | 只增不刪 | messages/tasks 無限增長——已讀且過期者歸檔/清理，手動 `vacuum` ＋ 每日自動，活資料（unread/claimed/active）絕不動。**✅ 已實作 2026-06-10。** |
| [RP-17](refine-plan/RP-17-durable-event-log.md) | Durable event log ＋ metrics | P2 | 事後可查 | 事件目前只進 WS——旁路落 append-only log ＋ 最小 metrics endpoint；是 EX-4（replay/eval）的地基。**✅ 已實作 2026-06-10。** |
| [RP-18](refine-plan/RP-18-ops-polish.md) | Ops 收口 | P2 | 雜項 | cron 方言文件化、daemon 自動重啟模板（launchd/systemd）、`/healthz` 擴充。**✅ 已實作 2026-06-10。** |

**建議落地序**：RP-13 → RP-14 →（RP-15 ∥ RP-16）→ RP-17 → RP-18。
RP-13/14 是運營 24/7 真金白銀系統最缺的儀表與保險絲，先做；RP-17 排在 EX-4 之前即可。

---

## Explore track — 探索（EX-1~6）

> Explore 與 refine 的差別：refine 有明確驗收、直接動工；explore 先做**最短 spike 驗證假設**，
> 成功訊號出現才升級成 RP。索引與格式見 [`explore/README.md`](explore/README.md)。

| # | 探索 | 規模 | 一句話 |
| --- | --- | --- | --- |
| [EX-1](explore/EX-1-member-native-memory.md) | 成員原生長期記憶 | 中 | 把 Sunday 自己長出來的 `/api/memory` 模式泛化：per-member memdir，喚醒注入索引，補 compaction 磨掉長期心智的洞。 |
| [EX-2](explore/EX-2-remote-persona.md) | Remote persona | 大 | `Roster.Controller` 是 `ui.Controller` 介面——做一個 RemoteController 讓遠端 runtime（nono 願景）入隊，bus/store 不動。 |
| [EX-3](explore/EX-3-leader-takeover.md) | Leader 單點退化保護 | 中 | leader 卡死＝全隊停擺（task 寫權唯一）——先做 operator 一鍵接管 UX，deputy 機制後議。 |
| [EX-4](explore/EX-4-replay-eval-harness.md) | Replay／eval harness | 大 | 把一天的事件流重放給新 prompt/模型版本做 regression eval；依賴 RP-17。 |
| [EX-5](explore/EX-5-wake-jitter.md) | 喚醒 jitter | 小 | Sunday 三成員整點齊醒（thundering herd）——schedule 加 jitter 散布，一行 config 的 spike。 |
| [EX-6](explore/EX-6-skill-sharing.md) | Skill 共享生態 | 中 | space 級共享 skill 庫＋ leader 教 worker 技能（write→reload 原語都在），治理是核心問題。 |

**建議**：EX-5 隨時可做（最小）；EX-1、EX-6 在 RP-13/14 之後；EX-4 依賴 RP-17；
EX-2、EX-3 是方向級探索，spike 通過後各自立 RP 再動工。

---

## 與既有文件的關係

- 第一波（RP-1~4）讓團隊**不卡死**；第二波（RP-5~10）讓團隊**跑得久、管得動、編得了**；
  第三波（FE v2）給 operator **看得清**的工作站。
- **第四波讓這一切「運營得起」**：成本、卡死、增長、審計——全是系統跑了幾週才會痛的維度。
- Explore track 則回應設計文件的長線願景（§1.1 multi-agent oracle、one runtime many personas）：
  swarm 只消費 `pkg/*` 的紀律守住了，所以 EX-2（remote persona）才有乾淨的 seam 可踩。
