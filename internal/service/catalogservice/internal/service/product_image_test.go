package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"testing"
)

func imageFixture(t *testing.T, format string) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, color.RGBA{R: uint8(40 + x*30), G: uint8(80 + y*30), B: 120, A: 255})
		}
	}

	var out bytes.Buffer
	var err error
	switch format {
	case "jpeg":
		err = jpeg.Encode(&out, img, &jpeg.Options{Quality: 95})
	case "png":
		err = png.Encode(&out, img)
	default:
		t.Fatalf("unsupported fixture format %q", format)
	}
	if err != nil {
		t.Fatalf("encode %s fixture: %v", format, err)
	}
	return out.Bytes()
}

func defaultImageLimits() ImageLimits {
	return ImageLimits{
		MaxWidth:       4096,
		MaxHeight:      4096,
		MaxPixels:      4096 * 4096,
		MaxQutputBytes: 10 << 20,
	}
}

func TestProcessPictureAcceptsJPEGAndStripsToConfiguredOutput(t *testing.T) {
	t.Parallel()

	input := imageFixture(t, "jpeg")
	processed, err := ProcessPicture(bytes.NewReader(input), "image/jpeg", defaultImageLimits())
	if err != nil {
		t.Fatalf("ProcessPicture() error = %v", err)
	}
	if processed.ContentType != "image/jpeg" {
		t.Fatalf("ContentType = %q, want image/jpeg", processed.ContentType)
	}
	if processed.Extension != "jpg" {
		t.Fatalf("Extension = %q, want jpg", processed.Extension)
	}
	if processed.Width != 2 || processed.Height != 3 {
		t.Fatalf("dimensions = %dx%d, want 2x3", processed.Width, processed.Height)
	}
	if _, format, err := image.Decode(bytes.NewReader(processed.Data)); err != nil || format != "jpeg" {
		t.Fatalf("processed JPEG cannot be decoded: format=%q err=%v", format, err)
	}
}

func TestProcessPictureAcceptsPNG(t *testing.T) {
	t.Parallel()

	input := imageFixture(t, "png")
	processed, err := ProcessPicture(bytes.NewReader(input), "image/png", defaultImageLimits())
	if err != nil {
		t.Fatalf("ProcessPicture() error = %v", err)
	}
	if processed.ContentType != "image/png" || processed.Extension != "png" {
		t.Fatalf("output metadata = (%q, %q), want (image/png, png)", processed.ContentType, processed.Extension)
	}
	if _, format, err := image.Decode(bytes.NewReader(processed.Data)); err != nil || format != "png" {
		t.Fatalf("processed PNG cannot be decoded: format=%q err=%v", format, err)
	}
}

func TestProcessPictureRejectsDeclaredTypeMismatch(t *testing.T) {
	t.Parallel()

	input := imageFixture(t, "png")
	_, err := ProcessPicture(bytes.NewReader(input), "image/jpeg", defaultImageLimits())
	if err == nil {
		t.Fatal("ProcessPicture() unexpectedly accepted a mismatched declared MIME type")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error = %v, want unsupported-image error", err)
	}
}

func TestProcessPictureRejectsInvalidSignature(t *testing.T) {
	t.Parallel()

	_, err := ProcessPicture(bytes.NewReader([]byte("not an image")), "image/png", defaultImageLimits())
	if err == nil {
		t.Fatal("ProcessPicture() unexpectedly accepted invalid image bytes")
	}
	if !strings.Contains(err.Error(), "unsupported") && !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("error = %v, want unsupported or invalid image error", err)
	}
}

func TestProcessPictureRejectsDimensionsAndOutputLimits(t *testing.T) {
	t.Parallel()

	input := imageFixture(t, "png")
	limits := defaultImageLimits()
	limits.MaxWidth = 1
	if _, err := ProcessPicture(bytes.NewReader(input), "image/png", limits); err == nil {
		t.Fatal("ProcessPicture() accepted an image over MaxWidth")
	}

	limits = defaultImageLimits()
	limits.MaxQutputBytes = 1
	if _, err := ProcessPicture(bytes.NewReader(input), "image/png", limits); err == nil {
		t.Fatal("ProcessPicture() accepted output over MaxQutputBytes")
	}
}

func TestSniffImageRecognizesWebPSignature(t *testing.T) {
	t.Parallel()

	header := []byte("RIFF\x00\x00\x00\x00WEBP")
	kind := sniffImage(header)
	if kind.mime != "image/webp" || kind.format != "webp" {
		t.Fatalf("sniffImage() = %#v, want WebP kind", kind)
	}
}

