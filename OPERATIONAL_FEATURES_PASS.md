
Operational features pass

Added:
- proper pagination on the opportunities screen
- page-size and sorting persistence in filter flow
- queue visibility summary cards and retry controls
- audit log page and audit entry persistence
- workflow history persistence and detail rendering
- tender/extraction detail page
- stronger empty/success/error presentation

Storage additions:
- audit_entries
- workflow_events

New pages:
- /audit-log
- /tenders/{id}

Key caveat:
This is a best-effort repo implementation pass in the sandbox. It still needs real local verification with:
- go mod tidy
- go test ./...
- docker compose up --build
