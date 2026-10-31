package westpocket

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"time"

	_ "golang.org/x/image/webp"

	"connectrpc.com/connect"
	"github.com/disintegration/imaging"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	tcprofile "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	iai "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/iai/v20180301"
	cossdk "github.com/tencentyun/cos-go-sdk-v5"
)

const (
	MaxUploadBytes = 10 * 1024 * 1024
	MaxImagePixels = 40 * 1000 * 1000
)

// 图像处理：校验上传图片的大小，格式和像素，逐步压缩为jpeg，返回处理后图片的字节，宽， 高
// 手机、相机拍照时，感光元件方向和实际拍摄方向可能不一致。设备会把“正确的旋转方向”写进图片的 EXIF 元数据里（Orientation 标签）。
// 如果直接读取图片像素，不处理 EXIF，图片可能会是横着的、倒着的或旋转 90° 的。
func NormalizeImage(data []byte) ([]byte, int, int, error) {
	if len(data) > MaxUploadBytes {
		return nil, 0, 0, failure(connect.CodeInvalidArgument, "图片不能超过 10 MB")
	}
	cfg, format, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil || (format != "jpeg" && format != "png" && format != "webp") {
		return nil, 0, 0, failure(connect.CodeInvalidArgument, "请上传 JPEG、PNG 或 WebP 图片，HEIC 请先转换")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxImagePixels {
		return nil, 0, 0, failure(connect.CodeInvalidArgument, "图片像素过大")
	}
	img, e := imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
	if e != nil {
		return nil, 0, 0, failure(connect.CodeInvalidArgument, "无法解码图片")
	}
	if img.Bounds().Dx() > 3000 || img.Bounds().Dy() > 3000 {
		img = imaging.Fit(img, 3000, 3000, imaging.Lanczos)
	}
	var b bytes.Buffer
	for _, quality := range []int{85, 75, 60, 45} {
		b.Reset()
		if e = jpeg.Encode(&b, img, &jpeg.Options{Quality: quality}); e != nil {
			return nil, 0, 0, e
		}
		if b.Len() <= 3*1024*1024 {
			return b.Bytes(), img.Bounds().Dx(), img.Bounds().Dy(), nil
		}
	}
	return nil, 0, 0, failure(connect.CodeInvalidArgument, "图片压缩后仍超过人脸服务限制")
}

// 私有cos存储
type PrivateCOS struct {
	sdk     *cossdk.Client
	id, key string
}

// 创建腾讯云私有cos存储客户端，要求 bucket 是合法的私有 HTTPS 地址，否则返回错误；参数为空时返回 nil, nil。
func NewPrivateCOS(bucket, id, key string) (*PrivateCOS, error) {
	if bucket == "" || id == "" || key == "" {
		return nil, nil
	}
	u, e := url.Parse(bucket)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("WEST_POCKET_COS_BUCKET_URL must be a private HTTPS bucket URL")
	}
	sdk := cossdk.NewClient(
		&cossdk.BaseURL{BucketURL: u},
		&http.Client{
			Timeout:   30 * time.Second,
			Transport: &cossdk.AuthorizationTransport{SecretID: id, SecretKey: key},
		},
	)
	return &PrivateCOS{sdk: sdk, id: id, key: key}, nil
}

// 将图片数据以私有acl，jepg类型上传到cos指定key
// ACL 是 Access Control List，访问控制列表。
func (c *PrivateCOS) Put(ctx context.Context, key string, data []byte) error {
	resp, e := c.sdk.Object.Put(
		ctx,
		key,
		bytes.NewReader(data),
		&cossdk.ObjectPutOptions{
			ObjectPutHeaderOptions: &cossdk.ObjectPutHeaderOptions{
				ContentType:   "image/jpeg",
				ContentLength: int64(len(data)),
			},
			ACLHeaderOptions: &cossdk.ACLHeaderOptions{XCosACL: "private"},
		},
	)
	if resp != nil && resp.Body != nil {
		closeResource(resp.Body)
	}
	return e
}

// 从cos读取指定key的对象，限制最大读取3MB
func (c *PrivateCOS) Read(ctx context.Context, key string) ([]byte, error) {
	resp, e := c.sdk.Object.Get(ctx, key, nil)
	if e != nil {
		return nil, e
	}
	defer func() { closeResource(resp.Body) }()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 3*1024*1024+1))
	if e == nil && len(data) > 3*1024*1024 {
		return nil, errors.New("private image exceeds maximum size")
	}
	return data, e
}

