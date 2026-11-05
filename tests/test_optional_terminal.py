"""Exercise the real optional preparation UI through an isolated native PTY."""
import errno
import json
import os
from pathlib import Path
import re
import select
import shutil
import struct
import subprocess
import sys
import tempfile
import time
import unittest

import buildflow
import fnpackkit

pty = None
if os.name == "posix":
    try:
        import fcntl
        import pty
        import termios
    except ImportError:
        pty = None


MENU = "可选功能"
MENU_INPUT = "选择可选功能 [全部]（编号/id用逗号分隔；0不安装，q取消）: "
BANNER = "BuildFlow · 原生构建"
FEATURES = ["alpha", "beta"]
ANSI = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")
CHILD = r'''
import json
from pathlib import Path
import sys
from unittest.mock import patch
from buildflow.contracts import Artifact, BuildError, Step
from fnpackkit.api import build
from fnpackkit.contracts import Payload
from fnpackkit.optional_packages import selection

project = Path(sys.argv[1])
options = json.loads(sys.argv[2])

def record(name, value):
    with (project.parent / (name + ".jsonl")).open("a") as stream:
        stream.write(json.dumps(value) + "\n")

def produce(ctx):
    chosen = selection(ctx)
    record("produce", {"ids": [item["id"] for item in chosen["selected"]],
                       "version": ctx.project.data["version"]})
    ctx.notice("FIXTURE_PRODUCE")

def payload(ctx):
    return Payload(ctx.project.root, [],
                   {"APP_ID": ctx.project.data["app_id"],
                    "VERSION": ctx.project.data["version"], "PLATFORM": "x86"},
                   "fixture.fpk")

def package(ctx, declaration, fnpack):
    record("package", {"version": declaration.values["VERSION"]})
    output = ctx.work / "fixture.txt"
    output.write_text("not an FPK\n")
    return [Artifact(output, "fixture.txt")]

try:
    with patch("fnpackkit.api.installed_version", return_value=None), \
            patch("fnpackkit.api.package", side_effect=package):
        build(project, steps=[Step("produce", produce)], payload=payload, **options)
except BuildError as error:
    if not error.reported:
        print(str(error), file=sys.stderr, flush=True)
    sys.exit(error.exit_code)
'''


class TerminalProcess:
    """Own one subprocess and PTY, reading only on readiness until a deadline."""

    def __init__(self, command, *, cwd, env, separate_stdout=False):
        self.master, slave = pty.openpty()
        self.output = bytearray()
        self.eof = False
        try:
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 180, 0, 0))
            self.process = subprocess.Popen(command, cwd=cwd, env=env, stdin=slave,
                                            stdout=subprocess.PIPE if separate_stdout else slave,
                                            stderr=slave)
        except BaseException:
            os.close(self.master)
            raise
        finally:
            os.close(slave)

    @property
    def text(self):
        return ANSI.sub("", self.output.decode("utf-8", errors="replace")).replace("\r", "")

    def __enter__(self):
        return self

    def __exit__(self, *exception):
        try:
            if self.process.poll() is None:
                self.process.terminate()
                try:
                    self.process.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    self.process.kill()
                    self.process.wait(timeout=3)
        finally:
            if self.process.stdout is not None:
                self.process.stdout.close()
            os.close(self.master)

    def _read(self, deadline):
        remaining = deadline - time.monotonic()
        if remaining <= 0 or not select.select([self.master], [], [], remaining)[0]:
            raise AssertionError("terminal output timed out:\n" + self.text)
        try:
            chunk = os.read(self.master, 65536)
        except OSError as error:
            if error.errno != errno.EIO:
                raise
            chunk = b""
        self.eof = not chunk
        self.output.extend(chunk)

    def expect(self, token, *, after=0):
        deadline = time.monotonic() + 20
        while token not in self.text[after:]:
            if self.eof:
                raise AssertionError(f"terminal closed before {token!r}:\n{self.text}")
            self._read(deadline)
        return self.text

    def send(self, value):
        os.write(self.master, value)

    def finish(self):
        deadline = time.monotonic() + 20
        while not self.eof:
            self._read(deadline)
        return self.process.wait(timeout=max(.1, deadline - time.monotonic()))


