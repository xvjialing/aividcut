# 精确版本的补充声明来源

声明收集默认使用实际Go模块缓存/npm包的顶层原许可文件，不凭SPDX字段合成文本。只有精确包缺文件时，才可采用build/dependency-license-sources.json登记的补充原文；必须固定组件版本、上游提交、来源URL与SHA256。

## Wails runtime 3.0.0

该npm发布包缺顶层LICENSE；其registry元数据gitHead为7b11001b92b712210b27d8cb53430e83f67ad8e4，与已缓存Go模块github.com/wailsapp/wails/v3@v3.0.0的Origin.Hash相同。补充文件从该精确Go模块的v3/LICENSE读取，保留原字节及哈希，没有使用另一版本或标准模板替代。

- [npm精确版本元数据](https://registry.npmjs.org/@wailsio%2fruntime/3.0.0)
- [对应提交的v3/LICENSE](https://github.com/wailsapp/wails/blob/7b11001b92b712210b27d8cb53430e83f67ad8e4/v3/LICENSE)
- [本地原文](wails-runtime-3.0.0-LICENSE.txt)

核查日期2026-10-27。这里记录文本来源，不作整个产品的许可履行或商业发行批准。

## 当前未补齐项

react-remove-scroll-bar@2.3.8的实际npm包没有顶层许可文本，README只写MIT。registry记录gitHead b3b1287aad81def2e2ae707274b74531b61ddbaf；本轮未取得该提交的原LICENSE（公开接口返回404），未据此断言文件永远不存在，也未挪用其它版本/同作者项目的文本。保留inventory.json中的缺项待复核。

2026-10-28复核：npm latest仍为2.3.8，精确gitHead的LICENSE入口仍404；当前master的package.json显示2.3.7，LICENSE为后续年份文本，未将该分支材料当作2.3.8精确原文。缺项继续保留。
