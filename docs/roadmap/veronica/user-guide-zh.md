# evva swarm 与 evva service — 用户指南（从 0 到精通）

> 语言：[English](user-guide-en.md) ｜ **中文**
> 读者：想让一群 evva agent 协作完成任务的人。
> 内容：swarm 的工作原理，以及从零搭建一个 swarm 的完整教程。

---

## 1. 这是什么？

evva 是一个终端编程 agent。**Veronica** 是它的 *swarm（蜂群）* 层：把单 agent 运行时
扩展成一个**多 agent 工作站**——一群长期存活的 agent 协作完成同一个目标。

两个命令：

- **`evva service`** —— 一个后台 Web 服务（默认 `127.0.0.1:8888`）。它是**宿主**：
  负责运行 agent、持久化状态、提供 Web 界面。一个 service 可以**同时托管多个互相
  独立的 swarm**。
- **`evva swarm`** —— 控制面。`evva swarm .` 把当前目录里声明的 swarm 注册进正在
  运行的 service。

> 原本的 `evva` TUI 不受影响 —— swarm 是纯增量功能。

### 心智模型

```
 evva service （单进程，:8888，Web UI + 会话 token）
 │
 ├── SwarmSpace "A"   ← 在 /path/to/A 执行 `evva swarm .`
 │     ├── leader        （写任务账本、派发、验收）
 │     ├── worker-1      （干活，回报）
 │     └── worker-2
 │     ├── .vero/vero.db   （任务账本 + 消息，SQLite）
 │     └── 消息总线 + 花名册  （每个 space 独立、隔离）
 │
 └── SwarmSpace "B"   ← 在 /path/to/B 执行 `evva swarm .`  （与 A 完全隔离）
```

- 一个 **space（子集团）** 就是一个 swarm：拥有自己的 agent、自己的数据库、自己的
  消息总线。两个 space **互不共享任何东西** —— 甚至成员名字相同也不会冲突。
- 每个成员都是一个完整的 evva agent（各自的模型、提示词、工具、性格）。
- 成员通过两种方式协作：
  1. **任务账本** —— 一个共享、持久、带 5 状态机的待办清单
     （`pending → running → verifying → completed`，外加 `suspended`）。
     **只有 leader 能写任务状态**，worker 只读。
  2. **消息** —— agent 之间互相发信（`send_message`）；空闲的收信人会被唤醒处理，
     繁忙的会把信折进当前的工作里。
- 它能扛住重启：杀掉 service 再启动，每个 space 都会被重建 ——
  未读信件重新入列、对话续上、账本完好。

---

## 2. 角色：leader 与 worker

| | Leader（`agents/main/…`） | Worker（`agents/sub/…`） |
| --- | --- | --- |
| 负责 | 规划、派发、验收 | 干活、回报 |
| 任务工具 | `task_create`、`task_assign`、`task_update_status`、`task_verify`、`task_list` | `my_tasks`、`task_get`（只读） |
| 沟通 | `send_message`、`list_members` | `send_message`、`list_members` |
| 能写账本？ | **能**（唯一写者） | 不能 |

leader 把目标拆成任务，**推送**给合适的 worker，验收结果后再向你汇报。worker 不能
改任务状态 —— 它们用 `send_message` 回报进度，由 leader 推进任务。

---

## 3. 前置条件

- `PATH` 里有可用的 `evva` 可执行文件（`go build ./cmd/evva` 或安装发布版）。
- 按 evva 常规方式配置好 LLM provider 凭证（`~/.evva/.env` / `evva-config.yml`）
  —— swarm 复用与 TUI 相同的 provider 配置。每个成员可在自己的 `profile.yml` 里
  覆盖模型。

快速检查：

```sh
evva -version
```

---

## 4. 快速上手（60 秒）

