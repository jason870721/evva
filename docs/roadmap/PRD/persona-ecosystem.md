# PRD — Persona Ecosystem (packs, index, install, trust) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (NOT audited; pin
> file:line references in the audit pass before implementation).
> **Target release:** TBD — tentative slot **W18 / v1.28** per
> [../long-range.md](../long-range.md). The pack manifest becomes part
> of the frozen v2.0 surface (W19 dependency edge).
> **Roadmap source:** 2026-07-06 long-range planning pass. "One runtime,
> many personas" is evva's founding idea; RP-29 made personas full swarm
> citizens. But a persona is still something you hand-author in
> `<EVVA_HOME>/agents/` — there is no way to *share* one. Every
> ecosystem moment in dev tooling (vim plugins, VS Code extensions, MCP
> servers, Claude Code plugins) happened when artifacts became
> installable units.
> **Reference source:** the agent-definition layout in
> `docs/architecture.md` (system_prompt.md + tools.yml + meta.yml) is
> the unit being packaged; distribution design is evva-native.

---

## 1. TL;DR

Make personas (and their skills) **shareable, installable artifacts**:

```
$ evva persona install github.com/frizo/nono
  nono v1.2.0 — financial manager persona
  requests: tools [read, web_search, calc], no hooks, no bash
  model preference: deepseek/deepseek-v3 (you have this configured)
  sha256 pinned: 4f2a…  Install? [y/N]
$ evva persona list
  evva   (builtin)   main
  nono   v1.2.0      main, subagent   from github.com/frizo/nono
```

A **pack** is a directory (usually a git repo) containing one or more
agent definitions in the existing on-disk layout plus a `pack.yml`
manifest (name, version, compat range against the evva definition
schema, contents inventory, requested tool families, bundled skills).
`evva persona install <ref>` fetches (git shallow-clone or tarball),
shows a **trust prompt built from the inventory** (which tools it
requests, whether it bundles hooks — the dangerous bits are surfaced,
not buried), pins the content hash in a lockfile, and installs into the
existing loader's search path. Updates are explicit (`persona update`),
diffed against the pinned hash, and re-consented when the requested
surface grows.

Discovery is a **git-based index** (an `evva-index` repo of pack
metadata, PR-to-list — the Homebrew-tap/MCP-registry pattern), so there
is no server to run and curation is a code-review process. `evva persona
search nono` greps the index; nothing phones home.

## 2. Goals / non-goals

### Goals

- `pack.yml` v1: name, version (semver), `defines: [agents…, skills…]`,
  `compat: evva-agent-schema >=1 <2`, tool-request inventory (derived +
  verified at install against the actual `tools.yml` contents — a
  manifest that understates its requests fails install), optional
  `signing: {sha256}`.
- Install/update/remove/list CLI with lockfile
  (`<EVVA_HOME>/packs.lock`): source ref, resolved version, content
  hash. Deterministic re-install from lockfile (`evva persona sync`).
- Trust UX: first-install consent screen (tools requested, hooks
  present y/n, bash-capable y/n, model prefs); *any* growth in the
  requested surface on update forces re-consent; hash mismatch vs
  lockfile refuses loudly.
- Loader integration: installed packs mount into the existing Go+disk
  registry merge with clear precedence (builtin < installed pack <
  user-local dir) and collision rules (explicit rename required —
  never silent shadowing).
- The index: schema for entries, a reference `evva-index` repo,
  `search` against a local clone (refreshed on demand), docs for
  listing your pack.
- `evva persona init` scaffold: template pack with lint
  (`persona lint`) checking manifest/layout/compat before publishing.

### Non-goals (this wave)

- A hosted registry, accounts, download counts, or payments — git refs
  and the index repo are the whole distribution story in v1.
- Cryptographic signature chains (hash-pinning + consent + code-review
  index curation is the v1 trust model; sigstore-style signing is a
  documented v2 candidate, §6).
