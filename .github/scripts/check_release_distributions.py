#!/usr/bin/env python3

import argparse
import dataclasses
import datetime as dt
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET
from collections.abc import Callable
from typing import Any


STABLE_TAG_RE = re.compile(r"^v(?P<version>[0-9]+\.[0-9]+\.[0-9]+)$")
BASE_VERSION_RE = re.compile(r"(?<![0-9])(?P<version>[0-9]+\.[0-9]+\.[0-9]+)(?![0-9])")
CHECKSUM_RE = re.compile(r"^(?P<checksum>[0-9a-fA-F]{64})[ \t]+\*?(?P<filename>\S+)$")
CDN_CLIENT_ARTIFACT_SUFFIXES = (
    "linux-amd64.tar.gz",
    "linux-arm64.tar.gz",
    "darwin-amd64.tar.gz",
    "darwin-arm64.tar.gz",
    "windows-amd64.tar.gz",
    "windows-amd64.zip",
)
CHECK_MODES = ("latest", "target")
UTC = dt.timezone.utc


class CheckError(RuntimeError):
    pass


@dataclasses.dataclass(frozen=True)
class ReleaseInfo:
    release_id: int
    tag: str
    version: str
    published_at: dt.datetime


@dataclasses.dataclass(frozen=True)
class CheckResult:
    channel: str
    status: str
    expected: str
    actual: str
    detail: str = ""


def parse_datetime(value: str) -> dt.datetime:
    if not isinstance(value, str):
        raise CheckError(f"invalid timestamp: {value!r}")
    try:
        parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise CheckError(f"invalid timestamp: {value!r}") from exc
    if parsed.tzinfo is None:
        raise CheckError(f"timestamp must include a timezone: {value!r}")
    return parsed.astimezone(UTC)


def parse_latest_release(payload: dict[str, Any], label: str = "GitHub latest release") -> ReleaseInfo:
    if payload.get("draft"):
        raise CheckError(f"{label} is a draft")
    if payload.get("prerelease"):
        raise CheckError(f"{label} is a prerelease")
    tag = payload.get("tag_name")
    match = STABLE_TAG_RE.fullmatch(tag or "")
    if not match:
        raise CheckError(f"{label} has an invalid stable tag: {tag!r}")
    release_id = payload.get("id")
    if not isinstance(release_id, int):
        raise CheckError(f"{label} has no numeric id")
    return ReleaseInfo(
        release_id=release_id,
        tag=tag,
        version=match.group("version"),
        published_at=parse_datetime(payload.get("published_at")),
    )


def extract_base_version(value: str) -> str | None:
    match = BASE_VERSION_RE.search(value or "")
    return match.group("version") if match else None


def version_status(expected: str, actual: str) -> str:
    return "current" if extract_base_version(actual) == expected else "stale"


def grace_expired(
    published_at: dt.datetime,
    grace_hours: int,
    now: dt.datetime | None = None,
) -> bool:
    if grace_hours < 0:
        raise CheckError("grace hours must not be negative")
    current = (now or dt.datetime.now(UTC)).astimezone(UTC)
    return current >= published_at + dt.timedelta(hours=grace_hours)


