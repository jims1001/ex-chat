#!/usr/bin/env python3
"""Validate explicit disposable, loopback-only test targets and Redis readiness."""

import ipaddress
import os
import shlex
import socket


def loopback(host):
    # Do not resolve arbitrary hostnames (including DNS aliases) into targets.
    if host == "localhost":
        return True
    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


def check_dsn(environ):
    if environ.get("EX_CHAT_CI_DISPOSABLE_DB") != "1":
        raise ValueError("Set EX_CHAT_CI_DISPOSABLE_DB=1 only for a disposable test database; tests create/drop schemas and write data")
    raw = environ.get("DB_DSN", "")
    if not raw:
        raise ValueError("DB_DSN is required; there is no fallback test database")
    if raw.startswith(("postgres://", "postgresql://")):
        raise ValueError("Use a libpq keyword DB_DSN: some existing tests append search_path and do not support URLs")
    allowed = {"host", "port", "user", "password", "dbname", "sslmode", "connect_timeout", "TimeZone"}
    fields = {}
    try:
        for token in shlex.split(raw):
            key, separator, value = token.partition("=")
            if not separator or key not in allowed or key in fields or not value:
                raise ValueError()
            fields[key] = value
    except ValueError as error:
        raise ValueError("DB_DSN must contain unique supported keyword=value fields; service/hostaddr/options overrides are forbidden") from error
    if not loopback(fields.get("host", "")):
        raise ValueError("DB_DSN must explicitly select a loopback host")
    if not fields.get("user") or not fields.get("dbname") or fields["dbname"] in {"postgres", "template0", "template1"}:
        raise ValueError("DB_DSN must explicitly select a user and a dedicated test database")
    if not fields.get("port", "").isdigit() or not 1 <= int(fields["port"]) <= 65535:
        raise ValueError("DB_DSN must explicitly select a valid port")
    if fields.get("sslmode") != "disable":
        raise ValueError("This loopback-only disposable gate requires sslmode=disable")
    return fields


def check_redis(environ):
    address = environ.get("REDIS_ADDR", "")
    host, separator, port = address.rpartition(":")
    host = host.strip("[]")
    if not separator or not loopback(host) or not port.isdigit() or not 1 <= int(port) <= 65535:
        raise ValueError("REDIS_ADDR must explicitly select a loopback host:port for a disposable Redis service")
    with socket.create_connection((host, int(port)), timeout=5) as connection:
        stream = connection.makefile("rwb")

        def command(*arguments):
            values = [argument.encode() for argument in arguments]
            request = b"*%d\r\n" % len(values)
            for value in values:
                request += b"$%d\r\n" % len(value) + value + b"\r\n"
            stream.write(request)
            stream.flush()
            return stream.readline(4096)

        password = environ.get("REDIS_PASSWORD")
        if password and command("AUTH", password) != b"+OK\r\n":
            raise ValueError("Redis authentication failed")
        if command("PING") != b"+PONG\r\n":
            raise ValueError("Redis did not return PONG")
    print("Redis PING passed (service readiness only)")


if __name__ == "__main__":
    try:
        check_dsn(os.environ)
        check_redis(os.environ)
    except (ValueError, OSError) as error:
        raise SystemExit(str(error)) from error
