-- tildoc schema. Development mode: this file is the whole schema; breaking
-- changes are folded in here until the first release.
--
-- Invariants live in the database: names, closed value sets, space
-- membership of every entry (composite foreign keys), one name per folder,
-- the document head (current version) and the trash state.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- valid_name: a file, folder or collection name (NFC, no '/', no control
-- characters, not "." or "..", at most 255 bytes).
CREATE FUNCTION valid_name(n text) RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT n <> '' AND n IS NFC NORMALIZED AND n NOT IN ('.', '..') AND strpos(n, '/') = 0
       AND octet_length(n) <= 255 AND n !~ '[\x01-\x1f\x7f-\u009f]'
$$;

-- ---------------------------------------------------------------- instance
-- Exactly one row: the identity and server-wide settings of this instance.
CREATE TABLE instance_settings (
    singleton      boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    instance_id    uuid NOT NULL DEFAULT uuidv7(),
    ocr_preference text NOT NULL DEFAULT 'existing' CHECK (ocr_preference IN ('existing', 'tildoc')),
    backup_keep    integer NOT NULL DEFAULT 3 CHECK (backup_keep BETWEEN 1 AND 100),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
INSERT INTO instance_settings DEFAULT VALUES;

-- ---------------------------------------------------------------- identity
CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    username      text NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9][a-z0-9._-]{1,31}$'),
    display_name  text NOT NULL DEFAULT '',
    password_hash text NOT NULL,
    is_admin      boolean NOT NULL DEFAULT false,
    disabled      boolean NOT NULL DEFAULT false,
    pdf_viewer    text NOT NULL DEFAULT 'pdfjs' CHECK (pdf_viewer IN ('pdfjs', 'native')),
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token_hash   bytea PRIMARY KEY CHECK (length(token_hash) = 32),
    user_id      uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE teams (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    name       text NOT NULL UNIQUE CHECK (btrim(name) = name AND name <> ''),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE team_members (
    team_id    uuid NOT NULL REFERENCES teams ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id)
);
CREATE INDEX team_members_user ON team_members(user_id);

CREATE TABLE spaces (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    slug          text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$' AND strpos(slug, '--') = 0
                                              AND slug NOT IN ('api', 'dav')),
    name          text NOT NULL CHECK (btrim(name) = name AND name <> ''),
    owner_user_id uuid REFERENCES users ON DELETE CASCADE,
    owner_team_id uuid REFERENCES teams ON DELETE CASCADE,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CHECK ((owner_user_id IS NULL) <> (owner_team_id IS NULL))
);
CREATE INDEX spaces_owner_user ON spaces(owner_user_id);
CREATE INDEX spaces_owner_team ON spaces(owner_team_id);

