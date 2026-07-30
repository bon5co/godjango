package accounts

import (
	"net/http"

	"github.com/a-h/templ"
	"github.com/bon5co/godjango/auth"
	"github.com/bon5co/godjango/web"
	"github.com/bon5co/godjango/web/view"
	"github.com/go-chi/chi/v5"
)

const (
	publishPermission = auth.Permission("library.publish_book")
	reviewPermission  = auth.Permission("library.review_book")
)

type runtime struct {
	services web.RuntimeServices
}

func (*App) RoutesWithServices(router chi.Router, services web.RuntimeServices) {
	current := runtime{services: services}
	router.Get("/", current.home)
	router.Get("/register/", current.registrationForm)
	router.Post("/register/", current.register)
	router.With(web.RequirePermission(publishPermission)).Get(
		"/publish/",
		current.publish,
	)
	router.With(web.RequirePermission(reviewPermission)).Get(
		"/review/",
		current.review,
	)
}

func (current runtime) home(response http.ResponseWriter, request *http.Request) {
	user, authenticated := web.CurrentUser(request)
	if !authenticated {
		current.render(response, request, http.StatusOK, "Bookshelf",
			view.Page("Bookshelf",
				view.Paragraph("Anonymous"),
				view.Link("/register/", "Register"),
				view.Link("/accounts/login/", "Sign in"),
			),
		)
		return
	}
	current.render(response, request, http.StatusOK, "Bookshelf",
		view.Page("Bookshelf",
			view.Paragraph("Signed in as "+user.Username),
			view.Link("/publish/", "Publish"),
			view.Link("/review/", "Review"),
			view.Link("/accounts/password-change/", "Change password"),
			view.Form(view.FormData{
				Action:      "/accounts/logout/",
				Method:      http.MethodPost,
				CSRFToken:   web.CSRFToken(request),
				SubmitLabel: "Sign out",
			}),
		),
	)
}

func (current runtime) registrationForm(
	response http.ResponseWriter,
	request *http.Request,
) {
	current.renderRegistration(response, request, "", "", nil)
}

func (current runtime) register(response http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		web.WriteError(response, request, http.StatusBadRequest, "invalid_form")
		return
	}
	form := web.NewForm(request.PostForm)
	form.Required("username", "email", "password1", "password2")
	form.Email("email")
	form.MinLength("password1", 8)
	if form.Value("password1") != form.Value("password2") {
		form.AddError("password2", "The two password fields did not match.")
	}
	if !form.Valid() {
		current.renderRegistration(
			response,
			request,
			form.Value("username"),
			form.Value("email"),
			form.Errors(),
		)
		return
	}
	password := form.Value("password1")
	user, err := current.services.Users.CreateUser(
		request.Context(),
		auth.CreateUserOptions{
			Username: form.Value("username"),
			Email:    form.Value("email"),
			Password: &password,
		},
	)
	if err != nil {
		form.AddError("username", "That username is not available.")
		current.renderRegistration(
			response,
			request,
			form.Value("username"),
			form.Value("email"),
			form.Errors(),
		)
		return
	}
	if err := current.services.Login(request, user); err != nil {
		web.WriteError(response, request, http.StatusInternalServerError, "session_unavailable")
		return
	}
	redirect(response, request, "/")
}

func (current runtime) renderRegistration(
	response http.ResponseWriter,
	request *http.Request,
	username string,
	email string,
	errors map[string][]string,
) {
	form := view.Form(view.FormData{
		Action:    "/register/",
		Method:    http.MethodPost,
		CSRFToken: web.CSRFToken(request),
		Fields: []view.Field{
			{
				Name:         "username",
				Label:        "Username",
				Value:        username,
				Autocomplete: "username",
				Errors:       errors["username"],
			},
			{
				Name:         "email",
				Label:        "Email address",
				Type:         "email",
				Value:        email,
				Autocomplete: "email",
				Errors:       errors["email"],
			},
			{
				Name:         "password1",
				Label:        "Password",
				Type:         "password",
				Autocomplete: "new-password",
				Errors:       errors["password1"],
			},
			{
				Name:         "password2",
				Label:        "Confirm password",
				Type:         "password",
				Autocomplete: "new-password",
				Errors:       errors["password2"],
			},
		},
		SubmitLabel: "Register",
	})
	content := view.Page("Register", form)
	if view.IsHTMX(request) {
		content = form
	}
	status := http.StatusOK
	if len(errors) > 0 {
		status = http.StatusUnprocessableEntity
	}
	current.render(response, request, status, "Register", content)
}

func (current runtime) publish(response http.ResponseWriter, request *http.Request) {
	current.render(response, request, http.StatusOK, "Publish",
		view.Page("Publish", view.Paragraph("Publishing allowed.")),
	)
}

func (current runtime) review(response http.ResponseWriter, request *http.Request) {
	current.render(response, request, http.StatusOK, "Review",
		view.Page("Review", view.Paragraph("Review allowed.")),
	)
}

func (current runtime) render(
	response http.ResponseWriter,
	request *http.Request,
	status int,
	title string,
	content templ.Component,
) {
	_ = view.Render(response, request, view.RenderOptions{
		Title:       title,
		Content:     content,
		CSRFToken:   web.CSRFToken(request),
		CachePolicy: view.NoStore,
		Status:      status,
	})
}

func redirect(response http.ResponseWriter, request *http.Request, target string) {
	if view.IsHTMX(request) {
		response.Header().Set("HX-Redirect", target)
		response.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(response, request, target, http.StatusSeeOther)
}
