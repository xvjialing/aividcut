#include "source_time.h"
#include "source_audio.h"
#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstring>
#include <memory>
#include <map>
#include <tuple>
#include <cstdint>
#include <set>
#include <stdexcept>
#include <string>
extern "C" {
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavfilter/avfilter.h>
#include <libavfilter/buffersrc.h>
#include <libavfilter/buffersink.h>
#include <libavutil/display.h>
#include <libswscale/swscale.h>
}

namespace {
using Clock=std::chrono::steady_clock;
struct SourceTime;
std::set<SourceTime*> source_states;
uint64_t pull_tick=0;
uint64_t created=0,opens=0,decoded_frames=0,seeks=0,anchors=0,fallbacks=0,cache_hits=0,cache_misses=0;
constexpr size_t max_sources=8;
struct Recipe {
 std::string path,asset_id,fingerprint;
 int64_t source_in,frames,placement,origin;bool origin_override;int speed_n,speed_d,fps_n,fps_d;uintptr_t legacy_identity;
 auto fields()const{return std::tie(path,asset_id,fingerprint,source_in,frames,placement,origin,origin_override,speed_n,speed_d,fps_n,fps_d,legacy_identity);}
 bool operator<(const Recipe& other)const{return fields()<other.fields();}
};
std::map<Recipe,std::shared_ptr<SourceTime>> source_cache;
struct SourceTime {
    AVFormatContext* format=nullptr;AVCodecContext* codec=nullptr;
    AVPacket* packet=nullptr;AVFrame* current=nullptr;AVFrame* next=nullptr;
    AVFilterGraph* image_graph=nullptr;AVFilterContext* image_source=nullptr;AVFilterContext* image_sink=nullptr;AVFrame* image_output=nullptr;std::string image_key;
    int stream=-1;AVRational source_tb{1,1};bool eof=false,flushed=false;int64_t anchor=AV_NOPTS_VALUE,last_position=-1;
    Clock::time_point started;uint64_t used_tick=0;int64_t timeline_origin=0;
    void release(){if(image_graph)avfilter_graph_free(&image_graph);av_frame_free(&image_output);image_source=image_sink=nullptr;image_key.clear();av_frame_free(&current);av_frame_free(&next);av_packet_free(&packet);avcodec_free_context(&codec);if(format)avformat_close_input(&format);anchor=AV_NOPTS_VALUE;last_position=-1;stream=-1;eof=flushed=false;}
    ~SourceTime(){source_states.erase(this);release();}
    static int interrupt(void* ptr){return Clock::now()-static_cast<SourceTime*>(ptr)->started>std::chrono::seconds(10);}
    void open(const char* path){
        if(format)return;started=Clock::now();format=avformat_alloc_context();if(!format)throw std::runtime_error("video allocation");
        format->interrupt_callback={interrupt,this};
        AVDictionary* options=nullptr;av_dict_set(&options,"protocol_whitelist","file,pipe",0);
        int opened=avformat_open_input(&format,path,nullptr,&options);av_dict_free(&options);
        if(opened<0||avformat_find_stream_info(format,nullptr)<0)throw std::runtime_error("video source open failed");
        for(unsigned i=0;i<format->nb_streams;++i)if(format->streams[i]->codecpar->codec_type==AVMEDIA_TYPE_VIDEO){stream=static_cast<int>(i);break;}
        if(stream<0)throw std::runtime_error("video source has no video");source_tb=format->streams[stream]->time_base;
        auto decoder=avcodec_find_decoder(format->streams[stream]->codecpar->codec_id);if(!decoder)throw std::runtime_error("video decoder unavailable");
        codec=avcodec_alloc_context3(decoder);if(!codec||avcodec_parameters_to_context(codec,format->streams[stream]->codecpar)<0)throw std::runtime_error("video decoder parameters");
        if(codec->width<1||codec->height<1||int64_t(codec->width)*codec->height>3840*2160)throw std::runtime_error("video decoder pixel bound");
        codec->thread_count=1;codec->max_pixels=3840*2160;if(avcodec_open2(codec,decoder,nullptr)<0)throw std::runtime_error("video decoder initialization");
        packet=av_packet_alloc();current=av_frame_alloc();next=av_frame_alloc();if(!packet||!current||!next)throw std::runtime_error("video frame allocation");++opens;
    }
    int64_t pts(const AVFrame* frame)const {auto value=frame->best_effort_timestamp;if(value==AV_NOPTS_VALUE)value=frame->pts;return value;}
    void seek(int64_t source_us){
        int64_t origin=timeline_origin;
        auto timestamp=av_rescale_q(std::max<int64_t>(0,source_us)+origin,AVRational{1,1000000},format->streams[stream]->time_base);
        if(av_seek_frame(format,stream,timestamp,AVSEEK_FLAG_BACKWARD)<0)throw std::runtime_error("video source seek failed");
        ++seeks;
        avcodec_flush_buffers(codec);eof=flushed=false;av_frame_unref(current);av_frame_unref(next);
    }
    bool decode(AVFrame* frame){
        av_frame_unref(frame);
        for(int attempts=0;attempts<1000000&&!interrupt(this);++attempts){
            int received=avcodec_receive_frame(codec,frame);
            if(received==0){if(pts(frame)==AV_NOPTS_VALUE)throw std::runtime_error("video actual PTS missing");++decoded_frames;return true;}
            if(received==AVERROR_EOF)return false;
            if(received!=AVERROR(EAGAIN))throw std::runtime_error("video source decode failed");
            if(eof){if(flushed)return false;avcodec_send_packet(codec,nullptr);flushed=true;continue;}
            int read=av_read_frame(format,packet);if(read<0){eof=true;continue;}
            if(packet->stream_index==stream){int result=avcodec_send_packet(codec,packet);av_packet_unref(packet);if(result<0&&result!=AVERROR(EAGAIN))throw std::runtime_error("video packet rejected");}
            else av_packet_unref(packet);
        }
        throw std::runtime_error("video pull exceeded bounded decode budget");
    }
    AVFrame* image(mlt_producer producer,int64_t position){
        used_tick=pull_tick;started=Clock::now();auto props=mlt_producer_properties(producer);open(mlt_properties_get(props,"resource"));
        timeline_origin=mlt_properties_get(props,"source_origin_us")?mlt_properties_get_int64(props,"source_origin_us"):(format->start_time==AV_NOPTS_VALUE?0:format->start_time);
        auto profile=mlt_service_profile(mlt_producer_service(producer));
        int64_t source_us=mlt_properties_get_int64(props,"source_in_us");
        int sn=mlt_properties_get_int(props,"speed_num"),sd=mlt_properties_get_int(props,"speed_den");
        if(source_us<0||sn<1||sd<1)throw std::runtime_error("video source time invalid");
        auto tb=format->streams[stream]->time_base;
        int64_t origin=timeline_origin;
        if(anchor==AV_NOPTS_VALUE){
            seek(source_us);
            while(decode(current)){
                long double us=static_cast<long double>(pts(current))*tb.num*1000000.0L/tb.den-origin;
                if(us>=source_us){anchor=pts(current);++anchors;break;}
            }
            if(anchor==AV_NOPTS_VALUE)throw std::runtime_error("video source in has no actual frame");
            decode(next);
        }
        // -ss后首有效PTS为锚；目标fps按实际PTS round-near，不按平均源fps猜VFR索引。
        auto output_position=[&](const AVFrame* frame){
            long double value=static_cast<long double>(pts(frame)-anchor)*tb.num*profile->frame_rate_num*sd/(static_cast<long double>(tb.den)*profile->frame_rate_den*sn);
            return static_cast<int64_t>(std::floor(value+0.5L));
        };
        if(position<last_position||position>last_position+120){
            long double target=static_cast<long double>(anchor)*tb.num*1000000.0L/tb.den-origin+static_cast<long double>(position)*profile->frame_rate_den*1000000.0L*sn/(profile->frame_rate_num*static_cast<long double>(sd));
            seek(std::max<int64_t>(source_us,static_cast<int64_t>(target)-1000000));
            if(!decode(current))throw std::runtime_error("video seek returned no frame");
            decode(next);
            if(output_position(current)>position){++fallbacks;seek(source_us);if(!decode(current))throw std::runtime_error("video source anchor unavailable");decode(next);}
        }
        while(next->buf[0]&&output_position(next)<=position){av_frame_unref(current);av_frame_move_ref(current,next);decode(next);}
        last_position=position;
        auto result=av_frame_clone(current);if(!result)throw std::runtime_error("video frame clone allocation");
        if(!av_frame_get_side_data(result,AV_FRAME_DATA_DISPLAYMATRIX)){
            auto par=format->streams[stream]->codecpar;auto matrix=av_packet_side_data_get(par->coded_side_data,par->nb_coded_side_data,AV_PKT_DATA_DISPLAYMATRIX);
            if(matrix&&matrix->size>=9*sizeof(int32_t)){auto side=av_frame_new_side_data(result,AV_FRAME_DATA_DISPLAYMATRIX,9*sizeof(int32_t));if(!side){av_frame_free(&result);throw std::runtime_error("display matrix allocation");}std::memcpy(side->data,matrix->data,9*sizeof(int32_t));}
        }
        return result;
    }
    AVFrame* rgba(AVFrame* input){
        if(input->width<1||input->height<1||int64_t(input->width)*input->height>3840*2160)throw std::runtime_error("decoded video pixel bound");
        std::string chain;
        if(auto side=av_frame_get_side_data(input,AV_FRAME_DATA_DISPLAYMATRIX);side&&side->size>=9*sizeof(int32_t)){
            auto matrix=reinterpret_cast<const int32_t*>(side->data);
            // 与同版FFmpeg 9 fftools/cmdutils.c与ffmpeg_filter.c的autorotate顺序一致，先于fit。
            // https://github.com/FFmpeg/FFmpeg/blob/n9.0.2/fftools/ffmpeg_filter.c
            double theta=-std::round(av_display_rotation_get(matrix));theta-=360*std::floor(theta/360+0.9/360);
            if(!std::isfinite(theta))throw std::runtime_error("source display matrix invalid");
            if(std::abs(theta-90)<1)chain=matrix[3]>0?"transpose=cclock_flip,":"transpose=clock,";
            else if(std::abs(theta-180)<1){if(matrix[0]<0)chain+="hflip,";if(matrix[4]<0)chain+="vflip,";}
            else if(std::abs(theta-270)<1)chain=matrix[3]<0?"transpose=clock_flip,":"transpose=cclock,";
            else if(std::abs(theta)>1)chain="rotate="+std::to_string(theta)+"*PI/180,";
            else if(matrix[4]<0)chain="vflip,";
        }
        chain+="format=rgba";
        auto tb=source_tb;auto sar=input->sample_aspect_ratio;if(sar.num<1||sar.den<1)sar={1,1};
        std::string args="video_size="+std::to_string(input->width)+"x"+std::to_string(input->height)+":pix_fmt="+std::to_string(input->format)+":time_base="+std::to_string(tb.num)+"/"+std::to_string(tb.den)+":pixel_aspect="+std::to_string(sar.num)+"/"+std::to_string(sar.den);
        auto key=args+chain;
        if(!image_graph||image_key!=key){
            if(image_graph)avfilter_graph_free(&image_graph);av_frame_free(&image_output);image_key=key;
            image_graph=avfilter_graph_alloc();if(!image_graph)throw std::runtime_error("source orientation graph allocation");image_graph->nb_threads=1;
            if(avfilter_graph_create_filter(&image_source,avfilter_get_by_name("buffer"),"input",args.c_str(),nullptr,image_graph)<0||avfilter_graph_create_filter(&image_sink,avfilter_get_by_name("buffersink"),"output",nullptr,nullptr,image_graph)<0)throw std::runtime_error("source orientation buffer unavailable");
            auto in=avfilter_inout_alloc(),out=avfilter_inout_alloc();if(!in||!out)throw std::runtime_error("source orientation graph endpoint allocation");
            in->name=av_strdup("out");in->filter_ctx=image_sink;in->pad_idx=0;out->name=av_strdup("in");out->filter_ctx=image_source;out->pad_idx=0;
            int parsed=avfilter_graph_parse_ptr(image_graph,chain.c_str(),&in,&out,nullptr);avfilter_inout_free(&in);avfilter_inout_free(&out);
            if(parsed<0||avfilter_graph_config(image_graph,nullptr)<0)throw std::runtime_error("source orientation graph configuration failed");
            image_output=av_frame_alloc();if(!image_output)throw std::runtime_error("source oriented frame allocation");
        }
        av_frame_unref(image_output);
        if(av_buffersrc_add_frame_flags(image_source,input,AV_BUFFERSRC_FLAG_KEEP_REF)<0||av_buffersink_get_frame(image_sink,image_output)<0)throw std::runtime_error("source orientation filter failed");
        return image_output;
    }
};
std::shared_ptr<SourceTime> acquire_source(mlt_producer producer){
 auto props=mlt_producer_properties(producer);auto profile=mlt_service_profile(mlt_producer_service(producer));
 auto path=mlt_properties_get(props,"resource"),id=mlt_properties_get(props,"asset_id"),fingerprint=mlt_properties_get(props,"source_fingerprint");
 if(!path)throw std::runtime_error("video source resource missing");
 Recipe key{path,id?id:"",fingerprint?fingerprint:"",mlt_properties_get_int64(props,"source_in_us"),mlt_properties_get_int64(props,"processing_duration_frames"),mlt_properties_get_int64(props,"processing_start_frame"),mlt_properties_get_int64(props,"source_origin_us"),mlt_properties_get(props,"source_origin_us")!=nullptr,mlt_properties_get_int(props,"speed_num"),mlt_properties_get_int(props,"speed_den"),profile->frame_rate_num,profile->frame_rate_den,id&&*id&&mlt_properties_get(props,"processing_start_frame")?0:reinterpret_cast<uintptr_t>(producer)};
 auto found=source_cache.find(key);if(found!=source_cache.end()){++cache_hits;found->second->used_tick=pull_tick;return found->second;}
 ++cache_misses;
 if(source_cache.size()>=max_sources){auto victim=std::min_element(source_cache.begin(),source_cache.end(),[](const auto& a,const auto& b){return a.second->used_tick<b.second->used_tick;});victim->second->release();source_cache.erase(victim);}
 auto state=std::make_shared<SourceTime>();state->used_tick=pull_tick;source_states.insert(state.get());source_cache.emplace(std::move(key),state);return state;
}
void close_video_frame(void* ptr){auto value=static_cast<AVFrame*>(ptr);av_frame_free(&value);}
void close_video_source(void* ptr){delete static_cast<std::shared_ptr<SourceTime>*>(ptr);}
int source_image(mlt_frame frame,uint8_t** image,mlt_image_format* format,int* width,int* height,int){
    auto input=static_cast<AVFrame*>(mlt_properties_get_data(mlt_frame_properties(frame),"_aividcut_video",nullptr));
    auto holder=static_cast<std::shared_ptr<SourceTime>*>(mlt_properties_get_data(mlt_frame_properties(frame),"_aividcut_video_source",nullptr));
    auto state=holder?holder->get():nullptr;
    try{
        if(!input||!state)throw std::runtime_error("source decoded image unavailable");auto oriented=state->rgba(input);
        size_t bytes=static_cast<size_t>(oriented->width)*oriented->height*4;auto output=static_cast<uint8_t*>(mlt_pool_alloc(bytes));if(!output)throw std::runtime_error("source RGBA allocation");
        for(int row=0;row<oriented->height;++row)std::memcpy(output+static_cast<size_t>(row)*oriented->width*4,oriented->data[0]+row*oriented->linesize[0],oriented->width*4);
        auto props=mlt_frame_properties(frame);mlt_properties_set_int(props,"width",oriented->width);mlt_properties_set_int(props,"height",oriented->height);mlt_properties_set_int(props,"format",mlt_image_rgba);
        if(oriented->sample_aspect_ratio.num>0)mlt_properties_set_double(props,"aspect_ratio",av_q2d(oriented->sample_aspect_ratio));
        mlt_frame_set_image(frame,output,bytes,mlt_pool_release);*image=output;*format=mlt_image_rgba;*width=oriented->width;*height=oriented->height;return 0;
    }catch(const std::exception& error){if(state)state->release();report_source_audio_error(error.what());return 1;}
}
int source_frame(mlt_producer producer,mlt_frame* frame,int){
    std::shared_ptr<SourceTime> state;
    *frame=mlt_frame_init(mlt_producer_service(producer));if(!*frame){report_source_audio_error("source frame allocation failed");return 1;}
    auto props=mlt_frame_properties(*frame);mlt_properties_set_int(props,"test_image",1);mlt_properties_set_int(props,"test_audio",1);
    AVFrame* input=nullptr;
    try{
        state=acquire_source(producer);auto position=mlt_producer_position(producer);auto offset=mlt_properties_get_int64(mlt_producer_properties(producer),"output_offset_frames");if(offset<0||position>2147483646-offset)throw std::runtime_error("video derived window invalid");input=state->image(producer,position+offset);
        // 分配先于frame接管，避免内存不足异常让同一AVFrame被释放两次。
        auto holder=std::make_unique<std::shared_ptr<SourceTime>>(state);
        mlt_properties_set_data(props,"_aividcut_video",input,0,close_video_frame,nullptr);
        mlt_properties_set_data(props,"_aividcut_video_source",holder.release(),0,close_video_source,nullptr);
        mlt_properties_set_int(props,"width",input->width);mlt_properties_set_int(props,"height",input->height);
        mlt_properties_set_int(props,"test_image",0);mlt_properties_set_int(props,"test_audio",1);
        mlt_properties_set(props,"consumer.rescale","none");mlt_properties_set(props,"rescale.interp","none");mlt_properties_set_int(props,"distort",1);
        mlt_properties_set_double(props,"aspect_ratio",input->sample_aspect_ratio.num>0?av_q2d(input->sample_aspect_ratio):1.0);
        mlt_frame_set_position(*frame,position);mlt_frame_push_get_image(*frame,source_image);mlt_producer_prepare_next(producer);return 0;
    }catch(const std::exception& error){
        av_frame_free(&input);if(state)state->release();report_source_audio_error(error.what());
        // MLT的playlist包装会在producer返回错误时仍访问frame。返回有效占位帧
        // 使其安全退栈，Engine检查明确错误后拒绝输出，绝不交付测试卡冒充成功。
        mlt_frame_set_position(*frame,mlt_producer_position(producer));mlt_producer_prepare_next(producer);return 0;
    }
}
void close_source(mlt_producer producer){producer->child=nullptr;producer->close=nullptr;mlt_producer_close(producer);}
void* source_factory(mlt_profile profile,mlt_service_type,const char*,const void* resource){
    auto producer=mlt_producer_new(profile);if(!producer)return nullptr;
    ++created;producer->get_frame=source_frame;producer->close=reinterpret_cast<mlt_destructor>(close_source);
    auto props=mlt_producer_properties(producer);mlt_properties_set(props,"resource",static_cast<const char*>(resource));mlt_properties_set_int(props,"loader_normalized",1);return producer;
}
}
void register_source_time(mlt_repository repository){mlt_repository_register(repository,mlt_service_producer_type,"aividcut.source_time",source_factory);}
void begin_source_time_pull(){++pull_tick;}
void retire_source_time_contexts(){for(auto it=source_cache.begin();it!=source_cache.end();){if(it->second->used_tick+1<pull_tick){it->second->release();it=source_cache.erase(it);}else ++it;}}
void clear_source_time_contexts(){for(auto& entry:source_cache)entry.second->release();source_cache.clear();}
const AVFrame* source_time_decoded_frame(mlt_frame frame){return static_cast<const AVFrame*>(mlt_properties_get_data(mlt_frame_properties(frame),"_aividcut_video",nullptr));}

std::string source_time_diagnostics_json(){uint64_t active=0;for(const auto* state:source_states)if(state->format)++active;
 return "{\"created\":"+std::to_string(created)+",\"opens\":"+std::to_string(opens)+",\"decodedFrames\":"+std::to_string(decoded_frames)+",\"seeks\":"+std::to_string(seeks)+",\"anchors\":"+std::to_string(anchors)+",\"fallbacks\":"+std::to_string(fallbacks)+",\"activeDecoders\":"+std::to_string(active)+",\"cacheHits\":"+std::to_string(cache_hits)+",\"cacheMisses\":"+std::to_string(cache_misses)+",\"residentStates\":"+std::to_string(source_cache.size())+"}";
}
