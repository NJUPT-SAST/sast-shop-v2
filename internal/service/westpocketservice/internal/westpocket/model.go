package westpocket

import (
	"encoding/json"
	"time"

	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"

	"github.com/uptrace/bun"
)

type Pocket struct {
	bun.BaseModel    `bun:"table:westpocket.pocket,alias:p"`
	ID               int64 `bun:"id,pk,autoincrement"`
	OwnerID          int64
	Title            string
	TotalCents       int32
	Status           string
	Revision         int64
	ParticipantCount int32
	OwnerShareCents  int32
	ReceivableCents  int32
	QrCiphertext     []byte
	PublishedAt      *time.Time
	SettledAt        *time.Time
	CancelledAt      *time.Time
	CancelReason     string
	CreatedAt        time.Time `bun:",default:current_timestamp"`
	UpdatedAt        time.Time `bun:",default:current_timestamp"`
}
type Member struct {
	bun.BaseModel   `bun:"table:westpocket.pocket_member,alias:m"`
	ID              int64 `bun:"id,pk,autoincrement"`
	PocketID        int64
	UserID          int64
	SelectionSource string
	FaceMatchID     *int64
	ShareCents      int32
	PaymentBillID   *int64
	BillStatus      string
	AlbumAccess     string
	LastRemindedAt  *time.Time
	CreatedAt       time.Time `bun:",default:current_timestamp"`
	UpdatedAt       time.Time `bun:",default:current_timestamp"`
}
type Upload struct {
	bun.BaseModel  `bun:"table:westpocket.upload,alias:u"`
	ID             int64 `bun:"id,pk,autoincrement"`
	OwnerID        int64
	PocketID       *int64
	Purpose        string
	Status         string
	ObjectKey      string
	SHA256         string `bun:"sha256"`
	ByteSize       int64
	Width          int
	Height         int
	ConsentVersion string
	RequestID      string
	Bound          bool
	ExpiresAt      time.Time
	DeletedAt      *time.Time
	CreatedAt      time.Time `bun:",default:current_timestamp"`
}
type Photo struct {
	bun.BaseModel        `bun:"table:westpocket.pocket_photo,alias:ph"`
	ID                   int64 `bun:"id,pk,autoincrement"`
	PocketID             int64
	UploadID             int64
	UploaderID           int64
	ObjectKey            string
	SHA256               string `bun:"sha256"`
	Width                int
	Height               int
	Status               string
	LatestJobID          *int64
	DetectedFaceCount    int32
	RetentionMode        string
	RetentionUntil       time.Time
	AuthorizationVersion string
	ErrorCode            string
	DeletedAt            *time.Time
	CreatedAt            time.Time `bun:",default:current_timestamp"`
	UpdatedAt            time.Time `bun:",default:current_timestamp"`
}
type Profile struct {
	bun.BaseModel     `bun:"table:westpocket.face_profile,alias:fp"`
	ID                int64 `bun:"id,pk,autoincrement"`
	UserID            int64
	PersonID          string
	Status            string
	Revision          int64
	EnrollmentVersion int64
	ConsentVersion    string
	ConsentedAt       time.Time
	ConsentExpiresAt  time.Time
	RevokedAt         *time.Time
	ProviderDeletedAt *time.Time
	CreatedAt         time.Time `bun:",default:current_timestamp"`
	UpdatedAt         time.Time `bun:",default:current_timestamp"`
}
type Sample struct {
	bun.BaseModel     `bun:"table:westpocket.face_sample,alias:fs"`
	ID                int64 `bun:"id,pk,autoincrement"`
	ProfileID         int64
	UploadID          int64
	EnrollmentVersion int64
	PersonID          string
	Status            string
	CreatedAt         time.Time `bun:",default:current_timestamp"`
}
type Job struct {
	bun.BaseModel  `bun:"table:westpocket.async_job,alias:j"`
	ID             int64 `bun:"id,pk,autoincrement"`
	Kind           string
	OwnerID        int64
	PocketID       *int64
	ProfileID      *int64
	InputVersion   int64
	Payload        json.RawMessage `bun:"type:jsonb"`
	DedupeKey      string
	Status         string
	AttemptCount   int
	NextAttemptAt  time.Time `bun:",default:current_timestamp"`
	LeaseOwner     string
	LeaseExpiresAt *time.Time
	ErrorCode      string
	Progress       json.RawMessage `bun:"type:jsonb"`
	CreatedAt      time.Time       `bun:",default:current_timestamp"`
	UpdatedAt      time.Time       `bun:",default:current_timestamp"`
}
type Candidate struct {
	UserID int64   `json:"user_id"`
	Score  float64 `json:"score"`
}
type Match struct {
	bun.BaseModel   `bun:"table:westpocket.face_match,alias:fm"`
	ID              int64 `bun:"id,pk,autoincrement"`
	PhotoID         int64
	JobID           int64
	FaceIndex       int
	BboxX           int `bun:"bbox_x"`
	BboxY           int `bun:"bbox_y"`
	BboxWidth       int
	BboxHeight      int
	SuggestedUserID *int64
	ConfirmedUserID *int64
	Candidates      []Candidate `bun:"type:jsonb"`
	Score           float64
	MatchStatus     string
	Resolution      string
	ExpiresAt       time.Time
}
type Notification struct {
	bun.BaseModel   `bun:"table:westpocket.notification_outbox,alias:n"`
	ID              int64 `bun:"id,pk,autoincrement"`
	PocketID        int64
	RecipientUserID int64
	Kind            string
	Sequence        int
	Status          string
	MessageUUID     string
	FeishuMessageID string
	AttemptCount    int
	NextAttemptAt   time.Time `bun:",default:current_timestamp"`
	LeaseExpiresAt  *time.Time
	ErrorCode       string
	CreatedAt       time.Time `bun:",default:current_timestamp"`
	UpdatedAt       time.Time `bun:",default:current_timestamp"`
}
type User struct {
	ID              int64
	Name, AvatarURL string
}
type Bill struct {
	ID, PayerID, PayeeID, SourceID         int64
	AmountCents                            int32
	SourceType, Status, BillNo, VerifyCode string
	UpdatedAt                              time.Time
	Proto                                  *paymentv1.Bill
}
type QR struct {
	ID        int64     `json:"id"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Face struct {
	X, Y, Width, Height int
	Status              string
	Candidates          []ProviderCandidate
}
type ProviderCandidate struct {
	PersonID string
	Score    float64
}
