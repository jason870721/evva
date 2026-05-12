# Task & Process Management

---

Here are all 12 Task & Process Management tool schemas, grouped by subcategory.                                                                                                                                    

---

## Task Management (6 tools)                                                                                                                                                                                          
                                                                                                                                                                                                                     
###  1. TaskCreate                                                                                                                                                                                                      
   
```                                                                      
  {                                      
    "name": "TaskCreate",
    "description": "Create a structured task in the session's task list. Use for complex multi-step work (3+ steps), plan mode, or when the user provides multiple tasks. Skip for single trivial tasks. All tasks   
  are created with status `pending`.",                                                                                                                                                                               
    "parameters": {                                                                                                                                                                                                  
      "$schema": "https://json-schema.org/draft/2020-12/schema",                                                                                                                                                     
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",
      "required": ["subject", "description"],                                                                                                                                                                        
      "properties": {                                                    
        "subject": {                     
          "type": "string",
          "description": "A brief title for the task (imperative form, e.g., \"Fix authentication bug\")"                                                                                                            
        },                               
        "description": {                                                                                                                                                                                             
          "type": "string",                                              
          "description": "What needs to be done"                                                                                                                                                                     
        },   
        "activeForm": {                                                                                                                                                                                              
          "type": "string",                                              
          "description": "Present continuous form shown in spinner when in_progress (e.g., \"Running tests\")"
        },                                                                                                                                                                                                           
        "metadata": {
          "type": "object",                                                                                                                                                                                          
          "additionalProperties": {},                                    
          "propertyNames": { "type": "string" },
          "description": "Arbitrary metadata to attach to the task"                                                                                                                                                  
        }        
      }                                                                                                                                                                                                              
    }                                                                    
  }                    
```

###  2. TaskGet     
                                         
```
  {
    "name": "TaskGet",
    "description": "Retrieve full task details by ID — subject, description, status, blocks, blockedBy. Verify blockedBy is empty before starting work on a task.",                                                  
    "parameters": {                                                                                                                                                                                                  
      "$schema": "https://json-schema.org/draft/2020-12/schema",                                                                                                                                                     
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",                                                                                                                                                                                              
      "required": ["taskId"],
      "properties": {                                                                                                                                                                                                
        "taskId": {                                                      
          "type": "string",              
          "description": "The ID of the task to retrieve"
        }                                                                                                                                                                                                            
      }                                  
    }                                                                                                                                                                                                                
  }                                                                      
```

###  3. TaskList

```                 
  {                                      
    "name": "TaskList",
    "description": "List all tasks in summary form (id, subject, status, owner, blockedBy). Prefer working on tasks in ID order (lowest first). Use TaskGet for full details.",
    "parameters": {                                                                                                                                                                                                  
      "$schema": "https://json-schema.org/draft/2020-12/schema",
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",                                                  
      "properties": {}                                                                                                                                                                                               
    }                                                                    
  }                    
```

###  4. TaskUpdate                                                                                                                                                                                                      

```                                         
  {                                                                                                                                                                                                                  
    "name": "TaskUpdate",                                                
    "description": "Update an existing task — status, subject, description, owner, dependencies. Status flow: pending → in_progress → completed (use `deleted` to permanently remove). Only mark completed when work 
  is fully done.",                                                                                                                                                                                                   
    "parameters": {
      "$schema": "https://json-schema.org/draft/2020-12/schema",                                                                                                                                                     
      "additionalProperties": false,                                     
      "type": "object",
      "required": ["taskId"],
      "properties": {                                                                                                                                                                                                
        "taskId": {                      
          "type": "string",                                                                                                                                                                                          
          "description": "The ID of the task to update"                  
        },             
        "status": {
          "description": "New status for the task",
          "anyOf": [                                                                                                                                                                                                 
            { "type": "string", "enum": ["pending", "in_progress", "completed"] },
            { "type": "string", "const": "deleted" }                                                                                                                                                                 
          ]                                                              
        },             
        "subject": {                                                                                                                                                                                                 
          "type": "string",
          "description": "New subject for the task"                                                                                                                                                                  
        },                                                               
        "description": {
          "type": "string",
          "description": "New description for the task"
        },                               
        "activeForm": {
          "type": "string",                                                                                                                                                                                          
          "description": "Present continuous form shown in spinner when in_progress"
        },                                                                                                                                                                                                           
        "owner": {                                                       
          "type": "string",
          "description": "New owner for the task"
        },                                                                                                                                                                                                           
        "addBlocks": {                   
          "type": "array",                                                                                                                                                                                           
          "items": { "type": "string" },                                 
          "description": "Task IDs that this task blocks"
        },   
        "addBlockedBy": {
          "type": "array",                                                                                                                                                                                           
          "items": { "type": "string" },
          "description": "Task IDs that block this task"                                                                                                                                                             
        },                                                               
        "metadata": {  
          "type": "object",
          "additionalProperties": {},                                                                                                                                                                                
          "propertyNames": { "type": "string" },
          "description": "Metadata keys to merge into the task. Set a key to null to delete it."                                                                                                                     
        }                                                                                                                                                                                                            
      }                
    }                                                                                                                                                                                                                
  }                                                                      
```


