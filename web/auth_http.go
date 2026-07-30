package web

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/bon5co/godjango/auth"
	"github.com/bon5co/godjango/web/view"
	"github.com/go-chi/chi/v5"
)

type AuthBackend interface {
	Authenticate(context.Context, string, string) (*auth.User, error)
	UserByID(context.Context, string) (*auth.User, error)
	UsersByEmail(context.Context, string) ([]*auth.User, error)
	SetPassword(context.Context, *auth.User, string) error
}

type ResetMessage struct {
	User  *auth.User
	Token string
	URL   string
}

type AuthHandlerConfig struct {
	Backend       AuthBackend
	SessionSecret []byte
	ResetTokens   auth.ResetTokenGenerator
	SendReset     func(context.Context, ResetMessage) error
	AllowedHosts  []string
}

type AuthHandlers struct {
	config AuthHandlerConfig
}

func NewAuthHandlers(config AuthHandlerConfig) (*AuthHandlers, error) {
	switch {
	case config.Backend == nil:
		return nil, errors.New("godjango web: auth backend is required")
	case len(config.SessionSecret) == 0:
		return nil, errors.New("godjango web: session auth secret is required")
	case len(config.ResetTokens.Secret) == 0 || config.ResetTokens.Timeout <= 0:
		return nil, errors.New("godjango web: valid reset token generator is required")
	case config.SendReset == nil:
		return nil, errors.New("godjango web: password reset sender is required")
	default:
		return &AuthHandlers{config: config}, nil
	}
}

func (handlers *AuthHandlers) Routes(router chi.Router) {
	router.Get("/accounts/login/", handlers.loginForm)
	router.Post("/accounts/login/", handlers.login)
	router.Post("/accounts/logout/", handlers.logout)
	router.With(RequireAuthentication()).Get(
		"/accounts/password-change/",
		handlers.passwordChangeForm,
	)
	router.With(RequireAuthentication()).Post(
		"/accounts/password-change/",
		handlers.passwordChange,
	)
	router.Get("/accounts/password-change/done/", handlers.passwordChangeDone)
	router.Get("/accounts/password-reset/", handlers.passwordResetForm)
	router.Post("/accounts/password-reset/", handlers.passwordReset)
	router.Get("/accounts/password-reset/done/", handlers.passwordResetDone)
	router.Get(
		"/accounts/password-reset/{uid}/{token}/",
		handlers.passwordResetConfirmForm,
	)
	router.Post(
		"/accounts/password-reset/{uid}/{token}/",
		handlers.passwordResetConfirm,
	)
	router.Get("/accounts/password-reset/complete/", handlers.passwordResetComplete)
}

type currentUserContextKey struct{}

func Authentication(backend AuthBackend, sessionSecret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if backend == nil || len(sessionSecret) == 0 {
				WriteError(response, request, http.StatusInternalServerError, "auth_unavailable")
				return
			}
			session := SessionFromRequest(request)
			if session == nil {
				WriteError(response, request, http.StatusInternalServerError, "session_unavailable")
				return
			}
			userID, authenticated := session.Get(auth.SessionUserIDKey)
			if !authenticated {
				next.ServeHTTP(response, request)
				return
			}
			user, err := backend.UserByID(request.Context(), userID)
			switch {
			case errors.Is(err, auth.ErrUserNotFound):
				_ = session.Flush()
				next.ServeHTTP(response, request)
				return
			case err != nil:
				WriteError(response, request, http.StatusInternalServerError, "auth_unavailable")
				return
			}
			if !user.IsActive {
				_ = session.Flush()
				next.ServeHTTP(response, request)
				return
			}
			if _, valid := auth.SessionUserID(session, user, sessionSecret); !valid {
				next.ServeHTTP(response, request)
				return
			}
			ctx := context.WithValue(request.Context(), currentUserContextKey{}, user)
			next.ServeHTTP(response, request.WithContext(ctx))
		})
	}
}

func (handlers *AuthHandlers) passwordChangeDone(
	response http.ResponseWriter,
	_ *http.Request,
) {
	_, _ = fmt.Fprint(response, "Password changed.")
}

