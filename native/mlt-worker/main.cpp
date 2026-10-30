#include "audio_mix.h"
// 所有MLT对象只在主线程访问；stdin有界控制请求，stdout只写AIV1二进制包。
#include <framework/mlt.h>
#include "audio_probe.h"
#include "source_time.h"
#include "source_audio.h"
#include "image_effects.h"
#include <algorithm>
#include <cmath>
#include <cstdint>
#include <cstdlib>
#include <cstring>
#include <iostream>
#include <limits>
#include <map>
#include <memory>
#include <stdexcept>
#include <string>
#include <vector>
#include <cstdio>
#ifdef AIVIDCUT_QT_METRICS
#include <QApplication>
#include <QFont>
#include <QFontMetrics>
#include <QFontDatabase>
#include <QRawFont>
#include <QFile>
#include <QCryptographicHash>
#include <QTextLayout>
#include <QGlyphRun>
#include <QString>
#endif
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#else
#include <unistd.h>
#endif

namespace {
constexpr size_t max_command=4*1024*1024, max_header=128*1024, max_image=3840*2160*4, max_audio=1024*1024;
FILE* wire=nullptr;
std::string quote(const std::string& s){std::string out="\"";for(unsigned char c:s){switch(c){case '"':out+="\\\"";break;case '\\':out+="\\\\";break;case '\n':out+="\\n";break;case '\r':out+="\\r";break;case '\t':out+="\\t";break;default:if(c<32)out+="?";else out+=static_cast<char>(c);}}return out+'"';}
void utf8(std::string& out,uint32_t n){if(n<=0x7f)out+=static_cast<char>(n);else if(n<=0x7ff){out+=static_cast<char>(0xc0|(n>>6));out+=static_cast<char>(0x80|(n&63));}else if(n<=0xffff){out+=static_cast<char>(0xe0|(n>>12));out+=static_cast<char>(0x80|((n>>6)&63));out+=static_cast<char>(0x80|(n&63));}else{out+=static_cast<char>(0xf0|(n>>18));out+=static_cast<char>(0x80|((n>>12)&63));out+=static_cast<char>(0x80|((n>>6)&63));out+=static_cast<char>(0x80|(n&63));}}

// 控制协议只允许平坦对象内的字符串或整数，不开放外部JSON图/插件参数。
class Json {
 const std::string& s;size_t p=0;
 void ws(){while(p<s.size()&&(s[p]==' '||s[p]=='\t'||s[p]=='\r'))++p;}
 uint32_t hex(){if(p+4>s.size())throw std::runtime_error("invalid escape");uint32_t n=0;for(int i=0;i<4;++i){char c=s[p++];n<<=4;if(c>='0'&&c<='9')n+=c-'0';else if(c>='a'&&c<='f')n+=c-'a'+10;else if(c>='A'&&c<='F')n+=c-'A'+10;else throw std::runtime_error("invalid escape");}return n;}
 std::string str(){if(p>=s.size()||s[p++]!='"')throw std::runtime_error("expected string");std::string out;while(p<s.size()){unsigned char c=s[p++];if(c=='"')return out;if(c<32)throw std::runtime_error("control character");if(c!='\\'){out+=static_cast<char>(c);continue;}if(p>=s.size())throw std::runtime_error("escape");switch(s[p++]){case '"':out+='"';break;case '\\':out+='\\';break;case '/':out+='/';break;case 'b':out+='\b';break;case 'f':out+='\f';break;case 'n':out+='\n';break;case 'r':out+='\r';break;case 't':out+='\t';break;case 'u':{auto n=hex();if(n>=0xd800&&n<=0xdbff){if(p+2>s.size()||s[p++]!='\\'||s[p++]!='u')throw std::runtime_error("surrogate");auto low=hex();if(low<0xdc00||low>0xdfff)throw std::runtime_error("surrogate");n=0x10000+((n-0xd800)<<10)+low-0xdc00;}else if(n>=0xdc00&&n<=0xdfff)throw std::runtime_error("surrogate");utf8(out,n);break;}default:throw std::runtime_error("escape");}}throw std::runtime_error("unterminated string");}
public:
 explicit Json(const std::string& v):s(v){}
 std::map<std::string,std::string> parse() {
  std::map<std::string,std::string> out;
  ws();
  if(p>=s.size()||s[p++]!='{')
   throw std::runtime_error("object");
  ws();
  if(p<s.size()&&s[p]=='}') {
   ++p;
   return out;
  }
  while(p<s.size()) {
   ws();
   auto key=str();
   if(out.count(key))
    throw std::runtime_error("duplicate key");
   ws();
   if(p>=s.size()||s[p++]!=':')
    throw std::runtime_error("colon");
   ws();
   std::string value;
   if(p<s.size()&&s[p]=='"') {
    value=str();
   } else {
    auto begin=p;
    if(p<s.size()&&s[p]=='-')
     ++p;
    while(p<s.size()&&s[p]>='0'&&s[p]<='9')
     ++p;
    if(p==begin||(p==begin+1&&s[begin]=='-'))
     throw std::runtime_error("integer");
    value=s.substr(begin,p-begin);
   }
   out.emplace(key,value);
   ws();
   if(p>=s.size())
    throw std::runtime_error("object");
   char end=s[p++];
   if(end=='}')
    break;
   if(end!=',')
    throw std::runtime_error("comma");
  }
  ws();
  if(p!=s.size()||out.size()>24)
   throw std::runtime_error("object limit");
  return out;
 }
};
int64_t integer(const std::map<std::string,std::string>& o,const std::string& key){auto it=o.find(key);if(it==o.end())throw std::runtime_error("missing integer");size_t used=0;auto n=std::stoll(it->second,&used);if(used!=it->second.size())throw std::runtime_error("integer");return n;}
std::string string(const std::map<std::string,std::string>& o,const std::string& key){auto it=o.find(key);if(it==o.end())throw std::runtime_error("missing string");return it->second;}
void write32(uint8_t* p,uint32_t n){p[0]=n>>24;p[1]=n>>16;p[2]=n>>8;p[3]=n;}
void packet(const std::string& header,const std::vector<uint8_t>& image={},const std::vector<uint8_t>& audio={}){
 if(header.empty()||header.size()>max_header||image.size()>max_image||audio.size()>max_audio)throw std::runtime_error("packet bound");uint8_t prefix[16]={'A','I','V','1'};write32(prefix+4,static_cast<uint32_t>(header.size()));write32(prefix+8,static_cast<uint32_t>(image.size()));write32(prefix+12,static_cast<uint32_t>(audio.size()));if(std::fwrite(prefix,1,16,wire)!=16||std::fwrite(header.data(),1,header.size(),wire)!=header.size())throw std::runtime_error("pipe closed");if(!image.empty()&&std::fwrite(image.data(),1,image.size(),wire)!=image.size())throw std::runtime_error("pipe closed");if(!audio.empty()&&std::fwrite(audio.data(),1,audio.size(),wire)!=audio.size())throw std::runtime_error("pipe closed");if(std::fflush(wire))throw std::runtime_error("pipe closed");
}
std::string list(mlt_properties values){std::string out="[";for(int i=0;i<mlt_properties_count(values);++i){if(i)out+=",";out+=quote(mlt_properties_get_name(values,i));}return out+"]";}
bool has(mlt_properties values,const char* name){for(int i=0;i<mlt_properties_count(values);++i)if(std::strcmp(mlt_properties_get_name(values,i),name)==0)return true;return false;}
std::string feature(bool value,const char* reason=""){return std::string("{\"supported\":")+(value?"true":"false")+",\"reason\":"+quote(reason)+"}";}

bool text_metrics_available(){
#ifdef AIVIDCUT_QT_METRICS
 return true;
#else
 return false;
#endif
}

#ifdef AIVIDCUT_QT_METRICS
struct ProjectFont { int id; QString family,style; int weight; QRawFont raw; qsizetype bytes; std::vector<QRawFont> checked; };
std::map<std::string,ProjectFont> project_fonts;
qsizetype project_font_bytes=0;
void ensure_qt(){if(!QCoreApplication::instance()){static int argc=1;static char name[]="aividcut-mlt-worker";static char* argv[]={name,nullptr};new QApplication(argc,argv);}}
QFont project_qfont(const ProjectFont& f,int size){QFont font; font.setPixelSize(size);font.setFamily(f.family);font.setWeight(QFont::Weight(f.weight));if(f.style.startsWith('i',Qt::CaseInsensitive))font.setStyle(QFont::StyleItalic);else font.setStyleName(f.style);return font;}
bool same_font(const QRawFont& a,const QRawFont& b){if(!a.isValid()||!b.isValid())return false;for(const char* tag:{"head","maxp","cmap","hhea","hmtx","name","OS/2","glyf","CFF ","GSUB","GPOS"}){if(a.fontTable(tag)!=b.fontTable(tag))return false;}return true;}
#endif
std::string load_project_font(const std::map<std::string,std::string>& command){
#ifdef AIVIDCUT_QT_METRICS
 ensure_qt();auto key=string(command,"sha256");auto old=project_fonts.find(key);if(old==project_fonts.end()){
  QFile file(QString::fromUtf8(string(command,"path").c_str()));if(!file.open(QIODevice::ReadOnly)||file.size()<12||file.size()>(32<<20)||project_fonts.size()>=32||file.size()>(128<<20)-project_font_bytes)throw std::runtime_error("工程字体文件不可读或超过容量限制");auto data=file.read((32<<20)+1);if(data.size()>(32<<20)||!file.atEnd())throw std::runtime_error("工程字体读取超过容量限制");if(QCryptographicHash::hash(data,QCryptographicHash::Sha256).toHex().toStdString()!=key)throw std::runtime_error("工程字体内容已变化");QRawFont raw(data,48);if(!raw.isValid())throw std::runtime_error("Qt不能读取所选工程字体");int id=QFontDatabase::addApplicationFontFromData(data);if(id<0)throw std::runtime_error("Qt不能加载所选工程字体");ProjectFont value{id,raw.familyName(),raw.styleName(),raw.weight(),raw,data.size(),{}};if(!same_font(QRawFont::fromFont(project_qfont(value,48)),raw)){QFontDatabase::removeApplicationFont(id);throw std::runtime_error("同名字体选择无法核对，请使用可区分的字体文件");}project_font_bytes+=data.size();old=project_fonts.emplace(key,std::move(value)).first;
 }
 const auto& f=old->second;return "{\"family\":"+quote(f.family.toUtf8().toStdString())+",\"style\":"+quote(f.style.toUtf8().toStdString())+",\"weight\":"+std::to_string(f.weight)+",\"italic\":"+(f.raw.style()==QFont::StyleNormal?"false":"true")+"}";
#else
 (void)command;throw std::runtime_error("worker未提供工程字体能力");
#endif
}
std::string measure_text(const std::map<std::string,std::string>& command){
#ifdef AIVIDCUT_QT_METRICS
 auto text=string(command,"text");auto size=integer(command,"fontPixels");if(text.size()>40000||size<1||size>1000)throw std::runtime_error("text measurement limit");
 if(!QCoreApplication::instance()){static int argc=1;static char name[]="aividcut-mlt-worker";static char* argv[]={name,nullptr};new QApplication(argc,argv);}
 QFont font;font.setFamily("Sans");font.setPixelSize(static_cast<int>(size));font.setWeight(QFont::Normal);font.setStyleName("normal");
 auto key=command.find("fontKey"); ProjectFont* selected=nullptr;if(key!=command.end()&&!key->second.empty()){auto found=project_fonts.find(key->second);if(found==project_fonts.end())throw std::runtime_error("工程字体尚未加载");selected=&found->second;font=project_qfont(*selected,static_cast<int>(size));}
 QFontMetrics metrics(font);
 auto lines=QString::fromUtf8(text.data(),static_cast<qsizetype>(text.size())).split('\n');int width=0;
 for(const auto& line:lines){if(selected&&!line.isEmpty()){QTextLayout layout(line,font);layout.beginLayout();auto l=layout.createLine();if(l.isValid())l.setLineWidth(10000000);layout.endLayout();for(const auto& run:layout.glyphRuns()){auto raw=run.rawFont();bool verified=false;for(const auto& checked:selected->checked){if(raw==checked){verified=true;break;}}if(!verified){if(!same_font(raw,selected->raw))throw std::runtime_error("文字使用了其它字体，不能保证所选字形");if(selected->checked.size()>=8)selected->checked.erase(selected->checked.begin());selected->checked.push_back(raw);}}}int value=metrics.horizontalAdvance(line);if(!line.isEmpty()){value-=std::min(0,metrics.leftBearing(line.at(0)));value-=std::min(0,metrics.rightBearing(line.at(line.size()-1)));}width=std::max(width,value);}
 auto height=static_cast<int64_t>(metrics.lineSpacing())*lines.size()+2;
 return "{\"width\":"+std::to_string(std::max(1,width))+",\"height\":"+std::to_string(height)+"}";
#else
 (void)command;throw std::runtime_error("Qt actual font measurement unavailable");
#endif
}

std::map<std::string,AudioOrigin> audio_origins;
struct AudioShiftState {int64_t previous=-2;int frequency=0,channels=0;std::vector<float> pending;};
void close_shift(void* value){delete static_cast<AudioShiftState*>(value);}
// 本进程自有逐路源时钟滤镜：实际首解码PTS前返回静音，不提前消费整段声音。
int range_audio(mlt_frame frame,void** buffer,mlt_audio_format* format,int* frequency,int* channels,int* samples){
 auto filter=static_cast<mlt_filter>(mlt_frame_pop_audio(frame));
 auto props=mlt_filter_properties(filter);
 auto evidence=mlt_frame_unique_properties(frame,mlt_filter_service(filter));
 auto source_position=mlt_properties_get_int64(evidence,"source_position");
 auto profile=mlt_service_profile(mlt_filter_service(filter));
 auto state=static_cast<AudioShiftState*>(mlt_properties_get_data(props,"_aividcut_shift",nullptr));
 const char* resource=mlt_properties_get(props,"resource");auto found=resource?audio_origins.find(resource):audio_origins.end();
 if(!state||found==audio_origins.end()||!found->second.has_audio||*frequency<1||*channels<1||*samples<1)return 1;
 int wanted=*samples,rate=*frequency,count=*channels;size_t bytes=static_cast<size_t>(wanted)*count*sizeof(float);if(bytes>max_audio)return 1;
 if(state->previous+1!=source_position||state->frequency!=rate||state->channels!=count)state->pending.clear();
 state->previous=source_position;state->frequency=rate;state->channels=count;
 int64_t begin=mlt_audio_calculate_samples_to_position(float(profile->frame_rate_num)/profile->frame_rate_den,rate,source_position);
 int64_t end_num=mlt_properties_get_int64(props,"end_num"),end_den=mlt_properties_get_int64(props,"end_den");
 if(end_den<=0)return 1;int64_t endpoint=std::numeric_limits<int64_t>::max();
 if(end_num>=0){long double value=static_cast<long double>(end_num)*rate/(static_cast<long double>(end_den)*1000000.0L);if(value>static_cast<long double>(endpoint))return 1;endpoint=static_cast<int64_t>(std::ceil(value));}
 long double frame_us=static_cast<long double>(source_position)*profile->frame_rate_den*1000000.0L/profile->frame_rate_num;
 long double lead=(static_cast<long double>(found->second.start_us)-frame_us)*rate/1000000.0L;
 int prefix=lead<=0?0:lead>=wanted?wanted:static_cast<int>(std::ceil(lead));
 auto copy=static_cast<float*>(mlt_pool_alloc(bytes));if(!copy)return 1;std::memset(copy,0,bytes);
 if(prefix<wanted&&begin<endpoint){
  void* input=nullptr;auto input_format=mlt_audio_f32le;int input_rate=rate,input_channels=count,input_samples=wanted;
  int error=mlt_frame_get_audio(frame,&input,&input_format,&input_rate,&input_channels,&input_samples);
  if(error||!input||input_format!=mlt_audio_f32le||input_rate!=rate||input_channels!=count||input_samples<1||static_cast<size_t>(input_samples)*count*sizeof(float)>max_audio){mlt_pool_release(copy);return 1;}
  auto data=static_cast<float*>(input);state->pending.insert(state->pending.end(),data,data+static_cast<size_t>(input_samples)*count);
  size_t needed=static_cast<size_t>(wanted-prefix)*count;
  if(state->pending.size()<needed||state->pending.size()*sizeof(float)>max_audio){mlt_pool_release(copy);return 1;}
  std::memcpy(copy+prefix*count,state->pending.data(),needed*sizeof(float));state->pending.erase(state->pending.begin(),state->pending.begin()+needed);
 }else {state->pending.clear();while(mlt_frame_pop_audio(frame)){} }
 for(int i=0;i<wanted;++i)if(begin+i>=endpoint)for(int c=0;c<count;++c)copy[i*count+c]=0;
 *format=mlt_audio_f32le;*frequency=rate;*channels=count;*samples=wanted;
 mlt_frame_set_audio(frame,copy,mlt_audio_f32le,static_cast<int>(bytes),mlt_pool_release);*buffer=copy;return 0;
}
mlt_frame range_process(mlt_filter filter,mlt_frame frame){auto props=mlt_frame_unique_properties(frame,mlt_filter_service(filter));mlt_properties_set_int64(props,"source_position",mlt_frame_original_position(frame));mlt_frame_push_audio(frame,filter);mlt_frame_push_audio(frame,reinterpret_cast<void*>(range_audio));return frame;}
void* range_factory(mlt_profile,mlt_service_type,const char*,const void*){auto filter=mlt_filter_new();if(filter){filter->process=range_process;mlt_properties_set_data(mlt_filter_properties(filter),"_aividcut_shift",new AudioShiftState,0,close_shift,nullptr);}return filter;}

uint64_t live_graphs=0;
void close_graph_marker(void* marker){--live_graphs;delete static_cast<char*>(marker);}
uint64_t live_producers=0;
void close_producer_marker(void* marker){--live_producers;delete static_cast<char*>(marker);}
uint64_t live_services=0;
std::map<std::string,uint64_t> live_service_kinds;
struct ServiceMarker{std::string kind;};
void close_service_marker(void* marker){auto value=static_cast<ServiceMarker*>(marker);--live_services;--live_service_kinds[value->kind];delete value;}
void mark_service_lifetime(mlt_service item){
 if(!item)return;auto props=mlt_service_properties(item);
 if(mlt_properties_get_data(props,"aividcut.service_lifetime",nullptr))return;
 auto id=mlt_properties_get(props,"mlt_service");std::string kind=id?id:"unnamed";
 ++live_services;++live_service_kinds[kind];mlt_properties_set_data(props,"aividcut.service_lifetime",new ServiceMarker{kind},0,close_service_marker,nullptr);
 for(int i=0;i<mlt_service_filter_count(item);++i)mark_service_lifetime(mlt_filter_service(mlt_service_filter(item,i)));
 auto parent=mlt_service_producer(item);if(parent&&parent!=item)mark_service_lifetime(parent);
}
// 只用公开结构/API标记当前图的producer寿命，不持有额外MLT引用。
void mark_producer_lifetime(mlt_producer item){
 if(!item)return;auto props=mlt_producer_properties(item);
 if(mlt_properties_get_data(props,"aividcut.producer_lifetime",nullptr))return;
 ++live_producers;mlt_properties_set_data(props,"aividcut.producer_lifetime",new char,0,close_producer_marker,nullptr);
 mark_service_lifetime(mlt_producer_service(item));
 auto type=mlt_service_identify(mlt_producer_service(item));
 if(type==mlt_service_tractor_type){auto tracks=mlt_tractor_multitrack(reinterpret_cast<mlt_tractor>(item));for(int i=0;tracks&&i<mlt_multitrack_count(tracks);++i)mark_producer_lifetime(mlt_multitrack_track(tracks,i));}
 if(type==mlt_service_playlist_type){auto list=reinterpret_cast<mlt_playlist>(item);for(int i=0;i<mlt_playlist_count(list);++i)mark_producer_lifetime(mlt_playlist_get_clip(list,i));}
 auto parent=mlt_producer_cut_parent(item);if(parent&&parent!=item)mark_producer_lifetime(parent);
}
// 受控图统一使用FFmpeg重采样；避免旧resample的自定义close遗漏父service释放。
void* controlled_resample_factory(mlt_profile profile,mlt_service_type,const char*,const void* resource){return mlt_factory_filter(profile,"swresample",resource);}
struct Engine {
 mlt_repository repository=nullptr;mlt_profile profile=nullptr;mlt_producer producer=nullptr;
 int width=0,height=0,output_width=0,output_height=0,fps_num=0,fps_den=1,rate=48000;int64_t length=0,position=0;bool playing=false,failed=false,offline_render=false;
 Engine(){repository=mlt_factory_init(nullptr);if(!repository)throw std::runtime_error("MLT repository unavailable");if(has(mlt_repository_filters(repository),"swresample"))mlt_repository_register(repository,mlt_service_filter_type,"resample",controlled_resample_factory);mlt_repository_register(repository,mlt_service_filter_type,"aividcut.audio_range",range_factory);register_source_time(repository);register_source_audio(repository);register_audio_mix(repository);register_image_effects(repository);}
 ~Engine(){clear();mlt_factory_close();}
 uint64_t graph_loads=0,graph_replacements=0,peak_graph_bytes=0,peak_release_refs=0;
 void clear(bool sources=true){if(producer){peak_release_refs=std::max(peak_release_refs,static_cast<uint64_t>(mlt_properties_ref_count(mlt_producer_properties(producer))));mlt_producer_close(producer);}producer=nullptr;if(sources){clear_source_audio_contexts();clear_source_time_contexts();}if(profile)mlt_profile_close(profile);profile=nullptr;playing=false;failed=false;}
 std::string graph_diagnostics(){std::string kinds="{";bool first=true;for(auto& entry:live_service_kinds)if(entry.second){if(!first)kinds+=",";first=false;kinds+=quote(entry.first)+":"+std::to_string(entry.second);}kinds+="}";return "{\"loads\":"+std::to_string(graph_loads)+",\"replacements\":"+std::to_string(graph_replacements)+",\"peakXmlBytes\":"+std::to_string(peak_graph_bytes)+",\"liveGraphs\":"+std::to_string(live_graphs)+",\"liveProducers\":"+std::to_string(live_producers)+",\"liveServices\":"+std::to_string(live_services)+",\"liveServicesByKind\":"+kinds+",\"peakReleaseReferences\":"+std::to_string(peak_release_refs)+"}";}
 std::string capabilities(){auto p=mlt_repository_producers(repository),f=mlt_repository_filters(repository),t=mlt_repository_transitions(repository);bool base=has(p,"avformat")&&has(p,"xml-string")&&has(p,"loader")&&has(p,"colour")&&has(t,"aividcut.audio_mix")&&has(t,"composite");
 return std::string("{\"available\":")+(base?"true":"false")
  +",\"reason\":"+quote(base?"":"MLT缺少avformat/xml-string/loader/colour/受控混音/composite插件")
  +",\"protocol\":\"AIV1\",\"mltVersion\":"+quote(mlt_version_get_string())
  +",\"producers\":"+list(p)+",\"filters\":"+list(f)+",\"transitions\":"+list(t)
  +",\"video\":"+feature(base)+",\"image\":"+feature(base)
  +",\"sourceProxies\":"+feature(base,"可验证代理PTS与独立原格式起点；声音仍原片")
  +",\"pictureSeek\":"+feature(base,"只取画面，不重放未消费的音轨")
  +",\"offlineRender\":"+feature(base,"工程原画幅离线输出，不扩展WebView传输")
  +",\"windowedGraph\":"+feature(base&&has(f,"swresample"),"保留绝对游标和源缓存；需受控FFmpeg重采样后端")
  +",\"audio\":"+feature(base&&source_audio_available(),"48k stereo；实际源半开区间/分数偏移，逐路流式解码")
  +",\"fontFiles\":"+feature(text_metrics_available(),"工程字体文件SHA与实际字形核对")
  +",\"audioFades\":"+feature(source_audio_available(),"输出采样时钟逐路线性淡入淡出")
  +",\"text\":"+feature(has(f,"qtext")&&text_metrics_available(),"实验qtext；启动前必须实际测量字体bbox，跨平台字体一致性未验收")
  +",\"transform\":"+feature(has(t,"aividcut.visual")&&visual_available(),"原始RGBA按工程坐标合成；双端实机另验")
  +",\"speed\":"+feature(source_audio_available()&&has(p,"aividcut.source_time"),"实际视频PTS与源采样时钟；atempo保持音高")
  +",\"lut\":"+feature(has(t,"aividcut.visual")&&visual_lut_available(),"逐帧LUT与原画面混合，坐标/像素由真实夹具核对")
  +",\"display\":\"webview_binary_canvas_audio\"}";
 }
 std::string info(){return "{\"durationFrames\":"+std::to_string(length)+",\"position\":"+std::to_string(position)+",\"playing\":"+(playing?"true":"false")+",\"width\":"+std::to_string(output_width)+",\"height\":"+std::to_string(output_height)+",\"fpsNum\":"+std::to_string(fps_num)+",\"fpsDen\":"+std::to_string(fps_den)+",\"sampleRate\":"+std::to_string(rate)+",\"channels\":2}";}
 void load(const std::map<std::string,std::string>& command,bool window=false){
  auto w=integer(command,"width"),h=integer(command,"height"),n=integer(command,"fpsNum"),d=integer(command,"fpsDen"),duration=integer(command,"durationFrames"),mw=integer(command,"maxWidth"),mh=integer(command,"maxHeight");
  auto mode=command.find("renderMode");bool offline=mode!=command.end()&&mode->second=="offline";
  if(mode!=command.end()&&mode->second!="offline"&&mode->second!="preview")throw std::runtime_error("invalid render mode");
  if(w<16||w>8192||h<16||h>8192||w*h>3840*2160||n<1||n>240000||d<1||d>120000||double(n)/d<2||double(n)/d>120||duration<1||duration>21000000||mw<16||mh<16||mw>(offline?8192:1920)||mh>(offline?8192:1080))throw std::runtime_error("profile limit");
  if(window&&(!producer||failed||offline!=offline_render||w!=width||h!=height||n!=fps_num||d!=fps_den||duration!=length))throw std::runtime_error("图窗口不能改变工程时钟或恢复失败图");
  bool was_playing=playing;int64_t previous_position=position;
  auto xml=string(command,"xml");if(xml.size()>3*1024*1024||xml.find("<!DOCTYPE")!=std::string::npos||xml.find("<!ENTITY")!=std::string::npos)throw std::runtime_error("graph limit");
  auto next_profile=mlt_profile_init(nullptr);if(!next_profile)throw std::runtime_error("profile allocation");std::unique_ptr<mlt_profile_s,decltype(&mlt_profile_close)> profile_guard(next_profile,mlt_profile_close);next_profile->width=w;next_profile->height=h;next_profile->frame_rate_num=n;next_profile->frame_rate_den=d;next_profile->progressive=1;next_profile->sample_aspect_num=1;next_profile->sample_aspect_den=1;next_profile->display_aspect_num=w;next_profile->display_aspect_den=h;next_profile->colorspace=709;next_profile->is_explicit=1;
  auto sources=window?std::string():string(command,"sources");size_t offset=0;std::map<std::string,AudioOrigin> next_origins;
  while(offset<sources.size()){
   auto end=sources.find('\n',offset);if(end==std::string::npos)throw std::runtime_error("invalid source registry");auto row=sources.substr(offset,end-offset);offset=end+1;auto tab=row.find('\t');if(tab==std::string::npos)throw std::runtime_error("invalid source registry");auto kind=row.substr(0,tab),path=row.substr(tab+1);
   auto origin=probe_audio_origin(path);if((kind=="audio"||kind=="video_audio")&&!origin.has_audio)throw std::runtime_error("registered audio stream is missing");next_origins[path]=origin;
   std::unique_ptr<mlt_producer_s,decltype(&mlt_producer_close)> candidate(mlt_factory_producer(next_profile,"loader",path.c_str()),mlt_producer_close);if(!candidate)throw std::runtime_error("registered media decoder unavailable");
   mlt_properties_set_int(mlt_producer_properties(candidate.get()),"threads",1);mlt_properties_set_int(mlt_producer_properties(candidate.get()),"cache",2);
   mlt_frame frame=nullptr;if(mlt_service_get_frame(mlt_producer_service(candidate.get()),&frame,0)||!frame)throw std::runtime_error("registered media failed to decode");
   std::unique_ptr<mlt_frame_s,decltype(&mlt_frame_close)> frame_guard(frame,mlt_frame_close);
   if(kind=="audio"){void* buffer=nullptr;auto format=mlt_audio_f32le;int frequency=48000,channels=2,samples=1600;if(mlt_frame_is_test_audio(frame)||mlt_frame_get_audio(frame,&buffer,&format,&frequency,&channels,&samples)||!buffer||mlt_frame_is_test_audio(frame))throw std::runtime_error("registered audio codec failed");}
   else {uint8_t* pixels=nullptr;auto format=mlt_image_rgba;int width=32,height=32;if(mlt_frame_is_test_card(frame)||mlt_frame_get_image(frame,&pixels,&format,&width,&height,0)||!pixels||mlt_frame_is_test_card(frame))throw std::runtime_error("registered video or image codec failed");}
  }
  std::map<std::string,AudioOrigin> previous_origins;if(!window){previous_origins=std::move(audio_origins);audio_origins=std::move(next_origins);}
  auto next=mlt_factory_producer(next_profile,"xml-string",xml.c_str());if(!next){if(!window)audio_origins=std::move(previous_origins);throw std::runtime_error("cannot load controlled MLT graph");}
  ++live_graphs;mlt_properties_set_data(mlt_producer_properties(next),"aividcut.graph_lifetime",new char,0,close_graph_marker,nullptr);
  mark_producer_lifetime(next);
  clear(!window);producer=next;profile=profile_guard.release();width=w;height=h;fps_num=n;fps_den=d;length=duration;position=window?previous_position:0;playing=window?was_playing:false;offline_render=offline;double scale=offline?1.0:std::min(1.0,std::min(double(mw)/width,double(mh)/height));output_width=std::max(1,int(std::round(width*scale)));output_height=std::max(1,int(std::round(height*scale)));mlt_producer_set_speed(producer,1.0);
  ++graph_loads;if(window)++graph_replacements;peak_graph_bytes=std::max(peak_graph_bytes,static_cast<uint64_t>(xml.size()));
 }
 void frame(uint64_t id,int64_t at,bool advance,bool with_audio=true){
  if(!producer||at<0||at>=length)throw std::runtime_error("position outside loaded project");
  if(failed)throw std::runtime_error("媒体图上次解码失败，请重新加载后再预览");
  clear_source_audio_error();
  begin_source_time_pull();if(with_audio)begin_source_audio_pull();
  mlt_producer_seek(producer,at);mlt_frame frame=nullptr;
  try{
   if(mlt_service_get_frame(mlt_producer_service(producer),&frame,0)||!frame){if(!source_audio_error().empty())throw std::runtime_error(source_audio_error());throw std::runtime_error("MLT failed to obtain frame");}
   if(!source_audio_error().empty())throw std::runtime_error(source_audio_error());
   auto props=mlt_frame_properties(frame);mlt_properties_set_int(props,"consumer_progressive",1);mlt_properties_set_int(props,"consumer_deinterlace",1);mlt_properties_set(props,"rescale.interp","bilinear");
   uint8_t* pixels=nullptr;auto format=mlt_image_rgba;int w=width,h=height;
   int image_error=mlt_frame_get_image(frame,&pixels,&format,&w,&h,0);
   if(!source_audio_error().empty())throw std::runtime_error(source_audio_error());
   if(mlt_properties_get_int(props,"aividcut.visual_error")){const char* reason=mlt_properties_get(props,"aividcut.visual_reason");throw std::runtime_error(reason&&*reason?reason:"MLT visual operation failed");}
   if(image_error||!pixels||format!=mlt_image_rgba||w!=width||h!=height)throw std::runtime_error("MLT RGBA conversion failed");
   std::vector<uint8_t> image(size_t(output_width)*output_height*4);
   // 先在原工程坐标合成，再缩小预览，避免字体/位移/LUT坐标随代理分辨率变化。
   if(output_width==width&&output_height==height)std::memcpy(image.data(),pixels,image.size());
   else for(int y=0;y<output_height;++y)for(int x=0;x<output_width;++x){int sx=std::min(width-1,x*width/output_width),sy=std::min(height-1,y*height/output_height);std::memcpy(&image[(size_t(y)*output_width+x)*4],pixels+(size_t(sy)*width+sx)*4,4);}
   auto audio_start=source_audio_frame_sample(at,fps_num,fps_den,rate);
   auto audio_end=source_audio_frame_sample(at+1,fps_num,fps_den,rate);
   std::vector<uint8_t> audio;int samples=0;
   if(with_audio){
    auto audio_format=mlt_audio_f32le;int frequency=rate,channels=2;samples=static_cast<int>(audio_end-audio_start);void* pcm=nullptr;
    if(samples<1||size_t(samples)*8>max_audio||mlt_frame_get_audio(frame,&pcm,&audio_format,&frequency,&channels,&samples)||!pcm||audio_format!=mlt_audio_f32le||frequency!=rate||channels!=2||samples<1||size_t(samples)*8>max_audio)throw std::runtime_error("MLT PCM conversion failed");
    if(!source_audio_error().empty())throw std::runtime_error(source_audio_error());
    if(samples!=audio_end-audio_start)throw std::runtime_error("MLT output sample clock changed requested exact span");
    audio.resize(size_t(samples)*8);std::memcpy(audio.data(),pcm,audio.size());
   }
   bool end=at==length-1;if(advance){position=std::min(at+1,length-1);if(end)playing=false;}else position=at;
   std::string header="{\"requestId\":"+std::to_string(id)+",\"type\":\"frame\",\"position\":"+std::to_string(at)+",\"width\":"+std::to_string(output_width)+",\"height\":"+std::to_string(output_height)+",\"fpsNum\":"+std::to_string(fps_num)+",\"fpsDen\":"+std::to_string(fps_den)+",\"sampleRate\":"+std::to_string(rate)+",\"channels\":2,\"samples\":"+std::to_string(samples)+",\"audioStartSample\":"+std::to_string(audio_start)+",\"pictureOnly\":"+(with_audio?"false":"true")+",\"end\":"+(end?"true":"false")+"}";
   packet(header,image,audio);mlt_frame_close(frame);retire_source_time_contexts();if(with_audio)retire_source_audio_contexts();
  }catch(...){failed=true;if(frame)mlt_frame_close(frame);throw;}
 }
};
}

