"""Cloudflare Python Worker entrypoint."""
from workers import asgi  # pants: no-infer-dep

from shortener.app import app

Default = asgi.entrypoint(app)
