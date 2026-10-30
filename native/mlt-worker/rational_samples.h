#pragma once
#include <array>
#include <cstdint>
#include <limits>
#include <stdexcept>

// 有理微秒端点只在最终输出采样网格ceil。384位固定容器覆盖int64端点/分母和速度的交叉乘积，
// 避免den*1000000溢出，也不依赖MSVC不提供的__int128；没有动态大数分配。
namespace aividcut_samples {
struct Wide {std::array<uint32_t,12> words{};};
inline Wide value(uint64_t v){Wide r;r.words[0]=static_cast<uint32_t>(v);r.words[1]=static_cast<uint32_t>(v>>32);return r;}
inline int compare(const Wide& a,const Wide& b){for(int i=11;i>=0;--i){if(a.words[i]!=b.words[i])return a.words[i]<b.words[i]?-1:1;}return 0;}
inline Wide add(const Wide& a,const Wide& b){Wide r;uint64_t carry=0;for(size_t i=0;i<r.words.size();++i){auto sum=uint64_t(a.words[i])+b.words[i]+carry;r.words[i]=static_cast<uint32_t>(sum);carry=sum>>32;}if(carry)throw std::runtime_error("sample rational addition bound");return r;}
inline Wide subtract(const Wide& a,const Wide& b){if(compare(a,b)<0)throw std::runtime_error("sample rational negative magnitude");Wide r;uint64_t borrow=0;for(size_t i=0;i<r.words.size();++i){uint64_t rhs=uint64_t(b.words[i])+borrow;uint64_t lhs=a.words[i];r.words[i]=static_cast<uint32_t>(lhs-rhs);borrow=lhs<rhs;}return r;}
inline Wide multiply32(const Wide& a,uint32_t b){Wide r;uint64_t carry=0;for(size_t i=0;i<r.words.size();++i){uint64_t product=uint64_t(a.words[i])*b+carry;r.words[i]=static_cast<uint32_t>(product);carry=product>>32;}if(carry)throw std::runtime_error("sample rational multiplication bound");return r;}
inline Wide multiply(const Wide& a,uint64_t b){auto low=multiply32(a,static_cast<uint32_t>(b));auto high=multiply32(a,static_cast<uint32_t>(b>>32));if(high.words.back())throw std::runtime_error("sample rational shift bound");for(int i=11;i>0;--i)high.words[i]=high.words[i-1];high.words[0]=0;return add(low,high);}
inline int64_t ceil_quotient(const Wide& n,const Wide& d){if(compare(d,value(0))==0)throw std::runtime_error("sample rational denominator zero");uint64_t low=0,high=std::numeric_limits<int64_t>::max();if(compare(multiply(d,high),n)<0)throw std::runtime_error("sample endpoint overflow");while(low<high){uint64_t middle=low+(high-low)/2;if(compare(multiply(d,middle),n)>=0)high=middle;else low=middle+1;}return static_cast<int64_t>(low);}
inline int64_t microsecond_sample(int64_t n,int64_t d,int64_t origin_us=0){
    if(n<0||d<=0||origin_us<0)throw std::runtime_error("sample source rational invalid");
    auto raw=value(static_cast<uint64_t>(n)),origin=multiply(value(origin_us),d);bool negative=compare(raw,origin)<0;
    auto numerator=multiply(negative?subtract(origin,raw):subtract(raw,origin),6);auto denominator=multiply(value(d),125);
    auto rounded=ceil_quotient(numerator,denominator);
    if(!negative)return rounded;
    return compare(multiply(denominator,rounded),numerator)==0?-rounded:1-rounded;
}
inline int64_t signed_microsecond_sample(int64_t n,int64_t d){
    if(d<=0)throw std::runtime_error("sample timeline rational invalid");
    if(n>=0)return microsecond_sample(n,d);
    auto magnitude=static_cast<uint64_t>(-(n+1))+1;
    auto numerator=multiply(value(magnitude),6),denominator=multiply(value(d),125);
    auto rounded=ceil_quotient(numerator,denominator);
    return compare(multiply(denominator,rounded),numerator)==0?-rounded:1-rounded;
}
inline bool before_origin(int64_t n,int64_t d,int64_t origin_us){return compare(value(n),multiply(value(origin_us),d))<0;}
inline int64_t shifted_start(int64_t timeline_n,int64_t timeline_d,int64_t source_n,int64_t source_d,int64_t origin_us,int speed_n,int speed_d){
    if(timeline_d<=0||source_n<0||source_d<=0||origin_us<0||speed_n<1||speed_d<1)throw std::runtime_error("sample shifted rational invalid");
    auto gap=subtract(multiply(value(origin_us),source_d),value(source_n));
    bool negative=timeline_n<0;auto absolute=negative?static_cast<uint64_t>(-(timeline_n+1))+1:static_cast<uint64_t>(timeline_n);
    auto original=multiply(multiply(value(absolute),source_d),speed_n);
    auto delayed=multiply(multiply(gap,speed_d),timeline_d);
    auto total=negative?(compare(delayed,original)<0?subtract(original,delayed):subtract(delayed,original)):add(original,delayed);
    bool result_negative=negative&&compare(delayed,original)<0;
    auto numerator=multiply(total,6);
    auto denominator=multiply(multiply(multiply(value(timeline_d),source_d),speed_n),125);
    auto rounded=ceil_quotient(numerator,denominator);
    if(!result_negative)return rounded;
    return compare(multiply(denominator,rounded),numerator)==0?-rounded:1-rounded;
}
}