```sh
# 1. 启动宿主（自动转入后台；打印会话 token）。
evva service start
#   → evva service started (pid 12345) on http://127.0.0.1:8888
#       token: ~/.evva/service/token

# 2. 查看状态。
evva service status

# 3. 打开 Web 界面并粘贴 token。
#    macOS:  open http://127.0.0.1:8888
#    Linux:  xdg-open http://127.0.0.1:8888

# 4. 用完后停止。
evva service stop
```

现在你有了一个运行中的空工作站。下一步给它装一个 swarm。

---

## 5. 从零搭建一个 swarm

我们来搭一个 3 人**工程团队**：一个 leader、一个后端 worker、一个前端 worker。

### 5.1 目录结构

新建一个项目目录。结构是固定的：

```
my-team/
├── evva-swarm.yml                 # 清单：团队由谁组成
└── agents/
    ├── main/                      # leader 放这里
    │   └── leader/
    │       ├── system_prompt.md   # 必填：agent 的人设/指令
    │       ├── profile.yml        # 选填：模型、effort、schedule……
    │       └── tools/
    │           ├── active.yml     # 立即暴露的工具
    │           └── deferr.yml     # 仅声明、按需获取的工具
    └── sub/                       # worker 放这里
        ├── backend-dev/
        │   ├── system_prompt.md
        │   ├── profile.yml
        │   └── tools/active.yml
        └── frontend-dev/
            ├── system_prompt.md
            ├── profile.yml
            └── tools/active.yml
```

> 规则：**leader** 目录放在 `agents/main/` 下，每个 **worker** 放在 `agents/sub/`
> 下。名字必须与清单一致。

### 5.2 清单 —— `evva-swarm.yml`

```yaml
name: my-eng-team           # 这个 swarm 的显示名
workdir: .                  # .vero/（数据库）所在；"." = 当前目录

leader:
  agent: leader             # → agents/main/leader/

workers:
  - agent: backend-dev      # → agents/sub/backend-dev/
  - agent: frontend-dev     # → agents/sub/frontend-dev/

settings:
  permission_mode: default  # default | accept_edits | plan | bypass
  max_iterations: 50        # 每个成员单次运行的循环上限
  # —— 运营保险丝（按需启用，详见 §8）——
  # daily_budget_tokens: 2000000  # 每成员每日 token 上限（in+out）；0/省略 = 不限
  # budget_stay_frozen: false     # true = 超额冻结跨日不自动解冻（需手动）
  # stall_threshold: 10m          # 成员忙超过即告警；"0" 关闭（省略 = 默认 10m）
  # stall_hard_timeout: 30m       # 忙超过即自动取消该次运行；0/省略 = 关闭
  # webhook_secret: "hunter2"     # 要求事件 POST 携带 X-Evva-Webhook-Secret（见 §10）
  # retention_days: 30            # 已消费历史 N 天后归档+删除；"0" = 永不删除
  # event_log: true               # 事件镜像到 .vero/events/（按日 jsonl）；false = 关闭
```

- 同一 space 内**成员名唯一**（不支持副本 —— 每个成员取不同名字）。
- `permission_mode`：
  - `default` —— 危险工具（写文件、shell）会请求审批；你在 Web 界面里批准。
  - `bypass` —— 不弹审批；agent 完全自主运行。很强大，但只在你信任工作目录和
    任务时使用。

### 5.3 定义 leader

> **你只需要写「人设」。** 每个成员的 `system_prompt.md` 描述的是*这个 agent 是谁、
> 该怎么协作* —— 它的领域、风格、什么时候沟通。你**不需要**解释任务账本、工具、
> 5 状态流程：那套**swarm 协作协议会根据角色（leader / worker）自动注入**，就跟
> swarm 工具一样。专注在「活儿」本身，别去教底层机制。

`agents/main/leader/system_prompt.md`：

```markdown
# 团队负责人

你领导一个工程团队。把任务拆小、写具体，按专长分派给合适的成员，并在向用户汇报
前验收结果。你负责规划与验收 —— 不亲自干 worker 的活。
```

`agents/main/leader/profile.yml`：

