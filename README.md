# awsfilter

`awsfilter` downloads the latest processed Forward snapshot for a network and writes a corrected zip where AWS interface data is limited to collected-account owners only.

That keeps CCH VM footprint aligned to collected-account compute instead of shared-account interface visibility.

## What it does

1. Calls `GET /api/networks/{networkId}/snapshots/latestProcessed`.
2. Calls `GET /api/snapshots/{snapshotId}` to export the snapshot package.
3. Reads `collect_aws_*,cloud_accounts.json` files from the zip and autodetects enabled collected AWS account IDs.
4. Rewrites `collect_aws_*,cloud_interfaces_json.json` so only interfaces whose `ownerId` is one of those collected account IDs remain.
5. Writes a new zip and prints a JSON summary.

## Build

```bash
make build
```

## Usage

```bash
./bin/awsfilter \
  --host https://fwd.app \
  --username you@example.com \
  --password 'secret' \
  --network-id NETWORK_ID
```

Optional flags:

```bash
./bin/awsfilter \
  --host https://fwd.app \
  --username you@example.com \
  --password 'secret' \
  --network-id NETWORK_ID \
  --output ./filtered.zip \
  --insecure
```

Import the corrected snapshot and delete the source snapshot only after a successful import:

```bash
./bin/awsfilter \
  --host https://fwd.app \
  --username you@example.com \
  --password 'secret' \
  --network-id NETWORK_ID \
  --import \
  --delete-source-snapshot
```

Environment variables are also supported:

```bash
export AWSFILTER_HOST=https://fwd.app
export AWSFILTER_USERNAME=you@example.com
export AWSFILTER_PASSWORD='secret'
export AWSFILTER_NETWORK_ID=NETWORK_ID
./bin/awsfilter
```

Check snapshot state directly from the tool:

```bash
./bin/awsfilter status \
  --host https://fwd.app \
  --username you@example.com \
  --password 'secret' \
  --network-id NETWORK_ID
```

Wait for an imported snapshot to finish processing:

```bash
./bin/awsfilter wait \
  --host https://fwd.app \
  --username you@example.com \
  --password 'secret' \
  --network-id NETWORK_ID \
  --snapshot-id SNAPSHOT_ID
```

Example output:

```json
{
  "host": "https://fwd.app",
  "network_id": "NETWORK_ID",
  "snapshot_id": "SNAPSHOT_ID",
  "output": "/path/to/network-NETWORK_ID-snapshot-SNAPSHOT_ID-collected-compute-only.zip",
  "collected_account_ids": [
    "COLLECTED_ACCOUNT_ID_1",
    "COLLECTED_ACCOUNT_ID_2"
  ],
  "original_interfaces": 12345,
  "kept_interfaces": 2345,
  "removed_interfaces": 10000,
  "files": [
    {
      "name": "collect_aws_1,cloud_interfaces_json.json",
      "original_interfaces": 12345,
      "kept_interfaces": 2345,
      "removed_interfaces": 10000
    }
  ]
}
```

## Notes

- This tool is intentionally raw-API only. It does not depend on `fwd-cli`.
- The snapshot package report workbook is copied through unchanged.
- The filter scope is intentionally narrow: only AWS interface payloads are rewritten.
- `--delete-source-snapshot` is guarded and only works together with `--import`.
- When `--delete-source-snapshot` is set, the tool waits for the imported snapshot to reach `PROCESSED` before deleting the source snapshot.
- `status` and `wait` are raw-API helpers for monitoring imported snapshots without needing separate curl calls.