class HttpClient:
    def __init__(self, timeout: int = 20, retries: int = 3):
        self.timeout = timeout
        self.retries = retries
        self.default_headers = {"User-Agent": "juicefs-release-distribution-check"}

    def request(
        self,
        url: str,
        *,
        headers: dict[str, str] | None = None,
        method: str = "GET",
        read_limit: int | None = None,
    ) -> bytes:
        if read_limit is not None and read_limit < 0:
            raise CheckError("read limit must not be negative")
        request_headers = {**self.default_headers, **(headers or {})}
        request = urllib.request.Request(url, headers=request_headers, method=method)
        last_error: Exception | None = None
        for attempt in range(self.retries):
            try:
                with urllib.request.urlopen(request, timeout=self.timeout) as response:
                    content = response.read() if read_limit is None else response.read(read_limit)
                    if read_limit and not content:
                        raise CheckError(f"empty response from {url}")
                    return content
            except (OSError, urllib.error.URLError, urllib.error.HTTPError, CheckError) as exc:
                last_error = exc
                if attempt + 1 < self.retries:
                    time.sleep(2**attempt)
        raise CheckError(f"request failed after {self.retries} attempts: {url}: {last_error}")

    def get_json(self, url: str, headers: dict[str, str] | None = None) -> Any:
        try:
            return json.loads(self.request(url, headers=headers))
        except (json.JSONDecodeError, UnicodeDecodeError) as exc:
            raise CheckError(f"invalid JSON response from {url}") from exc

    def get_text(self, url: str, headers: dict[str, str] | None = None) -> str:
        try:
            return self.request(url, headers=headers).decode("utf-8")
        except UnicodeDecodeError as exc:
            raise CheckError(f"invalid UTF-8 response from {url}") from exc

    def probe(self, url: str) -> None:
        try:
            self.request(url, method="HEAD")
        except CheckError:
            self.request(url, headers={"Range": "bytes=0-0"})

    def probe_download(self, url: str) -> None:
        self.request(url, headers={"Range": "bytes=0-0"}, read_limit=1)


def github_latest_release(
    client: HttpClient,
    repository: str,
    token: str = "",
) -> tuple[ReleaseInfo, dict[str, Any]]:
    return github_release_request(client, repository, "latest", "GitHub latest release", token)


def github_release_by_tag(
    client: HttpClient,
    repository: str,
    tag: str,
    token: str = "",
) -> tuple[ReleaseInfo, dict[str, Any]]:
    tag_path = urllib.parse.quote(tag, safe="")
    return github_release_request(client, repository, f"tags/{tag_path}", f"GitHub release {tag}", token)


def github_target_release(
    client: HttpClient,
    repository: str,
    trigger_tag: str = "",
    token: str = "",
) -> tuple[ReleaseInfo, dict[str, Any]]:
    if trigger_tag:
        return github_release_by_tag(client, repository, trigger_tag, token)
    return github_latest_release(client, repository, token)


def github_release_request(
    client: HttpClient,
    repository: str,
    path: str,
    label: str,
    token: str = "",
) -> tuple[ReleaseInfo, dict[str, Any]]:
    url = f"https://api.github.com/repos/{repository}/releases/{path}"
    headers = {
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
    }
    if token:
        headers["Authorization"] = f"Bearer {token}"
    payload = client.get_json(
        url,
        headers=headers,
    )
    if not isinstance(payload, dict):
        raise CheckError(f"{label} response is not an object")
    return parse_latest_release(payload, label), payload


def run_channel(
    channel: str,
    expected: str,
    checker: Callable[[], CheckResult],
) -> CheckResult:
    try:
        return checker()
    except CheckError as exc:
        return CheckResult(channel, "unavailable", expected, "unknown", str(exc))
    except Exception as exc:  # Keep one broken parser from hiding all other channels.
        return CheckResult(channel, "invalid", expected, "unknown", str(exc))


def should_fail(results: list[CheckResult], expired: bool) -> bool:
    if any(result.status == "invalid" for result in results):
        return True
    return expired and any(result.status in {"stale", "unavailable"} for result in results)


def append_github_output(path: str, values: dict[str, str]) -> None:
    if not path:
        return
    with open(path, "a", encoding="utf-8") as output:
        for key, value in values.items():
            if "\n" in value:
                raise CheckError(f"GitHub output {key} must be a single line")
            output.write(f"{key}={value}\n")


def append_summary(path: str, content: str) -> None:
    if not path:
        return
    with open(path, "a", encoding="utf-8") as summary:
        summary.write(content.rstrip() + "\n")


def result_icon(result: CheckResult, expired: bool) -> str:
    if result.status == "current":
        return "✅"
    if result.status == "invalid" or expired:
        return "❌"
    return "⚠️"


