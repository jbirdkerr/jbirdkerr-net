"""Issue an API key out-of-band.

Prints the raw key once and the SQL to register its hash. The raw key is never stored.

    uv run python scripts/issue_key.py "my laptop"
"""
from __future__ import annotations

import argparse
import secrets
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from shortener.auth import hash_key  # noqa: E402


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("label", help="Human-readable label for the key (stored in api_keys.label)")
    args = parser.parse_args()

    raw_key = "sk_" + secrets.token_urlsafe(32)
    label = args.label.replace("'", "''")

    print("Raw key (shown once, store it somewhere safe):\n")
    print(f"  {raw_key}\n")
    print("Run this against Supabase to register it (only the hash is stored):\n")
    print(
        "  INSERT INTO shortener.api_keys (key_hash, label)\n"
        f"  VALUES ('{hash_key(raw_key)}', '{label}')\n"
        "  RETURNING owner_id;"
    )


if __name__ == "__main__":
    main()
