# kiro4cpa-plugin 开发规则

## 插件标识

CPA 从动态库文件名去掉扩展名得到插件 ID 和版本：

- 生产产物必须命名 `kiro4cpa-v<版本>.so`（或 `.dylib` / `.dll`）
- 不能叫 `kiro4cpa.so`，否则插件 ID 会变成 `kiro4cpa`，版本丢失
- 代码里的 `pluginID = "kiro4cpa"` 必须与文件名前缀一致

## 本地调试流程

Mac 上 `make build-darwin` 会因 Xcode SDK 的 `.tbd` 不认 `arm64e.x1`
架构而链接失败，这是环境问题，不是代码问题。调试走 Docker：

    make build-linux-docker
    scp bin/linux/amd64/kiro4cpa.so aliyun-cloud:/tmp/
    ssh aliyun-cloud 'cp /tmp/kiro4cpa.so \
      /docker/api4ai/cpa_data/plugins/linux/amd64/kiro4cpa-v<版本>.so && \
      docker restart cpa'

服务器是 CPA v8.0.42，插件数据目录 `/CLIProxyAPI/data` 已挂载到宿主机
`/docker/api4ai/cpa_data/data`。管理接口有 IP 封禁保护，不要用命令反复猜
管理密钥，管理操作走面板。

## 发布流程

1. 改 `internal/kiro/version.go` 的版本号（必须递增，商店按版本比较）
2. `make test && go vet ./... && gofmt -l .`
3. commit → push main
4. `git tag -a v<版本> -m "v<版本>" && git push origin v<版本>`
5. CI 自动运行测试、编译 5 个平台、创建 GitHub Release

**推 tag 即正式发布。** 调试期间不要推 tag。

## 官方插件商店

`kiro4cpa` 已在官方 registry（PR #287 已合并）。发布新版本只需推 tag，
商店读取最新的 GitHub Release，无需再向官方提 PR。

只有以下情况需要修改官方 `registry.json`：

- 插件名称、描述、logo、仓库地址变化
- 新增插件

项目内的 `registry.json` 保留作为备用分发源。

## 测试要求

- `go test ./... -race` 必须通过
- `internal/kiro` 覆盖率保持在 85% 以上
- 提交前 `gofmt -l .` 无输出，`go vet ./...` 无错误
- CI 在 Linux / macOS / Windows 三平台跑测试

## 代码约定

- 只实现 Claude Messages 执行器，OpenAI 格式由 CPA 转换
- 执行器不持有 caller context，无法长等待；限速超上限时返回 429 交给 CPA 换号
- 凭据状态按 `AuthID`（文件名）区分，不用 accessToken，token 刷新后状态不丢
- 上游错误按状态码分类：402/403 冷却换号，429 走 CPA 退避，5xx 报 502
