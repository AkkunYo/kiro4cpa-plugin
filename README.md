# kiro4cpa-plugin

[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)（CPA）v8 的 Kiro provider 插件。以原生 c-shared 插件的形式加载，把 Kiro 账号接入 CPA，对外提供 Claude Messages、OpenAI Chat Completions / Responses 接口。

> 非官方项目，与 AWS / Kiro 无关联。请遵守 Kiro 的服务条款。

## 功能

- 凭据类型：Social（Google / GitHub）、IdC（Builder ID / IAM Identity Center）、External IdP（Entra ID 等）、API Key
- 自动刷新 access token；请求遇到 token 失效时强制刷新一次并重试，刷新结果写回凭据文件
- 刷新时从 Kiro 拉取账号可用的模型列表并缓存；拉取失败时使用内置列表
- 流式 / 非流式、extended thinking、tool use、图片输入（jpeg / png / gif / webp）
- 通过 CPA QuotaProvider 查询 Kiro 用量额度
- 上游错误按状态码交给 CPA：401 / 402 / 403 / 429 触发冷却和换号，5xx 报为可重试的 502

插件只实现 Claude Messages 执行器，OpenAI 格式由 CPA 自行转换。

## 构建

需要 Go 1.26 和 cgo。

```bash
make test                # 单元测试
make build-linux-docker  # 用 Docker 交叉编译 bin/linux/amd64/kiro4cpa.so
make build-darwin        # 本机编译 bin/darwin/arm64/kiro4cpa.dylib（需要可用的 Xcode Command Line Tools）
```

`build-linux-docker` 在 `golang:1.26-bookworm` 中用 `gcc-x86-64-linux-gnu` 交叉编译，产物要求 glibc ≥ 2.34。

## 安装

1. 把编译产物放进 CPA 的插件目录（默认是 CPA 工作目录下的 `plugins/`），按平台分子目录：

   ```
   plugins/linux/amd64/kiro4cpa.so
   ```

2. 在 CPA 的 `config.yaml` 中启用插件：

   ```yaml
   plugins:
     enabled: true
     configs:
       kiro4cpa:
         enabled: true
         # 以下均为可选，括号内为默认值
         # default_region: us-east-1   # 凭据未指定 apiRegion、profileArn 中也没有区域时使用
         # client_version: 1.4.5       # User-Agent 中上报的 Kiro IDE 版本
         # os_version: darwin#24.6.0
         # node_version: 22.22.0
   ```

3. 重启 CPA，日志中出现 `plugin registered plugin_id=kiro4cpa` 即加载成功。

## 导入凭据

每个账号在 CPA 的 `auth-dir` 下放一个 JSON 文件，顶层必须有 `"type": "kiro4cpa"`：

```json
{
  "type": "kiro4cpa",
  "authMethod": "social",
  "refreshToken": "<refresh-token>",
  "region": "us-east-1"
}
```

| authMethod | 必填字段 |
|---|---|
| `social` | `refreshToken` |
| `idc`（也接受 `builder-id`、`iam`） | `refreshToken`、`clientId`、`clientSecret` |
| `external_idp`（也接受 `azuread`、`entra`） | `refreshToken`、`clientId`、`tokenEndpoint`（https） |
| `api_key` | `kiroApiKey` |

可选字段：`accessToken`、`expiresAt`、`profileArn`、`authRegion`、`apiRegion`、`machineId`、`email`。没有 `accessToken` 时，CPA 加载后会立即刷新一次。

- kiro.rs 的 `credentials.json` 是数组，CPA 不支持数组文件。请把每个元素单独存成一个文件，并补上 `type` 字段。
- 刷新后的 token 和模型列表会写回同一个文件。这个文件等同于账号密码，请限制文件权限，不要提交到版本库。

## 模型

`/v1/models` 中列出的是 Kiro 自己的模型 ID，例如 `claude-sonnet-4.5`、`claude-haiku-4.5`、`auto`。需要其他写法时，用 CPA 的 `oauth-model-alias` 配置别名，通道名为 `kiro4cpa`：

```yaml
oauth-model-alias:
  kiro4cpa:
    - name: claude-sonnet-4.5
      alias: claude-sonnet-4-5
      fork: true   # 同时保留原 ID
```

## 用量额度

插件实现了 CPA 的 QuotaProvider，可以通过 CPA 管理接口查询某个账号的 Kiro 额度：

```
GET /v0/management/plugins/kiro4cpa/quota?auth_index=<auth-index>
```

管理面板能否展示额度，取决于面板是否支持插件 QuotaProvider。官方 [Cli-Proxy-API-Management-Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center) 支持；部分第三方面板只认内置 provider，不会显示。

## Web 登录授权

在管理面板「登录授权」页面点击 Kiro 登录，会在新标签页打开插件的登录页 `/v0/resource/plugins/kiro4cpa/connect`，在页面里选择登录方式：

| 方式 | 说明 |
|---|---|
| Builder ID | AWS 设备授权。打开 AWS 页面输入邮箱，确认授权码 |
| Google / GitHub | 打开 Kiro 登录页选择 Google 或 GitHub。登录后浏览器会跳到打不开的 `127.0.0.1:3128` 页面，把地址栏里的完整地址粘贴回登录页 |
| IAM Identity Center | 填写 `https://<目录>.awsapps.com/start` 和 Region，再走 AWS 设备授权 |
| 导入凭据 | Social 或 IdC 的 refreshToken，或 Kiro API Key |

完成后面板会自动拿到结果，由 CPA 保存凭据。

登录页的操作走 CPA 管理接口，需要管理密钥。如果面板登录时勾选了「记住密码」，并且面板和 CPA 用同一个地址打开，登录页会直接读取面板保存的密钥；否则会提示手动输入，输入的密钥不会保存。

凭据文件按 `kiro4cpa-<登录方式>-<邮箱>.json` 命名，例如 `kiro4cpa-google-user@example.com.json`。拿不到邮箱时用账号 ID 的 hash，同一账号再次登录会覆盖原文件。

## 已知限制

- 实际测试过的有 Social 账号与 Builder ID 设备流；IdC、External IdP、API Key 按协议实现
- 只测试过 linux/amd64 与 darwin/arm64

## 致谢

协议细节参考了以下 MIT 项目，版权声明见 [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES)：

- [hank9999/kiro.rs](https://github.com/hank9999/kiro.rs)、[ZyphrZero/kiro.rs](https://github.com/ZyphrZero/kiro.rs)
- [xiaokui-dev/cliproxyapi-kiro-plugin](https://github.com/xiaokui-dev/cliproxyapi-kiro-plugin)
- [nguyenha935/kiro-cpa-plugin](https://github.com/nguyenha935/kiro-cpa-plugin)

## License

[MIT](LICENSE)
