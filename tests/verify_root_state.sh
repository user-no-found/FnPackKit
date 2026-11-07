#!/usr/bin/env bash
# Run shared lifecycle checks in a synthetic volume with no NAS application mounts.
set -euo pipefail
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
buildflow="${BUILDFLOW_PATH:?set the registered BuildFlow checkout}"
"${DOCKER_BIN:-/usr/bin/docker}" run --rm -i --network none --entrypoint /bin/sh \
  --mount "type=bind,src=$root/src,dst=/kit/src,readonly" \
  --mount "type=bind,src=$root/tests,dst=/kit/tests,readonly" \
  --mount "type=bind,src=$buildflow/src,dst=/buildflow/src,readonly" \
  --mount type=bind,src=/usr/bin/python3.11,dst=/usr/bin/python3,readonly \
  --mount type=bind,src=/usr/lib/python3.11,dst=/usr/lib/python3.11,readonly \
  --mount type=bind,src=/lib/x86_64-linux-gnu/libexpat.so.1,dst=/lib/x86_64-linux-gnu/libexpat.so.1,readonly \
  --env FNP_TEST_USERNAME=fixtureowner \
  --env FNP_TEST_NAS_USERNAME=fixturenas \
  "${FNPACKKIT_ROOT_IMAGE:-postgres:15}" -se <<'FIXTURE'
groupadd -g 1000 fixtureowner
useradd -m -u 1000 -g 1000 fixtureowner
groupadd -g 1001 fixturenas
useradd -m -u 1001 -g 1001 fixturenas
exec /usr/bin/python3 -I -c 'import sys,unittest;sys.path[:0]=["/kit/src","/kit/tests","/kit","/buildflow/src"];suite=unittest.defaultTestLoader.loadTestsFromNames(["test_root_state", "test_uninstall", "test_nas_uninstall"]);raise SystemExit(not unittest.TextTestRunner(verbosity=1).run(suite).wasSuccessful())'
FIXTURE
