import os

from app.domains.licensing.fingerprint import get_or_create_fingerprint


def test_fingerprint_is_stable_across_calls(tmp_path):
    path = os.path.join(tmp_path, "installation.id")

    first = get_or_create_fingerprint(path)
    second = get_or_create_fingerprint(path)

    assert first == second
    assert len(first) == 32


def test_fingerprint_persists_to_disk(tmp_path):
    path = os.path.join(tmp_path, "installation.id")

    fingerprint = get_or_create_fingerprint(path)

    with open(path) as fh:
        assert fh.read().strip() == fingerprint
