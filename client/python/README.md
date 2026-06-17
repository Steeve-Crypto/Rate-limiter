# RateFlow Python Client

Thin, zero-dep client for the Rate Limiter Service. Works with any HTTP runtime.

## Install

```bash
cd client/python
pip install -e .
# or simply copy rate_limiter_client/ into your project
```

## Usage

```python
from rate_limiter_client import RateLimiterClient

client = RateLimiterClient("http://localhost:8080")

# Check
resp = client.check("user:123:checkout", max_tokens=50, window_seconds=30, cost=1)
print(resp.allowed, resp.remaining)

# Simulate
print(client.simulate("user:burst", costs=[1,1,10,1]))

# Policies
client.add_policy({"name": "fintech-tx", "pattern": "tx:*", "config": {"algorithm":"token_bucket","max_tokens":20,"window_seconds":10}, "priority": 50})

# Replication / Replay / Cluster
client.replicate("upsert", "feature:new-ui", {"enabled": True})
print(client.cluster_nodes())
```

## OpenAPI Generated Clients

See the root `scripts/generate-clients.sh` and `openapi.yaml`.

Recommended generator:

```bash
npm install -g @openapitools/openapi-generator-cli
# or use docker
```

Generates full-featured clients for Python (using python or python-prior), TypeScript, Go, Java, etc.

The hand-written `RateLimiterClient` above is intentionally small & dependency-free so you can drop it in immediately while using generated clients in larger codebases.

## Fintech example (see UseCase.md)

```python
client = RateLimiterClient(...)
decision = client.check("acct:acme:transfer:USD", max_tokens=5, window_seconds=1, labels={"risk":"high"})
if not decision.allowed:
    # queue or 429 backpressure
    ...
```
