"""Static discovery, explicit opt-in, terminal choice and frozen-input contracts."""
import io
import json
import os
import shutil
from pathlib import Path
import unittest
from unittest.mock import patch

from buildflow.contracts import BuildError
from fnpackkit.manifest import validate_project
from fnpackkit.optional_packages import selection, selection_prepare, validate_selection
from optional_fixture import OptionalFixture


class OptionalSelectionTests(OptionalFixture, unittest.TestCase):
    def test_no_declaration_preserves_existing_consumers(self):
        del self.data["fnpackkit"]["optional_packages"]
        self.assertIsNone(selection_prepare(self.project))
        self.assertIsNone(selection(self.ctx))
        self.assertNotIn("optional_packages", self.ctx.metadata)
        with self.assertRaisesRegex(ValueError, "not opted in"):
            selection_prepare(self.project, no_features=True)

    def test_opt_in_schema_rejects_unknown_paths_and_boolean(self):
        for value in (True, False, {"schema": True}, {"schema": 1, "directory": "elsewhere"}):
            with self.subTest(value=value), self.assertRaisesRegex(ValueError, "optional_packages"):
                self.data["fnpackkit"]["optional_packages"] = value
                validate_project(self.project)

    def test_missing_or_empty_directory_does_not_prompt_or_require_git(self):
        for create in (False, True):
            with self.subTest(create=create), patch("builtins.input", side_effect=AssertionError("menu")):
                if create:
                    (self.main / "optional").mkdir()
                record = self.choose()
                self.assertEqual(record["selected"], [])
                self.assertIsNone(record["project"])
                self.assertTrue((self.work / "optional-packages.json").is_file())

    def test_non_tty_defaults_to_all_and_displays_candidates_without_reading_input(self):
        self.package()
        self.package("other")
        with patch("sys.stdin.isatty", return_value=False), patch("sys.stderr", new_callable=io.StringIO) as output, \
                patch("builtins.input", side_effect=AssertionError("must not read input")):
            record = self.choose()
        self.assertEqual([item["id"] for item in record["selected"]], ["cyberstrike", "other"])
        self.assertIn("[x] Cyberstrike (cyberstrike)", output.getvalue())
        self.assertIn("[x] Other (other)", output.getvalue())
        self.assertNotIn("选择可选功能", output.getvalue())

    def test_explicit_missing_ids_fail_and_no_features_disables_all(self):
        self.package()
        with self.assertRaisesRegex(ValueError, "not found"):
            self.choose(["absent"])
        self.assertEqual(self.choose(no_features=True)["selected"], [])

    def test_explicit_and_uncommitted_freeze_matches_metadata_and_json(self):
        path = self.package(files=[{"source": "new.txt", "destination": "new.txt"}], tests=["new.txt"])
        (path / "new.txt").write_text("evidence\n")
        record = self.choose(["cyberstrike"])
        item = record["selected"][0]
        self.assertEqual(item["revision"], "uncommitted")
        self.assertEqual(item["manifest"]["tests"], ["new.txt"])
        self.assertEqual(item["manifest"]["project"], "FnDSH")
        self.assertEqual(json.loads((self.work / "optional-packages.json").read_text()), record)
        self.assertEqual(validate_selection(record), record)
        detached = selection(self.ctx)
        detached["selected"].clear()
        self.assertEqual(len(self.ctx.metadata["optional_packages"]["selected"]), 1)

    def test_tty_multiple_and_default_all_use_shared_interaction(self):
        self.package()
        self.package("other")
        with patch("sys.stdin.isatty", return_value=True), patch("sys.stderr.isatty", return_value=True), \
                patch.object(self.ctx, "interaction", wraps=self.ctx.interaction) as interaction, \
                patch("builtins.input", return_value="1,other"):
            result = self.choose()
        interaction.assert_called_once()
        self.assertEqual([p["id"] for p in result["selected"]], ["cyberstrike", "other"])
        for directory in (self.work / "optional-inputs").iterdir():
            # Separate Context work directories are used for subsequent choices.
            self.assertTrue(directory.is_dir())
        empty_work = self.root / "empty-choice"
        empty_work.mkdir()
        self.ctx.work = empty_work
        with patch("sys.stdin.isatty", return_value=True), patch("sys.stderr.isatty", return_value=True), \
                patch("builtins.input", return_value=""):
            self.assertEqual([item["id"] for item in self.choose()["selected"]], ["cyberstrike", "other"])

    def test_tty_zero_explicitly_disables_every_candidate(self):
        self.package()
        with patch("sys.stdin.isatty", return_value=True), patch("sys.stderr.isatty", return_value=True), \
                patch("builtins.input", return_value="0"):
            self.assertEqual(self.choose()["selected"], [])

    def test_tty_cancel_eof_and_invalid_choices_do_not_freeze(self):
        self.package()
        for answer in ("q", "99", "1,1", ",,,", EOFError()):
            with self.subTest(answer=answer), patch("sys.stdin.isatty", return_value=True), \
                    patch("sys.stderr.isatty", return_value=True), \
                    patch("builtins.input", **({"side_effect": answer} if isinstance(answer, Exception)
                                               else {"return_value": answer})), self.assertRaises(BuildError):
                self.choose()
            self.assertNotIn("optional_packages", self.ctx.metadata)
            self.assertFalse((self.work / "optional-inputs").exists())

    def test_unsafe_manifest_script_duplicate_id_and_name_are_rejected(self):
        path = self.package()
        cases = ({"script": "evil.py"}, {"id": "Wrong_ID"}, {"id": "other"}, {"project": "DSH"},
                 {"files": [{"source": "../secret", "destination": "x"}]},
                 {"patches": ["x", "x"]}, {"files": [{"source": "x", "destination": ".git/config"}]},
                 {"source": {"revision": "main"}})
        original = (path / "fpk-option.json").read_text()
        for fields in cases:
            with self.subTest(fields=fields):
                (path / "fpk-option.json").write_text(original)
                self.manifest(path, **fields)
                with self.assertRaises(ValueError):
                    self.choose(no_features=True)
        (path / "fpk-option.json").write_text('{"schema":1,"schema":1}')
        with self.assertRaisesRegex(ValueError, "duplicate"):
            self.choose(no_features=True)

    def test_origin_not_display_name_and_independent_repository(self):
        path = self.package()
        self.git(path, "remote", "set-url", "origin", "git@example.invalid:owner/DSHOptional-cyberstrike.git")
        with self.assertRaisesRegex(ValueError, "origin basename"):
            self.choose(no_features=True)
        self.git(path, "remote", "set-url", "origin", "git@example.invalid:owner/FnDSHOptional-cyberstrike.git")
        (path / ".git/objects/info/alternates").write_text(str(self.main / ".git/objects") + "\n")
        with self.assertRaisesRegex(ValueError, "sharing"):
            self.choose(no_features=True)

    def test_symlink_optional_input_or_repository_is_rejected(self):
        path = self.package(files=[{"source": "input", "destination": "target"}])
        (self.root / "outside").write_text("secret")
        (path / "input").symlink_to(self.root / "outside")
        with self.assertRaisesRegex(ValueError, "symlink"):
            self.choose(no_features=True)
        (path / "input").unlink()
        (path / "input").write_text("safe")
        (self.main / "optional/linked").symlink_to(path, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, "symlink"):
            self.choose(no_features=True)

    def test_one_level_only_no_import_or_arbitrary_nested_scan(self):
        nested = self.main / "optional/container/nested"
        nested.mkdir(parents=True)
        (nested / "fpk-option.json").write_text("broken")
        (nested / "evil.py").write_text("raise RuntimeError('imported')")
        self.assertEqual(self.choose()["selected"], [])

    def test_original_manifest_input_snapshot_and_git_changes_detected(self):
        path = self.package(committed=True, files=[{"source": "input", "destination": "output"}])
        (path / "input").write_text("frozen")
        record = self.choose(["cyberstrike"])
        tests = [(path / "input", b"mutated"), (path / "fpk-option.json", b"{}"),
                 (Path(record["selected"][0]["inputs"][0]["snapshot"]), b"tampered")]
        for filename, replacement in tests:
            original = filename.read_bytes()
            filename.write_bytes(replacement)
            with self.subTest(filename=filename), self.assertRaisesRegex(ValueError, "changed"):
                validate_selection(record)
            filename.write_bytes(original)
        self.git(path, "remote", "set-url", "origin", "other")
        with self.assertRaisesRegex(ValueError, "identity changed"):
            validate_selection(record)

    def test_duplicate_id_and_normalized_path_aliases_are_diagnostic(self):
        self.package()
        other = self.package("zzz")
        self.manifest(other, id="cyberstrike")
        with self.assertRaisesRegex(ValueError, "duplicate optional feature id"):
            self.choose(no_features=True)
        self.manifest(other, id="zzz")
        for name in ("a//b", "a/./b", "a/", "C:/outside", ".GIT/config"):
            self.manifest(other, files=[{"source": name, "destination": "output"}])
            with self.subTest(name=name), self.assertRaisesRegex(ValueError, "unsafe optional path"):
                self.choose(no_features=True)

    def test_hardlinked_input_and_snapshot_symlink_are_rejected(self):
        package = self.package(files=[{"source": "input", "destination": "output"}])
        (package / "input").write_text("evidence")
        os.link(package / "input", package / "alias")
        with self.assertRaisesRegex(ValueError, "unlinked regular"):
            self.choose(no_features=True)
        (package / "alias").unlink()
        record = self.choose(["cyberstrike"])
        snapshot = Path(record["selected"][0]["inputs"][0]["snapshot"])
        snapshot.unlink()
        snapshot.symlink_to(package / "input")
        with self.assertRaisesRegex(ValueError, "symlink"):
            validate_selection(record)

    def test_snapshot_parent_symlink_is_rejected_before_any_copy(self):
        self.package()
        external = self.root / "external"
        external.mkdir()
        (self.work / "optional-inputs").symlink_to(external, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, "symlink"):
            self.choose(["cyberstrike"])
        self.assertEqual(list(external.iterdir()), [])

    def test_feature_argument_types_and_mutual_exclusion(self):
        for values in ({"features": "cyberstrike"}, {"features": ["x", "x"]},
                       {"features": ["../x"]}, {"features": [], "no_features": True}):
            with self.subTest(values=values), self.assertRaises(ValueError):
                selection_prepare(self.project, **values)