def render_results_summary(
    release: ReleaseInfo,
    trigger_tag: str,
    check_mode: str,
    grace_hours: int,
    expired: bool,
    results: list[CheckResult],
) -> str:
    trigger = trigger_tag or "schedule/manual"
    lines = [
        "## JuiceFS release distribution check",
        "",
        f"- Trigger release: `{trigger}`",
        f"- Target release: `{release.tag}` (GitHub release `{release.release_id}`)",
        f"- Check mode: `{check_mode}`",
        f"- Published at: `{release.published_at.isoformat()}`",
        f"- Grace period: `{grace_hours}h` ({'expired' if expired else 'active'})",
        "",
        "| Channel | Status | Expected | Actual | Detail |",
        "| --- | --- | --- | --- | --- |",
    ]
    for result in results:
        detail = result.detail.replace("|", "\\|").replace("\n", " ")
        lines.append(
            f"| {result.channel} | {result_icon(result, expired)} {result.status} "
            f"| `{result.expected}` | `{result.actual}` | {detail} |"
        )
    return "\n".join(lines) + "\n"


def aggregate_versions(
    channel: str,
    expected: str,
    versions: dict[str, str],
    detail: str = "",
) -> CheckResult:
    if not versions:
        return CheckResult(channel, "invalid", expected, "unknown", "no stable packages found")
    mismatches = {
        name: version
        for name, version in versions.items()
        if extract_base_version(version) != expected
    }
    actual = ", ".join(f"{name}={version}" for name, version in sorted(versions.items()))
    if len(actual) > 300:
        actual = f"{len(versions) - len(mismatches)}/{len(versions)} current"
    if mismatches:
        mismatch_detail = ", ".join(
            f"{name}={version}" for name, version in sorted(mismatches.items())
        )
        detail = "; ".join(filter(None, [detail, f"mismatches: {mismatch_detail}"]))
    return CheckResult(
        channel,
        "stale" if mismatches else "current",
        expected,
        actual,
        detail,
    )


def parse_checksums(checksum_text: str) -> tuple[dict[str, str], list[int]]:
    checksums: dict[str, str] = {}
    invalid_lines: list[int] = []
    for line_number, raw_line in enumerate(checksum_text.splitlines(), start=1):
        line = raw_line.strip()
        if not line:
            continue
        match = CHECKSUM_RE.fullmatch(line)
        if not match or match.group("filename") in checksums:
            invalid_lines.append(line_number)
            continue
        checksums[match.group("filename")] = match.group("checksum").lower()
    return checksums, invalid_lines


def required_client_assets(expected: str) -> list[str]:
    return [f"juicefs-{expected}-{suffix}" for suffix in CDN_CLIENT_ARTIFACT_SUFFIXES]


def release_asset_names(release_payload: dict[str, Any]) -> set[str]:
    assets = release_payload.get("assets")
    if not isinstance(assets, list):
        raise CheckError("release assets are missing")
    return {
        asset["name"]
        for asset in assets
        if isinstance(asset, dict) and isinstance(asset.get("name"), str)
    }


def check_release_assets(expected: str, release_payload: dict[str, Any]) -> CheckResult:
    try:
        release_assets = release_asset_names(release_payload)
    except CheckError as exc:
        return CheckResult("GitHub release assets", "invalid", expected, "unknown", str(exc))
    hadoop_asset = f"juicefs-hadoop-{expected}.jar"
    required_assets = ["checksums.txt"] + required_client_assets(expected) + [hadoop_asset]
    missing_assets = [name for name in required_assets if name not in release_assets]
    unexpected_hadoop_assets = sorted(
        name
        for name in release_assets
        if name.startswith("juicefs-hadoop-") and name.endswith(".jar") and name != hadoop_asset
    )
    details: list[str] = []
    if missing_assets:
        details.append("missing release assets: " + ", ".join(missing_assets))
    if unexpected_hadoop_assets:
        details.append("unexpected Hadoop SDK assets: " + ", ".join(unexpected_hadoop_assets))
    return CheckResult(
        "GitHub release assets",
        "stale" if missing_assets else "current",
        expected,
        f"{len(required_assets) - len(missing_assets)}/{len(required_assets)} present",
        "; ".join(details),
    )


