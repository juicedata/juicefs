#!/usr/bin/env python3
"""Generate a static star history SVG for a GitHub repository.

GitHub restricted the stargazers API to a repository's own admins and
collaborators, which broke third-party chart services embedded in README
files. This script runs inside the repository's own CI, where the token is
authorized to read that data, and renders a self-contained SVG committed
back to the repository.
"""

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone

API = "https://api.github.com"
PER_PAGE = 100
# The stargazers API stops paginating past this many entries.
MAX_STARGAZERS = 40000
RETRIES = 5


def request(url, token, accept="application/vnd.github+json"):
    req = urllib.request.Request(url)
    req.add_header("Accept", accept)
    req.add_header("X-GitHub-Api-Version", "2022-11-28")
    req.add_header("User-Agent", "juicefs-star-history")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    # Collecting the full history takes hundreds of requests, so transient
    # network and server errors are expected and retried.
    for attempt in range(RETRIES):
        try:
            with urllib.request.urlopen(req, timeout=60) as resp:
                return json.load(resp)
        except urllib.error.HTTPError as e:
            if e.code not in (429, 500, 502, 503, 504) or attempt == RETRIES - 1:
                raise
        except (urllib.error.URLError, TimeoutError, OSError):
            if attempt == RETRIES - 1:
                raise
        time.sleep(2 ** attempt)


def fetch_total(repo, token):
    return request("%s/repos/%s" % (API, repo), token)["stargazers_count"]


def fetch_stargazers(repo, token):
    """Return sorted starred_at datetimes for every stargazer."""
    accept = "application/vnd.github.v3.star+json"
    dates = []
    page = 1
    while len(dates) < MAX_STARGAZERS:
        url = "%s/repos/%s/stargazers?per_page=%d&page=%d" % (API, repo, PER_PAGE, page)
        try:
            batch = request(url, token, accept)
        except urllib.error.HTTPError as e:
            if e.code in (401, 403, 404):
                detail = e.read().decode("utf-8", "replace")[:200]
                raise SystemExit(
                    "error: cannot read stargazers of %s (HTTP %d).\n"
                    "GitHub limits this API to the repository's admins and "
                    "collaborators; the token needs contents write access.\n%s"
                    % (repo, e.code, detail)
                )
            raise
        if not batch:
            break
        for item in batch:
            starred_at = item.get("starred_at")
            if starred_at:
                dates.append(datetime.strptime(starred_at, "%Y-%m-%dT%H:%M:%SZ"))
        if len(batch) < PER_PAGE:
            break
        page += 1
        if page % 20 == 0:
            sys.stderr.write("  fetched %d stargazers...\n" % len(dates))
            sys.stderr.flush()
    dates.sort()
    return dates


