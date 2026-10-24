"""Application-owned payload declarations consumed by the package pipeline."""
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any


@dataclass(frozen=True)
class PayloadFile:
    source: Path
    destination: str
    mode: int | None = None


@dataclass(frozen=True)
class LifecycleCommand:
    location: str
    path: str
    args: tuple[str, ...] = ()
    required_env: tuple[str, ...] = ()
    run_as: str = "current"


@dataclass(frozen=True)
class Lifecycle:
    hooks: dict[str, LifecycleCommand] = field(default_factory=dict)


@dataclass(frozen=True)
class UninstallPolicy:
    protected: tuple[str, ...]
    transient: tuple[str, ...]
    title: str
    label: str
    retain_label: str
    delete_label: str
    nas_user_roots: tuple[str, ...] = ()


@dataclass(frozen=True)
class Payload:
    template: Path
    files: list[PayloadFile]
    values: dict[str, str]
    artifact_name: str
    metadata: dict[str, Any] = field(default_factory=dict)
    required: list[str] = field(default_factory=list)
    lifecycle: Lifecycle | None = None
    uninstall: UninstallPolicy | None = None