func CurrentUser(request *http.Request) (*auth.User, bool) {
	if request == nil {
		return nil, false
	}
	user, ok := request.Context().Value(currentUserContextKey{}).(*auth.User)
	return user, ok && user != nil
}

func RequireAuthentication() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if _, ok := CurrentUser(request); !ok {
				WriteError(response, request, http.StatusUnauthorized, "authentication_required")
				return
			}
			next.ServeHTTP(response, request)
		})
	}
}

func (handlers *AuthHandlers) loginForm(response http.ResponseWriter, request *http.Request) {
	handlers.renderLogin(response, request, request.URL.Query().Get("next"), "", nil)
}

func (handlers *AuthHandlers) login(response http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		WriteError(response, request, http.StatusBadRequest, "invalid_form")
		return
	}
	form := NewForm(request.PostForm)
	form.Required("username", "password")
	var user *auth.User
	if form.Valid() {
		var err error
		user, err = handlers.config.Backend.Authenticate(
			request.Context(),
			form.Value("username"),
			form.Value("password"),
		)
		if err != nil {
			WriteError(response, request, http.StatusInternalServerError, "auth_unavailable")
			return
		}
	}
	if user == nil {
		form.AddError("", "credentials were not accepted")
		handlers.renderLogin(
			response,
			request,
			form.Value("next"),
			form.Value("username"),
			form.Errors(),
		)
		return
	}
	if err := auth.Login(
		SessionFromRequest(request),
		user,
		handlers.config.SessionSecret,
	); err != nil {
		WriteError(response, request, http.StatusInternalServerError, "session_unavailable")
		return
	}
	target := form.Value("next")
	if !IsSafeRedirect(
		request,
		target,
		handlers.config.AllowedHosts,
		request.TLS != nil,
	) {
		target = "/"
	}
	redirect(response, request, target)
}

func (handlers *AuthHandlers) logout(response http.ResponseWriter, request *http.Request) {
	if err := auth.Logout(SessionFromRequest(request)); err != nil {
		WriteError(response, request, http.StatusInternalServerError, "session_unavailable")
		return
	}
	redirect(response, request, "/")
}

func (handlers *AuthHandlers) passwordChangeForm(
	response http.ResponseWriter,
	request *http.Request,
) {
	handlers.renderPasswordChange(response, request, nil)
}

func (handlers *AuthHandlers) passwordChange(response http.ResponseWriter, request *http.Request) {
	user, ok := CurrentUser(request)
	if !ok {
		WriteError(response, request, http.StatusUnauthorized, "authentication_required")
		return
	}
	if err := request.ParseForm(); err != nil {
		WriteError(response, request, http.StatusBadRequest, "invalid_form")
		return
	}
	form := NewForm(request.PostForm)
	form.Required("old_password", "new_password1", "new_password2")
	form.MinLength("new_password1", 8)
	if form.Value("new_password1") != form.Value("new_password2") {
		form.AddError("new_password2", "The two password fields did not match.")
	}
	if form.Valid() {
		matched, err := handlers.config.Backend.Authenticate(
			request.Context(),
			user.Username,
			form.Value("old_password"),
		)
		if err != nil {
			WriteError(response, request, http.StatusInternalServerError, "auth_unavailable")
			return
		}
		if matched == nil {
			form.AddError("old_password", "The old password was not accepted.")
		}
	}
	if !form.Valid() {
		handlers.renderPasswordChange(response, request, form.Errors())
		return
	}
	if err := handlers.config.Backend.SetPassword(
		request.Context(),
		user,
		form.Value("new_password1"),
	); err != nil {
		WriteError(response, request, http.StatusInternalServerError, "auth_unavailable")
		return
	}
	session := SessionFromRequest(request)
	if err := session.Cycle(); err != nil {
		WriteError(response, request, http.StatusInternalServerError, "session_unavailable")
		return
	}
	if err := auth.Login(session, user, handlers.config.SessionSecret); err != nil {
		WriteError(response, request, http.StatusInternalServerError, "session_unavailable")
		return
	}
	redirect(response, request, "/accounts/password-change/done/")
}

func (handlers *AuthHandlers) passwordResetForm(
	response http.ResponseWriter,
	request *http.Request,
) {
	handlers.renderPasswordReset(response, request, "", nil)
}

