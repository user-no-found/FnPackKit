"""Reject origin secrets before any runner log, frozen record or diagnostic can expose them."""
import json
import subprocess
import traceback
import unittest
from unittest.mock import patch

from fnpackkit.optional_contracts import identity, validate_origin
from fnpackkit.optional_packages import apply_source
from optional_fixture import OptionalFixture

SECRET = "FAKE_ONLY_ORIGIN_SECRET_73a21"
UNSAFE = (
    f"https://user:{SECRET}@example.invalid/FnDSH.git",
    f"http://{SECRET}@example.invalid/FnDSH.git",
    f"https://example.invalid/FnDSH.git?access_token={SECRET}",
    f"https://example.invalid/FnDSH.git#{SECRET}",
    f"ssh://git:{SECRET}@example.invalid/owner/FnDSH.git",
)


class OptionalOriginTests(OptionalFixture, unittest.TestCase):
    def assert_secret_absent(self, error):
        self.assertNotIn(SECRET, "".join(traceback.format_exception(error)))
        self.assertNotIn(SECRET, json.dumps(self.ctx.metadata))
        self.assertNotIn(SECRET, json.dumps(self.events))
        self.assertNotIn(SECRET, (self.work / "commands.log").read_text())
        snapshot = self.work / "optional-packages.json"
        if snapshot.exists():
            self.assertNotIn(SECRET, snapshot.read_text())
        directory = self.work / "optional-inputs"
        if directory.exists():
            for path in directory.rglob("*"):
                if path.is_file():
                    self.assertNotIn(SECRET.encode(), path.read_bytes())

    def test_main_origin_secrets_are_never_logged_or_frozen(self):
        self.package()
        for origin in UNSAFE:
            with self.subTest(origin_kind=origin.split(":", 1)[0]):
                self.git(self.main, "remote", "set-url", "origin", origin)
                with self.assertRaisesRegex(ValueError, "credential-free") as error:
                    self.choose(["cyberstrike"])
                self.assert_secret_absent(error.exception)
                self.assertNotIn("optional_packages", self.ctx.metadata)
                self.assertFalse((self.work / "optional-inputs").exists())

    def test_optional_repository_origin_secrets_are_never_logged_or_frozen(self):
        package = self.package()
        for origin in UNSAFE:
            self.git(package, "remote", "set-url", "origin", origin)
            with self.assertRaisesRegex(ValueError, "credential-free") as error:
                self.choose(["cyberstrike"])
            self.assert_secret_absent(error.exception)
            self.assertNotIn("optional_packages", self.ctx.metadata)

    def test_target_source_origin_secrets_are_rejected_without_mutating_selection(self):
        self.package()
        record = json.dumps(self.choose(["cyberstrike"]), sort_keys=True)
        target = self.target()
        for origin in UNSAFE:
            self.git(target, "remote", "set-url", "origin", origin)
            with self.assertRaisesRegex(ValueError, "credential-free") as error:
                apply_source(self.ctx, target)
            self.assert_secret_absent(error.exception)
            self.assertEqual(json.dumps(self.ctx.metadata["optional_packages"], sort_keys=True), record)
            self.assertEqual((target / "existing.txt").read_text(), "original\n")

    def test_manifest_source_constraint_cannot_freeze_credentials(self):
        package = self.package()
        for origin in UNSAFE:
            self.manifest(package, source={"origin": origin})
            with self.assertRaisesRegex(ValueError, "credential-free") as error:
                self.choose(["cyberstrike"])
            self.assert_secret_absent(error.exception)
            self.assertFalse((self.work / "optional-inputs").exists())

    def test_failed_confidential_config_read_does_not_echo_git_stderr(self):
        self.package()
        failed = subprocess.CompletedProcess([], 128, stdout=SECRET, stderr="bad config " + SECRET)
        with patch("fnpackkit.optional_contracts.subprocess.run", return_value=failed), \
                self.assertRaisesRegex(ValueError, "cannot read optional Git origin") as error:
            self.choose(["cyberstrike"])
        self.assert_secret_absent(error.exception)

    def test_safe_http_and_ssh_user_names_remain_valid_but_origin_reads_are_unlogged(self):
        safe = ("https://example.invalid/owner/FnDSH.git", "git@example.invalid:owner/FnDSH.git",
                "ssh://git@example.invalid/owner/FnDSH.git", "file:///tmp/FnDSH.git", "/tmp/FnDSH.git")
        for origin in safe:
            self.assertEqual(validate_origin(origin), origin)
            self.git(self.main, "remote", "set-url", "origin", origin)
            result = identity(self.main, runner=self.ctx.runner, allow_uncommitted=True)
            self.assertEqual(result["origin"], origin)
            self.assertNotIn(origin, (self.work / "commands.log").read_text())
        commands = [event["argv"] for event in self.events if event.get("type") == "command"]
        self.assertFalse(any("remote.origin.url" in command for command in commands))

    def test_invalid_authority_and_empty_query_or_fragment_reject_without_raw_echo(self):
        for origin in (f"https://host:{SECRET}/FnDSH.git", f"https://[{SECRET}/FnDSH.git",
                       "https://host/FnDSH.git?", "https://host/FnDSH.git#"):
            with self.assertRaisesRegex(ValueError, "credential-free") as error:
                validate_origin(origin)
            self.assert_secret_absent(error.exception)


if __name__ == "__main__":
    unittest.main()