if __name__ == "__main__":
    unittest.main()

    def test_each_optional_source_validation_reports_a_named_activity(self):
        self.package("cyberstrike")
        self.package("clean-session")
        self.choose(features=[])
        starts = [event for event in self.events if event.get("type") == "activity_start"]
        self.assertEqual({event["target"] for event in starts},
                         {"FnDSHOptional-cyberstrike", "FnDSHOptional-clean-session"})
        self.assertTrue(all(event["name"] == "校验可选功能源" for event in starts))
        ends = [event for event in self.events if event.get("type") == "activity_end"]
        self.assertEqual(len(ends), 2)
        self.assertTrue(all(event["outcome"] == "completed" for event in ends))
        git = [event for event in self.events
               if event.get("type") == "command" and event.get("label") == "git"]
        self.assertTrue(git)
        self.assertTrue(all(event["activity_id"] for event in git))

    def test_invalid_optional_source_marks_activity_interrupted(self):
        path = self.package("broken")
        shutil.rmtree(path / ".git")
        with self.assertRaisesRegex(ValueError, "independent Git directory"):
            self.choose(features=[])
        ends = [event for event in self.events if event.get("type") == "activity_end"]
        self.assertEqual([event["target"] for event in ends], ["FnDSHOptional-broken"])
        self.assertEqual(ends[0]["outcome"], "interrupted")

    def test_no_optional_sources_open_no_activity(self):
        self.choose(features=[])
        self.assertFalse([event for event in self.events if event.get("type") == "activity_start"])
