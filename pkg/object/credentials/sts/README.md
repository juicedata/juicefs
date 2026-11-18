# In-process STS credentials

This package lets a JuiceFS client obtain short-lived object storage
credentials from the cloud STS service, without an extra agent, gateway, or
sidecar. The provider runs inside the `juicefs` process and is wired into the
S3 and OSS backends, so the regular `juicefs mount` command is still the only
process on an edge machine.

## How it works

When `JFS_STS_ENABLE=1`:

1. JuiceFS uses a local *seed* access key (or the cloud SDK's default
   credential chain) to call `sts:AssumeRole`.
2. The returned temporary credentials are cached in memory and refreshed before
   they expire.
3. If STS is temporarily unavailable, the last unexpired credentials are used.
4. If they are already expired and a static access key is present in the volume
   format, JuiceFS falls back to it (during a gradual migration).

The seed key is never written to Redis. It lives in a root-readable file on the
client and should have **only** `sts:AssumeRole` permission.

## Configuration

All options are environment variables (a systemd `EnvironmentFile` works well):

| Variable | Default | Description |
|---|---|---|
| `JFS_STS_ENABLE` | `false` | Enable the in-process STS provider. |
| `JFS_STS_CLOUD` | `aliyun` | `aliyun` or `aws`. |
| `JFS_STS_ROLE_ARN` | | Role to assume. Required when enabled. |
| `JFS_STS_SEED_FILE` | | Local file with the seed access key. |
| `JFS_STS_SEED_AK` / `JFS_STS_SEED_SK` | | Seed key via the environment (testing only; prefer the file). |
| `JFS_STS_ENDPOINT` | cloud default | Custom STS endpoint, e.g. `sts.cn-hangzhou.aliyuncs.com`. |
| `JFS_STS_REGION` | cloud default | Region for the STS endpoint (AWS defaults to `us-east-1`). |
| `JFS_STS_DURATION` | `1h` | Requested credential lifetime (seconds or Go duration). |
| `JFS_STS_SESSION_NAME` | `jfs-<host>-<pid>` | Session name for cloud audit logs. |
| `JFS_STS_POLICY` / `JFS_STS_POLICY_FILE` | | Optional session policy (JSON) to further restrict credentials. |
| `JFS_STS_FALLBACK` | `static` | `static` enables fallback to the AK/SK in the volume format; `none` disables it. |
| `JFS_STS_REFRESH_MARGIN` | `5m` | Refresh credentials this long before expiry. |
| `JFS_STS_TIMEOUT` | `10s` | Timeout for a single STS request. |

The volume format must not contain an access key for STS to be used. Clean it
with:

```bash
juicefs config redis://... \
  --access-key "" --secret-key "" --session-token "" --force
```

Make sure every client has the new binary and a working STS configuration
before doing this; otherwise those clients will fail when they reload the
format.

## Seed file

`/etc/juicefs/seed.ak` (mode `0600`):

```ini
access_key_id = LTAI...
access_key_secret = ...
```

JSON is also accepted:

```json
{"access_key_id": "LTAI...", "access_key_secret": "..."}
```

The seed RAM user only needs:

```json
{
  "Version": "1",
  "Statement": [{
    "Effect": "Allow",
    "Action": ["sts:AssumeRole"],
    "Resource": ["acs:ram::1234567890:role/juicefs-edge"]
  }]
}
```

The target role should be scoped to the bucket/prefix used by the volume and
must not have bucket-administration permissions.

## Example

`/etc/juicefs/sts.env`:

```bash
JFS_STS_ENABLE=1
JFS_STS_ROLE_ARN=acs:ram::1234567890:role/juicefs-edge
JFS_STS_SEED_FILE=/etc/juicefs/seed.ak
JFS_STS_ENDPOINT=sts.cn-hangzhou.aliyuncs.com
JFS_STS_DURATION=1h
JFS_STS_FALLBACK=static
```

systemd:

```ini
[Service]
EnvironmentFile=/etc/juicefs/sts.env
ExecStart=/usr/local/bin/juicefs mount redis://... /mnt/jfs
```

## Gradual migration

1. Deploy the new binary with `JFS_STS_ENABLE=0`; behavior is unchanged.
2. Enable STS on a few canary clients. Keep the static AK in the format as a
   fallback and watch the `fallback` warnings.
3. Roll out to all clients. Once there are no fallbacks, remove the static
   AK/SK from the format and set `JFS_STS_FALLBACK=none`.
4. Disable or rotate the old static AK.

If something goes wrong before step 3, set `JFS_STS_ENABLE=0` (or rely on the
automatic fallback). After step 3, keep a break-glass copy of the old static AK
in a secret manager, not in Redis.

## Limitations

- The Aliyun `credentials-go` provider does not expose the exact expiry of the
  credentials it returns. The provider therefore uses a short (three-minute)
  lease and relies on the SDK to refresh the underlying credentials. This is
  safe but means a long STS outage can cause a fallback earlier than the real
  token expiry.
- The seed file is read when the provider is created. Restart the mount (or
  reload it) after rotating the seed key.
- The provider currently supports the `s3` and `oss` backends. Other backends
  keep using static credentials.
- The static fallback is a migration aid. While it is enabled, the static AK is
  still present in the volume format and Redis; remove it once STS is stable.
- Root on a client machine can read the seed key and the credentials in memory.
  The provider reduces the blast radius and the lifetime of the data
  credentials; it does not protect a compromised host.
