package migrations

import "testing"

func TestEmbeddedMigrationFiles(t *testing.T) {
	for _, name := range []string{
		"files/000001_init.up.sql",
		"files/000001_init.down.sql",
		"files/000002_review_schema.up.sql",
		"files/000002_review_schema.down.sql",
		"files/000003_identity_and_order_limits.up.sql",
		"files/000003_identity_and_order_limits.down.sql",
	} {
		file, err := FS.Open(name)
		if err != nil {
			t.Fatalf("open embedded migration %q: %v", name, err)
		}
		_ = file.Close()
	}
}
