CREATE TABLE auth_users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username text NOT NULL UNIQUE,
    email text NOT NULL,
    password_hash text NOT NULL,
    is_staff boolean NOT NULL DEFAULT false,
    is_active boolean NOT NULL DEFAULT true,
    is_superuser boolean NOT NULL DEFAULT false,
    last_login timestamptz,
    date_joined timestamptz NOT NULL
);

CREATE TABLE auth_groups (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE
);

CREATE TABLE auth_permissions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    identity text NOT NULL UNIQUE
);

CREATE TABLE auth_user_groups (
    user_id uuid NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    group_id uuid NOT NULL REFERENCES auth_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, group_id)
);

CREATE TABLE auth_user_permissions (
    user_id uuid NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    permission_id uuid NOT NULL REFERENCES auth_permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, permission_id)
);

CREATE TABLE auth_group_permissions (
    group_id uuid NOT NULL REFERENCES auth_groups(id) ON DELETE CASCADE,
    permission_id uuid NOT NULL REFERENCES auth_permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, permission_id)
);

CREATE TABLE auth_sessions (
    session_key text PRIMARY KEY,
    data bytea NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX auth_sessions_expires_at_idx ON auth_sessions (expires_at);
