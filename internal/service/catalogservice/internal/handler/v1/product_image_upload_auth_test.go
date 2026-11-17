package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
)

type uploadAuthDependencies struct {
	user         *interceptor.AuthUser
	limitedUser  int64
	uploadedUser int64
}

func (d *uploadAuthDependencies) GetSession(context.Context, string) (*interceptor.AuthUser, error) {
	return d.user, nil
}

func (d *uploadAuthDependencies) GetUserByID(context.Context, int64) (*interceptor.AuthUser, error) {
	return d.user, nil
}

func (*uploadAuthDependencies) SaveSession(context.Context, string, *interceptor.AuthUser) error {
	return nil
}

func (d *uploadAuthDependencies) Allow(_ context.Context, userID int64) (bool, error) {
	d.limitedUser = userID
	return true, nil
}

func (d *uploadAuthDependencies) Upload(_ context.Context, userID int64, _ io.ReadSeeker, _ string) (string, error) {
	d.uploadedUser = userID
	return "https://example.test/uploaded.png", nil
}

func TestProductImageUploadMemberAuthorization(t *testing.T) {
	active := func(role string) *interceptor.AuthUser {
		return &interceptor.AuthUser{UserID: 42, Role: role, Status: "active"}
	}
	cases := []struct {
		name    string
		user    *interceptor.AuthUser
		roles   map[string]struct{}
		noToken bool
		status  int
		code    string
	}{
		{name: "user", user: active("user"), status: http.StatusOK},
		{name: "admin", user: active("admin"), status: http.StatusOK},
		{name: "superadmin", user: active("superadmin"), status: http.StatusOK},
		{name: "normalized_admin", user: active(" ADMIN "), status: http.StatusOK},
		{name: "missing_session", status: http.StatusUnauthorized, code: "UPLOAD_UNAUTHENTICATED"},
		{
			name:    "missing_token",
			user:    active("admin"),
			noToken: true,
			status:  http.StatusUnauthorized,
			code:    "UPLOAD_UNAUTHENTICATED",
		},
		{
			name:   "zero_id",
			user:   &interceptor.AuthUser{Role: "admin", Status: "active"},
			status: http.StatusUnauthorized,
			code:   "UPLOAD_UNAUTHENTICATED",
		},
		{
			name:   "restricted_user",
			user:   &interceptor.AuthUser{UserID: 42, Role: "user", Status: "restricted"},
			status: http.StatusUnauthorized,
			code:   "UPLOAD_UNAUTHENTICATED",
		},
		{
			name:   "banned_admin",
			user:   &interceptor.AuthUser{UserID: 42, Role: "admin", Status: "banned"},
			status: http.StatusUnauthorized,
			code:   "UPLOAD_UNAUTHENTICATED",
		},
		{
			name:   "deleted_superadmin",
			user:   &interceptor.AuthUser{UserID: 42, Role: "superadmin", Status: "deleted"},
			status: http.StatusUnauthorized,
			code:   "UPLOAD_UNAUTHENTICATED",
		},
		{name: "missing_role", user: active(""), status: http.StatusForbidden, code: "UPLOAD_FORBIDDEN"},
		{name: "unknown_role", user: active("guest"), status: http.StatusForbidden, code: "UPLOAD_FORBIDDEN"},
		{
			name:   "explicit_user_only",
			user:   active("admin"),
			roles:  map[string]struct{}{"user": {}},
			status: http.StatusForbidden,
			code:   "UPLOAD_FORBIDDEN",
		},
		{name: "explicit_admin", user: active("admin"), roles: map[string]struct{}{"admin": {}}, status: http.StatusOK},
		{
			name:   "explicit_empty",
			user:   active("user"),
			roles:  map[string]struct{}{},
			status: http.StatusForbidden,
			code:   "UPLOAD_FORBIDDEN",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := &uploadAuthDependencies{user: tc.user}
			handler := &ProductImageUploadHandler{
				SessionStore: deps, Limiter: deps, Uploader: deps, AllowedRoles: tc.roles,
			}
			req := newImageUploadRequest(t)
			if !tc.noToken {
				req.Header.Set("Authorization", "Bearer test-token")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			var payload struct {
				URL  string `json:"url"`
				Code string `json:"code"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if tc.status == http.StatusOK {
				if payload.URL != "https://example.test/uploaded.png" || deps.limitedUser != 42 ||
					deps.uploadedUser != 42 {
					t.Fatalf("upload did not use the authenticated user's quota and storage: %+v %+v", payload, deps)
				}
			} else if payload.Code != tc.code || deps.limitedUser != 0 || deps.uploadedUser != 0 {
				t.Fatalf("rejected request reached upload dependencies or returned wrong error: %+v %+v", payload, deps)
			}
		})
	}
}

func newImageUploadRequest(t *testing.T) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("picture", "logo.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(part, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, ProductImageUploadPath, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}
