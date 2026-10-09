"""Parse the strict line-based fnOS package manifest format."""

def manifest_fields(text):
    fields = {}
    for line in text.splitlines():
        if line.strip() and not line.lstrip().startswith("#"):
            key, separator, value = line.partition("=")
            key = key.strip()
            if not separator or not key or key in fields:
                raise ValueError("invalid or duplicate manifest field")
            fields[key] = value.strip()
    return fields
