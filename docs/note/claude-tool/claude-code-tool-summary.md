# Claude Code Tools Reference

## Active Tools (Ready to Call)

| Tool | Purpose |
|------|---------|
| **Read** | Read a file by absolute path. Handles text, PDFs (with pages), Jupyter notebooks, images. First-line defense for "what's in this file." |
| **Edit** | Exact string replacement in a file. Requires prior Read. Preferred over Write for modifying existing files. |
| **Write** | Create a new file or fully overwrite one. Use only when Edit doesn't fit. |
| **Bash** | Run shell commands. Catch-all for git, build/test runs, find/grep/rg, any CLI. Supports background execution. |
| **Agent** | Spawn a subagent (Explore, Plan, general-purpose, code-review, etc.) for parallel/independent work or to protect main context from big result dumps. |
| **ToolSearch** | Load schemas for deferred tools by name (`select:Foo,Bar`) or keyword search. Required before calling anything deferred. |
| **Skill** | Invoke a user-installed skill by name (e.g. `commit`, `code-review`, `make-prd`, `pgagent`). Same as the user typing `/skill-name`. |
| **ScheduleWakeup** | Self-pace iterations in `/loop` dynamic mode. Not relevant outside loops. |

---

## Deferred Tools (Load with ToolSearch First)

### Task & Process Management

- `TaskCreate`, `TaskGet`, `TaskList`, `TaskUpdate`, `TaskOutput`, `TaskStop` — track multi-step work as discrete tasks.
- `Monitor` — stream a running process's stdout as notifications.
- `EnterPlanMode`, `ExitPlanMode`, `EnterWorktree`, `ExitWorktree` — mode/isolation control.
- `NotebookEdit` — edit Jupyter notebook cells specifically.

### User Interaction

- `AskUserQuestion` — prompt the user for a structured answer mid-turn.
- `PushNotification` — surface a notification.

### Scheduling

- `CronCreate`, `CronList`, `CronDelete` — schedule recurring autonomous agent runs.
- `RemoteTrigger` — kick off a remote agent.

### Web

- `WebFetch` — fetch a URL and extract content.
- `WebSearch` — keyword web search.

---

<br>

---

# Ignore following feature (implement in v3.0)

## MCP Integrations

### Atlassian (Jira / Confluence / Compass)

**Jira:**
`searchJiraIssuesUsingJql`, `getJiraIssue`, `createJiraIssue`, `editJiraIssue`, `transitionJiraIssue`, `addCommentToJiraIssue`, `addWorklogToJiraIssue`, `createIssueLink`, `getJiraIssueRemoteIssueLinks`, `getTransitionsForJiraIssue`, `getVisibleJiraProjects`, `getJiraIssueTypeMetaWithFields`, `getJiraProjectIssueTypesMetadata`, `getIssueLinkTypes`, `lookupJiraAccountId`

**Confluence:**
`getConfluencePage`, `getConfluencePageDescendants`, `createConfluencePage`, `updateConfluencePage`, `searchConfluenceUsingCql`, `getPagesInConfluenceSpace`, `getConfluenceSpaces`, `createConfluenceFooterComment`, `createConfluenceInlineComment`, `getConfluencePageFooterComments`, `getConfluencePageInlineComments`, `getConfluenceCommentChildren`

**Compass (Service Catalog):**
`getCompassComponent(s)`, `createCompassComponent`, `createCompassComponentRelationship`, `getCompassCustomFieldDefinitions`, `createCompassCustomFieldDefinition`

**Cross-product:**
`search`, `fetch`, `getAccessibleAtlassianResources`, `atlassianUserInfo`

---

### Figma

**Read Designs:**
`get_design_context`, `get_screenshot`, `get_metadata`, `get_variable_defs`, `get_figjam`, `get_libraries`

**Write / Create:**
`create_new_file`, `generate_diagram` (FigJam), `upload_assets`, `use_figma`

**Code Connect:**
`get_code_connect_map`, `add_code_connect_map`, `get_code_connect_suggestions`, `get_context_for_code_connect`, `send_code_connect_mappings`, `create_design_system_rules`, `search_design_system`

**Account:**
`whoami`

---

### Miscellaneous MCP

- `ListMcpResourcesTool`, `ReadMcpResourceTool` — generic MCP resource reads.
- `mcp__ide__getDiagnostics` — pull current IDE diagnostics (lint/type errors).

<br>

---

<br>

### How do you know when to call ToolSearch?                                                                                                                                                                           
                                           
#### Three signals, in order:                                                                                                                                                                                           
                                                                         
  1. The system-reminder at session start. It explicitly listed deferred tools by name and told me their schemas aren't loaded — "calling them directly will fail with InputValidationError. Use ToolSearch..." So I 
  know which tools exist in name-only form and that ToolSearch is the bridge.
                                                                                                                                                                                                                     
  2. Intent to call one of those names. I never call ToolSearch speculatively. The trigger is: "I want to use TaskCreate / WebFetch / etc." → check my active tool list → not there → check the deferred list →      
  present → call ToolSearch with select:<name>. If the tool is already in my active set (Read, Bash, Edit, …), no ToolSearch needed.
                                                                                                                                                                                                                     
  3. The user explicitly asks for schemas. Like in this conversation — you asked me to dump categories of tools, so I fetched them in batches to get the full descriptions and parameter shapes into my context.     
  Without your request I wouldn't have loaded them, since I wasn't going to call them.
                                                                                                                                                                                                                     
#### A few practical heuristics layered on top:                                                                                                                                                                         
                                         
  - Batch by select: not keyword. select:Foo,Bar,Baz is deterministic and cheap. Keyword search ("notebook jupyter") is for when I don't know the exact name.                                                        
  - Parallel calls when independent. Loading Task tools and Plan/Worktree/Notebook tools at the same time is two parallel ToolSearch invocations in one message — same response time as one.
  - Once loaded, stay loaded. Schemas persist for the rest of the conversation, so I don't re-fetch.                                                                                                                 
  - Don't preload "just in case." Every loaded schema spends context tokens. If I'm not about to call it (or the user didn't ask to see it), I leave it deferred.                                                    
                                                                                                                                                                                                                     
#### The mental model: deferred tools are like lazy imports. ToolSearch is the import statement. I only run it when I'm about to use the symbol — or, in this case, when you asked to read its source.    