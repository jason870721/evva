# Active Tools Schema

---

## 1. Agent                                                                                                                                                                                                           

```             
  {                                                                                                                                                                                                                  
    "name": "Agent",                                                     
    "description": "Launch a new agent to handle complex, multi-step tasks. Each agent type has specific capabilities and tools available to it.\n\nAvailable agent types and the tools they have access to:\n- 
  claude-code-guide: Use this agent when the user asks questions (\"Can Claude...\", \"Does Claude...\", \"How do I...\") about: (1) Claude Code (the CLI tool) - features, hooks, slash commands, MCP servers,      
  settings, IDE integrations, keyboard shortcuts; (2) Claude Agent SDK - building custom agents; (3) Claude API (formerly Anthropic API) - API usage, tool use, Anthropic SDK usage. **IMPORTANT:** Before spawning a
   new agent, check if there is already a running or recently completed claude-code-guide agent that you can continue via SendMessage. (Tools: Bash, Read, WebFetch, WebSearch)\n- Explore: Fast read-only search    
  agent for locating code. Use it to find files by pattern (eg. \"src/components/**/*.tsx\"), grep for symbols or keywords (eg. \"API endpoints\"), or answer \"where is X defined / which files reference Y.\" Do 
  NOT use it for code review, design-doc auditing, cross-file consistency checks, or open-ended analysis — it reads excerpts rather than whole files and will miss content past its read window. When calling, 
  specify search breadth: \"quick\" for a single targeted lookup, \"medium\" for moderate exploration, or \"very thorough\" to search across multiple locations and naming conventions. (Tools: All tools except 
  Agent, ExitPlanMode, Edit, Write, NotebookEdit)\n- general-purpose: General-purpose agent for researching complex questions, searching for code, and executing multi-step tasks. When you are searching for a 
  keyword or file and are not confident that you will find the right match in the first few tries use this agent to perform the search for you. (Tools: *)\n- Plan: Software architect agent for designing
  implementation plans. Use this when you need to plan the implementation strategy for a task. Returns step-by-step plans, identifies critical files, and considers architectural trade-offs. (Tools: All tools
  except Agent, ExitPlanMode, Edit, Write, NotebookEdit)\n- statusline-setup: Use this agent to configure the user's Claude Code status line setting. (Tools: Read, Edit)\n\nWhen using the Agent tool, specify a
  subagent_type parameter to select which agent type to use. If omitted, the general-purpose agent is used.",
    "parameters": {
      "$schema": "https://json-schema.org/draft/2020-12/schema",
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",
      "required": ["description", "prompt"],                                                                                                                                                                         
      "properties": {                                                    
        "description": {
          "type": "string",
          "description": "A short (3-5 word) description of the task"
        },                                                                                                                                                                                                           
        "prompt": {
          "type": "string",                                                                                                                                                                                          
          "description": "The task for the agent to perform"             
        },             
        "subagent_type": {
          "type": "string",                                                                                                                                                                                          
          "description": "The type of specialized agent to use for this task"
        },                                                                                                                                                                                                           
        "model": {                                                       
          "type": "string",
          "enum": ["sonnet", "opus", "haiku"],
          "description": "Optional model override. Takes precedence over the agent definition's model frontmatter."                                                                                                  
        },                                                                                                                                                                                                           
        "isolation": {                                                                                                                                                                                               
          "type": "string",                                                                                                                                                                                          
          "enum": ["worktree"],                                          
          "description": "Isolation mode. \"worktree\" creates a temporary git worktree so the agent works on an isolated copy of the repo."
        },                                                                                                                                                                                                           
        "run_in_background": {           
          "type": "boolean",                                                                                                                                                                                         
          "description": "Set to true to run this agent in the background. You will be notified when it completes."
        }                                                                                                                                                                                                            
      }      
    }                                                                                                                                                                                                                
  }                                                                      
```

##  2. Bash    

```                                         
  {
    "name": "Bash",
    "description": "Executes a given bash command and returns its output.\n\nThe working directory persists between commands, but shell state does not. The shell environment is initialized from the user's profile 
  (bash or zsh).\n\nPrefer dedicated tools when one fits: Read for known paths, Edit for edits, Write for new files. Reserve Bash for shell-only operations.\n\nSupports: optional timeout (max 600000 ms),          
  background execution via run_in_background, and a dangerouslyDisableSandbox escape hatch.\n\nIncludes detailed protocols for safe git commits, PR creation via gh, and avoiding destructive operations without 
  explicit user consent.",                                                                                                                                                                                           
    "parameters": {                                                      
      "$schema": "https://json-schema.org/draft/2020-12/schema",
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",                  
      "required": ["command"],                                                                                                                                                                                       
      "properties": {                                                    
        "command": {   
          "type": "string",
          "description": "The command to execute"                                                                                                                                                                    
        },
        "description": {                                                                                                                                                                                             
          "type": "string",                                              
          "description": "Clear, concise description of what this command does in active voice."
        },                                                                                                                                                                                                           
        "timeout": {                     
          "type": "number",                                                                                                                                                                                          
          "description": "Optional timeout in milliseconds (max 600000)" 
        },             
        "run_in_background": {
          "type": "boolean",             
          "description": "Set to true to run this command in the background. Use Read to read the output later."                                                                                                     
        },
        "dangerouslyDisableSandbox": {                                                                                                                                                                               
          "type": "boolean",                                                                                                                                                                                         
          "description": "Set this to true to dangerously override sandbox mode and run commands without sandboxing."
        }                                                                                                                                                                                                            
      }                                                                  
    }                  
  }          
```