```yaml
model: claude-sonnet-4-6        # 覆盖默认模型（选填）
effort: high                    # low | medium | high | ultra（选填）
when_to_use: "团队负责人 —— 规划、派发、验收。"
inject_memory: true             # 把 EVVA.md / 记忆载入提示词
advertise_skills: true
```

`agents/main/leader/tools/active.yml` —— 只放這個成員需要的**一般 evva 工具**
（leader 只需讀檔來驗收 worker 的產出）：

```yaml
- read
- grep
- glob
- tree
```

> **重要 —— 不要列 swarm 工具。** `task_create`、`task_assign`、
> `task_update_status`、`task_verify`、`task_list`、`send_message`、
> `list_members`、`my_tasks`、`task_get` 會**根據角色（leader / worker）自動注入**。
> 在 `active.yml` 裡再列一次會造成**重複註冊**，LLM 呼叫會因工具名重複而失敗。
> `active.yml`（與 `deferr.yml`）只放一般 evva 工具（`read`、`write`、`bash`…）。
> 一個不需要額外 evva 工具的成員，`tools/` 整個省略即可。

### 5.4 定义一个 worker

`agents/sub/backend-dev/system_prompt.md`：

```markdown
# 后端工程师

你负责后端工作：API、数据模型、迁移、测试。写干净、带测试的代码；任务清楚时优先
动手而不是反复问。
```

`agents/sub/backend-dev/profile.yml`：

```yaml
model: claude-sonnet-4-6
effort: medium
when_to_use: "后端：API、数据库 schema、迁移、服务端测试。"
# 选填：按定时器唤醒做自检（cron 与 every 二选一）：
# schedule:
#   cron: "*/5 * * * *"     # 每 5 分钟（本地时区；方言见 §11）
#   # every: "30s"          # 或固定间隔
# 选填：个别 token 预算覆写（见 §8）：>0 自有上限、-1 完全豁免、省略 = 继承 settings
# budget_tokens: 250000
```

`agents/sub/backend-dev/tools/active.yml` —— 程序员真正需要的干活工具
（協作工具 `my_tasks` / `task_get` / `send_message` / `list_members` 由 worker
角色**自動注入**，不要在這裡列）：

```yaml
- read
- write
- edit
- bash
- grep
- glob
- tree
```

对 `frontend-dev` 照做（各自的提示词/专长；工具集通常相同）。

### 5.5 注册 swarm

在 service 运行的前提下，进入 `my-team/`：

```sh
cd my-team
evva swarm .          # 校验 evva-swarm.yml 并注册该 space
#   → registered space <id>
#       open: http://127.0.0.1:8888/?space=<id>
```

列出已注册的：

```sh
evva swarm ls
#   ID        NAME          MEMBERS  WORKDIR
#   a1b2c3…   my-eng-team   3        /home/you/my-team
```

打开那个 URL，粘贴 token，就能看到你的团队上线了。

---

## 6. 在 Web 工作站里驱动它

Web 界面（`:8888`）针对每个 space 提供：

- **Space 选择器** —— 已注册 swarm 的列表；点一个进入。
- **Member Console（成员控制台）** —— 某个成员的实时聚焦视图：它的流式 turn 与
  工具调用。默认聚焦 leader（输入目标即可启动工作），但你也可以**点击花名册里的
  任意成员，聚焦它的控制台并直接给它发消息** —— 你能像跟 leader 对话一样，直接跟
  基层 worker 沟通。你的消息走 swarm 的消息总线，所以空闲成员会被唤醒处理、繁忙
  成员会把它折进当前工作 —— 而**不打扰团队其余的工作流**（扁平化管理）。
- **Team Board（看板）** —— 5 列看板（`pending / running / suspended /
  verifying / completed`），随任务账本的流转实时反映。
- **Agent Roster（花名册）** —— 列出每个成员的成员状态（active/frozen）和运行状态
  （idle/busy/suspended），并提供操作：**冻结 / 解冻 / 暂停 / 恢复 / 新增成员**。