@unittest.skipUnless(pty is not None, "real PTY interaction requires POSIX")
class OptionalTerminalTests(unittest.TestCase):
    def setUp(self):
        self.git = shutil.which("git")
        if self.git is None:
            self.skipTest("local Git is required for optional package fixtures")
        self.temporary = tempfile.TemporaryDirectory(prefix="fnpackkit-terminal-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        inherited = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        self.env = {**inherited, "TERM": "xterm-256color", "NO_COLOR": "1",
                    "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull,
                    "GIT_TERMINAL_PROMPT": "0", "PYTHONIOENCODING": "utf-8",
                    "PYTHONDONTWRITEBYTECODE": "1",
                    "PYTHONPATH": os.pathsep.join(str(Path(module.__file__).resolve().parent.parent)
                                                  for module in (fnpackkit, buildflow))}

    def fixture(self, name, *, candidates=True, directory="missing"):
        root = self.root / name
        root.mkdir()
        self.git_run(root, "init", "-q")
        self.git_run(root, "remote", "add", "origin", "git@github.com:fixture/TerminalApp.git")
        data = {"app_id": "terminal-fixture", "display_name": "Terminal fixture", "version": "1.2.3",
                "buildflow": {"schema": 1, "targets": ["fpk"], "steps": []},
                "fnpackkit": {"schema": 1, "requires": ">=0.1,<0.2", "payload": "payload.json",
                              "optional_packages": {"schema": 1}}}
        (root / "project.json").write_text(json.dumps(data))
        if candidates:
            for feature, label in (("alpha", "Alpha tools"), ("beta", "Beta extras")):
                path = root / "optional" / ("TerminalAppOptional-" + feature)
                path.mkdir(parents=True)
                self.git_run(path, "init", "-q")
                self.git_run(path, "remote", "add", "origin",
                             "git@github.com:fixture/" + path.name + ".git")
                (path / "fpk-option.json").write_text(json.dumps(
                    {"schema": 1, "id": feature, "label": label, "project": "TerminalApp",
                     "patches": [], "files": []}))
        elif directory != "missing":
            (root / "optional").mkdir()
            if directory == "no-manifest":
                (root / "optional" / "unrelated").mkdir()
        return root

    def git_run(self, root, *arguments):
        subprocess.run([self.git, *arguments], cwd=root, check=True,
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=self.env)

    def command(self, root, **options):
        return [sys.executable, "-B", "-u", "-c", CHILD, str(root / "project.json"),
                json.dumps(options)]

    def terminal(self, root, *, separate_stdout=False, **options):
        return TerminalProcess(self.command(root, **options), cwd=root, env=self.env,
                               separate_stdout=separate_stdout)

    def versions(self, terminal):
        before_type = terminal.expect("构建类型 [")
        self.assertNotIn(MENU, before_type)
        self.assertNotIn(BANNER, before_type)
        terminal.send(b"\n")
        before_version = terminal.expect("构建版本 [")
        self.assertNotIn(MENU, before_version)
        self.assertNotIn("Alpha tools", before_version)
        self.assertNotIn(BANNER, before_version)
        terminal.send(b"\n")

    def options(self, terminal, root):
        self.versions(terminal)
        before_answer = terminal.expect(MENU_INPUT)
        for text in ("1. [x] Alpha tools (alpha)", "2. [x] Beta extras (beta)"):
            self.assertIn(text, before_answer)
        self.assertNotIn(BANNER, before_answer)
        self.assertNotIn("FIXTURE_PRODUCE", before_answer)
        self.assertNotIn("git", before_answer.casefold())
        events = self.events(root)
        self.assertIn("interaction_start", [event["type"] for event in events])
        self.assertFalse(any(event["type"] in {"build_start", "step_start", "command"}
                             for event in events))
        return before_answer

    @staticmethod
    def events(root):
        paths = list((root / ".build").glob("*/events.jsonl"))
        return [json.loads(line) for path in paths for line in path.read_text().splitlines()]

    @staticmethod
    def calls(root, name):
        path = root / (name + ".jsonl")
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def success(self, root, expected, text, *, menu=False):
        produced = self.calls(root, "produce")
        self.assertEqual(len(produced), 1)
        self.assertEqual(produced[0]["ids"], expected)
        self.assertEqual(len(self.calls(root, "package")), 1)
        self.assertEqual((root / "output/fixture.txt").read_text(), "not an FPK\n")
        self.assertTrue((root / ".build/last-success.json").is_file())
        report_path, = (root / ".build").glob("*/report.json")
        report = json.loads(report_path.read_text())
        self.assertEqual([item["id"] for item in report["metadata"]["optional_packages"]["selected"]], expected)
        self.assertEqual(produced[0]["version"], report["version"])
        events = self.events(root)
        starts = [event["name"] for event in events if event["type"] == "step_start"]
        self.assertEqual(starts, ["produce", "FPK 渲染、封包与实际归档验证"])
        self.assertEqual(sum(event["type"] == "activity_start" and event.get("name") == "发布产物"
                             for event in events), 1)
        self.assertLess(text.index("构建版本") if "构建版本" in text else 0, text.index(BANNER))
        self.assertLess(text.index(BANNER), text.index("[1/2] produce"))
        self.assertLess(text.index("[1/2] produce"), text.index("FIXTURE_PRODUCE"))
        if menu:
            self.assertLess(text.index("构建版本"), text.index(MENU_INPUT))
            self.assertLess(text.index(MENU_INPUT), text.index(BANNER))
            kinds = [event["type"] for event in events]
            self.assertLess(kinds.index("interaction_end"), kinds.index("build_start"))
            notices = [event.get("message", "") for event in events if event["type"] == "notice"]
            self.assertFalse(any("Alpha tools" in message or "Beta extras" in message for message in notices))
        else:
            self.assertNotIn(MENU_INPUT, text)

    def test_enter_selects_every_visible_default_after_both_version_answers(self):
        root = self.fixture("enter")
        with self.terminal(root) as terminal:
            self.options(terminal, root)
            terminal.send(b"\n")
            self.assertEqual(terminal.finish(), 0, terminal.text)
            self.success(root, FEATURES, terminal.text, menu=True)

    def test_menu_and_input_prompt_use_the_stderr_terminal_not_stdout(self):
        root = self.fixture("stderr")
        with self.terminal(root, separate_stdout=True) as terminal:
            self.options(terminal, root)
            terminal.send(b"\n")
            self.assertEqual(terminal.finish(), 0, terminal.text)
            self.success(root, FEATURES, terminal.text, menu=True)
            self.assertEqual(terminal.process.stdout.read(), b"")

    def test_number_and_id_select_subsets(self):
        for name, answer, expected in (("number", b"1\n", ["alpha"]), ("id", b"beta\n", ["beta"])):
            with self.subTest(answer=answer):
                root = self.fixture(name)
                with self.terminal(root) as terminal:
                    self.options(terminal, root)
                    terminal.send(answer)
                    self.assertEqual(terminal.finish(), 0, terminal.text)
                    self.success(root, expected, terminal.text, menu=True)

    def test_zero_and_none_install_nothing(self):
        for name, answer in (("zero", b"0\n"), ("none", b"none\n")):
            with self.subTest(answer=answer):
                root = self.fixture(name)
                with self.terminal(root) as terminal:
                    self.options(terminal, root)
                    terminal.send(answer)
                    self.assertEqual(terminal.finish(), 0, terminal.text)
                    self.success(root, [], terminal.text, menu=True)

    def test_quit_and_terminal_eof_cancel_without_production_or_publication(self):
        for name, answer in (("quit", b"q\n"), ("eof", b"\x04")):
            with self.subTest(answer=answer):
                root = self.fixture(name)
                with self.terminal(root) as terminal:
                    self.options(terminal, root)
                    terminal.send(answer)
                    self.assertEqual(terminal.finish(), 130, terminal.text)
                    self.assertIn("取消", terminal.text)
                    self.assertNotIn(BANNER, terminal.text)
                self.assertEqual(self.calls(root, "produce"), [])
                self.assertEqual(self.calls(root, "package"), [])
                self.assertEqual(list((root / "output").iterdir()), [])
                self.assertFalse((root / ".build/last-success.json").exists())
                self.assertFalse(any(event["type"] == "build_start" or event.get("name") == "发布产物"
                                     for event in self.events(root)))

    def test_absent_empty_and_manifestless_directories_have_no_prompt(self):
        for directory in ("missing", "empty", "no-manifest"):
            with self.subTest(directory=directory):
                root = self.fixture(directory, candidates=False, directory=directory)
                with self.terminal(root) as terminal:
                    self.versions(terminal)
                    self.assertEqual(terminal.finish(), 0, terminal.text)
                    self.success(root, [], terminal.text)
                    self.assertNotIn(MENU, terminal.text)

    def test_explicit_features_and_no_features_never_ask(self):
        for name, options, expected in (("flags-subset", {"features": ["beta"]}, ["beta"]),
                                         ("flags-none", {"no_features": True}, [])):
            with self.subTest(options=options):
                root = self.fixture(name)
                with self.terminal(root, **options) as terminal:
                    self.versions(terminal)
                    self.assertEqual(terminal.finish(), 0, terminal.text)
                    self.success(root, expected, terminal.text)

    def test_non_tty_defaults_to_all_without_reading_stdin(self):
        root = self.fixture("non-tty")
        result = subprocess.run(self.command(root, build_version="1.2.3"), cwd=root, env=self.env,
                                stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                text=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stdout)
        self.success(root, FEATURES, result.stdout)


if __name__ == "__main__":
    unittest.main()
