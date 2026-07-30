package web

import (
	"fmt"
	"net/mail"
	"net/url"
	"strings"
)

type Form struct {
	values url.Values
	errors map[string][]string
}

func NewForm(values url.Values) *Form {
	cloned := make(url.Values, len(values))
	for name, current := range values {
		cloned[name] = append([]string(nil), current...)
	}
	return &Form{values: cloned, errors: make(map[string][]string)}
}

func (form *Form) Value(name string) string {
	return form.values.Get(name)
}

func (form *Form) Required(names ...string) {
	for _, name := range names {
		if strings.TrimSpace(form.Value(name)) == "" {
			form.AddError(name, "This field is required.")
		}
	}
}

func (form *Form) Email(name string) {
	value := strings.TrimSpace(form.Value(name))
	if value == "" {
		return
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || !strings.Contains(address.Address, "@") {
		form.AddError(name, "Enter a valid email address.")
	}
}

func (form *Form) MinLength(name string, minimum int) {
	value := form.Value(name)
	if value != "" && len([]rune(value)) < minimum {
		form.AddError(
			name,
			fmt.Sprintf("Ensure this value has at least %d characters.", minimum),
		)
	}
}

func (form *Form) AddError(name, message string) {
	form.errors[name] = append(form.errors[name], message)
}

func (form *Form) Valid() bool {
	return len(form.errors) == 0
}

func (form *Form) Errors() map[string][]string {
	cloned := make(map[string][]string, len(form.errors))
	for name, messages := range form.errors {
		cloned[name] = append([]string(nil), messages...)
	}
	return cloned
}
