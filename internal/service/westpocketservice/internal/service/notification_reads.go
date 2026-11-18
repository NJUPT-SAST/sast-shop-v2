package service

import (
	"context"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/model"
)

type notificationReadStatus interface {
	ReadStatus(context.Context, string) (bool, error)
}

func (s *Service) LoadNotificationReads(ctx context.Context, notifications []model.Notification) {
	reader, ok := s.Messenger.(notificationReadStatus)
	if !ok {
		return
	}
	type lookup struct {
		index     int
		messageID string
	}
	type result struct {
		index int
		read  bool
		err   error
	}
	pending := make(chan lookup, len(notifications))
	for i, n := range notifications {
		if n.Kind == "payment" && n.Status == "sent" && n.FeishuMessageID != "" {
			pending <- lookup{index: i, messageID: n.FeishuMessageID}
		}
	}
	close(pending)
	count := len(pending)
	results := make(chan result, count)

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for range min(8, count) {
		go func() {
			for n := range pending {
				if ctx.Err() != nil {
					return
				}
				read, err := reader.ReadStatus(ctx, n.messageID)
				results <- result{index: n.index, read: read, err: err}
			}
		}()
	}
	for range count {
		select {
		case r := <-results:
			if r.err == nil {
				notifications[r.index].IsRead = &r.read
			}
		case <-ctx.Done():
			return
		}
	}
}
