Default operating context for repository bootstrap work:

- Choose intentionally boring, maintainable defaults unless the issue specifies otherwise.
- Keep scaffolding light; do not add large frameworks or infrastructure without an explicit reason in the issue.
- Prefer a project structure that makes follow-up backend, frontend, and verification issues easy to implement.
- Prefer writing or replacing human-facing docs and source files before running install commands.
- Verification should prove the required slice of work and then exit. If a server must run, test the exported app/module instead of starting a permanent listener during tests.
- When blocked by ambiguity, ask a short, specific question. If no answer is available, document the blocker and stop cleanly.