func TestNewObjectKeyIsUnpredictableAndUsesSafeExtension(t *testing.T) {
	t.Parallel()

	first, err := NewObjectKey("sast-shop/products", "png")
	if err != nil {
		t.Fatalf("NewObjectKey() error = %v", err)
	}
	second, err := NewObjectKey("sast-shop/products", "png")
	if err != nil {
		t.Fatalf("second NewObjectKey() error = %v", err)
	}
	if first == second {
		t.Fatal("NewObjectKey() generated a duplicate object key")
	}
	for _, key := range []string{first, second} {
		if !strings.HasPrefix(key, "sast-shop/products/") || !strings.HasSuffix(key, ".png") {
			t.Fatalf("key %q does not use expected prefix/extension", key)
		}
		if strings.ContainsAny(key, "\\\r\n") {
			t.Fatalf("key %q contains unsafe path characters", key)
		}
	}
}

func TestNewObjectKeyRejectsEmptyPrefix(t *testing.T) {
	t.Parallel()

	if _, err := NewObjectKey("///", "png"); err == nil {
		t.Fatal("NewObjectKey() accepted an empty prefix")
	}
}

type fakeObjectStore struct {
	putErr      error
	publicErr   error
	probeErr    error
	url         string
	key         string
	content     []byte
	contentType string
	deleted     string
	probedURL   string
}

func (s *fakeObjectStore) Put(_ context.Context, key string, body io.Reader, size int64, contentType string) error {
	s.key = key
	s.contentType = contentType
	s.content, _ = io.ReadAll(body)
	if int64(len(s.content)) != size {
		return errors.New("size mismatch")
	}
	return s.putErr
}

func (s *fakeObjectStore) Delete(_ context.Context, key string) error {
	s.deleted = key
	return nil
}

func (s *fakeObjectStore) PublicURL(string) (string, error) {
	if s.publicErr != nil {
		return "", s.publicErr
	}
	return s.url, nil
}

func (s *fakeObjectStore) ProbePublic(_ context.Context, url, _ string) error {
	s.probedURL = url
	return s.probeErr
}

func (s *fakeObjectStore) Ready(context.Context) error { return nil }

func TestProductImageUploadServiceStoresAndProbesBeforeReturning(t *testing.T) {
	store := &fakeObjectStore{url: "https://cdn.example.com/sast-shop/products/object.png"}
	uploader := NewProductImageUploadService(store, "sast-shop/products", defaultImageLimits())
	input := imageFixture(t, "png")
	got, err := uploader.Upload(context.Background(), 7, bytes.NewReader(input), "image/png")
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if got != store.url || store.probedURL != got {
		t.Fatalf("Upload() url/probe = (%q, %q), want %q", got, store.probedURL, store.url)
	}
	if !strings.HasPrefix(store.key, "sast-shop/products/") || store.contentType != "image/png" {
		t.Fatalf("stored object metadata = (%q, %q)", store.key, store.contentType)
	}
	if len(store.content) == 0 {
		t.Fatal("Upload() stored an empty object")
	}
	if store.deleted != "" {
		t.Fatalf("successful upload deleted object %q", store.deleted)
	}
}

func TestProductImageUploadServiceDeletesWhenPublicProbeFails(t *testing.T) {
	store := &fakeObjectStore{
		url:      "https://cdn.example.com/sast-shop/products/object.png",
		probeErr: errors.New("not readable"),
	}
	uploader := NewProductImageUploadService(store, "sast-shop/products", defaultImageLimits())
	_, err := uploader.Upload(context.Background(), 7, bytes.NewReader(imageFixture(t, "png")), "image/png")
	if !errors.Is(err, ErrStorage) {
		t.Fatalf("Upload() error = %v, want ErrStorage", err)
	}
	if store.deleted == "" {
		t.Fatal("Upload() did not delete object after failed public probe")
	}
}

func TestProductImageUploadServiceRejectsUnsafeURLAndDeletesObject(t *testing.T) {
	store := &fakeObjectStore{url: "https://user:password@cdn.example.com/object.png"}
	uploader := NewProductImageUploadService(store, "sast-shop/products", defaultImageLimits())
	_, err := uploader.Upload(context.Background(), 7, bytes.NewReader(imageFixture(t, "png")), "image/png")
	if !errors.Is(err, ErrStorage) {
		t.Fatalf("Upload() error = %v, want ErrStorage", err)
	}
	if store.deleted == "" {
		t.Fatal("Upload() did not delete object after unsafe URL")
	}
}