- **审批弹窗** —— 在 `default` 模式下，成员触发需审批的工具（写文件、shell 命令）
  时会弹出提示；**Allow（允许）** 或 **Deny（拒绝）** 即可放行。提问
  （`ask_user_question`）以同样方式出现。
- **单 agent 视图** —— 点一个成员，查看它的对话记录和收件箱。

> **想直接玩、不想自己刻？** 這裡有一套現成的 example swarm：
> [`example-swarm/`](example-swarm/) —— 複製出去、`evva swarm .`，照它的 README 走即可。

典型的第一次运行：进入 space → 在 Member Console（聚焦 leader）里输入「搭一个 TODO REST API，
带 Postgres schema 和一个小型 Web UI，把活分一下」→ 看着 leader
`task_create`/`task_assign`，worker 接走各自的任务、回报，看板一路推进到
**completed**。

---

## 7. 协作到底是怎么运作的（底层）

- **自动注入的协议 + 工具。** 每个成员都会被**自动**赋予它角色对应的协作**工具**
  *与*协作**协议**（注入到它的系统提示词里）—— leader 拿到任务账本工具 + leader
  协议，worker 拿到只读任务工具 + worker 协议。你**永远不用**在 `system_prompt.md`
  或 `active.yml` 里声明这些；你只写人设。（这就是下面这些机制「开箱即用」、不用你
  教的原因。）
- **任务账本（5 状态）。** leader `task_create` → `task_assign`（转 `running`，
  通知 worker）→ worker 干活并回报 → leader `task_update_status` → `verifying`
  → `task_verify` 批准（转 `completed`）或驳回（退回 `running`）。状态机在 SQLite
  里强制执行，非法跃迁会被拒绝。
- **消息。** `send_message {to, body}`（或 `to: "all"` 广播）写入一条持久记录并
  叮一下收信人的信箱。
  - 收信人**空闲**时，会被唤醒、读信、据此行动（*drain A*）。
  - 收信人正在**忙**（运行中）时，信件会在下一步被折进它当前的推理里，所以紧急信
    （「马上停」）能立刻送达（*drain B*）。
- **定时唤醒。** 在 `profile.yml` 里配了 `schedule` 的成员会按该节奏被运行
  （心跳 / 自检）。没有唤醒源的成员保持空闲，**不烧 token**。
- **空闲即省钱。** 没有理由（消息、任务、定时器）就什么都不跑。一个空闲的 swarm
  不产生任何花费。

---

## 8. 日常运维

```sh
# 查看已注册的 space
evva swarm ls

# 向运行中的 space 热加入一个新 worker（无需重启）。
# 对应的 agent 目录必须已存在于 agents/sub/<name>/。
evva swarm add <space-id> <成员名>

# 停掉一个 space（其它的继续运行）。
evva swarm stop <space-id>

# 服务生命周期
evva service status
evva service stop
```

在 **Web 花名册** 里，你可以对每个成员：

- **冻结 / 解冻** —— 让成员停止服务但不删除（被冻结者不再被派任务；解冻即可回归）。
- **暂停 / 恢复** —— 立刻中止成员正在飞的运行，之后再恢复（它的未读工作会被重新处理）。
- **全部停止（Halt all）** —— 紧急制动：取消该 space 里所有在飞的运行。

### 成本与卡死保险丝（token 预算 / stall 看门狗）

7×24 跑的团队需要两根保险丝。都在 `evva-swarm.yml` 的 `settings:` 里、按 space 生效；
不设就完全不介入。

**每日 token 预算（budget breaker）**

```yaml
settings:
  daily_budget_tokens: 2000000   # 每成员每天（本地日界线）in+out token 上限；0 = 不限
  budget_stay_frozen: false      # true = 跨日不自动解冻，须手动
workers:
  - agent: watchdog
    budget_tokens: -1            # 个别覆写：>0 自有上限；-1 完全豁免；省略 = 继承
```

- 成员在一次运行结束后越线 → **自动冻结**，leader 与你（Web 收件箱 / Timeline）各收到一封
  `⚠️ budget breaker` 通知。
