"""Directory modes survive strict defaults and real archive extraction."""
from contextlib import contextmanager
import io
import os
from pathlib import Path
import shutil
import stat
import subprocess
import tarfile
import tempfile
import unittest

from fnpackkit.contracts import Lifecycle, LifecycleCommand, Payload, PayloadFile
from fnpackkit.lifecycle import generate_lifecycle
from fnpackkit.lifecycle_verify import LifecycleFixture
from buildflow.contracts import Context, Project
from buildflow.runner import Runner
from fnpackkit.packaging import package as pack_payload


@contextmanager
def strict_defaults():
    original = os.umask(0o077)
    try:
        yield
    finally:
        os.umask(original)


def mode(path):
    return stat.S_IMODE(path.stat().st_mode)


class DirectoryModesTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="fnpack-directory-modes-")
        self.root = Path(self.temp.name)
        self.addCleanup(self.cleanup)

    def cleanup(self):
        for path in [self.root, *self.root.rglob("*")]:
            if path.is_dir() and not path.is_symlink():
                path.chmod(0o700)
        self.temp.cleanup()

    def package(self):
        stage = self.root / "source"
        (stage / "app/libexec").mkdir(parents=True)
        (stage / "app/private").mkdir()
        (stage / "app/readonly").mkdir()
        hook = stage / "app/libexec/hook"
        hook.write_text("#!/bin/sh\nid -u\n")
        hook.chmod(0o755)
        (stage / "app/private/secret").write_text("synthetic private bytes")
        (stage / "app/private/secret").chmod(0o600)
        (stage / "app/readonly/note").write_text("readonly content")
        (stage / "app/readonly/note").chmod(0o400)
        for name, value in [("libexec", 0o755), ("private", 0o700), ("readonly", 0o500)]:
            (stage / "app" / name).chmod(value)
        (stage / "manifest").write_text("appname=directory-fixture\nversion=1.0.0\n")
        declaration = Lifecycle({"install_callback": LifecycleCommand(
            "target", "libexec/hook", run_as="package")})
        generate_lifecycle(stage, declaration)
        content = io.BytesIO()
        with tarfile.open(fileobj=content, mode="w:gz") as inner:
            for path in sorted((stage / "app").rglob("*")):
                inner.add(path, arcname=path.relative_to(stage / "app").as_posix(),
                          recursive=False)
        package = self.root / "directory-fixture.fpk"
        with tarfile.open(package, "w:gz") as outer:
            member = tarfile.TarInfo("app.tgz")
            member.size = len(content.getvalue())
            outer.addfile(member, io.BytesIO(content.getvalue()))
            for path in sorted(stage.rglob("*")):
                if path == stage / "app" or stage / "app" in path.parents:
                    continue
                outer.add(path, arcname=path.relative_to(stage).as_posix(), recursive=False)
        return package

    def test_archive_directory_modes_are_restored_under_strict_defaults(self):
        package = self.package()
        target = self.root / "fixture"
        target.mkdir(mode=0o700)
        with strict_defaults():
            model = LifecycleFixture(package, target)
            model.deploy()
        self.assertEqual(mode(model.app / "libexec"), 0o755)
        self.assertEqual(mode(model.app / "private"), 0o700)
        self.assertEqual(mode(model.app / "private/secret"), 0o600)
        self.assertEqual(mode(model.app / "readonly"), 0o500)
        self.assertEqual((model.app / "readonly/note").read_text(), "readonly content")
        self.assertEqual(mode(model.metadata / "cmd"), 0o755)
        self.assertEqual(mode(model.root), 0o700)
        result = subprocess.run([str(model.app / "libexec/hook")],
                                capture_output=True, text=True, check=True)
        self.assertEqual(result.stdout.strip(), str(os.geteuid()))

    def test_directory_helper_keeps_existing_private_parents_and_rejects_links(self):
        from fnpackkit.directories import ensure_directory
        private = self.root / "private"
        private.mkdir(mode=0o700)
        with strict_defaults():
            ensure_directory(self.root, private / "new/deep")
        self.assertEqual(mode(private), 0o700)
        self.assertEqual(mode(private / "new"), 0o755)
        self.assertEqual(mode(private / "new/deep"), 0o755)
        external = self.root / "outside"
        external.mkdir()
        (self.root / "linked").symlink_to(external)
        with self.assertRaises(ValueError):
            ensure_directory(self.root, self.root / "linked/escape")
        with self.assertRaises(ValueError):
            ensure_directory(self.root, self.root / "../escape")
        self.assertFalse((external / "escape").exists())

    def test_lifecycle_directories_ignore_umask_and_preserve_existing_private_modes(self):
        for existing in (False, True):
            with self.subTest(existing=existing):
                stage = self.root / ("existing" if existing else "new")
                (stage / "app").mkdir(parents=True)
                hook = stage / "app/hook"
                hook.write_text("#!/bin/sh\nexit 0\n")
                hook.chmod(0o755)
                if existing:
                    (stage / "cmd/hooks").mkdir(parents=True)
                    (stage / "cmd").chmod(0o700)
                    (stage / "cmd/hooks").chmod(0o700)
                profile = Lifecycle({"install_callback": LifecycleCommand(
                    "target", "hook", run_as="package")})
                with strict_defaults():
                    generate_lifecycle(stage, profile)
                expected = 0o700 if existing else 0o755
                self.assertEqual(mode(stage / "cmd"), expected)
                self.assertEqual(mode(stage / "cmd/hooks"), expected)
                self.assertEqual(mode(stage / "cmd/hooks/fnpack_identity.py"), 0o755)

    @unittest.skipUnless(os.geteuid() == 0 and os.environ.get("FNP_TEST_USERNAME"),
                         "requires explicit synthetic root/nonroot verification")
    def test_root_namespace_parent_modes_allow_real_nonroot_execution(self):
        import pwd
        from fnpackkit.namespace_fixture import NamespaceFixture
        package = self.package()
        self.root.chmod(0o711)
        target = self.root / "namespace"
        target.mkdir(mode=0o700)
        with strict_defaults():
            model = NamespaceFixture(package, target, os.environ["FNP_TEST_USERNAME"])
            outputs = []
            def run(argv, cwd, env):
                result = subprocess.run(argv, cwd=cwd, env={"PATH": "/usr/bin:/bin", **env},
                                        stdin=subprocess.DEVNULL, capture_output=True,
                                        text=True, check=True)
                outputs.append(result.stdout)
                return result.stdout
            model.install(run)
        for path in [model.volume, model.metadata, model.volume / "@appcenter",
                     *[p.parent for p in model.roots.values()]]:
            self.assertEqual(mode(path), 0o755)
        self.assertEqual(mode(model.app / "private"), 0o700)
        self.assertEqual(mode(model.app / "private/secret"), 0o600)
        self.assertEqual(outputs[-1].strip(), str(pwd.getpwnam(os.environ["FNP_TEST_USERNAME"]).pw_uid))


