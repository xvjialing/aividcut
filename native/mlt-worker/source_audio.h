#pragma once
#include <framework/mlt.h>
#include <string>

void register_source_audio(mlt_repository repository);
bool source_audio_available();
// C回调不可跨ABI抛异常；主线程在get_audio后读取实际解码/滤镜错误。
void clear_source_audio_error();
void report_source_audio_error(const std::string& reason);
std::string source_audio_error();
int64_t source_audio_frame_sample(int64_t frame,int fps_num,int fps_den,int sample_rate);
void begin_source_audio_pull();
void retire_source_audio_contexts();
void clear_source_audio_contexts();
std::string source_audio_diagnostics_json();
