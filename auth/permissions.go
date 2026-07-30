package auth

import "strings"

// ModelBackend resolves direct and group permissions.
type ModelBackend struct {
	RegisteredPermissions []Permission
}

func (b ModelBackend) UserPermissions(user *User) map[Permission]struct{} {
	permissions := make(map[Permission]struct{})
	if user == nil || !user.IsActive {
		return permissions
	}
	for _, permission := range user.DirectPermissions {
		if validPermission(permission) {
			permissions[permission] = struct{}{}
		}
	}
	return permissions
}

func (b ModelBackend) GroupPermissions(user *User) map[Permission]struct{} {
	permissions := make(map[Permission]struct{})
	if user == nil || !user.IsActive {
		return permissions
	}
	for _, group := range user.Groups {
		for _, permission := range group.Permissions {
			if validPermission(permission) {
				permissions[permission] = struct{}{}
			}
		}
	}
	return permissions
}

func (b ModelBackend) AllPermissions(user *User) map[Permission]struct{} {
	permissions := make(map[Permission]struct{})
	if user == nil || !user.IsActive {
		return permissions
	}
	if user.IsSuperuser {
		for _, permission := range b.RegisteredPermissions {
			if validPermission(permission) {
				permissions[permission] = struct{}{}
			}
		}
		return permissions
	}
	for permission := range b.UserPermissions(user) {
		permissions[permission] = struct{}{}
	}
	for permission := range b.GroupPermissions(user) {
		permissions[permission] = struct{}{}
	}
	return permissions
}

func (b ModelBackend) HasPermission(user *User, permission Permission) bool {
	if !validPermission(permission) {
		return false
	}
	_, ok := b.AllPermissions(user)[permission]
	return ok
}

func validPermission(permission Permission) bool {
	parts := strings.Split(string(permission), ".")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

// AnonymousUser is the request principal when no authenticated session exists.
type AnonymousUser struct{}

func (AnonymousUser) IsAuthenticated() bool {
	return false
}

func (AnonymousUser) IsAnonymous() bool {
	return true
}

func (AnonymousUser) HasPermission(Permission) bool {
	return false
}