@unittest.skipUnless(os.environ.get("FNPACKKIT_E2E") == "1", "requires real fnpack")
class NativeDirectoryModesTests(unittest.TestCase):
    def test_staging_and_actual_archive_keep_declared_modes_with_strict_defaults(self):
        with tempfile.TemporaryDirectory(prefix="fnpack-stage-modes-") as directory:
            root = Path(directory)
            subprocess.run(["fnpack", "create", "template", "--without-ui"],
                           cwd=root, check=True, capture_output=True)
            template = root / "template"
            for p in template.rglob(".DS_Store"):
                p.unlink()
            (template / "manifest").unlink()
            (template / "manifest.in").write_text(
                "appname=@APP_ID@\nversion=@VERSION@\ndisplay_name=Modes\n"
                "desc=Directory modes\nplatform=@PLATFORM@\nsource=thirdparty\n"
                "maintainer=Example\ndistributor=Example\n")
            (template / "app/libexec").mkdir()
            (template / "app/libexec").chmod(0o755)
            (template / "app/private").mkdir()
            (template / "app/private").chmod(0o700)
            program = root / "program"
            program.write_text("#!/bin/sh\nexit 0\n")
            payload = Payload(template, [
                PayloadFile(program, "app/libexec/hook", 0o755),
                PayloadFile(program, "app/generated/deep/program", 0o755),
                PayloadFile(program, "app/private/secret", 0o600)],
                {"APP_ID": "fixture", "VERSION": "1.0.0", "PLATFORM": "x86"},
                "fixture_1.0.0_x86.fpk")
            project = Project(root / "project.json", root,
                              {"app_id": "fixture", "version": "1.0.0"}, {})
            ctx = Context(project, root / "work", root / "output", Runner(root / "build.log"))
            with strict_defaults():
                artifacts = pack_payload(ctx, payload)
                current = os.umask(0o077)
                self.assertEqual(current, 0o077, "packaging changed the caller umask")
            stage = ctx.work / "fpk/fixture_1.0.0_x86/stage"
            self.assertEqual(mode(stage / "app/libexec"), 0o755)
            self.assertEqual(mode(stage / "app/generated/deep"), 0o755)
            self.assertEqual(mode(stage / "app/private"), 0o700)
            self.assertEqual(mode(stage / "app/private/secret"), 0o600)
            with tarfile.open(artifacts[0].path) as outer:
                with outer.extractfile("app.tgz") as stream, tarfile.open(fileobj=stream) as inner:
                    entries = {i.name.rstrip("/"): i.mode for i in inner}
            self.assertEqual(entries["libexec"], 0o755)
            self.assertEqual(entries["generated/deep"], 0o755)
            self.assertEqual(entries["private"], 0o700)
            self.assertEqual(entries["private/secret"], 0o600)


if __name__ == "__main__":
    unittest.main(verbosity=2)
