package web

import (
	"net/url"
	"reflect"
	"testing"
)

func TestFormValidationHasStableFieldAndNonFieldErrors(t *testing.T) {
	form := NewForm(url.Values{
		"email":    {"not-an-email"},
		"password": {"short"},
	})
	form.Required("email", "password", "username")
	form.Email("email")
	form.MinLength("password", 12)
	form.AddError("", "Credentials were not accepted.")
	form.AddError("email", "Second email error.")

	if form.Valid() {
		t.Fatal("form is valid")
	}
	want := map[string][]string{
		"":         {"Credentials were not accepted."},
		"email":    {"Enter a valid email address.", "Second email error."},
		"password": {"Ensure this value has at least 12 characters."},
		"username": {"This field is required."},
	}
	if got := form.Errors(); !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %#v, want %#v", got, want)
	}
	got := form.Errors()
	got["email"][0] = "mutated"
	if form.Errors()["email"][0] == "mutated" {
		t.Fatal("Errors exposed mutable internal state")
	}
	if value := form.Value("email"); value != "not-an-email" {
		t.Fatalf("email value = %q", value)
	}
}

func TestFormAcceptsValidValuesAndDoesNotValidateEmptyOptionalEmail(t *testing.T) {
	form := NewForm(url.Values{
		"email":    {""},
		"password": {"long-enough-password"},
	})
	form.Required("password")
	form.Email("email")
	form.MinLength("password", 12)
	if !form.Valid() {
		t.Fatalf("errors = %#v", form.Errors())
	}
}
