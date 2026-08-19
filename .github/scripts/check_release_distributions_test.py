#!/usr/bin/env python3

import datetime as dt
import tempfile
import unittest
from argparse import Namespace
from pathlib import Path
from unittest import mock

import check_release_distributions as checker


CDN_ASSETS = [
    "juicefs-1.4.1-linux-amd64.tar.gz",
    "juicefs-1.4.1-linux-arm64.tar.gz",
    "juicefs-1.4.1-darwin-amd64.tar.gz",
    "juicefs-1.4.1-darwin-arm64.tar.gz",
    "juicefs-1.4.1-windows-amd64.tar.gz",
    "juicefs-1.4.1-windows-amd64.zip",
]


class FakeClient:
    def __init__(self, *, json_values=None, text_values=None, fail=False, download_error=False):
        self.json_values = json_values or {}
        self.text_values = text_values or {}
        self.fail = fail
        self.download_error = download_error
        self.urls = []
        self.download_urls = []

    def _lookup(self, values, url):
        if self.fail:
            raise checker.CheckError("network unavailable")
        for marker, value in values.items():
            if marker in url:
                return value
        raise checker.CheckError(f"unexpected URL: {url}")

    def get_json(self, url, headers=None):
        self.urls.append(url)
        return self._lookup(self.json_values, url)

    def get_text(self, url, headers=None):
        self.urls.append(url)
        return self._lookup(self.text_values, url)

    def probe(self, url):
        self.urls.append(url)
        if self.fail:
            raise checker.CheckError("network unavailable")

    def probe_download(self, url):
        self.urls.append(url)
        self.download_urls.append(url)
        if self.fail or self.download_error:
            raise checker.CheckError("download unavailable")


