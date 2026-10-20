// Package tier2 holds no code. Its test runs openits-models' own Tier 2
// conformance checks (tools/conformance/tests) against the catalog events a
// live collector publishes. It lives under internal/wire because that is the
// only place allowed to import openits-models (ADR 0002), and the point is to
// run the harness's checks rather than a transcription of them.
package tier2