def check_cdn(
    client: HttpClient,
    expected: str,
    release_payload: dict[str, Any],
) -> CheckResult:
    base_url = "https://d.juicefs.com/juicefs/releases"
    latest = client.get_text(f"{base_url}/latest-version.txt").strip()
    checksum_text = client.get_text(f"{base_url}/download/v{expected}/checksums.txt")
    required_assets = required_client_assets(expected)
    try:
        release_assets = release_asset_names(release_payload)
    except CheckError as exc:
        return CheckResult("CDN", "invalid", expected, latest or "unknown", str(exc))
    missing_release_assets = [name for name in required_assets if name not in release_assets]
    return check_cdn_artifacts_from_checksums(
        client,
        expected,
        required_assets,
        checksum_text,
        "CDN",
        latest or "unknown",
        missing_release_assets,
    )


def check_cdn_artifacts(client: HttpClient, expected: str) -> CheckResult:
    base_url = "https://d.juicefs.com/juicefs/releases"
    checksum_text = client.get_text(f"{base_url}/download/v{expected}/checksums.txt")
    required_assets = required_client_assets(expected)
    return check_cdn_artifacts_from_checksums(
        client,
        expected,
        required_assets,
        checksum_text,
        "CDN artifacts",
        expected,
        [],
    )


def check_cdn_artifacts_from_checksums(
    client: HttpClient,
    expected: str,
    required_assets: list[str],
    checksum_text: str,
    channel: str,
    actual: str,
    missing_release_assets: list[str],
) -> CheckResult:
    base_url = "https://d.juicefs.com/juicefs/releases"
    checksums, invalid_checksum_lines = parse_checksums(checksum_text)
    missing_checksums = [name for name in required_assets if name not in checksums]
    download_failures: list[str] = []
    for name in required_assets:
        try:
            client.probe_download(f"{base_url}/download/v{expected}/{name}")
        except CheckError:
            download_failures.append(name)
    passed_downloads = len(required_assets) - len(download_failures)
    details = [f"{passed_downloads}/{len(required_assets)} ranged GET download probes passed"]
    if download_failures:
        details.append("failed download probes: " + ", ".join(download_failures))
    if missing_release_assets:
        details.append("missing release assets: " + ", ".join(missing_release_assets))
    if missing_checksums:
        details.append("missing or invalid checksums: " + ", ".join(missing_checksums))
    if invalid_checksum_lines:
        lines = ", ".join(str(line) for line in invalid_checksum_lines)
        details.append(f"invalid checksum lines: {lines}")
    if download_failures:
        status = "unavailable"
    elif (
        actual == expected
        and not missing_release_assets
        and not missing_checksums
        and not invalid_checksum_lines
    ):
        status = "current"
    else:
        status = "stale"
    return CheckResult(channel, status, expected, actual, "; ".join(details))


def check_homebrew(client: HttpClient, expected: str) -> CheckResult:
    payload = client.get_json("https://formulae.brew.sh/api/formula/juicefs.json")
    try:
        actual = payload["versions"]["stable"]
    except (KeyError, TypeError) as exc:
        raise CheckError("Homebrew formula has no stable version") from exc
    return CheckResult("Homebrew", version_status(expected, actual), expected, actual)


