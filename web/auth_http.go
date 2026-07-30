package web

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/bon5co/godjango/auth"
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
	setCSRFHeader(response, request)
	next := request.URL.Query().Get("next")
	_, _ = fmt.Fprintf(
		response,
		`<!doctype html><form method="post"><input type="hidden" name="csrf_token" value="%s"><input name="username"><input type="password" name="password"><input type="hidden" name="next" value="%s"></form>`,
		html.EscapeString(CSRFToken(request)),
		html.EscapeString(next),
	)
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
		response.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprintf(
			response,
			`<!doctype html><input name="username" value="%s"><p>%s</p>`,
			html.EscapeString(form.Value("username")),
			html.EscapeString(strings.Join(form.Errors()[""], " ")),
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
	http.Redirect(response, request, target, http.StatusSeeOther)
}

func (handlers *AuthHandlers) logout(response http.ResponseWriter, request *http.Request) {
	if err := auth.Logout(SessionFromRequest(request)); err != nil {
		WriteError(response, request, http.StatusInternalServerError, "session_unavailable")
		return
	}
	http.Redirect(response, request, "/", http.StatusSeeOther)
}

func (handlers *AuthHandlers) passwordChangeForm(
	response http.ResponseWriter,
	request *http.Request,
) {
	setCSRFHeader(response, request)
	_, _ = fmt.Fprintf(
		response,
		`<!doctype html><form method="post"><input type="hidden" name="csrf_token" value="%s"><input type="password" name="old_password"><input type="password" name="new_password1"><input type="password" name="new_password2"></form>`,
		html.EscapeString(CSRFToken(request)),
	)
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
		response.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprint(response, "password change was not accepted")
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
	http.Redirect(response, request, "/accounts/password-change/done/", http.StatusSeeOther)
}

func (handlers *AuthHandlers) passwordResetForm(
	response http.ResponseWriter,
	request *http.Request,
) {
	setCSRFHeader(response, request)
	_, _ = fmt.Fprintf(
		response,
		`<!doctype html><form method="post"><input type="hidden" name="csrf_token" value="%s"><input name="email"></form>`,
		html.EscapeString(CSRFToken(request)),
	)
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
		response.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprintf(
			response,
			`<!doctype html><input name="email" value="%s"><p>Enter a valid email address.</p>`,
			html.EscapeString(form.Value("email")),
		)
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
	http.Redirect(
		response,
		request,
		"/accounts/password-reset/done/",
		http.StatusSeeOther,
	)
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
	_, _ = fmt.Fprintf(
		response,
		`<!doctype html><form method="post"><input type="hidden" name="csrf_token" value="%s"><input type="password" name="new_password1"><input type="password" name="new_password2"></form>`,
		html.EscapeString(CSRFToken(request)),
	)
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
		response.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprint(response, "password reset was not accepted")
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
	http.Redirect(
		response,
		request,
		"/accounts/password-reset/complete/",
		http.StatusSeeOther,
	)
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

func setCSRFHeader(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("X-CSRF-Token", CSRFToken(request))
}
