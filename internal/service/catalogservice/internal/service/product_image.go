package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/url"
	"strings"

	_ "golang.org/x/image/webp"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/storage"
)

const (
	DefaultMaxImageWidth    = 8192
	DefaultMaxImageHeight   = 8192
	DefaultMaxImagePixels   = 16 * 1024 * 1024
	DefaultMaxOutputBytes   = 10 * 1024 * 1024
	DefaultProductImagePath = "sast-shop/products"
)

var (
	ErrImageUnsupported = errors.New("unsupported image")
	ErrImageInvalid     = errors.New("invalid image")
	ErrImageTooLarge    = errors.New("processed image too large")
	ErrQuotaExceeded    = errors.New("upload quota exceeded")
	ErrQuotaUnavailable = errors.New("upload quota unavailable")
	ErrStorage          = errors.New("upload storage failed")

	// Keep the initial scaffold names as aliases for source compatibility.
	ErrImageToolLarge = ErrImageTooLarge
	ErrQuoteExceeded  = ErrQuotaExceeded
)

type ImageLimits struct {
	MaxWidth       int
	MaxHeight      int
	MaxPixels      int64
	MaxOutputBytes int64

	// MaxQutputBytes is retained for source compatibility with the initial
	// scaffold. New code should use MaxOutputBytes.
	MaxQutputBytes int64
}

func (l ImageLimits) withDefaults() ImageLimits {
	if l.MaxWidth <= 0 {
		l.MaxWidth = DefaultMaxImageWidth
	}
	if l.MaxHeight <= 0 {
		l.MaxHeight = DefaultMaxImageHeight
	}
	if l.MaxPixels <= 0 {
		l.MaxPixels = DefaultMaxImagePixels
	}
	if l.MaxOutputBytes <= 0 {
		l.MaxOutputBytes = l.MaxQutputBytes
	}
	if l.MaxOutputBytes <= 0 {
		l.MaxOutputBytes = DefaultMaxOutputBytes
	}
	return l
}

type ProcessedImage struct {
	Data        []byte
	ContentType string
	Extension   string
	Width       int
	Height      int
}

type imageKind struct {
	mime   string
	format string
	ext    string
}

// ProcessPicture 验证文件签名，对其进行解码，通过重新编码去除元数据，
// 并返回一个尺寸受限、对浏览器安全的表示形式。
func ProcessPicture(src io.ReadSeeker, declared string, limits ImageLimits) (ProcessedImage, error) {
	var empty ProcessedImage
	if src == nil {
		return empty, ErrImageInvalid
	}
	limits = limits.withDefaults()

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return empty, ErrImageInvalid
	}
	header := make([]byte, 12)
	n, err := io.ReadFull(src, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return empty, ErrImageInvalid
	}

	kind := sniffImage(header[:n])
	if kind.mime == "" {
		return empty, ErrImageUnsupported
	}
	declared = strings.TrimSpace(strings.ToLower(declared))
	if declared != "" {
		declared, _, err = mime.ParseMediaType(declared)
		if err != nil {
			return empty, ErrImageUnsupported
		}
		if declared != "application/octet-stream" && declared != kind.mime {
			return empty, ErrImageUnsupported
		}
	}

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return empty, ErrImageInvalid
	}
	cfg, format, err := image.DecodeConfig(src)
	if err != nil || format != kind.format {
		return empty, ErrImageInvalid
	}
	if cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width > limits.MaxWidth || cfg.Height > limits.MaxHeight ||
		int64(cfg.Width) > limits.MaxPixels/int64(cfg.Height) {
		return empty, ErrImageInvalid
	}

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return empty, ErrImageInvalid
	}
	img, format, err := image.Decode(src)
	if err != nil || format != kind.format {
		return empty, ErrImageInvalid
	}

	var output bytes.Buffer
	contentType := "image/png"
	extension := "png"
	if kind.mime == "image/jpeg" {
		if err := jpeg.Encode(&output, img, &jpeg.Options{Quality: 85}); err != nil {
			return empty, ErrImageInvalid
		}
		contentType = "image/jpeg"
		extension = "jpg"
	} else if err := png.Encode(&output, img); err != nil {
		return empty, ErrImageInvalid
	}

	if int64(output.Len()) > limits.MaxOutputBytes {
		return empty, ErrImageTooLarge
	}
	return ProcessedImage{
		Data:        output.Bytes(),
		ContentType: contentType,
		Extension:   extension,
		Width:       cfg.Width,
		Height:      cfg.Height,
	}, nil
}

