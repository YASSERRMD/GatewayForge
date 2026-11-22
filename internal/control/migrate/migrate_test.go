package migrate

import (
	"testing"
	"testing/fstest"
)

func TestLoadValidationAndOrdering(t *testing.T) {
	validFS := fstest.MapFS{
		"0002_second.sql": &fstest.MapFile{Data: []byte("SELECT 2;")},
		"0001_first.sql":  &fstest.MapFile{Data: []byte("SELECT 1;")},
		"README.md":       &fstest.MapFile{Data: []byte("ignored")},
	}
	list, err := Load(validFS)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(list) != 2 || list[0].Version != 1 || list[1].Version != 2 {
		t.Fatalf("unexpected migration order: %+v", list)
	}
	if len(list[0].Checksum) != 64 {
		t.Fatalf("expected 64-hex sha256 checksum, got %q", list[0].Checksum)
	}

	dupFS := fstest.MapFS{
		"0001_a.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
		"0001_b.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
	}
	if _, err := Load(dupFS); err == nil {
		t.Fatal("expected duplicate version error")
	}

	badFS := fstest.MapFS{
		"invalid.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
	}
	if _, err := Load(badFS); err == nil {
		t.Fatal("expected bad filename error")
	}
}
