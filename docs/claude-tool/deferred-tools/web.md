# Web

---

## 1. WebFetch                                                                                                                                                                                                        
     
```                                                                    
  {                    
    "name": "WebFetch",
    "description": "Fetch a URL, convert HTML to markdown, then run a small fast model over the content with a user-supplied prompt. Read-only. HTTP auto-upgrades to HTTPS. 15-minute self-cleaning cache. WILL FAIL
   on authenticated URLs (Google Docs, Confluence, Jira, GitHub private) — use a specialized MCP tool for those. For GitHub URLs, prefer `gh` CLI via Bash. On cross-host redirect, the tool returns the new URL and 
  you must re-call WebFetch with it.",                                                                                                                                                                               
    "parameters": {                                                                                                                                                                                                  
      "$schema": "https://json-schema.org/draft/2020-12/schema",         
      "additionalProperties": false,                                                                                                                                                                                 
      "type": "object",
      "required": ["url", "prompt"],                                                                                                                                                                                 
      "properties": {                                                    
        "url": {       
          "type": "string",
          "format": "uri",
          "description": "The URL to fetch content from. Must be fully-formed and valid."                                                                                                                            
        },
        "prompt": {                                                                                                                                                                                                  
          "type": "string",                                              
          "description": "The prompt to run on the fetched (markdown-converted) content."
        }                                                                                                                                                                                                            
      }          
    }                                                                                                                                                                                                                
  }                                                                      
```
                       
##  2. WebSearch

```
  {                                      
    "name": "WebSearch",
    "description": "Search the web and return results as markdown-formatted blocks with links. Use for information beyond the model's knowledge cutoff or current events. Supports domain include/block lists. Only  
  available from US-routed requests. You MUST include a \"Sources:\" section listing the search-result URLs as markdown hyperlinks at the end of any response that uses this tool. When searching for current info,  
  use the current year in the query.",                                                                                                                                                                               
    "parameters": {                                                                                                                                                                                                  
      "$schema": "https://json-schema.org/draft/2020-12/schema",         
      "additionalProperties": false,
      "type": "object",
      "required": ["query"],                                                                                                                                                                                         
      "properties": {                    
        "query": {                                                                                                                                                                                                   
          "type": "string",                                              
          "minLength": 2,
          "description": "The search query to use."
        },       
        "allowed_domains": {             
          "type": "array",                                                                                                                                                                                           
          "items": { "type": "string" },
          "description": "Only include search results from these domains."                                                                                                                                           
        },                                                               
        "blocked_domains": {
          "type": "array",
          "items": { "type": "string" },                                                                                                                                                                             
          "description": "Never include search results from these domains."
        }                                                                                                                                                                                                            
      }                                                                  
    }                  
  }          
```

---   

## Porting notes:
                
  - WebFetch is two-stage: HTTP fetch → HTML→markdown conversion → secondary LLM call with the user's prompt as the "what do you want to extract?" instruction. The secondary LLM is small/fast (presumably
  Haiku-class). To replicate, you need: an HTTP client, an HTML→markdown converter (turndown in JS, html2text or markdownify in Python, pandoc as a shell-out), and a second model call. The prompt-over-content     
  pattern is what keeps the response small — the parent model never sees raw HTML.
  - The 15-minute cache is implemented at the harness level, keyed on the URL. Cheap to add: (url, prompt) → cached_markdown_or_response.                                                                            
  - Auth failure mode: WebFetch can't carry cookies or tokens. If you want authenticated fetches in your runtime, build dedicated tools per service (the way Atlassian/GitHub MCP servers do) rather than extending  
  WebFetch.                                                                                                                                                                                                          
  - WebSearch's mandatory "Sources:" trailer is enforced by prompt instruction, not by schema. If you want it in your own runtime, repeat the instruction in the tool description — there's no machine-readable way  
  to enforce.                                                                                                                                                                                                        
  - WebSearch backend is Anthropic's hosted search (likely a Brave/Bing wrapper). For your own runtime you'd plug in Brave Search API, Tavily, Exa, SerpAPI, or similar — same schema, different provider.
  - US-only restriction is purely a backend constraint of Anthropic's hosted search and won't apply to your own implementation.       