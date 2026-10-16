#!/usr/bin/env python3
"""Read-only P0 probe. Credentials and raw responses never leave this process.

Run locally, or pipe this script into `ssh <existing-alias> python3 -`.
Only the documented account-earnings endpoint and limit parameter are used.
Output contains field types, counts, timestamp formats and a redacted example.
It neither writes device files nor changes a provider's state.
"""
import argparse
import datetime as dt
import json
from pathlib import Path
import plistlib
import re
import urllib.error
import urllib.request

ENDPOINT = "https://api.darkbloom.dev/v1/provider/account-earnings"
MAX_BYTES = 2 << 20


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def shape(value):
    if isinstance(value, dict):
        return {key: shape(val) for key, val in value.items()}
    if isinstance(value, list):
        return {"type": "array", "first": shape(value[0]) if value else None}
    return type(value).__name__


def timestamp(value):
    # The target ships Python 3.9, whose fromisoformat rejects 4/5 fractional
    # digits. The observed wire format is UTC Z with up to six fractional digits.
    return dt.datetime.strptime(value, "%Y-%m-%dT%H:%M:%S.%fZ").replace(tzinfo=dt.timezone.utc)


def redacted_sample(payload):
    """Keep only verified fields, replace all identity and financial values."""
    rows = []
    for i, row in enumerate(payload.get("earnings", [])[:2]):
        rows.append({
            "id": 100 + i if isinstance(row.get("id"), int) else f"record_example_{i}",
            "account_id": "acct_example", "provider_id": "provider_example",
            "provider_key": "provider_key_example", "job_id": f"job_example_{i}",
            "model": "model-example", "amount_micro_usd": 1234 + i,
            "prompt_tokens": 120, "completion_tokens": 45,
            "created_at": f"2026-10-16T01:02:0{3-i}.123456Z",
        })
    return {
        "account_id": "acct_example", "email": "redacted@example.invalid",
        "count": max(2, len(rows)), "recent_count": len(rows),
        "history_limit": payload.get("history_limit"),
        "total_micro_usd": 10000, "total_usd": "0.010000",
        "available_balance_micro_usd": 5000, "available_balance_usd": "0.005000",
        "withdrawable_balance_micro_usd": 4000, "withdrawable_balance_usd": "0.004000",
        "earnings": rows,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--credential-file", type=Path, default=Path.home()/".darkbloom/auth_token")
    args = parser.parse_args()
    try:
        token = args.credential_file.read_text().strip()
        if not token or "\n" in token or "\r" in token:
            raise ValueError("invalid credential")
    except Exception:
        raise SystemExit("Cannot read a valid credential file; no contents were printed")
    version = "unknown"
    try:
        with (Path.home()/".darkbloom/Darkbloom.app/Contents/Info.plist").open("rb") as file:
            version = plistlib.load(file).get("CFBundleShortVersionString", "unknown")
    except (OSError, ValueError):
        pass
    report = {"probed_at": dt.datetime.now(dt.timezone.utc).isoformat(), "darkbloom_version": version, "endpoint": ENDPOINT, "observations": []}
    opener = urllib.request.build_opener(NoRedirect())
    for limit, authenticated in [(50, False), (50, True), (1000, True), (1001, True)]:
        headers = {"Accept": "application/json"}
        if authenticated:
            headers["Authorization"] = "Bearer " + token
        request = urllib.request.Request(ENDPOINT + "?limit=" + str(limit), headers=headers)
        observation = {"requested_limit": limit, "authenticated": authenticated}
        try:
            try:
                response = opener.open(request, timeout=15)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                observation["status"] = response.code
                data = response.read(MAX_BYTES + 1)
            if len(data) > MAX_BYTES:
                raise ValueError("response size")
            payload = json.loads(data)
            observation["shape"] = shape(payload)
            if response.code == 200:
                rows = payload["earnings"]
                times = None
                observation["timestamp_formats"] = sorted({re.sub(r"\d", "#", row["created_at"]) for row in rows})
                try:
                    times = [timestamp(row["created_at"]) for row in rows]
                except ValueError:
                    observation["python_timestamp_parse"] = False
                observation.update({
                    "returned_count": len(rows), "history_limit": payload["history_limit"],
                    "recent_count_matches": payload["recent_count"] == len(rows),
                    "unique_ids": len({str(row["id"]) for row in rows}),
                    "consistent_account": all(row["account_id"] == payload["account_id"] for row in rows),
                    "descending_created_at": times == sorted(times, reverse=True) if times is not None else None,
                    "timestamp_formats": sorted({re.sub(r"\d", "#", row["created_at"]) for row in rows}),
                    "integer_amounts": all(type(row["amount_micro_usd"]) is int for row in rows),
                    "all_amounts_fit_int64": all(-(2**63) <= row["amount_micro_usd"] < 2**63 for row in rows),
                })
                if "redacted_sample" not in report:
                    report["redacted_sample"] = redacted_sample(payload)
        except Exception as error:
            observation["failure_class"] = type(error).__name__  # never include exception text
        report["observations"].append(observation)
    print(json.dumps(report, indent=2))
    return 0 if all("failure_class" not in item for item in report["observations"]) else 1


if __name__ == "__main__":
    raise SystemExit(main())