class ReleaseResolutionTest(unittest.TestCase):
    def test_resolver_calls_the_explicit_latest_release_endpoint(self):
        client = FakeClient(
            json_values={
                "/releases/latest": {
                    "id": 362269109,
                    "tag_name": "v1.4.1",
                    "published_at": "2026-07-30T08:03:19Z",
                    "draft": False,
                    "prerelease": False,
                }
            }
        )

        release, _ = checker.github_latest_release(client, "juicedata/juicefs")

        self.assertEqual("v1.4.1", release.tag)
        self.assertEqual(
            ["https://api.github.com/repos/juicedata/juicefs/releases/latest"],
            client.urls,
        )

    def test_release_event_uses_the_trigger_release_tag(self):
        client = FakeClient(
            json_values={
                "/releases/tags/v1.3.4": {
                    "id": 410000001,
                    "tag_name": "v1.3.4",
                    "published_at": "2026-08-19T06:00:00Z",
                    "draft": False,
                    "prerelease": False,
                }
            }
        )

        release, _ = checker.github_target_release(client, "juicedata/juicefs", "v1.3.4")

        self.assertEqual("v1.3.4", release.tag)
        self.assertEqual("1.3.4", release.version)
        self.assertEqual(
            ["https://api.github.com/repos/juicedata/juicefs/releases/tags/v1.3.4"],
            client.urls,
        )

    def test_github_token_is_scoped_to_the_latest_release_request(self):
        requests = []

        class Response:
            def __init__(self, body):
                self.body = body

            def __enter__(self):
                return self

            def __exit__(self, *_):
                return False

            def read(self):
                return self.body

        def urlopen(request, timeout):
            requests.append(request)
            if request.full_url.endswith("/releases/latest"):
                return Response(
                    b'{"id": 362269109, "tag_name": "v1.4.1", '
                    b'"published_at": "2026-07-30T08:03:19Z", '
                    b'"draft": false, "prerelease": false}'
                )
            return Response(b'{"versions": {"stable": "1.4.1"}}')

        client = checker.HttpClient(retries=1)
        with mock.patch.object(checker.urllib.request, "urlopen", urlopen):
            checker.github_latest_release(client, "juicedata/juicefs", token="dummy-token")
            checker.check_homebrew(client, "1.4.1")

        self.assertEqual("Bearer dummy-token", requests[0].get_header("Authorization"))
        self.assertIsNone(requests[1].get_header("Authorization"))

    def test_resolve_command_exports_the_trigger_release(self):
        payload = {
            "id": 410000001,
            "tag_name": "v1.3.4",
            "published_at": "2026-08-19T06:00:00Z",
            "draft": False,
            "prerelease": False,
        }
        original_client = checker.HttpClient
        with tempfile.TemporaryDirectory() as temp_dir:
            output_path = Path(temp_dir, "output.txt")
            summary_path = Path(temp_dir, "summary.md")
            checker.HttpClient = lambda: FakeClient(json_values={"/releases/tags/v1.3.4": payload})
            try:
                result = checker.resolve_command(
                    Namespace(
                        repository="juicedata/juicefs",
                        trigger_tag="v1.3.4",
                        github_output=str(output_path),
                        summary=str(summary_path),
                    )
                )
            finally:
                checker.HttpClient = original_client

            self.assertEqual(0, result)
            outputs = output_path.read_text(encoding="utf-8")
            self.assertIn("target_version=1.3.4", outputs)
            self.assertIn("release_id=410000001", outputs)
            summary = summary_path.read_text(encoding="utf-8")
            self.assertIn("Trigger release: `v1.3.4`", summary)
            self.assertIn("Target release: `v1.3.4`", summary)

    def test_resolve_command_exports_the_latest_release_without_trigger(self):
        payload = {
            "id": 362269109,
            "tag_name": "v1.4.1",
            "published_at": "2026-07-30T08:03:19Z",
            "draft": False,
            "prerelease": False,
        }
        original_client = checker.HttpClient
        with tempfile.TemporaryDirectory() as temp_dir:
            output_path = Path(temp_dir, "output.txt")
            summary_path = Path(temp_dir, "summary.md")
            checker.HttpClient = lambda: FakeClient(json_values={"/releases/latest": payload})
            try:
                result = checker.resolve_command(
                    Namespace(
                        repository="juicedata/juicefs",
                        trigger_tag="",
                        github_output=str(output_path),
                        summary=str(summary_path),
                    )
                )
            finally:
                checker.HttpClient = original_client

            self.assertEqual(0, result)
            outputs = output_path.read_text(encoding="utf-8")
            self.assertIn("target_version=1.4.1", outputs)
            summary = summary_path.read_text(encoding="utf-8")
            self.assertIn("Trigger release: `schedule/manual`", summary)
            self.assertIn("Target release: `v1.4.1`", summary)

    def test_invalid_latest_release_is_rejected(self):
        cases = [
            {"id": 1, "tag_name": "v1.4.1", "published_at": "2026-01-01T00:00:00Z", "draft": True},
            {"id": 1, "tag_name": "v1.4.1", "published_at": "2026-01-01T00:00:00Z", "prerelease": True},
        ]
        for payload in cases:
            with self.subTest(payload=payload), self.assertRaises(checker.CheckError):
                checker.parse_latest_release(payload)


class HttpClientTest(unittest.TestCase):
    def test_download_probe_uses_range_get_and_reads_only_one_byte(self):
        requests = []
        read_sizes = []

        class Response:
            def __enter__(self):
                return self

            def __exit__(self, *_):
                return False

            def read(self, size=-1):
                read_sizes.append(size)
                return b"x"

        def urlopen(request, timeout):
            requests.append(request)
            return Response()

        with mock.patch.object(checker.urllib.request, "urlopen", urlopen):
            checker.HttpClient(retries=1).probe_download("https://cdn.example/client.tar.gz")

        self.assertEqual("GET", requests[0].get_method())
        self.assertEqual("bytes=0-0", requests[0].get_header("Range"))
        self.assertEqual([1], read_sizes)

    def test_download_probe_rejects_an_empty_response(self):
        class Response:
            def __enter__(self):
                return self

            def __exit__(self, *_):
                return False

            def read(self, size=-1):
                return b""

        with mock.patch.object(checker.urllib.request, "urlopen", lambda request, timeout: Response()):
            with self.assertRaises(checker.CheckError):
                checker.HttpClient(retries=1).probe_download("https://cdn.example/empty.tar.gz")


