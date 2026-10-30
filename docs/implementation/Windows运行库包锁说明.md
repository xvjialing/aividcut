# Windows运行库包锁说明

本工具准备明确来源的UCRT64运行文件，不安装系统SDK、不执行Windows程序或包安装脚本，不替代MSYS2开发工具链、本项目worker和Windows实机。固定来源为MSYS2官方镜像，锁中包版本、URL和SHA可重放；PGP签名未验证，releaseVerified仍false。

## 已锁版本的运行文件重放

```sh
python3 scripts/resolve_msys2_runtime.py --zstd /absolute/path/zstd apply \
  --lock build/windows-runtime.packages.lock.json \
  --cache /absolute/path/package-cache \
  --output /absolute/path/new-runtime-files
```

输出必须不存在、父目录已存在。已有缓存也逐个按锁中大小/摘要核对；没有缓存才通过HTTPS下载。新stage只接收普通DLL、FFmpeg/FFprobe、MLT/Qt插件、MLT数据和许可文本，不提取SDK工具程序或执行安装逻辑。失败保留本次stage，旧SDK和已有输出不覆盖。

结果中的ucrt64是运行库准备集合。它不含完整头文件/导入库/编译器，不能直接当build_windows_media.ps1的开发SDK；原生编译仍需同ABI的完整前缀。当前workerMissing=true时装配器继续拒绝提交，没有自动改用其它程序。

## 从明确快照解析新锁

参考[MSYS2官方包管理说明](https://www.msys2.org/docs/package-management/)，保留同次获取的ucrt64.db与ucrt64.files及摘要，再根据静态扫描的missingNames计划。不要把旧FFmpeg/MLT修订版与新DLL混合。

```sh
python3 scripts/resolve_msys2_runtime.py --zstd /absolute/path/zstd plan \
  --database /absolute/path/ucrt64.db --files-database /absolute/path/ucrt64.files \
  --missing /absolute/path/missing.json \
  --root-package mingw-w64-ucrt-x86_64-ffmpeg \
  --provider zlib1.dll=mingw-w64-ucrt-x86_64-zlib \
  --output-lock /absolute/path/new.lock.json
```

冲突保留所有提供者，明确选择需在该DLL候选内，不猜第一项。快照/版本不一致、无提供者或锁仍有未解析项不能重放。首次先从同一快照取根包，扫描其实际DLL，再迭代传递依赖；锁最终记录根包/明确提供者、观察到的许可/依赖及系统基线，不能把元数据当法律授权。

脚本默认plan只读，apply才产生新运行文件。锁定版本不自动更新；镜像旧版本不可取得时需要核对缓存或明确生成新锁，不静默替换。对应源码、包签名与Windows KnownDLL/API-set/驱动、设备和动态LoadLibrary仍有独立验收。
