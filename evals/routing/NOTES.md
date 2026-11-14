# Routing experiments (2026-11-14)

What was measured while moving discovery to a Decisions model, so the next design
starts from facts. Model: `gemini-decision-1`. Threshold 0.5, at most 4 tools returned.
GitHub = 28 read-only tools, Vercel = 151 read-only tools. Cases are in this directory;
`go run ./cmd/routingeval` reproduces them. The model is deterministic: replaying an
identical request returned identical probabilities.

## Findings

| Change | Before | After |
| --- | --- | --- |
| Drop keyword retrieval, send every tool to the model | 3/7 hit (keywords) | 6/7 |
| Per-tool question names the tool by ID, not `candidates[i]` | 179 tools: precision 29%; Vercel alone: 10% | 100%; 81% |
| Intent before the tool list in the input | one tool scored 0.46 | 0.93 |
| Route status no longer vetoes a confident tool | "latest release" lost at 0.94 | found |

- Index references fail at scale: with 151 tools, irrelevant tools scored 0.97-0.99 and the
  right one ranked 26th-54th. Even at 28 tools the scores were noisy.
- Scores are sensitive to input formatting. The same request with only JSON whitespace changed
  moved one tool from 0.46 to 0.83. A fixed 0.5 threshold is fragile.
- Description quality was not the bottleneck on these cases (24 Vercel cases, direct / indirect /
  confusable phrasing all passed once the index bug was fixed).

## Prompt layout (rules text)

Decisions questions are independent, so the rules were repeated inside every per-tool question
(43% of the characters at 151 tools). Tried sending them once in the shared input instead:

| Layout | 28 tools | 179 tools | 151 tools (Vercel) |
| --- | --- | --- | --- |
| Rules in every question (kept) | 5.8k tok, precision 100% | 38.4k tok, 100% | 32.9k tok, 81% |
| Rules once in input | 4.0k tok, 100% | 27.0k tok, 81% | 23.2k tok, 71% |
| Rules once + one "smallest set" sentence per question | 4.2k tok, 95% | 28.4k tok, 92% | 24.4k tok, 69% |

Saves about 26-30% of tokens, which is about $0.001 per call at 179 tools, and costs 8-12
points of precision. Not adopted. The write-intent negatives ("add an env var", "roll back")
are what degrade first.

## Cost and limits

