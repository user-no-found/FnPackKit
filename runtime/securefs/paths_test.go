package securefs

import "testing"

func TestUserDirectoryNameBoundary(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../outside", "nested/child", "a\\b", "a\x00b"} {
		if _, err := UserDirectory("/", name, 1000, 1000); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	if _, err := UserDirectory("/", "root", 0, 0); err == nil {
		t.Fatal("root accepted")
	}
}

func TestRootPathRequiresAbsolute(t *testing.T) {
	for _, path := range []string{"", ".", "etc", "../etc"} {
		if _, err := RootPath(path); err == nil {
			t.Fatalf("relative root path accepted: %q", path)
		}
	}
}