- Sandboxing personas at runtime beyond what their tool grants already
  imply (the permission gate + W3 sandbox are the runtime story; packs
  change what's *installed*, not how it's confined).
- Paid/private index support beyond "point at any git URL" (which
  works by construction).
- Packaging MCP server configs (adjacent but different trust surface;
  revisit after this wave soaks).

## 3. Design sketch

- **The unit already exists:** the loader's agent-definition layout is
  untouched — a pack is a veneer (manifest + provenance + lifecycle)
  over directories the loader already understands. The audit pins the
  loader's merge/precedence behavior and the definition-schema
  versioning hook (`compat` needs the schema to *have* a version;
  introducing one may be ticket zero, coordinated with the v2.0
  freeze).
- **Consent is inventory-driven:** the trust screen is generated from
  the pack's actual contents (parsed `tools.yml`, hook presence,
  skill list), not from self-description — the manifest's claims are
  cross-checked and mismatches fail install. What you approve is what
  loads.
- **Skills ride along:** bundled skills install into the persona's
  scope (not global) by default; the consent screen lists them.
- **Update diffing:** `persona update` shows a surface diff (tools
  added/removed, hooks added, prompt changed y/n) before applying —
  the same honesty-at-the-boundary philosophy as everything else in
  this roadmap.

## 4. Work items

- **PER-1 — Pack format + validation.** `pack.yml` schema,
  inventory cross-check, `persona lint`, definition-schema version
  hook. *Accept:* fixture packs (valid, understating-manifest,
  future-compat) validate/fail correctly.
- **PER-2 — Fetch + lockfile.** Git shallow-clone + tarball fetchers,
  hash pinning, `sync`, deterministic re-install. *Accept:* install →
  delete → `sync` reproduces byte-identical installs; tampered content
  vs lockfile refuses.
- **PER-3 — Consent + update flow.** First-install screen, surface-
  growth re-consent, update diff. *Accept:* an update adding `bash` to
  tools.yml forces re-consent in a fixture; no-change updates don't.
- **PER-4 — Loader mount + precedence.** Pack mount point, collision
  rules, `persona list` provenance column. *Accept:* a pack colliding
  with a user-local persona name fails with the rename instruction;
  precedence table is table-tested.
- **PER-5 — Index + search.** Entry schema, reference index repo,
  local-clone search, listing docs. *Accept:* `search` finds a fixture
  entry; index refresh is explicit, never automatic at session start.
- **PER-6 — Scaffold.** `persona init` template + lint integration +
  publishing walkthrough. *Accept:* scaffold output passes lint and
  installs from a local path unchanged.
- **PER-7 — Docs + changelog.** User-guide (en + zh-tw): installing,
  trusting (what the consent screen means), authoring, listing on the
  index; the `evva → nono` delegation story told end-to-end with an
  installable nono.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Malicious pack = prompt-injection-shaped supply chain | inventory-driven consent, hash pinning, surface-growth re-consent, curated index via PR review; runtime confinement unchanged (permission gate still governs every dangerous call) |
| Ecosystem never materializes (empty index) | the wave's floor value is internal: versioned, reproducible persona provisioning across the operator's own machines (lockfile + sync), which the swarm-manifest workflow already wants |
| Schema evolution breaks installed packs | `compat` range + definition-schema version, coordinated with the v2.0 freeze so the contract is stable exactly when the ecosystem needs it |
| Name squatting / typosquatting in the index | index curation is code review; names are namespaced by source (`github.com/frizo/nono`), never bare |

## 6. Open questions

1. Should `persona install` support a `--as <name>` rename at install
   (nice for collisions) or keep names author-fixed for provenance
   clarity? Leaning `--as` with provenance preserved in `list`.
2. Sigstore-style signing as v2 — what adoption signal triggers it?
3. Do swarm manifests learn to reference packs (`member: {persona:
   github.com/frizo/nono@1.2.0}`) in this wave or a fast-follow?
   Leaning fast-follow; the lockfile makes it mechanical.
