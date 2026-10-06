package service

import (
	"testing"
	"time"
)

func TestSameUpdatedAtVersion(t *testing.T) {
	t.Parallel()

	dbUpdatedAt := time.Date(2026, 8, 3, 9, 10, 11, 123456000, time.UTC)
	clientUpdatedAt := time.Date(2026, 8, 3, 17, 10, 11, 123456000, time.FixedZone("UTC+8", 8*60*60))

	if !sameUpdatedAtVersion(dbUpdatedAt, clientUpdatedAt) {
		t.Fatal("the same version in different time zones must match")
	}

	if sameUpdatedAtVersion(dbUpdatedAt, clientUpdatedAt.Add(time.Second)) {
		t.Fatal("timestamps in different UTC seconds must not match")
	}
}

func TestSameUpdatedAtVersionRejectsDifferentVersionWithinSecond(t *testing.T) {
	t.Parallel()
	current := time.Date(2026, 8, 3, 9, 10, 11, 123456000, time.UTC)
	if sameUpdatedAtVersion(current, current.Add(time.Microsecond)) {
		t.Fatal("different database versions within the same second must conflict")
	}
}
