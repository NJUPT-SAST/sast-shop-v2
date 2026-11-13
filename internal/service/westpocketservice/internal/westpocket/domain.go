package westpocket

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
)

// 错误构造与预定义错误
func failure(code connect.Code, message string) error {
	return connect.NewError(code, errors.New(message))
}

var (
	ErrUnavailable = failure(
		connect.CodeUnavailable,
		"West Pocket dependency is not configured or temporarily unavailable",
	)
	ErrConflict = failure(connect.CodeAborted, "资源已更新，请刷新后重试")
	ErrState    = failure(connect.CodeFailedPrecondition, "当前状态不允许此操作")
	ErrDenied   = failure(connect.CodePermissionDenied, "无权访问此资源")
)

// uuid检查工具
func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// 生成一个符合 RFC 4122 的 UUID v4：
func randomUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// 校验用户输入的标题
func validateTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "聚餐 AA"
	}
	if utf8.RuneCountInString(title) > 100 {
		return "", failure(connect.CodeInvalidArgument, "标题不能超过 100 字")
	}
	return title, nil
}

// aa分摊算法：总金额先平均分，算出余数rem,排序后，前rem个人各多分一分，保证每人份额相差最多1分，总和精确等于total
// Split allocates remainder cents by ascending user ID, including the owner only when selected.
func Split(total int32, owner int64, members []Member) ([]Member, int32, int32, error) {
	if total <= 0 || len(members) < 2 || len(members) > 50 || int64(total) < int64(len(members)) {
		return nil, 0, 0, failure(connect.CodeInvalidArgument, "请选择 2–50 位成员，总金额须不少于人数（分）")
	}
	// 复制成员列表
	result := append([]Member(nil), members...)
	// 按userID升序排序
	sort.Slice(result, func(i, j int) bool { return result[i].UserID < result[j].UserID })
	base, rem := total/boundedInt32(len(result)), total%boundedInt32(len(result))
	var own int32
	for i := range result {
		if result[i].UserID <= 0 || (i > 0 && result[i].UserID == result[i-1].UserID) {
			return nil, 0, 0, failure(connect.CodeInvalidArgument, "成员无效或重复")
		}
		result[i].ShareCents = base
		if int32(i) < rem {
			result[i].ShareCents++
		}
		// 余数 1 分给了排序后 UserID 最小的成员（UserID=1）
		if result[i].UserID == owner {
			own = result[i].ShareCents
		}
	}
	return result, own, total - own, nil
}

// 使用 AES-256-GCM 提供认证加密。绑定 pocketID 作为 AAD，防跨上下文重放
// AEAD = Authenticated Encryption with Associated Data，即“带关联数据的认证加密”。
// 对称：加密和解密用同一个密钥。
// 分组：每次处理固定大小的数据块，AES 的块大小是 128 位（16 字节）。
// AES-256 就是密钥长度 256 位（32 字节）的版本。
type Vault struct{ aead cipher.AEAD }

func NewVault(key string) (*Vault, error) {
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != 32 {
		return nil, errors.New("WEST_POCKET_ENCRYPTION_KEY must be a base64 encoded 32-byte key")
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	// GCM让aes可以流式加密，生成密文+认证标签AAD，可以检测篡改
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

// pocketID作为AAD绑定业务上下文，防重放攻击，减少密文体积
func (v *Vault) Encrypt(plain []byte, pocketID int64) ([]byte, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	// 每次加密都生成一个随机nonce
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return v.aead.Seal(nonce, nonce, plain, []byte(fmt.Sprint(pocketID))), nil
}

func (v *Vault) Decrypt(data []byte, pocketID int64) ([]byte, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	n := v.aead.NonceSize()
	if len(data) < n {
		return nil, errors.New("invalid encrypted snapshot")
	}
	return v.aead.Open(nil, data[:n], data[n:], []byte(fmt.Sprint(pocketID)))
}
