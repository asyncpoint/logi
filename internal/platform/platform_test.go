package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatPathAndIdentity(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.log")
	if err := os.WriteFile(p, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := StatPath(p)
	if err != nil || info.Size != 6 || info.ID.IsZero() {
		t.Fatalf("StatPath = %+v, %v", info, err)
	}
	f, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, err := IdentityOfFile(f)
	if err != nil || fi.ID != info.ID || fi.Size != info.Size {
		t.Fatalf("open handle %+v (%v) disagrees with path %+v", fi, err, info)
	}

	// Appending keeps identity and grows size.
	af, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	af.WriteString("more\n")
	af.Close()
	grown, _ := StatPath(p)
	if grown.ID != info.ID || grown.Size != 11 {
		t.Fatalf("after append: %+v (was %+v)", grown, info)
	}

	// A different file has a different identity.
	q := filepath.Join(dir, "b.log")
	os.WriteFile(q, []byte("x"), 0o644)
	other, _ := StatPath(q)
	if other.ID == info.ID {
		t.Fatal("two distinct files share an identity")
	}

	// Rotation: rename + recreate under the same path changes identity, while
	// the already-open handle still follows the old file.
	os.Rename(p, p+".1")
	os.WriteFile(p, []byte("new\n"), 0o644)
	rotated, _ := StatPath(p)
	if rotated.ID == info.ID {
		t.Fatal("recreated path kept the old identity")
	}
	if still, _ := IdentityOfFile(f); still.ID != info.ID {
		t.Fatal("open handle should still identify the old file")
	}

	if _, err := StatPath(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("StatPath on a missing file must fail")
	}
}
