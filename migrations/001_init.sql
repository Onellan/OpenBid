-- Starter production migration path when replacing the current file-backed store with SQLite/PostgreSQL.
create table if not exists tenders (
  id text primary key, source_key text not null, external_id text not null, title text not null, issuer text not null,
  province text, category text, tender_number text, published_date text, closing_date text, status text, cidb_grading text,
  summary text, original_url text, document_url text, engineering_relevant boolean default false, relevance_score real default 0,
  document_status text, excerpt text, extracted_facts_json text, created_at text not null, updated_at text not null
);
create unique index if not exists ux_tenders_source_external on tenders(source_key, external_id);
