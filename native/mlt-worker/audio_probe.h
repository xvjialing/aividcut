#pragma once
#include <cstdint>
#include <string>

struct AudioOrigin {
    bool has_audio = false;
    int64_t start_us = 0;
    int sample_rate = 0;
};

// 读取首个有效解码音频帧PTS，减去容器原点；不拿容器时长反推样本。
AudioOrigin probe_audio_origin(const std::string& path);
