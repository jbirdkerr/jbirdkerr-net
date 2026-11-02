# Pants targets for apps/shortener.
#
# Requires `apps/shortener/src` as a Pants source root (it is listed in [source].root_patterns in
# pants.toml). Imports of `workers` and `pyodide` only exist inside the Cloudflare runtime, so the
# files that use them carry `# pants: no-infer-dep` on those lines.

python_requirements(
    name="reqs",
    source="pyproject.toml",
)

python_sources(
    name="src",
    sources=["src/**/*.py", "scripts/*.py"],
)

python_test_utils(
    name="test_utils",
    sources=["tests/conftest.py"],
)

python_tests(
    name="tests",
    sources=["tests/test_*.py"],
    dependencies=[
        "db/supabase:files",  # tests scan the shortener migrations
        # starlette.testclient imports httpx2 lazily, so Pants cannot infer it from an import.
        ":reqs#httpx2",
    ],
)

files(
    name="worker_files",
    sources=["src/**/*.py", "pyproject.toml", "wrangler.toml"],
)

archive(
    name="dist",
    format="tar.gz",
    files=[":worker_files"],
    output_path="apps/shortener.tar.gz",
)
