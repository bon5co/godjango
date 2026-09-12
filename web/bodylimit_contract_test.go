package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bon5co/godjango/web"
)

func limitedHandler(limits web.BodyLimits) http.Handler {
	return limits.Middleware()(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			if _, err := io.ReadAll(request.Body); err != nil {
				response.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			response.WriteHeader(http.StatusNoContent)
		}))
}

func postWithLength(path string, length int64) *http.Request {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
	request.ContentLength = length
	return request
}

func TestBodyLimitsApplyThePrefixCeilingAndTheDefaultElsewhere(t *testing.T) {
	handler := limitedHandler(web.BodyLimits{
		Default:  1 << 10,
		ByPrefix: map[string]int64{"/api/uploads": 1 << 20},
	})

	for _, testCase := range []struct {
		path   string
		length int64
		status int
	}{
		{"/api/uploads", 1 << 15, http.StatusNoContent},
		{"/api/uploads/photo", 1 << 15, http.StatusNoContent},
		{"/api/uploads", (1 << 20) + 1, http.StatusRequestEntityTooLarge},
		{"/accounts/login/", 1 << 15, http.StatusRequestEntityTooLarge},
		{"/accounts/login/", 500, http.StatusNoContent},
		// A prefix matches at a segment boundary, never in the middle of one.
		{"/api/uploadsomething", 1 << 15, http.StatusRequestEntityTooLarge},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, postWithLength(testCase.path, testCase.length))
		if response.Code != testCase.status {
			t.Errorf("POST %s with %d bytes = %d, want %d",
				testCase.path, testCase.length, response.Code, testCase.status)
		}
	}
}

func TestBodyLimitsPreferTheLongestMatchingPrefix(t *testing.T) {
	// Map iteration order is random, so a general rule and a specific one must
	// resolve by specificity rather than by which came out of the map first.
	handler := limitedHandler(web.BodyLimits{
		Default: 1 << 10,
		ByPrefix: map[string]int64{
			"/api":         1 << 12,
			"/api/uploads": 1 << 20,
		},
	})
	for i := 0; i < 20; i++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, postWithLength("/api/uploads", 1<<15))
		if response.Code != http.StatusNoContent {
			t.Fatalf("the narrower prefix lost to the wider one: status %d", response.Code)
		}
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, postWithLength("/api/status", 1<<15))
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("the wider prefix did not apply to its own routes: status %d", response.Code)
		}
	}
}

func TestBodyLimitsCapStreamingBodiesThatDeclareNoLength(t *testing.T) {
	handler := limitedHandler(web.BodyLimits{Default: 5})
	// An undeclared length reaches the handler and is cut off by the reader,
	// so a chunked body cannot walk past the limit.
	request := httptest.NewRequest(http.MethodPost, "/",
		struct{ io.Reader }{strings.NewReader("123456")})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", response.Code)
	}
}

func TestBodyLimitsMatchTheCleanedPath(t *testing.T) {
	// "/api/uploads/../../admin" is a request for /admin, and must not borrow
	// the upload prefix's ceiling.
	handler := limitedHandler(web.BodyLimits{
		Default:  1 << 10,
		ByPrefix: map[string]int64{"/api/uploads": 1 << 20},
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, postWithLength("/api/uploads/../../admin", 1<<15))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a traversal borrowed the upload limit: status %d", response.Code)
	}
}

func TestBodyLimitsCallTheApplicationsOwnRejection(t *testing.T) {
	// An application serving somebody else's wire contract answers in that
	// contract's shape, and has to know which limit was exceeded to say so.
	var reportedLimit int64
	handler := limitedHandler(web.BodyLimits{
		Default:  1 << 10,
		ByPrefix: map[string]int64{"/v1.0": 22 << 20},
		Reject: func(response http.ResponseWriter, _ *http.Request, limit int64) {
			reportedLimit = limit
			response.WriteHeader(http.StatusBadRequest)
			_, _ = response.Write([]byte(`{"errors":[{"code":"file_too_large"}]}`))
		},
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, postWithLength("/v1.0/removebg", (22<<20)+1))
	if response.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want the application's own 400", response.Code)
	}
	if !strings.Contains(response.Body.String(), "file_too_large") {
		t.Errorf("body = %q, want the application's own error", response.Body.String())
	}
	if reportedLimit != 22<<20 {
		t.Errorf("rejection was told the limit was %d, want %d", reportedLimit, 22<<20)
	}
}

func TestBodyLimitsRefuseAConfigurationThatCannotMeanWhatItSays(t *testing.T) {
	for name, limits := range map[string]web.BodyLimits{
		"no default":       {ByPrefix: map[string]int64{"/api": 1 << 20}},
		"negative default": {Default: -1},
		"rootless prefix":  {Default: 1 << 10, ByPrefix: map[string]int64{"api": 1 << 20}},
		"empty prefix":     {Default: 1 << 10, ByPrefix: map[string]int64{"": 1 << 20}},
		"zero for prefix":  {Default: 1 << 10, ByPrefix: map[string]int64{"/api": 0}},
	} {
		if err := limits.Validate(); err == nil {
			t.Errorf("%s: accepted, want an error", name)
		}
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: Middleware served an invalid configuration", name)
				}
			}()
			_ = limits.Middleware()
		}()
	}
}

func TestLimitForAnswersWithoutRestatingTheConfiguration(t *testing.T) {
	limits := web.BodyLimits{
		Default: 1 << 10,
		ByPrefix: map[string]int64{
			"/api":         1 << 12,
			"/api/uploads": 1 << 20,
		},
	}
	for path, want := range map[string]int64{
		"/":                  1 << 10,
		"/api":               1 << 12,
		"/api/status":        1 << 12,
		"/api/uploads":       1 << 20,
		"/api/uploads/photo": 1 << 20,
		"/apiary":            1 << 10,
	} {
		if got := limits.LimitFor(path); got != want {
			t.Errorf("LimitFor(%q) = %d, want %d", path, got, want)
		}
	}
}
