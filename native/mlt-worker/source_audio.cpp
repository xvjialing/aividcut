#include "source_audio.h"
#include "audio_probe.h"
#include "rational_samples.h"
#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstring>
#include <numeric>
#include <map>
#include <memory>
#include <tuple>
#include <stdexcept>
#include <string>
#include <vector>
extern "C" {
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavfilter/avfilter.h>
#include <libavfilter/buffersrc.h>
#include <libavfilter/buffersink.h>
#include <libavutil/channel_layout.h>
#include <libavutil/opt.h>
#include <libavutil/samplefmt.h>
}

namespace {
constexpr int output_rate=48000;
constexpr size_t max_queue_frames=192000;
using Clock=std::chrono::steady_clock;
std::string last_error;
struct SourceAudio;
uint64_t cache_hits=0,cache_misses=0,opens=0,decoder_resets=0,graph_builds=0,decoded_frames=0,gated_frames=0,peak_queue_frames=0;
constexpr size_t max_states=8;
struct Recipe {
    std::string path,asset_id,fingerprint;
    int64_t start_n,start_d,end_n,end_d,processing_n,processing_d;
    int speed_n,speed_d;
    auto fields()const{return std::tie(path,asset_id,fingerprint,start_n,start_d,end_n,end_d,processing_n,processing_d,speed_n,speed_d);}
    bool operator<(const Recipe& other)const{return fields()<other.fields();}
};
std::map<Recipe,std::unique_ptr<SourceAudio>> source_states;
uint64_t pull_tick=0;
int64_t sample_time(mlt_properties props,const char* numerator,const char* denominator){
    auto n=mlt_properties_get_int64(props,numerator),d=mlt_properties_get_int64(props,denominator);
    if(d<=0)throw std::runtime_error("audio rational denominator invalid");
    return aividcut_samples::signed_microsecond_sample(n,d);
}
struct SourceAudio {
    AVFormatContext* format=nullptr;AVCodecContext* codec=nullptr;AVPacket* packet=nullptr;AVFrame* frame=nullptr;AVFrame* filtered=nullptr;
    AVFilterGraph* graph=nullptr;AVFilterContext* source=nullptr;AVFilterContext* sink=nullptr;
    int stream=-1,input_rate=0;int64_t source_a=0,source_b=0,processing_a=0;
    int64_t raw_anchor=0,decode_out_anchor=0,next_output=0,queue_start=0,keep_from=0,expected_raw=-1;
    bool demux_eof=false,codec_flushed=false,graph_flushed=false,output_eof=false,first=true;
    std::vector<float> queue;Clock::time_point started;std::string path;AudioOrigin origin;uint64_t used_tick=0;
    void release(){reset();av_frame_free(&frame);av_frame_free(&filtered);av_packet_free(&packet);avcodec_free_context(&codec);if(format)avformat_close_input(&format);std::vector<float>().swap(queue);stream=-1;input_rate=0;}
    ~SourceAudio(){release();}
    static int interrupt(void* p){return Clock::now()-static_cast<SourceAudio*>(p)->started>std::chrono::seconds(10);}
    void reset(){if(graph)avfilter_graph_free(&graph);source=sink=nullptr;queue.clear();queue_start=next_output=0;demux_eof=codec_flushed=graph_flushed=output_eof=false;first=true;expected_raw=-1;}
    void open(mlt_properties props){
        if(format)return;
        const char* resource=mlt_properties_get(props,"resource");if(!resource)throw std::runtime_error("audio source resource missing");path=resource;
        origin=probe_audio_origin(path);if(!origin.has_audio)throw std::runtime_error("audio source has no decoded samples");
        format=avformat_alloc_context();if(!format)throw std::runtime_error("audio format allocation");format->interrupt_callback={interrupt,this};
        AVDictionary* options=nullptr;av_dict_set(&options,"protocol_whitelist","file,pipe",0);
        int opened=avformat_open_input(&format,path.c_str(),nullptr,&options);av_dict_free(&options);
        if(opened<0||avformat_find_stream_info(format,nullptr)<0)throw std::runtime_error("audio source open failed");
        for(unsigned i=0;i<format->nb_streams;++i)if(format->streams[i]->codecpar->codec_type==AVMEDIA_TYPE_AUDIO){stream=static_cast<int>(i);break;}
        if(stream<0)throw std::runtime_error("audio stream missing");auto decoder=avcodec_find_decoder(format->streams[stream]->codecpar->codec_id);
        if(!decoder)throw std::runtime_error("audio decoder missing");codec=avcodec_alloc_context3(decoder);
        if(!codec||avcodec_parameters_to_context(codec,format->streams[stream]->codecpar)<0)throw std::runtime_error("audio codec parameters");
        codec->pkt_timebase=format->streams[stream]->time_base;
        codec->thread_count=1;if(avcodec_open2(codec,decoder,nullptr)<0)throw std::runtime_error("audio codec initialization");input_rate=codec->sample_rate;
        frame=av_frame_alloc();filtered=av_frame_alloc();packet=av_packet_alloc();if(!frame||!filtered||!packet||input_rate<1)throw std::runtime_error("audio frame allocation");
        auto source_n=mlt_properties_get_int64(props,"source_start_num"),source_d=mlt_properties_get_int64(props,"source_start_den");
        source_a=aividcut_samples::microsecond_sample(source_n,source_d,origin.start_us);
        source_b=aividcut_samples::microsecond_sample(mlt_properties_get_int64(props,"source_end_num"),mlt_properties_get_int64(props,"source_end_den"),origin.start_us);
        processing_a=sample_time(props,"processing_start_num","processing_start_den");
        if(aividcut_samples::before_origin(source_n,source_d,origin.start_us)){
            processing_a=aividcut_samples::shifted_start(mlt_properties_get_int64(props,"processing_start_num"),mlt_properties_get_int64(props,"processing_start_den"),source_n,source_d,origin.start_us,mlt_properties_get_int(props,"speed_num"),mlt_properties_get_int(props,"speed_den"));source_a=0;
        }
        reset_decoder(true);++opens;
    }
    void reset_decoder(bool fresh=false){
        ++decoder_resets;reset();int64_t format_origin=format->start_time==AV_NOPTS_VALUE?0:format->start_time;
        // 解码预卷最多一秒用于重采样器；atempo始终从完整clip源起点重建，绝不按稿估时或预渲染整段。
        int64_t source_us=std::max<int64_t>(0,origin.start_us+av_rescale(source_a,1000000,output_rate)-1000000);
        // 新打开且靠近文件开头时直接读原始包，保留AAC priming/skip samples。
        if(fresh&&source_us==0)return;
        auto target=av_rescale_q(source_us+format_origin,AVRational{1,1000000},format->streams[stream]->time_base);
        if(av_seek_frame(format,stream,target,AVSEEK_FLAG_BACKWARD)<0)throw std::runtime_error("audio source seek failed");avcodec_flush_buffers(codec);
    }
    bool decode(){
        av_frame_unref(frame);
        for(int attempt=0;attempt<1000000&&!interrupt(this);++attempt){
            int result=avcodec_receive_frame(codec,frame);if(result==0){++decoded_frames;return true;}if(result==AVERROR_EOF)return false;
            if(result!=AVERROR(EAGAIN))throw std::runtime_error("audio decode failed");
            if(demux_eof){if(codec_flushed)return false;avcodec_send_packet(codec,nullptr);codec_flushed=true;continue;}
            int read=av_read_frame(format,packet);if(read<0){demux_eof=true;continue;}
            if(packet->stream_index==stream){int sent=avcodec_send_packet(codec,packet);av_packet_unref(packet);if(sent<0&&sent!=AVERROR(EAGAIN))throw std::runtime_error("audio packet decode failed");}
            else av_packet_unref(packet);
        }
        throw std::runtime_error("audio pull exceeded bounded decode budget");
    }
    void build_graph(mlt_properties props){
        graph=avfilter_graph_alloc();if(!graph)throw std::runtime_error("audio filter allocation");graph->nb_threads=1;
        char layout[128];av_channel_layout_describe(&frame->ch_layout,layout,sizeof(layout));
        std::string args="time_base=1/"+std::to_string(input_rate)+":sample_rate="+std::to_string(input_rate)+":sample_fmt="+av_get_sample_fmt_name(static_cast<AVSampleFormat>(frame->format))+":channel_layout="+layout;
        if(avfilter_graph_create_filter(&source,avfilter_get_by_name("abuffer"),"input",args.c_str(),nullptr,graph)<0||avfilter_graph_create_filter(&sink,avfilter_get_by_name("abuffersink"),"output",nullptr,nullptr,graph)<0)throw std::runtime_error("audio buffer filters unavailable");
        auto inputs=avfilter_inout_alloc(),outputs=avfilter_inout_alloc();if(!inputs||!outputs)throw std::runtime_error("audio graph endpoints allocation");
        outputs->name=av_strdup("in");outputs->filter_ctx=source;outputs->pad_idx=0;
        inputs->name=av_strdup("out");inputs->filter_ctx=sink;inputs->pad_idx=0;
        std::string chain="aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo,atrim=start_sample="+std::to_string(std::max<int64_t>(0,source_a-decode_out_anchor))+":end_sample="+std::to_string(std::max<int64_t>(0,source_b-decode_out_anchor))+",asetpts=N/SR/TB";
        int sn=mlt_properties_get_int(props,"speed_num"),sd=mlt_properties_get_int(props,"speed_den");if(sn<1||sd<1)throw std::runtime_error("audio speed invalid");
        double speed=double(sn)/sd;
        while(speed>2){chain+=",atempo=2";speed/=2;}while(speed<0.5){chain+=",atempo=0.5";speed*=2;}
        if(sn!=sd)chain+=",atempo="+std::to_string(speed);
        chain+=",aformat=sample_fmts=flt:sample_rates=48000:channel_layouts=stereo";
        int parsed=avfilter_graph_parse_ptr(graph,chain.c_str(),&inputs,&outputs,nullptr);avfilter_inout_free(&inputs);avfilter_inout_free(&outputs);
        if(parsed<0||avfilter_graph_config(graph,nullptr)<0)throw std::runtime_error("audio atempo/resample graph initialization failed");++graph_builds;
    }
    void feed(mlt_properties props){
        if(!decode()){
            if(graph&&!graph_flushed){int result=av_buffersrc_add_frame_flags(source,nullptr,0);if(result<0&&result!=AVERROR_EOF)throw std::runtime_error("audio filter drain rejected");graph_flushed=true;}
            else output_eof=true;
            return;
        }
        auto pts=frame->best_effort_timestamp;if(pts==AV_NOPTS_VALUE)pts=frame->pts;if(pts==AV_NOPTS_VALUE||frame->sample_rate!=input_rate||frame->nb_samples<1)throw std::runtime_error("audio frame has no actual clock");
        auto tb=format->streams[stream]->time_base;int64_t format_origin=format->start_time==AV_NOPTS_VALUE?0:format->start_time;
        auto raw=av_rescale_q(pts,tb,AVRational{1,input_rate})-av_rescale_q(format_origin+origin.start_us,AVRational{1,1000000},AVRational{1,input_rate});
        if(expected_raw>=0&&std::llabs(raw-expected_raw)>std::max<int64_t>(1,input_rate/1000))throw std::runtime_error("audio source internal PTS gap/overlap");expected_raw=raw+frame->nb_samples;
        if(first){
            // 对齐重采样有理周期，seek/切开同源时不重置44.1->48k相位。
            int period=input_rate/std::gcd(input_rate,output_rate);
            raw_anchor=av_rescale_rnd(std::max<int64_t>(0,raw),1,period,AV_ROUND_UP)*period;
            if(raw+frame->nb_samples<=raw_anchor)return;
            int skip=static_cast<int>(std::max<int64_t>(0,raw_anchor-raw));
            if(skip){if(av_frame_make_writable(frame)<0||av_samples_copy(frame->extended_data,frame->extended_data,0,skip,frame->nb_samples-skip,frame->ch_layout.nb_channels,static_cast<AVSampleFormat>(frame->format))<0)throw std::runtime_error("audio seek alignment failed");frame->nb_samples-=skip;raw+=skip;}
            decode_out_anchor=av_rescale(raw_anchor,output_rate,input_rate);if(decode_out_anchor>source_a)throw std::runtime_error("audio seek missed requested source in");
            build_graph(props);first=false;
        }
        frame->pts=raw-raw_anchor;
        if(av_buffersrc_add_frame_flags(source,frame,AV_BUFFERSRC_FLAG_KEEP_REF)<0)throw std::runtime_error("audio filter input rejected");
    }
    void fill(mlt_properties props,int64_t through){
        while(!output_eof&&next_output<through){
            if(interrupt(this))throw std::runtime_error("audio seek replay exceeded decode budget");
            if(!graph){feed(props);continue;}
            av_frame_unref(filtered);int result=av_buffersink_get_frame(sink,filtered);
            if(result==AVERROR(EAGAIN)){feed(props);continue;}if(result==AVERROR_EOF){output_eof=true;break;}if(result<0)throw std::runtime_error("audio filter output failed");
            if(filtered->format!=AV_SAMPLE_FMT_FLT||filtered->ch_layout.nb_channels!=2||filtered->sample_rate!=output_rate)throw std::runtime_error("audio output clock/format mismatch");
            int64_t begin=next_output,end=begin+filtered->nb_samples;next_output=end;
            if(end<=keep_from)continue;
            int64_t skip=std::max<int64_t>(0,keep_from-begin);auto samples=reinterpret_cast<float*>(filtered->data[0]);
            if(queue.empty())queue_start=begin+skip;
            if(filtered->nb_samples<1||static_cast<size_t>(filtered->nb_samples)>max_queue_frames||queue.size()/2+filtered->nb_samples-skip>max_queue_frames)throw std::runtime_error("audio filter queue exceeds bounded cache");
            queue.insert(queue.end(),samples+skip*2,samples+filtered->nb_samples*2);peak_queue_frames=std::max(peak_queue_frames,static_cast<uint64_t>(queue.size()/2));
        }
    }
    void read(mlt_properties props,int64_t global_start,int wanted,float* output){
        used_tick=pull_tick;started=Clock::now();open(props);int64_t a=std::max(std::max(global_start,sample_time(props,"timeline_start_num","timeline_start_den")),processing_a),b=std::min(global_start+wanted,sample_time(props,"timeline_end_num","timeline_end_den"));
        if(a>=b||source_a>=source_b)return;
        int64_t local=a-processing_a,end=b-processing_a;
        if(local<queue_start){release();open(props);}
        keep_from=std::max<int64_t>(0,local-output_rate);
        if(!queue.empty()&&queue_start<keep_from){auto skip=std::min<int64_t>(keep_from-queue_start,queue.size()/2);queue.erase(queue.begin(),queue.begin()+skip*2);queue_start+=skip;}
        fill(props,end);
        auto available=static_cast<int64_t>(queue.size()/2);int64_t begin=std::max(local,queue_start),last=std::min(end,queue_start+available);
        if(last>begin)std::memcpy(output+(a-global_start+begin-local)*2,queue.data()+(begin-queue_start)*2,static_cast<size_t>(last-begin)*2*sizeof(float));
    }
};
Recipe recipe(mlt_properties props){
    auto path=mlt_properties_get(props,"resource"),id=mlt_properties_get(props,"asset_id"),fingerprint=mlt_properties_get(props,"source_fingerprint");
    if(!path||!id||!*id)throw std::runtime_error("audio processing identity missing");
    return {path,id,fingerprint?fingerprint:"",mlt_properties_get_int64(props,"source_start_num"),mlt_properties_get_int64(props,"source_start_den"),mlt_properties_get_int64(props,"source_end_num"),mlt_properties_get_int64(props,"source_end_den"),mlt_properties_get_int64(props,"processing_start_num"),mlt_properties_get_int64(props,"processing_start_den"),mlt_properties_get_int(props,"speed_num"),mlt_properties_get_int(props,"speed_den")};
}
SourceAudio* acquire(mlt_properties props){
    auto key=recipe(props);auto found=source_states.find(key);
    if(found!=source_states.end()){++cache_hits;found->second->used_tick=pull_tick;return found->second.get();}
    ++cache_misses;
    if(source_states.size()>=max_states){
        auto victim=std::min_element(source_states.begin(),source_states.end(),[](const auto& a,const auto& b){return a.second->used_tick<b.second->used_tick;});
        source_states.erase(victim);
    }
    auto state=std::make_unique<SourceAudio>();state->used_tick=pull_tick;auto pointer=state.get();
    source_states.emplace(std::move(key),std::move(state));return pointer;
}
int get_audio(mlt_frame frame,void** buffer,mlt_audio_format* format,int* frequency,int* channels,int* samples){
    auto filter=static_cast<mlt_filter>(mlt_frame_pop_audio(frame));auto props=mlt_filter_properties(filter);
    SourceAudio* state=nullptr;
    auto evidence=mlt_frame_unique_properties(frame,mlt_filter_service(filter));auto position=mlt_properties_get_int64(evidence,"local_position");
    auto profile=mlt_service_profile(mlt_filter_service(filter));int64_t global=position+mlt_properties_get_int64(props,"canvas_start_frame");
    auto begin=source_audio_frame_sample(global,profile->frame_rate_num,profile->frame_rate_den,output_rate);
    auto end=source_audio_frame_sample(global+1,profile->frame_rate_num,profile->frame_rate_den,output_rate);int wanted=static_cast<int>(end-begin);
    if(wanted<1||wanted>131072){report_source_audio_error("source audio sample bound");return 1;}size_t bytes=static_cast<size_t>(wanted)*2*sizeof(float);auto output=static_cast<float*>(mlt_pool_alloc(bytes));if(!output){report_source_audio_error("source audio allocation failed");return 1;}std::memset(output,0,bytes);
    // MLT的get_audio包装不传播回调error；保留安全零缓冲，主线程检查last_error后拒绝整帧。
    try{
        // 完全不在真实输出窗内的占位帧不查缓存、不打开解码器。
        if(end<=sample_time(props,"timeline_start_num","timeline_start_den")||begin>=sample_time(props,"timeline_end_num","timeline_end_den")){++gated_frames;}
        else{state=acquire(props);state->read(props,begin,wanted,output);
            // 淡化只处理本路新输出缓冲，不修改可被其它片段复用的源PCM。
            if(mlt_properties_get_int(props,"audio_fade_enabled")){
                double a=mlt_properties_get_double(props,"audio_fade_start"),b=mlt_properties_get_double(props,"audio_fade_end");
                double in=mlt_properties_get_double(props,"audio_fade_in"),out=mlt_properties_get_double(props,"audio_fade_out");
                if(!std::isfinite(a)||!std::isfinite(b)||!std::isfinite(in)||!std::isfinite(out)||b<=a||in<0||out<0)throw std::runtime_error("invalid audio fade envelope");
                for(int i=0;i<wanted;++i){double at=static_cast<double>(begin+i),gain=1;
                    if(in>0)gain=std::min(gain,(at-a)/in);if(out>0)gain=std::min(gain,(b-at)/out);
                    gain=std::clamp(gain,0.0,1.0);output[i*2]=static_cast<float>(output[i*2]*gain);output[i*2+1]=static_cast<float>(output[i*2+1]*gain);
                }
            }
        }
    }catch(const std::exception& error){if(state)state->release();last_error=error.what();}
    *format=mlt_audio_f32le;*frequency=output_rate;*channels=2;*samples=wanted;*buffer=output;
    mlt_frame_set_audio(frame,output,mlt_audio_f32le,bytes,mlt_pool_release);return 0;
}
mlt_frame process(mlt_filter filter,mlt_frame frame){
    auto props=mlt_frame_unique_properties(frame,mlt_filter_service(filter));mlt_properties_set_int64(props,"local_position",mlt_frame_original_position(frame));
    mlt_properties_set_int(mlt_frame_properties(frame),"test_audio",0);
    mlt_frame_push_audio(frame,filter);mlt_frame_push_audio(frame,reinterpret_cast<void*>(get_audio));return frame;
}
void* factory(mlt_profile,mlt_service_type,const char*,const void*){auto filter=mlt_filter_new();if(filter)filter->process=process;return filter;}
}
int64_t source_audio_frame_sample(int64_t frame,int n,int d,int rate){return av_rescale_rnd(frame,int64_t(d)*rate,n,AV_ROUND_UP);}
void register_source_audio(mlt_repository repository){mlt_repository_register(repository,mlt_service_filter_type,"aividcut.audio_source",factory);}
bool source_audio_available(){return avfilter_get_by_name("atempo")&&avfilter_get_by_name("aresample");}
void clear_source_audio_error(){last_error.clear();}
void report_source_audio_error(const std::string& reason){last_error=reason;}
std::string source_audio_error(){return last_error;}
void begin_source_audio_pull(){++pull_tick;}
void retire_source_audio_contexts(){
    for(auto it=source_states.begin();it!=source_states.end();){if(it->second->used_tick+1<pull_tick)it=source_states.erase(it);else ++it;}
}
void clear_source_audio_contexts(){source_states.clear();}
std::string source_audio_diagnostics_json(){
    uint64_t active=0,queued=0;for(const auto& entry:source_states){if(entry.second->format)++active;queued+=entry.second->queue.size()/2;}
    return "{\"cacheHits\":"+std::to_string(cache_hits)+",\"cacheMisses\":"+std::to_string(cache_misses)+",\"opens\":"+std::to_string(opens)+",\"decoderResets\":"+std::to_string(decoder_resets)+",\"graphBuilds\":"+std::to_string(graph_builds)+",\"decodedFrames\":"+std::to_string(decoded_frames)+",\"gatedFrames\":"+std::to_string(gated_frames)+",\"residentStates\":"+std::to_string(source_states.size())+",\"activeDecoders\":"+std::to_string(active)+",\"queuedSampleFrames\":"+std::to_string(queued)+",\"peakQueueSampleFrames\":"+std::to_string(peak_queue_frames)+"}";
}
