"""Apply the fixed publisher identity required for packages built by this toolkit."""
from .formats import manifest_fields


ACCOUNT = "user-no-found"
PROFILE_URL = "https://github.com/" + ACCOUNT


def apply_profile(manifest_path):
    """Write the shared package identity without changing upstream license metadata."""
    fields = manifest_fields(manifest_path.read_text(encoding="utf-8"))
    fields.update(maintainer=ACCOUNT, maintainer_url=PROFILE_URL,
                  distributor=ACCOUNT, distributor_url=PROFILE_URL)
    manifest_path.write_text("".join(f"{key}={value}\n" for key, value in fields.items()), encoding="utf-8")
    return fields
