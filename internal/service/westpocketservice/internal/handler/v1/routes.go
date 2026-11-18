package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	adminconnect "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/admin/v1/adminv1connect"
	wpconnect "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/westpocket/v1/westpocketv1connect"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/redis"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/service"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/support"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

// Register mounts the RPC and HTTP endpoints.
func Register(e *echo.Echo, s *service.Service) error {
	// 构造拦截器链
	shared, err := interceptor.NewValidationChain(log.Logger)
	if err != nil {
		return err
	}
	// 创建redis会话存储
	store := redis.NewSessionStore()
	// 判断开发环境
	dev := config.AppConfig.AppEnv == config.Development
	// 创造鉴权拦截器，要求请求必须登录
	auth := connect.WithInterceptors(interceptor.AuthRequired(store, log.Logger, dev))
	// 开发环境下 AuthRequired 可能允许特殊调试逻辑。
	opts := []connect.HandlerOption{shared, auth, connect.WithInterceptors(s.LifecycleInterceptor())}
	handler := &Handler{S: s}
	// 注册connect服务
	for _, register := range []func() (string, http.Handler){
		func() (string, http.Handler) {
			return adminconnect.NewWestPocketAdminServiceHandler(&AdminHandler{S: s}, opts...)
		},
		func() (string, http.Handler) {
			return wpconnect.NewWestPocketServiceHandler(handler, opts...)
		},
		func() (string, http.Handler) {
			return wpconnect.NewFaceProfileServiceHandler(handler, opts...)
		},
		func() (string, http.Handler) {
			return wpconnect.NewWestPocketInternalServiceHandler(handler, shared)
		},
	} {
		path, h := register()
		e.Any(path+"*", echo.WrapHandler(h))
	}
	e.POST("/api/v1/west-pocket/uploads", echo.WrapHandler(uploadHandler(s, store, dev)))
	// 验证进程是否活着
	// 失败后重启容器
	e.GET(
		"/health/live",
		func(c *echo.Context) error {
			return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
		},
	)
	// 验证能否接业务流量
	// 检查 PostgreSQL 中是否存在表 westpocket.pocket：
	// 失败后从负载均衡摘除，不接流量
	e.GET("/health/ready", func(c *echo.Context) error {
		exists, err := s.Ready(c.Request().Context())
		// 查询表不存在则 503 Service Unavailable
		if err != nil || !exists {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"status": "migration_required"})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	return nil
}

// 处理图片上传
func uploadHandler(s *service.Service, store interceptor.SessionStore, dev bool) http.Handler {
	// 创建一个容量为2的信号量，限制同时最多处理2个上传请求
	slots := make(chan struct{}, 2)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		// 鉴权
		uid, e := httpActor(r.Context(), r, store, dev)
		if e != nil {
			writeUploadError(w, e)
			return
		}
		// 如果当前已有 2 个上传在处理，则立即返回 ResourceExhausted，最终映射为 HTTP 429。
		// 带缓冲的channel实现信号量，限制并发数
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			writeUploadError(w, support.Failure(connect.CodeResourceExhausted, "正在处理较多照片，请稍后重试"))
			return
		}
		// 先限制总请求体大小 最大11mb
		r.Body = http.MaxBytesReader(w, r.Body, service.MaxUploadBytes+1024*1024)
		// 解析multipart表单
		//nolint:gosec // MaxBytesReader above bounds the entire request to 11 MB.
		if e = r.ParseMultipartForm(
			1024 * 1024,
		); e != nil {
			writeUploadError(w, support.Failure(connect.CodeInvalidArgument, "上传文件过大或表单无效"))
			return
		}
		if r.MultipartForm != nil {
			defer func() { support.ObserveError(r.MultipartForm.RemoveAll()) }()
		}
		// 读取文件
		file, _, e := r.FormFile("file")
		if e != nil {
			writeUploadError(w, support.Failure(connect.CodeInvalidArgument, "请选择照片"))
			return
		}
		defer func() { support.CloseResource(file) }()
		data, e := io.ReadAll(io.LimitReader(file, service.MaxUploadBytes+1))
		if e != nil {
			writeUploadError(w, e)
			return
		}
		// 解析可选参数pocketID
		pocketID := int64(0)
		if raw := r.FormValue("pocket_id"); raw != "" {
			pocketID, e = strconv.ParseInt(raw, 10, 64)
			if e != nil || pocketID <= 0 {
				writeUploadError(w, support.Failure(connect.CodeInvalidArgument, "活动编号无效"))
				return
			}
		}
		// 调用业务上传逻辑
		u, url, e := s.Upload(
			r.Context(),
			uid,
			pocketID,
			r.FormValue("purpose"),
			r.FormValue("consent_version"),
			r.FormValue("request_id"),
			data,
		)
		if e != nil {
			writeUploadError(w, e)
			return
		}
		support.ObserveError(
			json.NewEncoder(w).
				Encode(map[string]any{"upload_id": strconv.FormatInt(u.ID, 10), "expires_at": u.ExpiresAt, "preview_url": url}),
		)
	})
}

// 用于上传接口这类非 Connect 请求的鉴权。
func httpActor(ctx context.Context, r *http.Request, store interceptor.SessionStore, dev bool) (int64, error) {
	var user *interceptor.AuthUser
	var e error
	if dev {
		if id, err := strconv.ParseInt(r.Header.Get("X-Dev-User-ID"), 10, 64); err == nil && id > 0 {
			user, e = store.GetUserByID(ctx, id)
		}
	}
	if user == nil {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			return 0, support.Failure(connect.CodeUnauthenticated, "请先登录")
		}
		user, e = store.GetSession(ctx, token)
	}
	if e != nil || user == nil || user.UserID <= 0 || !strings.EqualFold(user.Status, "active") {
		return 0, support.Failure(connect.CodeUnauthenticated, "请先登录")
	}
	return user.UserID, nil
}

func writeUploadError(w http.ResponseWriter, e error) {
	e = support.RPCError(e)
	status := http.StatusInternalServerError
	switch connect.CodeOf(e) {
	case connect.CodeInvalidArgument:
		status = http.StatusBadRequest
	case connect.CodeUnauthenticated:
		status = http.StatusUnauthorized
	case connect.CodePermissionDenied:
		status = http.StatusForbidden
	case connect.CodeAborted, connect.CodeAlreadyExists:
		status = http.StatusConflict
	case connect.CodeResourceExhausted:
		status = http.StatusTooManyRequests
	case connect.CodeFailedPrecondition:
		status = http.StatusPreconditionFailed
	case connect.CodeUnavailable:
		status = http.StatusServiceUnavailable
	}
	w.WriteHeader(status)
	support.ObserveError(json.NewEncoder(w).Encode(map[string]string{"error": e.Error()}))
}
