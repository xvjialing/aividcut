#include "audio_probe.h"
#include <algorithm>
#include <chrono>
#include <memory>
#include <stdexcept>
extern "C" {
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavutil/intreadwrite.h>
}

namespace {
struct FormatCloser { void operator()(AVFormatContext* value)const { avformat_close_input(&value); } };
struct CodecCloser { void operator()(AVCodecContext* value)const { avcodec_free_context(&value); } };
struct FrameCloser { void operator()(AVFrame* value)const { av_frame_free(&value); } };
struct PacketCloser { void operator()(AVPacket* value)const { av_packet_free(&value); } };
using Clock=std::chrono::steady_clock;
int interrupted(void* opaque){return Clock::now()-*static_cast<Clock::time_point*>(opaque)>std::chrono::seconds(10);}
}

AudioOrigin probe_audio_origin(const std::string& path) {
    auto started=Clock::now();
    AVFormatContext* raw=avformat_alloc_context();
    if(!raw)throw std::runtime_error("audio probe allocation");
    raw->interrupt_callback={interrupted,&started};
    AVDictionary* options=nullptr;av_dict_set(&options,"protocol_whitelist","file,pipe",0);
    int opened=avformat_open_input(&raw,path.c_str(),nullptr,&options);av_dict_free(&options);
    if(opened<0){if(raw)avformat_close_input(&raw);throw std::runtime_error("audio probe open failed");}
    std::unique_ptr<AVFormatContext,FormatCloser> format(raw);
    if(avformat_find_stream_info(raw,nullptr)<0)throw std::runtime_error("audio probe stream information failed");
    int index=-1;
    for(unsigned i=0;i<raw->nb_streams;++i)if(raw->streams[i]->codecpar->codec_type==AVMEDIA_TYPE_AUDIO){index=static_cast<int>(i);break;}
    if(index<0)return {};
    auto stream=raw->streams[index];auto decoder=avcodec_find_decoder(stream->codecpar->codec_id);
    if(!decoder)throw std::runtime_error("audio probe codec unavailable");
    std::unique_ptr<AVCodecContext,CodecCloser> codec(avcodec_alloc_context3(decoder));
    if(!codec||avcodec_parameters_to_context(codec.get(),stream->codecpar)<0)throw std::runtime_error("audio probe codec parameters failed");
    // 解码器需要包时间基才能在MP3部分skip/discard后更新首个有效样本的PTS。
    codec->pkt_timebase=stream->time_base;
    codec->thread_count=1;
    if(avcodec_open2(codec.get(),decoder,nullptr)<0)throw std::runtime_error("audio probe codec initialization failed");
    const int64_t origin=raw->start_time==AV_NOPTS_VALUE?0:raw->start_time;
    // 文件开头保留demuxer缓冲的负PTS priming包，seek到0可能丢掉AAC首个有效块。
    // 很晚才开始的音轨只跳到其起点前一秒，保留编解码预卷而不扫描长视频前缀。
    if(stream->start_time!=AV_NOPTS_VALUE&&av_rescale_q(stream->start_time,stream->time_base,AVRational{1,1000000})-origin>1000000){
        auto target=stream->start_time-av_rescale_q(1000000,AVRational{1,1000000},stream->time_base);
        if(av_seek_frame(raw,index,target,AVSEEK_FLAG_BACKWARD)<0)throw std::runtime_error("audio probe preroll seek failed");
    }
    std::unique_ptr<AVPacket,PacketCloser> packet(av_packet_alloc());
    std::unique_ptr<AVFrame,FrameCloser> frame(av_frame_alloc());
    if(!packet||!frame)throw std::runtime_error("audio probe frame allocation");
    for(int attempts=0;attempts<100000&&!interrupted(&started);++attempts){
        int read=av_read_frame(raw,packet.get());
        if(read<0){if(avcodec_send_packet(codec.get(),nullptr)<0)break;}
        else if(packet->stream_index!=index){av_packet_unref(packet.get());continue;}
        else {int sent=avcodec_send_packet(codec.get(),packet.get());av_packet_unref(packet.get());if(sent<0&&sent!=AVERROR(EAGAIN))throw std::runtime_error("audio probe packet decode failed");}
        while(avcodec_receive_frame(codec.get(),frame.get())>=0){
            auto pts=frame->best_effort_timestamp;if(pts==AV_NOPTS_VALUE)pts=frame->pts;
            int rate=frame->sample_rate>0?frame->sample_rate:codec->sample_rate;
            if(pts==AV_NOPTS_VALUE||rate<=0||frame->nb_samples<=0){av_frame_unref(frame.get());continue;}
            int64_t skip=0;
            if(auto side=av_frame_get_side_data(frame.get(),AV_FRAME_DATA_SKIP_SAMPLES);side&&side->size>=8)skip=AV_RL32(side->data);
            if(skip>=frame->nb_samples){av_frame_unref(frame.get());continue;}
            int64_t start=av_rescale_q(pts,stream->time_base,AVRational{1,1000000})-origin;
            start+=av_rescale_q(skip,AVRational{1,rate},AVRational{1,1000000});
            int64_t end=start+av_rescale_q(frame->nb_samples-skip,AVRational{1,rate},AVRational{1,1000000});
            if(end<=0){av_frame_unref(frame.get());continue;}
            return {true,std::max<int64_t>(0,start),rate};
        }
        if(read<0)break;
    }
    throw std::runtime_error("audio first decoded timestamp unavailable");
}