func (handlers *AuthHandlers) passwordReset(response http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		WriteError(response, request, http.StatusBadRequest, "invalid_form")
		return
	}
	form := NewForm(request.PostForm)
	form.Required("email")
	form.Email("email")
	if !form.Valid() {
		handlers.renderPasswordReset(response, request, form.Value("email"), form.Errors())
		return
	}
	email := auth.NormalizeEmail(strings.TrimSpace(form.Value("email")))
	users, err := handlers.config.Backend.UsersByEmail(request.Context(), email)
	if err != nil {
		WriteError(response, request, http.StatusInternalServerError, "auth_unavailable")
		return
	}
	for _, user := range users {
		if user == nil ||
			!user.IsActive ||
			user.PasswordHash == "" ||
			strings.HasPrefix(user.PasswordHash, "!") {
			continue
		}
		token, err := handlers.config.ResetTokens.Make(user)
		if err != nil {
			continue
		}
		uid := base64.RawURLEncoding.EncodeToString([]byte(user.ID))
		message := ResetMessage{
			User:  user,
			Token: token,
			URL: "/accounts/password-reset/" + uid + "/" +
				url.PathEscape(token) + "/",
		}
		if err := handlers.config.SendReset(request.Context(), message); err != nil {
			slog.ErrorContext(
				request.Context(),
				"password reset delivery failed",
				"request_id",
				RequestIDFromContext(request.Context()),
				"error",
				err,
			)
		}
	}
	redirect(response, request, "/accounts/password-reset/done/")
}

func (handlers *AuthHandlers) passwordResetDone(
	response http.ResponseWriter,
	_ *http.Request,
) {
	_, _ = fmt.Fprint(response, "If an account exists, reset instructions were sent.")
}

func (handlers *AuthHandlers) passwordResetConfirmForm(
	response http.ResponseWriter,
	request *http.Request,
) {
	if _, ok := handlers.resetUser(request); !ok {
		WriteError(response, request, http.StatusBadRequest, "invalid_reset")
		return
	}
	setCSRFHeader(response, request)
	handlers.renderPasswordResetConfirm(response, request, nil)
}

func (handlers *AuthHandlers) passwordResetConfirm(
	response http.ResponseWriter,
	request *http.Request,
) {
	user, ok := handlers.resetUser(request)
	if !ok {
		WriteError(response, request, http.StatusBadRequest, "invalid_reset")
		return
	}
	if err := request.ParseForm(); err != nil {
		WriteError(response, request, http.StatusBadRequest, "invalid_form")
		return
	}
	form := NewForm(request.PostForm)
	form.Required("new_password1", "new_password2")
	form.MinLength("new_password1", 8)
	if form.Value("new_password1") != form.Value("new_password2") {
		form.AddError("new_password2", "The two password fields did not match.")
	}
	if !form.Valid() {
		handlers.renderPasswordResetConfirm(response, request, form.Errors())
		return
	}
	if err := handlers.config.Backend.SetPassword(
		request.Context(),
		user,
		form.Value("new_password1"),
	); err != nil {
		WriteError(response, request, http.StatusInternalServerError, "auth_unavailable")
		return
	}
	redirect(response, request, "/accounts/password-reset/complete/")
}

func (handlers *AuthHandlers) passwordResetComplete(
	response http.ResponseWriter,
	_ *http.Request,
) {
	_, _ = fmt.Fprint(response, "Password reset complete.")
}

func (handlers *AuthHandlers) resetUser(request *http.Request) (*auth.User, bool) {
	uid, err := base64.RawURLEncoding.DecodeString(chi.URLParam(request, "uid"))
	if err != nil || len(uid) == 0 {
		return nil, false
	}
	user, err := handlers.config.Backend.UserByID(request.Context(), string(uid))
	if err != nil || user == nil {
		return nil, false
	}
	return user, handlers.config.ResetTokens.Check(user, chi.URLParam(request, "token"))
}

