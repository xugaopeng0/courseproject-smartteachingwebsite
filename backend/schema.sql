CREATE TABLE IF NOT EXISTS users (
 id text PRIMARY KEY, username text NOT NULL UNIQUE, name text NOT NULL,
 password_hash text NOT NULL, role text NOT NULL CHECK(role IN ('admin','teacher','student')),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS sessions (
 token_hash text PRIMARY KEY, user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_expiry ON sessions(expires_at);
CREATE TABLE IF NOT EXISTS classrooms (
 id text PRIMARY KEY, name text NOT NULL, description text NOT NULL DEFAULT '',
 owner_id text NOT NULL REFERENCES users(id), invite_code text NOT NULL UNIQUE,
 archived boolean NOT NULL DEFAULT false, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS memberships (
 class_id text NOT NULL REFERENCES classrooms(id) ON DELETE CASCADE,
 user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 joined_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(class_id,user_id)
);
CREATE TABLE IF NOT EXISTS handouts (
 id text PRIMARY KEY, class_id text NOT NULL REFERENCES classrooms(id),
 title text NOT NULL, filename text NOT NULL, stored_name text NOT NULL UNIQUE,
 size bigint NOT NULL, mime text NOT NULL, uploaded_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS handouts_class ON handouts(class_id);
