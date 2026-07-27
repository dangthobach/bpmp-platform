package projection

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"testing/quick"
)

func events(count int) []Event {
	result := make([]Event, count)
	statuses := []string{"ACTIVE", "COMPLETED", "FAILED"}
	for index := range result {
		result[index] = Event{
			TenantID: "tenant-a",
			Sequence: uint64(index + 1),
			EntityID: fmt.Sprintf("entity-%03d", index%17),
			Status:   statuses[index%len(statuses)],
			Deleted:  index%11 == 0,
		}
	}
	return result
}

// Feature: rust-bpm-platform, Property 42: Projection resume equals full rebuild
func TestCheckpointResumeEqualsFullRebuild(t *testing.T) {
	property := func(rawCount, rawSplit uint8) bool {
		all := events(int(rawCount%150) + 1)
		split := int(rawSplit) % (len(all) + 1)
		incremental := New()
		for _, event := range all[:split] {
			if incremental.Apply(event) != nil {
				return false
			}
		}
		resumed, err := NewFromSnapshot(incremental.Snapshot())
		if err != nil {
			return false
		}
		for _, event := range all[split:] {
			if resumed.Apply(event) != nil {
				return false
			}
		}
		rebuilt, err := Rebuild(all)
		return err == nil && reflect.DeepEqual(resumed.Snapshot(), rebuilt.Snapshot())
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// Feature: rust-bpm-platform, Property 43: Read-model query exactly applies tenant and field filters
func TestQueryMatchesBackingFilter(t *testing.T) {
	property := func(rawCount, rawStatus uint8, includeDeleted bool) bool {
		all := events(int(rawCount%150) + 1)
		projector, err := Rebuild(all)
		if err != nil {
			return false
		}
		statuses := []string{"ACTIVE", "COMPLETED", "FAILED"}
		selected := statuses[int(rawStatus)%len(statuses)]
		filter := Filter{
			TenantID:      "tenant-a",
			Statuses:      map[string]struct{}{selected: {}},
			IncludeDelete: includeDeleted,
		}
		backing := projector.Snapshot().Records
		expected := make([]Record, 0)
		for _, record := range backing {
			if record.TenantID == filter.TenantID &&
				record.Status == selected &&
				(includeDeleted || !record.Deleted) {
				expected = append(expected, record)
			}
		}
		sort.Slice(expected, func(i, j int) bool {
			return expected[i].EntityID < expected[j].EntityID
		})
		return reflect.DeepEqual(projector.Query(filter), expected) &&
			len(projector.Query(Filter{TenantID: "tenant-b"})) == 0
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}
