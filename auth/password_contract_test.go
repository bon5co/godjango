// Adapted from Django tests/auth_tests/test_hashers.py at commit 274a1d4.
// See THIRD_PARTY_NOTICES.md.
package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/bon5co/godjango/auth"
)

// Django: test_hashers.py::TestUtilsHashPass::test_simple.
func TestPasswordHasherRoundTrip(t *testing.T) {
	hasher := auth.NewPasswordHasher()
	raw := "lètmein"

	encoded, err := hasher.Encode(&raw)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if !strings.HasPrefix(encoded, "pbkdf2_sha256$") {
		t.Errorf("encoded password %q lacks pbkdf2_sha256 prefix", encoded)
	}
	if !hasher.IsUsable(encoded) {
		t.Error("encoded password is not usable")
	}

	correct, err := hasher.Check(&raw, encoded)
	if err != nil {
		t.Fatalf("Check(correct) error = %v", err)
	}
	if !correct.OK {
		t.Error("Check(correct).OK = false")
	}

	wrong := "lètmeinz"
	incorrect, err := hasher.Check(&wrong, encoded)
	if err != nil {
		t.Fatalf("Check(wrong) error = %v", err)
	}
	if incorrect.OK {
		t.Error("Check(wrong).OK = true")
	}
}

// Django: test_hashers.py::TestUtilsHashPass::test_simple (blank-password cases).
func TestBlankPasswordIsUsableAndDistinctFromSpace(t *testing.T) {
	hasher := auth.NewPasswordHasher()
	blank := ""

	encoded, err := hasher.Encode(&blank)
	if err != nil {
		t.Fatalf("Encode(blank) error = %v", err)
	}
	if !hasher.IsUsable(encoded) {
		t.Error("blank encoded password is not usable")
	}
	check, err := hasher.Check(&blank, encoded)
	if err != nil || !check.OK {
		t.Fatalf("Check(blank) = %+v, %v; want OK", check, err)
	}
	space := " "
	check, err = hasher.Check(&space, encoded)
	if err != nil {
		t.Fatalf("Check(space) error = %v", err)
	}
	if check.OK {
		t.Error("space matched blank password")
	}
}

// Django: test_hashers.py::TestUtilsHashPass::test_unusable.
func TestUnusablePasswordsNeverVerifyAndAreRandom(t *testing.T) {
	hasher := auth.NewPasswordHasher()

	first, err := hasher.Encode(nil)
	if err != nil {
		t.Fatalf("Encode(nil) error = %v", err)
	}
	second, err := hasher.Encode(nil)
	if err != nil {
		t.Fatalf("Encode(nil) second error = %v", err)
	}
	if first == second {
		t.Fatalf("two unusable encodings collided: %q", first)
	}
	if hasher.IsUsable(first) {
		t.Error("unusable encoding reported usable")
	}
	for _, candidate := range []string{"", "!", first, "lètmein"} {
		check, checkErr := hasher.Check(&candidate, first)
		if checkErr != nil {
			t.Fatalf("Check(%q) error = %v", candidate, checkErr)
		}
		if check.OK {
			t.Errorf("unusable password verified candidate %q", candidate)
		}
	}
}

// Django: test_hashers.py::TestUtilsHashPass::test_no_upgrade.
func TestWrongPasswordNeverRequestsHashUpgrade(t *testing.T) {
	hasher := auth.NewPasswordHasher()
	raw := "correct"
	encoded, err := hasher.Encode(&raw)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	wrong := "wrong"
	check, err := hasher.Check(&wrong, encoded)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if check.OK {
		t.Error("wrong password verified")
	}
	if check.NeedsUpdate {
		t.Error("wrong password requested a hash upgrade")
	}
}

// Django: test_hashers.py::TestUtilsHashPass::test_bad_algorithm.
func TestUnknownPasswordAlgorithmReturnsError(t *testing.T) {
	hasher := auth.NewPasswordHasher()
	raw := "secret"

	if _, err := hasher.Check(&raw, "lolcat$salt$hash"); !errors.Is(err, auth.ErrUnknownPasswordAlgorithm) {
		t.Fatalf("Check() error = %v, want %v", err, auth.ErrUnknownPasswordAlgorithm)
	}
}

// Django: test_models.py::AbstractBaseUserTests::test_has_usable_password and
// test_hashers.py::TestUtilsHashPass::test_simple.
func TestUserSetAndCheckPassword(t *testing.T) {
	hasher := auth.NewPasswordHasher()
	user := &auth.User{}
	raw := "sëcure"

	if err := user.SetPassword(hasher, &raw); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}
	if !user.HasUsablePassword(hasher) {
		t.Error("HasUsablePassword() = false")
	}
	ok, err := user.CheckPassword(hasher, raw)
	if err != nil {
		t.Fatalf("CheckPassword() error = %v", err)
	}
	if !ok {
		t.Error("CheckPassword() = false")
	}
}
