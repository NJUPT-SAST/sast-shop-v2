package westpocket

import (
	"context"
	"testing"
)

func TestPostgresAdminSchemaCompatibility(t *testing.T) {
	s := integrationService(t)
	ctx := context.Background()
	requestID := randomUUID()
	id, err := s.Create(ctx, 10, "schema compatibility", 1100, requestID)
	if err != nil {
		t.Fatal(err)
	}
	retryID, err := s.Create(ctx, 10, "schema compatibility", 1100, requestID)
	if err != nil || retryID != id {
		t.Fatalf("create retry: id=%d err=%v", retryID, err)
	}
	mustMembers(t, s, id)
	if _, err = s.Publish(ctx, 10, id, 2, randomUUID()); err != nil {
		t.Fatal(err)
	}
	if err = s.runNextJob(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.runNextNotification(ctx); err != nil {
		t.Fatal(err)
	}
	p, _, err := s.Pocket(ctx, 10, id)
	if err != nil || p.Status != "collecting" {
		t.Fatalf("published pocket: %+v err=%v", p, err)
	}
}
