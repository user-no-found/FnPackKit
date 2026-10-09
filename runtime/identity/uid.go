package identity

import (
	"errors"
	"strconv"
	"strings"
)

// ResolveUID obtains the current NSS name and applies the same two-way identity checks as Resolve.
// UID-only persisted data cannot establish whether an account was deleted and its UID reused.
func ResolveUID(uid uint32) (User, error) {
	if uid < 1000 || uid == 65534 || uid == 4294967295 {
		return User{}, errors.New("identity is not a NAS user")
	}
	row, err := lookup("/usr/bin/getent", "passwd", strconv.FormatUint(uint64(uid), 10))
	if err != nil {
		return User{}, errors.New("UID is absent from NSS")
	}
	fields := strings.Split(strings.TrimSuffix(row, "\n"), ":")
	if len(fields) != 7 {
		return User{}, errors.New("invalid NSS record")
	}
	return Resolve(Request{UID: uid, Name: fields[0]})
}