###  5. TaskOutput

```                 
  {                                      
    "name": "TaskOutput",
    "description": "Retrieve stdout/stderr output from a running or completed task (background shell, agent, or remote session). DEPRECATED for many cases — prefer reading the task's output file path directly with
   Read for bash/remote_agent tasks. Local_agent tasks: use the Agent result, never Read the .output file (transcript will overflow context).",                                                                      
    "parameters": {
      "$schema": "https://json-schema.org/draft/2020-12/schema",                                                                                                                                                     
      "additionalProperties": false,                                     
      "type": "object",
      "required": ["task_id", "block", "timeout"],                                                                                                                                                                   
      "properties": {
        "task_id": {                                                                                                                                                                                                 
          "type": "string",                                              
          "description": "The task ID to get output from"
        },   
        "block": {                                                                                                                                                                                                   
          "type": "boolean",             
          "default": true,                                                                                                                                                                                           
          "description": "Whether to wait for completion"                
        },             
        "timeout": {
          "type": "number",
          "default": 30000,              
          "minimum": 0,                                                                                                                                                                                              
          "maximum": 600000,
          "description": "Max wait time in ms"                                                                                                                                                                       
        }                                                                
      }                
    }        
  }              
```

###  6. TaskStop

```
  {                                                                                                                                                                                                                  
    "name": "TaskStop",
    "description": "Stop a running background task by ID. Returns success or failure status.",                                                                                                                       
    "parameters": {                                                      
      "$schema": "https://json-schema.org/draft/2020-12/schema",
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",
      "properties": {                                                                                                                                                                                                
        "task_id": {                                                     
          "type": "string",
          "description": "The ID of the background task to stop"                                                                                                                                                     
        },       
        "shell_id": {                                                                                                                                                                                                
          "type": "string",                                              
          "description": "Deprecated: use task_id instead"
        }                                                                                                                                                                                                            
      }          
    }                                                                                                                                                                                                                
  }                                                                      
```

---

##  Process Monitoring (1 tool)
                 
###  7. Monitor                             

```
  {
    "name": "Monitor",
    "description": "Start a background monitor that streams events from a long-running script. Each stdout line becomes a notification. Use for per-occurrence events (log watchers, file change watchers, poll 
  loops). For a single \"tell me when X is done\" notification, prefer Bash with run_in_background + an `until` loop instead. Use `grep --line-buffered` in pipes. Filter must cover terminal failure states, not    
  just success — silence ≠ success.",
    "parameters": {                                                                                                                                                                                                  
      "$schema": "https://json-schema.org/draft/2020-12/schema",                                                                                                                                                     
      "additionalProperties": false,
      "type": "object",                                                                                                                                                                                              
      "required": ["description", "timeout_ms", "persistent", "command"],
      "properties": {                                                                                                                                                                                                
        "command": {
          "type": "string",                                                                                                                                                                                          
          "description": "Shell command or script. Each stdout line is an event; exit ends the watch."
        },                                                                                                                                                                                                           
        "description": {
          "type": "string",                                                                                                                                                                                          
          "description": "Short human-readable description of what you are monitoring (shown in notifications)."
        },                                                                                                                                                                                                           
        "persistent": {
          "type": "boolean",                                                                                                                                                                                         
          "default": false,                                              
          "description": "Run for the lifetime of the session (no timeout). Stop with TaskStop."
        },                                                                                                                                                                                                           
        "timeout_ms": {
          "type": "number",                                                                                                                                                                                          
          "default": 300000,                                             
          "minimum": 1000,
          "description": "Kill the monitor after this deadline. Default 300000ms, max 3600000ms. Ignored when persistent is true."                                                                                   
        }        
      }                                                                                                                                                                                                              
    }                                                                                                                                                                                                                
  }                    
```