##  3. Edit

```                                                                                                                                                                                                                     
  {                                                                      
    "name": "Edit",    
    "description": "Performs exact string replacements in files.\n\nUsage:\n- You must use Read at least once in the conversation before editing.\n- Preserve exact indentation as it appears AFTER the line-number 
  prefix in Read output. Never include any part of the line-number prefix in old_string or new_string.\n- Prefer editing existing files. Never create new files unless explicitly required.\n- The edit will FAIL if 
  old_string is not unique — provide more surrounding context or use replace_all.",
    "parameters": {                                                                                                                                                                                                  
      "$schema": "https://json-schema.org/draft/2020-12/schema",         
      "additionalProperties": false,
      "type": "object",                                                                                                                                                                                              
      "required": ["file_path", "old_string", "new_string"],
      "properties": {                                                                                                                                                                                                
        "file_path": {                                                   
          "type": "string",
          "description": "The absolute path to the file to modify"
        },                                                                                                                                                                                                           
        "old_string": {
          "type": "string",                                                                                                                                                                                          
          "description": "The text to replace"                           
        },             
        "new_string": {
          "type": "string",              
          "description": "The text to replace it with (must be different from old_string)"                                                                                                                           
        },
        "replace_all": {                                                                                                                                                                                             
          "type": "boolean",                                             
          "default": false,
          "description": "Replace all occurrences of old_string (default false)"
        }                                                                                                                                                                                                            
      }
    }                                                                                                                                                                                                                
  }                                                                      
```
                       
##  4. Read    

```                                         
  {
    "name": "Read",
    "description": "Reads a file from the local filesystem by absolute path.\n\n- Default reads up to 2000 lines from the start; use offset/limit for partial reads of large files.\n- Returns content in cat -n 
  format (line numbers starting at 1).\n- Supports images (PNG, JPG, etc.) — displayed visually.\n- Supports PDF (.pdf) — large PDFs (>10 pages) require the pages parameter (max 20 pages per request).\n- Supports 
  Jupyter notebooks (.ipynb) — returns cells with outputs.\n- Reads only files, not directories.",
    "parameters": {                                                                                                                                                                                                  
      "$schema": "https://json-schema.org/draft/2020-12/schema",         
      "additionalProperties": false,
      "type": "object",
      "required": ["file_path"],         
      "properties": {                                                                                                                                                                                                
        "file_path": {
          "type": "string",                                                                                                                                                                                          
          "description": "The absolute path to the file to read"         
        },             
        "offset": {
          "type": "integer",             
          "minimum": 0,                                                                                                                                                                                              
          "maximum": 9007199254740991,
          "description": "The line number to start reading from. Only provide if the file is too large to read at once."                                                                                             
        },                                                               
        "limit": {                                                                                                                                                                                                   
          "type": "integer",
          "exclusiveMinimum": 0,                                                                                                                                                                                     
          "maximum": 9007199254740991,                                   
          "description": "The number of lines to read. Only provide if the file is too large to read at once."
        },                                                                                                                                                                                                           
        "pages": {                       
          "type": "string",                                                                                                                                                                                          
          "description": "Page range for PDF files (e.g., \"1-5\", \"3\", \"10-20\"). Max 20 pages per request."
        }                                                                                                                                                                                                            
      }      
    }                                                                                                                                                                                                                
  }                                                                      
```

##  5. Write   

```                                         
  {
    "name": "Write",
    "description": "Writes a file to the local filesystem.\n\n- Overwrites any existing file at the path.\n- If editing an existing file, you MUST Read it first (the tool will fail otherwise).\n- Prefer Edit for 
  modifying existing files — only use Write to create new files or for full rewrites.\n- Never create .md/README files unless explicitly requested.",                                                                
    "parameters": {
      "$schema": "https://json-schema.org/draft/2020-12/schema",                                                                                                                                                     
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",
      "required": ["file_path", "content"],                                                                                                                                                                          
      "properties": {                                                    
        "file_path": { 
          "type": "string",
          "description": "The absolute path to the file to write (must be absolute, not relative)"
        },                                                                                                                                                                                                           
        "content": {
          "type": "string",                                                                                                                                                                                          
          "description": "The content to write to the file"              
        }              
      }      
    }                                                                                                                                                                                                                
  }
```

