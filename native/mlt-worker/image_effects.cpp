#include "image_effects.h"
#include "source_time.h"
#include <algorithm>
#include <atomic>
#include <cmath>
#include <chrono>
#include <cstdint>
#include <cstring>
#include <filesystem>
#include <iomanip>
#include <list>
#include <memory>
#include <mutex>
#include <sstream>
#include <stdexcept>
#include <string>
#include <vector>
extern "C" {
#include <libavfilter/avfilter.h>
#include <libavfilter/buffersink.h>
#include <libavfilter/buffersrc.h>
#include <libavutil/imgutils.h>
#include <libavutil/display.h>
#include <libavutil/opt.h>
}

namespace {
constexpr int64_t max_source_pixels=64LL*1024*1024;
constexpr int64_t max_scale_pixels=40LL*1024*1024;
constexpr int64_t max_visible_pixels=24LL*1024*1024;
constexpr uint64_t graph_budget=1024ULL*1024*1024;
constexpr size_t graph_count=8;

std::string numeric(double value){std::ostringstream out;out<<std::fixed<<std::setprecision(9)<<value;return out.str();}
template<class Integer>std::string integral_text(Integer value){bool negative=value<0;std::string result;do{int digit=static_cast<int>(value%10);result+=char('0'+(digit<0?-digit:digit));value/=10;}while(value!=0);if(negative)result+='-';std::reverse(result.begin(),result.end());return result;}
void checked(int status,const char* reason){if(status<0)throw std::runtime_error(reason);}
struct FrameCloser{void operator()(AVFrame* frame)const{av_frame_free(&frame);}};
using Frame=std::unique_ptr<AVFrame,FrameCloser>;

struct Parameters {
    int source_w=0,source_h=0,canvas_w=0,canvas_h=0,scale_w=0,scale_h=0;
    int source_format=AV_PIX_FMT_RGBA,sar_num=1,sar_den=1,transpose=-1;
    int source_space=0,source_range=0,source_primaries=0,source_trc=0;
    bool source_hflip=false,source_vflip=false;
    double source_rotation=0;
    double rotation=0,x=0,y=0,intensity=0;
    std::string lut;
};

void native_input(Parameters& p,const AVFrame* frame){
    p.source_format=frame->format;p.sar_num=frame->sample_aspect_ratio.num>0?frame->sample_aspect_ratio.num:1;p.sar_den=frame->sample_aspect_ratio.den>0?frame->sample_aspect_ratio.den:1;
    p.source_space=frame->colorspace;p.source_range=frame->color_range;p.source_primaries=frame->color_primaries;p.source_trc=frame->color_trc;
    if(auto side=av_frame_get_side_data(frame,AV_FRAME_DATA_DISPLAYMATRIX);side&&side->size>=9*sizeof(int32_t)){
        auto matrix=reinterpret_cast<const int32_t*>(side->data);double theta=-std::round(av_display_rotation_get(matrix));theta-=360*std::floor(theta/360+0.9/360);
        if(!std::isfinite(theta))throw std::runtime_error("source display matrix invalid");
        if(std::abs(theta-90)<1)p.transpose=matrix[3]>0?0:1;
        else if(std::abs(theta-180)<1){p.source_hflip=matrix[0]<0;p.source_vflip=matrix[4]<0;}
        else if(std::abs(theta-270)<1)p.transpose=matrix[3]<0?3:2;
        else if(std::abs(theta)>1)p.source_rotation=theta;
        else p.source_vflip=matrix[4]<0;
    }
}

Parameters parameters(mlt_transition transition,int source_w,int source_h){
    auto props=mlt_transition_properties(transition);Parameters p;
    p.source_w=source_w;p.source_h=source_h;
    p.canvas_w=mlt_properties_get_int(props,"canvas_width");p.canvas_h=mlt_properties_get_int(props,"canvas_height");
    p.scale_w=mlt_properties_get_int(props,"scale_width");p.scale_h=mlt_properties_get_int(props,"scale_height");
    p.rotation=mlt_properties_get_double(props,"rotation");p.x=mlt_properties_get_double(props,"offset_x");p.y=mlt_properties_get_double(props,"offset_y");p.intensity=mlt_properties_get_double(props,"lut_intensity");
    if(auto file=mlt_properties_get(props,"lut_file"))p.lut=file;
    if(source_w<1||source_h<1||int64_t(source_w)*source_h>max_source_pixels||p.canvas_w<1||p.canvas_h<1||int64_t(p.canvas_w)*p.canvas_h>3840*2160||p.scale_w<2||p.scale_h<2||p.scale_w>16384||p.scale_h>16384||int64_t(p.scale_w)*p.scale_h>max_scale_pixels)
        throw std::runtime_error("visual source or scale surface exceeds bounded pixels");
    if(!std::isfinite(p.rotation)||std::abs(p.rotation)>360000||!std::isfinite(p.x)||!std::isfinite(p.y)||!std::isfinite(p.intensity)||p.intensity<0||p.intensity>1||(p.intensity>0&&p.lut.empty()))
        throw std::runtime_error("visual transform or LUT parameters invalid");
    return p;
}

class Graph {
public:
    AVFilterGraph* graph=nullptr;AVFilterContext* input=nullptr;AVFilterContext* output=nullptr;
    int full_w=0,full_h=0,crop_x=0,crop_y=0;uint64_t estimate=0;int64_t sequence=0;
    ~Graph(){avfilter_graph_free(&graph);}
    AVFilterContext* node(const char* name,const std::vector<std::pair<std::string,std::string>>& options={}){
        auto filter=avfilter_get_by_name(name);if(!filter)throw std::runtime_error("required libavfilter visual filter unavailable");
        auto context=avfilter_graph_alloc_filter(graph,filter,nullptr);if(!context)throw std::runtime_error("visual filter allocation failed");
        for(const auto& option:options)checked(av_opt_set(context,option.first.c_str(),option.second.c_str(),AV_OPT_SEARCH_CHILDREN),"visual filter option rejected");
        checked(avfilter_init_str(context,nullptr),"visual filter initialization failed");return context;
    }
    void link(AVFilterContext* a,int out,AVFilterContext* b,int in){checked(avfilter_link(a,out,b,in),"visual filter link failed");}
    void configure(const Parameters& p,int roi_w=0,int roi_h=0){
        avfilter_graph_free(&graph);graph=avfilter_graph_alloc();if(!graph)throw std::runtime_error("visual graph allocation failed");graph->nb_threads=1;
        input=node("buffer",{{"video_size",std::to_string(p.source_w)+"x"+std::to_string(p.source_h)},{"pix_fmt",std::to_string(p.source_format)},{"time_base","1/1"},{"pixel_aspect",std::to_string(p.sar_num)+"/"+std::to_string(p.sar_den)}});
        AVFilterContext* oriented=input;
        if(p.transpose>=0){auto transform=node("transpose",{{"dir",std::to_string(p.transpose)}});link(oriented,0,transform,0);oriented=transform;}
        for(auto option:{std::make_pair(p.source_hflip,"hflip"),std::make_pair(p.source_vflip,"vflip")})if(option.first){auto transform=node(option.second);link(oriented,0,transform,0);oriented=transform;}
        if(p.source_rotation!=0){auto transform=node("rotate",{{"angle",numeric(p.source_rotation)+"*PI/180"}});link(oriented,0,transform,0);oriented=transform;}
        auto scale=node("scale",{{"w",std::to_string(p.scale_w)},{"h",std::to_string(p.scale_h)},{"force_original_aspect_ratio","decrease"}});link(oriented,0,scale,0);
        auto sar=node("setsar",{{"sar","1"}});link(scale,0,sar,0);
        auto format=node("format",{{"pix_fmts","rgba"}});link(sar,0,format,0);AVFilterContext* previous=format;
        if(p.rotation!=0||roi_w>0){
            auto angle=numeric(p.rotation)+"*PI/180";
            auto rotate=node("rotate",{{"angle",angle},{"ow",roi_w>0?std::to_string(roi_w):"rotw("+angle+")"},{"oh",roi_h>0?std::to_string(roi_h):"roth("+angle+")"},{"c","none"},{"bilinear","1"}});
            link(previous,0,rotate,0);previous=rotate;
        }
        if(p.intensity>0){
            auto lut=node("lut3d",{{"file",p.lut},{"interp","tetrahedral"}});
            if(p.intensity==1){link(previous,0,lut,0);previous=lut;}
            else{
                auto split=node("split",{{"outputs","2"}});link(previous,0,split,0);
                // normal的opacity是上层原画面A的权重，避免每个分量执行表达式求值。
                auto blend=node("blend",{{"all_mode","normal"},{"all_opacity",numeric(1-p.intensity)}});
                link(split,0,blend,0);link(split,1,lut,0);link(lut,0,blend,1);previous=blend;
            }
        }
        auto rgba=node("format",{{"pix_fmts","rgba"}});link(previous,0,rgba,0);output=node("buffersink");link(rgba,0,output,0);
        checked(avfilter_graph_config(graph,nullptr),"visual graph negotiation failed");
    }
    void initialize(const Parameters& p){
        configure(p);full_w=output->inputs[0]->w;full_h=output->inputs[0]->h;
        if(full_w<1||full_h<1||full_w>32768||full_h>32768)throw std::runtime_error("rotated extent exceeds bounded dimensions");
        auto need_w=std::min<long double>(full_w,p.canvas_w+2*std::ceil(std::abs(static_cast<long double>(p.x)))+4);
        auto need_h=std::min<long double>(full_h,p.canvas_h+2*std::ceil(std::abs(static_cast<long double>(p.y)))+4);
        int w=static_cast<int>(need_w),h=static_cast<int>(need_h);
        if((full_w-w)&1)++w;if((full_h-h)&1)++h;
        if(int64_t(w)*h>max_visible_pixels)throw std::runtime_error("visible rotated ROI exceeds 24Mi pixels; reduce scale or offset");
        crop_x=(full_w-w)/2;crop_y=(full_h-h)/2;
        if(w!=full_w||h!=full_h)configure(p,w,h);
        estimate=uint64_t(p.source_w)*p.source_h*4+uint64_t(p.scale_w)*p.scale_h*8+uint64_t(w)*h*24+uint64_t(p.canvas_w)*p.canvas_h*4;
        if(estimate>graph_budget)throw std::runtime_error("visual graph exceeds 1GiB estimated working-surface budget");
    }
    Frame apply(const Parameters& p,const uint8_t* pixels,const AVFrame* native=nullptr){
        Frame frame(native?av_frame_clone(native):av_frame_alloc());if(!frame)throw std::runtime_error("visual frame allocation failed");
        if(!native){frame->format=AV_PIX_FMT_RGBA;frame->width=p.source_w;frame->height=p.source_h;frame->sample_aspect_ratio={1,1};checked(av_frame_get_buffer(frame.get(),32),"visual input buffer allocation failed");for(int y=0;y<p.source_h;++y)std::memcpy(frame->data[0]+y*frame->linesize[0],pixels+size_t(y)*p.source_w*4,size_t(p.source_w)*4);}
        frame->pts=sequence++;frame->time_base={1,1};
        checked(av_buffersrc_add_frame_flags(input,frame.get(),AV_BUFFERSRC_FLAG_KEEP_REF),"visual frame submission failed");
        Frame result(av_frame_alloc());if(!result)throw std::runtime_error("visual output allocation failed");
        checked(av_buffersink_get_frame(output,result.get()),"visual frame output failed");
        if(result->format!=AV_PIX_FMT_RGBA||result->width<1||result->height<1||int64_t(result->width)*result->height>max_visible_pixels)throw std::runtime_error("visual output format or extent invalid");
        return result;
    }
};

struct CachedGraph{std::string key;std::shared_ptr<Graph> graph;};
std::list<CachedGraph> cache;
std::mutex cache_mutex;
std::atomic<uint64_t> builds{0},hits{0},calls{0},fast_path_calls{0},filter_ns{0},composite_ns{0};
std::string key(const Parameters& p){
    std::ostringstream out;out<<p.source_w<<':'<<p.source_h<<':'<<p.source_format<<':'<<p.sar_num<<':'<<p.sar_den<<':'<<p.transpose<<':'<<p.source_hflip<<':'<<p.source_vflip<<':'<<numeric(p.source_rotation)<<':'<<p.source_space<<':'<<p.source_range<<':'<<p.source_primaries<<':'<<p.source_trc<<':'<<p.canvas_w<<':'<<p.canvas_h<<':'<<p.scale_w<<':'<<p.scale_h<<':'<<numeric(p.rotation)<<':'<<numeric(std::abs(p.x))<<':'<<numeric(std::abs(p.y))<<':'<<numeric(p.intensity)<<':'<<p.lut;
    if(p.intensity>0){std::error_code error;auto size=std::filesystem::file_size(p.lut,error);if(error)throw std::runtime_error("LUT file unavailable");auto modified=std::filesystem::last_write_time(p.lut,error);if(error)throw std::runtime_error("LUT timestamp unavailable");out<<':'<<size<<':'<<integral_text(modified.time_since_epoch().count());}
    return out.str();
}
std::shared_ptr<Graph> acquire(const Parameters& p){
    auto id=key(p);std::lock_guard<std::mutex> lock(cache_mutex);
    for(auto it=cache.begin();it!=cache.end();++it)if(it->key==id){auto result=it->graph;cache.splice(cache.begin(),cache,it);++hits;return result;}
    auto result=std::make_shared<Graph>();result->initialize(p);
    ++builds;
    uint64_t bytes=result->estimate;for(const auto& entry:cache)bytes+=entry.graph->estimate;
    while(!cache.empty()&&(cache.size()>=graph_count||bytes>graph_budget)){bytes-=cache.back().graph->estimate;cache.pop_back();}
    cache.push_front({id,result});return result;
}

// 与导出默认yuv420 overlay一致，将摆放坐标落到2像素的色度网格。
int overlay_position(double value){if(value>10000000||value<-10000000)throw std::runtime_error("visual offset outside bounded coordinates");return static_cast<int>(value)&~1;}
void over(uint8_t* destination,const uint8_t* source){
    unsigned alpha=source[3];if(alpha==0)return;if(alpha==255){std::memcpy(destination,source,4);return;}
    if(destination[3]==255){for(int c=0;c<3;++c)destination[c]=static_cast<uint8_t>((source[c]*alpha+destination[c]*(255-alpha)+127)/255);return;}
    unsigned combined=alpha*255+destination[3]*(255-alpha);
    for(int c=0;c<3;++c)destination[c]=static_cast<uint8_t>((source[c]*alpha*255+destination[c]*destination[3]*(255-alpha)+combined/2)/combined);
    destination[3]=static_cast<uint8_t>((combined+127)/255);
}
int get_image(mlt_frame frame,uint8_t** image,mlt_image_format* format,int* width,int* height,int){
    // push_frame与push_service共用MLT服务栈，按压栈逆序先取B，再取transition。
    auto b=mlt_frame_pop_frame(frame);auto transition=static_cast<mlt_transition>(mlt_frame_pop_service(frame));
    try{
        *format=mlt_image_rgba;checked(mlt_frame_get_image(frame,image,format,width,height,1),"visual background read failed");if(!*image||*format!=mlt_image_rgba)throw std::runtime_error("visual background is not RGBA");
        if(mlt_frame_is_test_card(b))return 0;
        auto props=mlt_transition_properties(transition);auto bp=mlt_frame_properties(b);
        mlt_properties_set(bp,"consumer.rescale","none");mlt_properties_set_int(bp,"distort",1);
        int bw=mlt_properties_get_int(props,"source_width"),bh=mlt_properties_get_int(props,"source_height");auto bf=mlt_image_rgba;uint8_t* pixels=nullptr;
        auto kind=mlt_properties_get(props,"source_kind");
        // 仅从本项目source_time持有的AVFrame借用视图；视频不经过MLT的隐式normalize。
        const AVFrame* native=kind&&std::strcmp(kind,"video")==0?source_time_decoded_frame(b):nullptr;
        if(kind&&std::strcmp(kind,"video")==0&&!native)throw std::runtime_error("controlled decoded video view unavailable");
        if(native){bw=native->width;bh=native->height;}
        else{checked(mlt_frame_get_image(b,&pixels,&bf,&bw,&bh,0),"visual source read failed");if(!pixels||bf!=mlt_image_rgba||mlt_frame_is_test_card(b))throw std::runtime_error("visual source is not decoded RGBA");}
        auto p=parameters(transition,bw,bh);if(native)native_input(p,native);if(*width!=p.canvas_w||*height!=p.canvas_h)throw std::runtime_error("visual canvas differs from original project");
        auto started=std::chrono::steady_clock::now();Frame result;std::shared_ptr<Graph> graph;
        const uint8_t* processed=pixels;int processed_w=bw,processed_h=bh,processed_stride=bw*4,full_w=bw,full_h=bh,crop_x=0,crop_y=0;
        // 原尺寸、无旋转且无LUT时没有重采样操作；直接使用本帧真实RGBA，不缓存画面。
        if(!native&&p.scale_w==bw&&p.scale_h==bh&&p.rotation==0&&p.intensity==0){++fast_path_calls;}
        else{graph=acquire(p);result=graph->apply(p,pixels,native);processed=result->data[0];processed_w=result->width;processed_h=result->height;processed_stride=result->linesize[0];full_w=graph->full_w;full_h=graph->full_h;crop_x=graph->crop_x;crop_y=graph->crop_y;}
        auto composed=std::chrono::steady_clock::now();filter_ns+=std::chrono::duration_cast<std::chrono::nanoseconds>(composed-started).count();
        int x=overlay_position((p.canvas_w-full_w)/2.0+p.x)+crop_x,y=overlay_position((p.canvas_h-full_h)/2.0+p.y)+crop_y;
        int from_x=std::max(0,-x),from_y=std::max(0,-y),to_x=std::min(processed_w,p.canvas_w-x),to_y=std::min(processed_h,p.canvas_h-y);
        for(int row=from_y;row<to_y&&to_x>from_x;++row){
            auto source=processed+row*processed_stride;auto destination=*image+(size_t(row+y)*p.canvas_w+x+from_x)*4;
            bool opaque=true;for(int column=from_x;column<to_x;++column)if(source[column*4+3]!=255){opaque=false;break;}
            if(opaque&&to_x>from_x)std::memcpy(destination,source+from_x*4,size_t(to_x-from_x)*4);
            else for(int column=from_x;column<to_x;++column)over(destination+(column-from_x)*4,source+column*4);
        }
        composite_ns+=std::chrono::duration_cast<std::chrono::nanoseconds>(std::chrono::steady_clock::now()-composed).count();++calls;
        *format=mlt_image_rgba;return 0;
    }catch(const std::exception& error){auto props=mlt_frame_properties(frame);mlt_properties_set_int(props,"aividcut.visual_error",1);mlt_properties_set(props,"aividcut.visual_reason",error.what());return 1;}
}
mlt_frame process(mlt_transition transition,mlt_frame a,mlt_frame b){
    if(mlt_frame_is_test_card(b))return a;
    mlt_frame_push_service(a,transition);mlt_frame_push_frame(a,b);mlt_frame_push_get_image(a,get_image);return a;
}
void* factory(mlt_profile,mlt_service_type,const char*,const void*){
    auto transition=mlt_transition_new();if(transition){transition->process=process;mlt_properties_set_int(mlt_transition_properties(transition),"_transition_type",1);}return transition;
}
}