func (handlers *AuthHandlers) renderLogin(
	response http.ResponseWriter,
	request *http.Request,
	next string,
	username string,
	errors map[string][]string,
) {
	handlers.renderForm(response, request, "Sign in", view.FormData{
		Action:       "/accounts/login/",
		Method:       http.MethodPost,
		CSRFToken:    CSRFToken(request),
		ErrorSummary: errors[""],
		Fields: []view.Field{
			{
				Name:         "username",
				Label:        "Username",
				Value:        username,
				Autocomplete: "username",
				Errors:       errors["username"],
			},
			{
				Name:         "password",
				Label:        "Password",
				Type:         "password",
				Autocomplete: "current-password",
				Errors:       errors["password"],
			},
			{Name: "next", Type: "hidden", Value: next},
		},
		SubmitLabel: "Sign in",
	}, errors)
}

func (handlers *AuthHandlers) renderPasswordChange(
	response http.ResponseWriter,
	request *http.Request,
	errors map[string][]string,
) {
	handlers.renderForm(response, request, "Change password", view.FormData{
		Action:    "/accounts/password-change/",
		Method:    http.MethodPost,
		CSRFToken: CSRFToken(request),
		Fields: []view.Field{
			{
				Name:         "old_password",
				Label:        "Current password",
				Type:         "password",
				Autocomplete: "current-password",
				Errors:       errors["old_password"],
			},
			{
				Name:         "new_password1",
				Label:        "New password",
				Type:         "password",
				Autocomplete: "new-password",
				Errors:       errors["new_password1"],
			},
			{
				Name:         "new_password2",
				Label:        "Confirm new password",
				Type:         "password",
				Autocomplete: "new-password",
				Errors:       errors["new_password2"],
			},
		},
		SubmitLabel: "Change password",
	}, errors)
}

func (handlers *AuthHandlers) renderPasswordReset(
	response http.ResponseWriter,
	request *http.Request,
	email string,
	errors map[string][]string,
) {
	handlers.renderForm(response, request, "Reset password", view.FormData{
		Action:    "/accounts/password-reset/",
		Method:    http.MethodPost,
		CSRFToken: CSRFToken(request),
		Fields: []view.Field{{
			Name:         "email",
			Label:        "Email address",
			Type:         "email",
			Value:        email,
			Autocomplete: "email",
			Errors:       errors["email"],
		}},
		SubmitLabel: "Send reset link",
	}, errors)
}

func (handlers *AuthHandlers) renderPasswordResetConfirm(
	response http.ResponseWriter,
	request *http.Request,
	errors map[string][]string,
) {
	handlers.renderForm(response, request, "Choose a new password", view.FormData{
		Action:    request.URL.Path,
		Method:    http.MethodPost,
		CSRFToken: CSRFToken(request),
		Fields: []view.Field{
			{
				Name:         "new_password1",
				Label:        "New password",
				Type:         "password",
				Autocomplete: "new-password",
				Errors:       errors["new_password1"],
			},
			{
				Name:         "new_password2",
				Label:        "Confirm new password",
				Type:         "password",
				Autocomplete: "new-password",
				Errors:       errors["new_password2"],
			},
		},
		SubmitLabel: "Reset password",
	}, errors)
}

func (handlers *AuthHandlers) renderForm(
	response http.ResponseWriter,
	request *http.Request,
	title string,
	form view.FormData,
	errors map[string][]string,
) {
	setCSRFHeader(response, request)
	status := http.StatusOK
	if len(errors) > 0 {
		status = http.StatusUnprocessableEntity
	}
	content := view.Page(title, view.Form(form))
	if view.IsHTMX(request) {
		content = view.Form(form)
	}
	err := view.Render(response, request, view.RenderOptions{
		Title:       title,
		Content:     content,
		CSRFToken:   CSRFToken(request),
		CachePolicy: view.NoStore,
		Status:      status,
	})
	if err != nil {
		slog.ErrorContext(request.Context(), "auth page render failed", "error", err)
	}
}

func setCSRFHeader(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("X-CSRF-Token", CSRFToken(request))
}

func redirect(response http.ResponseWriter, request *http.Request, target string) {
	if view.IsHTMX(request) {
		response.Header().Set("HX-Redirect", target)
		response.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(response, request, target, http.StatusSeeOther)
}