func sniffImage(b []byte) imageKind {
	if len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff {
		return imageKind{mime: "image/jpeg", format: "jpeg", ext: "jpg"}
	}
	if len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}) {
		return imageKind{mime: "image/png", format: "png", ext: "png"}
	}
	if len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP" {
		return imageKind{mime: "image/webp", format: "webp", ext: "png"}
	}
	return imageKind{}
}

// NewObjectKey 在受信任的前缀下创建一个随机对象名称。
// 不会使用原始文件名，也不使用用户可控的路径片段。
func NewObjectKey(prefix, ext string) (string, error) {
	cleanPrefix := strings.Trim(strings.TrimSpace(prefix), "/")
	if cleanPrefix == "" {
		return "", ErrStorage
	}
	for _, segment := range strings.Split(cleanPrefix, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, "\\\x00") {
			return "", ErrStorage
		}
	}
	ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	switch ext {
	case "jpg", "jpeg", "png", "webp":
	default:
		return "", ErrStorage
	}
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", ErrStorage
	}
	return cleanPrefix + "/" + hex.EncodeToString(randomBytes) + "." + ext, nil
}

// ValidatePublicURL 会拒绝那些可能泄露凭证或指向可执行文件/非公开资源的 URL。
func ValidatePublicURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(raw, "\r\n\x00") {
		return false
	}
	return parsed.IsAbs()
}

// ProductImageUploadService 协调一张产品图片的处理、存储和公开可读性验证。
type ProductImageUploadService struct {
	Store  storage.ObjectStore
	Prefix string
	Limits ImageLimits
}

func NewProductImageUploadService(store storage.ObjectStore, prefix string, limits ImageLimits) *ProductImageUploadService {
	if strings.TrimSpace(prefix) == "" {
		prefix = DefaultProductImagePath
	}
	return &ProductImageUploadService{Store: store, Prefix: prefix, Limits: limits}
}

func (s *ProductImageUploadService) Upload(ctx context.Context, userID int64, src io.ReadSeeker, declared string) (result string, err error) {
	if s == nil || s.Store == nil || userID <= 0 {
		return "", ErrStorage
	}
	processed, err := ProcessPicture(src, declared, s.Limits)
	if err != nil {
		return "", err
	}
	key, err := NewObjectKey(s.Prefix, processed.Extension)
	if err != nil {
		return "", err
	}
	if err := s.Store.Put(ctx, key, bytes.NewReader(processed.Data), int64(len(processed.Data)), processed.ContentType); err != nil {
		_ = s.Store.Delete(ctx, key)
		return "", ErrStorage
	}
	stored := true
	defer func() {
		if stored && err != nil {
			_ = s.Store.Delete(ctx, key)
		}
	}()

	publicURL, err := s.Store.PublicURL(key)
	if err != nil || !ValidatePublicURL(publicURL) {
		return "", ErrStorage
	}
	if err := s.Store.ProbePublic(ctx, publicURL, processed.ContentType); err != nil {
		return "", ErrStorage
	}
	return publicURL, nil
}

func (s *ProductImageUploadService) Ready(ctx context.Context) error {
	if s == nil || s.Store == nil {
		return ErrStorage
	}
	return s.Store.Ready(ctx)
}
