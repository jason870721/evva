#  User Interaction

---

##  1. AskUserQuestion                                                                                                                                                                                                 

```                                                                         
  {                                                                                                                                                                                                                  
    "name": "AskUserQuestion",
    "description": "Ask the user 1–4 structured questions mid-execution to gather preferences, clarify ambiguous instructions, or get decisions on direction. Each question has 2–4 options; the UI auto-adds an     
  \"Other\" free-text option. Set multiSelect: true when choices aren't mutually exclusive. Use option `preview` for visual comparisons (ASCII mockups, code snippets, diagrams) — single-select only. If            
  recommending an option, put it first and append \"(Recommended)\". Do NOT use this to ask \"is the plan ready?\" — use ExitPlanMode for plan approval.",                                                           
    "parameters": {                                                                                                                                                                                                  
      "$schema": "https://json-schema.org/draft/2020-12/schema",         
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",
      "required": ["questions"],                                                                                                                                                                                     
      "properties": {                                                    
        "questions": { 
          "type": "array",
          "minItems": 1,
          "maxItems": 4,                                                                                                                                                                                             
          "description": "Questions to ask the user (1-4 questions)",
          "items": {                                                                                                                                                                                                 
            "type": "object",                                            
            "additionalProperties": false,
            "required": ["question", "header", "options", "multiSelect"],                                                                                                                                            
            "properties": {
              "question": {                                                                                                                                                                                          
                "type": "string",                                        
                "description": "The complete question. Clear, specific, ending with a question mark."
              },                                                                                                                                                                                                     
              "header": {
                "type": "string",                                                                                                                                                                                    
                "description": "Very short label displayed as a chip/tag (max 12 chars). E.g., \"Auth method\", \"Library\"."
              },                                                                                                                                                                                                     
              "multiSelect": {
                "type": "boolean",                                                                                                                                                                                   
                "default": false,                                        
                "description": "Allow multiple options to be selected. Use when choices are not mutually exclusive."                                                                                                 
              },
              "options": {                                                                                                                                                                                           
                "type": "array",                                         
                "minItems": 2,                                                                                                                                                                                       
                "maxItems": 4,
                "description": "2-4 mutually exclusive choices. Do NOT add an \"Other\" option — the UI provides it automatically.",                                                                                 
                "items": {                                                                                                                                                                                           
                  "type": "object",
                  "additionalProperties": false,                                                                                                                                                                     
                  "required": ["label", "description"],                                                                                                                                                              
                  "properties": {        
                    "label": {                                                                                                                                                                                       
                      "type": "string",                                  
                      "description": "Display text (1-5 words)."
                    },
                    "description": {
                      "type": "string",                                                                                                                                                                              
                      "description": "Explanation of what choosing this option means or implies."
                    },                                                                                                                                                                                               
                    "preview": {                                         
                      "type": "string",
                      "description": "Optional preview content (markdown in monospace). Use for mockups, code snippets, side-by-side comparisons. Single-select only."                                               
                    }                                                                                                                                                                                                
                  }                                                                                                                                                                                                  
                }                                                                                                                                                                                                    
              }                                                          
            }          
          }      
        },                               
        "answers": {                     
          "type": "object",
          "additionalProperties": { "type": "string" },
          "propertyNames": { "type": "string" },                                                                                                                                                                     
          "description": "User answers collected by the permission component (populated by runtime)."
        },                                                                                                                                                                                                           
        "annotations": {                                                 
          "type": "object",                                                                                                                                                                                          
          "propertyNames": { "type": "string" },                         
          "additionalProperties": {
            "type": "object",            
            "additionalProperties": false,
            "properties": {                                                                                                                                                                                          
              "notes": {
                "type": "string",                                                                                                                                                                                    
                "description": "Free-text notes the user added to their selection."
              },       
              "preview": {
                "type": "string",
                "description": "The preview content of the selected option."                                                                                                                                         
              }
            }                                                                                                                                                                                                        
          },                                                             
          "description": "Optional per-question annotations from the user, keyed by question text."
        },   
        "metadata": {
          "type": "object",                                                                                                                                                                                          
          "additionalProperties": false,
          "description": "Optional metadata for analytics. Not displayed to user.",                                                                                                                                  
          "properties": {                                                
            "source": {                                                                                                                                                                                              
              "type": "string",
              "description": "Identifier for the source of this question (e.g., \"remember\" for /remember)."                                                                                                        
            }                                                            
          }            
        }                                                                                                                                                                                                            
      }          
    }                                                                                                                                                                                                                
  }                                                                      
```                       

###  2. PushNotification
```                 
  {                                      
    "name": "PushNotification",
    "description": "Send a desktop notification to the user's terminal (and to their phone if Remote Control is connected). Pulls the user's attention away from whatever they're doing — use sparingly. Send only 
  when there's a real chance the user has walked away AND something worth coming back for has happened: long task finished, build ready, blocker hit that needs their decision. Never send for routine progress or a 
  task that completed seconds after they asked. Lead with the actionable detail (\"build failed: 2 auth tests\" beats \"task done\"). Under 200 chars, one line, no markdown. If the result says push wasn't sent, no
   action needed.",                                                                                                                                                                                                  
    "parameters": {                                                      
      "$schema": "https://json-schema.org/draft/2020-12/schema",
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",
      "required": ["message", "status"],                                                                                                                                                                             
      "properties": {                                                    
        "message": {   
          "type": "string",
          "minLength": 1,
          "description": "The notification body. Keep under 200 characters; mobile OSes truncate."                                                                                                                   
        },
        "status": {                                                                                                                                                                                                  
          "type": "string",                                              
          "const": "proactive",
          "description": "Always the literal string \"proactive\"."
        }                                                                                                                                                                                                            
      }                                  
    }                                                                                                                                                                                                                
  }                                                                      
```

---

##  Porting notes: 
                                         
  - AskUserQuestion is the most UI-heavy tool in the harness. To replicate, your runtime needs: (1) a way to pause model execution mid-turn, (2) a UI component to render questions + options + previews, (3) a way
  to feed the user's selection back into the tool result. The answers and annotations fields are output fields (populated by the runtime when the user responds) — the model only fills questions. Most agent        
  frameworks call this pattern "human-in-the-loop" or "interrupt."
  - preview rendering: side-by-side layout switches on automatically when any option has a preview. Content is rendered as monospace markdown — line breaks supported. Cheap to add to a custom UI but worth the     
  effort for code/mockup comparisons.                                                                                                                                                                                
  - PushNotification's status field is a constant "proactive" — looks like a future-proofing slot for other notification kinds. Trivial to ignore in a custom runtime; just send the message.
  - Notification delivery: terminal notification + optional mobile push via Anthropic's "Remote Control." For your own runtime you'd wire to your OS (notify-send on Linux, osascript on macOS, Toast API on Windows)
   and/or your own push service.    