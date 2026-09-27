package migrations

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Every migration is embedded into the migrate binary. Keep the sequence
// contiguous and require a reversible Down section so a disposable database
// can exercise an upgrade/rollback round trip before phase 8.
func TestEmbeddedMigrationsAreContiguousAndReversible(t *testing.T) {
	entries, err := FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	nameRE := regexp.MustCompile(`^(\d{5})_[^/]+\.sql$`)
	versions := make([]int, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := nameRE.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		version, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		source, err := FS.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		text := string(source)
		if strings.Count(text, "-- +goose Up") != 1 || strings.Count(text, "-- +goose Down") != 1 {
			t.Errorf("%s must contain exactly one goose Up and Down section", entry.Name())
		}
		versions = append(versions, version)
	}
	if len(versions) == 0 {
		t.Fatal("no embedded migrations found")
	}
	sort.Ints(versions)
	if versions[0] != 1 {
		t.Fatalf("migration sequence starts at %05d, want %05d", versions[0], 1)
	}
	for i := 1; i < len(versions); i++ {
		if versions[i] != versions[i-1]+1 {
			t.Fatalf("migration sequence gap between %05d and %05d", versions[i-1], versions[i])
		}
	}
	if got := versions[len(versions)-1]; got != len(versions) {
		t.Fatalf("migration %05d is not the %dth file", got, len(versions))
	}
}
