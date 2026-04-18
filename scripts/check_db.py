import sqlite3, json, sys

db = sys.argv[1] if len(sys.argv) > 1 else "ProductionDeployment/runtime/data/local-smoke.db"
conn = sqlite3.connect(db)
tables = conn.execute("SELECT name FROM sqlite_master WHERE type='table'").fetchall()
print("Tables:", [t[0] for t in tables])
for table_name in [t[0] for t in tables]:
    count = conn.execute(f"SELECT COUNT(*) FROM {table_name}").fetchone()[0]
    print(f"  {table_name}: {count} rows")
    if table_name == "users" and count > 0:
        rows = conn.execute(f"SELECT id, payload FROM {table_name}").fetchall()
        for row in rows:
            try:
                p = json.loads(row[1])
                print(f"    email={p.get('email')} role={p.get('role')}")
            except Exception:
                print(f"    raw: {row[1][:100]}")
conn.close()
