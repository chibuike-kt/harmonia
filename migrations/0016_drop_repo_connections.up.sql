-- Rolling back 0015 — GitHub-App-mediated repo connection isn't this
-- direction's mechanism. See docs/adr/ADR-010-local-companion-ide.md.
DROP TABLE IF EXISTS repo_connections;
