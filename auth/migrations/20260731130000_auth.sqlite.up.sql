CREATE TABLE auth_users (
    id text PRIMARY KEY,
    username text NOT NULL UNIQUE,
    email text NOT NULL,
    password_hash text NOT NULL,
    is_staff boolean NOT NULL DEFAULT false,
    is_active boolean NOT NULL DEFAULT true,
    is_superuser boolean NOT NULL DEFAULT false,
    last_login timestamp,
    date_joined timestamp NOT NULL
);
CREATE TABLE auth_groups (
    id text PRIMARY KEY,
    name text NOT NULL UNIQUE
);
CREATE TABLE auth_permissions (
    id text PRIMARY KEY,
    identity text NOT NULL UNIQUE
);
CREATE TABLE auth_user_groups (
    user_id text NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    group_id text NOT NULL REFERENCES auth_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, group_id)
);
CREATE TABLE auth_user_permissions (
    user_id text NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    permission_id text NOT NULL REFERENCES auth_permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, permission_id)
);
CREATE TABLE auth_group_permissions (
    group_id text NOT NULL REFERENCES auth_groups(id) ON DELETE CASCADE,
    permission_id text NOT NULL REFERENCES auth_permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, permission_id)
);
CREATE TABLE auth_sessions (
    session_key text PRIMARY KEY,
    data blob NOT NULL,
    expires_at timestamp NOT NULL
);
CREATE INDEX auth_sessions_expires_at_idx ON auth_sessions (expires_at);