- 它的邮箱照常排队、什么都不丢；**本地日界线一过自动解冻**（除非 `budget_stay_frozen`）。
- 在花名册手动解冻视为操作员覆写：若当日额度仍超标，它跑完下一轮会**再次熔断（只再通知
  一次）** —— 真要放行请调高预算。
- 用量随时看得到：leader 跑 `list_members` 每行带 `tok in 1.2M out 345k, today 89k/500k`；
  Web 花名册 API 带 `tokensIn / tokensOut / tokensToday / tokensBudget`。计数与熔断状态
  会持久化 —— **重启服务不会清零当天额度**。

**Stall 看门狗（卡死告警 / 自动止损）**

```yaml
settings:
  stall_threshold: 10m      # 忙超过此时长且不是在等人 → 告警；"0" 关闭（省略 = 默认 10m）
  stall_hard_timeout: 0     # 忙超过此时长 → 自动取消该次运行；0/省略 = 关闭（建议先观察再开）
```

- 成员**忙**超过 `stall_threshold`（卡死的 LLM 调用、挂住的工具、或确实很长的任务），
  你和 leader 各收到一封 `⏳ stall` 通知 —— **每次运行最多一封**，不刷屏。
- 正在**等人**的不算卡死：waiting-approval / waiting-input / paused 阶段一律豁免。
- 开了 `stall_hard_timeout`，超时的运行会被取消：它认领中的邮件自动退回未读、下次唤醒
  重试 —— **不丢工作**；同一件事再挂住会再告警/再取消。
- leader 自己卡死时，至少你会收到通知。

**时间与时区（v1.4.5-beta.2 起）**

- 注入给成员的所有时间（`currenttime`、事件戳、信件 `[sent …]`、alarm 回执）一律带明确
  UTC 偏移，如 `2026-06-10 20:25:00 +08:00`。
- `alarm_set` 等处的裸时间字符串按**系统本地时区**解析；要表达 UTC 用 RFC3339
  （`2026-06-10T12:25:00Z`），确认回执会同时给出 UTC 对照，下错时区一眼可见。
- cron（manifest 的 `schedule` 与 leader 的 `schedule_set`）按系统本地墙钟比对。

### Ledger 瘦身（`retention_days` / `evva swarm vacuum`）

24/7 跑的 swarm 会无限累积 messages 和已完成任务，Web/API 的读取随表变大而变慢。
Retention 在**不丢历史**的前提下控制工作集：符合条件的行先追加到
`<workdir>/.vero/archive/YYYY-MM.jsonl.gz`（按行自己的月份分桶），再删除并压缩
数据库。

只有这些行会被清（其余永不动）：

- 已**读**、且读取发生在 ≥ `retention_days` 天前的 messages；
- 进入终态 **completed** 且 ≥ `retention_days` 天的任务——但若仍被存活的行引用
 （某条留存消息的 `ref_task`、某个子任务的 parent 链），则继续保留。

未读信、claimed（折叠中）的信、以及 pending/running/suspended/verifying 状态的
任务无论多老都碰不得。

只要 `settings.retention_days` > 0（默认 **30**；写 `"0"` 保持旧的永不删除行为），
service 每个本地日自动跑一次（service 启动时也补跑一次，弥补睡过午夜的机器）。
手动跑、先预览：

```bash
evva swarm vacuum my-eng-team --dry-run     # 只报数字，什么都不动
evva swarm vacuum my-eng-team               # 按配置窗口归档+删除
evva swarm vacuum my-eng-team --days 7      # 本次临时覆盖窗口
```

之后要查归档：它就是 gzip 的 JSON-lines ——
`zcat .vero/archive/2026-06.jsonl.gz | jq .`（每行带 `kind` message/task 和完整
原始行）。量级参考：积压 10 万条 messages 时 API 单次 ~300 ms，vacuum 后回到
亚毫秒，清理本身约 1.2 秒。

### 飞行记录器与 metrics（event log / `/metrics`）

