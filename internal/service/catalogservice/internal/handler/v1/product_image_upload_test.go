package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/service"
	"github.com/labstack/echo/v5"
)

type uploadSessionStoreStub struct {
	user      *rpcinterceptor.AuthUser
	err       error
	lastToken string
	calls     int
}

func (s *uploadSessionStoreStub) GetSession(_ context.Context, token string) (*rpcinterceptor.AuthUser, error) {
	s.calls++
	s.lastToken = token
	return s.user, s.err
}

func (s *uploadSessionStoreStub) GetUserByID(context.Context, int64) (*rpcinterceptor.AuthUser, error) {
	return nil, errors.New("not implemented")
}

func (s *uploadSessionStoreStub) SaveSession(context.Context, string, *rpcinterceptor.AuthUser) error {
	return errors.New("not implemented")
}

type uploadLimiterStub struct {
	allowed bool
	err     error
	userID  int64
	calls   int
}

func (l *uploadLimiterStub) Allow(_ context.Context, userID int64) (bool, error) {
	l.calls++
	l.userID = userID
	return l.allowed, l.err
}

type uploadServiceStub struct {
	url      string
	err      error
	userID   int64
	declared string
	data     []byte
	calls    int
}

func (u *uploadServiceStub) Upload(
	_ context.Context,
	userID int64,
	picture io.ReadSeeker,
	declared string,
) (string, error) {
	u.calls++
	u.userID = userID
	u.declared = declared
	if picture != nil {
		_, _ = picture.Seek(0, io.SeekStart)
		u.data, _ = io.ReadAll(picture)
	}
	return u.url, u.err
}

func newUploadHandler(
	session *uploadSessionStoreStub,
	limiter *uploadLimiterStub,
	uploader *uploadServiceStub,
) *ProductImageUploadHandler {
	return &ProductImageUploadHandler{
		SessionStore:    session,
		Limiter:         limiter,
		Uploader:        uploader,
		MaxRequestBytes: DefaultProductImageMaxBytes + 2*1024*1024,
		MaxImageBytes:   DefaultProductImageMaxBytes,
	}
}

