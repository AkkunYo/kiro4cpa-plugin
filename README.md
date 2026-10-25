# kiro4cpa-plugin

中文 | [English](./README.en.md)

[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)（CPA）v8 的 Kiro provider 插件。以原生 c-shared 插件的形式加载，把 Kiro 账号接入 CPA，对外提供 Claude Messages、OpenAI Chat Completions / Responses 接口。

> 非官方项目，与 AWS / Kiro 无关联。请遵守 Kiro 的服务条款。

## 功能

- 浏览器内选择登录方式：Builder ID、Google / GitHub、IAM Identity Center，或导入 Refresh Token / API Key
- 凭据类型：Social（Google / GitHub）、IdC（Builder ID / IAM Identity Center）、External IdP（Entra ID 等）、API Key
- 登录后读取账号邮箱，凭据文件按账号命名；同一账号再次登录会覆盖原文件，不会出现重复账号
- 自动刷新 access token；请求遇到 token 失效时强制刷新一次并重试，刷新结果写回凭据文件
- 刷新时从 Kiro 拉取账号可用的模型列表并缓存；拉取失败时使用内置列表
- 流式 / 非流式、extended thinking、tool use、图片输入（jpeg / png / gif / webp）
- 通过 CPA QuotaProvider 查询 Kiro 用量额度；免费试用和赠送额度只统计生效中的部分
- 全局 `oauth-excluded-models` 对每个账号都生效，账号自己的禁用列表在此基础上追加
- Kiro 以 403 `User is not authorized to make this call` 拒绝账号时，自动禁用该账号并写入备注
- 上游错误按状态码交给 CPA：401 / 402 / 403 / 429 触发冷却和换号，5xx 报为可重试的 502
- 按凭据限速，并记住 Kiro 报出的月度额度和账号暂停，避免反复打上游

插件只实现 Claude Messages 执行器，OpenAI 格式由 CPA 自行转换。

## 构建

需要 Go 1.26 和 cgo。

```bash
make test                # 单元测试
make build-linux-docker  # 用 Docker 交叉编译 bin/linux/amd64/kiro4cpa.so
make build-darwin        # 本机编译 bin/darwin/arm64/kiro4cpa.dylib（需要可用的 Xcode Command Line Tools）
```

`build-linux-docker` 在 `golang:1.26-bookworm` 中用 `gcc-x86-64-linux-gnu` 交叉编译，产物要求 glibc ≥ 2.34。

部署到 CPA 时，动态库文件名决定插件 ID 和版本：必须是 `kiro4cpa-v<版本>.so`（或 `.dylib` / `.dll`），命名成 `kiro4cpa.so` 会让宿主注册成不带版本的 `kiro4cpa`。开发、测试和发布流程见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 安装

### 从插件商店安装

在 CPA 管理面板的插件商店搜索 **Kiro OAuth**（ID `kiro4cpa`）安装即可，商店会按当前系统和架构下载对应的发布包。

插件商店未收录时，可以在 CPA 的 `config.yaml` 里加上本仓库的商店源：

```yaml
plugins:
  enabled: true
  store-sources:
    - "https://raw.githubusercontent.com/AkkunYo/kiro4cpa-plugin/main/registry.json"
```

### 手动安装

