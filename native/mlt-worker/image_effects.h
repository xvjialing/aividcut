#pragma once
#include <framework/mlt.h>
#include <cstdint>
#include <string>

// 仅注册本项目受控视觉transition，不接受外部filtergraph字符串。
void register_image_effects(mlt_repository repository);
bool visual_available();
bool visual_lut_available();

struct ImageEffectsStatistics {
    uint64_t builds=0,hits=0,calls=0,fast_path_calls=0,filter_ns=0,composite_ns=0,estimated_bytes=0;
    uint64_t resident_graphs=0;
};
ImageEffectsStatistics image_effects_statistics();
// 所有计数为当前worker进程累计，仅数值，不包含路径或认证信息。
std::string image_effects_diagnostics_json();
