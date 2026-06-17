"""Python client for the RateFlow rate limiter service.

Usage:
    from rate_limiter_client import RateLimiterClient
    client = RateLimiterClient("http://localhost:8080")
    resp = client.check(key="user:42:api", max_tokens=100, window_seconds=60)
    print(resp.allowed, resp.remaining)
"""

from __future__ import annotations
import json
from dataclasses import dataclass, asdict
from typing import Any, Dict, List, Optional
import urllib.request
import urllib.error


@dataclass
class CheckRequest:
    key: str
    max_tokens: int = 100
    window_seconds: int = 60
    algorithm: str = "token_bucket"
    cost: int = 1
    labels: Optional[Dict[str, str]] = None


@dataclass
class CheckResponse:
    allowed: bool
    remaining: int
    limit: int
    retry_after_ms: Optional[int] = None
    reset_at: Optional[int] = None
    algorithm: str = ""


@dataclass
class Policy:
    name: str
    pattern: str
    config: Dict[str, Any]
    labels: Optional[Dict[str, Any]] = None
    priority: int = 100


class RateLimiterClient:
    """HTTP client for RateFlow service endpoints."""

    def __init__(self, base_url: str = "http://localhost:8080", timeout: float = 5.0):
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout

    def _request(self, method: str, path: str, body: Any = None) -> Any:
        url = f"{self.base_url}{path}"
        data = None
        headers = {"Content-Type": "application/json", "Accept": "application/json"}
        if body is not None:
            data = json.dumps(body).encode("utf-8")

        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                raw = resp.read().decode("utf-8")
                if not raw:
                    return {}
                return json.loads(raw)
        except urllib.error.HTTPError as e:
            err = e.read().decode("utf-8") if e.fp else str(e)
            raise RuntimeError(f"HTTP {e.code}: {err}") from e
        except urllib.error.URLError as e:
            raise RuntimeError(f"Connection error: {e.reason}") from e

    # Core
    def check(self, key: str, max_tokens: int = 100, window_seconds: int = 60,
              algorithm: str = "token_bucket", cost: int = 1,
              labels: Optional[Dict[str, str]] = None) -> CheckResponse:
        req = CheckRequest(key=key, max_tokens=max_tokens, window_seconds=window_seconds,
                           algorithm=algorithm, cost=cost, labels=labels)
        data = self._request("POST", "/v1/check", asdict(req))
        return CheckResponse(**{k: data.get(k) for k in CheckResponse.__dataclass_fields__ if k in data})

    def visualize(self, key: str, algorithm: str = "token_bucket",
                  max_tokens: int = 100, window_seconds: int = 60,
                  include_history: bool = False) -> Dict[str, Any]:
        qs = f"key={key}&algorithm={algorithm}&max_tokens={max_tokens}&window_seconds={window_seconds}&include_history={str(include_history).lower()}"
        return self._request("GET", f"/v1/visualize?{qs}")

    def simulate(self, key: str, max_tokens: int = 100, window_seconds: int = 60,
                 algorithm: str = "token_bucket", costs: Optional[List[int]] = None) -> Dict[str, Any]:
        if costs is None:
            costs = [1, 1]
        body = {"key": key, "max_tokens": max_tokens, "window_seconds": window_seconds,
                "algorithm": algorithm, "costs": costs}
        return self._request("POST", "/v1/simulate", body)

    # Policies
    def list_policies(self) -> List[Dict[str, Any]]:
        return self._request("GET", "/v1/policies") or []

    def add_policy(self, policy: Policy) -> Dict[str, Any]:
        return self._request("POST", "/v1/policies", asdict(policy))

    # Replication
    def replicate(self, op: str, key: str, value: Any, version: int = 1, node: str = "") -> Dict[str, Any]:
        ev = {"op": op, "key": key, "value": value, "version": version}
        if node:
            ev["node"] = node
        return self._request("POST", "/v1/replicate", ev)

    def get_replicated(self, key: str) -> Dict[str, Any]:
        return self._request("GET", f"/v1/replicated/{key}")

    # Replay / Admin / Cluster
    def replay(self, from_ts: int, to_ts: int, key: str = "") -> Dict[str, Any]:
        return self._request("POST", "/v1/replay", {"from_ts": from_ts, "to_ts": to_ts, "key": key})

    def inspect(self, key: str) -> Dict[str, Any]:
        return self._request("GET", f"/v1/admin/inspect?key={key}")

    def reset(self, key: str) -> Dict[str, Any]:
        return self._request("POST", f"/v1/admin/reset?key={key}")

    def cluster_nodes(self) -> Dict[str, Any]:
        return self._request("GET", "/v1/cluster/nodes")

    def health(self) -> Dict[str, Any]:
        return self._request("GET", "/health")


# Convenience alias for generated client migration
Client = RateLimiterClient