Web 界面看到的每一个事件（run/turn 生命周期、工具调用与结果、审批、错误——除了
token 级的流式 chunk）都会同时追加到 `<workdir>/.vero/events/YYYY-MM-DD.jsonl`，
每行一条带时间戳的 JSON。「昨晚 03:00 发生了什么」从此一句 grep 就能回答，重启
也不丢：

```bash
grep '03:0' .vero/events/2026-06-09.jsonl | jq '.event.event.Kind' | sort | uniq -c
```

文件按日切；旧文件按同一个 `retention_days` 窗口清理（`"0"` = 永久保留）。
`event_log: false` 关闭记录器。记录器永远不会拖慢 swarm：缓冲满了就丢行并计数
（`eventsDropped`），绝不阻塞事件泵。

实时计数器（按成员，自 space 启动起累计）：

```bash
curl -s -H "Authorization: Bearer $(cat ~/.evva/service/token)" \
  http://127.0.0.1:8888/api/swarm/<ref>/metrics | jq .
```

返回 `uptimeSecs`、`eventsLogged` / `eventsDropped`（记录器）、`hintsDropped`
（信箱背压——持续上涨说明某成员长期积压）、以及每成员的 `wakesMessage` /
`wakesTimer` / `runs` / `aborts` 和运行时长直方图（`runSeconds`：lt10s / lt1m /
lt10m / gte10m）。纯 JSON——要历史曲线就自己接 exporter。

### 开机自启（扛住 crash 与重启）

`evva service start` 会守护化，但 crash 或重开机后没有人把它拉起来——把这件事
交给平台的 supervisor：

```bash
evva service install-unit     # 写入 launchd plist（macOS）或 systemd user unit（Linux）
```

然后执行它打印的启用指令（它自己绝不启用任何东西）。unit 跑的是
`evva service start --foreground`——supervisor 直接拥有进程、失败即重启，swarm
按下方「重启与续跑」路径原地恢复（session、未读信、membership、alarm）。在
supervisor 之下请用 `launchctl` / `systemctl --user` 启停，不要用
`evva service stop`（supervisor 会立刻把它拉回来）。手动配置模板见
[docs/user-guide/zh-tw/service-autostart.md](../../user-guide/zh-tw/service-autostart.md)。

给监控用：`GET /healthz` 免 token、回 JSON——

```json
{"status":"ok","version":"v1.5.0","uptimeSecs":86400,
 "spacesRunning":1,"spacesStopped":0,"membersActive":3,"membersFrozen":0}
```

`spacesRunning` 或 `membersActive` 为 0 即「活着但空转」；只有计数、没有名字——
每个 space 的细节都在 token 后面。

### 重启与续跑

swarm 是崩溃安全的。在 `evva service stop`（或崩溃）后重新 `evva service start`：

- 每个先前注册过的 space 都会**从磁盘重建**，
- 每个成员的**对话从中断处续上**，
- **未读消息重新入列**（不丢信），
- **任务账本完好**（停在 `running` 的任务仍是 `running`），
- **被冻结的成员回来时仍是冻结的**。

你什么都不用做 —— 它自然续跑。

---

## 9. 同时跑多个 swarm

service 从第一天就是**多 space 宿主**。想注册多少就注册多少，各自来自自己的目录：

```sh
cd ~/projects/web-team   && evva swarm .
cd ~/projects/data-team  && evva swarm .
evva swarm ls            # 两个都列出，完全隔离
```

它们共用同一个 `:8888` 进程和 Web 界面（在 space 选择器里切换），但**别无共享**
—— 各自独立的数据库、总线、花名册和命名。停掉一个绝不影响另一个。

---

## 10. 安全

- service 默认**只绑定 `127.0.0.1`** —— 外部机器无法访问。（agent 会跑 shell、改
  文件，所以这个工作站等同于远程代码执行；务必留在 loopback 上。）