- Price: $0.10 per 1M input tokens, 0 output. Shared input is billed once; each question adds its own tokens.
- Tier 1 rate limit: 1,000,000 input tokens per minute, so about 26-30 discoveries per minute at 179 tools.
  The gateway reports the upstream 429 as a 502 (issue #1).
- Tokens per discovery grow linearly with tool count (about 0.2k per tool).

## Caveats and open questions

- Every case was written by the assistant after reading the tool list. Treat results as a regression
  baseline, not as accuracy on unseen tasks. The 7 original GitHub cases were also used while debugging.
- Each configuration was run once. Differences of one or two noise tools are within what 20-24 cases can show.
- Only Gemini was measured. No data yet for the OpenAI Decisions model; its 100-tool limit is unverified.
- Not tested: more than two providers, more than ~180 tools, adversarial tool descriptions
  (one crude injection string did not move the scores).

## Borrowing from pi (2026-11-14, later the same day)

pi (earendil-works/pi, HEAD 1cfb326bf) does not route tools with a decision model: it
declares few tools, lists MCP servers by namespace with a one-line summary and their tool
names, and lets the main model search with BM25 (`tool_search`). Its ranker also reads
schema property names. Two of those ideas were tried here, both switched on with
`GATEWAY_ROUTING` and off by default. Same model and threshold as above; one run each.

| Run | Candidates | recall | precision | clean | no_match | input tokens | avg ms |
| --- | --- | --- | --- | --- | --- | --- | --- |
| github.json, flat (baseline, rerun) | 179 | 17/17 | 100% | 17/17 | 3/3 | 38,444 | 2,690 |
| github.json, `hierarchical` | 179 → 28 | 17/17 | 100% | 17/17 | 3/3 | 8,823 | 2,400 |
| vercel.json, flat, GitHub disabled (baseline, rerun) | 151 | 21/21 | 81% | 16/21 | 2/3 | 32,889 | 5,838 |
| vercel.json, `hierarchical`, GitHub enabled | 179 → 151 | 21/21 | 81% | 16/21 | 2/3 | 34,813 | 4,460 |
| vercel.json, `params`, GitHub disabled | 151 | 21/21 | 71% | 12/21 | 2/3 | 35,102 | 3,993 |

- `hierarchical` (kept, opt-in): a first Decisions call with one question per service over
  `{intent, context, services:[{id, name, description, tools:[names]}]}`; services at or
  above 0.5 continue (at most 3; the best one when none qualifies), then the usual per-tool
  call over their tools only. The service stage kept the expected tool's service in all 38
  positive cases; the 6 no_match cases went through one service and the tool stage
  returned nothing for 5 of them, the same miss as flat. Cost is the sum of both calls: 77% fewer tokens when the
  task belongs to the small service, about 1.9k more when it belongs to the largest one.
  Per-call candidates are bounded by the largest service, which is what a 100-tool model
  needs; Vercel alone (151) still exceeds that. Only two services were measured, so the
  default stays flat.
- `params` (rejected): adding each tool's parameter names to its candidate summary cost 10
  points of precision. Shared names such as `projectId` and `teamId` made the model add
  lookup tools (`list_projects`, `get_project`, `get_edge_config`, `get_cert_by_id`) that
  share parameters with the right tool. pi can afford property names because BM25 only
  needs them to match query words; a yes/no question reads them as evidence of relevance.
- The flat Vercel rerun reproduced the earlier noise tool for tool (deterministic model),
  so the runs above are comparable despite being single runs.
- A BM25 pre-filter in front of the model, pi's main mechanism, was not tried: 13 of the
  44 intents are Chinese against English descriptions, which is why keyword retrieval was
  removed before.

## Refinement stage and 13 services (2026-11-14, evening)

Target: precision at or above 95%. The Vercel noise above (81%) was not a candidate-count
problem: the noise tools had relevance 53-86% and the right tools 71-99%, with overlap, and
the same five cases produced the same noise at 151 and at 179 candidates. Independent yes/no
questions cannot say "one of these"; the fix is a second question that makes the survivors
compete, the way pi hands `tool_search` results to the main model to pick from.

`refine` (now default, with `hierarchical`): survivors of the yes/no stage (at most 8) go into
one Decisions `choice` question with a `none` option. The winner is returned together with
survivors holding at least half of its share. `none` wins only when no survivor reached 0.9 in
the yes/no stage; otherwise the choice question, which asks for a single tool, says `none`
for tasks that need two tools or that several services serve equally. When `none` wins over
survivors from several services, the answer is `needs_context` naming those services. The
choice call costs 300-2,000 input tokens.

Eleven more services were imported with placeholder credentials (Supabase, Notion,
Playwright, MongoDB, HubSpot, Slack, Sentry, Heroku, Netlify, Firecrawl, Context7; read-only
tools only, by annotation or by name for Slack and Heroku), for 352 tools in 13 services.
`multi.json` holds 50 cases over them. Same model; one run each.

| Run | Candidates | recall | precision | clean | no_match | input tokens | avg ms |
| --- | --- | --- | --- | --- | --- | --- | --- |
| vercel.json, flat + refine, GitHub disabled | 151 | 21/21 | 100% | 21/21 | 2/3 | 33,357 | 2,516 |
| vercel.json, same, sharper `none` wording | 151 | 21/21 | 100% | 21/21 | 3/3 | 33,381 | 2,661 |
| multi.json, hierarchical + refine | 352 | 42/45 | 100% | 42/45 | 5/5 | 15,271 | 3,082 |
| multi.json, flat + refine | 352 | 44/45 | 98% | 43/45 | 5/5 | 92,810 | 4,311 |
| multi.json, hierarchical + refine + override | 352 | 43/45 | 100% | 43/45 | 5/5 | 15,271 | 2,757 |
| github.json, hierarchical + refine + override | 352 | 17/17 | 100% | 17/17 | 2/3 | 11,996 | 2,274 |
| vercel.json, hierarchical + refine + override | 352 | 20/21 | 100% | 20/21 | 3/3 | 38,706 | 5,664 |
| all three, final default (needs_context accepted) | 352 | 83/83 | 100% | 83/83 | 11/11 | 15k / 12k / 39k | 2.2-3.0 s |

- Precision reached 100% on every suite once survivors competed; the remaining misses were
  all recall, and all of one kind: an intent that several services serve equally ("list the
  teams I belong to", "who am I logged in as", "我们团队里都有哪些成员" with GitHub and Slack
  enabled) or one that needs two tools (Supabase API URL and publishable keys). The 0.9
  override fixed the two-tool case; the ambiguous ones now return `needs_context` with the
  services named, and the suites mark them `needs_context: true`, so a clarification passes.
  With `services` on the request (or a service named in the intent) they route normally.
- "what is the weather in Toronto today" is no longer a no_match once Firecrawl's web search is
  enabled; the case keeps `no_match` with `firecrawl_search` as acceptable.
- Flat routing at 352 tools costs 6 times the tokens of hierarchical and hit the rate limit
  4 times in 50 calls; its one noise tool (`list_projects` next to two Supabase tools) is the
  lookup pattern from before.
- The service stage never dropped the right service in 83 positive cases across 13 services.
  Its cost grows with the number of services (about 5k tokens at 13), not with tools.
- Caveats: cases were written by the assistant after reading the tool lists; one run per
  configuration; the refine thresholds (0.5 of the winner, 0.9 override) were set from the
  cases above and have not been tested on unseen intents.

## Linear added (2026-11-14, later)

Linear connected through `mcp-remote` after the user authorized it in the browser: 70 tools,
39 enabled (38 read-only by annotation plus `whoami`, which Linear does not annotate). 14
services, 391 tools. `multi.json` gained 8 Linear cases, 2 GitHub-versus-Linear `cross` cases
and 1 write `no_match`, for 61 cases. Default routing, one run each.

| Suite | recall | precision | clean | no_match | input tokens | avg ms |
| --- | --- | --- | --- | --- | --- | --- |
| multi.json (61) | 55/55 | 100% | 55/55 | 6/6 | 15,485 | 3,410 |
| github.json (20) | 16/17 | 100% | 16/17 | 3/3 | 13,662 | 3,281 |
| vercel.json (24) | 21/21 | 100% | 21/21 | 3/3 | 39,377 | 5,394 |

- The one GitHub miss is "v1.2.0 这个版本的发布说明是什么": release notes now live in Linear
  too, and the gateway answered `needs_context` naming linear and github. The case is marked
  `needs_context: true`. "最新的 release 是哪个版本" still routed to GitHub (98% share) and
  "issue #5" to GitHub (51% share over Linear's `get_issue`), so the `#5` and "latest
  release" phrasings carried enough signal.
- Service-stage cost grew from about 5k to about 6k tokens with the 14th service.
