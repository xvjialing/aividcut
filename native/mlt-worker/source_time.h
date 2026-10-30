#pragma once
#include <framework/mlt.h>
#include <string>
struct AVFrame;

// 工程帧与原生素材帧分别计时；不将微秒入点舍入到工程网格。
void register_source_time(mlt_repository repository);
void begin_source_time_pull();
void retire_source_time_contexts();
void clear_source_time_contexts();
std::string source_time_diagnostics_json();
// 受控解码帧只借用至MLT frame close；包含真实PTS和DISPLAYMATRIX，外部需要保留时av_frame_clone。
const AVFrame* source_time_decoded_frame(mlt_frame frame);
