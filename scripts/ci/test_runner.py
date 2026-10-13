"""CI infrastructure failure-path tests; no Go compiler or services required."""

import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock


spec = importlib.util.spec_from_file_location("runner", Path(__file__).with_name("runner.py"))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)
results = runner.RESULTS
PACKAGE = "example/tests"


def event(action, test="", package=PACKAGE, **extra):
    return dict(Package=package, Action=action, **({"Test": test} if test else {}), **extra)


def success(names=("TestA",)):
    return [event("start")] + [row for name in names for row in (event("run", name), event("pass", name))] + [event("pass")]


class JSONTests(unittest.TestCase):
    def test_listing_includes_tests_examples_fuzz_not_benchmarks(self):
        names = ["TestA", "ExampleA", "FuzzA", "BenchmarkA"]
        rows = [event("start")] + [event("output", Output=name + "\n") for name in names] + [event("pass")]
        self.assertEqual(results.inventory(rows, [PACKAGE]), {PACKAGE: names[:3]})

    def test_listing_requires_every_package_terminal(self):
        for rows in ([event("start")], [event("start"), event("fail")], [event("start"), event("pass"), event("pass")]):
            with self.subTest(rows=rows), self.assertRaises(ValueError):
                results.inventory(rows, [PACKAGE])
        with self.assertRaises(ValueError):
            results.inventory([event("start"), event("pass")], [PACKAGE, "example/missing"])

    def test_zero_test_package_requires_terminal(self):
        self.assertEqual(results.validate([event("start"), event("skip")], {PACKAGE: []})["packages"], {PACKAGE: "skip"})
        with self.assertRaises(ValueError):
            results.validate([event("start")], {PACKAGE: []})

    def test_examples_and_fuzz_roots_require_completion(self):
        names = ["TestA", "ExampleA", "FuzzA"]
        self.assertEqual(len(results.validate(success(names), {PACKAGE: names})["passed"]), 3)
        with self.assertRaises(ValueError):
            results.validate(success(names[:2]), {PACKAGE: names})

    def test_missing_nested_case_is_not_a_parent_pass(self):
        with self.assertRaises(ValueError):
            results.validate(success(), {PACKAGE: ["TestA"]}, {(PACKAGE, "TestA"), (PACKAGE, "TestA/Postgres")}, exact=True)

    def test_extra_native_child_requires_manifest_review(self):
        rows = [event("start"), event("run", "TestA"), event("run", "TestA/new"), event("pass", "TestA/new"), event("pass", "TestA"), event("pass")]
        with self.assertRaises(ValueError):
            results.validate(rows, {PACKAGE: ["TestA"]}, exact=True)

    def test_unfinished_child_duplicate_root_and_missing_run_fail(self):
        candidates = [
            [event("start"), event("run", "TestA"), event("run", "TestA/unfinished"), event("pass", "TestA"), event("pass")],
            success()[:-1] + [event("pass", "TestA"), event("pass")],
            [event("start"), event("pass", "TestA"), event("pass")],
            success() + [event("output", Output="trailing output")],
            success()[:-1],
            success()[:-1] + [event("fail")],
        ]
        for rows in candidates:
            with self.subTest(rows=rows), self.assertRaises(ValueError):
                results.validate(rows, {PACKAGE: ["TestA"]})

    def test_skip_mapping_is_exact_and_never_enabled_for_native(self):
        rows = [event("start"), event("run", "TestA"), event("run", "TestA/Postgres"), event("skip", "TestA/Postgres"), event("pass", "TestA"), event("pass")]
        with self.assertRaises(ValueError):
            results.validate(rows, {PACKAGE: ["TestA"]}, allowed_skips={(PACKAGE, "TestA")})
        self.assertEqual(results.validate(rows, {PACKAGE: ["TestA"]}, allowed_skips={(PACKAGE, "TestA/Postgres")})["skipped"], [[PACKAGE, "TestA/Postgres"]])
        with self.assertRaises(ValueError):
            results.validate(rows, {PACKAGE: ["TestA"]})

    def test_malformed_truncated_and_blank_json_fail(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "test.jsonl"
            for data in ['{', '{}\n', '[]\n', '\n', json.dumps(event("start")), json.dumps(event("start", Test=5)) + '\n']:
                with self.subTest(data=data), self.assertRaises(ValueError):
                    path.write_text(data)
                    list(results.events(path))


class DiscoveryConfigurationTests(unittest.TestCase):
    def test_race_discovery_uses_race_build_for_packages_and_tests(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            def listing(command, cwd, log, stderr):
                log.write_text("".join(json.dumps(row) + "\n" for row in [event("start"), event("output", Output="TestRaceOnly\n"), event("pass")]))
                stderr.write_text("")
                return 0
            with mock.patch.object(runner.subprocess, "run", return_value=mock.Mock(returncode=0, stdout=PACKAGE + "\n")) as packages, mock.patch.object(runner, "stream_command", side_effect=listing) as tests:
                found = runner.discover(root, root, ["-count=1"], race=True)
                self.assertEqual(found, {PACKAGE: ["TestRaceOnly"]})
                self.assertIn("-race", packages.call_args.args[0])
                self.assertIn("-race", tests.call_args.args[0])

    def test_read_only_module_check_accepts_null_and_rejects_local_replacements(self):
        for module in ({}, {"Replace": None}, {"Replace": []}, {"Replace": [{"New": {"Path": "example.test/dependency", "Version": "v1.2.3"}}]}):
            runner.check_local_replacements(module)
        with self.assertRaises(ValueError):
            runner.check_local_replacements({"Replace": [{"New": {"Path": "../external"}}]})

    def test_environment_changes_invalidate_without_recording_values(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            original = {"EXCHAT_SENSITIVE_SMTP_TLS_TEST_CHILD": "/original", "DB_DSN": "private-test-value"}
            first = runner.environment_identity(root, original)
            self.assertEqual(first, runner.environment_identity(root, original))
            changed = dict(original, EXCHAT_SENSITIVE_SMTP_TLS_TEST_CHILD="/changed")
            self.assertNotEqual(first, runner.environment_identity(root, changed))
            self.assertNotIn(b"private-test-value", (root / "environment-salt").read_bytes())
            self.assertEqual(len(first), 64)


class ManifestTests(unittest.TestCase):
    def setUp(self):
        self.manifest = json.loads(Path(__file__).with_name("coverage-manifest.json").read_text())
        self.discovered = {}
        for label in ("native", "isolation", "standalone"):
            for package, names in self.manifest[label].items():
                self.discovered.setdefault(package, set()).update(name.split("/", 1)[0] for name in names)
        self.discovered = {package: sorted(names) for package, names in self.discovered.items()}

    def test_reviewed_native_inventory_and_exact_supplements(self):
        native = runner.validate_manifest(self.manifest, self.discovered)
        self.assertEqual(len(native), 2954)
        self.assertEqual(sum("/" not in test for _, test in native), 181)
        for zone in ("UTC", "Asia/Yerevan", "Asia/Shanghai"):
            self.assertIn(("github.com/OracleBetX-Projects/ex-chat/internal/modruntime",
                           "TestPostgresAICUsage/session_timezone/" + zone), native)
        for root in ("TestPostgresReportCountOffset", "TestPostgresReportCountOffsetBroadYearMicroseconds",
                     "TestPostgresReportCountOffsetInvalidBuckets", "TestPostgresReportCountOffsetSelectionConservation"):
            self.assertIn(("github.com/OracleBetX-Projects/ex-chat/internal/repository", root), native)
        self.assertIn(("github.com/OracleBetX-Projects/ex-chat/internal/service", "TestPostgresReportHumanDurationOwnerStores"), native)
        self.assertIn(("github.com/OracleBetX-Projects/ex-chat/test", "TestPostgresReportHumanDurationHTTP"), native)
        self.assertIn(("github.com/OracleBetX-Projects/ex-chat/internal/service", "TestPostgresReportInboxDependencyOwnerStores"), native)
        self.assertIn(("github.com/OracleBetX-Projects/ex-chat/test", "TestPostgresReportInboxDependencyHTTP"), native)
        self.assertIn(("github.com/OracleBetX-Projects/ex-chat/internal/service", "TestPostgresReportPartialDependencyOwnerStores"), native)
        self.assertIn(("github.com/OracleBetX-Projects/ex-chat/test", "TestPostgresReportPartialDependencyHTTP"), native)
        migrations = [test for _, test in native if test.startswith("TestModuleMigrationsAreIsolatedAndRepeatable/postgres/")]
        self.assertEqual(len(migrations), 26)
        self.assertTrue(any(test == "TestAutomationOwnerNativePGConcurrentMuteAndLabelResolution" for _, test in native))
        self.assertTrue(any(test == "TestAUTSnoozeExpiryAliasesRecoverOnceThroughOrdinaryRules/Postgres" for _, test in native))

    def test_message_actor_admission_native_obligations(self):
        package = "github.com/OracleBetX-Projects/ex-chat/internal/modruntime"
        native = runner.validate_manifest(self.manifest, self.discovered)
        counts = {
            "TestPostgresMSGMessageActorAdmissionAuthority": 6,
            "TestPostgresMSGMessageActorAdmissionFailures": 19,
            "TestPostgresMSGMessageActorAdmissionCompatibility": 16,
            "TestPostgresMSGMessageActorAdmissionLegacyRecovery": 1,
        }
        for root, count in counts.items():
            cases = {test for pkg, test in native if pkg == package and (test == root or test.startswith(root + "/"))}
            self.assertIn(root, cases)
            self.assertEqual(len(cases), count)
        self.assertEqual(sum(counts.values()), 42)

    def test_canned_preview_native_obligations(self):
        package = "github.com/OracleBetX-Projects/ex-chat/internal/modruntime"
        native = runner.validate_manifest(self.manifest, self.discovered)
        counts = {
            'TestPostgresOPSCannedResponsePreviewSemantics': 8,
            'TestPostgresOPSCannedResponsePreviewValidation': 41,
            'TestPostgresOPSCannedResponsePreviewWire': 10,
            'TestPostgresOPSCannedResponsePreviewBounds': 8,
            'TestPostgresOPSCannedResponsePreviewConcurrency': 1,
            'TestPostgresOPSCannedResponsePreviewCancellation': 1,
            'TestPostgresOPSCannedResponsePreviewReadOnlyTransaction': 1,
        }
        for root, count in counts.items():
            cases = {test for pkg, test in native if pkg == package and (test == root or test.startswith(root + "/"))}
            self.assertIn(root, cases)
            self.assertEqual(len(cases), count)
        self.assertEqual(sum(counts.values()), 70)

    def test_macro_plan_preflight_native_obligations(self):
        package = "github.com/OracleBetX-Projects/ex-chat/test"
        native = runner.validate_manifest(self.manifest, self.discovered)
        for root, count in (("TestPostgresMacroPlanPreflightOwners", 64),
                            ("TestPostgresMacroPlanPreflightHTTP", 16)):
            cases = {name for pkg, name in native if pkg == package
                     and (name == root or name.startswith(root + "/"))}
            self.assertIn(root, cases)
            self.assertEqual(len(cases), count)
        for case in ("TestPostgresMacroPlanPreflightOwners/reject_entire_plan_before_effects/empty_name",
                     "TestPostgresMacroPlanPreflightOwners/parsing_and_name_precedence/unsupported_action_name_wins",
                     "TestPostgresMacroPlanPreflightOwners/implemented_names_keep_owner_effects/send_attachment",
                     "TestPostgresMacroPlanPreflightOwners/preflight_before_owner_availability",
                     "TestPostgresMacroPlanPreflightHTTP/authority_and_visibility_remain_first/personal_admin",
                     "TestPostgresMacroPlanPreflightHTTP/external_creation_contract_unchanged/send_email_transcript",
                     "TestPostgresMacroPlanPreflightHTTP/valid_plan_existing_success_envelope"):
            self.assertIn((package, case), native)

    def test_canned_response_maintenance_native_obligations(self):
        native = runner.validate_manifest(self.manifest, self.discovered)
        for package, root, count in (
            ("internal/modruntime", "TestPostgresOPSCannedResponseManagement", 6),
            ("internal/modruntime", "TestPostgresOPSCannedResponseValidation", 25),
            ("internal/modruntime", "TestPostgresOPSCannedResponseRollback", 7),
            ("internal/modruntime", "TestPostgresOPSCannedResponseConcurrency", 8),
            ("internal/modruntime", "TestPostgresOPSCannedResponseRecovery", 10),
            ("internal/modruntime", "TestPostgresOPSCannedResponseMigration", 1),
            ("internal/handler", "TestPostgresCannedResponseRuntimeRESTGuard", 7),
            ("internal/modruntime", "TestPostgresOPSCannedResponseEventDelivery", 3),
            ("internal/modruntime", "TestPostgresOPSCannedResponseWire", 15),
        ):
            package = "github.com/OracleBetX-Projects/ex-chat/" + package
            cases = {name for pkg, name in native if pkg == package
                     and (name == root or name.startswith(root + "/"))}
            self.assertIn(root, cases)
            self.assertEqual(len(cases), count)

    def test_new_native_root_cannot_silently_disappear(self):
        package = next(iter(self.discovered))
        self.discovered[package].append("TestNewNativePGCase")
        with self.assertRaises(ValueError):
            runner.validate_manifest(self.manifest, self.discovered)

    def test_deleted_or_renamed_root_is_not_satisfied_by_old_manifest(self):
        package = next(iter(self.manifest["native"]))
        self.discovered[package].remove(self.manifest["native"][package][0].split("/", 1)[0])
        with self.assertRaises(ValueError):
            runner.validate_manifest(self.manifest, self.discovered)

    def test_missing_nested_selector_is_invalid(self):
        package = next(iter(self.manifest["native"]))
        self.manifest["native"][package].remove("TestModuleMigrationsAreIsolatedAndRepeatable/postgres")
        with self.assertRaises(ValueError):
            runner.validate_manifest(self.manifest, self.discovered)

    def test_partition_is_exhaustive_and_slow_cases_standalone(self):
        names = [f"TestCase{i}" for i in range(31)]
        batches = list(runner.partition(names, ["TestCase13"]))
        self.assertEqual([name for batch in batches for name in batch], names)
        self.assertIn(["TestCase13"], batches)
        self.assertTrue(all(len(batch) <= 12 for batch in batches))
        self.assertEqual(list(runner.partition([])), [[]])

    def test_nested_go_patterns_are_segment_anchored(self):
        self.assertEqual(runner.nested_pattern("TestA/Postgres"), "^TestA$/^Postgres$")
        self.assertEqual(runner.nested_pattern("TestM/postgres"), "^TestM$/^postgres$")


class CheckpointTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "fixture.txt").write_text("source")
        self.directory = self.root / "tmp" / "evidence"
        self.directory.mkdir(parents=True)
        self.source = runner.source_inventory(self.root)
        self.store = runner.Store(self.root, self.directory, "fingerprint", self.source)
        self.command = ["go", "test", "-count=1", "-race", "-json", PACKAGE]
        self.expected = {PACKAGE: ["TestA"]}

    def emit(self, command, root, log, stderr, env):
        log.write_text("".join(json.dumps(row) + "\n" for row in success()))
        stderr.write_text("")
        return 0

    def complete(self):
        with mock.patch.object(runner, "stream_command", side_effect=self.emit):
            return self.store.run("batch", self.command, self.expected)

    def test_resume_revalidates_successful_complete_batch(self):
        expected = self.complete()
        resumed = runner.Store(self.root, self.directory, "fingerprint", self.source, resume=True)
        with mock.patch.object(runner, "stream_command") as execute:
            self.assertEqual(resumed.run("batch", self.command, self.expected), expected)
            execute.assert_not_called()
        self.assertEqual(resumed.reused, 1)

    def test_default_never_reuses_existing_checkpoints(self):
        self.complete()
        with self.assertRaises(ValueError):
            self.store.run("batch", self.command, self.expected)

    def test_changed_fingerprint_command_inventory_or_source_rejected(self):
        self.complete()
        for fingerprint, command, expected in [
            ("changed-toolchain-flags-or-environment", self.command, self.expected),
            ("fingerprint", self.command + ["-short"], self.expected),
            ("fingerprint", self.command, {PACKAGE: ["TestA", "ExampleB"]}),
        ]:
            resumed = runner.Store(self.root, self.directory, fingerprint, self.source, resume=True)
            with self.subTest(fingerprint=fingerprint, command=command, expected=expected), self.assertRaises(ValueError):
                resumed.run("batch", command, expected)
        (self.root / "fixture.txt").write_text("changed")
        with self.assertRaises(ValueError):
            self.store.run("new", self.command, self.expected)

    def test_partial_attempt_is_retained_but_rerun(self):
        folder = self.directory / "batch"
        folder.mkdir()
        (folder / "attempt-001.start.json").write_text("{}")
        partial = folder / "attempt-001.jsonl"
        partial.write_text(json.dumps(event("start")) + "\n")
        self.store.resume = True
        self.complete()
        self.assertEqual(partial.read_text(), json.dumps(event("start")) + "\n")
        self.assertTrue((folder / "attempt-002.jsonl").exists())
        self.assertEqual(self.store.reused, 0)

    def test_failure_even_with_successful_json_is_not_checkpoint(self):
        def failed(*args):
            self.emit(*args)
            return 1
        with mock.patch.object(runner, "stream_command", side_effect=failed), self.assertRaises(ValueError):
            self.store.run("batch", self.command, self.expected)
        self.store.resume = True
        with mock.patch.object(runner, "stream_command") as execute, self.assertRaises(ValueError):
            self.store.run("batch", self.command, self.expected)
        execute.assert_not_called()

    def test_truncated_log_with_updated_digest_still_fails(self):
        self.complete()
        checkpoint = self.directory / "batch" / "result.json"
        saved = json.loads(checkpoint.read_text())
        log = checkpoint.parent / saved["log"]
        log.write_text("".join(json.dumps(row) + "\n" for row in success()[:-1]))
        saved["log_sha256"] = runner.sha(log)
        runner.save(checkpoint, saved)
        self.store.resume = True
        with self.assertRaises(ValueError):
            self.store.run("batch", self.command, self.expected)

    def test_changed_raw_log_is_not_reused(self):
        self.complete()
        log = self.directory / "batch" / "attempt-001.jsonl"
        log.write_text(log.read_text() + "\n")
        self.store.resume = True
        with self.assertRaises(ValueError):
            self.store.run("batch", self.command, self.expected)

    def test_full_source_and_toolchain_inventory_flags_affect_identity(self):
        original = {"source": self.source, "discovered": self.expected, "flags": ["-count=1"], "go_env": {"GOVERSION": "go1.25.4"}}
        digest = runner.identity(original)
        for key, value in [("source", {}), ("discovered", {}), ("flags", []), ("go_env", {"GOVERSION": "other"})]:
            self.assertNotEqual(digest, runner.identity(dict(original, **{key: value})))


if __name__ == "__main__":
    unittest.main()
