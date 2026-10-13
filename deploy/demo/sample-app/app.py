"""A small upload job, the kind of code the demo's agents plan and review."""


def upload(bundle: bytes) -> int:
    """Send the bundle and return how many bytes were accepted."""
    return len(bundle)


if __name__ == "__main__":
    print(upload(b"hello"))