def check_ppa(client: HttpClient, expected: str) -> CheckResult:
    versions: dict[str, str] = {}
    supported_series: dict[str, bool] = {}
    for ppa in ("ppa", "arm64"):
        url = (
            f"https://api.launchpad.net/1.0/~juicefs/+archive/ubuntu/{ppa}"
            "?ws.op=getPublishedSources&exact_match=true&source_name=juicefs"
            "&status=Published&order_by_date=true"
        )
        payload = client.get_json(url)
        entries = payload.get("entries") if isinstance(payload, dict) else None
        if not isinstance(entries, list):
            raise CheckError(f"Launchpad {ppa} response has no entries")
        latest_by_series: dict[str, dict[str, Any]] = {}
        for entry in entries:
            if not isinstance(entry, dict):
                continue
            series_url = entry.get("distro_series_link")
            if not isinstance(series_url, str):
                continue
            latest_by_series.setdefault(series_url, entry)
        for series_url, entry in latest_by_series.items():
            if series_url not in supported_series:
                series = client.get_json(series_url)
                supported_series[series_url] = (
                    bool(series.get("supported")) if isinstance(series, dict) else False
                )
            if not supported_series[series_url]:
                continue
            series_name = series_url.rstrip("/").rsplit("/", 1)[-1]
            version = entry.get("source_package_version")
            if not isinstance(version, str):
                raise CheckError(f"Launchpad {ppa}/{series_name} has no source version")
            versions[f"{ppa}/{series_name}"] = version
    return aggregate_versions("Ubuntu PPA", expected, versions, "supported Ubuntu series only")


def check_copr(client: HttpClient, expected: str) -> CheckResult:
    payload = client.get_json(
        "https://copr.fedorainfracloud.org/api_3/monitor?ownername=juicedata&projectname=juicefs"
    )
    packages = payload.get("packages") if isinstance(payload, dict) else None
    package = next(
        (item for item in packages or [] if isinstance(item, dict) and item.get("name") == "juicefs"),
        None,
    )
    if not package or not isinstance(package.get("chroots"), dict):
        raise CheckError("Copr monitor has no juicefs chroots")
    versions: dict[str, str] = {}
    failed: list[str] = []
    for name, build in package["chroots"].items():
        if not isinstance(build, dict):
            failed.append(f"{name}=invalid")
            continue
        if build.get("state") != "succeeded":
            failed.append(f"{name}={build.get('state', 'unknown')}")
        versions[name] = str(build.get("pkg_version", "unknown"))
    result = aggregate_versions("Fedora Copr", expected, versions)
    if failed:
        return dataclasses.replace(
            result,
            status="stale",
            detail="failed chroots: " + ", ".join(sorted(failed)),
        )
    return result


def check_snap(client: HttpClient, expected: str) -> CheckResult:
    payload = client.get_json(
        "https://api.snapcraft.io/v2/snaps/info/juicefs",
        headers={"Snap-Device-Series": "16"},
    )
    channel_map = payload.get("channel-map") if isinstance(payload, dict) else None
    versions: dict[str, str] = {}
    for item in channel_map or []:
        if not isinstance(item, dict) or not isinstance(item.get("channel"), dict):
            continue
        channel = item["channel"]
        if channel.get("track") != "latest" or channel.get("risk") != "stable":
            continue
        architecture = str(channel.get("architecture", "unknown"))
        versions[architecture] = str(item.get("version", "unknown"))
    return aggregate_versions("Snap stable", expected, versions)


def check_aur(client: HttpClient, expected: str) -> CheckResult:
    payload = client.get_json(
        "https://aur.archlinux.org/rpc/v5/info?arg[]=juicefs&arg[]=juicefs-bin"
    )
    results = payload.get("results") if isinstance(payload, dict) else None
    versions = {
        item["Name"]: item["Version"]
        for item in results or []
        if isinstance(item, dict)
        and item.get("Name") in {"juicefs", "juicefs-bin"}
        and isinstance(item.get("Version"), str)
    }
    missing = {"juicefs", "juicefs-bin"} - set(versions)
    if missing:
        return CheckResult("AUR stable", "invalid", expected, "unknown", "missing: " + ", ".join(sorted(missing)))
    return aggregate_versions("AUR stable", expected, versions, "juicefs-git excluded")


