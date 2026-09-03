package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type objectStoreStub struct {
	putErr       error
	deleteErr    error
	publicURLErr error
	probeErr     error
	publicURL    string

	putCalls    int
	deleteCalls int
	probeCalls  int
	putKey      string
	putBody     []byte
	putSize     int64
	putType     string
	deleteKey   string
	probeURL    string
	probeType   string
}

func (s *objectStoreStub) Put(_ context.Context, key string, body io.Reader, size int64, contentType string) error {
	s.putCalls++
	s.putKey = key
	s.putSize = size
	s.putType = contentType
	s.putBody, _ = io.ReadAll(body)
	return s.putErr
}

func (s *objectStoreStub) Delete(_ context.Context, key string) error {
	s.deleteCalls++
	s.deleteKey = key
	return s.deleteErr
}

func (s *objectStoreStub) PublicURL(string) (string, error) {
	return s.publicURL, s.publicURLErr
}

func (s *objectStoreStub) ProbePublic(_ context.Context, url, contentType string) error {
	s.probeCalls++
	s.probeURL = url
	s.probeType = contentType
	return s.probeErr
}

func (s *objectStoreStub) Ready(context.Context) error { return nil }

func TestProductImageUploadServiceStoresProcessedImageAndProbesURL(t *testing.T) {
	t.Parallel()

	store := &objectStoreStub{publicURL: "https://cdn.example.com/sast-shop/products/abc.jpg"}
	uploader := NewProductImageUploadService(store, "sast-shop/products", defaultImageLimits())
	input := imageFixture(t, "jpeg")

	got, err := uploader.Upload(context.Background(), 42, bytes.NewReader(input), "image/jpeg")
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if got != store.publicURL {
		t.Fatalf("URL = %q, want %q", got, store.publicURL)
	}
	if store.putCalls != 1 || store.probeCalls != 1 || store.deleteCalls != 0 {
		t.Fatalf("store calls = put:%d probe:%d delete:%d, want 1,1,0", store.putCalls, store.probeCalls, store.deleteCalls)
	}
	if !strings.HasPrefix(store.putKey, "sast-shop/products/") || !strings.HasSuffix(store.putKey, ".jpg") {
		t.Fatalf("object key = %q, want generated .jpg key under prefix", store.putKey)
	}
	if store.putType != "image/jpeg" || store.probeType != "image/jpeg" {
		t.Fatalf("content type = put:%q probe:%q, want image/jpeg", store.putType, store.probeType)
	}
	if int64(len(store.putBody)) != store.putSize || len(store.putBody) == 0 {
		t.Fatalf("stored body size = %d (declared %d), want non-empty equal sizes", len(store.putBody), store.putSize)
	}
}

func TestProductImageUploadServiceDeletesObjectWhenPublicURLFails(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name      string
		publicURL string
		urlErr    error
		probeErr  error
	}{
		{name: "public URL error", urlErr: errors.New("url unavailable")},
		{name: "credentials in URL", publicURL: "https://user:secret@cdn.example.com/products/abc.jpg"},
		{name: "query in URL", publicURL: "https://cdn.example.com/products/abc.jpg?token=secret"},
		{name: "probe error", publicURL: "https://cdn.example.com/products/abc.jpg", probeErr: errors.New("not readable")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := &objectStoreStub{publicURL: testCase.publicURL, publicURLErr: testCase.urlErr, probeErr: testCase.probeErr}
			uploader := NewProductImageUploadService(store, "sast-shop/products", defaultImageLimits())
			_, err := uploader.Upload(context.Background(), 42, bytes.NewReader(imageFixture(t, "jpeg")), "image/jpeg")
			if !errors.Is(err, ErrStorage) {
				t.Fatalf("Upload() error = %v, want ErrStorage", err)
			}
			if store.putCalls != 1 || store.deleteCalls != 1 {
				t.Fatalf("store calls = put:%d delete:%d, want 1,1", store.putCalls, store.deleteCalls)
			}
			if store.deleteKey != store.putKey {
				t.Fatalf("deleted key = %q, put key = %q", store.deleteKey, store.putKey)
			}
			if testCase.probeErr == nil && store.probeCalls != 0 && testCase.urlErr != nil {
				t.Fatalf("ProbePublic called after PublicURL error")
			}
		})
	}
}

func TestProductImageUploadServiceDoesNotStoreInvalidInput(t *testing.T) {
	t.Parallel()

	store := &objectStoreStub{publicURL: "https://cdn.example.com/products/abc.jpg"}
	uploader := NewProductImageUploadService(store, "sast-shop/products", defaultImageLimits())

	if _, err := uploader.Upload(context.Background(), 42, bytes.NewReader([]byte("not an image")), "image/jpeg"); !errors.Is(err, ErrImageUnsupported) {
		t.Fatalf("invalid input error = %v, want ErrImageUnsupported", err)
	}
	if store.putCalls != 0 || store.deleteCalls != 0 {
		t.Fatalf("store calls = put:%d delete:%d, want 0,0", store.putCalls, store.deleteCalls)
	}
}

func TestProductImageUploadServiceCleansUpAfterPutFailure(t *testing.T) {
	store := &objectStoreStub{
		putErr:    errors.New("provider write failed"),
		publicURL: "https://cdn.example.com/products/abc.jpg",
	}
	uploader := NewProductImageUploadService(store, "sast-shop/products", defaultImageLimits())
	_, err := uploader.Upload(context.Background(), 42, bytes.NewReader(imageFixture(t, "jpeg")), "image/jpeg")
	if !errors.Is(err, ErrStorage) {
		t.Fatalf("Upload() error = %v, want ErrStorage", err)
	}
	if store.deleteCalls != 1 || store.deleteKey != store.putKey {
		t.Fatalf("cleanup calls = %d key=%q, want one delete of %q", store.deleteCalls, store.deleteKey, store.putKey)
	}
}

func TestProductImageUploadServiceRejectsInvalidUserOrStore(t *testing.T) {
	t.Parallel()

	input := imageFixture(t, "jpeg")
	store := &objectStoreStub{publicURL: "https://cdn.example.com/products/abc.jpg"}
	uploader := NewProductImageUploadService(store, "sast-shop/products", defaultImageLimits())
	if _, err := uploader.Upload(context.Background(), 0, bytes.NewReader(input), "image/jpeg"); !errors.Is(err, ErrStorage) {
		t.Fatalf("zero user ID error = %v, want ErrStorage", err)
	}
	if store.putCalls != 0 {
		t.Fatalf("store Put calls = %d, want 0", store.putCalls)
	}

	noStore := NewProductImageUploadService(nil, "sast-shop/products", defaultImageLimits())
	if _, err := noStore.Upload(context.Background(), 1, bytes.NewReader(input), "image/jpeg"); !errors.Is(err, ErrStorage) {
		t.Fatalf("nil store error = %v, want ErrStorage", err)
	}
}

func TestValidatePublicURLRequiresCredentialFreeHTTPS(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"https://cdn.example.com/image.jpg":                    true,
		"https://cdn.example.com/path/image.jpg?token=secret":  false,
		"https://user:secret@cdn.example.com/image.jpg":        false,
		"http://cdn.example.com/image.jpg":                     false,
		"https://cdn.example.com/image.jpg#fragment":           false,
		"https://cdn.example.com/image.jpg\r\nX-Injected: yes": false,
	}
	for raw, want := range cases {
		if got := ValidatePublicURL(raw); got != want {
			t.Errorf("ValidatePublicURL(%q) = %v, want %v", raw, got, want)
		}
	}
}
