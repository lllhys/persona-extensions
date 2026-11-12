#!/usr/bin/env python3
"""Read exact cumulative GHCR downloads from public GitHub package detail pages.

The documented Packages REST API does not expose download counts. Never use a
rounded list-page value, a release asset count, or zero on an HTTP/parser failure.
"""
import concurrent.futures
import datetime
import html.parser
import re
import time
import urllib.error
import urllib.parse
import urllib.request

MAX_PAGE_BYTES = 4 * 1024 * 1024


class DownloadParser(html.parser.HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.span = None
        self.after_label = False
        self.values = []

    def handle_starttag(self, tag, attrs):
        if tag == "span":
            self.span = []
        elif tag == "h3" and self.after_label:
            title = dict(attrs).get("title", "")
            if not re.fullmatch(r"[0-9]+|[0-9]{1,3}(?:,[0-9]{3})+", title):
                raise ValueError("GitHub total downloads is not an exact integer")
            self.values.append(int(title.replace(",", "")))
            self.after_label = False

    def handle_data(self, data):
        if self.span is not None:
            self.span.append(data)

    def handle_endtag(self, tag):
        if tag == "span" and self.span is not None:
            if "".join(self.span).strip() == "Total downloads":
                self.after_label = True
            self.span = None


def parse_downloads(text):
    parser = DownloadParser()
    parser.feed(text)
    parser.close()
    if len(parser.values) != 1:
        raise ValueError("GitHub package page must have exactly one Total downloads counter")
    return parser.values[0]


def package_url(extension_id):
    if not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", extension_id):
        raise ValueError("unsafe extension id for downloads")
    return "https://github.com/orgs/nwverse/packages/container/package/" + urllib.parse.quote(
        "persona-extensions/" + extension_id, safe="")


def read_downloads(extension_id):
    url = package_url(extension_id)
    request = urllib.request.Request(url, headers={"User-Agent": "Persona-Extensions-Catalog/1.0"})
    for attempt in range(3):
        try:
            with urllib.request.urlopen(request, timeout=45) as response:
                final = urllib.parse.urlsplit(response.url)
                if response.status != 200 or final.scheme != "https" or final.netloc != "github.com":
                    raise ValueError("GitHub package statistics response identity mismatch")
                data = response.read(MAX_PAGE_BYTES + 1)
                if len(data) > MAX_PAGE_BYTES:
                    raise ValueError("GitHub package statistics page exceeds size limit")
                return parse_downloads(data.decode("utf-8"))
        except urllib.error.HTTPError as error:
            if error.code not in (429, 500, 502, 503, 504) or attempt == 2:
                raise ValueError("GitHub package downloads unavailable for " + extension_id) from None
        except (urllib.error.URLError, TimeoutError, OSError):
            if attempt == 2:
                raise ValueError("GitHub package downloads unavailable for " + extension_id) from None
        time.sleep(2 ** attempt)
    raise ValueError("GitHub package downloads unavailable for " + extension_id)


def snapshot(extension_ids, reader=read_downloads):
    ids = sorted(extension_ids)
    if len(set(ids)) != len(ids):
        raise ValueError("duplicate extension ids in downloads snapshot")
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        counts = dict(zip(ids, pool.map(reader, ids)))
    if any(type(value) is not int or value < 0 for value in counts.values()):
        raise ValueError("downloads must be nonnegative integers")
    timestamp = datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")
    return {"downloads_updated_at": timestamp, "downloads": counts}


def apply(body, metadata):
    entries = body["extensions"]
    counts = metadata["downloads"]
    if set(counts) != {entry["manifest"]["id"] for entry in entries}:
        raise ValueError("downloads snapshot does not cover the complete catalog")
    if any(type(value) is not int or value < 0 for value in counts.values()):
        raise ValueError("downloads must be nonnegative integers")
    for entry in entries:
        entry["downloads"] = counts[entry["manifest"]["id"]]
    entries.sort(key=lambda entry: (-entry["downloads"], entry["manifest"]["id"]))
    body["downloads_updated_at"] = metadata["downloads_updated_at"]
    return body