---

##  Plan Mode (2 tools)                                                    
   
                                      
###  8. EnterPlanMode

```                 
  {                                      
    "name": "EnterPlanMode",
    "description": "Transition into plan mode to explore the codebase and design an implementation approach before writing code. Use proactively for non-trivial implementation tasks (new features, multiple valid 
  approaches, multi-file changes, unclear requirements). Skip for typo fixes, single-function additions, or pure research.",                                                                                         
    "parameters": {
      "$schema": "https://json-schema.org/draft/2020-12/schema",                                                                                                                                                     
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",
      "properties": {}                                                                                                                                                                                               
    }                                                                    
  }                                      
```

###  9. ExitPlanMode                                                                                                                                                                                                    

```                                         
  {                                                                                                                                                                                                                  
    "name": "ExitPlanMode",                                              
    "description": "Signal that the plan is complete and ready for user approval. The plan content is read from the plan file (not passed as a parameter). Use only for implementation planning, not for research 
  tasks. Do NOT use AskUserQuestion to ask \"is this plan okay?\" — that's exactly what this tool does.",                                                                                                            
    "parameters": {
      "$schema": "https://json-schema.org/draft/2020-12/schema",                                                                                                                                                     
      "additionalProperties": {},                                                                                                                                                                                    
      "type": "object",
      "properties": {                                                                                                                                                                                                
        "allowedPrompts": {                                              
          "type": "array",               
          "description": "Prompt-based permissions needed to implement the plan.",
          "items": {                                                                                                                                                                                                 
            "type": "object",            
            "additionalProperties": false,                                                                                                                                                                           
            "required": ["tool", "prompt"],                              
            "properties": {
              "tool": {                                                                                                                                                                                              
                "type": "string",
                "enum": ["Bash"],                                                                                                                                                                                    
                "description": "The tool this prompt applies to"         
              },       
              "prompt": {
                "type": "string",                                                                                                                                                                                    
                "description": "Semantic description of the action, e.g. \"run tests\", \"install dependencies\""
              }                                                                                                                                                                                                      
            }                                                            
          }            
        }    
      }                                                                                                                                                                                                              
    }                                    
  }                                                                                                                                                                                                                  
```
  
---

##  Worktree Isolation (2 tools)
             
###  10. EnterWorktree

```                                         
  {
    "name": "EnterWorktree",
    "description": "Create an isolated git worktree and switch the session into it. Use ONLY when the user explicitly says \"worktree\" or CLAUDE.md/memory instructs it. Do not use for ordinary branch work. Pass  
  `path` to enter an existing worktree instead of creating one.",                                                                                                                                                    
    "parameters": {                                                                                                                                                                                                  
      "$schema": "https://json-schema.org/draft/2020-12/schema",                                                                                                                                                     
      "additionalProperties": false,                                     
      "type": "object",
      "properties": {
        "name": {                                                                                                                                                                                                    
          "type": "string",              
          "description": "Optional name for a new worktree. Each \"/\"-separated segment may contain only letters, digits, dots, underscores, and dashes; max 64 chars total. Mutually exclusive with `path`."       
        },                                                                                                                                                                                                           
        "path": {      
          "type": "string",                                                                                                                                                                                          
          "description": "Path to an existing worktree of the current repository to switch into. Must appear in `git worktree list`. Mutually exclusive with `name`."
        }                                                                                                                                                                                                            
      }      
    }                                                                                                                                                                                                                
  }
```
                                                                                                                                                                                                                     