def check_scoop(client: HttpClient, expected: str) -> CheckResult:
    payload = client.get_json(
        "https://raw.githubusercontent.com/ScoopInstaller/Main/master/bucket/juicefs.json"
    )
    if not isinstance(payload, dict):
        raise CheckError("Scoop manifest is not an object")
    actual = str(payload.get("version", "unknown"))
    architecture = payload.get("architecture")
    entry = architecture.get("64bit") if isinstance(architecture, dict) else None
    url = entry.get("url") if isinstance(entry, dict) else None
    checksum = entry.get("hash") if isinstance(entry, dict) else None
    if not isinstance(url, str) or not isinstance(checksum, str) or not checksum:
        return CheckResult("Scoop", "invalid", expected, actual, "64-bit URL or hash is missing")
    status = version_status(expected, actual)
    if f"/v{expected}/" not in url:
        status = "stale"
    client.probe(url)
    return CheckResult("Scoop", status, expected, actual, "manifest URL and hash present")


def check_docker(client: HttpClient, expected: str) -> CheckResult:
    tags = [f"ce-v{expected}", "latest"]
    states: dict[str, str] = {}
    for tag in tags:
        payload = client.get_json(
            f"https://hub.docker.com/v2/repositories/juicedata/mount/tags/{tag}"
        )
        state = payload.get("tag_status") if isinstance(payload, dict) else None
        states[tag] = str(state or "unknown")
    stale = {tag: state for tag, state in states.items() if state != "active"}
    return CheckResult(
        "Docker Hub",
        "stale" if stale else "current",
        expected,
        ", ".join(f"{tag}={state}" for tag, state in states.items()),
        "binary versions are checked by docker smoke tests",
    )


def check_docker_artifact(client: HttpClient, expected: str) -> CheckResult:
    tag = f"ce-v{expected}"
    payload = client.get_json(
        f"https://hub.docker.com/v2/repositories/juicedata/mount/tags/{tag}"
    )
    state = payload.get("tag_status") if isinstance(payload, dict) else None
    actual = f"{tag}={state or 'unknown'}"
    return CheckResult(
        "Docker Hub versioned image",
        "current" if state == "active" else "stale",
        expected,
        actual,
    )


def read_maven_metadata(client: HttpClient) -> tuple[str, str, set[str]]:
    base_url = "https://repo1.maven.org/maven2/io/juicefs/juicefs-hadoop"
    metadata = client.get_text(f"{base_url}/maven-metadata.xml")
    try:
        root = ET.fromstring(metadata)
    except ET.ParseError as exc:
        raise CheckError("Maven metadata is invalid XML") from exc
    latest = root.findtext("./versioning/latest") or "unknown"
    release = root.findtext("./versioning/release") or "unknown"
    versions = {item.text for item in root.findall("./versioning/versions/version") if item.text}
    return latest, release, versions


def check_maven(client: HttpClient, expected: str) -> CheckResult:
    base_url = "https://repo1.maven.org/maven2/io/juicefs/juicefs-hadoop"
    latest, release, versions = read_maven_metadata(client)
    status = "current" if latest == expected and release == expected and expected in versions else "stale"
    if expected in versions:
        client.probe(f"{base_url}/{expected}/juicefs-hadoop-{expected}.pom")
        client.probe(f"{base_url}/{expected}/juicefs-hadoop-{expected}.jar")
    return CheckResult(
        "Maven Central",
        status,
        expected,
        f"latest={latest}, release={release}",
        "POM and JAR checked" if expected in versions else "target version is absent",
    )


