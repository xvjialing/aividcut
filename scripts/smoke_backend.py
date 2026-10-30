"""使用真实 Go 开发服务和媒体文件验证编辑流程，不调用任何收费模型。"""
from pathlib import Path
import argparse
import json
import os
import struct
import subprocess
import tempfile
import time
import urllib.request
from urllib.parse import urlparse


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--base', default='http://127.0.0.1:43110')
    args = parser.parse_args()
    parsed = urlparse(args.base)
    if parsed.hostname not in ('127.0.0.1', 'localhost', '::1'):
        raise SystemExit('此验证脚本仅允许使用本地开发服务')
    root = Path(__file__).resolve().parents[1]
    (root/'work/e2e').mkdir(parents=True, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix='smoke-', dir=root/'work/e2e'))
    checks = []

    def api(method, *values, failure=False):
        payload = json.dumps({'args': values}, ensure_ascii=False).encode()
        request = urllib.request.Request(args.base+'/api/'+method, payload, {'Content-Type':'application/json'})
        with urllib.request.urlopen(request, timeout=90) as response:
            result = json.load(response)
        if failure:
            assert result.get('error'), (method, result)
            return result['error']
        if result.get('error'):
            raise RuntimeError(f'{method}: {result["error"]}')
        return result['result']

    def check(name, condition):
        assert condition, name
        checks.append(name)

    current = api('GetBootstrap')
    if current['dirty'] and not current['project']['name'].startswith('自动验证'):
        raise SystemExit('开发服务含未保存的非验证工程，停止以免覆盖')
    state = api('NewProject', '自动验证·真实多轨剪辑')
    ffmpeg = os.environ.get('AIVIDCUT_FFMPEG')
    ffprobe = os.environ.get('AIVIDCUT_FFPROBE')
    if not ffmpeg or not ffprobe:
        raise SystemExit('请显式配置开发验证用 AIVIDCUT_FFMPEG / AIVIDCUT_FFPROBE')
    video = work/'测试视频.mp4'
    subprocess.run([ffmpeg,'-hide_banner','-loglevel','error','-f','lavfi','-i','testsrc2=size=640x360:rate=30','-f','lavfi','-i','sine=frequency=440:sample_rate=48000','-t','4','-c:v','libx264','-pix_fmt','yuv420p','-c:a','aac','-shortest','-y',str(video)], check=True)
    lut = work/'恒等.cube'
    lut.write_text('LUT_3D_SIZE 2\n'+'\n'.join(f'{r} {g} {b}' for b in (0,1) for g in (0,1) for r in (0,1))+'\n')
    srt = work/'中文字幕.srt'
    srt.write_text('1\n00:00:00,500 --> 00:00:01,500\n真实字幕\n\n2\n00:00:00,500 --> 00:00:01,000\n重叠保留\n', encoding='utf-8')
    state = api('ImportPaths', [str(video),str(lut)])
    check('真实音视频和 LUT 导入', len(state['project']['assets'])==2 and any(x['hasAudio'] for x in state['project']['assets']))
    asset = next(x for x in state['project']['assets'] if x['kind']=='video')
    lut_asset = next(x for x in state['project']['assets'] if x['kind']=='lut')
    request = urllib.request.Request(asset['url'], headers={'Range':'bytes=0-31'})
    with urllib.request.urlopen(request) as response:
        check('真实素材 Range 读取', response.status==206 and len(response.read())==32)

    def clip(identifier, kind, track, duration=120):
        return {'id':identifier,'kind':kind,'name':'验证片段','trackId':track,'startFrame':0,'durationFrames':duration,'sourceInUs':0,'speedNum':1,'speedDen':1,'volume':1,'muted':False,'transform':{'x':0,'y':0,'scale':1,'rotation':0},'textStyle':{'fontSize':48,'color':'#ffffff','background':'transparent','align':'center'}}

    picture=clip('smoke-video','video','track-video-1')
    picture['assetId']=asset['id']
    picture['lut']={'assetId':lut_asset['id'],'intensity':.5}
    edit={'baseRevision':state['project']['revision'],'idempotencyKey':'smoke-add','label':'添加真实视频','commands':[{'type':'add_clip','clip':picture}]}
    state=api('ApplyEdit',edit)
    retried=api('ApplyEdit',edit)
    check('重复请求不重复剪辑', retried['project']['revision']==state['project']['revision'] and len(retried['project']['clips'])==1)
    def apply(commands, key):
        nonlocal state
        state=api('ApplyEdit',{'baseRevision':state['project']['revision'],'idempotencyKey':key,'label':key,'commands':commands})
        return state
    apply([{'type':'split_clip','clipId':'smoke-video','atFrame':60}],'smoke-split')
    halves=sorted(state['project']['clips'],key=lambda x:x['startFrame'])
    check('分割映射保留源入点', len(halves)==2 and halves[1]['sourceInUs']==2_000_000)
    second=halves[1].copy()
    second['startFrame']=75
    apply([{'type':'update_clip','clip':second}],'smoke-move')
    track=next(x for x in state['project']['tracks'] if x['kind']=='video').copy()
    track['locked']=True
    apply([{'type':'update_track','track':track}],'smoke-lock')
    revision=state['project']['revision']
    api('ApplyEdit',{'baseRevision':revision,'idempotencyKey':'smoke-delete-locked','commands':[{'type':'delete_clip','clipId':'smoke-video'}]},failure=True)
    check('锁轨禁止编辑且保持事务',api('GetBootstrap')['project']['revision']==revision)
    track['locked']=False
    apply([{'type':'update_track','track':track}],'smoke-unlock')
    title=clip('smoke-title','text','track-text-1',135)
    title['text']='AIVidCut · 真实剪辑核验'
    apply([{'type':'add_clip','clip':title}],'smoke-title')
    state=api('ImportCaptions',str(srt),'')
    check('重叠字幕完整分轨导入',sum(x['kind']=='caption' for x in state['project']['clips'])==2)
    out_srt=work/'导出字幕.srt'
    api('ExportCaptionsTo',str(out_srt),'')
    check('字幕导出保留全部文字','真实字幕' in out_srt.read_text() and '重叠保留' in out_srt.read_text())
    project=work/'保存工程.aividcut'
    state=api('SaveProjectTo',str(project))
    check('保存后状态准确',not state['dirty'] and project.exists())
    undone=api('Undo')
    check('整批字幕可撤销',sum(x['kind']=='caption' for x in undone['project']['clips'])==0 and undone['dirty'])
    state=api('Redo')
    check('重做返回已保存内容',not state['dirty'])
    api('NewProject','自动验证·重开')
    state=api('OpenProjectPath',str(project))
    check('关闭重开工程数据完整',len(state['project']['clips'])==5 and not state['dirty'])
    composed=api('GetPreviewFrame',30)
    with urllib.request.urlopen(composed['url'],timeout=60) as response:
        png=response.read()
    width,height=struct.unpack('>II',png[16:24])
    check('真实合成帧且版本一致',png.startswith(b'\x89PNG') and (width,height)==(1920,1080) and composed['revision']==state['project']['revision'])
    (work/'合成检查.png').write_bytes(png)
    output=work/'真实导出.mp4'
    job=api('ExportVideo',{'path':str(output),'preset':'720p'})
    deadline=time.monotonic()+90
    while time.monotonic()<deadline:
        current=next(x for x in api('ListJobs') if x['id']==job['id'])
        if current['status'] in ('succeeded','failed','cancelled'):break
        time.sleep(.2)
    check('导出任务提交真实文件',current['status']=='succeeded' and output.exists())
    info=json.loads(subprocess.check_output([ffprobe,'-v','error','-show_streams','-show_format','-of','json',str(output)]))
    stream=next(x for x in info['streams'] if x['codec_type']=='video')
    check('导出画幅、时长与音频正确',stream['width']==1280 and stream['height']==720 and abs(float(info['format']['duration'])-4.5)<.08 and any(x['codec_type']=='audio' for x in info['streams']))
    result={'checks':checks,'output':str(output),'project':str(project),'preview':str(work/'合成检查.png'),'nativePlayback':state['capabilities']['nativePlayback']}
    (work/'evidence.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps(result,ensure_ascii=False,indent=2))


if __name__=='__main__':main()
