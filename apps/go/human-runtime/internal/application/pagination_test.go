package application

import (
	"fmt"
	"testing"
	"testing/quick"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
)

// Feature: rust-bpm-platform, Property 35: Keyset pages are bounded and traverse each work item once
func TestKeysetPaginationIsCompleteAndDuplicateFree(t *testing.T) {
	property := func(rawCount, rawLimit uint8) bool {
		count := int(rawCount)
		limit := int(rawLimit%31) + 1
		all := make([]domain.WorkItem, count)
		for index := range all {
			all[index] = domain.WorkItem{
				ID:        fmt.Sprintf("work-%04d", count-index),
				UpdatedAt: time.Unix(int64(count-index), 0).UTC(),
			}
		}
		cursor := (*PageCursor)(nil)
		seen := make(map[string]struct{}, count)
		for {
			fetched := make([]domain.WorkItem, 0, limit+1)
			for _, item := range all {
				if cursor != nil &&
					!item.UpdatedAt.Before(cursor.UpdatedAt) &&
					!(item.UpdatedAt.Equal(cursor.UpdatedAt) && item.ID < cursor.WorkItemID) {
					continue
				}
				fetched = append(fetched, item)
				if len(fetched) == limit+1 {
					break
				}
			}
			page, next := BuildWorkItemPage(fetched, limit)
			if len(page) > limit {
				return false
			}
			for _, item := range page {
				if _, duplicate := seen[item.ID]; duplicate {
					return false
				}
				seen[item.ID] = struct{}{}
			}
			if next == nil {
				break
			}
			cursor = next
		}
		return len(seen) == len(all)
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}
