package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/service"
)

const (
	ProductImageUploadPath       = "/api/uploads/product-image"
	ProductImageUploadUploadPath = ProductImageUploadPath // compatibility alias

	DefaultProductImageMaxBytes   int64 = 10 * 1024 * 1024
	DefaultProductImageMaxRequest int64 = DefaultProductImageMaxBytes + 2*1024*1024
)

var (
	ErrMultipartInvalid = errors.New("invalid multipart request")
	ErrRequestTooLarge  = errors.New("request too large")
	ErrPictureTooLarge  = errors.New("picture too large")
	ErrPictureEmpty     = errors.New("picture is empty")
)

type UploadLimiter interface {
	Allow(context.Context, int64) (bool, error)
}

type UploadService interface {
	Upload(context.Context, int64, io.ReadSeeker, string) (string, error)
}

type ProductImageUploadHandler struct {
	SessionStore    rpcinterceptor.SessionStore
	Limiter         UploadLimiter
	Uploader        UploadService
	AllowedRoles    map[string]struct{}
	MaxRequestBytes int64
	MaxImageBytes   int64
}

func (h *ProductImageUploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Handle(w, r)
}

func (h *ProductImageUploadHandler) Handle(w http.ResponseWriter, r *http.Request) {
	setResponseHeaders(w)
	if r == nil {
		writeUploadError(w, http.StatusBadRequest, "UPLOAD_MULTIPART_INVALID")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeUploadError(w, http.StatusMethodNotAllowed, "UPLOAD_METHOD_NOT_ALLOWED")
		return
	}

	maxImageBytes := h.MaxImageBytes
	if maxImageBytes <= 0 {
		maxImageBytes = DefaultProductImageMaxBytes
	}
	maxRequestBytes := h.MaxRequestBytes
	if maxRequestBytes <= 0 {
		maxRequestBytes = maxImageBytes + 2*1024*1024
	}
	// Cap the complete request before multipart parsing. readPicture applies a
	// second streaming guard so direct calls are bounded as well.
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	}

	user, status, ok := h.authenticate(r)
	if !ok {
		writeUploadError(w, status, "UPLOAD_UNAUTHENTICATED")
		return
	}
	if !h.allowedRole(user.Role) {
		writeUploadError(w, http.StatusForbidden, "UPLOAD_FORBIDDEN")
		return
	}
	if h.Limiter == nil {
		writeUploadError(w, http.StatusServiceUnavailable, "UPLOAD_UNAVAILABLE")
		return
	}
	allowed, err := h.Limiter.Allow(r.Context(), user.UserID)
	if err != nil {
		writeUploadError(w, http.StatusServiceUnavailable, "UPLOAD_UNAVAILABLE")
		return
	}
	if !allowed {
		writeUploadError(w, http.StatusTooManyRequests, "UPLOAD_RATE_LIMITED")
		return
	}
	if h.Uploader == nil {
		writeUploadError(w, http.StatusServiceUnavailable, "UPLOAD_UNAVAILABLE")
		return
	}

	picture, declared, err := readPicture(r, maxRequestBytes, maxImageBytes)
	if err != nil {
		writeUploadReadError(w, err)
		return
	}
	defer func() {
		_ = picture.Close()
		_ = os.Remove(picture.Name())
	}()

	publicURL, err := h.Uploader.Upload(r.Context(), user.UserID, picture, declared)
	if err != nil {
		status, code := mapUploadError(err)
		writeUploadError(w, status, code)
		return
	}
	if !service.ValidatePublicURL(publicURL) {
		writeUploadError(w, http.StatusServiceUnavailable, "UPLOAD_STORAGE_FAILED")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		URL string `json:"url"`
	}{URL: publicURL})
}

func (h *ProductImageUploadHandler) authenticate(r *http.Request) (*rpcinterceptor.AuthUser, int, bool) {
	if h.SessionStore == nil || r == nil {
		return nil, http.StatusUnauthorized, false
	}
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		return nil, http.StatusUnauthorized, false
	}
	user, err := h.SessionStore.GetSession(r.Context(), token)
	if err != nil || user == nil || user.UserID <= 0 {
		return nil, http.StatusUnauthorized, false
	}
	if status := strings.ToLower(strings.TrimSpace(user.Status)); status != "active" {
		return nil, http.StatusUnauthorized, false
	}
	return user, 0, true
}

func bearerToken(value string) (string, bool) {
	if !strings.HasPrefix(value, "Bearer ") {
		return "", false
	}
	token := strings.TrimPrefix(value, "Bearer ")
	if token == "" || strings.TrimSpace(token) != token || strings.ContainsAny(token, " \t\r\n") {
		return "", false
	}
	return token, true
}

func (h *ProductImageUploadHandler) allowedRole(role string) bool {
	roles := h.AllowedRoles
	if roles == nil {
		roles = map[string]struct{}{"user": {}}
	}
	_, ok := roles[strings.ToLower(strings.TrimSpace(role))]
	return ok
}

func setResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func writeUploadError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code string `json:"code"`
	}{Code: code})
}

func writeUploadReadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrRequestTooLarge), errors.Is(err, ErrPictureTooLarge):
		writeUploadError(w, http.StatusRequestEntityTooLarge, "UPLOAD_TOO_LARGE")
	case errors.Is(err, service.ErrImageUnsupported):
		writeUploadError(w, http.StatusUnsupportedMediaType, "UPLOAD_UNSUPPORTED_TYPE")
	case errors.Is(err, service.ErrImageTooLarge):
		writeUploadError(w, http.StatusRequestEntityTooLarge, "UPLOAD_TOO_LARGE")
	case errors.Is(err, service.ErrQuotaExceeded):
		writeUploadError(w, http.StatusRequestEntityTooLarge, "UPLOAD_QUOTA_EXCEEDED")
	case errors.Is(err, ErrPictureEmpty), errors.Is(err, service.ErrImageInvalid):
		writeUploadError(w, http.StatusBadRequest, "UPLOAD_IMAGE_INVALID")
	default:
		writeUploadError(w, http.StatusBadRequest, "UPLOAD_MULTIPART_INVALID")
	}
}

func mapUploadError(err error) (int, string) {
	switch {
	case errors.Is(err, service.ErrImageUnsupported):
		return http.StatusUnsupportedMediaType, "UPLOAD_UNSUPPORTED_TYPE"
	case errors.Is(err, service.ErrImageTooLarge):
		return http.StatusRequestEntityTooLarge, "UPLOAD_TOO_LARGE"
	case errors.Is(err, service.ErrQuotaExceeded):
		return http.StatusRequestEntityTooLarge, "UPLOAD_QUOTA_EXCEEDED"
	case errors.Is(err, service.ErrImageInvalid):
		return http.StatusBadRequest, "UPLOAD_IMAGE_INVALID"
	case errors.Is(err, service.ErrQuotaUnavailable):
		return http.StatusServiceUnavailable, "UPLOAD_UNAVAILABLE"
	default:
		return http.StatusServiceUnavailable, "UPLOAD_STORAGE_FAILED"
	}
}

type requestLimitReader struct {
	r        io.Reader
	limit    int64
	read     int64
	exceeded bool
}

func (r *requestLimitReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.exceeded {
		return 0, ErrRequestTooLarge
	}
	remaining := r.limit - r.read
	if remaining < 0 {
		r.exceeded = true
		return 0, ErrRequestTooLarge
	}
	readLen := len(p)
	if int64(readLen) > remaining+1 {
		readLen = int(remaining + 1)
	}
	if readLen <= 0 {
		readLen = 1
	}
	n, err := r.r.Read(p[:readLen])
	r.read += int64(n)
	if r.read > r.limit {
		r.exceeded = true
		return n, ErrRequestTooLarge
	}
	if isRequestTooLarge(err) {
		r.exceeded = true
		return n, ErrRequestTooLarge
	}
	return n, err
}

func isRequestTooLarge(err error) bool {
	if errors.Is(err, ErrRequestTooLarge) {
		return true
	}
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
}

func readPicture(r *http.Request, maxRequestBytes, maxImageBytes int64) (*os.File, string, error) {
	if r == nil || r.Body == nil {
		return nil, "", ErrMultipartInvalid
	}
	if maxImageBytes <= 0 {
		maxImageBytes = DefaultProductImageMaxBytes
	}
	if maxRequestBytes <= 0 {
		maxRequestBytes = maxImageBytes + 2*1024*1024
	}
	if r.ContentLength > maxRequestBytes {
		return nil, "", ErrRequestTooLarge
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") || params["boundary"] == "" {
		return nil, "", ErrMultipartInvalid
	}

	limitedBody := &requestLimitReader{r: r.Body, limit: maxRequestBytes}
	reader := multipart.NewReader(limitedBody, params["boundary"])
	var picture *os.File
	var declared string
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if isRequestTooLarge(err) || limitedBody.exceeded {
				return closePictureOnError(picture, ErrRequestTooLarge)
			}
			return closePictureOnError(picture, ErrMultipartInvalid)
		}
		if part.FormName() != "picture" || part.FileName() == "" || picture != nil {
			_ = part.Close()
			return closePictureOnError(picture, ErrMultipartInvalid)
		}
		declared = part.Header.Get("Content-Type")
		if declared != "" {
			declared, _, err = mime.ParseMediaType(declared)
			if err != nil {
				_ = part.Close()
				return closePictureOnError(picture, ErrMultipartInvalid)
			}
		}
		picture, err = os.CreateTemp("", "sast-product-image-*")
		if err != nil {
			_ = part.Close()
			return nil, "", ErrMultipartInvalid
		}
		n, copyErr := io.Copy(picture, io.LimitReader(part, maxImageBytes+1))
		_ = part.Close()
		if n == 0 {
			return closePictureOnError(picture, ErrPictureEmpty)
		}
		if n > maxImageBytes {
			return closePictureOnError(picture, ErrPictureTooLarge)
		}
		if copyErr != nil {
			if isRequestTooLarge(copyErr) || limitedBody.exceeded {
				return closePictureOnError(picture, ErrRequestTooLarge)
			}
			return closePictureOnError(picture, ErrMultipartInvalid)
		}
	}
	if picture == nil {
		return nil, "", ErrMultipartInvalid
	}
	if limitedBody.exceeded {
		return closePictureOnError(picture, ErrRequestTooLarge)
	}
	if _, err := picture.Seek(0, io.SeekStart); err != nil {
		return closePictureOnError(picture, ErrMultipartInvalid)
	}
	return picture, declared, nil
}

func closePictureOnError(picture *os.File, err error) (*os.File, string, error) {
	if picture != nil {
		_ = picture.Close()
		_ = os.Remove(picture.Name())
	}
	return nil, "", err
}
