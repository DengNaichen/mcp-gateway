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
