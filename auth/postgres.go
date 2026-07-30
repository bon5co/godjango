package auth

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/bon5co/godjango/database"
	"github.com/uptrace/bun"
)

type appConfig struct{}

func (appConfig) Name() string { return "auth" }

//go:embed migrations/*.sql
var authMigrationFiles embed.FS

func (appConfig) MigrationFS() fs.FS {
	files, err := fs.Sub(authMigrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return files
}

// App registers default auth migrations with a project.
var App appConfig

type groupModel struct {
	bun.BaseModel `bun:"table:auth_groups,alias:g"`
	ID            string `bun:"id,pk,type:uuid,nullzero,default:gen_random_uuid()"`
	Name          string `bun:"name,unique,notnull"`
}

type permissionModel struct {
	bun.BaseModel `bun:"table:auth_permissions,alias:p"`
	ID            string `bun:"id,pk,type:uuid,nullzero,default:gen_random_uuid()"`
	Identity      string `bun:"identity,unique,notnull"`
}

// BunStore is GoDjangGo's default PostgreSQL auth store.
type BunStore struct {
	db  *bun.DB
	idb bun.IDB
}

func NewBunStore(db *database.DB) *BunStore {
	return &BunStore{db: db.Bun(), idb: db.Bun()}
}

func (store *BunStore) InsertUser(ctx context.Context, user *User) error {
	_, err := store.idb.NewInsert().
		Model(user).
		Returning("id").
		Exec(ctx)
	return err
}

func (store *BunStore) UserByUsername(ctx context.Context, username string) (*User, error) {
	user := new(User)
	err := store.idb.NewSelect().
		Model(user).
		Where("username = ?", username).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	if err := store.idb.NewSelect().
		TableExpr("auth_permissions AS p").
		ColumnExpr("p.identity").
		Join("JOIN auth_user_permissions AS up ON up.permission_id = p.id").
		Where("up.user_id = ?", user.ID).
		OrderExpr("p.identity").
		Scan(ctx, &user.DirectPermissions); err != nil {
		return nil, err
	}

	type groupPermissionRow struct {
		GroupName  string      `bun:"group_name"`
		Permission *Permission `bun:"permission"`
	}
	var rows []groupPermissionRow
	if err := store.idb.NewSelect().
		TableExpr("auth_groups AS g").
		ColumnExpr("g.name AS group_name").
		ColumnExpr("p.identity AS permission").
		Join("JOIN auth_user_groups AS ug ON ug.group_id = g.id").
		Join("LEFT JOIN auth_group_permissions AS gp ON gp.group_id = g.id").
		Join("LEFT JOIN auth_permissions AS p ON p.id = gp.permission_id").
		Where("ug.user_id = ?", user.ID).
		OrderExpr("g.name, p.identity").
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	groups := make(map[string]*Group)
	var order []string
	for _, row := range rows {
		group, exists := groups[row.GroupName]
		if !exists {
			group = &Group{Name: row.GroupName}
			groups[row.GroupName] = group
			order = append(order, row.GroupName)
		}
		if row.Permission != nil {
			group.Permissions = append(group.Permissions, *row.Permission)
		}
	}
	for _, name := range order {
		user.Groups = append(user.Groups, *groups[name])
	}
	return user, nil
}

func (store *BunStore) UpdatePassword(ctx context.Context, user *User) error {
	result, err := store.idb.NewUpdate().
		Model(user).
		Column("password_hash").
		WherePK().
		Exec(ctx)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (store *BunStore) CreateGroup(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("godjango auth: group name is required")
	}
	_, err := store.idb.NewInsert().
		Model(&groupModel{Name: name}).
		On("CONFLICT (name) DO NOTHING").
		Exec(ctx)
	return err
}

func (store *BunStore) CreatePermission(ctx context.Context, permission Permission) error {
	if !validPermission(permission) {
		return fmt.Errorf("godjango auth: invalid permission %q", permission)
	}
	_, err := store.idb.NewInsert().
		Model(&permissionModel{Identity: string(permission)}).
		On("CONFLICT (identity) DO NOTHING").
		Exec(ctx)
	return err
}

func (store *BunStore) GrantUserPermission(
	ctx context.Context,
	userID string,
	permission Permission,
) error {
	result, err := store.idb.ExecContext(
		ctx,
		`INSERT INTO auth_user_permissions (user_id, permission_id)
		 SELECT ?, id FROM auth_permissions WHERE identity = ?
		 ON CONFLICT DO NOTHING`,
		userID,
		permission,
	)
	return requireAffectedOrExists(
		ctx,
		store.idb,
		result,
		err,
		`SELECT EXISTS (
			SELECT 1 FROM auth_user_permissions AS up
			JOIN auth_permissions AS p ON p.id = up.permission_id
			WHERE up.user_id = ? AND p.identity = ?
		)`,
		userID,
		permission,
	)
}

func (store *BunStore) AddUserToGroup(ctx context.Context, userID, groupName string) error {
	result, err := store.idb.ExecContext(
		ctx,
		`INSERT INTO auth_user_groups (user_id, group_id)
		 SELECT ?, id FROM auth_groups WHERE name = ?
		 ON CONFLICT DO NOTHING`,
		userID,
		groupName,
	)
	return requireAffectedOrExists(
		ctx,
		store.idb,
		result,
		err,
		`SELECT EXISTS (
			SELECT 1 FROM auth_user_groups AS ug
			JOIN auth_groups AS g ON g.id = ug.group_id
			WHERE ug.user_id = ? AND g.name = ?
		)`,
		userID,
		groupName,
	)
}

func (store *BunStore) GrantGroupPermission(
	ctx context.Context,
	groupName string,
	permission Permission,
) error {
	result, err := store.idb.ExecContext(
		ctx,
		`INSERT INTO auth_group_permissions (group_id, permission_id)
		 SELECT g.id, p.id
		 FROM auth_groups AS g, auth_permissions AS p
		 WHERE g.name = ? AND p.identity = ?
		 ON CONFLICT DO NOTHING`,
		groupName,
		permission,
	)
	return requireAffectedOrExists(
		ctx,
		store.idb,
		result,
		err,
		`SELECT EXISTS (
			SELECT 1 FROM auth_group_permissions AS gp
			JOIN auth_groups AS g ON g.id = gp.group_id
			JOIN auth_permissions AS p ON p.id = gp.permission_id
			WHERE g.name = ? AND p.identity = ?
		)`,
		groupName,
		permission,
	)
}

func (store *BunStore) RunInTx(
	ctx context.Context,
	fn func(context.Context, *BunStore) error,
) error {
	return store.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return fn(ctx, &BunStore{db: store.db, idb: tx})
	})
}

func requireAffectedOrExists(
	ctx context.Context,
	idb bun.IDB,
	result sql.Result,
	err error,
	query string,
	args ...any,
) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		var exists bool
		if err := idb.NewRaw(query, args...).Scan(ctx, &exists); err != nil {
			return err
		}
		if !exists {
			return errors.New("godjango auth: referenced access object not found")
		}
	}
	return nil
}
