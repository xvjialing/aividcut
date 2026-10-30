# 应用第三方声明材料

客户端生产构建需要 Python3.9+、项目支持的 Node.js 和已通过 pnpm 安装的前端依赖（只在构建机，最终客户端不需要这些环境）。Vite 插件生成实际包/版本和最终 JS/CSS 哈希；Go 构建后，收集器读取实际二进制的 BuildInfo 及现有缓存，输出 THIRD_PARTY_APPLICATION_NOTICES。收集不安装包、不运行安装脚本或联网下载模块。

当前前端构建清单为 schemaVersion=2，以 `pnpm-lock.yaml` 的文件名、SHA256 与精确 packageManager 身份绑定。Vite 和 Python 收集器复用 `frontend/build/pnpm-lock.ts` 的 YAML 解析，兼容 pnpm 12 的包管理器/应用多文档，只选择唯一应用 importer。实际模块按 pnpm `.pnpm/.../node_modules/...` 路径读取元数据，再核对锁中 `name@version` 和 integrity；路径仍须位于本工程 `frontend/node_modules` 内，外部链接继续拒绝。`yaml` 仅为构建依赖，不进入客户端运行环境。

```sh
python3 scripts/collect_dependency_notices.py \
  --artifact /absolute/AIVidCut \
  --project-root /absolute/aividcut \
  --output /absolute/new-notices-directory
```

新目录才可生成。自动模式读取frontend/dist/dependency-inventory.json及build/dependency-license-sources.json，Go检查器用GOTOOLCHAIN=local/GOPROXY=off/GOSUMDB=off，Go工具链许可只从匹配VERSION的根读取；缺失缓存或版本/hash不符不会自动下载/猜测。

多产物可使用--config JSON，字段为projectRoot、frontendRoot、frontendInventory、artifacts（name/path数组）、licenseSources（精确组件补充文本）、toolchains（version/directory数组）。当前路径需明确指定，推荐绝对路径；产物名1–64位小写字母/数字/连字符。补充文本来源说明见[build/notices](../build/notices/README.md)。

inventory.json的artifactHashScope说明哈希属于收集时输入，签名可随后改变字节；不能与最终签名二进制直接混用。签名后在材料目录外生成绑定：

```sh
python3 scripts/collect_dependency_notices.py \
  --artifact /absolute/final-signed/AIVidCut \
  --bind-notices /absolute/THIRD_PARTY_APPLICATION_NOTICES \
  --output /absolute/application-notices-binding.json
```

绑定先验证所有已登记文本原SHA，再核对目录文件，记录最终可执行文件、inventory与完整材料树哈希。绑定必须在材料目录外，防止签名/清单互相引用；不能覆盖程序或材料。Mac构建输出build/bin/AIVidCut.app-notices-binding.json；Windows随包根输出application-notices-binding.json。重新签名或改材料后必须重新绑定。

NOTICES.md便于人工阅读，texts保留原文及专利声明，issues列未补齐文本/来源。licenseApproval固定false；即使文本齐全也不等于对应源码、重链接安排、内嵌第三方、媒体GPL/Qt、专利和签名义务已履行。媒体SDK声明仍单独保留，不用Go/npm清单替代。

```sh
python3 -m unittest discover -s scripts -p 'test_collect_dependency_notices.py' -v
go test -tags production -run '^TestProduction' .
```

后者校验实际embed.FS的chunk哈希和无源码映射，不只核对另一个dist副本。生成材料/构建时版本、chunk、路径、链接、产物替换或预算异常应先修复，不用空白模板或忽略错误使检查变绿。
