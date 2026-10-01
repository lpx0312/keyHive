package cli

import (
	"encoding/json"
	"testing"
	"time"
)

func TestStaleFilterAndCount(t *testing.T) {
	old := time.Now().AddDate(0, 0, -120).UTC().Format(time.RFC3339)
	fresh := time.Now().UTC().Format(time.RFC3339)
	data, _ := json.Marshal([]staleEntry{{UpdatedAt: old}, {UpdatedAt: fresh}, {UpdatedAt: old}})

	if n := countStale(data, 90); n != 2 {
		t.Fatalf("countStale 期望 2，实际 %d", n)
	}
	filtered, err := filterStale(data, 90)
	if err != nil {
		t.Fatal(err)
	}
	var list []staleEntry
	json.Unmarshal(filtered, &list)
	if len(list) != 2 {
		t.Fatalf("filterStale 期望 2 条，实际 %d", len(list))
	}
}

func TestParseListAndSearchArgs(t *testing.T) {
	if c, d := parseListArgs([]string{"--category", "mysql", "--stale", "30"}); c != "mysql" || d != 30 {
		t.Fatalf("parseListArgs: %s %d", c, d)
	}
	if q, d := parseSearchArgs([]string{"华为", "--stale", "90"}); q != "华为" || d != 90 {
		t.Fatalf("parseSearchArgs: %s %d", q, d)
	}
	if q, _ := parseSearchArgs([]string{"--stale", "90", "x"}); q != "x" {
		t.Fatalf("关键词在后的解析: %q", q)
	}
}
