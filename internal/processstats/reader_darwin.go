//go:build darwin && cgo

package processstats

/*
#cgo LDFLAGS: -lproc
#include <libproc.h>
#include <sys/proc_info.h>
#include <sys/resource.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <mach/mach_time.h>
#include <pthread.h>

static mach_timebase_info_data_t av_scale;
static pthread_once_t av_scale_once=PTHREAD_ONCE_INIT;
static void av_init_scale(void){mach_timebase_info(&av_scale);}
static uint64_t av_ns(uint64_t value){pthread_once(&av_scale_once,av_init_scale);if(av_scale.denom==0)return 0;return (uint64_t)((__uint128_t)value*av_scale.numer/av_scale.denom);}
typedef struct { int pid; int ppid; int64_t started; char name[64]; uint64_t rss; uint64_t footprint; uint64_t cpu; } av_process;
static int av_identity(int pid,av_process* out){
 struct proc_bsdinfo info; memset(&info,0,sizeof(info));
 if(proc_pidinfo(pid,PROC_PIDTBSDINFO,0,&info,sizeof(info))!=sizeof(info))return -1;
 memset(out,0,sizeof(*out));out->pid=pid;out->ppid=info.pbi_ppid;
 out->started=(int64_t)info.pbi_start_tvsec*1000000000+(int64_t)info.pbi_start_tvusec*1000;
 const char* name=info.pbi_name[0]?info.pbi_name:info.pbi_comm;strncpy(out->name,name,sizeof(out->name)-1);return 0;
}
static int av_counters(int pid,av_process* out){
 if(av_identity(pid,out))return -1;
 struct rusage_info_v4 value;memset(&value,0,sizeof(value));
 if(proc_pid_rusage(pid,RUSAGE_INFO_V4,(rusage_info_t*)&value))return -2;
 out->rss=value.ri_resident_size;out->footprint=value.ri_phys_footprint;out->cpu=av_ns(value.ri_user_time)+av_ns(value.ri_system_time);return 0;
}
*/
import "C"
import (
	"context"
	"errors"
	"fmt"
	"unsafe"
)

type platformReader struct{}

func (platformReader) Enumerate(ctx context.Context) ([]Identity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	capacity := int(C.proc_listallpids(nil, 0)) + 256
	if capacity < 1 || capacity > MaxEnumerated {
		return nil, errors.New("系统进程数量超出采集范围")
	}
	pids := make([]C.int, capacity)
	count := int(C.proc_listallpids(unsafe.Pointer(&pids[0]), C.int(len(pids)*4)))
	if count <= 0 || count >= capacity {
		return nil, errors.New("进程枚举失败或观测期间数量已变化，请重试")
	}
	out := make([]Identity, 0, count)
	for _, pid := range pids[:count] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var row C.av_process
		if pid > 0 && C.av_identity(pid, &row) == 0 {
			out = append(out, Identity{PID: int(row.pid), PPID: int(row.ppid), Name: C.GoString(&row.name[0]), StartedNS: int64(row.started)})
		}
	}
	return out, nil
}
func (platformReader) Read(ctx context.Context, node Identity) (Counters, error) {
	if err := ctx.Err(); err != nil {
		return Counters{}, err
	}
	var row C.av_process
	code := C.av_counters(C.int(node.PID), &row)
	if code == -1 {
		return Counters{}, errors.New("进程已退出或无权读取身份")
	}
	out := Counters{Identity: Identity{PID: int(row.pid), PPID: int(row.ppid), Name: C.GoString(&row.name[0]), StartedNS: int64(row.started)}, PrivateKind: "physicalFootprint"}
	if code != 0 {
		out.Issue = fmt.Sprintf("PID %d资源读取受系统限制", node.PID)
		return out, nil
	}
	rss, private, cpu := uint64(row.rss), uint64(row.footprint), uint64(row.cpu)
	out.RSSBytes = &rss
	out.PrivateBytes = &private
	out.CPUTimeNS = &cpu
	return out, nil
}
