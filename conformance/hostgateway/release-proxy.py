"""Materialize the unavailable frontendpayload revision from the pinned host release."""

import json
from pathlib import Path
import subprocess
import sys
from zipfile import ZipFile

host, proxy = map(Path, sys.argv[1:])
module = "github.com/codefly-dev/module-saas-starter/module/frontendpayload"
version = "v0.0.0-20261018173826-0d423cb8bd17"
directory = "module/frontendpayload"


def git(*args):
    return subprocess.check_output(["git", "-C", host, *args])


target = proxy / module / "@v"
target.mkdir(parents=True)
(target / f"{version}.info").write_text(
    json.dumps({"Version": version, "Time": "2026-10-18T17:38:26Z"})
)
(target / f"{version}.mod").write_bytes(git("show", f"HEAD:{directory}/go.mod"))
paths = git("ls-tree", "-r", "--name-only", "-z", "HEAD", directory)
with ZipFile(target / f"{version}.zip", "w") as archive:
    # Go includes the repository license for nested modules without their own.
    for path in [*paths.decode().rstrip("\0").split("\0"), "LICENSE"]:
        name = path.removeprefix(directory + "/")
        archive.writestr(f"{module}@{version}/{name}", git("show", f"HEAD:{path}"))
