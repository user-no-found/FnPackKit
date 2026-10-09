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
class Payload:
    template: Path
    files: list[PayloadFile]
    values: dict[str, str]
    artifact_name: str
    metadata: dict[str, Any] = field(default_factory=dict)
    required: list[str] = field(default_factory=list)