// 删除
func (c *PrivateCOS) Delete(ctx context.Context, key string) error {
	resp, e := c.sdk.Object.Delete(ctx, key)
	if resp != nil && resp.Body != nil {
		closeResource(resp.Body)
	}
	return e
}

// 为指定key生成5分钟有效的预签名GET URL
func (c *PrivateCOS) URL(ctx context.Context, key string) (string, error) {
	u, e := c.sdk.Object.GetPresignedURL(ctx, http.MethodGet, key, c.id, c.key, 5*time.Minute, nil)
	if e != nil {
		return "", e
	}
	return u.String(), nil
}

// 腾讯云人脸识别
type TencentRecognizer struct {
	client *iai.Client
	group  string
}

// 创建IAI客户端
func NewTencentRecognizer(id, key, region, group string) (*TencentRecognizer, error) {
	if id == "" || key == "" || group == "" {
		return nil, nil
	}
	p := tcprofile.NewClientProfile()
	p.HttpProfile.ReqTimeout = 30
	if region == "" {
		region = "ap-shanghai"
	}
	client, e := iai.NewClient(common.NewCredential(id, key), region, p)
	if e != nil {
		return nil, e
	}
	return &TencentRecognizer{client: client, group: group}, nil
}

func value[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
func ptr[T any](v T) *T { return &v }

// 调用DetectFace检测图片中的人脸
func (t *TencentRecognizer) detect(ctx context.Context, data []byte) ([]*iai.FaceInfo, error) {
	r := iai.NewDetectFaceRequest()
	r.Image = ptr(base64.StdEncoding.EncodeToString(data))
	r.MaxFaceNum = ptr(uint64(120))
	r.MinFaceSize = ptr(uint64(34))
	r.NeedQualityDetection = ptr(uint64(1))
	r.NeedFaceAttributes = ptr(uint64(0))
	r.FaceModelVersion = ptr("3.0")
	resp, e := t.client.DetectFaceWithContext(ctx, r)
	if e != nil {
		return nil, e
	}
	if resp == nil || resp.Response == nil {
		return nil, ErrUnavailable
	}
	return resp.Response.FaceInfos, nil
}

// 为指定人员注册人脸，先逐张检测图片，要求每张图只有一张脸，且质量分不低于70，然后先删除旧人员，再创建人员并上传第一张人脸；如果有多张图，继续通过 CreateFace 追加，并校验成功数量。
func (t *TencentRecognizer) Enroll(ctx context.Context, person string, images [][]byte) error {
	if len(images) == 0 {
		return errors.New("INVALID_SAMPLES")
	}
	for _, data := range images {
		faces, e := t.detect(ctx, data)
		if e != nil {
			return e
		}
		if len(faces) != 1 {
			return errors.New("ONE_FACE_REQUIRED")
		}
		if faces[0].FaceQualityInfo == nil || value(faces[0].FaceQualityInfo.Score) < 70 {
			return errors.New("LOW_QUALITY")
		}
	}
	// The staged person has no active local mapping. Delete before a retry so timeout recovery never duplicates samples.
	if e := t.Delete(ctx, person); e != nil {
		return e
	}
	r := iai.NewCreatePersonRequest()
	r.GroupId = &t.group
	r.PersonId = &person
	r.PersonName = &person
	r.Image = ptr(base64.StdEncoding.EncodeToString(images[0]))
	r.QualityControl = ptr(uint64(3))
	if _, e := t.client.CreatePersonWithContext(ctx, r); e != nil {
		return e
	}
	if len(images) > 1 {
		f := iai.NewCreateFaceRequest()
		f.PersonId = &person
		f.QualityControl = ptr(uint64(3))
		f.FaceMatchThreshold = ptr(float64(80))
		for _, data := range images[1:] {
			f.Images = append(f.Images, ptr(base64.StdEncoding.EncodeToString(data)))
		}
		resp, e := t.client.CreateFaceWithContext(ctx, f)
		if e != nil {
			return e
		}
		if resp == nil || resp.Response == nil || len(resp.Response.SucFaceIds) != len(images)-1 {
			return errors.New("SAMPLE_REJECTED")
		}
	}
	return nil
}

// 删除指定人员。如果腾讯云返回“人员不存在”类错误，则视为删除成功。
func (t *TencentRecognizer) Delete(ctx context.Context, person string) error {
	if person == "" {
		return nil
	}
	r := iai.NewDeletePersonRequest()
	r.PersonId = &person
	_, e := t.client.DeletePersonWithContext(ctx, r)
	var api *tcerr.TencentCloudSDKError
	if errors.As(e, &api) &&
		(api.Code == "InvalidParameterValue.PersonIdNotExist" || api.Code == "ResourceNotFound.PersonNotExist") {
		return nil
	}
	return e
}

// 识别图片中的人脸。先检测所有人脸，若无人脸返回 NO_FACE，若达到 120 张返回 FACE_LIMIT_REACHED。然后对每张脸按检测框加边距裁剪，逐张调用 SearchPersons 搜索候选人员，返回带有候选人和状态的 Face 列表。
func (t *TencentRecognizer) Recognize(ctx context.Context, data []byte) ([]Face, error) {
	detected, e := t.detect(ctx, data)
	if e != nil {
		return nil, e
	}
	if len(detected) == 0 {
		return nil, errors.New("NO_FACE")
	}
	if len(detected) >= 120 {
		return nil, errors.New("FACE_LIMIT_REACHED")
	}
	img, _, e := image.Decode(bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	out := make([]Face, 0, len(detected))
	// Crop each detected face with padding. SearchPersons always receives one face, including >10-person photos.
	for _, d := range detected {
		f := Face{
			X:      int(value(d.X)),
			Y:      int(value(d.Y)),
			Width:  int(value(d.Width)),
			Height: int(value(d.Height)),
			Status: "unknown",
		}
		if min(f.Width, f.Height) < 34 {
			f.Status = "low_quality"
			out = append(out, f)
			continue
		}
		pad := min(f.Width, f.Height) / 8
		rect := image.Rect(f.X-pad, f.Y-pad, f.X+f.Width+pad, f.Y+f.Height+pad).Intersect(img.Bounds())
		if rect.Empty() {
			f.Status = "low_quality"
			out = append(out, f)
			continue
		}
		crop := imaging.Crop(img, rect)
		var b bytes.Buffer
		if e = jpeg.Encode(&b, crop, &jpeg.Options{Quality: 90}); e != nil {
			return nil, e
		}
		r := iai.NewSearchPersonsRequest()
		r.GroupIds = []*string{&t.group}
		r.Image = ptr(base64.StdEncoding.EncodeToString(b.Bytes()))
		r.MaxFaceNum = ptr(uint64(1))
		r.MaxPersonNum = ptr(uint64(3))
		r.MinFaceSize = ptr(uint64(34))
		r.QualityControl = ptr(uint64(2))
		r.NeedPersonInfo = ptr(int64(0))
		resp, e := t.client.SearchPersonsWithContext(ctx, r)
		if e != nil {
			return nil, e
		}
		if resp == nil || resp.Response == nil {
			return nil, ErrUnavailable
		}
		if len(resp.Response.Results) > 0 {
			result := resp.Response.Results[0]
			if value(result.RetCode) != 0 {
				f.Status = "low_quality"
			}
			for _, c := range result.Candidates {
				f.Candidates = append(
					f.Candidates,
					ProviderCandidate{PersonID: value(c.PersonId), Score: value(c.Score)},
				)
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// 将错误转换为安全的错误码字符串。腾讯云 SDK 错误转为 PROVIDER_<Code>；已知业务错误原样返回；超时转为 PROVIDER_TIMEOUT；其他错误转为 DEPENDENCY_<connect.Code>，避免向外泄露底层细节。
func safeError(err error) string {
	if err == nil {
		return ""
	}
	var api *tcerr.TencentCloudSDKError
	if errors.As(err, &api) {
		return "PROVIDER_" + api.Code
	}
	for _, code := range []string{"NO_FACE", "LOW_QUALITY", "ONE_FACE_REQUIRED", "FACE_LIMIT_REACHED", "SAMPLE_REJECTED"} {
		if err.Error() == code {
			return code
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "PROVIDER_TIMEOUT"
	}
	return fmt.Sprintf("DEPENDENCY_%s", connect.CodeOf(err).String())
}
