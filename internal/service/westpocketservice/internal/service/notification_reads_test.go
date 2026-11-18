package service

import (
	"context"
	"errors"
	"testing"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/model"
)

type readStatusMessenger struct {
	Messenger
}

func (readStatusMessenger) ReadStatus(_ context.Context, messageID string) (bool, error) {
	switch messageID {
	case "read":
		return true, nil
	case "unread":
		return false, nil
	case "error":
		return false, errors.New("provider unavailable")
	default:
		panic("only sent payment notifications should be queried")
	}
}

func TestLoadNotificationReads(t *testing.T) {
	s := Service{Messenger: readStatusMessenger{}}
	notifications := []model.Notification{
		{Kind: "payment", Status: "sent", FeishuMessageID: "read"},
		{Kind: "payment", Status: "sent", FeishuMessageID: "unread"},
		{Kind: "payment", Status: "sent", FeishuMessageID: "error"},
		{Kind: "payment", Status: "sent"},
		{Kind: "payment", Status: "pending", FeishuMessageID: "pending"},
		{Kind: "payment_qr", Status: "sent", FeishuMessageID: "qr"},
		{Kind: "reminder", Status: "sent", FeishuMessageID: "reminder"},
		{Kind: "summary", Status: "sent", FeishuMessageID: "summary"},
	}
	s.LoadNotificationReads(context.Background(), notifications)
	if notifications[0].IsRead == nil || !*notifications[0].IsRead {
		t.Fatal("expected an explicit read status")
	}
	if notifications[1].IsRead == nil || *notifications[1].IsRead {
		t.Fatal("expected an explicit unread status")
	}
	for _, n := range notifications[2:] {
		if n.IsRead != nil {
			t.Errorf("unexpected read status for %+v", n)
		}
	}
}