void register_image_effects(mlt_repository repository){mlt_repository_register(repository,mlt_service_transition_type,"aividcut.visual",factory);}
bool visual_available(){for(auto name:{"buffer","scale","setsar","format","rotate","transpose","hflip","vflip","buffersink"})if(!avfilter_get_by_name(name))return false;return true;}
bool visual_lut_available(){return visual_available()&&avfilter_get_by_name("lut3d")&&avfilter_get_by_name("split")&&avfilter_get_by_name("blend");}
ImageEffectsStatistics image_effects_statistics(){std::lock_guard<std::mutex> lock(cache_mutex);ImageEffectsStatistics value;value.builds=builds.load();value.hits=hits.load();value.calls=calls.load();value.fast_path_calls=fast_path_calls.load();value.filter_ns=filter_ns.load();value.composite_ns=composite_ns.load();value.resident_graphs=cache.size();for(const auto& entry:cache)value.estimated_bytes+=entry.graph->estimate;return value;}
std::string image_effects_diagnostics_json(){auto value=image_effects_statistics();return "{\"visual\":{\"builds\":"+std::to_string(value.builds)+",\"hits\":"+std::to_string(value.hits)+",\"calls\":"+std::to_string(value.calls)+",\"fastPathCalls\":"+std::to_string(value.fast_path_calls)+",\"filterNs\":"+std::to_string(value.filter_ns)+",\"compositeNs\":"+std::to_string(value.composite_ns)+",\"residentGraphs\":"+std::to_string(value.resident_graphs)+",\"estimatedBytes\":"+std::to_string(value.estimated_bytes)+"}}";}
