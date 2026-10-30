# Windows媒体构建与验收说明

本说明针对原生Windows上的明确MSYS2 UCRT64 SDK。当前Mac已有本项目完整C++ worker交叉编译和随包静态核验，见[交叉构建与交接说明](Windows交叉构建与交接说明.md)；Windows上的DLL加载、视频音频及此原生入口执行仍未完成实机验收。

## 前置输入

准备同一UCRT64前缀下的GCC/libstdc++、CMake、Ninja、pkg-config、MLT7开发库、兼容FFmpeg开发库、Qt6 Gui/Widgets和对应插件。MLT可选Qt/SVG/FFTW/音频模块依赖也要在同一前缀中补齐，避免混用MSVC或CLANG64库。[MSYS2官方环境说明](https://www.msys2.org/docs/environments/)。脚本不自动安装或执行安装脚本。

本次调研核对的官方输入为[MLT7.42](https://packages.msys2.org/packages/mingw-w64-ucrt-x86_64-mlt)、[FFmpeg9.0.2](https://packages.msys2.org/packages/mingw-w64-ucrt-x86_64-ffmpeg)和[Qt6.12](https://packages.msys2.org/packages/mingw-w64-ucrt-x86_64-qt6-base)，实际接入时重新核对版本、完整依赖、源码/许可与包签名。不能只取得三个主包就视为完整SDK。

## 明确的新构建目录

```powershell
pwsh -NoProfile -File .\scripts\build_windows_media.ps1 `
  -SdkRoot C:\msys64\ucrt64 `
  -BuildDirectory C:\work\aividcut-native-new `
  -OutputDirectory C:\work\aividcut-media-new
```

需要Go及SDK工具已安装；构建/输出目录必须不存在。脚本从SDK bin选择工具，核对x64 MinGW triple，限制PATH/pkg-config及CMake包注册表来源，最终恢复调用者环境。只构建本项目worker，失败保留当前日志/目录，不写原SDK。

PowerShell源文件使用UTF8 BOM以保留Windows PowerShell中文内容，写给Go的配置则明确无BOM。Mac便携PowerShell解析通过只证明语法，不代替Windows执行、CMake或ABI验证。

## DLL规划与候选包

构建成功后生成明确worker/模块/Qt插件配置，调用Go工具。默认不加-assemble只输出只读依赖图；缺DLL和worker保持具体缺项。仅闭包齐全时装配：

```powershell
.\windows-media-package.exe -config .\windows-runtime-config.json
.\windows-media-package.exe -config .\windows-runtime-config.json -assemble
```

MLT/Qt插件不因为放在独立目录就免除依赖解析。Windows加载依赖时还受系统API-set、KnownDLL及加载上下文影响，[微软加载顺序](https://learn.microsoft.com/en-us/windows/win32/dlls/dynamic-link-library-search-order)与[API-set说明](https://learn.microsoft.com/en-us/windows/win32/apiindex/windows-apisets)。固定systemDlls列表只是明确的OS基线假设，未知DLL仍报missing，延迟导入也不跳过。

新候选中media具有清单v2，可作为AIVIDCUT_BUNDLE_MEDIA_RUNTIME输入交给现有Wails Windows构建复制/核对。需要的真实验证仍包括worker协议、Qt离屏/字体、图像/视频/声音、LUT、中文路径、导出保存重开、取消/资源、签名与安装更新；无实机证据时继续标未完成。
