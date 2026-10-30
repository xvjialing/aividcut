#include "audio_mix.h"
#include "source_audio.h"
#include <algorithm>
#include <cmath>
#include <cstdint>
#include <cstring>
#include <stdexcept>

namespace {
uint64_t created=0,calls=0,skipped=0,mixed_samples=0,peak_frame_bytes=0;
constexpr int rate=48000,channels=2,max_samples=131072;

void read_audio(mlt_frame frame,int wanted,void** buffer){
    auto format=mlt_audio_f32le;int actual_rate=rate,actual_channels=channels,actual_samples=wanted;
    int error=mlt_frame_get_audio(frame,buffer,&format,&actual_rate,&actual_channels,&actual_samples);
    if(error||!*buffer||format!=mlt_audio_f32le||actual_rate!=rate||actual_channels!=channels||actual_samples!=wanted)
        throw std::runtime_error("controlled mix input differs from shared sample clock");
}

int get_audio(mlt_frame frame,void** buffer,mlt_audio_format* format,int* frequency,int* count,int* samples){
    auto other=static_cast<mlt_frame>(mlt_frame_pop_audio(frame));
    auto transition=static_cast<mlt_transition>(mlt_frame_pop_audio(frame));
    auto profile=mlt_service_profile(mlt_transition_service(transition));
    auto position=mlt_frame_get_position(frame);
    auto begin=source_audio_frame_sample(position,profile->frame_rate_num,profile->frame_rate_den,rate);
    auto end=source_audio_frame_sample(position+1,profile->frame_rate_num,profile->frame_rate_den,rate);
    auto wanted=end-begin;
    if(wanted<1||wanted>max_samples){report_source_audio_error("controlled mix sample bound");return 1;}
    size_t bytes=static_cast<size_t>(wanted)*channels*sizeof(float);
    auto output=static_cast<float*>(mlt_pool_alloc(bytes));
    if(!output){report_source_audio_error("controlled mix allocation failed");return 1;}
    ++calls;peak_frame_bytes=std::max(peak_frame_bytes,static_cast<uint64_t>(bytes));
    try{
        void* right=nullptr;read_audio(other,static_cast<int>(wanted),&right);
        auto evidence=mlt_frame_unique_properties(frame,mlt_transition_service(transition));
        void* left=nullptr;
        if(!mlt_properties_get_int(evidence,"blank_base"))read_audio(frame,static_cast<int>(wanted),&left);
        auto a=static_cast<const float*>(left),b=static_cast<const float*>(right);
        for(int64_t i=0;i<wanted*channels;++i){
            float value=(a?a[i]:0.0f)+b[i];
            if(!std::isfinite(value))throw std::runtime_error("controlled mix contains non-finite PCM");
            output[i]=value;
        }
        mixed_samples+=wanted;
    }catch(const std::exception& error){
        // MLT不保证传播get_audio回调错误；提供安全缓冲后由Engine拒绝整帧。
        std::memset(output,0,bytes);report_source_audio_error(error.what());
    }
    mlt_frame_set_audio(frame,output,mlt_audio_f32le,bytes,mlt_pool_release);
    *buffer=output;*format=mlt_audio_f32le;*frequency=rate;*count=channels;*samples=static_cast<int>(wanted);
    return 0;
}

mlt_frame process(mlt_transition transition,mlt_frame base,mlt_frame other){
    // 时间线空白不建立回调链，也不为尚未进入/已经离开的片段解码声音。
    if(mlt_frame_is_test_audio(other)){++skipped;return base;}
    auto evidence=mlt_frame_unique_properties(base,mlt_transition_service(transition));
    mlt_properties_set_int(evidence,"blank_base",mlt_frame_is_test_audio(base));
    mlt_frame_push_audio(base,transition);mlt_frame_push_audio(base,other);
    mlt_frame_push_audio(base,reinterpret_cast<void*>(get_audio));
    mlt_properties_set_int(mlt_frame_properties(base),"test_audio",0);
    return base;
}

void* factory(mlt_profile,mlt_service_type,const char*,const void*){
    auto transition=mlt_transition_new();
    if(transition){++created;transition->process=process;mlt_properties_set_int(mlt_transition_properties(transition),"_transition_type",2);}
    return transition;
}
}

void register_audio_mix(mlt_repository repository){mlt_repository_register(repository,mlt_service_transition_type,"aividcut.audio_mix",factory);}
std::string audio_mix_diagnostics_json(){
    return "{\"created\":"+std::to_string(created)+",\"calls\":"+std::to_string(calls)+",\"skippedBlankFrames\":"+std::to_string(skipped)+",\"mixedSamples\":"+std::to_string(mixed_samples)+",\"peakFrameBytes\":"+std::to_string(peak_frame_bytes)+"}";
}