def check_maven_artifact(client: HttpClient, expected: str) -> CheckResult:
    base_url = "https://repo1.maven.org/maven2/io/juicefs/juicefs-hadoop"
    latest, release, versions = read_maven_metadata(client)
    if expected in versions:
        client.probe(f"{base_url}/{expected}/juicefs-hadoop-{expected}.pom")
        client.probe(f"{base_url}/{expected}/juicefs-hadoop-{expected}.jar")
        status = "current"
        detail = "POM and JAR checked"
    else:
        status = "stale"
        detail = "target version is absent"
    return CheckResult(
        "Maven Central artifact",
        status,
        expected,
        f"latest={latest}, release={release}",
        detail,
    )


def perform_checks(
    client: HttpClient,
    release: ReleaseInfo,
    release_payload: dict[str, Any],
    check_mode: str = "latest",
) -> list[CheckResult]:
    expected = release.version
    if check_mode == "target":
        checkers: list[tuple[str, Callable[[], CheckResult]]] = [
            ("GitHub release assets", lambda: check_release_assets(expected, release_payload)),
            ("CDN artifacts", lambda: check_cdn_artifacts(client, expected)),
            ("Docker Hub versioned image", lambda: check_docker_artifact(client, expected)),
            ("Maven Central artifact", lambda: check_maven_artifact(client, expected)),
        ]
        return [run_channel(name, expected, checker) for name, checker in checkers]
    if check_mode != "latest":
        raise CheckError(f"invalid check mode: {check_mode}")
    checkers: list[tuple[str, Callable[[], CheckResult]]] = [
        ("CDN", lambda: check_cdn(client, expected, release_payload)),
        ("Homebrew", lambda: check_homebrew(client, expected)),
        ("Ubuntu PPA", lambda: check_ppa(client, expected)),
        ("Fedora Copr", lambda: check_copr(client, expected)),
        ("Snap stable", lambda: check_snap(client, expected)),
        ("AUR stable", lambda: check_aur(client, expected)),
        ("Scoop", lambda: check_scoop(client, expected)),
        ("Docker Hub", lambda: check_docker(client, expected)),
        ("Maven Central", lambda: check_maven(client, expected)),
    ]
    return [run_channel(name, expected, checker) for name, checker in checkers]


def check_command(args: argparse.Namespace) -> int:
    client = HttpClient()
    try:
        release, release_payload = github_target_release(
            client,
            args.repository,
            args.trigger_tag,
            token=os.environ.get("GITHUB_TOKEN", ""),
        )
    except CheckError as exc:
        append_summary(args.summary, f"## JuiceFS release distribution check\n\n❌ {exc}")
        print(exc, file=sys.stderr)
        return 1
    expired = grace_expired(release.published_at, args.grace_hours)
    append_github_output(
        args.github_output,
        {
            "target_version": release.version,
            "target_tag": release.tag,
            "release_id": str(release.release_id),
            "published_at": release.published_at.isoformat(),
            "grace_expired": str(expired).lower(),
            "check_mode": args.check_mode,
        },
    )
    results = perform_checks(client, release, release_payload, args.check_mode)
    summary = render_results_summary(
        release,
        args.trigger_tag,
        args.check_mode,
        args.grace_hours,
        expired,
        results,
    )
    append_summary(args.summary, summary)
    print(summary)
    if args.json_output:
        with open(args.json_output, "w", encoding="utf-8") as output:
            json.dump(
                {
                    "release": dataclasses.asdict(release),
                    "trigger_tag": args.trigger_tag,
                    "check_mode": args.check_mode,
                    "grace_expired": expired,
                    "results": [dataclasses.asdict(result) for result in results],
                },
                output,
                default=str,
                indent=2,
            )
    return 1 if should_fail(results, expired) else 0


