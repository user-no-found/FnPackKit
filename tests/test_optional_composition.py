"""Explicit opt-in composition keeps selection-wide preflight and mutation barriers."""
import json
from pathlib import Path
import unittest
from unittest.mock import patch

from buildflow.contracts import BuildError
from fnpackkit.optional_manifest import parse
from fnpackkit.optional_packages import apply_source
from fnpackkit.optional_patch import patch_plan
from fnpackkit import optional_transaction
from optional_fixture import OptionalFixture


class OptionalCompositionTests(OptionalFixture, unittest.TestCase):
    @staticmethod
    def tree(root):
        return {path.relative_to(root).as_posix(): (path.read_bytes(), path.stat().st_mode)
                for path in root.rglob("*") if path.is_file()}

    def pair(self, declarations=(True, True)):
        packages = []
        for feature, declared, old, new in zip(
                ("alpha", "beta"), declarations, ("original", "first"), ("first", "final")):
            fields = {"shared_patch_targets": ["existing.txt"]} if declared else {}
            package = self.package(feature, patches=["change.patch"], **fields)
            self.text_patch(package, old=old, new=new)
            packages.append(package)
        return packages

    def prepare(self, declarations=(True, True)):
        packages = self.pair(declarations)
        self.choose(["beta", "alpha"])
        return packages, self.target()

    def rejected(self, packages, target, pattern, exception=ValueError):
        before = [self.tree(path) for path in (*packages, target)]
        with self.assertRaisesRegex(exception, pattern):
            apply_source(self.ctx, target)
        self.assertEqual([self.tree(path) for path in (*packages, target)], before)

    def reject_first_patch(self, content, pattern):
        packages = self.pair()
        (packages[0] / "change.patch").write_bytes(content)
        self.choose(["alpha", "beta"])
        self.rejected(packages, self.target(), pattern)

    def test_shared_declarations_are_bounded_unique_normalized_relative_arrays(self):
        base = {"schema": 1, "id": "alpha", "label": "Alpha", "project": "FnDSH",
                "patches": [], "files": []}
        invalid = [None, True, "existing.txt", {}, ["x"] * 2, [str(n) for n in range(4097)]]
        invalid += [[name] for name in ("", "/x", "../x", "a/../x", "./x", "a//b", "a/",
                                        ".GIT/config", "a\\b", "a\nx", "C:/x")]
        invalid += [[None], [1], [{}]]
        for value in invalid:
            with self.subTest(value=str(value)[:80]), self.assertRaises(ValueError):
                parse(json.dumps({**base, "shared_patch_targets": value}), project="FnDSH", directory="alpha")
        for value in ([], ["existing.txt"], [f"nested/{n}" for n in range(4096)]):
            parsed = parse(json.dumps({**base, "shared_patch_targets": value}), project="FnDSH", directory="alpha")
            self.assertEqual(parsed["shared_patch_targets"], value)
        self.assertNotIn("shared_patch_targets", parse(json.dumps(base), project="FnDSH", directory="alpha"))

    def test_default_same_target_still_rejected(self):
        packages, target = self.prepare((False, False))
        self.rejected(packages, target, "collision")

    def test_only_first_owner_declaring_is_rejected(self):
        packages, target = self.prepare((True, False))
        self.rejected(packages, target, "collision")

    def test_only_later_owner_declaring_is_rejected(self):
        packages, target = self.prepare((False, True))
        self.rejected(packages, target, "collision")

    def test_all_touching_packages_must_declare_not_just_first_two(self):
        packages = self.pair()
        third = self.package("third", patches=["change.patch"])
        self.text_patch(third, old="final", new="third")
        self.choose(["alpha", "beta", "third"])
        self.rejected([*packages, third], self.target(), "collision")

    def test_frozen_package_and_patch_order_composes_without_git_or_source_writes(self):
        packages = self.pair()
        self.manifest(packages[0], patches=["change.patch", "next.patch"])
        self.text_patch(packages[0], old="original", new="intermediate")
        (packages[0] / "next.patch").write_text(
            (packages[0] / "change.patch").read_text().replace("-original", "-intermediate").replace(
                "+intermediate", "+first"))
        record = self.choose(["beta", "alpha"])
        self.assertEqual([item["id"] for item in record["selected"]], ["alpha", "beta"])
        target = self.target()
        (target / "existing.txt").chmod(0o755)
        (target / "baseline-only").write_text("keep\n")
        sources = [self.tree(path) for path in packages]
        git_before = self.tree(target / ".git")
        with patch.object(self.ctx.runner, "capture", wraps=self.ctx.runner.capture) as capture:
            result = apply_source(self.ctx, target)
        self.assertEqual((target / "existing.txt").read_text(), "final\n")
        self.assertEqual((target / "existing.txt").stat().st_mode & 0o777, 0o755)
        self.assertEqual((target / "baseline-only").read_text(), "keep\n")
        self.assertEqual(result["paths"], ["existing.txt"])
        self.assertEqual([self.tree(path) for path in packages], sources)
        self.assertEqual(self.tree(target / ".git"), git_before)
        calls = [call for call in capture.call_args_list if "apply" in call.args[0]]
        expected = [str(Path(item["inputs"][index]["snapshot"]))
                    for item in record["selected"] for index in range(len(item["inputs"]))]
        for offset, snapshot in enumerate(expected):
            for index, flags in enumerate((["--numstat", "-z"], ["--check"], [])):
                call = calls[offset * 3 + index]
                argv = call.args[0]
                self.assertEqual(argv[argv.index("apply") + 1:], [*flags, snapshot])
                self.assertNotEqual(call.kwargs["cwd"], target)
        self.assertEqual(len(calls), 9)

    def test_declared_target_can_be_selected_without_other_cooperators(self):
        package = self.package("alpha", patches=["change.patch"], shared_patch_targets=["existing.txt"])
        self.text_patch(package)
        self.choose(["alpha"])
        target = self.target()
        apply_source(self.ctx, target)
        self.assertEqual((target / "existing.txt").read_text(), "enhanced\n")

    def test_dangling_or_extra_declarations_rejected(self):
        packages = self.pair()
        self.manifest(packages[0], shared_patch_targets=["existing.txt", "unused"])
        self.choose(["alpha", "beta"])
        self.rejected(packages, self.target(), "not modified.*unused")

    def test_declaration_without_any_patch_rejected(self):
        package = self.package("alpha", shared_patch_targets=["existing.txt"])
        self.choose(["alpha"])
        self.rejected([package], self.target(), "not modified")

    def test_files_projection_is_not_a_shared_patch_modify(self):
        package = self.package("alpha", shared_patch_targets=["addition"],
                               files=[{"source": "addition", "destination": "addition"}])
        (package / "addition").write_text("new\n")
        self.choose(["alpha"])
        self.rejected([package], self.target(), "not modified")

    def test_declared_noop_patch_is_not_a_modification(self):
        packages = self.pair()
        self.text_patch(packages[0], old="original", new="original")
        self.choose(["alpha", "beta"])
        self.rejected(packages, self.target(), "requires a content modification")

    def test_shared_addition_rejected(self):
        self.reject_first_patch(
            b"diff --git a/existing.txt b/existing.txt\nnew file mode 100644\n"
            b"--- /dev/null\n+++ b/existing.txt\n@@ -0,0 +1 @@\n+first\n", "text modification")

    def test_shared_deletion_rejected(self):
        self.reject_first_patch(
            b"diff --git a/existing.txt b/existing.txt\ndeleted file mode 100644\n"
            b"--- a/existing.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-original\n", "text modification")

    def test_shared_mode_change_even_with_text_hunk_rejected(self):
        self.reject_first_patch(
            b"diff --git a/existing.txt b/existing.txt\nold mode 100644\nnew mode 100755\n"
            b"--- a/existing.txt\n+++ b/existing.txt\n@@ -1 +1 @@\n-original\n+first\n", "mode changes")

    def test_shared_pure_mode_change_rejected(self):
        self.reject_first_patch(
            b"diff --git a/existing.txt b/existing.txt\nold mode 100644\nnew mode 100755\n", "text hunks")

    def test_binary_patch_rejected(self):
        self.reject_first_patch(
            b"diff --git a/existing.txt b/existing.txt\nGIT binary patch\nliteral 1\nx\n", "unsupported")

    def test_rename_rejected(self):
        self.reject_first_patch(
            b"diff --git a/existing.txt b/renamed.txt\nsimilarity index 100%\n"
            b"rename from existing.txt\nrename to renamed.txt\n", "renames")

    def test_declared_existing_file_must_be_utf8_and_not_binary(self):
        packages, target = self.prepare()
        for content in (b"original\n\xff", b"original\n\0"):
            (target / "existing.txt").write_bytes(content)
            with self.subTest(content=content):
                self.rejected(packages, target, "UTF-8|binary")

    def test_patch_with_non_utf8_or_nul_bytes_rejected(self):
        for suffix in (b"\xff", b"\0"):
            with self.subTest(suffix=suffix), self.assertRaises(ValueError):
                patch_plan(b"diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b" + suffix + b"\n",
                           shared_targets=["x"])

    def test_missing_existing_target_rejected(self):
        packages, target = self.prepare()
        (target / "existing.txt").unlink()
        self.rejected(packages, target, "existing regular UTF-8")

    def test_shared_patch_and_files_collision_rejected(self):
        packages = self.pair()
        self.manifest(packages[1], files=[{"source": "addition", "destination": "existing.txt"}])
        (packages[1] / "addition").write_text("replace\n")
        self.choose(["alpha", "beta"])
        self.rejected(packages, self.target(), "collision")

    def test_files_before_later_patch_cannot_be_overridden_by_declaration(self):
        packages = self.pair()
        self.manifest(packages[0], patches=[], shared_patch_targets=[],
                      files=[{"source": "addition", "destination": "existing.txt"}])
        (packages[0] / "addition").write_text("first\n")
        self.choose(["alpha", "beta"])
        self.rejected(packages, self.target(), "file/patch destination collision")

    def test_shared_target_file_directory_conflict_rejected(self):
        packages = self.pair()
        self.manifest(packages[1], files=[{"source": "addition", "destination": "existing.txt/child"}])
        (packages[1] / "addition").write_text("new\n")
        self.choose(["alpha", "beta"])
        self.rejected(packages, self.target(), "file/directory collision")

    def test_later_conflicting_patch_discards_the_entire_selection(self):
        packages = self.pair()
        self.text_patch(packages[1], old="original", new="conflicting")
        self.manifest(packages[0], files=[{"source": "addition", "destination": "nested/added"}])
        (packages[0] / "addition").write_text("new\n")
        self.choose(["alpha", "beta"])
        target = self.target()
        self.rejected(packages, target, "patch failed|does not apply", BuildError)
        self.assertFalse((target / "nested").exists())

    def test_original_and_snapshot_inputs_are_rechecked_after_preflight(self):
        packages, target = self.prepare()
        entry = self.ctx.metadata["optional_packages"]["selected"][1]["inputs"][0]
        capture = self.ctx.runner.capture
        for filename in (packages[1] / "change.patch", Path(entry["snapshot"])):
            content = filename.read_bytes()
            target_before = self.tree(target)
            mutated = False

            def mutate(argv, cwd, env=None):
                nonlocal mutated
                result = capture(argv, cwd=cwd, env=env)
                if "apply" in argv and "--check" not in argv and "--numstat" not in argv and not mutated:
                    filename.write_bytes(content + b"\n")
                    mutated = True
                return result

            with self.subTest(filename=filename), patch.object(self.ctx.runner, "capture", side_effect=mutate), \
                    self.assertRaisesRegex(ValueError, "input changed"):
                apply_source(self.ctx, target)
            self.assertTrue(mutated)
            self.assertEqual(self.tree(target), target_before)
            self.assertEqual(filename.read_bytes(), content + b"\n")
            filename.write_bytes(content)

    def test_target_changes_are_not_overwritten_by_composed_projection(self):
        packages, target = self.prepare()
        git_before = self.tree(target / ".git")
        sources = [self.tree(path) for path in packages]
        capture = self.ctx.runner.capture
        mutated = False

        def mutate(argv, cwd, env=None):
            nonlocal mutated
            result = capture(argv, cwd=cwd, env=env)
            if "apply" in argv and "--check" not in argv and "--numstat" not in argv and not mutated:
                (target / "existing.txt").write_text("external writer\n")
                mutated = True
            return result

        with patch.object(self.ctx.runner, "capture", side_effect=mutate), \
                self.assertRaisesRegex(ValueError, "target changed"):
            apply_source(self.ctx, target)
        self.assertTrue(mutated)
        self.assertEqual((target / "existing.txt").read_text(), "external writer\n")
        self.assertEqual(self.tree(target / ".git"), git_before)
        self.assertEqual([self.tree(path) for path in packages], sources)

    def test_shared_declarations_do_not_relax_files_to_files_collisions(self):
        packages = self.pair()
        for package in packages:
            self.manifest(package, files=[{"source": "addition", "destination": "new-file"}])
            (package / "addition").write_text("new\n")
        self.choose(["alpha", "beta"])
        self.rejected(packages, self.target(), "collision")

    def test_composed_target_rolls_back_if_final_files_projection_fails(self):
        packages = self.pair()
        self.manifest(packages[1], files=[{"source": "addition", "destination": "z/new-file"}])
        (packages[1] / "addition").write_text("new\n")
        self.choose(["alpha", "beta"])
        target = self.target()
        (target / "existing.txt").chmod(0o444)
        write = optional_transaction._write

        def fail_file(path, value):
            if path.name == "new-file":
                raise OSError("injected final projection failure")
            return write(path, value)

        with patch.object(optional_transaction, "_write", side_effect=fail_file):
            self.rejected(packages, target, "injected final projection", OSError)
        self.assertFalse((target / "z").exists())

    def test_changed_target_git_identity_rejects_all_composed_writes(self):
        packages, target = self.prepare()
        content_before = (target / "existing.txt").read_bytes()
        index_before = (target / ".git/index").read_bytes()
        head_before = (target / ".git/HEAD").read_bytes()
        sources = [self.tree(path) for path in packages]
        capture = self.ctx.runner.capture
        mutated = False

        def mutate(argv, cwd, env=None):
            nonlocal mutated
            result = capture(argv, cwd=cwd, env=env)
            if "apply" in argv and "--check" not in argv and "--numstat" not in argv and not mutated:
                self.git(target, "remote", "set-url", "origin", "https://example.invalid/changed.git")
                mutated = True
            return result

        with patch.object(self.ctx.runner, "capture", side_effect=mutate), \
                self.assertRaisesRegex(ValueError, "Git identity changed"):
            apply_source(self.ctx, target)
        self.assertTrue(mutated)
        self.assertEqual((target / "existing.txt").read_bytes(), content_before)
        self.assertEqual((target / ".git/index").read_bytes(), index_before)
        self.assertEqual((target / ".git/HEAD").read_bytes(), head_before)
        self.assertEqual([self.tree(path) for path in packages], sources)
        self.assertEqual(self.git(target, "remote", "get-url", "origin"), "https://example.invalid/changed.git")


if __name__ == "__main__":
    unittest.main()