从 [Releases](https://github.com/AkkunYo/kiro4cpa-plugin/releases) 下载对应平台的 `kiro4cpa_<版本>_<系统>_<架构>.zip`，按 `checksums.txt` 校验后解压。

1. 把动态库放进 CPA 的插件目录（默认是 CPA 工作目录下的 `plugins/`），按平台分子目录：

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
         # min_token_interval: 1s
         # max_token_interval: 2s
         # suspend_cooldown: 1h
   ```

3. 重启 CPA，日志中出现 `plugin registered plugin_id=kiro4cpa` 即加载成功。

## 手动放置凭据文件

一般直接用下方「Web 登录授权」里的登录或导入即可。需要批量迁移或脚本化时，也可以在 CPA 的 `auth-dir` 下为每个账号放一个 JSON 文件，顶层必须有 `"type": "kiro4cpa"`：

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

## 请求限速

多账号场景下 CPA 自己会换号，但同一个凭据被并发打到 Kiro 时仍会触发上游限流。插件按凭据做节流，并把 Kiro 报出的持续性状态记下来，避免反复打上游：

```yaml
plugins:
  configs:
    kiro4cpa:
      min_token_interval: 1s   # 同一凭据两次请求的最小间隔
      max_token_interval: 2s   # 间隔上限，也是排队等待的上限
      suspend_cooldown: 1h     # 被 Kiro 判定暂停后退出轮换的时长
```

行为说明：

- 间隔在 `min_token_interval` 与 `max_token_interval` 之间随机取值，再叠加 ±30% 抖动，避免多实例同时发起
- 并发请求在 `max_token_interval` 内排队；超过上限时返回 429，交给 CPA 换号，而不是无限等待
- 上游返回 402（月度额度）时，该凭据退出轮换到下一个 UTC 零点
- 上游返回 403 且带 `SUSPENDED` 或 `User is not authorized to make this call` 时，退出轮换 `suspend_cooldown`
- 单个凭据文件加 `"disable_cooling": true` 可退出上述全部逻辑，CPA 自身的冷却不受影响

凭据选择、换号和 429 退避仍由 CPA 负责，插件不重复实现。

## Web 登录授权

在管理面板「登录授权」页面点击 Kiro 登录，会在新标签页打开插件的登录页 `/v0/resource/plugins/kiro4cpa/connect`，在页面里选择登录方式：

| 方式 | 说明 |
|---|---|
| Builder ID | AWS 设备授权。打开 AWS 页面输入邮箱，确认授权码 |
| Google / GitHub | 打开 Kiro 登录页选择 Google 或 GitHub。登录后浏览器会跳到打不开的 `127.0.0.1:3128` 页面，把地址栏里的完整地址粘贴回登录页 |
| IAM Identity Center | 填写 `https://<目录>.awsapps.com/start` 和 Region，再走 AWS 设备授权 |
| 导入凭据 | 已有 Refresh Token 或 API Key 时直接导入，见下表 |

完成后面板会自动拿到结果，由 CPA 保存凭据。

「导入凭据」支持三种类型：

| 凭据类型 | 需要填写 | 从哪里取 |
|---|---|---|
| Social Refresh Token（Google / GitHub） | Refresh Token、Region | Kiro IDE 或 kiro.rs 凭据里的 `refreshToken` |
| IdC Refresh Token（Builder ID / 企业 SSO） | Refresh Token、Client ID、Client Secret、Region | AWS SSO 缓存（`~/.aws/sso/cache/`）里的 `refreshToken`，以及同目录 client 注册文件里的 `clientId`、`clientSecret` |
| Kiro API Key | API Key | Kiro 后台生成的 API Key |

Region 默认 `us-east-1`。导入时只校验必填字段；保存后会尝试刷新 token 并读取账号邮箱用于命名，刷新失败时仍会保存，凭据是否可用以面板里的账号状态为准。

登录页的操作走 CPA 管理接口，需要管理密钥。如果面板登录时勾选了「记住密码」，并且面板和 CPA 用同一个地址打开，登录页会直接读取面板保存的密钥；否则会提示手动输入，输入的密钥不会保存。

凭据文件按 `kiro4cpa-<登录方式>-<邮箱>.json` 命名，例如 `kiro4cpa-google-user@example.com.json`。拿不到邮箱时用账号 ID 的 hash，同一账号再次登录会覆盖原文件。

## 已知限制

- Builder ID、Google、GitHub 登录已实测；IAM Identity Center、External IdP、API Key 按协议实现
- 发布产物覆盖 linux/amd64、linux/arm64、darwin/amd64、darwin/arm64、windows/amd64；实际跑通过的是 linux/amd64 与 darwin/arm64

## 致谢

协议细节参考了以下 MIT 项目，版权声明见 [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES)：

- [hank9999/kiro.rs](https://github.com/hank9999/kiro.rs)、[ZyphrZero/kiro.rs](https://github.com/ZyphrZero/kiro.rs)
- [xiaokui-dev/cliproxyapi-kiro-plugin](https://github.com/xiaokui-dev/cliproxyapi-kiro-plugin)
- [nguyenha935/kiro-cpa-plugin](https://github.com/nguyenha935/kiro-cpa-plugin)

## License

[MIT](LICENSE)