-- A space can additionally be shared with single users or whole teams.
-- Grants never confer ownership.
CREATE TABLE space_grants (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    space_id   uuid NOT NULL REFERENCES spaces ON DELETE CASCADE,
    user_id    uuid REFERENCES users ON DELETE CASCADE,
    team_id    uuid REFERENCES teams ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('viewer', 'member', 'admin')),
    created_by uuid REFERENCES users ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((user_id IS NULL) <> (team_id IS NULL))
);
CREATE UNIQUE INDEX space_grants_user ON space_grants(space_id, user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX space_grants_team ON space_grants(space_id, team_id) WHERE team_id IS NOT NULL;
CREATE INDEX space_grants_user_lookup ON space_grants(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX space_grants_team_lookup ON space_grants(team_id) WHERE team_id IS NOT NULL;

CREATE FUNCTION role_rank(r text) RETURNS integer LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE r WHEN 'owner' THEN 4 WHEN 'admin' THEN 3 WHEN 'member' THEN 2 WHEN 'viewer' THEN 1 ELSE 0 END
$$;

-- space_role: the strongest role a user has on a space, through personal
-- ownership, membership in the owning team, a direct grant, or a grant to
-- one of their teams (capped by their role in that team). NULL = no access.
CREATE FUNCTION space_role(sid uuid, uid uuid) RETURNS text LANGUAGE sql STABLE AS $$
    SELECT r FROM (
        SELECT 'owner' AS r FROM spaces WHERE id = sid AND owner_user_id = uid
        UNION ALL
        SELECT m.role FROM spaces s JOIN team_members m ON m.team_id = s.owner_team_id AND m.user_id = uid WHERE s.id = sid
        UNION ALL
        SELECT g.role FROM space_grants g WHERE g.space_id = sid AND g.user_id = uid
        UNION ALL
        SELECT CASE WHEN role_rank(m.role) < role_rank(g.role) THEN m.role ELSE g.role END
        FROM space_grants g JOIN team_members m ON m.team_id = g.team_id AND m.user_id = uid WHERE g.space_id = sid
    ) x ORDER BY role_rank(r) DESC LIMIT 1
$$;

CREATE TABLE access_keys (
    id           text PRIMARY KEY CHECK (id ~ '^DK[A-Z2-7]{18}$'),
    user_id      uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    secret_enc   bytea NOT NULL,
    label        text NOT NULL DEFAULT '',
    space_id     uuid REFERENCES spaces ON DELETE CASCADE,
    read_only    boolean NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at   timestamptz
);
CREATE INDEX access_keys_user ON access_keys(user_id);

CREATE TABLE ssh_keys (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id      uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    fingerprint  text NOT NULL UNIQUE,
    public_key   text NOT NULL,
    label        text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz
);
CREATE INDEX ssh_keys_user ON ssh_keys(user_id);

-- ---------------------------------------------------------------- content
CREATE TABLE blobs (
    sha256       bytea PRIMARY KEY CHECK (length(sha256) = 32),
    md5          bytea NOT NULL CHECK (length(md5) = 16),
    size         bigint NOT NULL CHECK (size >= 0),
    mime         text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE imports (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id          uuid REFERENCES users ON DELETE SET NULL,
    space_id         uuid NOT NULL REFERENCES spaces ON DELETE CASCADE,
    protocol         text NOT NULL CHECK (protocol IN ('web', 'api', 'webdav', 'sftp', 's3')),
    client_root      text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    last_activity_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX imports_user ON imports(user_id, protocol, space_id, last_activity_at);

-- A document is the identity of content inside one space. Its content is the
-- current version; the hash lives only in document_versions.
CREATE TABLE documents (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    space_id        uuid NOT NULL REFERENCES spaces ON DELETE CASCADE,
    current_version integer NOT NULL CHECK (current_version >= 1),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    created_by      uuid REFERENCES users ON DELETE SET NULL,
    forked_from     uuid REFERENCES documents ON DELETE SET NULL,
    deleted_at      timestamptz, -- in the trash since (exactly when no live link exists)
    UNIQUE (id, space_id)
);
CREATE INDEX documents_space ON documents(space_id);
CREATE INDEX documents_deleted ON documents(deleted_at) WHERE deleted_at IS NOT NULL;

CREATE TABLE document_versions (
    document_id uuid NOT NULL REFERENCES documents ON DELETE CASCADE,
    version     integer NOT NULL CHECK (version >= 1),
    sha256      bytea NOT NULL REFERENCES blobs,
    created_at  timestamptz NOT NULL DEFAULT now(),
    created_by  uuid REFERENCES users ON DELETE SET NULL,
    source      text NOT NULL CHECK (source IN ('web', 'api', 'webdav', 'sftp', 's3', 'rename', 'copy', 'restore')),
    meta        jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(meta) = 'object'),
    PRIMARY KEY (document_id, version)
);
CREATE INDEX document_versions_sha ON document_versions(sha256);

ALTER TABLE documents ADD FOREIGN KEY (id, current_version)
    REFERENCES document_versions (document_id, version) DEFERRABLE INITIALLY DEFERRED;

-- document_heads: documents with the content of their current version.
CREATE VIEW document_heads AS
    SELECT d.id, d.space_id, d.current_version, d.created_at, d.updated_at, d.created_by, d.forked_from, d.deleted_at,
           v.sha256, v.meta
    FROM documents d JOIN document_versions v ON v.document_id = d.id AND v.version = d.current_version;

-- ---------------------------------------------------------------- namespace
-- entries is the namespace of a space: folders and links (a document's
-- placement under a name in a folder). One name per folder, parents are
-- folders of the same space, links point to documents of the same space.
-- A trashed link keeps its parent while the folder exists (needed to revive
-- protocol "delete + recreate" saves) and its former path in deleted_path.
CREATE TABLE entries (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    space_id      uuid NOT NULL REFERENCES spaces ON DELETE CASCADE,
    kind          text NOT NULL CHECK (kind IN ('folder', 'link')),
    parent_id     uuid,
    parent_kind   text NOT NULL DEFAULT 'folder' CHECK (parent_kind = 'folder'), -- makes the FK below accept folders only
    name          text NOT NULL,
    document_id   uuid,
    import_id     uuid REFERENCES imports ON DELETE SET NULL,
    original_path text,
    deleted_at    timestamptz,
    deleted_path  text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    created_by    uuid REFERENCES users ON DELETE SET NULL,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, space_id, kind),
    FOREIGN KEY (parent_id, space_id, parent_kind) REFERENCES entries (id, space_id, kind)
        ON DELETE SET NULL (parent_id) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (document_id, space_id) REFERENCES documents (id, space_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    CHECK (kind = 'folder' AND document_id IS NULL AND import_id IS NULL AND original_path IS NULL
               AND deleted_at IS NULL AND deleted_path IS NULL
        OR kind = 'link' AND document_id IS NOT NULL AND original_path IS NOT NULL),
    -- the root folder is the only entry without a name; folders have a parent
    -- unless they are the root; live links always have a parent
    CHECK (name = '' AND kind = 'folder' AND parent_id IS NULL OR valid_name(name) AND (parent_id IS NOT NULL OR deleted_at IS NOT NULL)),
    CHECK ((deleted_at IS NULL) = (deleted_path IS NULL))
);
CREATE UNIQUE INDEX entries_name ON entries(parent_id, name) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX entries_root ON entries(space_id) WHERE parent_id IS NULL AND kind = 'folder';
CREATE INDEX entries_document ON entries(document_id) WHERE kind = 'link';
CREATE INDEX entries_import ON entries(import_id) WHERE import_id IS NOT NULL;
CREATE INDEX entries_trashed ON entries(parent_id, name, deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX entries_name_trgm ON entries USING gin (name gin_trgm_ops) WHERE kind = 'link';

-- A document is in the trash exactly when none of its links is live.
CREATE FUNCTION check_document_trash() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    ids uuid[] := ARRAY[]::uuid[];
    doc uuid;
BEGIN
    IF TG_TABLE_NAME = 'documents' THEN
        ids := ARRAY[NEW.id];
    ELSE
        IF TG_OP <> 'INSERT' AND OLD.document_id IS NOT NULL THEN ids := ids || OLD.document_id; END IF;
        IF TG_OP <> 'DELETE' AND NEW.document_id IS NOT NULL THEN ids := ids || NEW.document_id; END IF;
    END IF;
    FOREACH doc IN ARRAY ids LOOP
        IF EXISTS (SELECT 1 FROM documents d WHERE d.id = doc AND (d.deleted_at IS NULL) <>
                   EXISTS (SELECT 1 FROM entries e WHERE e.document_id = d.id AND e.deleted_at IS NULL)) THEN
            RAISE EXCEPTION 'document % must be in the trash exactly when it has no live link', doc
                USING ERRCODE = 'integrity_constraint_violation';
        END IF;
    END LOOP;
    RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER entries_document_trash AFTER INSERT OR UPDATE OR DELETE ON entries
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_document_trash();
CREATE CONSTRAINT TRIGGER documents_trash AFTER INSERT OR UPDATE OF deleted_at ON documents
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_document_trash();

-- folder_path returns "" for a root folder and "/a/b" otherwise.
CREATE FUNCTION folder_path(fid uuid) RETURNS text LANGUAGE sql STABLE AS $$
    WITH RECURSIVE up(id, parent_id, name, depth) AS (
        SELECT id, parent_id, name, 0 FROM entries WHERE id = fid AND kind = 'folder'
        UNION ALL
        SELECT e.id, e.parent_id, e.name, up.depth + 1 FROM entries e JOIN up ON e.id = up.parent_id
    )
    SELECT coalesce(string_agg('/' || name, '' ORDER BY depth DESC) FILTER (WHERE parent_id IS NOT NULL), '')
    FROM up
$$;

-- ---------------------------------------------------------------- tags & collections
-- Tags are one vocabulary per server; they attach to documents.
CREATE TABLE tags (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    name       text NOT NULL CHECK (btrim(name) = name AND name <> '' AND char_length(name) <= 64
                                    AND name IS NFC NORMALIZED AND name !~ '[\x01-\x1f\x7f-\u009f]'),
    color      text NOT NULL DEFAULT '' CHECK (color = '' OR color ~ '^#[0-9a-f]{6}$'),
    created_by uuid REFERENCES users ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX tags_name ON tags(lower(name));

CREATE TABLE document_tags (
    document_id uuid NOT NULL REFERENCES documents ON DELETE CASCADE,
    tag_id      uuid NOT NULL REFERENCES tags ON DELETE CASCADE,
    added_by    uuid REFERENCES users ON DELETE SET NULL,
    added_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (document_id, tag_id)
);
CREATE INDEX document_tags_tag ON document_tags(tag_id);

-- A collection groups documents across spaces. It never grants access:
-- viewers only see items in spaces they can read. With tag_id set it also
-- contains every document carrying that tag.
CREATE TABLE collections (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    name          text NOT NULL CHECK (valid_name(name) AND btrim(name) = name),
    owner_user_id uuid REFERENCES users ON DELETE CASCADE,
    owner_team_id uuid REFERENCES teams ON DELETE CASCADE,
    tag_id        uuid REFERENCES tags ON DELETE SET NULL,
    created_by    uuid REFERENCES users ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CHECK ((owner_user_id IS NULL) <> (owner_team_id IS NULL))
);
CREATE UNIQUE INDEX collections_user_name ON collections(owner_user_id, name) WHERE owner_user_id IS NOT NULL;
CREATE UNIQUE INDEX collections_team_name ON collections(owner_team_id, name) WHERE owner_team_id IS NOT NULL;

CREATE TABLE collection_items (
    collection_id uuid NOT NULL REFERENCES collections ON DELETE CASCADE,
    document_id   uuid NOT NULL REFERENCES documents ON DELETE CASCADE,
    added_by      uuid REFERENCES users ON DELETE SET NULL,
    added_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (collection_id, document_id)
);
CREATE INDEX collection_items_document ON collection_items(document_id);

-- collection_documents: manual items plus documents carrying the collection's tag.
CREATE FUNCTION collection_documents(cid uuid) RETURNS SETOF uuid LANGUAGE sql STABLE AS $$
    SELECT document_id FROM collection_items WHERE collection_id = cid
    UNION
    SELECT dt.document_id FROM collections c JOIN document_tags dt ON dt.tag_id = c.tag_id WHERE c.id = cid
$$;

-- ---------------------------------------------------------------- derived (per blob)
CREATE TABLE blob_processing (
    sha256     bytea PRIMARY KEY REFERENCES blobs ON DELETE CASCADE,
    status     text NOT NULL CHECK (status IN ('pending', 'processing', 'done', 'ocr_pending', 'unsupported', 'failed')),
    kind       text NOT NULL DEFAULT '' CHECK (kind IN ('', 'text', 'pdf', 'pdf-digital', 'pdf-scanned', 'pdf-mixed', 'pdf-ocred')),
    pages      integer CHECK (pages >= 0),
    error      text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE blob_texts (
    sha256     bytea PRIMARY KEY REFERENCES blobs ON DELETE CASCADE,
    method     text NOT NULL CHECK (method IN ('text', 'pdftotext', 'ocr')),
    content    text NOT NULL,
    tsv        tsvector NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX blob_texts_tsv ON blob_texts USING gin(tsv);

-- Variants derived from an original blob. They form a tree through
-- input_sha256 (the blob the tool ran on): original -> searchable_pdf, or
-- original -> image_only_pdf (foreign OCR layer removed) -> searchable_pdf.
CREATE TABLE blob_derivatives (
    source_sha256  bytea NOT NULL REFERENCES blobs ON DELETE CASCADE,
    kind           text NOT NULL CHECK (kind IN ('searchable_pdf', 'image_only_pdf')),
    derived_sha256 bytea NOT NULL REFERENCES blobs,
    input_sha256   bytea NOT NULL REFERENCES blobs,
    engine         text NOT NULL DEFAULT '',
    meta           jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(meta) = 'object'),
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_sha256, kind),
    CHECK (derived_sha256 <> source_sha256)
);
CREATE INDEX blob_derivatives_derived ON blob_derivatives(derived_sha256);
CREATE INDEX blob_derivatives_input ON blob_derivatives(input_sha256);

-- text_sha: whose extracted text represents a blob in search and text view.
-- With the "tildoc" OCR preference, a PDF that came with a foreign OCR layer
-- is represented by tildoc's own OCR of its image-only variant.
CREATE FUNCTION text_sha(sha bytea) RETURNS bytea LANGUAGE sql STABLE AS $$
    SELECT coalesce((
        SELECT x.derived_sha256 FROM blob_derivatives x
        JOIN blob_texts t ON t.sha256 = x.derived_sha256
        WHERE x.source_sha256 = sha AND x.kind = 'searchable_pdf'
          AND (SELECT ocr_preference FROM instance_settings) = 'tildoc'
          AND EXISTS (SELECT 1 FROM blob_derivatives y WHERE y.source_sha256 = sha AND y.kind = 'image_only_pdf')
    ), sha)
$$;

-- ---------------------------------------------------------------- jobs
CREATE TABLE jobs (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    kind         text NOT NULL CHECK (kind IN ('process_blob', 'purge_trash', 'gc_blobs', 'transfer', 'backup')),
    payload      jsonb NOT NULL DEFAULT '{}',
    dedup_key    text,
    status       text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'failed')),
    attempts     integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL DEFAULT 5 CHECK (max_attempts >= 1),
    run_after    timestamptz NOT NULL DEFAULT now(),
    locked_at    timestamptz,
    locked_by    text,
    last_error   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'running') = (locked_at IS NOT NULL))
);
CREATE UNIQUE INDEX jobs_dedup ON jobs(dedup_key) WHERE status IN ('queued', 'running');
CREATE INDEX jobs_ready ON jobs(run_after) WHERE status = 'queued';
CREATE INDEX jobs_running ON jobs(locked_at) WHERE status = 'running';

CREATE FUNCTION jobs_notify() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('tildoc_jobs', NEW.kind);
    RETURN NULL;
END $$;
CREATE TRIGGER jobs_notify AFTER INSERT ON jobs FOR EACH ROW EXECUTE FUNCTION jobs_notify();

-- ---------------------------------------------------------------- s3 multipart
CREATE TABLE multipart_uploads (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    space_id   uuid NOT NULL REFERENCES spaces ON DELETE CASCADE,
    key        text NOT NULL,
    meta       jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(meta) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE multipart_parts (
    upload_id   uuid NOT NULL REFERENCES multipart_uploads ON DELETE CASCADE,
    part_number integer NOT NULL CHECK (part_number BETWEEN 1 AND 10000),
    size        bigint NOT NULL CHECK (size >= 0),
    md5         bytea NOT NULL CHECK (length(md5) = 16),
    path        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (upload_id, part_number)
);

-- ---------------------------------------------------------------- transfers & backups
-- An import runs as a job: the uploaded file is staged, then applied.
CREATE TABLE transfers (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    kind        text NOT NULL CHECK (kind IN ('import_bundle', 'import_zip')),
    user_id     uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    status      text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'failed')),
    options     jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(options) = 'object'),
    file        text NOT NULL,
    size        bigint NOT NULL CHECK (size >= 0),
    progress    real NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 1),
    result      jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(result) = 'object'),
    error       text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    CHECK ((status IN ('done', 'failed')) = (finished_at IS NOT NULL))
);
CREATE INDEX transfers_user ON transfers(user_id, created_at);

-- Server backups: full (all blobs) or differential (blobs not in its base).
CREATE TABLE backups (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    mode        text NOT NULL CHECK (mode IN ('full', 'differential')),
    base_id     uuid REFERENCES backups ON DELETE RESTRICT,
    status      text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'failed')),
    file        text NOT NULL DEFAULT '',
    size        bigint NOT NULL DEFAULT 0 CHECK (size >= 0),
    blob_count  integer NOT NULL DEFAULT 0 CHECK (blob_count >= 0),
    error       text NOT NULL DEFAULT '',
    created_by  uuid REFERENCES users ON DELETE SET NULL,
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    CHECK ((mode = 'differential') = (base_id IS NOT NULL)),
    CHECK ((status IN ('done', 'failed')) = (finished_at IS NOT NULL))
);

-- the blob set of a full backup (differentials store the difference)
CREATE TABLE backup_blobs (
    backup_id uuid NOT NULL REFERENCES backups ON DELETE CASCADE,
    sha256    bytea NOT NULL CHECK (length(sha256) = 32),
    PRIMARY KEY (backup_id, sha256)
);
