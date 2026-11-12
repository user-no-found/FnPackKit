"""Public API ordering and CLI parameter forwarding without invoking fnpack."""
import contextlib
import io
import json
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from buildflow.contracts import Artifact, BuildError, Step
from fnpackkit.api import build
from fnpackkit.cli import main
from fnpackkit.contracts import Payload
from fnpackkit.optional_packages import selection
from optional_fixture import OptionalFixture


class OptionalEntryTests(OptionalFixture, unittest.TestCase):
    def project_file(self, *, enabled=True):
        if not enabled:
            del self.data["fnpackkit"]["optional_packages"]
        self.project.path.write_text(json.dumps(self.data))

    def invoke(self, *, enabled=True, mutate=None, **kwargs):
        self.project_file(enabled=enabled)
        observed = []

        def produce(ctx):
            observed.append(selection(ctx))
            if mutate is not None:
                mutate(ctx)

        def package(ctx, payload, fnpack):
            artifact = ctx.work / "fixture.txt"
            artifact.write_text("not an FPK")
            return [Artifact(artifact, "fixture.txt")]

        payload = Payload(self.main, [], {"APP_ID": "dsh", "VERSION": "1.2.3", "PLATFORM": "x86"}, "fixture.fpk")
        with patch("fnpackkit.api.installed_version", return_value=None), \
                patch("fnpackkit.api.package", side_effect=package), contextlib.redirect_stderr(io.StringIO()):
            result = build(self.project.path, steps=[Step("produce", produce)], payload=payload,
                           build_version="1.2.3", ui=self.events.append, **kwargs)
        return result, observed

    def test_opt_in_prepares_selection_before_production_without_an_extra_step(self):
        self.package()
        result, observed = self.invoke(features=["cyberstrike"])
        self.assertEqual(observed[0]["selected"][0]["id"], "cyberstrike")
        self.assertIn("optional_packages", result.metadata)
        starts = [event for event in self.events if event["type"] == "step_start"]
        self.assertEqual(starts[0]["name"], "produce")
        self.assertNotIn("选择并冻结可选构建包", [event["name"] for event in starts])
        self.assertEqual(self.events[0]["type"], "build_start")
        self.assertEqual([event for event in self.events if event["type"] == "command"], [])

    def test_no_declaration_uses_original_pipeline_without_git_scan(self):
        result, observed = self.invoke(enabled=False)
        self.assertEqual(observed, [None])
        self.assertNotIn("optional_packages", result.metadata)
        self.assertEqual([event for event in self.events if event["type"] == "step_start"][0]["name"], "produce")

    def test_non_tty_defaults_to_all_before_production_without_input(self):
        self.package()
        self.package("other")
        with patch("sys.stdin.isatty", return_value=False), \
                patch("builtins.input", side_effect=AssertionError("must not read input")):
            result, observed = self.invoke()
        self.assertEqual([item["id"] for item in observed[0]["selected"]], ["cyberstrike", "other"])
        self.assertEqual(result.metadata["optional_packages"], observed[0])
        self.assertTrue((self.main / "output/fixture.txt").is_file())

    def test_explicit_no_features_still_runs_original_production(self):
        self.package()
        result, observed = self.invoke(no_features=True)
        self.assertEqual(observed[0]["selected"], [])
        self.assertEqual(result.metadata["optional_packages"]["selected"], [])

    def test_selected_input_mutation_during_production_blocks_packaging(self):
        package = self.package(tests=["test.txt"])
        (package / "test.txt").write_text("frozen")
        with self.assertRaisesRegex(BuildError, "input changed"):
            self.invoke(features=["cyberstrike"], mutate=lambda ctx: (package / "test.txt").write_text("mutated"))
        self.assertFalse((self.main / "output/fixture.txt").exists())
        self.assertFalse((self.main / ".build/last-success.json").exists())

    def test_selected_input_mutation_after_verify_blocks_publication(self):
        package = self.package(tests=["test.txt"])
        (package / "test.txt").write_text("frozen")

        def verify(ctx, artifacts):
            (package / "test.txt").write_text("mutated")

        with self.assertRaisesRegex(BuildError, "input changed"):
            self.invoke(features=["cyberstrike"], verify=verify)
        self.assertFalse((self.main / "output/fixture.txt").exists())
        self.assertFalse((self.main / ".build/last-success.json").exists())

    def test_cli_features_no_features_and_original_defaults_forward(self):
        self.project_file()
        for arguments, features, no_features in (
                (["--features", "cyberstrike,other", "--features", "third"], ["cyberstrike", "other", "third"], False),
                (["--no-features"], None, True), ([], None, False)):
            with self.subTest(arguments=arguments), patch("fnpackkit.cli.select_project", return_value=self.project), \
                    patch("fnpackkit.cli.build", return_value=SimpleNamespace(output=self.root / "out")) as call, \
                    contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(main(["--project", str(self.project.path), *arguments]), 0)
                self.assertEqual(call.call_args.kwargs["features"], features)
                self.assertEqual(call.call_args.kwargs["no_features"], no_features)

    def test_cli_mutual_exclusion_and_cancel_exit_code(self):
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as error:
            main(["--features", "cyberstrike", "--no-features"])
        self.assertEqual(error.exception.code, 2)
        with patch("fnpackkit.cli.select_project", return_value=self.project), \
                patch("fnpackkit.cli.build", side_effect=BuildError("选择已取消", 130)), \
                contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(main(["--project", str(self.project.path)]), 130)


if __name__ == "__main__":
    unittest.main()
