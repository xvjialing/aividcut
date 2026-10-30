#pragma once
#include <framework/mlt.h>
#include <string>

// 受控48kHz双声道同样本窗求和，不持有跨帧PCM队列。
void register_audio_mix(mlt_repository repository);
std::string audio_mix_diagnostics_json();