class VersionTest(unittest.TestCase):
    def test_package_and_binary_revisions_use_the_base_version(self):
        current = [
            "1.4.1",
            "1.4.1-1",
            "1:1.4.1-1~ubuntu24.04.1",
            "1.4.1-1.fc42",
            "juicefs version 1.4.1+2026-07-30.0b90c7d",
        ]
        for actual in current:
            with self.subTest(actual=actual):
                self.assertEqual("current", checker.version_status("1.4.1", actual))

    def test_older_or_newer_base_version_is_stale(self):
        for actual in ["1.3.3", "1.4.0-9", "1.4.2", "unknown"]:
            with self.subTest(actual=actual):
                self.assertEqual("stale", checker.version_status("1.4.1", actual))


class GracePeriodTest(unittest.TestCase):
    def setUp(self):
        self.published = checker.parse_datetime("2026-07-30T08:03:19Z")

    def test_grace_uses_target_release_publication_time(self):
        self.assertFalse(
            checker.grace_expired(
                self.published,
                24,
                checker.parse_datetime("2026-07-31T08:03:18Z"),
            )
        )
        self.assertTrue(
            checker.grace_expired(
                self.published,
                24,
                checker.parse_datetime("2026-07-31T08:03:19Z"),
            )
        )

    def test_stale_and_unavailable_fail_only_after_grace(self):
        results = [checker.CheckResult("test", "stale", "1.4.1", "1.3.3")]
        self.assertFalse(checker.should_fail(results, expired=False))
        self.assertTrue(checker.should_fail(results, expired=True))
        invalid = [checker.CheckResult("test", "invalid", "1.4.1", "unknown")]
        self.assertTrue(checker.should_fail(invalid, expired=False))


