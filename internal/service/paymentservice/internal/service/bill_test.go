package service

import (
	"testing"
	"time"
)

func TestSameUpdatedAtSecond(t *testing.T) {
	t.Parallel()

	dbUpdatedAt := time.Date(2026, 8, 3, 9, 10, 11, 123456000, time.UTC)
	clientUpdatedAt := time.Date(2026, 8, 3, 17, 10, 11, 999999999, time.FixedZone("UTC+8", 8*60*60))

	if !sameUpdatedAtSecond(dbUpdatedAt, clientUpdatedAt) {
		t.Fatal("timestamps in the same UTC second must match")
	}

	if sameUpdatedAtSecond(dbUpdatedAt, clientUpdatedAt.Add(time.Second)) {
		t.Fatal("timestamps in different UTC seconds must not match")
	}
}
