#!/usr/bin/env python3
"""Portable bounded CI tests. Successful whole batches are the only checkpoints."""

import argparse
import fcntl
import hashlib
import hmac
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[2]
PATTERNS = ["./cmd/...", "./internal/...", "./pkg/...", "./test/..."]
BATCH_SIZE = 12
SPEC = importlib.util.spec_from_file_location("results", Path(__file__).with_name("check-results.py"))
RESULTS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RESULTS)


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def identity(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def save(path, value):
    temp = path.with_suffix(path.suffix + ".tmp")
    with temp.open("w", encoding="utf-8") as stream:
        json.dump(value, stream, indent=2, sort_keys=True)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    temp.replace(path)


def source_inventory(root):
    inventory = {}
    for path in sorted(root.rglob("*")):
        relative = path.relative_to(root)
        if (".git" in relative.parts or "__pycache__" in relative.parts
                or relative.parts[0] == "tmp" or relative.as_posix().startswith("test/uploads/")):
            continue
        if path.is_symlink():
            raise ValueError("Source symlinks are not supported: " + str(relative))
        if path.is_file():
            inventory[relative.as_posix()] = {"sha256": sha(path), "executable": bool(path.stat().st_mode & 0o111)}
    return inventory


def pairs(mapping):
    return {(package, test) for package, names in mapping.items() for test in names}


def validate_manifest(manifest, discovered):
    if manifest.get("schema") != 1:
        raise ValueError("Unsupported coverage manifest schema")
    native = pairs(manifest["native"])
    native_roots = {(p, t) for p, t in native if "/" not in t}
    nested = pairs(manifest["native_nested_selectors"])
    supplemental_roots = {(p, t.split("/", 1)[0]) for p, t in nested}
    conventional = {(p, t) for p, names in discovered.items() for t in names
                    if re.search(manifest["native_name_pattern"], t)}
    if native_roots != conventional | supplemental_roots:
        raise ValueError("Native root discovery differs from coverage-manifest.json; review and update the manifest")
    for label in ("native", "native_nested_selectors", "isolation", "core_excluded", "standalone"):
        for package, names in manifest[label].items():
            if not names or len(names) != len(set(names)):
                raise ValueError("Empty/duplicate manifest entries: " + label)
            for test in names:
                root = test.split("/", 1)[0]
                if root not in discovered.get(package, []):
                    raise ValueError(f"Undiscovered manifest case: {package} {test}")
                if label == "native":
                    # A single t.Run name may contain slashes; intermediate
                    # slash prefixes need not be separately emitted Go cases.
                    if (package, root) not in native:
                        raise ValueError("Manifest omits a native root: " + test)
    if not pairs(manifest["core_excluded"]) <= native:
        raise ValueError("Every core exclusion requires an exact native case")
    if not nested <= native or any("/" not in test for _, test in nested):
        raise ValueError("Invalid nested native selector")
    for package, root in supplemental_roots:
        selectors = [s for p, s in nested if p == package and s.split("/", 1)[0] == root]
        for p, case in native:
            if p == package and case.startswith(root + "/") and not any(case == s or case.startswith(s + "/") for s in selectors):
                raise ValueError("Native manifest case outside its nested selector: " + case)
    return native


def partition(names, standalone=()):
    batch = []
    for name in names:
        if name in standalone:
            if batch:
                yield batch
                batch = []
            yield [name]
        else:
            batch.append(name)
            if len(batch) == BATCH_SIZE:
                yield batch
                batch = []
    if batch:
        yield batch
    if not names:
        yield []  # Compilation and a package terminal still required.


def pattern(names):
    return "^(" + "|".join(re.escape(name) for name in names) + ")$" if names else "^$"


def nested_pattern(path):
    return "/".join("^" + re.escape(part) + "$" for part in path.split("/"))


def stream_command(command, root, log, stderr, env=None):
    """Unbuffered disk writes retain partial evidence; stderr cannot corrupt JSON."""
    with log.open("xb", buffering=0) as output, stderr.open("xb", buffering=0) as errors:
        process = subprocess.Popen(command, cwd=root, env=env, stdout=subprocess.PIPE,
                                   stderr=errors, start_new_session=True)
        try:
            for line in process.stdout:
                output.write(line)
            code = process.wait()
        except BaseException:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
            raise
        finally:
            process.stdout.close()
            os.fsync(output.fileno())
            os.fsync(errors.fileno())
    return code


class Store:
    def __init__(self, root, directory, fingerprint, source, resume=False):
        self.root, self.directory = root, directory
        self.fingerprint, self.source, self.resume = fingerprint, source, resume
        self.reused = 0

    def unchanged(self):
        if source_inventory(self.root) != self.source:
            raise ValueError("Source changed during verification; start fresh")

    def run(self, key, command, expected, required=None, allowed_skips=(), exact=False, env=None):
        self.unchanged()
        folder = self.directory / key
        folder.mkdir(exist_ok=True)
        checkpoint = folder / "result.json"
        contract = {"fingerprint": self.fingerprint, "command": command, "expected": expected,
                    "required": sorted(map(list, required or ())), "allowed_skips": sorted(map(list, allowed_skips)),
                    "exact": exact}
        if checkpoint.exists():
            if not self.resume:
                raise ValueError("Existing checkpoint without explicit trusted resume")
            result = json.loads(checkpoint.read_text())
            if result.get("contract") != contract or result.get("exit_code") != 0 or result.get("validated") is not True:
                raise ValueError("Failed or mismatched checkpoint; inspect evidence and start fresh")
            name = result.get("log", "")
            if not re.fullmatch(r"attempt-\d{3,}\.jsonl", name):
                raise ValueError("Invalid checkpoint log path")
            log = folder / name
            if log.is_symlink() or sha(log) != result.get("log_sha256"):
                raise ValueError("Changed checkpoint log")
            verification = RESULTS.validate(RESULTS.events(log), expected, required, allowed_skips, exact)
            if verification != result.get("verification"):
                raise ValueError("Changed checkpoint verification")
            self.reused += 1
            print("REUSED validated complete batch " + key, flush=True)
            return verification
        attempt = len(list(folder.glob("attempt-*.start.json"))) + 1
        prefix = f"attempt-{attempt:03d}"
        log, stderr = folder / (prefix + ".jsonl"), folder / (prefix + ".stderr")
        save(folder / (prefix + ".start.json"), {"contract": contract, "log": log.name})
        print("START " + key, flush=True)
        code = stream_command(command, self.root, log, stderr, env)
        result = {"contract": contract, "exit_code": code, "log": log.name,
                  "log_sha256": sha(log), "stderr_sha256": sha(stderr), "validated": False}
        try:
            self.unchanged()
            if code:
                raise ValueError(f"Command exited {code}; inspect {log} and {stderr}")
            result["verification"] = RESULTS.validate(RESULTS.events(log), expected, required, allowed_skips, exact)
            result["validated"] = True
        except (ValueError, OSError, KeyError) as error:
            result["error"] = str(error)
            save(checkpoint, result)
            raise
        save(checkpoint, result)
        print("PASS " + key, flush=True)
        return result["verification"]


def discover(root, directory, flags, suffix="", selection=".", race=False):
    build_flags = ["-race"] if race else []
    package_result = subprocess.run(["go", "list", "-buildvcs=false"] + build_flags + PATTERNS,
                                    cwd=root, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if package_result.returncode:
        raise ValueError("Package discovery failed (check the Go environment)")
    packages = package_result.stdout.splitlines()
    if not packages or len(packages) != len(set(packages)):
        raise ValueError("Empty or duplicate package inventory")
    log = directory / ("discovery" + suffix + ".jsonl")
    error = directory / ("discovery" + suffix + ".stderr")
    code = stream_command(["go", "test", "-json"] + build_flags + flags + PATTERNS + ["-list", selection], root, log, error)
    if code:
        raise ValueError("Test discovery failed; inspect " + str(error))
    return RESULTS.inventory(RESULTS.events(log), packages)


def check_local_replacements(module):
    if any(not replacement["New"].get("Version") for replacement in (module.get("Replace") or [])):
        raise ValueError("Local module replacements are outside this repository-only checkpoint contract")


def environment_identity(directory, environment):
    salt_file = directory / "environment-salt"
    if not salt_file.exists():
        with salt_file.open("xb") as stream:
            os.chmod(salt_file, 0o600)
            stream.write(os.urandom(32))
    return hmac.new(salt_file.read_bytes(), json.dumps(dict(environment), sort_keys=True).encode(), hashlib.sha256).hexdigest()


def run(mode, directory, flags, resume=False):
    save(directory / "coverage.json", {"complete": False, "mode": mode})
    source = source_inventory(ROOT)
    # Discovery always runs again. Old listing files never authorize resumption.
    attempt = len(list(directory.glob("discovery-*.jsonl"))) + 1
    inventories = {"race": discover(ROOT, directory, flags, f"-race-{attempt:03d}", race=True)}
    if mode == "core":
        inventories["ordinary"] = discover(ROOT, directory, flags, f"-ordinary-{attempt:03d}")
    discovered = {}
    for inventory in inventories.values():
        for package, names in inventory.items():
            discovered[package] = list(dict.fromkeys(discovered.get(package, []) + names))
    manifest = json.loads(Path(__file__).with_name("coverage-manifest.json").read_text())
    native = validate_manifest(manifest, discovered)
    allowed = native | pairs(manifest["isolation"])
    go_env = json.loads(subprocess.check_output(["go", "env", "-json", "GOVERSION", "GOOS", "GOARCH", "GOAMD64",
        "GOARM", "GOARM64", "GO386", "GOMIPS", "GOMIPS64", "GOPPC64", "GORISCV64", "GOWASM",
        "GOEXPERIMENT", "CGO_ENABLED", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS",
        "CC", "CXX", "GOFLAGS", "GOTOOLCHAIN", "GOENV", "GOWORK", "GOTOOLDIR"], cwd=ROOT))
    if go_env["GOWORK"] != "off":
        raise ValueError("Repository CI requires GOWORK=off; use scripts/ci.sh")
    # -json with no edit flags prints the existing go.mod without rewriting it
    # or loading/downloading the unrelated full dependency graph.
    module = json.loads(subprocess.check_output(["go", "mod", "edit", "-json"], cwd=ROOT))
    check_local_replacements(module)
    tool_hashes = {name: sha(Path(go_env["GOTOOLDIR"]) / name) for name in ("compile", "link", "asm", "cgo")}
    # Bind every inherited variable, including test-specific child controls,
    # without storing values or unsalted per-secret hashes. This is an identity
    # check for trusted local evidence, not an authenticity/security guarantee.
    environment_hmac = environment_identity(directory, {"process": dict(os.environ), "go": go_env})
    contract = {"source": source, "discovered": inventories, "flags": flags, "mode": mode,
                "toolchain": go_env["GOVERSION"], "cgo_enabled": go_env["CGO_ENABLED"],
                "go_tools_sha256": tool_hashes, "environment_hmac": environment_hmac, "go_binary_sha256": sha(Path(shutil.which("go"))),
                "platform": list(os.uname()), "python": sys.version, "batch_size": BATCH_SIZE,
                "extra_pattern": os.environ.get("CI_POSTGRES_EXTRA_PATTERN", "") if mode == "postgres" else ""}
    fingerprint = identity(contract)
    inventory_file = directory / "inventory.json"
    if inventory_file.exists():
        if not resume or json.loads(inventory_file.read_text()) != contract:
            raise ValueError("Source, inventory, flags, toolchain or environment changed; start fresh")
    else:
        save(inventory_file, contract)
    store = Store(ROOT, directory, fingerprint, source, resume)
    print(f"INVENTORY {len(discovered)} packages, {sum(map(len, discovered.values()))} Test/Example/Fuzz roots; fingerprint {fingerprint}", flush=True)
    coverage = {"fingerprint": fingerprint, "mode": mode, "batches": {}, "native_obligations": [], "complete": False}
    if mode == "core":
        for variant in ("ordinary", "race"):
            for index, (package, names) in enumerate(inventories[variant].items()):
                names = [name for name in names if (package, name) not in pairs(manifest["core_excluded"])]
                for number, batch in enumerate(partition(names, manifest["standalone"].get(package, []))):
                    key = f"{variant}-p{index:03d}-b{number:03d}"
                    command = ["go", "test"] + (["-race"] if variant == "race" else []) + ["-json"] + flags + ["-skip", "^TestDockerPostgresIntegration$", package, "-run", pattern(batch)]
                    coverage["batches"][key] = store.run(key, command, {package: batch}, allowed_skips=allowed)
        # Isolation always runs fresh, including after explicit core resumption.
        isolation_env = dict(os.environ, EX_CHAT_MODULE_ACCEPTANCE="1")
        for index, (package, names) in enumerate(manifest["isolation"].items()):
            key = f"isolation-{attempt:03d}-{index:03d}"
            command = ["go", "test", "-json"] + flags + [package, "-run", pattern(names)]
            coverage["batches"][key] = store.run(key, command, {package: names}, env=isolation_env)
        skipped = {tuple(pair) for value in coverage["batches"].values() for pair in value["skipped"]}
        coverage["native_obligations"] = sorted(map(list, (skipped & native) | pairs(manifest["core_excluded"])))
        coverage["isolation_obligations"] = sorted(map(list, skipped & pairs(manifest["isolation"])))
    else:
        nested = pairs(manifest["native_nested_selectors"])
        nested_roots = {(p, t.split("/", 1)[0]) for p, t in nested}
        roots = {package: [name for name in names if (package, name) in native
                           and (package, name) not in nested_roots]
                 for package, names in discovered.items()}
        required = {(p, t) for p, t in native if (p, t.split("/", 1)[0]) not in nested_roots}
        # One grouped invocation preserves package parallelism, including zero-selection packages.
        command = ["go", "test", "-race", "-json"] + flags + PATTERNS + ["-run", pattern(sorted({t for names in roots.values() for t in names}))]
        coverage["batches"]["native"] = store.run("native", command, roots, required=required, exact=True)
        for index, (package, selector) in enumerate(sorted(nested)):
            root = selector.split("/", 1)[0]
            required = {(p, t) for p, t in native if p == package and (t == root or t == selector or t.startswith(selector + "/"))}
            command = ["go", "test", "-race", "-json"] + flags + [package, "-run", nested_pattern(selector)]
            key = f"native-nested-{index:03d}"
            coverage["batches"][key] = store.run(key, command, {package: [root]}, required=required, exact=True)
        extra = os.environ.get("CI_POSTGRES_EXTRA_PATTERN", "")
        if extra:
            extras = discover(ROOT, directory, flags, "-extra", extra, race=True)
            for index, (package, names) in enumerate(extras.items()):
                names = [name for name in names if (package, name) not in native]
                if names:
                    key = f"native-extra-{index:03d}"
                    command = ["go", "test", "-race", "-json"] + flags + [package, "-run", pattern(names)]
                    coverage["batches"][key] = store.run(key, command, {package: names})
        observed = {tuple(pair) for value in coverage["batches"].values() for pair in value["passed"]}
        if not native <= observed:
            raise ValueError("Native gate missing required final cases")
        coverage["required_native_cases"] = sorted(map(list, native))
    store.unchanged()
    coverage.update(complete=True, reused_batches=store.reused,
                    verification="complete component coverage" if store.reused else "fresh complete component coverage")
    save(directory / "coverage.json", coverage)
    print(f"COMPLETE {mode}: {len(coverage['batches'])} validated batches; {store.reused} reused", flush=True)
    if mode == "core":
        print(f"Native obligations: {len(coverage['native_obligations'])}; the separate required PostgreSQL gate must pass for this source", flush=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=["core", "postgres"])
    parser.add_argument("directory", type=Path)
    parser.add_argument("--resume", action="store_true")
    parser.add_argument("--jobs", type=int, default=2)
    parser.add_argument("--parallel", type=int, default=4)
    args = parser.parse_args()
    if not 1 <= args.jobs <= 32 or not 1 <= args.parallel <= 32:
        parser.error("parallelism must be 1..32")
    if args.resume and (args.mode != "core" or os.environ.get("CI") or os.environ.get("GITHUB_ACTIONS")):
        parser.error("trusted resume is local core-only; fresh execution is required in CI and for native services")
    directory = args.directory.resolve()
    if directory.parent != ROOT / "tmp" or not directory.is_dir():
        parser.error("evidence must be an existing immediate directory under repository tmp/")
    with (directory / "runner.lock").open("w") as lock:
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        flags = ["-count=1", "-p", str(args.jobs), "-parallel", str(args.parallel), "-timeout", "20m"]
        run(args.mode, directory, flags, args.resume)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, KeyError) as error:
        raise SystemExit(str(error)) from error
