"""Regression checks for CI guards; no services or Go compiler needed."""

import importlib.util
import shlex
import unittest
from unittest import mock
from pathlib import Path


def load(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + ".py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


results = load("check-results")
postgres = load("check-postgres")


class ResultsTests(unittest.TestCase):
    def setUp(self):
        self.listed = [
            {"Package": "example/repository", "Action": "start"},
            {"Package": "example/repository", "Action": "output", "Output": "TestPostgresExample\n"},
            {"Package": "example/repository", "Action": "pass"},
        ]
        self.passed = [
            {"Package": "example/repository", "Action": "start"},
            {"Package": "example/repository", "Test": "TestPostgresExample", "Action": "run"},
            {"Package": "example/repository", "Test": "TestPostgresExample", "Action": "pass"},
            {"Package": "example/repository", "Action": "pass"},
        ]

    def test_pass(self):
        self.assertEqual(results.check(self.listed, self.passed), 1)

    def test_no_tests_is_not_pass(self):
        with self.assertRaises(ValueError):
            results.check([], self.passed)

    def test_skip_is_not_pass(self):
        skipped = self.passed[:2] + [dict(self.passed[2], Action="skip"), self.passed[3]]
        with self.assertRaises(ValueError):
            results.check(self.listed, skipped)

    def test_selected_subtest_skip_is_not_pass(self):
        child = {"Package": "example/repository", "Test": "TestPostgresExample/concurrency", "Action": "run"}
        with self.assertRaises(ValueError):
            results.check(self.listed, self.passed[:2] + [child, dict(child, Action="skip")] + self.passed[2:])

    def test_unrelated_unlisted_test_is_not_pass(self):
        unrelated = dict(self.passed[1], Test="TestOptionalProvider", Action="skip")
        with self.assertRaises(ValueError):
            results.check(self.listed, self.passed[:-1] + [unrelated, self.passed[-1]])

    def test_truncated_result_is_not_pass(self):
        with self.assertRaises(ValueError):
            results.check(self.listed, self.passed[:-1])

    def test_package_failure_is_not_pass(self):
        with self.assertRaises(ValueError):
            results.check(self.listed, self.passed[:-1] + [{"Package": "example/repository", "Action": "fail"}])

    def test_same_name_in_another_package_must_pass_separately(self):
        listed = self.listed + [dict(row, Package="example/other") for row in self.listed]
        with self.assertRaises(ValueError):
            results.check(listed, self.passed)


class PostgresTargetTests(unittest.TestCase):
    def setUp(self):
        self.environment = {"EX_CHAT_CI_DISPOSABLE_DB": "1", "DB_DSN": "host=127.0.0.1 port=55433 user=postgres password=test-only dbname=ex_chat_ci sslmode=disable"}

    def test_explicit_disposable_loopback(self):
        self.assertEqual(postgres.check_dsn(self.environment)["dbname"], "ex_chat_ci")

    def test_missing_acknowledgment(self):
        del self.environment["EX_CHAT_CI_DISPOSABLE_DB"]
        with self.assertRaises(ValueError):
            postgres.check_dsn(self.environment)

    def test_missing_dsn(self):
        del self.environment["DB_DSN"]
        with self.assertRaises(ValueError):
            postgres.check_dsn(self.environment)

    def test_unsafe_or_ambiguous_targets(self):
        targets = [
            "host=remote.example", "host=127.0.0.1 hostaddr=203.0.113.1",
            "host=127.0.0.1 service=production", "host=127.0.0.1 host=remote.example",
            "host=127.0.0.1,remote.example", "host=/var/run/postgresql",
        ]
        for target in targets:
            with self.subTest(target=target), self.assertRaises(ValueError):
                postgres.check_dsn(dict(self.environment, DB_DSN=self.environment["DB_DSN"].replace("host=127.0.0.1", target)))

    def test_default_admin_database_rejected(self):
        with self.assertRaises(ValueError):
            postgres.check_dsn(dict(self.environment, DB_DSN=self.environment["DB_DSN"].replace("dbname=ex_chat_ci", "dbname=postgres")))

    def test_url_rejected_for_legacy_test_compatibility(self):
        with self.assertRaises(ValueError):
            postgres.check_dsn(dict(self.environment, DB_DSN="postgres://postgres@127.0.0.1:55433/ex_chat_ci"))


class RedisReadinessTests(unittest.TestCase):
    @mock.patch.object(postgres.socket, "create_connection")
    def test_passwordless_ping(self, connect):
        stream = connect.return_value.__enter__.return_value.makefile.return_value
        stream.readline.return_value = b"+PONG\r\n"
        postgres.check_redis({"REDIS_ADDR": "127.0.0.1:6380"})
        stream.write.assert_called_once_with(b"*1\r\n$4\r\nPING\r\n")

    @mock.patch.object(postgres.socket, "create_connection")
    def test_authentication_then_ping(self, connect):
        stream = connect.return_value.__enter__.return_value.makefile.return_value
        stream.readline.side_effect = [b"+OK\r\n", b"+PONG\r\n"]
        postgres.check_redis({"REDIS_ADDR": "127.0.0.1:6380", "REDIS_PASSWORD": "test-only"})
        self.assertEqual(stream.write.call_args_list, [
            mock.call(b"*2\r\n$4\r\nAUTH\r\n$9\r\ntest-only\r\n"),
            mock.call(b"*1\r\n$4\r\nPING\r\n"),
        ])

    @mock.patch.object(postgres.socket, "create_connection")
    def test_failed_authentication_does_not_ping(self, connect):
        stream = connect.return_value.__enter__.return_value.makefile.return_value
        stream.readline.return_value = b"-ERR invalid password\r\n"
        with self.assertRaisesRegex(ValueError, "authentication failed"):
            postgres.check_redis({"REDIS_ADDR": "127.0.0.1:6380", "REDIS_PASSWORD": "test-only"})
        self.assertEqual(stream.write.call_count, 1)

    @mock.patch.object(postgres.socket, "create_connection")
    def test_non_loopback_address_never_connects(self, connect):
        with self.assertRaises(ValueError):
            postgres.check_redis({"REDIS_ADDR": "remote.example:6379"})
        connect.assert_not_called()


class GateConfigurationTests(unittest.TestCase):
    def test_all_postgres_dsns_are_enabled_and_cleared(self):
        script = Path(__file__).parents[1].joinpath("ci.sh").read_text(encoding="utf-8")
        exported, cleared = {}, set()
        for line in script.splitlines():
            if line.strip().startswith("export "):
                for assignment in shlex.split(line)[1:]:
                    key, _, value = assignment.partition("=")
                    exported[key] = value
            if line.strip().startswith("unset "):
                cleared.update(shlex.split(line)[1:])
        for name in ("PAYMENT_TEST_POSTGRES_DSN", "CHANNEL_TEST_POSTGRES_DSN", "AUTOMATION_HISTORY_TEST_POSTGRES_DSN", "SAML_TEST_POSTGRES_DSN"):
            with self.subTest(name=name):
                self.assertEqual(exported[name], "$DB_DSN")
                self.assertIn(name, cleared)
        for name in ("EX_CHAT_AUTH_POSTGRES_TESTS", "EX_CHAT_AUT_POSTGRES_TESTS"):
            with self.subTest(name=name):
                self.assertEqual(exported[name], "1")
                self.assertIn(name, cleared)


if __name__ == "__main__":
    unittest.main()
