"""Complete preflight, stage-only writes, mutation detection and transactional failure."""
from pathlib import Path
import unittest
from unittest.mock import patch

from buildflow.contracts import BuildError
from fnpackkit.optional_packages import apply_source
from fnpackkit.optional_patch import patch_paths
from fnpackkit import optional_transaction
from optional_fixture import OptionalFixture


class OptionalSourceTests(OptionalFixture, unittest.TestCase):
    def prepared(self, *, bad_patch=False):
        path = self.package(patches=["change.patch"], files=[
            {"source": "addition", "destination": "nested/added.txt", "mode": 0o755}])
        self.text_patch(path, old="absent" if bad_patch else "baseline")
        (path / "addition").write_text("new file\n")
        self.choose(["cyberstrike"])
        target = self.target()
        (target / "existing.txt").write_text("baseline\n")
        return path, target

    def test_private_dirty_baseline_patch_then_file_preserves_package_and_git(self):
        package, target = self.prepared()
        original = {name: (package / name).read_bytes() for name in ("change.patch", "addition", "fpk-option.json")}
        head = self.git(target, "rev-parse", "HEAD")
        (target / "unrelated.txt").write_text("keep")
        result = apply_source(self.ctx, target, source_origin="https://example.invalid/upstream.git", source_revision=head)
        self.assertEqual((target / "existing.txt").read_text(), "enhanced\n")
        self.assertEqual((target / "nested/added.txt").read_text(), "new file\n")
        self.assertEqual((target / "nested/added.txt").stat().st_mode & 0o777, 0o755)
        self.assertEqual((target / "unrelated.txt").read_text(), "keep")
        self.assertEqual(self.git(target, "rev-parse", "HEAD"), head)
        self.assertEqual(result["paths"], ["existing.txt", "nested/added.txt"])
        self.assertEqual({name: (package / name).read_bytes() for name in original}, original)

    def test_bad_patch_does_not_apply_earlier_changes_or_create_dirs(self):
        _, target = self.prepared(bad_patch=True)
        before = self.git(target, "status", "--porcelain=v1")
        with self.assertRaises(BuildError):
            apply_source(self.ctx, target)
        self.assertEqual((target / "existing.txt").read_text(), "baseline\n")
        self.assertEqual(self.git(target, "status", "--porcelain=v1"), before)
        self.assertFalse((target / "nested").exists())

    def test_text_patch_updates_read_only_target_without_partial_projection(self):
        package = self.package(patches=["change.patch"])
        (package / "change.patch").write_text(
            "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n"
            "diff --git a/z.txt b/z.txt\n--- a/z.txt\n+++ b/z.txt\n@@ -1 +1 @@\n-old\n+new\n")
        self.choose(["cyberstrike"])
        target = self.target()
        for name, mode in (("a.txt", 0o644), ("z.txt", 0o444)):
            (target / name).write_text("old\n")
            (target / name).chmod(mode)
        apply_source(self.ctx, target)
        for name in ("a.txt", "z.txt"):
            self.assertEqual((target / name).read_text(), "new\n")
        self.assertFalse(list(target.glob(".optional-write-*")))

    def test_second_patch_failure_does_not_pollute_target(self):
        package = self.package(patches=["change.patch", "second.patch"])
        self.text_patch(package)
        (package / "second.patch").write_text((package / "change.patch").read_text())
        self.choose(["cyberstrike"])
        target = self.target()
        with self.assertRaises(BuildError):
            apply_source(self.ctx, target)
        self.assertEqual((target / "existing.txt").read_text(), "original\n")
        self.assertEqual(self.git(target, "status", "--porcelain"), "")

    def test_files_never_replace_existing_target(self):
        package = self.package(files=[{"source": "addition", "destination": "existing.txt"}])
        (package / "addition").write_text("replace")
        self.choose(["cyberstrike"])
        target = self.target()
        with self.assertRaisesRegex(ValueError, "already exists"):
            apply_source(self.ctx, target)
        self.assertEqual((target / "existing.txt").read_text(), "original\n")

    def test_cross_package_patch_collision_fails_before_writing(self):
        for feature in ("cyberstrike", "other"):
            package = self.package(feature, patches=["change.patch"])
            self.text_patch(package)
        self.choose(["cyberstrike", "other"])
        target = self.target()
        with self.assertRaisesRegex(ValueError, "collision"):
            apply_source(self.ctx, target)
        self.assertEqual((target / "existing.txt").read_text(), "original\n")

    def test_file_patch_and_parent_file_collisions_rejected(self):
        package = self.package(patches=["change.patch"], files=[{"source": "add", "destination": "existing.txt"}])
        self.text_patch(package)
        (package / "add").write_text("data")
        self.choose(["cyberstrike"])
        target = self.target()
        with self.assertRaisesRegex(ValueError, "collision"):
            apply_source(self.ctx, target)

    def test_symlink_ancestor_does_not_write_outside_stage(self):
        _, target = self.prepared()
        outside = self.root / "outside"
        outside.mkdir()
        (target / "nested").symlink_to(outside, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, "symlink"):
            apply_source(self.ctx, target)
        self.assertEqual(list(outside.iterdir()), [])
        self.assertEqual((target / "existing.txt").read_text(), "baseline\n")

    def test_target_must_be_private_independent_git_checkout(self):
        _, target = self.prepared()
        for path in (self.main, self.work, self.work / "missing"):
            with self.subTest(path=path), self.assertRaises(ValueError):
                apply_source(self.ctx, path)
        (target / ".git/objects/info/alternates").write_text(str(self.main / ".git/objects") + "\n")
        with self.assertRaisesRegex(ValueError, "sharing"):
            apply_source(self.ctx, target)

    def test_source_identity_and_manifest_constraints_checked(self):
        package = self.package(source={"origin": "https://wrong.invalid/upstream.git"})
        self.choose(["cyberstrike"])
        target = self.target()
        with self.assertRaisesRegex(ValueError, "constraint"):
            apply_source(self.ctx, target)
        with self.assertRaisesRegex(ValueError, "caller identity"):
            apply_source(self.ctx, target, source_revision="0" * 40)
        self.assertEqual(self.git(target, "status", "--porcelain"), "")

    def test_changed_package_after_selection_fails_before_target(self):
        package, target = self.prepared()
        (package / "addition").write_text("changed")
        with self.assertRaisesRegex(ValueError, "input changed"):
            apply_source(self.ctx, target)
        self.assertEqual((target / "existing.txt").read_text(), "baseline\n")

    def test_target_mutation_during_scratch_preflight_is_detected(self):
        _, target = self.prepared()
        original = self.ctx.runner.capture
        changed = False

        def capture(argv, cwd, env=None):
            nonlocal changed
            result = original(argv, cwd=cwd, env=env)
            if "apply" in argv and "--check" not in argv and not changed:
                (target / "existing.txt").write_text("concurrent writer\n")
                changed = True
            return result

        with patch.object(self.ctx.runner, "capture", side_effect=capture), self.assertRaisesRegex(ValueError, "target changed"):
            apply_source(self.ctx, target)
        self.assertEqual((target / "existing.txt").read_text(), "concurrent writer\n")
        self.assertFalse((target / "nested").exists())

    def test_write_failure_rolls_back_prior_bytes_modes_and_new_directories(self):
        _, target = self.prepared()
        original = optional_transaction._write
        failed = False

        def write(path, value):
            nonlocal failed
            if path.name == "added.txt" and not failed:
                failed = True
                raise OSError("injected I/O failure")
            return original(path, value)

        before = self.git(target, "status", "--porcelain")
        with patch.object(optional_transaction, "_write", side_effect=write), self.assertRaisesRegex(OSError, "injected"):
            apply_source(self.ctx, target)
        self.assertEqual((target / "existing.txt").read_text(), "baseline\n")
        self.assertFalse((target / "nested").exists())
        self.assertEqual(self.git(target, "status", "--porcelain"), before)

    def test_parent_file_collision_is_diagnosed_before_projection(self):
        package = self.package(files=[{"source": "one", "destination": "parent"},
                                      {"source": "two", "destination": "parent/child"}])
        (package / "one").write_text("one")
        (package / "two").write_text("two")
        self.choose(["cyberstrike"])
        target = self.target()
        with self.assertRaisesRegex(ValueError, "file/directory collision"):
            apply_source(self.ctx, target)
        self.assertFalse((target / "parent").exists())

    def test_git_parser_cannot_apply_an_extra_unlisted_raw_diff(self):
        package = self.package(patches=["change.patch"])
        self.text_patch(package)
        with (package / "change.patch").open("a") as stream:
            stream.write("--- /dev/null\n+++ b/hidden\n@@ -0,0 +1 @@\n+unexpected\n")
        self.choose(["cyberstrike"])
        target = self.target()
        with self.assertRaisesRegex(ValueError, "undeclared or ambiguous"):
            apply_source(self.ctx, target)
        self.assertFalse((target / "hidden").exists())
        self.assertEqual((target / "existing.txt").read_text(), "original\n")

    def test_ordered_text_patch_addition_and_deletion(self):
        package = self.package(patches=["change.patch", "create.patch"])
        (package / "change.patch").write_text(
            "diff --git a/existing.txt b/existing.txt\ndeleted file mode 100644\n"
            "--- a/existing.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-original\n")
        (package / "create.patch").write_text(
            "diff --git a/created.txt b/created.txt\nnew file mode 100755\n"
            "--- /dev/null\n+++ b/created.txt\n@@ -0,0 +1 @@\n+created\n")
        self.choose(["cyberstrike"])
        target = self.target()
        apply_source(self.ctx, target)
        self.assertFalse((target / "existing.txt").exists())
        self.assertEqual((target / "created.txt").read_text(), "created\n")
        self.assertEqual((target / "created.txt").stat().st_mode & 0o777, 0o755)

    def test_unsafe_patch_paths_modes_binary_and_renames_rejected(self):
        samples = [
            b"diff --git a/../escape b/../escape\n--- a/../escape\n+++ b/../escape\n@@ -1 +1 @@\n-a\n+b\n",
            b"diff --git a/.git/config b/.git/config\n--- a/.git/config\n+++ b/.git/config\n@@ -1 +1 @@\n-a\n+b\n",
            b"diff --git a/a b/b\nsimilarity index 100%\nrename from a\nrename to b\n",
            b"diff --git a/a b/a\nnew file mode 120000\n--- /dev/null\n+++ b/a\n@@ -0,0 +1 @@\n+target\n",
            b"diff --git a/a b/a\nGIT binary patch\nliteral 1\nx\n",
        ]
        for content in samples:
            with self.subTest(content=content), self.assertRaises(ValueError):
                patch_paths(content)


if __name__ == "__main__":
    unittest.main()
