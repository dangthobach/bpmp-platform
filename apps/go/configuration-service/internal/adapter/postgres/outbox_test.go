package postgres

import (
	"testing"
	"time"
)

func TestPublicationLeaseIsExclusiveEvenForSameWorkerID(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	owner := "configuration-publisher"
	activeUntil := now.Add(time.Second)
	expiredAt := now.Add(-time.Second)

	if !publicationLeaseIsActive(&owner, &activeUntil, now) {
		t.Fatal("an unexpired lease must block every publisher instance")
	}
	if publicationLeaseIsActive(&owner, &expiredAt, now) {
		t.Fatal("an expired lease must be reclaimable")
	}
}
