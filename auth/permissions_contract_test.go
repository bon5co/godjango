// Adapted from Django tests/auth_tests/test_auth_backends.py and
// tests/auth_tests/test_models.py at commit 274a1d4.
// See THIRD_PARTY_NOTICES.md.
package auth_test

import (
	"testing"

	"github.com/bon5co/godjango/auth"
)

func permissionSetContains(set map[auth.Permission]struct{}, permission auth.Permission) bool {
	_, ok := set[permission]
	return ok
}

// Django: test_auth_backends.py::BaseModelBackendTest::{
// test_get_all_permissions,test_get_user_permissions,test_get_group_permissions}.
func TestPermissionBackendUnionsDirectAndGroupPermissions(t *testing.T) {
	user := &auth.User{
		IsActive:          true,
		DirectPermissions: []auth.Permission{"books.change_book"},
		Groups: []auth.Group{{
			Name:        "editors",
			Permissions: []auth.Permission{"books.view_book"},
		}},
	}
	backend := auth.ModelBackend{}

	all := backend.AllPermissions(user)
	for _, permission := range []auth.Permission{"books.change_book", "books.view_book"} {
		if !permissionSetContains(all, permission) {
			t.Errorf("AllPermissions() missing %q", permission)
		}
	}
}

// Django: test_auth_backends.py::BaseModelBackendTest::test_inactive_has_no_permissions.
func TestInactiveUserHasNoPermissions(t *testing.T) {
	user := &auth.User{
		IsActive:          false,
		DirectPermissions: []auth.Permission{"books.change_book"},
		Groups: []auth.Group{{
			Name:        "editors",
			Permissions: []auth.Permission{"books.view_book"},
		}},
	}
	backend := auth.ModelBackend{}

	if got := backend.AllPermissions(user); len(got) != 0 {
		t.Fatalf("AllPermissions(inactive) = %v, want empty", got)
	}
	if backend.HasPermission(user, "books.change_book") {
		t.Error("HasPermission(inactive) = true")
	}
}

// Django: test_auth_backends.py::BaseModelBackendTest::test_get_all_superuser_permissions.
func TestSuperuserHasEveryRegisteredPermission(t *testing.T) {
	registered := []auth.Permission{
		"books.add_book",
		"books.change_book",
		"books.delete_book",
		"books.view_book",
	}
	backend := auth.ModelBackend{RegisteredPermissions: registered}
	user := &auth.User{IsActive: true, IsSuperuser: true}

	got := backend.AllPermissions(user)
	if len(got) != len(registered) {
		t.Fatalf("AllPermissions(superuser) count = %d, want %d", len(got), len(registered))
	}
	for _, permission := range registered {
		if !permissionSetContains(got, permission) {
			t.Errorf("AllPermissions(superuser) missing %q", permission)
		}
	}
}

// Django: test_models.py::AnonymousUserTests::test_properties and
// test_auth_backends.py::AnonymousUserBackendTest::test_has_perm.
func TestAnonymousUserHasNoPermissions(t *testing.T) {
	user := auth.AnonymousUser{}

	if user.IsAuthenticated() {
		t.Error("IsAuthenticated() = true")
	}
	if !user.IsAnonymous() {
		t.Error("IsAnonymous() = false")
	}
	if user.HasPermission("books.view_book") {
		t.Error("HasPermission() = true")
	}
}

// Django: test_models.py::UserWithPermTestCase::{
// test_invalid_permission_name,test_nonexistent_permission}.
func TestMalformedPermissionIsDenied(t *testing.T) {
	backend := auth.ModelBackend{}
	user := &auth.User{
		IsActive:          true,
		DirectPermissions: []auth.Permission{"books.view_book"},
	}

	if !backend.HasPermission(user, "books.view_book") {
		t.Error("HasPermission(valid direct permission) = false")
	}
	for _, permission := range []auth.Permission{"", "view_book", "books.", ".view_book"} {
		if backend.HasPermission(user, permission) {
			t.Errorf("HasPermission(%q) = true", permission)
		}
	}
}