def resolve_command(args: argparse.Namespace) -> int:
    client = HttpClient()
    try:
        release, _ = github_target_release(
            client,
            args.repository,
            args.trigger_tag,
            token=os.environ.get("GITHUB_TOKEN", ""),
        )
    except CheckError as exc:
        append_summary(args.summary, f"## JuiceFS target release\n\n❌ {exc}")
        print(exc, file=sys.stderr)
        return 1
    append_github_output(
        args.github_output,
        {
            "target_version": release.version,
            "target_tag": release.tag,
            "release_id": str(release.release_id),
            "published_at": release.published_at.isoformat(),
        },
    )
    trigger = args.trigger_tag or "schedule/manual"
    append_summary(
        args.summary,
        "\n".join(
            [
                "## JuiceFS target release",
                "",
                f"- Trigger release: `{trigger}`",
                f"- Target release: `{release.tag}` (GitHub release `{release.release_id}`)",
            ]
        ),
    )
    print(f"trigger={trigger}, target={release.tag}, release_id={release.release_id}")
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Check JuiceFS release distribution versions")
    subparsers = parser.add_subparsers(dest="command", required=True)

    check = subparsers.add_parser("check", help="check all official stable distribution channels")
    check.add_argument("--repository", default=os.environ.get("GITHUB_REPOSITORY", "juicedata/juicefs"))
    check.add_argument("--trigger-tag", default="")
    check.add_argument("--check-mode", choices=CHECK_MODES, default="latest")
    check.add_argument("--grace-hours", type=int, default=24)
    check.add_argument("--github-output", default=os.environ.get("GITHUB_OUTPUT", ""))
    check.add_argument("--summary", default=os.environ.get("GITHUB_STEP_SUMMARY", ""))
    check.add_argument("--json-output", default="")

    resolve = subparsers.add_parser("resolve", help="resolve GitHub's explicitly marked latest release")
    resolve.add_argument("--repository", default=os.environ.get("GITHUB_REPOSITORY", "juicedata/juicefs"))
    resolve.add_argument("--trigger-tag", default="")
    resolve.add_argument("--github-output", default=os.environ.get("GITHUB_OUTPUT", ""))
    resolve.add_argument("--summary", default=os.environ.get("GITHUB_STEP_SUMMARY", ""))

    verify = subparsers.add_parser("verify-version", help="verify one installed binary version")
    verify.add_argument("--channel", required=True)
    verify.add_argument("--expected", required=True)
    verify.add_argument("--actual-file", required=True)
    verify.add_argument("--command-status", type=int, default=0)
    verify.add_argument("--published-at", required=True)
    verify.add_argument("--grace-hours", type=int, default=24)
    verify.add_argument("--summary", default=os.environ.get("GITHUB_STEP_SUMMARY", ""))

    return parser


def verify_version_command(args: argparse.Namespace) -> int:
    published_at = parse_datetime(args.published_at)
    expired = grace_expired(published_at, args.grace_hours)
    try:
        with open(args.actual_file, encoding="utf-8", errors="replace") as actual_file:
            actual_output = actual_file.read().strip()
    except OSError as exc:
        actual_output = str(exc)
        args.command_status = args.command_status or 1
    if args.command_status:
        result = CheckResult(
            args.channel,
            "unavailable",
            args.expected,
            "unknown",
            f"command exited with {args.command_status}: {actual_output[-500:]}",
        )
    else:
        actual_version = extract_base_version(actual_output) or "unknown"
        status = version_status(args.expected, actual_output)
        result = CheckResult(args.channel, status, args.expected, actual_version)
    icon = result_icon(result, expired)
    append_summary(
        args.summary,
        "\n".join(
            [
                f"## {args.channel}",
                "",
                "| Status | Expected | Actual | Detail |",
                "| --- | --- | --- | --- |",
                f"| {icon} {result.status} | `{result.expected}` | `{result.actual}` | {result.detail} |",
            ]
        ),
    )
    return 1 if should_fail([result], expired) else 0


def main() -> int:
    args = build_parser().parse_args()
    if args.command == "check":
        return check_command(args)
    if args.command == "resolve":
        return resolve_command(args)
    if args.command == "verify-version":
        return verify_version_command(args)
    raise AssertionError(f"unhandled command: {args.command}")


if __name__ == "__main__":
    sys.exit(main())