func multipartUploadRequest(t *testing.T, field, filename, declared string, data []byte) *http.Request {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if filename == "" {
		if err := writer.WriteField(field, string(data)); err != nil {
			t.Fatalf("write form field: %v", err)
		}
	} else {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="`+field+`"; filename="`+filename+`"`)
		header.Set("Content-Type", declared)
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatalf("create multipart part: %v", err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatalf("write multipart file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, ProductImageUploadPath, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer session-token")
	return req
}

func invokeUpload(t *testing.T, h *ProductImageUploadHandler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	return response
}

func responseCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response JSON: %v; body=%q", err, response.Body.String())
	}
	return payload.Code
}

func TestProductImageUploadHandlerRejectsUnauthenticatedRequests(t *testing.T) {
	t.Parallel()

	session := &uploadSessionStoreStub{}
	limiter := &uploadLimiterStub{allowed: true}
	uploader := &uploadServiceStub{url: "https://cdn.example.com/products/image.jpg"}
	handler := newUploadHandler(session, limiter, uploader)

	req := multipartUploadRequest(t, "picture", "image.jpg", "image/jpeg", []byte("image"))
	req.Header.Del("Authorization")
	response := invokeUpload(t, handler, req)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if code := responseCode(t, response); code != "UPLOAD_UNAUTHENTICATED" {
		t.Fatalf("error code = %q, want UPLOAD_UNAUTHENTICATED", code)
	}
	if session.calls != 0 || limiter.calls != 0 || uploader.calls != 0 {
		t.Fatalf(
			"unauthenticated request reached dependencies: session=%d limiter=%d uploader=%d",
			session.calls,
			limiter.calls,
			uploader.calls,
		)
	}
}

func TestProductImageUploadHandlerRejectsInvalidSessionAndForbiddenRole(t *testing.T) {
	t.Parallel()

	t.Run("invalid session", func(t *testing.T) {
		session := &uploadSessionStoreStub{err: errors.New("expired")}
		handler := newUploadHandler(session, &uploadLimiterStub{allowed: true}, &uploadServiceStub{})
		response := invokeUpload(
			t,
			handler,
			multipartUploadRequest(t, "picture", "image.jpg", "image/jpeg", []byte("image")),
		)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
		}
	})

	t.Run("forbidden role", func(t *testing.T) {
		session := &uploadSessionStoreStub{user: &rpcinterceptor.AuthUser{UserID: 7, Role: "admin", Status: "active"}}
		limiter := &uploadLimiterStub{allowed: true}
		uploader := &uploadServiceStub{url: "https://cdn.example.com/products/image.jpg"}
		handler := newUploadHandler(session, limiter, uploader)
		response := invokeUpload(
			t,
			handler,
			multipartUploadRequest(t, "picture", "image.jpg", "image/jpeg", []byte("image")),
		)
		if response.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
		}
		if code := responseCode(t, response); code != "UPLOAD_FORBIDDEN" {
			t.Fatalf("error code = %q, want UPLOAD_FORBIDDEN", code)
		}
		if limiter.calls != 0 || uploader.calls != 0 {
			t.Fatalf("forbidden request reached dependencies: limiter=%d uploader=%d", limiter.calls, uploader.calls)
		}
	})
}

func TestProductImageUploadHandlerUsesBearerTokenAndUserID(t *testing.T) {
	t.Parallel()

	session := &uploadSessionStoreStub{user: &rpcinterceptor.AuthUser{UserID: 42, Role: "user", Status: "active"}}
	limiter := &uploadLimiterStub{allowed: true}
	uploader := &uploadServiceStub{url: "https://cdn.example.com/products/image.jpg"}
	handler := newUploadHandler(session, limiter, uploader)
	response := invokeUpload(
		t,
		handler,
		multipartUploadRequest(t, "picture", "image.jpg", "image/jpeg", []byte("image")),
	)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", response.Code, http.StatusOK, response.Body.String())
	}
	if session.lastToken != "session-token" || limiter.userID != 42 || uploader.userID != 42 {
		t.Fatalf(
			"identity propagation = token %q, limiter user %d, uploader user %d",
			session.lastToken,
			limiter.userID,
			uploader.userID,
		)
	}
}

func TestProductImageUploadHandlerRequiresPictureMultipartFile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		field string
		file  string
	}{
		{name: "missing picture", field: "other", file: ""},
		{name: "empty filename", field: "picture", file: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			session := &uploadSessionStoreStub{
				user: &rpcinterceptor.AuthUser{UserID: 1, Role: "user", Status: "active"},
			}
			limiter := &uploadLimiterStub{allowed: true}
			uploader := &uploadServiceStub{url: "https://cdn.example.com/products/image.jpg"}
			handler := newUploadHandler(session, limiter, uploader)
			request := multipartUploadRequest(t, testCase.field, testCase.file, "text/plain", nil)
			response := invokeUpload(t, handler, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
			if code := responseCode(t, response); code != "UPLOAD_MULTIPART_INVALID" {
				t.Fatalf("error code = %q, want UPLOAD_MULTIPART_INVALID", code)
			}
			if uploader.calls != 0 {
				t.Fatalf("uploader calls = %d, want 0", uploader.calls)
			}
		})
	}

	t.Run("wrong content type", func(t *testing.T) {
		session := &uploadSessionStoreStub{user: &rpcinterceptor.AuthUser{UserID: 1, Role: "user", Status: "active"}}
		handler := newUploadHandler(session, &uploadLimiterStub{allowed: true}, &uploadServiceStub{})
		request := httptest.NewRequest(http.MethodPost, ProductImageUploadPath, strings.NewReader("not multipart"))
		request.Header.Set("Authorization", "Bearer session-token")
		request.Header.Set("Content-Type", "application/octet-stream")
		response := invokeUpload(t, handler, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
		}
	})
}

func TestProductImageUploadHandlerRejectsDuplicatePictureFields(t *testing.T) {
	t.Parallel()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, name := range []string{"first.jpg", "second.jpg"} {
		part, err := writer.CreateFormFile("picture", name)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		_, _ = part.Write([]byte("image"))
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, ProductImageUploadPath, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer session-token")

	session := &uploadSessionStoreStub{user: &rpcinterceptor.AuthUser{UserID: 1, Role: "user", Status: "active"}}
	uploader := &uploadServiceStub{}
	response := invokeUpload(t, newUploadHandler(session, &uploadLimiterStub{allowed: true}, uploader), request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if uploader.calls != 0 {
		t.Fatalf("uploader calls = %d, want 0", uploader.calls)
	}
}

func TestProductImageUploadHandlerMapsLimiterOutcomes(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		allow  bool
		err    error
		status int
		code   string
	}{
		{name: "limited", allow: false, status: http.StatusTooManyRequests, code: "UPLOAD_RATE_LIMITED"},
		{name: "unavailable", allow: false, err: errors.New("redis down"), status: http.StatusServiceUnavailable, code: "UPLOAD_UNAVAILABLE"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			session := &uploadSessionStoreStub{
				user: &rpcinterceptor.AuthUser{UserID: 7, Role: "user", Status: "active"},
			}
			limiter := &uploadLimiterStub{allowed: testCase.allow, err: testCase.err}
			uploader := &uploadServiceStub{url: "https://cdn.example.com/products/image.jpg"}
			handler := newUploadHandler(session, limiter, uploader)
			response := invokeUpload(
				t,
				handler,
				multipartUploadRequest(t, "picture", "image.jpg", "image/jpeg", []byte("image")),
			)
			if response.Code != testCase.status {
				t.Fatalf("status = %d, want %d", response.Code, testCase.status)
			}
			if code := responseCode(t, response); code != testCase.code {
				t.Fatalf("error code = %q, want %q", code, testCase.code)
			}
			if uploader.calls != 0 {
				t.Fatalf("uploader calls = %d, want 0", uploader.calls)
			}
		})
	}
}

func TestProductImageUploadHandlerMapsUploaderErrors(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "unsupported", err: service.ErrImageUnsupported, status: http.StatusUnsupportedMediaType, code: "UPLOAD_UNSUPPORTED_TYPE"},
		{name: "invalid", err: service.ErrImageInvalid, status: http.StatusBadRequest, code: "UPLOAD_IMAGE_INVALID"},
		{name: "storage", err: service.ErrStorage, status: http.StatusServiceUnavailable, code: "UPLOAD_STORAGE_FAILED"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			session := &uploadSessionStoreStub{
				user: &rpcinterceptor.AuthUser{UserID: 7, Role: "user", Status: "active"},
			}
			uploader := &uploadServiceStub{err: testCase.err}
			handler := newUploadHandler(session, &uploadLimiterStub{allowed: true}, uploader)
			response := invokeUpload(
				t,
				handler,
				multipartUploadRequest(t, "picture", "image.jpg", "image/jpeg", []byte("image")),
			)
			if response.Code != testCase.status {
				t.Fatalf("status = %d, want %d", response.Code, testCase.status)
			}
			if code := responseCode(t, response); code != testCase.code {
				t.Fatalf("error code = %q, want %q", code, testCase.code)
			}
		})
	}
}

func TestProductImageUploadHandlerEnforcesImageByteBoundary(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		imageBytes int
		status     int
		calls      int
	}{
		{name: "exact max", imageBytes: int(DefaultProductImageMaxBytes), status: http.StatusOK, calls: 1},
		{name: "over max", imageBytes: int(DefaultProductImageMaxBytes) + 1, status: http.StatusRequestEntityTooLarge, calls: 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			session := &uploadSessionStoreStub{
				user: &rpcinterceptor.AuthUser{UserID: 7, Role: "user", Status: "active"},
			}
			uploader := &uploadServiceStub{url: "https://cdn.example.com/products/image.jpg"}
			handler := newUploadHandler(session, &uploadLimiterStub{allowed: true}, uploader)
			response := invokeUpload(
				t,
				handler,
				multipartUploadRequest(
					t,
					"picture",
					"image.jpg",
					"image/jpeg",
					bytes.Repeat([]byte{'x'}, testCase.imageBytes),
				),
			)
			if response.Code != testCase.status {
				t.Fatalf("status = %d, want %d", response.Code, testCase.status)
			}
			if uploader.calls != testCase.calls {
				t.Fatalf("uploader calls = %d, want %d", uploader.calls, testCase.calls)
			}
		})
	}
}

func TestProductImageUploadHandlerReturnsURLOnlyForValidPublicHTTPSURL(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		url    string
		status int
		code   string
	}{
		{name: "valid", url: "https://cdn.example.com/sast-shop/products/abc.jpg", status: http.StatusOK},
		{name: "embedded credentials", url: "https://user:secret@cdn.example.com/products/abc.jpg", status: http.StatusServiceUnavailable, code: "UPLOAD_STORAGE_FAILED"},
		{name: "query string", url: "https://cdn.example.com/products/abc.jpg?token=secret", status: http.StatusServiceUnavailable, code: "UPLOAD_STORAGE_FAILED"},
		{name: "insecure scheme", url: "http://cdn.example.com/products/abc.jpg", status: http.StatusServiceUnavailable, code: "UPLOAD_STORAGE_FAILED"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			session := &uploadSessionStoreStub{
				user: &rpcinterceptor.AuthUser{UserID: 7, Role: "user", Status: "active"},
			}
			uploader := &uploadServiceStub{url: testCase.url}
			handler := newUploadHandler(session, &uploadLimiterStub{allowed: true}, uploader)
			response := invokeUpload(
				t,
				handler,
				multipartUploadRequest(t, "picture", "image.jpg", "image/jpeg", []byte("image")),
			)
			if response.Code != testCase.status {
				t.Fatalf("status = %d, want %d; body=%q", response.Code, testCase.status, response.Body.String())
			}
			if testCase.code != "" {
				if code := responseCode(t, response); code != testCase.code {
					t.Fatalf("error code = %q, want %q", code, testCase.code)
				}
				return
			}
			var payload struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode success JSON: %v", err)
			}
			if payload.URL != testCase.url {
				t.Fatalf("url = %q, want %q", payload.URL, testCase.url)
			}
		})
	}
}

func TestRegisterProductImageUploadRegistersNativePostRoute(t *testing.T) {
	session := &uploadSessionStoreStub{user: &rpcinterceptor.AuthUser{UserID: 7, Role: "user", Status: "active"}}
	limiter := &uploadLimiterStub{allowed: true}
	uploader := &uploadServiceStub{url: "https://cdn.example.com/products/image.jpg"}
	e := echo.New()
	RegisterProductImageUpload(e, UploadDependencies{
		SessionStore: session,
		Limiter:      limiter,
		Uploader:     uploader,
	})
	response := httptest.NewRecorder()
	e.ServeHTTP(response, multipartUploadRequest(t, "picture", "image.jpg", "image/jpeg", []byte("image")))
	if response.Code != http.StatusOK {
		t.Fatalf("registered route status = %d, want 200; body=%q", response.Code, response.Body.String())
	}
}