- 每个 Web/API 请求都需要**会话 token**。自 v1.5 起它是每次 `evva service start`
  随机铸造的密钥（固定的开发 token `root` 已移除），存于 `~/.evva/service/token`
 （权限 0600）。正常情况下你根本见不到它：同一台机器上的浏览器会自动登录
 （一个仅限 loopback 的 bootstrap 端点把 token 交给页面），CLI 直接读文件。
  轮换 = 重启。
- 在 `permission_mode: default` 下，写/ shell 类工具会走审批弹窗 —— 你始终在环路里。
  仅在你信任任务和工作目录时才用 `bypass`。

### 把工作站暴露到本机之外（`--allow-remote`）

默认情况下，非 loopback 绑定**直接拒绝启动**。要从其他设备（局域网、或经反向
代理）访问工作站，必须显式开启：

```bash
evva service start --addr 0.0.0.0:8888 --allow-remote
```

先想清楚威胁模型：**谁拿到会话 token，谁就是 operator** —— 可以批准工具调用、给
成员发消息，等同于在这台机器上执行 shell。远程模式下，loopback 的便利全部关闭：

- FE 自动登录的 bootstrap 端点消失（经代理后所有请求看起来都来自本机）。每台
  设备、每次 service 重启后，从 `~/.evva/service/token` 粘贴一次 token。
- 其他主机发来的 webhook POST 一律拒绝，除非目标 space 配置了
  `settings.webhook_secret`（见下）。

TLS 终结和 IP 过滤交给你的反向代理 —— service 本身保持纯 HTTP、单 operator
（没有账号体系，没有 RBAC）。

### 外部事件 webhook 与 `webhook_secret`

外部应用可以 POST 一个事件来唤醒某个成员（默认 leader），不需要会话 token：

```bash
curl -X POST http://127.0.0.1:8888/api/swarm/<space-id>/event \
  -H 'Content-Type: application/json' \
  -H 'X-Evva-Webhook-Secret: hunter2' \
  -d '{"title":"BTC spike","body":"vol>3sigma","source":"trader-engine",
       "idempotency_key":"evt-123"}'
```

鉴权规则（RP-15）：

| space 配置 | 本机调用 | 远程调用 |
| --- | --- | --- |
| 未设 `webhook_secret` | 放行（沿用 loopback 信任） | **401** |
| 设了 `webhook_secret` | 必须带对的 header | 必须带对的 header |

返回码：新事件 → 202，重复 `idempotency_key` → 200，secret 缺失/错误 → 401，
未知 space → 404，已停止 → 409。请求体上限 64 KB。

---

## 11. 速查

### CLI

| 命令 | 作用 |
| --- | --- |
| `evva service start` | 以后台守护进程启动 `:8888` 宿主（铸造并保存 token）。旗标：`--addr <host:port>`、`--allow-remote`（任何非 loopback 地址都必须带它）。 |
| `evva service status` | 报告运行/停止、pid、地址、token 位置。 |
| `evva service stop` | 停止守护进程（space 会被保留，下次启动续跑）。 |
| `evva swarm .` | 把当前目录的 `evva-swarm.yml` 注册为一个新 space。 |
| `evva swarm ls` | 列出已注册的 space。 |
| `evva swarm stop <id>` | 停止（并移除）一个 space。 |
| `evva swarm add <id> <成员>` | 向 space 热加载一个 worker（`agents/sub/<成员>/`）。 |
| `evva swarm vacuum <ref> [--days N] [--dry-run]` | 归档后删除已消费历史（RP-16）；dry-run 先预览。 |

### 环境变量

| 变量 | 作用 |
| --- | --- |
| `EVVA_SERVICE_ADDR` | 覆盖监听/目标地址（默认 `127.0.0.1:8888`）。 |
| `EVVA_SERVICE_HOME` | 覆盖运行时目录（默认 `<AppHome>/service/`：pidfile、token、addr、log）。 |
| `EVVA_SERVICE_ALLOW_REMOTE` | `1` = 允许非 loopback 绑定（`--allow-remote` 传给守护子进程的形式）。 |

### 运行时文件（`~/.evva/service/`）

