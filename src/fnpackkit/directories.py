"""Create package-tree directories with explicit, local permission modes."""
from pathlib import Path


def ensure_directory(root, path):
    """Create traversable parents without widening existing private directories."""
    root, path = Path(root), Path(path)
    relative = path.relative_to(root)
    if ".." in relative.parts or root.is_symlink() or not root.is_dir():
        raise ValueError("directory must stay within a regular tree root")
    current = root
    for part in relative.parts:
        current /= part
        try:
            current.mkdir(mode=0o755)
        except FileExistsError:
            if current.is_symlink() or not current.is_dir():
                raise ValueError("directory path contains a link or non-directory")
        else:
            current.chmod(0o755)
    return path
