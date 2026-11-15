# Client setup implementation

- [x] Ground: traced desktop/bootstrap/MCP wiring and checked all five client formats.
- [x] Sketch: desktop-local Manager with Statuses / Write / Revoke; per-client receipts and conservative conflict handling.
- [x] Agree: user authorized global config and one Gateway endpoint for five clients.
- [x] Implement: format adapters, native page, write ownership, local verification socket and rerunnable checker.
- [x] Verify / revise: real client reading/connection, native button interaction, unit regression and app build. Evidence and limitations: verification.md.

The agreed design was already fixed, so arena's exception for an already determined design applied. Local file mutation belongs to the desktop host, not a remotely hosted gateway HTTP endpoint. Configuration status remains distinct from MCP connectivity.
