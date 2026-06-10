package feishu

import "fmt"

// openAPIResponse is the common envelope returned by most open.feishu.cn APIs.
type openAPIResponse[T any] struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data T      `json:"data"`
}

func (r *openAPIResponse[T]) Err() error {
	if r.Code == 0 {
		return nil
	}
	return &APIError{
		Code:    r.Code,
		Message: r.Msg,
	}
}

type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("feishu api error: code=%d message=%s", e.Code, e.Message)
}

type OAuthError struct {
	Code             int
	ErrorCode        string
	ErrorDescription string
}

func (e *OAuthError) Error() string {
	if e == nil {
		return ""
	}
	if e.ErrorCode == "" {
		return fmt.Sprintf("feishu oauth error: code=%d", e.Code)
	}
	return fmt.Sprintf(
		"feishu oauth error: code=%d error=%s description=%s",
		e.Code,
		e.ErrorCode,
		e.ErrorDescription,
	)
}

type OAuthToken struct {
	AccessToken           string `json:"access_token"`
	ExpiresIn             int32  `json:"expires_in"`
	RefreshToken          string `json:"refresh_token,omitempty"`
	RefreshTokenExpiresIn int32  `json:"refresh_token_expires_in,omitempty"`
	TokenType             string `json:"token_type,omitempty"`
	Scope                 string `json:"scope,omitempty"`
}

type UserInfo struct {
	Name            string `json:"name"`
	AvatarURL       string `json:"avatar_url"`
	OpenID          string `json:"open_id"`
	UnionID         string `json:"union_id"`
	Email           string `json:"email"`
	EnterpriseEmail string `json:"enterprise_email"`
	UserID          string `json:"user_id"`
	TenantKey       string `json:"tenant_key"`
	EmployeeNo      string `json:"employee_no"`
}

type JSAPITicket struct {
	Ticket   string `json:"ticket"`
	ExpireIn int32  `json:"expire_in"`
}

type JSAPISignature struct {
	AppID     string `json:"app_id"`
	NonceStr  string `json:"nonce_str"`
	Timestamp string `json:"timestamp"`
	Signature string `json:"signature"`
	URL       string `json:"url"`
}

type SendMessageResult struct {
	MessageID string `json:"message_id"`
}