def sample(dates, total, max_points):
    """Reduce the raw timeline to a cumulative series of at most max_points."""
    if not dates:
        return []
    # Stars beyond the pagination limit are missing from the head of the
    # timeline, so offset the running count to keep the final value accurate.
    offset = max(0, total - len(dates))
    step = max(1, len(dates) // max_points)
    points = [(dates[i], offset + i + 1) for i in range(0, len(dates), step)]
    if points[-1][0] != dates[-1]:
        points.append((dates[-1], offset + len(dates)))
    return points


def nice_ceil(value):
    """Round up to a visually clean axis bound."""
    if value <= 0:
        return 1
    magnitude = 10 ** (len(str(int(value))) - 1)
    for factor in (1, 2, 2.5, 5, 10):
        bound = magnitude * factor
        if bound >= value:
            return int(bound)
    return int(magnitude * 10)


def fmt_count(value):
    if value >= 1000:
        text = "%.1f" % (value / 1000.0)
        return text.rstrip("0").rstrip(".") + "k"
    return str(int(value))


def render(points, repo, total):
    width, height = 800, 400
    left, right, top, bottom = 70, 30, 50, 55
    plot_w = width - left - right
    plot_h = height - top - bottom

    t0 = points[0][0].timestamp()
    t1 = points[-1][0].timestamp()
    span = max(1.0, t1 - t0)
    y_max = nice_ceil(total)

    def px(dt):
        return left + (dt.timestamp() - t0) / span * plot_w

    def py(count):
        return top + plot_h - (count / float(y_max)) * plot_h

    parts = [
        '<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" '
        'viewBox="0 0 %d %d" role="img" aria-label="Star history of %s">'
        % (width, height, width, height, repo),
        "<style>"
        ".bg{fill:#ffffff}"
        ".grid{stroke:#e5e7eb;stroke-width:1}"
        ".axis{stroke:#9ca3af;stroke-width:1}"
        ".line{fill:none;stroke:#e05d44;stroke-width:2.5;"
        "stroke-linejoin:round;stroke-linecap:round}"
        ".area{fill:#e05d44;fill-opacity:0.08}"
        ".title{font:600 16px -apple-system,BlinkMacSystemFont,Segoe UI,"
        "Helvetica,Arial,sans-serif;fill:#111827}"
        ".label{font:12px -apple-system,BlinkMacSystemFont,Segoe UI,"
        "Helvetica,Arial,sans-serif;fill:#6b7280}"
        "@media (prefers-color-scheme:dark){"
        ".bg{fill:#0d1117}.grid{stroke:#21262d}.axis{stroke:#484f58}"
        ".title{fill:#e6edf3}.label{fill:#8b949e}}"
        "</style>",
        '<rect class="bg" width="%d" height="%d"/>' % (width, height),
        '<text class="title" x="%d" y="28">Star history of %s</text>'
        % (left, repo),
    ]

    # Horizontal grid lines with value labels.
    ticks = 5
    for i in range(ticks + 1):
        value = y_max * i / float(ticks)
        y = py(value)
        parts.append(
            '<line class="grid" x1="%d" y1="%.1f" x2="%d" y2="%.1f"/>'
            % (left, y, left + plot_w, y)
        )
        parts.append(
            '<text class="label" x="%d" y="%.1f" text-anchor="end">%s</text>'
            % (left - 10, y + 4, fmt_count(value))
        )

    # Year boundaries on the x axis. The first year usually starts mid-year,
    # so it gets a label at the series start instead of a grid line.
    first_year = points[0][0].year
    if datetime(first_year, 1, 1).timestamp() < t0:
        parts.append(
            '<text class="label" x="%d" y="%d" text-anchor="middle">%d</text>'
            % (left, top + plot_h + 20, first_year)
        )
    for year in range(first_year, points[-1][0].year + 1):
        marker = datetime(year, 1, 1)
        if not (t0 <= marker.timestamp() <= t1):
            continue
        x = px(marker)
        parts.append(
            '<line class="grid" x1="%.1f" y1="%d" x2="%.1f" y2="%d"/>'
            % (x, top, x, top + plot_h)
        )
        parts.append(
            '<text class="label" x="%.1f" y="%d" text-anchor="middle">%d</text>'
            % (x, top + plot_h + 20, year)
        )

    coords = ["%.1f,%.1f" % (px(dt), py(count)) for dt, count in points]
    parts.append(
        '<polygon class="area" points="%.1f,%.1f %s %.1f,%.1f"/>'
        % (
            px(points[0][0]),
            top + plot_h,
            " ".join(coords),
            px(points[-1][0]),
            top + plot_h,
        )
    )
    parts.append('<polyline class="line" points="%s"/>' % " ".join(coords))
    parts.append(
        '<line class="axis" x1="%d" y1="%d" x2="%d" y2="%d"/>'
        % (left, top + plot_h, left + plot_w, top + plot_h)
    )

    updated = datetime.now(timezone.utc).strftime("%Y-%m-%d")
    parts.append(
        '<text class="label" x="%d" y="%d">%s stars &#183; updated %s</text>'
        % (left, height - 15, format(total, ","), updated)
    )
    parts.append("</svg>")
    return "\n".join(parts) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", default="juicedata/juicefs")
    parser.add_argument("--output", default="docs/en/images/star-history.svg")
    parser.add_argument("--max-points", type=int, default=300)
    args = parser.parse_args()

    token = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN")
    if not token:
        raise SystemExit("error: GITHUB_TOKEN is required to read stargazers")

    total = fetch_total(args.repo, token)
    dates = fetch_stargazers(args.repo, token)
    if not dates:
        raise SystemExit("error: no stargazer data returned for " + args.repo)

    points = sample(dates, total, args.max_points)
    svg = render(points, args.repo, total)

    directory = os.path.dirname(args.output)
    if directory:
        os.makedirs(directory, exist_ok=True)
    with open(args.output, "w", encoding="utf-8") as f:
        f.write(svg)

    sys.stderr.write(
        "wrote %s (%d stargazers, %d points, total %d)\n"
        % (args.output, len(dates), len(points), total)
    )


if __name__ == "__main__":
    main()
