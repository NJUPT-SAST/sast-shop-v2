package service

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
)

var (
	ErrImageUnsupported = errors.New("unsupported image")
	ErrImageInvalid     = errors.New("invalid image")
	ErrImageToolLarge   = errors.New("processed image too large")
	ErrQuoteExceeded    = errors.New("upload quota exceeded")
	ErrQuotaUnavailable = errors.New("upload quota unavaliable")
	ErrStorage          = errors.New("upload storage failed")
)

type ImageLimits struct {
	MaxWidth int
	MaxHeight int
	MaxPixels int64
	MaxQutputBytes int64
}

type ProcessedImage struct {
	Data []byte
	ContentType string
	Extension string
	Width int
	Height int
}

type imageKind struct{
	mime string
	format string
	ext string
}

func ProcessPicture(
	src io.ReadSeeker,  // 可读可定位的流（临时文件）
	declared string,  // 客户端声明的ConterntType
	limits ImageLimits,
)(ProcessedImage, error) {
	var empty ProcessedImage
	//如果文件已关闭或者不可定位，返回不合法图片
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return empty, ErrImageInvalid
	}
	// 读取文件头
	// 分配12字节（WebP签名至少需要12字节）
	header := make([]byte, 12)
	n, err := io.ReadFull(src, header)  // 尝试读满12字节
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF){
		return empty, ErrImageInvalid
	}

	// 识别图片签名
	kind := sniffImage(header[:n])
	if kind.mime == "" {
		return empty, ErrImageUnsupported
	}

	// 交叉验证客户端声明
	declared = strings.ToLower(strings.TrimSpace(declared))
	if declared != "" && declared != "application/octet-stream"  && declared != kind.mime {
		return empty, ErrImageUnsupported
	}

	// 解码配置（获取宽高和格式）
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return empty, ErrImageInvalid
	}
	cfg, format, err := image.DecodeConfig(src)
	if err != nil || format != kind.format {
		return empty, ErrImageInvalid
	}

	// 尺寸和像素限制检查
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > limits.MaxWidth || cfg.Height > limits.MaxHeight || int64(cfg.Width) > limits.MaxPixels/int64(cfg.Height) {
		return empty, ErrImageInvalid
	}

	// 完整解码（验证内容有效性）
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return empty, ErrImageInvalid
	}
	img, format, err := image.Decode(src)
	if err != nil || format != kind.format {
		return empty, ErrImageInvalid
	}

	// 重编码（清理EXIF+ 压缩）
	var output bytes.Buffer

	switch kind.mime {
	case "image/jpeg" :
		if err := jpeg.Encode(&output, img, &jpeg.Options{Quality: 85}); err != nil {
			//以质量85重新压缩
			return empty,ErrImageInvalid
		}
	// 其他格式统一编码成png
	default:
		if err := png.Encode(&output, img); err != nil {
			return empty, ErrImageInvalid
		}
	}

	if int64(output.Len()) > limits.MaxQutputBytes{
		return empty, ErrImageToolLarge
	}

	return ProcessedImage{
		Data: output.Bytes(),
		ContentType: kind.mime,
		Extension: kind.ext,
		Width: cfg.Width,
		Height: cfg.Height,
	}, nil
}

func sniffImage(b []byte) imageKind {
	if len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff {
		return imageKind{"image/jpeg", "jpeg", "jpg"}
	}

	if len(b) >= 8 && bytes.Equal(b[:8],[]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a} ){
		return imageKind{"image/png", "png", "png"}
	}
	if len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP" {
		return imageKind{"image/webp", "webp", "png"}
	}

	return imageKind{}
}

// 生成存储键
// 如 sast-shop/products/5a6b...d3e4.jpg
func NewObjectKey(prefix, ext string) (string, error) {
	if strings.Trim(prefix, "/") == "" {
		return "", ErrStorage
	}
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", ErrStorage
	}
	return strings.Trim(prefix, "/") + "/" + hex.EncodeToString(randomBytes) + "." + ext, nil
}