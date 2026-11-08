CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ---------------------------------------------------------------- identity
CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username      text NOT NULL UNIQUE,
    display_name  text NOT NULL DEFAULT '',
    password_hash text NOT NULL,
    is_admin      boolean NOT NULL DEFAULT false,
    disabled      boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token_hash   bytea PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE teams (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE team_members (
    team_id    uuid NOT NULL REFERENCES teams ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('owner','admin','member','viewer')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id)
);
CREATE INDEX team_members_user ON team_members(user_id);

CREATE TABLE spaces (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug          text NOT NULL UNIQUE,
    name          text NOT NULL,
    owner_user_id uuid REFERENCES users ON DELETE CASCADE,
    owner_team_id uuid REFERENCES teams ON DELETE CASCADE,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CHECK ((owner_user_id IS NULL) <> (owner_team_id IS NULL))
);
CREATE INDEX spaces_owner_user ON spaces(owner_user_id);
CREATE INDEX spaces_owner_team ON spaces(owner_team_id);

CREATE TABLE access_keys (
    id           text PRIMARY KEY,
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
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    fingerprint  text NOT NULL UNIQUE,
    public_key   text NOT NULL,
    label        text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz
);

-- ---------------------------------------------------------------- structure
CREATE TABLE folders (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    space_id   uuid NOT NULL REFERENCES spaces ON DELETE CASCADE,
    parent_id  uuid REFERENCES folders ON DELETE CASCADE,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid REFERENCES users ON DELETE SET NULL,
    CHECK ((parent_id IS NULL) = (name = ''))
);
CREATE UNIQUE INDEX folders_parent_name ON folders(parent_id, name);
CREATE UNIQUE INDEX folders_root ON folders(space_id) WHERE parent_id IS NULL;
CREATE INDEX folders_space ON folders(space_id);

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
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          uuid REFERENCES users ON DELETE SET NULL,
    space_id         uuid NOT NULL REFERENCES spaces ON DELETE CASCADE,
    protocol         text NOT NULL,
    client_root      text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    last_activity_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX imports_user ON imports(user_id, protocol, space_id, last_activity_at);

CREATE TABLE documents (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    space_id        uuid NOT NULL REFERENCES spaces ON DELETE CASCADE,
    current_sha256  bytea NOT NULL REFERENCES blobs,
    current_version integer NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    created_by      uuid REFERENCES users ON DELETE SET NULL,
    forked_from     uuid REFERENCES documents ON DELETE SET NULL,
    deleted_at      timestamptz
);
CREATE INDEX documents_space_sha ON documents(space_id, current_sha256) WHERE deleted_at IS NULL;
CREATE INDEX documents_deleted ON documents(deleted_at) WHERE deleted_at IS NOT NULL;

CREATE TABLE document_versions (
    document_id uuid NOT NULL REFERENCES documents ON DELETE CASCADE,
    version     integer NOT NULL,
    sha256      bytea NOT NULL REFERENCES blobs,
    created_at  timestamptz NOT NULL DEFAULT now(),
    created_by  uuid REFERENCES users ON DELETE SET NULL,
    source      text NOT NULL DEFAULT '',
    meta        jsonb NOT NULL DEFAULT '{}',
    PRIMARY KEY (document_id, version)
);
CREATE INDEX document_versions_sha ON document_versions(sha256);

CREATE TABLE links (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id   uuid NOT NULL REFERENCES documents ON DELETE CASCADE,
    folder_id     uuid REFERENCES folders ON DELETE SET NULL,
    name          text NOT NULL,
    import_id     uuid REFERENCES imports ON DELETE SET NULL,
    original_path text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    created_by    uuid REFERENCES users ON DELETE SET NULL,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz,
    deleted_path  text,
    CHECK (deleted_at IS NOT NULL OR folder_id IS NOT NULL)
);
CREATE UNIQUE INDEX links_folder_name ON links(folder_id, name) WHERE deleted_at IS NULL;
CREATE INDEX links_document ON links(document_id);
CREATE INDEX links_import ON links(import_id);
CREATE INDEX links_name_trgm ON links USING gin (name gin_trgm_ops);

-- ---------------------------------------------------------------- derived
CREATE TABLE blob_processing (
    sha256     bytea PRIMARY KEY REFERENCES blobs ON DELETE CASCADE,
    status     text NOT NULL CHECK (status IN ('pending','processing','done','ocr_pending','unsupported','failed')),
    kind       text NOT NULL DEFAULT '',
    pages      integer,
    error      text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE blob_texts (
    sha256     bytea PRIMARY KEY REFERENCES blobs ON DELETE CASCADE,
    method     text NOT NULL,
    content    text NOT NULL,
    tsv        tsvector NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX blob_texts_tsv ON blob_texts USING gin(tsv);

CREATE TABLE blob_derivatives (
    source_sha256  bytea NOT NULL REFERENCES blobs ON DELETE CASCADE,
    kind           text NOT NULL,
    derived_sha256 bytea NOT NULL REFERENCES blobs,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_sha256, kind)
);
CREATE INDEX blob_derivatives_derived ON blob_derivatives(derived_sha256);

-- ---------------------------------------------------------------- jobs
CREATE TABLE jobs (
    id           bigserial PRIMARY KEY,
    kind         text NOT NULL,
    payload      jsonb NOT NULL DEFAULT '{}',
    dedup_key    text,
    status       text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','done','failed')),
    attempts     integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 5,
    run_after    timestamptz NOT NULL DEFAULT now(),
    locked_at    timestamptz,
    locked_by    text,
    last_error   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX jobs_dedup ON jobs(dedup_key) WHERE status IN ('queued','running');
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
    id         text PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    space_id   uuid NOT NULL REFERENCES spaces ON DELETE CASCADE,
    key        text NOT NULL,
    meta       jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE multipart_parts (
    upload_id   text NOT NULL REFERENCES multipart_uploads ON DELETE CASCADE,
    part_number integer NOT NULL CHECK (part_number BETWEEN 1 AND 10000),
    size        bigint NOT NULL,
    md5         bytea NOT NULL,
    path        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (upload_id, part_number)
);

-- ---------------------------------------------------------------- helpers
-- folder_path returns "" for a root folder and "/a/b" otherwise.
CREATE FUNCTION folder_path(fid uuid) RETURNS text LANGUAGE sql STABLE AS $$
    WITH RECURSIVE up(id, parent_id, name, depth) AS (
        SELECT id, parent_id, name, 0 FROM folders WHERE id = fid
        UNION ALL
        SELECT f.id, f.parent_id, f.name, up.depth + 1 FROM folders f JOIN up ON f.id = up.parent_id
    )
    SELECT coalesce(string_agg('/' || name, '' ORDER BY depth DESC) FILTER (WHERE parent_id IS NOT NULL), '')
    FROM up
$$;
