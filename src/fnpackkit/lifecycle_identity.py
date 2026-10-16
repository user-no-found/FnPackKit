"""Standalone package-account dispatcher; embedded into generated FPK metadata."""
import os
import grp
import pwd
import re
import stat
import sys


def package_identity():
    uid, gid = os.environ.get('TRIM_UID', ''), os.environ.get('TRIM_GID', '')
    username = os.environ.get('TRIM_USERNAME', '')
    if not uid.isdecimal() or not gid.isdecimal() or int(uid) == 0 or int(gid) == 0:
        raise ValueError('invalid package identity')
    if not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_.-]*', username):
        raise ValueError('invalid package account')
    account = pwd.getpwnam(username)
    group = grp.getgrgid(int(gid))
    if account.pw_uid != int(uid) or (account.pw_gid != group.gr_gid and username not in group.gr_mem):
        raise ValueError('package identity mismatch')
    return account, group


def main():
    try:
        account, group = package_identity()
        if len(sys.argv) < 2:
            raise ValueError('missing application command')
        program = sys.argv[1]
        mode = os.lstat(program).st_mode
        if not stat.S_ISREG(mode) or not mode & 0o111:
            raise ValueError('unsafe application command')
        if os.geteuid() not in (0, account.pw_uid):
            raise ValueError('incorrect lifecycle account')
        env = dict(os.environ)
        env.update(TRIM_RUN_UID=str(account.pw_uid), TRIM_RUN_GID=str(group.gr_gid),
                   PATH='/usr/bin:/bin', USER=account.pw_name, LOGNAME=account.pw_name)
        if os.geteuid() == account.pw_uid:
            if os.getegid() != group.gr_gid:
                raise ValueError('incorrect lifecycle group')
            os.execve(program, sys.argv[1:], env)
        os.execve('/usr/sbin/runuser', ['runuser', '-u', account.pw_name, '-g', group.gr_name, '--',
                                      *sys.argv[1:]], env)
    except (OSError, KeyError, ValueError):
        print('lifecycle: package-account dispatch rejected', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
