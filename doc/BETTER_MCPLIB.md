# Making `mcplib` Better: what official `go-sdk` does better and what to change

## Executive takeaways

`mcplib` works for nsmcp today, but it falls behind official `go-sdk` in the areas that most affect long-term stability:

1. HTTP transport correctness and security defaults
2. Structured auth and authorization handling
3. Prompt contract clarity and compatibility
4. Protocol feature completeness (capabilities/lifecycle/utilities)
5. Maintainable architecture and test coverage

The most relevant problems to fix first are transport+security+auth. Prompt compatibility is the next highest risk.

---

## 1) Transports: where `go-sdk` is better

## What `go-sdk` does better

- Provides modern streamable HTTP transport (`NewStreamableHTTPHandler`) with explicit session lifecycle.
- Keeps SSE as a separate legacy transport (`NewSSEHandler`) instead of mixing concerns.
- Enforces clearer HTTP semantics for methods/headers/content types.
- Uses clean server abstractions (`Run`, `Connect`, transport interfaces).

## Current `mcplib` limitations

- Transport modes are centralized in a custom path (`ListenAndServe`) and tightly coupled to server internals.
- HTTP/SSE semantics are custom and can drift from current MCP expectations.
- Session management is less structured than streamable HTTP behavior.

## Changes needed in `mcplib`

1. Split transports into explicit modules (`stdio`, `tcp`, `http_streamable`, `sse_legacy`).
2. Add streamable HTTP-compatible behavior (session-id lifecycle, method semantics, stricter headers).
3. Introduce a transport boundary interface so wire concerns are decoupled from core MCP method handling.
4. Keep compatibility mode for legacy behavior but make strict mode the default.

Priority: **Critical**

---

## 2) Security defaults: where `go-sdk` is better

## What `go-sdk` does better

- Streamable HTTP includes protections for localhost abuse patterns and cross-origin checks.
- Security-sensitive behavior is explicit and configurable.

## Current `mcplib` limitations

- Security behavior is mostly implicit in custom handler logic.
- Safer-by-default HTTP hardening is limited and not centralized.

## Changes needed in `mcplib`

1. Add a centralized `SecurityOptions` policy for HTTP serving.
2. Include toggles for host/origin/content-type/accept checks.
3. Enable secure defaults; require explicit opt-out for compatibility exceptions.
4. Add tests for malicious/malformed requests (host spoofing, wrong origin/content-type/session).

Priority: **Critical**

---

## 3) Auth and token handling: where `go-sdk` is better

## What `go-sdk` does better

- Uses explicit bearer auth middleware (`RequireBearerToken`).
- Propagates structured token metadata (`TokenInfo`: scopes, expiration, user id, extra fields).
- Makes session/user consistency checks possible and straightforward.

## Current `mcplib` limitations

- Auth callback support exists but policies are less explicit.
- HTTP behavior can be permissive unless carefully configured.
- Token metadata model is narrower than what modern auth/authorization workflows need.

## Changes needed in `mcplib`

1. Adopt middleware-first auth flow before request processing.
2. Expand `AuthResult` to include `UserID`, `Scopes`, `ExpiresAt`, and extension fields.
3. Add explicit auth policies:
   - token required vs optional
   - required scopes
   - session-user binding
4. Standardize auth error responses and optional `WWW-Authenticate` behavior.

Priority: **Critical**

---

## 4) Tools and validation: where `go-sdk` is better

## What `go-sdk` does better

- Typed tool registration path (`AddTool`) with schema inference and validation support.
- Cleaner distinction between protocol errors and tool execution errors.

## Current `mcplib` limitations

- Validation is mostly manual/passthrough.
- Tool response modes are flexible, but contract consistency depends on each handler.
- No first-class typed registration API to reduce runtime shape errors.

## Changes needed in `mcplib`

1. Add typed tool registration API (`RegisterTypedTool[TIn, TOut]`).
2. Add optional strict input/output schema validation per tool.
3. Standardize tool error mapping (`isError` content vs protocol-level errors).
4. Add schema caching to avoid repeated expensive schema work.

Priority: **High**

---

## 5) Prompts model: where `go-sdk` is better (and where `mcplib` is custom)

## What `go-sdk` does better

- Prompt lifecycle is integrated with capability signaling and broader spec-consistent server behavior.

## Current `mcplib` limitations

- `prompts/apply` is custom and non-standard.
- `map[string]any` argument flexibility is useful, but compatibility boundaries are unclear.
- Custom templating (`#if`, `#each`) is powerful but under-specified from a protocol standpoint.

## Changes needed in `mcplib`

1. Define two explicit prompt modes:
   - `SpecStrict` (protocol-aligned behavior)
   - `Extended` (current rich templating behavior)
2. Version and document `prompts/apply` as an extension endpoint if retained.
3. Harden template parsing/rendering with deterministic behavior and clearer errors.
4. Add compatibility tests for all prompt patterns currently used in `src/prompts/*.md`.

Priority: **High**

---

## 6) Protocol completeness and lifecycle: where `go-sdk` is better

## What `go-sdk` does better

- Broader capability management and inference.
- Better lifecycle handling around initialize/initialized/session handling.
- Built-in extension points for middleware and operational concerns.

## Current `mcplib` limitations

- Capability handling is comparatively minimal.
- Lifecycle behavior is simpler, but less robust for mixed clients and evolving MCP semantics.

## Changes needed in `mcplib`

1. Expand capability declaration/inference from registered features.
2. Tighten initialization state transitions and invalid-order request handling.
3. Add optional utility surfaces where relevant (progress/logging/ping).

Priority: **Medium/High**

---

## 7) Maintainability: where `go-sdk` is better

## What `go-sdk` does better

- Better separation of concerns.
- Clearer internal boundaries and more testable components.
- More examples and mature edge-case handling.

## Current `mcplib` limitations

- Large files with mixed responsibilities (`lib.go`, `http.go`) increase regression risk.
- Internal APIs are less modular for isolated testing and incremental upgrades.

## Changes needed in `mcplib`

1. Split by responsibility (server core, transports, auth/context, prompts renderer, protocol methods).
2. Introduce small internal interfaces for auth verifier, session manager, and renderer.
3. Expand tests beyond happy path:
   - transport conformance
   - auth policy matrix
   - prompt rendering fixtures
   - protocol ordering/lifecycle checks

Priority: **High**

---

## Prioritized roadmap to improve `mcplib`

## Phase 1 (must-do)

- Transport hardening and streamable-compatible HTTP behavior
- Security defaults and policy controls
- Structured auth middleware + richer token metadata

## Phase 2 (high impact)

- Typed tool API + schema validation
- Prompt contract split (`SpecStrict` vs `Extended`) and template hardening
- Capability inference improvements

## Phase 3 (stability and future-proofing)

- Lifecycle/utilities polish (progress/logging/ping as needed)
- Architectural refactor for modularity
- Expanded compatibility and regression tests

---

## Bottom line

If `mcplib` stays, it should evolve around transport correctness, security defaults, and structured auth first; these are the highest-value improvements and the biggest current gaps versus official `go-sdk`.

If long-term MCP alignment is a priority, the same gaps also justify eventual migration to official `go-sdk` as the lower-maintenance foundation.
