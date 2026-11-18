package service

import (
	"crypto/sha256"
	"errors"
	"math"
	"os"
	"strconv"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/feishu"
	"github.com/uptrace/bun"
)

// NewService constructs the service and its configured dependencies.
func NewService(db *bun.DB) (*Service, error) {
	// 初始化私有cos存储
	storage, e := NewPrivateCOS(
		os.Getenv("WEST_POCKET_COS_BUCKET_URL"),
		os.Getenv("WEST_POCKET_COS_SECRET_ID"),
		os.Getenv("WEST_POCKET_COS_SECRET_KEY"),
	)
	if e != nil {
		return nil, e
	}
	// 初始化腾讯云人脸识别
	faces, e := NewTencentRecognizer(
		os.Getenv("WEST_POCKET_IAI_SECRET_ID"),
		os.Getenv("WEST_POCKET_IAI_SECRET_KEY"),
		os.Getenv("WEST_POCKET_IAI_REGION"),
		os.Getenv("WEST_POCKET_IAI_GROUP_ID"),
	)
	if e != nil {
		return nil, e
	}
	// 初始化内部服务客户端
	// 创建用户服务（Directory）和支付服务（Payments）的 RPC 客户端，通过内部 token 认证。
	token := os.Getenv("WEST_POCKET_INTERNAL_TOKEN")
	directory, payments := NewClients(
		config.AppConfig.UserServiceURL+":"+strconv.Itoa(int(config.AppConfig.UserServicePort)),
		config.AppConfig.PaymentServiceURL+":"+strconv.Itoa(int(config.AppConfig.PaymentServicePort)),
		token,
	)
	s := &Service{
		DB:          db,
		Directory:   directory,
		Payments:    payments,
		FacePolicy:  "face-v1",
		PhotoPolicy: "photo-v1",
		MobileURL:   os.Getenv("WEST_POCKET_MOBILE_URL"),
		Threshold:   85,
		Margin:      5,
	}
	// 可选组件按需启用
	if storage != nil {
		s.Storage = storage
	}
	if faces != nil {
		s.Faces = faces
	}
	if feishu.AppClient != nil && s.MobileURL != "" {
		s.Messenger = &FeishuMessenger{}
	}
	// 初始化加密valut
	key := os.Getenv("WEST_POCKET_ENCRYPTION_KEY")
	if key != "" {
		s.Vault, e = NewVault(key)
		if e != nil {
			return nil, e
		}
	}
	// 生成游标签名密钥
	cursorSecret := token
	if cursorSecret == "" {
		cursorSecret = randomUUID()
	}
	digest := sha256.Sum256([]byte("west-pocket-cursor:" + cursorSecret))
	s.CursorKey = digest[:]
	s.Threshold, e = floatSetting("WEST_POCKET_FACE_THRESHOLD", 85)
	if e != nil {
		return nil, e
	}
	s.Margin, e = floatSetting("WEST_POCKET_FACE_MARGIN", 5)
	if e != nil {
		return nil, e
	}
	return s, nil
}

func floatSetting(name string, fallback float64) (float64, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	v, e := strconv.ParseFloat(raw, 64)
	if e != nil || v < 0 || v > 100 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, errors.New("invalid " + name)
	}
	return v, nil
}
