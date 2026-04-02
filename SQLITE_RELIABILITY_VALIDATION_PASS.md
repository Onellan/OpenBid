SQLite reliability validation pass

What was added:
- stronger SQLite startup pragmas
- schema metadata and user_version validation
- runtime validation helper
- backup script
- restore script
- validation script
- JSON assumption audit script
- queue deduplication test
- concurrent write test
- seeded startup test

What was tightened:
- SQLite now uses:
  - WAL mode
  - busy_timeout
  - foreign_keys ON
  - synchronous NORMAL
  - max open conns = 1
  - max idle conns = 1

Why this matters:
- safer app/worker coexistence
- more predictable single-file DB behavior
- explicit validation of startup state
- clearer operational path for backup and recovery

Environment caveat:
This sandbox still cannot honestly claim a fully executed `go test ./...` result for the SQLite-backed repo because external module resolution for the Go SQLite driver is not reliable here. The code and tests were added, but real validation still needs to run on your machine.