`evva-service.pid` · `token` · `addr` · `evva-service.log`

### `profile.yml` 字段

| 字段 | 含义 |
| --- | --- |
| `model` | 该成员的 LLM 模型 id（覆盖默认）。 |
| `effort` | `low` / `medium` / `high` / `ultra`。 |
| `when_to_use` | 在 `list_members` / 花名册里显示的一句话专长。 |
| `inject_memory` | 把 `EVVA.md` + 记忆索引载入提示词。 |
| `advertise_skills` | 在提示词里列出已安装的 skill。 |
| `schedule.cron` | 5 字段 cron 定时唤醒（如 `"*/5 * * * *"`）。 |
| `schedule.every` | 用固定间隔代替 cron（如 `"30s"`、`"5m"`）。 |

### Schedule cron 方言

swarm 的 cron 是自写的、刻意精简。五个字段——`分 时 日 月 星期`——按**系统本地
墙钟**匹配，分钟精度。

每个字段支持：`*`、单值（`5`）、范围（`9-17`）、步进（`*/5`、`9-17/2`）、逗号
列表（`0,30`）及任意组合（`0,15,30-45/5`）。星期为 `0-7`，0 和 7 都是周日。
当「日」和「星期」**同时**受限时，任一匹配即算匹配（标准 cron 的 OR 语义）。

**不支持**——parser 会点名拒绝：秒字段（6 字段写法）、`@daily` / `@every` 别名、
`L` / `W` / `#` / `?` 特殊符、`TZ=` 前缀（时区永远是系统本地）。

```
*/5 * * * *      每 5 分钟
0 17 * * 1-5     工作日 17:00
0 9,18 * * *     每天 09:00 与 18:00
0 3 1 * *        每月 1 号 03:00
```

### swarm 工具名

這些會**根據角色自動注入** —— **永遠不要在 `active.yml` 裡列它們**。
Leader：`task_create`、`task_assign`、`task_update_status`、`task_verify`、
`task_list`。Worker：`my_tasks`、`task_get`。两者都有：`send_message`、
`list_members`。`active.yml` 裡只列成員需要的常规 evva 工具 —— `read`、`write`、
`edit`、`bash`、`grep`、`glob`、`tree`、`web_fetch`……

---

## 12. 排错

| 现象 | 解决 |
| --- | --- |
| `evva swarm .` 报连不上 service | 先启动：`evva service start`。 |
| `no evva-swarm.yml in <dir>` | 在有清单的那个目录里执行 `evva swarm .`。 |
| Web 提示「unauthorized」 | 粘贴 `~/.evva/service/token` 里的 token（或从 `evva service start` 重新复制）。 |
| 某个成员什么都不做 | 在花名册里确认它是 `active`（未被冻结），并且 `tools/active.yml` 里给了它所需工具。 |
| worker 改不了任务状态 | 这是设计如此 —— 只有 leader 写账本；worker 用 `send_message` 回报。 |
| `evva service start` 拒绝（"already running"） | 已有一个在跑；`evva service status` 确认，`stop` 后再换。 |
| 端口被占用 | `EVVA_SERVICE_ADDR=127.0.0.1:9999 evva service start`。 |

---

## 13. 从 0 到精通 回顾

1. **启动宿主：** `evva service start`（记下 token）。
2. **搭骨架：** 一个 `evva-swarm.yml` + `agents/main/<leader>/` +
   `agents/sub/<workers>/`，每个含 `system_prompt.md`（外加选填的
   `profile.yml`、`tools/active.yml`）。
3. **注册：** `evva swarm .`。
4. **驱动：** 打开 `:8888`，粘贴 token，在 Member Console 里跟 leader（或任一成员）对话。
5. **观察：** Team Board 走 `pending → running → verifying → completed`；
   花名册显示谁在忙。
6. **运维：** 新增/冻结/暂停成员；多个 swarm 并排运行。
7. **放心：** 随时停止与重启 —— swarm 会精确续跑。

这就是全部旅程。欢迎来到 swarm。