class VerifyVersionCommandTest(unittest.TestCase):
    def test_build_metadata_is_accepted(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            actual_path = Path(temp_dir, "actual.txt")
            summary_path = Path(temp_dir, "summary.md")
            actual_path.write_text("juicefs version 1.4.1+revision\n", encoding="utf-8")
            result = checker.verify_version_command(
                Namespace(
                    channel="installer",
                    expected="1.4.1",
                    actual_file=str(actual_path),
                    command_status=0,
                    published_at=(dt.datetime.now(checker.UTC) - dt.timedelta(hours=1)).isoformat(),
                    grace_hours=24,
                    summary=str(summary_path),
                )
            )
            self.assertEqual(0, result)
            self.assertIn("current", summary_path.read_text(encoding="utf-8"))


class ChannelFixtureTest(unittest.TestCase):
    expected = "1.4.1"
    release_payload = {
        "assets": [{"name": "checksums.txt"}]
        + [{"name": name} for name in CDN_ASSETS]
        + [{"name": "juicefs-hadoop-1.4.1.jar"}]
    }

    def assert_current_and_stale(self, build):
        current, stale = build()
        self.assertEqual("current", current.status)
        self.assertEqual("stale", stale.status)

    def test_cdn_current_and_stale(self):
        checksums = "".join(f"{'a' * 64}  {name}\n" for name in CDN_ASSETS)
        current_client = FakeClient(
            text_values={"latest-version.txt": "1.4.1\n", "checksums.txt": checksums}
        )
        stale_client = FakeClient(
            text_values={"latest-version.txt": "1.3.3\n", "checksums.txt": checksums}
        )

        current = checker.check_cdn(current_client, self.expected, self.release_payload)
        stale = checker.check_cdn(stale_client, self.expected, self.release_payload)

        self.assertEqual("current", current.status)
        self.assertEqual("stale", stale.status)
        self.assertEqual(6, len(current_client.download_urls))
        self.assertIn("6/6 ranged GET", current.detail)

    def test_cdn_requires_every_fixed_client_asset(self):
        checksums = "".join(f"{'a' * 64}  {name}\n" for name in CDN_ASSETS)
        payload = {"assets": [{"name": name} for name in CDN_ASSETS[:-1]]}
        result = checker.check_cdn(
            FakeClient(text_values={"latest-version.txt": "1.4.1", "checksums.txt": checksums}),
            self.expected,
            payload,
        )

        self.assertEqual("stale", result.status)
        self.assertIn(CDN_ASSETS[-1], result.detail)

    def test_cdn_rejects_missing_or_invalid_checksums(self):
        checksums = "".join(
            ("not-a-sha" if index == 0 else "a" * 64) + f"  {name}\n"
            for index, name in enumerate(CDN_ASSETS[:-1])
        )
        result = checker.check_cdn(
            FakeClient(text_values={"latest-version.txt": "1.4.1", "checksums.txt": checksums}),
            self.expected,
            self.release_payload,
        )

        self.assertEqual("stale", result.status)
        self.assertIn(CDN_ASSETS[0], result.detail)
        self.assertIn(CDN_ASSETS[-1], result.detail)

    def test_cdn_download_failure_is_unavailable(self):
        checksums = "".join(f"{'a' * 64}  {name}\n" for name in CDN_ASSETS)
        client = FakeClient(
            text_values={"latest-version.txt": "1.4.1", "checksums.txt": checksums},
            download_error=True,
        )
        result = checker.run_channel(
            "CDN",
            self.expected,
            lambda: checker.check_cdn(client, self.expected, self.release_payload),
        )

        self.assertEqual("unavailable", result.status)
        self.assertIn("0/6 ranged GET", result.detail)
        self.assertIn(CDN_ASSETS[0], result.detail)

    def test_homebrew_current_and_stale(self):
        self.assert_current_and_stale(
            lambda: (
                checker.check_homebrew(FakeClient(json_values={"formula": {"versions": {"stable": "1.4.1"}}}), self.expected),
                checker.check_homebrew(FakeClient(json_values={"formula": {"versions": {"stable": "1.3.3"}}}), self.expected),
            )
        )

    def test_ppa_filters_unsupported_series(self):
        def client(version):
            entries = {
                "entries": [
                    {"distro_series_link": "https://api.launchpad.net/1.0/ubuntu/noble", "source_package_version": version},
                    {"distro_series_link": "https://api.launchpad.net/1.0/ubuntu/oracular", "source_package_version": "1.2.0-1"},
                ]
            }
            return FakeClient(
                json_values={
                    "+archive/ubuntu/ppa?": entries,
                    "+archive/ubuntu/arm64?": entries,
                    "/ubuntu/noble": {"supported": True},
                    "/ubuntu/oracular": {"supported": False},
                }
            )

        current = checker.check_ppa(client("1.4.1-1"), self.expected)
        stale = checker.check_ppa(client("1.3.3-1"), self.expected)
        self.assertEqual("current", current.status)
        self.assertEqual("stale", stale.status)
        self.assertNotIn("oracular", current.actual)

    def test_copr_current_and_partial_failure(self):
        def payload(state, version):
            return {
                "packages": [
                    {
                        "name": "juicefs",
                        "chroots": {
                            "fedora-x86_64": {"state": state, "pkg_version": version},
                            "fedora-aarch64": {"state": "succeeded", "pkg_version": "1.4.1-1"},
                        },
                    }
                ]
            }

        current = checker.check_copr(FakeClient(json_values={"monitor": payload("succeeded", "1.4.1-1")}), self.expected)
        stale = checker.check_copr(FakeClient(json_values={"monitor": payload("failed", "1.3.3-1")}), self.expected)
        self.assertEqual("current", current.status)
        self.assertEqual("stale", stale.status)
        self.assertIn("failed chroots", stale.detail)

    def test_snap_uses_stable_channel_only(self):
        def payload(stable):
            return {
                "channel-map": [
                    {"channel": {"track": "latest", "risk": "stable", "architecture": "amd64"}, "version": stable},
                    {"channel": {"track": "latest", "risk": "edge", "architecture": "amd64"}, "version": "9.9.9"},
                ]
            }

        self.assert_current_and_stale(
            lambda: (
                checker.check_snap(FakeClient(json_values={"snaps/info": payload("1.4.1")}), self.expected),
                checker.check_snap(FakeClient(json_values={"snaps/info": payload("1.3.3")}), self.expected),
            )
        )

    def test_aur_checks_only_stable_packages(self):
        def payload(version):
            return {
                "results": [
                    {"Name": "juicefs", "Version": version},
                    {"Name": "juicefs-bin", "Version": version},
                    {"Name": "juicefs-git", "Version": "dev-1"},
                ]
            }

        self.assert_current_and_stale(
            lambda: (
                checker.check_aur(FakeClient(json_values={"aur.archlinux": payload("1.4.1-1")}), self.expected),
                checker.check_aur(FakeClient(json_values={"aur.archlinux": payload("1.3.3-1")}), self.expected),
            )
        )

    def test_scoop_current_and_stale(self):
        def payload(version):
            return {
                "version": version,
                "architecture": {
                    "64bit": {
                        "url": f"https://github.com/juicedata/juicefs/releases/download/v{version}/juicefs.tar.gz",
                        "hash": "abc",
                    }
                },
            }

        self.assert_current_and_stale(
            lambda: (
                checker.check_scoop(FakeClient(json_values={"juicefs.json": payload("1.4.1")}), self.expected),
                checker.check_scoop(FakeClient(json_values={"juicefs.json": payload("1.3.3")}), self.expected),
            )
        )

    def test_docker_requires_versioned_and_latest_tags(self):
        current = checker.check_docker(
            FakeClient(json_values={"ce-v1.4.1": {"tag_status": "active"}, "/latest": {"tag_status": "active"}}),
            self.expected,
        )
        stale = checker.check_docker(
            FakeClient(json_values={"ce-v1.4.1": {"tag_status": "active"}, "/latest": {"tag_status": "inactive"}}),
            self.expected,
        )
        self.assertEqual("current", current.status)
        self.assertEqual("stale", stale.status)

    def test_maven_current_and_stale(self):
        def metadata(version):
            return (
                "<metadata><versioning>"
                f"<latest>{version}</latest><release>{version}</release>"
                f"<versions><version>{version}</version></versions>"
                "</versioning></metadata>"
            )

        self.assert_current_and_stale(
            lambda: (
                checker.check_maven(FakeClient(text_values={"maven-metadata": metadata("1.4.1")}), self.expected),
                checker.check_maven(FakeClient(text_values={"maven-metadata": metadata("1.3.3")}), self.expected),
            )
        )

    def test_every_channel_reports_unavailable_without_hiding_others(self):
        client = FakeClient(fail=True)
        checks = [
            ("CDN", lambda: checker.check_cdn(client, self.expected, self.release_payload)),
            ("Homebrew", lambda: checker.check_homebrew(client, self.expected)),
            ("Ubuntu PPA", lambda: checker.check_ppa(client, self.expected)),
            ("Fedora Copr", lambda: checker.check_copr(client, self.expected)),
            ("Snap stable", lambda: checker.check_snap(client, self.expected)),
            ("AUR stable", lambda: checker.check_aur(client, self.expected)),
            ("Scoop", lambda: checker.check_scoop(client, self.expected)),
            ("Docker Hub", lambda: checker.check_docker(client, self.expected)),
            ("Maven Central", lambda: checker.check_maven(client, self.expected)),
        ]
        for name, check in checks:
            with self.subTest(channel=name):
                result = checker.run_channel(name, self.expected, check)
                self.assertEqual("unavailable", result.status)


if __name__ == "__main__":
    unittest.main(verbosity=2)
