# Scheduling

---

## 1. CronCreate                                                                                                                                                                                                      

```                                                                         
  {                    
    "name": "CronCreate",
    "description": "Schedule a prompt to be enqueued at a future time. Supports recurring (default) and one-shot jobs. Uses standard 5-field cron in the user's local timezone: \"M H DoM Mon DoW\". Avoid :00 and   
  :30 minute marks when possible — pick off-minutes like 7 or 57 to spread load. Recurring jobs auto-expire after 7 days. Jobs only fire while the REPL is idle. Session-only by default (use `durable: true` to     
  persist).",                                                                                                                                                                                                        
    "parameters": {                                                                                                                                                                                                  
      "$schema": "https://json-schema.org/draft/2020-12/schema",         
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",
      "required": ["cron", "prompt"],                                                                                                                                                                                
      "properties": {                                                    
        "cron": {      
          "type": "string",
          "description": "Standard 5-field cron expression in local time: \"M H DoM Mon DoW\" (e.g. \"*/5 * * * *\" = every 5 minutes, \"30 14 28 2 *\" = Feb 28 at 2:30pm local once)."                             
        },                                                                                                                                                                                                           
        "prompt": {                                                                                                                                                                                                  
          "type": "string",                                                                                                                                                                                          
          "description": "The prompt to enqueue at each fire time."      
        },                                                                                                                                                                                                           
        "recurring": {
          "type": "boolean",                                                                                                                                                                                         
          "description": "true (default) = fire on every cron match until deleted or auto-expired after 7 days. false = fire once at the next match, then auto-delete."
        },                                                                                                                                                                                                           
        "durable": {
          "type": "boolean",                                                                                                                                                                                         
          "description": "true = persist to .claude/scheduled_tasks.json and survive restarts. false (default) = in-memory only, dies when this Claude session ends."
        }                                                                                                                                                                                                            
      }      
    }                                                                                                                                                                                                                
  }                                                                      
```

##  2. CronList

```                 
  {                                      
    "name": "CronList",
    "description": "List all cron jobs scheduled via CronCreate in this session.",                                                                                                                                   
    "parameters": {
      "$schema": "https://json-schema.org/draft/2020-12/schema",                                                                                                                                                     
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",
      "properties": {}                                                                                                                                                                                               
    }                                                                    
  }                                      
```

##  3. CronDelete  

```                                         
  {
    "name": "CronDelete",
    "description": "Cancel a cron job previously scheduled with CronCreate. Removes it from the in-memory session store.",                                                                                           
    "parameters": {
      "$schema": "https://json-schema.org/draft/2020-12/schema",                                                                                                                                                     
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",
      "required": ["id"],                                                                                                                                                                                            
      "properties": {                                                    
        "id": {                          
          "type": "string",
          "description": "Job ID returned by CronCreate."                                                                                                                                                            
        }                                
      }                                                                                                                                                                                                              
    }                                                                    
  }                    
```

##  4. RemoteTrigger
 
```                                        
  {
    "name": "RemoteTrigger",
    "description": "Call the claude.ai remote-trigger API. Use this instead of curl — the OAuth token is added automatically in-process and never exposed. Actions: list (GET all), get (GET one), create (POST new —
   requires body), update (POST partial update — requires body), run (POST /run — optional body). Returns raw JSON from the API.",                                                                                   
    "parameters": {                                                                                                                                                                                                  
      "$schema": "https://json-schema.org/draft/2020-12/schema",                                                                                                                                                     
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",
      "required": ["action"],                                                                                                                                                                                        
      "properties": {                                                    
        "action": {                      
          "type": "string",
          "enum": ["list", "get", "create", "update", "run"],
          "description": "API operation to perform."                                                                                                                                                                 
        },
        "trigger_id": {                                                                                                                                                                                              
          "type": "string",                                              
          "pattern": "^[\\w-]+$",
          "description": "Required for get, update, and run."                                                                                                                                                        
        },       
        "body": {                                                                                                                                                                                                    
          "type": "object",                                              
          "additionalProperties": {},
          "propertyNames": { "type": "string" },
          "description": "Required for create and update; optional for run."                                                                                                                                         
        }                                
      }                                                                                                                                                                                                              
    }                                                                    
  }
```

---

## Porting notes:                         
   
  - CronCreate/List/Delete are a local in-session scheduler. To replicate you need: (1) a cron-expression parser (e.g. croniter in Python, node-cron in JS), (2) an idle-detector so jobs only fire when the model
  isn't mid-turn, (3) a way to inject the prompt back into the conversation queue. The "small deterministic jitter" (≤10% of period, max 15 min for recurring; ≤90 s early for :00/:30 one-shots) is a load-spreading
   feature you can copy verbatim or ignore.
  - The off-minute guidance (avoid 0 and 30) is purely a backend-traffic-shaping concern for Anthropic's fleet. If you're building your own runtime that doesn't share an API with thousands of other users, you can 
  drop this rule.                                                                                                                                                                                                    
  - 7-day auto-expiry for recurring jobs is a session-hygiene policy, not a technical constraint — tune to your taste.
  - durable: true writes to .claude/scheduled_tasks.json. If you implement this, decide whether durable jobs survive across users / installs / machines, since cron + persistence can quickly become a security      
  surface (replaying a stale prompt against new state).                                                                                                                                                              
  - RemoteTrigger is a thin Anthropic-specific API wrapper — it only makes sense if you build an equivalent "remote routine" service on your side. The model-facing benefit is just "don't make me handle OAuth      
  tokens." For local-only projects, skip it entirely.   