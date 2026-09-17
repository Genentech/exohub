-- adb-standalone SurrealDB schema (idempotent)

-- ── documents ──────────────────────────────────────────────────────────
DEFINE TABLE IF NOT EXISTS document SCHEMALESS;

DEFINE FIELD IF NOT EXISTS _extra            ON document TYPE object;
DEFINE FIELD IF NOT EXISTS _extra.project_id ON document TYPE string;
DEFINE FIELD IF NOT EXISTS _extra.version    ON document TYPE string;
DEFINE FIELD IF NOT EXISTS _extra.latest     ON document TYPE bool    DEFAULT false;
DEFINE FIELD IF NOT EXISTS _extra.type       ON document TYPE string;
DEFINE FIELD IF NOT EXISTS _extra.permissions.read_access  ON document TYPE string;
DEFINE FIELD IF NOT EXISTS _extra.permissions.write_access ON document TYPE string;
DEFINE FIELD IF NOT EXISTS search_text       ON document TYPE string;

DEFINE INDEX IF NOT EXISTS doc_project ON document FIELDS _extra.project_id;
DEFINE INDEX IF NOT EXISTS doc_pv      ON document FIELDS _extra.project_id, _extra.version;
DEFINE INDEX IF NOT EXISTS doc_latest  ON document FIELDS _extra.project_id, _extra.latest;
DEFINE INDEX IF NOT EXISTS doc_read    ON document FIELDS _extra.permissions.read_access;

-- ── full-text search (BM25) ────────────────────────────────────────────
DEFINE ANALYZER IF NOT EXISTS adb_txt TOKENIZERS class FILTERS lowercase, ascii, snowball(english);
DEFINE INDEX IF NOT EXISTS doc_fts ON document FIELDS search_text FULLTEXT ANALYZER adb_txt BM25(1.2,0.75) HIGHLIGHTS;

-- ── scroll sessions ────────────────────────────────────────────────────
DEFINE TABLE IF NOT EXISTS scroll_session SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS query      ON scroll_session TYPE object FLEXIBLE;
DEFINE FIELD IF NOT EXISTS cursor     ON scroll_session TYPE int;
DEFINE FIELD IF NOT EXISTS total      ON scroll_session TYPE int;
DEFINE FIELD IF NOT EXISTS expires_at ON scroll_session TYPE datetime;

-- ── schema registry ────────────────────────────────────────────────────
DEFINE TABLE IF NOT EXISTS schema_type SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS name ON schema_type TYPE string;
DEFINE FIELD IF NOT EXISTS json ON schema_type TYPE object FLEXIBLE;

-- ── knowledge graph: entities ────────────────────────────────────────────
DEFINE TABLE IF NOT EXISTS entity SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS entity_id   ON entity TYPE string;
DEFINE FIELD IF NOT EXISTS name        ON entity TYPE string;
DEFINE FIELD IF NOT EXISTS type        ON entity TYPE string;
DEFINE FIELD IF NOT EXISTS summary     ON entity TYPE string;
DEFINE FIELD IF NOT EXISTS source_docs ON entity TYPE array DEFAULT [];
DEFINE INDEX IF NOT EXISTS entity_name ON entity FIELDS name UNIQUE;

-- ── knowledge graph: relationships ───────────────────────────────────────
DEFINE TABLE IF NOT EXISTS relationship SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS rel_id    ON relationship TYPE string;
DEFINE FIELD IF NOT EXISTS subject   ON relationship TYPE string;
DEFINE FIELD IF NOT EXISTS predicate ON relationship TYPE string;
DEFINE FIELD IF NOT EXISTS object    ON relationship TYPE string;
DEFINE INDEX IF NOT EXISTS rel_subject ON relationship FIELDS subject;
DEFINE INDEX IF NOT EXISTS rel_object  ON relationship FIELDS object;
