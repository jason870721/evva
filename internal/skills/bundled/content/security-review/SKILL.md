# security-review Conduct a focused security review of the branch's pending changes

Use this skill when the user wants a security pass on a branch's pending changes ("security review", "check for vulnerabilities", "/security-review"). This is NOT a general code review — focus only on security implications introduced by the diff.

## Gather the diff

Run these in parallel with `bash`:

- `git status`
- `git diff --name-only origin/HEAD...`
- `git log --no-decorate origin/HEAD...`
- `git diff origin/HEAD...`

If `origin/HEAD` does not exist, fall back to `git diff main...` or `git diff master...`. If neither exists, ask the user which branch the diff should be taken against.

## Objective

Identify HIGH-CONFIDENCE security vulnerabilities with real exploitation potential introduced by THIS diff. Do not flag existing concerns; do not flag theoretical issues.

## Critical instructions

1. **Minimize false positives.** Only flag issues where you are >80% confident of actual exploitability.
2. **Avoid noise.** Skip theoretical issues, style concerns, low-impact findings.
3. **Focus on impact.** Prioritize vulnerabilities that lead to unauthorized access, data breach, or system compromise.
4. **Hard exclusions** — never report:
   - Denial of Service (DOS) vulnerabilities, even if they disrupt service.
   - Secrets or sensitive data stored on disk (handled by other processes).
   - Rate limiting or resource exhaustion.
   - Memory safety issues in memory-safe languages (Go, Rust, Java, Python, ...).
   - Race conditions or timing attacks that are theoretical rather than practical.
   - SSRF that only controls the path (must control host or protocol to be a vulnerability).
   - Findings in test files or test-only code.
   - Log spoofing or non-PII data logging.
   - Vulnerabilities in outdated third-party libraries (managed separately).

## Categories to examine

**Input validation**
- SQL injection via unsanitized user input
- Command injection in system calls or subprocesses
- XXE in XML parsing
- Template injection
- NoSQL injection
- Path traversal in file operations

**Authentication & authorization**
- Authentication bypass logic
- Privilege escalation paths
- Session management flaws
- JWT vulnerabilities
- Authorization logic bypasses

**Crypto & secrets**
- Hardcoded API keys, passwords, tokens
- Weak cryptographic algorithms
- Improper key storage
- Cryptographic randomness issues
- Certificate validation bypasses

**Injection & code execution**
- RCE via deserialization
- Pickle/YAML injection
- Eval injection
- XSS (reflected, stored, DOM-based) in web frontends NOT mediated by an auto-escaping framework

**Data exposure**
- Sensitive data in logs or persistent storage
- PII handling violations
- API endpoint data leakage
- Debug information exposure

## Methodology

### Phase 1 — Repository context
Use `read`, `grep`, `glob` to:
- Identify existing security frameworks/libraries in use.
- Look for established secure-coding patterns elsewhere in the repo.
- Examine existing sanitization and validation patterns.

### Phase 2 — Comparative analysis
- Compare the diff against the established patterns above.
- Identify deviations from secure practice.
- Flag code that introduces new attack surfaces.

### Phase 3 — Vulnerability assessment
- Inspect each modified file for security implications.
- Trace data flow from user inputs to sensitive operations.
- Identify privilege boundaries crossed unsafely.

### Phase 4 — False-positive filtering (parallel subagents)
For each candidate finding, spawn a parallel `agent` with `subagent_type: "explore"` and the false-positive filtering rules below as part of the prompt. Each subagent assigns a confidence score 1–10. Drop any finding with confidence < 8.

## Output format

Markdown. One section per finding, in severity order:

```
# Vuln 1: <category>: `path/to/file.go:42`

* Severity: High | Medium | Low
* Description: <what's wrong>
* Exploit scenario: <how an attacker exploits it>
* Recommendation: <concrete fix>
```

Severity guidelines:
- **High** — directly exploitable, leading to RCE, data breach, or auth bypass.
- **Medium** — vulnerabilities requiring specific conditions but with significant impact.
- **Low** — defense-in-depth issues or low-impact vulnerabilities (only report if obvious and concrete).

Confidence scoring (internal — not reported):
- 0.9–1.0: Certain exploit path identified.
- 0.8–0.9: Clear vulnerability pattern with known exploitation methods.
- 0.7–0.8: Suspicious pattern needing specific conditions.
- < 0.7: Do not report.

## False-positive filtering rules (give these to the per-finding subagents verbatim)

> You do not need to run commands to reproduce — read the code. Do not write files. Do not modify settings.
>
> Auto-exclude:
> 1. Denial of Service or resource exhaustion.
> 2. Secrets or credentials on disk if otherwise secured.
> 3. Rate limiting / service overload.
> 4. Memory / CPU exhaustion.
> 5. Lack of input validation on non-security-critical fields without a proven security impact.
> 6. Input-sanitization concerns in GitHub Action workflows unless clearly triggerable via untrusted input.
> 7. Lack of hardening measures — code is not expected to implement every best practice.
> 8. Theoretical race or timing attacks. Only report concretely problematic ones.
> 9. Outdated third-party library vulnerabilities.
> 10. Memory safety issues in memory-safe languages.
> 11. Files that are only unit tests.
> 12. Log spoofing of un-sanitized user input.
> 13. SSRF that only controls the path.
> 14. User-controlled content in AI system prompts.
> 15. Regex injection / regex DOS.
> 16. Findings in documentation files.
> 17. Lack of audit logs.
>
> Precedents:
> 1. Logging high-value secrets in plaintext IS a vulnerability. Logging URLs is assumed safe.
> 2. UUIDs are unguessable; do not require validation.
> 3. Environment variables and CLI flags are trusted in secure environments.
> 4. Resource leaks (memory, file descriptors) are not vulnerabilities.
> 5. React and Angular are XSS-safe except through `dangerouslySetInnerHTML` / `bypassSecurityTrustHtml` / similar.
> 6. Lack of permission checks in client-side JS/TS is not a vulnerability — the backend is responsible.
> 7. Most GitHub Actions vulnerabilities are not exploitable in practice.
>
> Signal-quality criteria:
> 1. Concrete, exploitable vulnerability with a clear attack path?
> 2. Real security risk vs. theoretical best practice?
> 3. Specific code locations and reproduction steps?
> 4. Actionable for a security team?
>
> Confidence score 1–10:
> - 1–3: low confidence, likely false positive
> - 4–6: medium, needs investigation
> - 7–10: high confidence, likely true vulnerability

## Final reminder

Better to miss some theoretical issues than to flood the report with false positives. Each finding should be something a security engineer would confidently raise in PR review.