###  11. ExitWorktree

```                                                                                                                                                                                                                     
  {                                                                      
    "name": "ExitWorktree",
    "description": "Exit a worktree session created by EnterWorktree and return to the original working directory. No-op if no worktree session is active. Only operates on worktrees created by EnterWorktree in 
  this session — never touches manually-created worktrees.",                                                                                                                                                         
    "parameters": {                      
      "$schema": "https://json-schema.org/draft/2020-12/schema",                                                                                                                                                     
      "additionalProperties": false,                                     
      "type": "object",                                                                                                                                                                                              
      "required": ["action"],
      "properties": {                                                                                                                                                                                                
        "action": {                                                      
          "type": "string",
          "enum": ["keep", "remove"],
          "description": "\"keep\" leaves the worktree and branch on disk; \"remove\" deletes both."                                                                                                                 
        },                               
        "discard_changes": {                                                                                                                                                                                         
          "type": "boolean",                                             
          "description": "Required true when action is \"remove\" and the worktree has uncommitted files or unmerged commits."
        }                                                                                                                                                                                                            
      }          
    }                                                                                                                                                                                                                
  }                                                                      
```

---

##  Notebook Editing (1 tool)                                                                                                                                                                                          
                 
###  12. NotebookEdit                                                                                                                                                                                                   

```                                                                         
  {                    
    "name": "NotebookEdit",
    "description": "Replace, insert, or delete a cell in a Jupyter notebook (.ipynb). The notebook_path must be absolute. Cells are 0-indexed. Use edit_mode=insert to add a new cell after the index/cell_id; 
  edit_mode=delete to remove the cell at that index.",                                                                                                                                                               
    "parameters": {
      "$schema": "https://json-schema.org/draft/2020-12/schema",                                                                                                                                                     
      "additionalProperties": false,                                     
      "type": "object",                                                                                                                                                                                              
      "required": ["notebook_path", "new_source"],                       
      "properties": {                                                                                                                                                                                                
        "notebook_path": {               
          "type": "string",                                                                                                                                                                                          
          "description": "The absolute path to the Jupyter notebook file (must be absolute, not relative)"
        },                                                                                                                                                                                                           
        "new_source": {
          "type": "string",                                                                                                                                                                                          
          "description": "The new source for the cell"                   
        },             
        "cell_id": {
          "type": "string",
          "description": "The ID of the cell to edit. When inserting, the new cell goes after this ID, or at the beginning if not specified."
        },                                                                                                                                                                                                           
        "cell_type": {
          "type": "string",                                                                                                                                                                                          
          "enum": ["code", "markdown"],                                  
          "description": "Cell type. Defaults to current cell type. Required when edit_mode=insert."
        },                                                                                                                                                                                                           
        "edit_mode": {
          "type": "string",                                                                                                                                                                                          
          "enum": ["replace", "insert", "delete"],                       
          "description": "The type of edit. Defaults to replace."
        }                                                                                                                                                                                                            
      }          
    }                                                                                                                                                                                                                
  }
```

---

## Porting notes: 
                                         
  - Task tools (1-6) are a self-contained task-tracker subsystem. You'd need: persistent storage (in-memory or file-backed), an ID generator, and dependency-graph logic for blocks/blockedBy. Tasks live for the
  lifetime of a session.                                                                                                                                                                                             
  - Monitor (7) is the trickiest to replicate. It requires a streaming-process supervisor: spawn a shell, read stdout line-by-line, debounce 200ms windows, push each line as an out-of-band conversation event. Most
   agent runtimes don't have this primitive — you'd need a custom event channel back to the model.                                                                                                                   
  - Plan mode (8-9) is mostly a UX layer: it gates write tools and surfaces an approval prompt. Implementable as a permission-mode toggle in your harness.
  - Worktree (10-11) is git-specific. Outside git, the docs hint that WorktreeCreate/WorktreeRemove hooks let you delegate isolation to other VCS — useful if your project isn't on git.                             
  - NotebookEdit (12) is just JSON manipulation on .ipynb files — easy to reimplement standalone with the nbformat library or by hand.        