##  6. ToolSearch                                                          

```                       
  {          
    "name": "ToolSearch",                
    "description": "Fetches full schema definitions for deferred tools so they can be called.\n\nDeferred tools appear by name in <system-reminder> messages. Until fetched, only the name is known — there is no 
  parameter schema, so the tool cannot be invoked. This tool takes a query, matches it against the deferred tool list, and returns the matched tools' complete JSONSchema definitions inside a <functions>           
  block.\n\nQuery forms:\n- \"select:Read,Edit,Grep\" — fetch these exact tools by name\n- \"notebook jupyter\" — keyword search, up to max_results best matches\n- \"+slack send\" — require \"slack\" in the name, 
  rank by remaining terms",                                                                                                                                                                                          
    "parameters": {                                                      
      "$schema": "https://json-schema.org/draft/2020-12/schema",
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",                  
      "required": ["query", "max_results"],                                                                                                                                                                          
      "properties": {                                                    
        "query": {     
          "type": "string",                                                                                                                                                                                          
          "description": "Query to find deferred tools. Use \"select:<tool_name>\" for direct selection, or keywords to search."
        },                                                                                                                                                                                                           
        "max_results": {                                                 
          "type": "number",                                                                                                                                                                                          
          "default": 5,                                                  
          "description": "Maximum number of results to return (default: 5)"
        }    
      }                                  
    }
  }
```

##  7. Skill                                                                                                                                                                                                           

```   
  {                                                                                                                                                                                                                  
    "name": "Skill",                                                     
    "description": "Execute a skill within the main conversation.\n\nWhen users reference a \"slash command\" or \"/<something>\", they are referring to a skill. Use this tool to invoke it.\n\n- Set `skill` to the
   exact name from the available-skills list (no leading slash). Plugin-namespaced skills use `plugin:skill`.\n- Set `args` to pass optional arguments.\n- Only invoke a skill that appears in the available-skills  
  list or that the user explicitly typed as /<name>.\n- Do not invoke a skill that is already running.\n- Do not use this tool for built-in CLI commands like /help or /clear.",
    "parameters": {                                                                                                                                                                                                  
      "$schema": "https://json-schema.org/draft/2020-12/schema",         
      "additionalProperties": false,
      "type": "object",
      "required": ["skill"],                                                                                                                                                                                         
      "properties": {
        "skill": {                                                                                                                                                                                                   
          "type": "string",                                              
          "description": "The name of a skill from the available-skills list. Do not guess names."
        },                                                                                                                                                                                                           
        "args": {                        
          "type": "string",                                                                                                                                                                                          
          "description": "Optional arguments for the skill"              
        }              
      }      
    }                                    
  }
```

##  8. ScheduleWakeup

```
  {                                                                                                                                                                                                                  
    "name": "ScheduleWakeup",
    "description": "Schedule when to resume work in /loop dynamic mode — the user invoked /loop without an interval, asking you to self-pace iterations of a specific task.\n\nPass the same /loop prompt back via   
  `prompt` each turn so the next firing repeats the task. For an autonomous /loop (no user prompt), pass the literal sentinel `<<autonomous-loop-dynamic>>` as `prompt` instead. Omit the call to end the            
  loop.\n\nPicking delaySeconds: Anthropic prompt cache has a 5-minute TTL — sleeping past 300s pays a cache miss. Stay <270s for active work (cache warm), commit to 1200s+ when waiting longer is fine. Don't pick 
  exactly 300s.",                                                                                                                                                                                                    
    "parameters": {                                                      
      "$schema": "https://json-schema.org/draft/2020-12/schema",
      "additionalProperties": false,
      "type": "object",                                                                                                                                                                                              
      "required": ["delaySeconds", "reason", "prompt"],
      "properties": {                                                                                                                                                                                                
        "delaySeconds": {                                                
          "type": "number",
          "description": "Seconds from now to wake up. Clamped to [60, 3600] by the runtime."                                                                                                                        
        },                               
        "prompt": {                                                                                                                                                                                                  
          "type": "string",                                              
          "description": "The /loop input to fire on wake-up. Pass the same input verbatim each turn, or use the `<<autonomous-loop-dynamic>>` sentinel for autonomous loops."                                       
        },                                                                                                                                                                                                           
        "reason": {                                                                                                                                                                                                  
          "type": "string",                                                                                                                                                                                          
          "description": "One short sentence explaining the chosen delay. Shown back to the user."
        }                                                                                                                                                                                                            
      }                                  
    }                                                                                                                                                                                                                
  }                                                                      
```     