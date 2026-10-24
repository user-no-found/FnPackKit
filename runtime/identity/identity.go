// Package identity resolves authenticated NAS identities through system NSS.
package identity

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// User contains the authoritative operating-system identity of one NAS user.
type User struct {
	UID    uint32
	GID    uint32
	Groups []uint32
	Name   string
	Home   string
	Shell  string
}

// Request identifies a NAS account across trusted application control boundaries.
// It does not authenticate a transport or grant application access.
type Request struct {
	UID  uint32 `json:"uid"`
	Name string `json:"name"`
}

// Resolve rechecks the exact name/UID pair and the account's current groups.
func Resolve(request Request) (User, error) {
	if request.UID < 1000 || request.UID == 65534 || request.UID == 4294967295 || !ValidName(request.Name) {
		return User{}, errors.New("identity is not a NAS user")
	}
	row, err := lookup("/usr/bin/getent", "passwd", strconv.FormatUint(uint64(request.UID), 10))
	if err != nil {
		return User{}, errors.New("UID is absent from NSS")
	}
	user, err := parsePasswd(row, request.UID, request.Name)
	if err != nil {
		return User{}, err
	}
	byName, err := lookup("/usr/bin/getent", "passwd", request.Name)
	if err != nil || strings.TrimSpace(byName) != strings.TrimSpace(row) {
		return User{}, errors.New("NSS name and UID disagree")
	}
	groups, err := lookup("/usr/bin/id", "-G", "--", user.Name)
	if err != nil {
		return User{}, errors.New("cannot resolve supplementary groups")
	}
	for _, value := range strings.Fields(groups) {
		group, err := strconv.ParseUint(value, 10, 32)
		if err != nil || group == 4294967295 {
			return User{}, errors.New("NSS returned invalid group")
		}
		user.Groups = append(user.Groups, uint32(group))
	}
	if !slices.Contains(user.Groups, user.GID) {
		return User{}, errors.New("primary group missing from NSS group list")
	}
	slices.Sort(user.Groups)
	user.Groups = slices.Compact(user.Groups)
	return user, nil
}

// SystemAccount resolves service and trusted gateway accounts without granting them terminals.
func SystemAccount(name string) (User, error) {
	if !ValidName(name) {
		return User{}, errors.New("invalid service account name")
	}
	row, err := lookup("/usr/bin/getent", "passwd", name)
	if err != nil {
		return User{}, fmt.Errorf("service account %s is absent", name)
	}
	fields := strings.Split(strings.TrimSpace(row), ":")
	if len(fields) != 7 || fields[0] != name {
		return User{}, errors.New("invalid service account record")
	}
	uid, e1 := strconv.ParseUint(fields[2], 10, 32)
	gid, e2 := strconv.ParseUint(fields[3], 10, 32)
	if e1 != nil || e2 != nil || uid == 4294967295 || gid == 4294967295 {
		return User{}, errors.New("invalid service account UID/GID")
	}
	return User{UID: uint32(uid), GID: uint32(gid), Name: name}, nil
}

// ValidName excludes NSS option syntax and multi-record/header encodings.
func ValidName(name string) bool {
	return len(name) > 0 && len(name) <= 128 && !strings.HasPrefix(name, "-") && !strings.ContainsAny(name, ":/\\\x00\r\n\t ,")
}

func parsePasswd(row string, uid uint32, name string) (User, error) {
	row = strings.TrimSuffix(row, "\n")
	fields := strings.Split(row, ":")
	if len(fields) != 7 || strings.ContainsAny(row, "\r\n") || fields[0] != name || fields[2] != strconv.FormatUint(uint64(uid), 10) || uid < 1000 {
		return User{}, errors.New("NSS identity mismatch")
	}
	gid, err := strconv.ParseUint(fields[3], 10, 32)
	if err != nil || gid == 4294967295 || !filepath.IsAbs(fields[5]) || filepath.Clean(fields[5]) != fields[5] || fields[5] == "/" {
		return User{}, errors.New("invalid NSS home or group")
	}
	if !filepath.IsAbs(fields[6]) {
		return User{}, errors.New("invalid NSS shell path")
	}
	return User{UID: uid, GID: uint32(gid), Name: name, Home: fields[5], Shell: fields[6]}, nil
}

func lookup(program string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	data, err := cmd.Output()
	if len(data) > 16384 {
		return "", errors.New("NSS output exceeds limit")
	}
	return string(data), err
}
