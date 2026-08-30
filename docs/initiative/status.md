# Release and evidence ledger

This document records **identifiers, not conclusions**. A run ID, a commit SHA, a release ID and
a ruleset ID either exist or they do not; a sentence describing how a project is going decays
silently the moment the project moves. Every claim below is therefore anchored to something that
can be fetched and checked, and any claim that cannot be anchored is written as unverified rather
than as a soft yes.

## Division of authority

| Source | Authoritative for |
|---|---|
| `docs/initiative/architecture.md`, `contracts.md`, `decisions.md` | **Accepted design.** The trust boundaries, the cross-repository ownership matrix, and the numbered decision log D-001…D-011. |
| The Linear `hextap` project (team SB23) | **State, work, and evidence.** What is broken, what is being done about it, who is doing it, and what proved it. |
| This file | The **evidence index** — the identifiers a reader needs in order to check the platform's claims against GitHub directly. |

This file does not restate defects. It names their Linear ids and stops. That is deliberate: a
defect described in two places drifts in one of them, and the copy in the repository is always the
one that goes stale — which is exactly how the document this one replaces became misleading.

Identifiers below were verified live against GitHub and Linear on **2026-08-30**. Reverify before
acting on them — [Reverifying](#reverifying) gives the commands for both, including the Linear
queries, since no `gh` command can check the defect list.

One gate is an exception to the rule above and is marked as such where it appears: **install** rests
on a local observation with no fetchable identifier behind it.

## The five gates

Hextap is an authority-and-evidence firewall. It exists because passing one boundary was once
allowed to imply the next, and a tap-owned Formula got overwritten. The gates are therefore named,
separate, and never collapsed into a single status:

| Gate | Question it answers | What it explicitly does **not** prove |
|---|---|---|
| **Source** | Did the commit reach `main` through the protected pull-request flow, with the required check green and tag protection enforced? | That a human reviewed it. `required_approving_review_count` is **0** on the owned rulesets (`internal/onboard/templates.go`), with no code-owner or last-push approval, so a PR can merge unapproved. This gate evidences protected merge, not review. It also does not prove anything was built or released. |
| **Release** | Did the hosted matrix build, verify, attest and publish an immutable release? | That Homebrew received anything. Release publication and Formula publication are separate jobs and have failed independently — repeatedly. |
| **Tap** | Was the tap-owned Formula updated by a real tap commit, and did tap CI pass on that exact SHA? | That the Formula installs, or that the tap itself is protected. Note the tap carries the download URL and SHA-256, not the package bytes. |
| **Install** | Does the package install, and does the installed binary report the expected version and commit? | That the running service is healthy or that its data is compatible. |
| **Runtime** | Is the running process actually serving correctly against its current data? | Nothing further — this is the last gate, and it is the weakest one here. |

> **One passed gate never grants authority or proves the next.** Code approval is not tag
> authority. A green source run is not Formula publication. Installation is not runtime health.

## Per-version ledger

**One row per version, and this table is the only place a version's outcome is stated.** Every
identifier for a toolkit version — source, release, and tap — lives here. Prose elsewhere in this
document points at these rows and does not restate them.

That constraint is deliberate and was learned the hard way: the `0.1.0` outcome was previously
written in three places (a narrative sentence, a release row, and a job table), was wrong three
times in three different directions, and each correction fixed one representation while leaving
another standing. A fact stated once cannot go inconsistent with itself. It is the same argument
this document makes about not duplicating Linear defect descriptions, applied one level up.

Read the gate columns independently. **A failed release run does not mean the release gate
failed** — for `0.1.0` and `0.5.0` the immutable GitHub release published successfully and the run
failed later, at the separate Homebrew boundary.

**Source gate — evidenced for every row, not inferred from identity.** Merge/tag identity alone
proves only that the tag names the merge commit; it does not prove the PR-head check passed or that
protection was enforced. So each row carries its own **PR head CI** and **merged-`main` CI** run.
All eleven pairs are `success`, and the PR merge commit equals the tag commit in all eleven rows.

Protection needs separate, dated evidence, because a CI run proves a conclusion and not that a
ruleset was enforced when the merge happened. Ruleset **version history** supplies it:

| Ruleset | Latest version | Last modified (UTC) | Target | Enforcement |
|---|---|---|---|---|
| `21411731` `hextap/main` | `47577297` | `2026-08-25T10:22:54Z` | branch | `active` |
| `21411735` `hextap/release-tags` | `47577264` | `2026-08-25T10:22:35Z` | tag | `active` |

`hextap/main` has two versions, both created that day; `hextap/release-tags` has one and has never
been modified. Both predate `v0.1.0`, published `2026-08-26T14:28:43Z`. So **both rulesets have
been `active`, with unchanged definitions, since before the first release** — which covers every
row in the table. Enforcement is part of a ruleset's definition, so toggling it would have created
a later version, and none exists.

```sh
gh api repos/SijanC147/hextap-toolkit/rulesets/21411731/history
gh api repos/SijanC147/hextap-toolkit/rulesets/21411731/history/47577297 \
  --jq '{name:.state.name,target:.state.target,enforcement:.state.enforcement}'
```

**The limit of that evidence, stated rather than glossed:** it establishes the rulesets' continuous
configuration, not that any individual historical merge was evaluated against them.
`repos/SijanC147/hextap-toolkit/rulesets/rule-suites`, which would carry per-evaluation records,
returns `[]`. So per-merge enforcement records are **not available** for these releases, and no
claim in this document rests on them.

The source gate passed for every version; there is no column for it because the value never varies.

A **bold tap commit** was hand-authored by a person; the rest were written by the publisher.

| Version | PR | Merge / tag commit | PR head CI | Merged-`main` CI | Release run | Release ID | **Release gate** | Homebrew step | Tap commit | Tap CI | **Tap gate** |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `v0.1.0` | #3 | `2d4b4615829f983bb4ea7ff2a4b154fb56fd16ea` | `32838086815` | `32969601857` | `32980009852` | `377208649` | **passed** | **failed** — recovery `32990280006` also failed | **`9d27f112`** | `32981835518` | **failed** — see below |
| `v0.1.1` | #4 | `f96c843ea73ebbd521fed3ddbd6622e9ba6982d6` | `32995754608` | `32996124215` | `32996698520` | `377318696` | passed | passed | `345653c0` | `32997289004` | passed |
| `v0.1.2` | #5 | `ddc8371e522a968b051fba26a64bc0d4c39d4d8b` | `33003923022` | `33004349130` | `33004764036` | `377369388` | passed | passed | `3cc76f5d` | `33005207889` | passed |
| `v0.2.0` | #6 | `9a59d2ac9aace0f14a08a921bad0276c00be29e8` | `33014779714` | `33015151916` | `33015551268` | `377435759` | passed | passed | `2aaf9df9` | `33016011545` | passed |
| `v0.3.0` | #7 | `16117b0839fc70487aab9491ee3665379c1f026c` | `33024390659` | `33024655766` | `33024860915` | `377484943` | passed | passed | `dee6a25c` | `33025200293` | passed |
| `v0.3.1` | #8 | `b6a270c29aa02e9b0c76c1d638e8f0d100cba925` | `33026009028` | `33026299647` | `33026549801` | `377493707` | passed | passed | `3ea83f86` | `33026787454` | passed |
| `v0.4.0` | #9 | `f3106610e2ffe4aeb673721219592816a3ef8c1e` | `33037762660` | `33038031679` | `33038299518` | `377559153` | passed | passed | `2519d6f0` | `33038612791` | passed |
| `v0.4.1` | #10 | `67898bb09280a5325b89c1b23a70f2fc8b64ffae` | `33042376451` | `33042645416` | `33042920074` | `377586076` | passed | passed | `dd0b55e0` | `33043233099` | passed |
| `v0.4.2` | #11 | `613f0d37a0c84cff20a8e277fc5e9c374f9cbc26` | `33048175244` | `33048572779` | `33048934828` | `377628533` | passed | passed | `3e30186f` | `33049332506` | passed |
| `v0.5.0` | #12 | `1cd2338f522a22b26d79d5f5303ef11130f495d6` | `33222541721` | `33222845927` | `33223146605` | `378825128` | **passed** | **failed** — recovery `33223947922` passed | **`895c1d3b`** | `33223670639` | passed |
| `v0.6.0` | #13 | `9d1f6ef1ca365f83b118473d5bfcda416e7bf77c` | `33232103954` | `33232339531` | `33232555139` | `378869000` | passed | passed | `f8719fb0` | `33232801566` | passed |

### Reading the two exceptional rows

`v0.1.0` and `v0.5.0` are why the release and tap gates are separate columns rather than one
status. In both, the **release gate passed** — the immutable GitHub release was published — and the
Homebrew step of the same run failed afterwards. Their tap outcomes then diverge, and they diverge
in a way no single "did it ship?" column could express:

- **`v0.5.0` — tap gate passed.** The Formula was hand-authored into the tap (`895c1d3b`, tap
  PR #13 `a9a96e62`) and `brew test-bot` `33223670639` passed on that SHA.
- **`v0.1.0` — tap gate failed.** The gate asks whether tap CI passed on that exact SHA. Run
  `32981835518` concluded **failure**, so the answer is no and the gate did not pass.

  One further detail, which narrows *what* failed without softening *that* it failed. Job level:
  `test-bot (macos-15)` **failure**, `Claude RC proxy release tooling` success, and
  **`Published Formula gate` skipped** — the Formula-validating job never executed, because it was
  skipped once `test-bot` failed. So the gate failed on `test-bot`, and **no verdict was ever
  reached on the Formula itself**. `v0.1.1` superseded the version about four hours later, so none
  ever was. The gate outcome is *failed*; the Formula's own status is *unknown*.

Every version from `0.1.1` on has `Published Formula gate: success` on its own tap commit.
`v0.1.0` is the sole exception — and **nothing in a commit-only view could have surfaced it**,
because a failing tap run and a passing one render identically when only the commit is recorded.
That is the entire justification for the tap CI column existing.

This row has been rewritten four times, and the last two swings are worth recording because they
went in opposite directions. It was called *failed*, then over-corrected to *never evaluated* on
the strength of the skipped job, and is now *failed* again with the skipped job kept as the
narrower point it actually supports. The lesson is that the gate's verdict is whatever its own
stated definition asks — here, "did tap CI pass on that exact SHA" — and a detail discovered one
level down refines that answer without replacing it.

Nine of the eleven tap commits were written by the automated publisher. The two hand-authored
exceptions are exactly the two versions whose Homebrew step failed, so commit authorship is a
durable signal for which versions did not publish cleanly, independent of anyone recording it.

All eleven releases report `immutable: true`.

As of **2026-08-30**, before this document's own pull request merged, `main` was
`6632b029cee58feaa62736149a761f381388e323` — PR #14 (`docs: link operator manual`, main CI
`33233091419`), one docs-only commit ahead of the `v0.6.0` tag. That SHA is recorded as a dated
snapshot, not as a claim about where `main` points now; merging this PR alone moves it.

The durable point is the relationship, which survives the SHA going stale: **a checkout ahead of
the installed stable CLI is normal here.** Reason about what the CLI can do from
`hextap --version`, not from the working tree. For the current head:
`gh api repos/SijanC147/hextap-toolkit/commits/main --jq .sha`

## Adopter ledger

| | `hextap-toolkit` | `claude-rc-proxy` | `better-ccflare` |
|---|---|---|---|
| Current tag | `v0.6.0` | `v0.2.0` | `v3.9.0` |
| Tag commit | `9d1f6ef1ca365f83b118473d5bfcda416e7bf77c` | `dac8b2cf0f4e74d757892ffc6ae945cb10386e3d` | `3839c07d3901aa3dbfb228a7b1cf14f9d398eb0c` |
| Protected PR head CI | #13 `33232103954` | #8 `33235247808` | #42 `33178228054`, #36 `33044333046` |
| Merged-`main` CI | `33232339531` | `33235317382` | `33178383321` |
| Release run | `33232555139` | `33237547108` | `33179109842` (RC `33178706785`) |
| Release ID | `378869000` | `378892646` | `378533895` |
| Immutable | yes | yes | yes |
| Tap commit | `f8719fb0172a15ea31c88fbd62841fb2b96edd48` | `01de3a9c26779a514baf6b47b0f1409469514598` | `9f054fe0ab34cc06abddef56c9ab1c8fd1cf022c` |
| Exact tap CI run | `33232801566` | `33237683107` | `33179476413` |
| Installed version | `0.6.0` | `0.2.0` | `3.9.0` |
| Source rulesets | `21411731` / `21411735` | `21345746` / `21345761` | `21627513` / `21627514` |

`better-ccflare v3.9.0-rc.1` and `v3.9.0` were cut from the **same commit** `3839c07d…` — the
first clean RC-to-stable sequence, and the intended shape of the flow.

`hextap/main` and `hextap/release-tags` are `active` on all three repositories. They are the
**source** repositories' rulesets. Earlier notes recorded `21411731` / `21411735` as belonging to
the tap; they belong to `hextap-toolkit`, and the tap has none — see the tap gate below.

### The `v0.5.0` ordering

| Time (UTC, 2026-08-29) | Event |
|---|---|
| `00:18:40` | Release run `33223146605` starts. |
| `00:24:56` | It **fails** at the Homebrew boundary. |
| `00:32:39` | Tap PR #13 merges as `a9a96e62`, carrying the hand-authored Formula commit `895c1d3b`. |
| `00:34:48` | `homebrew-only` recovery `33223947922` starts. |
| `00:35:46` | It **passes** — against Formula bytes a human had already placed. |

That ordering matters and is easy to get backwards: the recovery run did not author the `0.5.0`
Formula, it validated one that was already there. Recording the recovery as having "published"
`0.5.0` would credit the automation with a step a person performed.

### Recovery precedent

`homebrew-only` dispatch completes the Homebrew stage without rebuilding or re-releasing, and has
run green three times. Each is a `workflow_dispatch` run in the **source** repository, not the tap
— querying the tap for them returns `404`.

The three are routinely cited together as evidence that the recovery path works. Traced
individually, **they are three different events**:

| Run | Version | What actually happened |
|---|---|---|
| `33223947922` | toolkit `v0.5.0` | Formula bytes were **already in the tap** from PR #13 (`895c1d3b`, hand-authored) before this run started. It validated them; it did not author them. |
| `33000535526` | claude-rc-proxy `v0.1.1` | The publisher **succeeded** — it pushed `00098656` at `18:19:08`, inside the failing run. Tap CI `32999041554` then failed, and the release run failed 6 seconds later because it was waiting on it. This run is a **retry**, not a re-publication. |
| `33051618673` | better-ccflare | Not a recovery at all. Stable run `33050987950` had already **succeeded** at `07:52:08`; this ran at `07:53:27` as the documented idempotent rerun. |

**`homebrew-only` has never authored a Formula into the tap.** In all three cases the bytes were
already there — placed by a person for `v0.5.0`, by the failing run's own publisher for
claude-rc-proxy `v0.1.1`, and by an entirely successful run for better-ccflare. All three runs
finished in **under 70 seconds** (58s, 65s, 61s), which is consistent with finding the tap already
current rather than performing work.

State the property precisely, because the loose version of this sentence has already misled this
project once:

> **Proven: idempotent `homebrew-only` re-validation, three times.**
> **Never exercised: republish-after-publisher-failure.**

"Recovery repeatedly proven" is the phrasing to avoid. It reads as the second property while only
the first has evidence.

### What failed in the claude-rc-proxy case — SB23-745

The `v0.1.1` failure is the one with a legible cause, and it is not the publisher. Tap run
`32999041554`, on the Formula commit the publisher had just pushed:

| Job | Conclusion |
|---|---|
| `test-bot (macos-15)` | **success** |
| `Published Formula gate` | **success** |
| `Claude RC proxy release tooling` | **failure**, at step `Test release payload and publication logic` |

The Formula published correctly and passed `brew test-bot`. An **unrelated job in the same tap
workflow** failed, which set the run's overall conclusion to failure, which the source release run
was waiting on — so the release failed at `Publish Formula and wait for tap CI` despite Homebrew
publication having worked.

That is this project's own founding error running backwards: a gate reading a *different* gate's
result as its own. `Publish Formula and wait for tap CI` waits on the tap run's aggregate
conclusion rather than on the jobs that actually evidence Formula publication.

The source-side job timings localise the fault further, and rule the push logic out:

| Time (UTC, 2026-08-26) | Event |
|---|---|
| `18:18:38` | `Publish Homebrew Formula` job starts. |
| `18:19:06` | Step 8, `Publish Formula and wait for tap CI`, starts. |
| `18:19:08` | Formula commit `00098656` lands in the tap — **2 seconds in**. The push succeeded. |
| `18:21:47` | The same step **fails**, after a further 2m 39s. |
| `18:21:50` | The job ends. |

So the publisher pushed successfully almost immediately and then died in the **wait and
tap-CI-correlation** phase — not in push, CAS, or retry. Whatever is wrong is downstream of
publication, in run discovery and result interpretation.

**No remedy is proposed here, deliberately.** An earlier draft of this section prescribed how the
wait step should be narrowed. That was a design change stated in an evidence index — the same
category error as keeping a roadmap in this file — and it would have relaxed the Tap gate's
requirement that tap CI pass, which `scripts/publish-homebrew.sh` enforces explicitly. Accepted
design belongs in `architecture.md` and `decisions.md`; proposed work belongs in Linear.

The evidence above is recorded here. The hypothesis, the argument about which axis may narrow and
which must not, and the caution against loosening run discovery are on **SB23-745**, where they can
be reviewed and decided. This is one instance and is **not** a called root cause — the toolkit
`v0.1.0` failures (`32980009852`, `32990280006`) and `v0.5.0`'s failure cause are still untraced to
job level.

For the shape of the underlying mistake: SB23-642 was one gate **overwriting** another's work.
This is one gate **reading** another's verdict. Both are gate conflation — here sitting inside the
publisher itself.

## Gate status — as of 2026-08-30

Each heading below states a **dated conclusion**, not a standing one. Conclusions move; the
identifiers under them do not. When SB23-737 lands, the tap heading is wrong and this file is
behind Linear — which is authoritative for current gate state. Re-derive before relying on any
verdict here; each section names the command that does it.

### Source — evidenced as of 2026-08-30

Protected PR head CI, protected merge, merged-`main` CI, and tag-to-commit identity are green
across all three repositories, with the run IDs in the adopter ledger above.

### Release — evidenced as of 2026-08-30

The full hosted matrix, hosted Windows proof, exact asset graph, attestations, immutable
publication, and stable/prerelease routing are all evidenced by the run and release IDs above.
Windows is proved through better-ccflare `33179109842`; the earlier Windows failure `33045627299`
was corrected by toolkit `v0.4.2` before that proof was accepted.

### Tap — publication evidenced; protection **absent as of 2026-08-30**

Publication is evidenced: three real tap commits, each with a green `brew test-bot` run on that
exact SHA.

Protection was not, on the date checked. `homebrew-hextap` `main` reported `protected: false` and
its rulesets endpoint returned `[]`. Re-derive rather than trusting this paragraph:

```sh
gh api repos/SijanC147/homebrew-hextap/branches/main --jq '{protected:.protected}'
gh api repos/SijanC147/homebrew-hextap/rulesets
```

To be precise about why that matters, since it is easy to overstate: the tap does **not** hold the
package bytes. A rendered Formula carries the release URL and its SHA-256
(`internal/formula/render.go`), and the assets themselves stay in the source repository's immutable
release. What the tap holds is the **download instruction** — which URL every consumer fetches and
which digest it is checked against.

That is not a smaller problem than "holding the bytes", it is a different one. Write access to the
tap does not let an attacker swap the payload behind a fixed checksum; it lets them repoint the URL
**and** supply a matching `sha256` in the same commit. Controlling both halves defeats checksum
verification entirely, because the checksum is only ever compared against the value the tap itself
supplies. The mitigation is therefore review and protection of tap commits, not stronger hashing.
Unprotected, this is the least protected repository in the platform and the most valuable one to
tamper with. → **SB23-737**

The credential used to push those tap commits is a broad classic PAT rather than a tap-scoped
one. → **SB23-736**

### Install — observed on one machine, and the one gate with no durable evidence

On 2026-08-30, `hextap 0.6.0` reported commit `9d1f6ef1ca365f83b118473d5bfcda416e7bf77c`, matching
the `v0.6.0` tag exactly, and `brew list --versions` reported `hextap 0.6.0`,
`claude-rc-proxy 0.2.0`, `better-ccflare 3.9.0`, with agent skill `1.3.0`.

**Read that as an observation, not as an identifier.** Every other gate in this file is anchored to
something a later reader can fetch — a run ID, a commit, a release ID. This one is not. Re-running
the install commands inspects *the reader's own machine now*; it cannot confirm that this gate
passed here on that date, and no transcript or artifact was captured that would. By the standard
the top of this document sets, that makes the install gate **unverified in the durable sense**,
however green it looked at the time.

The gap is worth closing rather than papering over: a hosted install check, or a stored transcript,
would give this gate an identifier like every other. Until then, treat it as the weakest row in the
file after runtime.

It is also an installation gate, not a distribution gate — it says nothing about a consumer without
tap access. → **SB23-751**

### Runtime — **not verified**

`brew services list` reports the `claude-rc-proxy` and `better-ccflare` LaunchAgents as `started`.
That is process registration, not health, and installing a keg does not restart a service. No
process was inspected and no request was served as evidence for this document. better-ccflare
runtime and database compatibility after the `v3.9.0` upgrade is **unknown, not disproven**.
→ **SB23-750**

Hextap deliberately does not own this gate. It manages package publication and installation; it
does not start, stop or restart services, apply or reverse migrations, or certify data
compatibility. Those are project-owned.

## Open defects

Ids only, with the priority and status they carried on 2026-08-30. **No titles or summaries** —
those are the part that goes stale silently while a status stays put, and duplicating them here
would rebuild the second source of truth this document exists to remove. Read the issue.

Even the priority and status columns below are a dated convenience, not authority. Get the current
list, with titles, from Linear:

```sh
# Platform issues — requires the Linear MCP tools or the Linear API
list_issues(project: "hextap", fields: ["id","title","priority","status"])
```

The same drift already happened once to the issue index in this project's agent memory, which was
written and wrong within the hour. It was deleted rather than corrected, for this reason.

### Platform — the `hextap` project

| Id | Priority | Status |
|---|---|---|
| SB23-680 | Urgent | In Progress |
| SB23-736 | Urgent | Todo |
| SB23-737 | High | Todo |
| SB23-739 | High | In Progress |
| SB23-742 | High | Todo |
| SB23-753 | High | In Progress |
| SB23-754 | High | Todo |
| SB23-755 | High | Todo |
| SB23-756 | High | Todo |
| SB23-738 | Medium | In Progress |
| SB23-740 | Medium | In Progress |
| SB23-741 | Medium | Backlog |
| SB23-743 | Medium | Backlog |
| SB23-745 | Medium | Backlog |
| SB23-746 | Medium | Todo |
| SB23-751 | Medium | Backlog |
| SB23-744 | Low | Backlog |
| SB23-747 | Low | Backlog |
| SB23-748 | Low | Backlog |
| SB23-749 | Low | In Progress |
| SB23-752 | None | Backlog |

### Cross-linked — owned by adopter projects

These are tagged `hextap:adopter` and stay visible from the platform side, but they are **not**
platform work and must not be pulled into the platform backlog.

| Id | Project | Priority | Status |
|---|---|---|---|
| SB23-314 | `better-ccflare` | Medium | Backlog |
| SB23-750 | `better-ccflare` | Medium | Backlog |
| SB23-713 | `claude-peers` | Low | — |

### Documentation surfaces

**GitBook — published and public.** GitBook project `hextap-toolkit` (workspace SB23,
`site_qHQ9P`) is connected by Git Sync to `./docs` and serves
**<https://sb23.gitbook.io/hextap-toolkit/>**, which returns HTTP `200` unauthenticated. The
`GitBook (./docs)` check runs on every pull request, so sync is gated rather than assumed — which
is what makes this URL safe to record here.

The Operator Manual Codex Site is a **separate** surface from that GitBook site, and is not in the
same state — see below.

### Unverified surfaces

Absence of evidence, recorded as such rather than as failure:

- **`doctor --online`** — local `gh` authentication is currently **working** (`SijanC147`, scopes
  `gist`, `read:org`, `repo`, `workflow`), so authentication is no longer the obstacle it was when
  SB23-742 was filed. Run against this repository it now fails with `doctor: caller workflow lacks
  an exact stable toolkit version and full SHA pin` — that is SB23-739, the self-caller false
  negative, **not** a real drift finding. An authenticated transcript against an *external* adopter
  is still uncaptured.
- **The Operator Manual Codex Site** — an unauthenticated request returns HTTP `401`. That may be
  intentional; the access model has never been decided or written down. → **SB23-746**
- **WebMCP** — only ever exposed `ask_hextap_manual`, and it has not been discovered from recent
  clients. Not being discovered is not proof the Site stopped exposing it.
- **Required-secret inventory and the immutable-release repository setting** — functionally
  exercised by every release above, but the settings themselves are unverified.

An empty result is not a negative result. `hextap status --json` reporting an empty
`projects`/`formulae` array beside an ownership warning is SB23-738 failing to resolve ownership —
it is **not** evidence that those objects do not exist.

## Reverifying

Every identifier in this document is checkable. None of these commands mutate anything.

```sh
# Releases, immutability, and tag-to-commit identity
gh release list --repo SijanC147/hextap-toolkit --limit 20
gh api repos/SijanC147/hextap-toolkit/releases \
  --jq '.[]|"\(.tag_name) id=\(.id) immutable=\(.immutable)"'
gh api repos/SijanC147/hextap-toolkit/tags --jq '.[]|"\(.name) \(.commit.sha)"'

# Any run ID in this file: name, trigger, conclusion, and the commit it ran on.
# Substitute the repository that owns the run — a run ID is only valid in its own
# repository, and asking the wrong one returns a 404 that looks like a missing run.
#   source runs  -> hextap-toolkit | claude-rc-proxy | better-ccflare
#   tap CI runs  -> homebrew-hextap
gh api repos/SijanC147/<REPO>/actions/runs/<RUN_ID> \
  --jq '"\(.name) | \(.head_branch) | \(.event) | \(.conclusion) | \(.head_sha)"'

# Worked examples, one of each
gh api repos/SijanC147/hextap-toolkit/actions/runs/33232555139 --jq '.conclusion'   # toolkit release
gh api repos/SijanC147/claude-rc-proxy/actions/runs/33237547108 --jq '.conclusion'  # adopter release
gh api repos/SijanC147/homebrew-hextap/actions/runs/33237683107 --jq '.conclusion'  # tap CI

# Job-level detail, which is what distinguishes "the Formula failed to publish"
# from "an unrelated job failed and took the run's conclusion with it"
gh api repos/SijanC147/<REPO>/actions/runs/<RUN_ID>/jobs \
  --jq '.jobs[]|"\(.name): \(.conclusion)"'

# Tap publication, and tap protection
gh api repos/SijanC147/homebrew-hextap/commits/f8719fb0 --jq '.commit.message'
gh api repos/SijanC147/homebrew-hextap/branches/main --jq '{protected:.protected}'
gh api repos/SijanC147/homebrew-hextap/rulesets

# Source protection.
# Listing id/name/enforcement is NOT sufficient: conditions, protected targets,
# required checks and bypass actors can all drift while the name and enforcement
# stay identical, leaving the gate looking green. Fetch each ruleset's detail
# endpoint and compare the fields that actually confer protection, as
# internal/onboard's validateOnlineRulesets does.
gh api repos/SijanC147/hextap-toolkit/rulesets --jq '.[]|"\(.id) \(.name) \(.enforcement)"'
gh api repos/SijanC147/hextap-toolkit/rulesets/21411731 \
  --jq '{target,enforcement,conditions,bypass_actors,rules:[.rules[]|{type,parameters}]}'
# `target` matters: a ruleset drifting between branch and tag protection is
# invisible if you compare only conditions and rules.

# Install gate
hextap --version
brew list --versions hextap claude-rc-proxy better-ccflare
```

Recovery runs live in the **source** repository, not the tap. Querying the tap for them returns
`404`, and that `404` means the wrong repository was asked, not that the recovery never happened.

### Linear state

The GitHub commands above cover the release, tap and install identifiers. They do **not** cover the
defect list, which is Linear's, and no `gh` command can. Use the Linear MCP tools or the Linear API:

```
list_issues(project: "hextap")          # current platform issues, priorities, statuses
get_issue("SB23-745")                   # one issue, with its full description and comments
list_comments(issueId: "SB23-745")      # the worklog, including evidence posted by agents
```

Cross-linked adopter issues live in the `better-ccflare` and `claude-peers` projects, not in
`hextap` — `list_issues(project: "hextap")` will not return them.

## Maintaining this file

Add a row when a release ships. Move a gate's paragraph when its evidence changes. Remove an entry
from [Open defects](#open-defects) when Linear closes the issue, not when it feels resolved.

Do not add narrative status. If something cannot be written as an identifier or as an explicit
"unverified", it belongs in Linear, where it can be assigned and closed — not here, where it will
quietly rot and mislead the next session that reads it.
