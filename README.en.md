# kiro4cpa-plugin

English | [中文](./README.md)

A Kiro provider plugin for [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) (CPA) v8. It loads as a native c-shared plugin, connects Kiro accounts to CPA, and serves Claude Messages and [OI] Chat Completions / Responses.

> Unofficial project, not affiliated with AWS or Kiro. Follow Kiro's terms of service.

## Features

- Credential types: Social (Google / GitHub), IdC (Builder ID / IAM Identity Center), External IdP (Entra ID and similar), API Key
- Refreshes the access token automatically; on an auth failure a request forces one refresh and retries, and the result is written back to the credential file
- Pulls the account's available model list from Kiro on refresh and caches it; falls back to the built-in list when the pull fails
- Streaming / non-streaming, extended thinking, tool use, image input (jpeg / png / gif / webp)
- Reports Kiro usage through CPA's QuotaProvider
- Upstream errors go to CPA by status code: 401 / 402 / 403 / 429 trigger cooling and account switching, 5xx is reported as a retryable 502
- Paces each credential and remembers the monthly limit and suspension Kiro reports, so it stops retrying the same account

The plugin implements only the Claude Messages executor; CPA converts [OI] formats itself.

## Build

Requires Go 1.26 and cgo.

```bash
make test                # unit tests
make build-linux-docker  # cross-compile bin/linux/amd64/kiro4cpa.so with Docker
make build-darwin        # build bin/darwin/arm64/kiro4cpa.dylib locally (needs working Xcode Command Line Tools)
```

`build-linux-docker` cross-compiles with `gcc-x86-64-linux-gnu` inside `golang:1.26-bookworm`; the result requires glibc ≥ 2.34.

## Install

1. Put the built library in CPA's plugin directory (by default `plugins/` under CPA's working directory), in a per-platform subdirectory:

   ```
   plugins/linux/amd64/kiro4cpa.so
   ```

2. Enable the plugin in CPA's `config.yaml`:

   ```yaml
   plugins:
     enabled: true
     configs:
       kiro4cpa:
         enabled: true
         # All of the following are optional; defaults in parentheses
         # default_region: us-east-1   # used when the credential has no apiRegion and no region in profileArn
         # client_version: 1.4.5       # Kiro IDE version reported in the User-Agent
         # os_version: darwin#24.6.0
         # node_version: 22.22.0
         # min_token_interval: 1s
         # max_token_interval: 2s
         # suspend_cooldown: 1h
   ```

3. Restart CPA. `plugin registered plugin_id=kiro4cpa` in the log means it loaded.

## Importing credentials

Each account is one JSON file in CPA's `auth-dir`, with `"type": "kiro4cpa"` at the top level:

```json
{
  "type": "kiro4cpa",
  "authMethod": "social",
  "refreshToken": "<refresh-token>",
  "region": "us-east-1"
}
```

| authMethod | Required fields |
|---|---|
| `social` | `refreshToken` |
| `idc` (also accepts `builder-id`, `iam`) | `refreshToken`, `clientId`, `clientSecret` |
| `external_idp` (also accepts `azuread`, `entra`) | `refreshToken`, `clientId`, `tokenEndpoint` (https) |
| `api_key` | `kiroApiKey` |

Optional fields: `accessToken`, `expiresAt`, `profileArn`, `authRegion`, `apiRegion`, `machineId`, `email`. Without `accessToken`, CPA refreshes once right after loading.

- kiro.rs's `credentials.json` is an array, which CPA cannot read. Store each element as its own file and add the `type` field.
- Refreshed tokens and the model list are written back to the same file. That file is equivalent to an account password: restrict its permissions and never commit it.

## Models

`/v1/models` lists Kiro's own model IDs, for example `claude-sonnet-4.5`, `claude-haiku-4.5`, `auto`. To use other spellings, configure an alias with CPA's `oauth-model-alias` under the channel name `kiro4cpa`:

```yaml
oauth-model-alias:
  kiro4cpa:
    - name: claude-sonnet-4.5
      alias: claude-sonnet-4-5
      fork: true   # keep the original ID as well
```

## Quota

The plugin implements CPA's QuotaProvider, so an account's Kiro quota can be queried through CPA's management API:

```
GET /v0/management/plugins/kiro4cpa/quota?auth_index=<auth-index>
```

Whether a panel shows the quota depends on whether it supports plugin QuotaProviders. The official [Cli-Proxy-API-Management-Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center) does; some third-party panels only know built-in providers and will not show it.

## Request pacing

CPA fails over between credentials on its own, but concurrent requests on one credential still trip Kiro's upstream limits. The plugin paces each credential and remembers the conditions Kiro reports that last longer than one request:

```yaml
plugins:
  configs:
    kiro4cpa:
      min_token_interval: 1s   # minimum gap between two requests of one credential
      max_token_interval: 2s   # upper bound of the gap, and of the queued wait
      suspend_cooldown: 1h     # how long a suspended credential stays out of rotation
```

Behaviour:

- The gap is drawn between `min_token_interval` and `max_token_interval` with ±30% jitter, so parallel instances do not fire together
- Concurrent requests queue up to `max_token_interval`; anything past that is refused with 429 so CPA can fail over instead of waiting
- A 402 (monthly limit) takes the credential out of rotation until the next UTC midnight
- A 403 carrying `SUSPENDED` or `User is not authorized to make this call` takes it out for `suspend_cooldown`
- A credential file with `"disable_cooling": true` opts out of all of the above; CPA's own cooldown still applies

Credential selection, failover and the 429 backoff ladder remain CPA's job.

## Web sign-in

On the panel's sign-in page, clicking the Kiro login opens the plugin's sign-in page `/v0/resource/plugins/kiro4cpa/connect` in a new tab, where you pick a method:

| Method | Description |
|---|---|
| Builder ID | AWS device authorization. Open the AWS page, enter the email, confirm the code |
| Google / GitHub | Open the Kiro sign-in page and choose Google or GitHub. After signing in the browser lands on an unreachable `127.0.0.1:3128` page; paste the full address back into the sign-in page |
| IAM Identity Center | Enter `https://<directory>.awsapps.com/start` and the region, then follow AWS device authorization |
| Import credentials | A Social or IdC refreshToken, or a Kiro API Key |

The panel picks up the result automatically and CPA saves the credential.

The sign-in page talks to CPA's management API and needs the management key. If "Remember password" was ticked when signing into the panel and both are opened from the same address, the page reads the key the panel saved; otherwise it asks for it and does not store it.

Credential files are named `kiro4cpa-<method>-<email>.json`, for example `kiro4cpa-google-user@example.com.json`. When the email is unavailable a hash of the account ID is used, and signing in again with the same account overwrites the file.

## Known limitations

- Social accounts and the Builder ID device flow are the ones actually tested; IdC, External IdP and API Key are implemented to spec
- Released binaries cover linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and windows/amd64; only linux/amd64 and darwin/arm64 have been exercised end to end

## Acknowledgements

Protocol details were informed by these MIT projects; see [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES) for their copyright notices:

- [hank9999/kiro.rs](https://github.com/hank9999/kiro.rs), [ZyphrZero/kiro.rs](https://github.com/ZyphrZero/kiro.rs)
- [xiaokui-dev/cliproxyapi-kiro-plugin](https://github.com/xiaokui-dev/cliproxyapi-kiro-plugin)
- [nguyenha935/kiro-cpa-plugin](https://github.com/nguyenha935/kiro-cpa-plugin)

## License

[MIT](LICENSE)