int main(){
#ifdef _WIN32
 _setmode(_fileno(stdin),_O_BINARY);_setmode(_fileno(stdout),_O_BINARY);
 wire=_fdopen(_dup(_fileno(stdout)),"wb");_dup2(_fileno(stderr),_fileno(stdout));
#else
 wire=fdopen(dup(fileno(stdout)),"wb");dup2(fileno(stderr),fileno(stdout));
#endif
 if(!wire)return 1;
 std::ios::sync_with_stdio(false);std::cin.tie(nullptr);
 try{
  Engine engine;std::string line;
  while(true){line.clear();char c;bool eof=true;while(std::cin.get(c)){eof=false;if(c=='\n')break;if(line.size()>=max_command)return 2;line+=c;}if(eof)break;
   uint64_t id=0;
   try{auto command=Json(line).parse();auto signed_id=integer(command,"requestId");if(signed_id<1)throw std::runtime_error("request id");id=signed_id;auto op=string(command,"op");
    if(op=="inspect"){packet("{\"requestId\":"+std::to_string(id)+",\"type\":\"capabilities\",\"capabilities\":"+engine.capabilities()+"}");continue;}
    if(op=="diagnostics"){auto diagnostics=image_effects_diagnostics_json();diagnostics.pop_back();diagnostics+=",\"audioMix\":"+audio_mix_diagnostics_json()+",\"sourceAudio\":"+source_audio_diagnostics_json()+",\"sourceVideo\":"+source_time_diagnostics_json()+",\"graph\":"+engine.graph_diagnostics()+"}";packet("{\"requestId\":"+std::to_string(id)+",\"type\":\"diagnostics\",\"diagnostics\":"+diagnostics+"}");continue;}
    if(op=="load_font"){packet("{\"requestId\":"+std::to_string(id)+",\"type\":\"font_loaded\",\"font\":"+load_project_font(command)+"}");continue;}
    if(op=="measure_text"){packet("{\"requestId\":"+std::to_string(id)+",\"type\":\"measurement\",\"measurement\":"+measure_text(command)+"}");continue;}
    if(op=="load"){engine.load(command);packet("{\"requestId\":"+std::to_string(id)+",\"type\":\"loaded\",\"info\":"+engine.info()+"}");continue;}
    if(op=="load_window"){engine.load(command,true);packet("{\"requestId\":"+std::to_string(id)+",\"type\":\"loaded\",\"info\":"+engine.info()+"}");continue;}
    if(op=="close"){packet("{\"requestId\":"+std::to_string(id)+",\"type\":\"closed\"}");break;}
    if(!engine.producer)throw std::runtime_error("load project first");
    if(op=="play"||op=="pause"){engine.playing=op=="play";packet("{\"requestId\":"+std::to_string(id)+",\"type\":\"ack\"}");continue;}
    if(op=="preview"){engine.frame(id,integer(command,"position"),false,false);continue;}
    if(op=="seek"){engine.frame(id,integer(command,"position"),false);continue;}
    if(op=="next"){if(!engine.playing)throw std::runtime_error("playback paused");engine.frame(id,engine.position,true);continue;}
    throw std::runtime_error("unsupported control command");
   }catch(const std::exception& error){packet("{\"requestId\":"+std::to_string(id)+",\"type\":\"error\",\"error\":"+quote(std::string("MLT原生操作未通过：")+error.what())+"}");}
  }
 }catch(const std::exception&){std::cerr<<"AIVidCut MLT worker初始化或管道失败\n";return 1;}
 return 0;
}
