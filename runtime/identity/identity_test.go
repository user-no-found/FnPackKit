package identity

import (
	"os"
	"os/user"
	"testing"
)

// NSS output is trusted only after the requested uid/name pair is matched.
func TestPasswdBoundary(t *testing.T) {
	good := "sun:x:1000:1001:Name:/home/sun:/bin/bash\n"
	if _, err := parsePasswd(good, 1000, "sun"); err != nil {
		t.Fatal(err)
	}
	for _, row := range []string{
		"root:x:0:0:root:/root:/bin/bash", "other:x:1000:1001::/home/other:/bin/bash",
		"sun:x:1001:1001::/home/sun:/bin/bash", "sun:x:1000:1001::relative:/bin/bash",
		"sun:x:1000:1001::/home/sun:relative-shell", good + good,
	} {
		if _, err := parsePasswd(row, 1000, "sun"); err == nil {
			t.Fatalf("accepted invalid record %q", row)
		}
	}
}

// The native current account is resolved through both NSS directions without changing it.
func TestCurrentNSSBoundary(t *testing.T) {
	uid := uint32(os.Getuid())
	if uid < 1000 || uid == 65534 {
		t.Skip("requires an ordinary current user")
	}
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve(Request{UID: uid, Name: account.Username})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.UID != uid || resolved.Name != account.Username || len(resolved.Groups) == 0 {
		t.Fatal("incorrect NSS identity")
	}
	if _, err := Resolve(Request{UID: uid, Name: account.Username + "-invalid"}); err == nil {
		t.Fatal("mismatched NSS identity accepted")
	}
}
