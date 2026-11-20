claude-ok.ndjson: a real `claude -p --output-format stream-json --verbose --include-partial-messages`
run (Claude Code 2.1.329, haiku, 2026-11-20) against the -demo MCP server: one tool call, then the answer.

claude-limit.ndjson: made up, NOT recorded. What a run that hits the 5-hour limit prints is not known
yet; this follows the recorded rate_limit_event shape with status "rejected". Replace it with a real
recording once a limit is hit (the runner logs the result line).
