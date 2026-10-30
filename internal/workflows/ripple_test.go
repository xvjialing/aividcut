package workflows

import (
	"aividcut/internal/editor"
	"fmt"
	"reflect"
	"testing"
)

func TestRippleWorkflowMatchesCoreAndIncludesAllInstances(t *testing.T) {
	p := wordCutProject(t)
	plan, err := PreviewRippleDelete(p, RippleInput{ProjectID: p.ID, ProjectRevision: p.Revision, FromFrame: 30, ToFrame: 45})
	if err != nil {
		t.Fatal(err)
	}
	if plan.RangeSummary == nil || plan.SavedFrames != 15 || plan.RangeSummary.Moved == 0 {
		t.Fatal("未包含未选实例全局影响", plan)
	}
	_, after := applyAlgorithm(t, p, plan)
	_, twice := applyAlgorithm(t, p, plan)
	if !reflect.DeepEqual(after, twice) {
		t.Fatal("字幕/轨道的新标识在预览后改变")
	}
	if clipWithID(after, "v2").StartFrame != 165 {
		t.Fatal("其它实例未前移")
	}
	for _, id := range plan.AffectedClipIDs {
		found := false
		for _, c := range p.Clips {
			found = found || c.ID == id
		}
		for _, c := range after.Clips {
			found = found || c.ID == id
		}
		if !found {
			t.Fatal("预览生成幽灵ID", id)
		}
	}
	if !reflect.DeepEqual(p.Transcripts[0].RawWords, after.Transcripts[0].RawWords) {
		t.Fatal("原话改变")
	}
	in := wordCutInput(p, "w2")
	in.CloseGap = false
	in.RippleAll = true
	wordPlan, err := PreviewTranscriptCut(p, in)
	if err != nil || wordPlan.WorkflowID != "ripple-delete-range" || wordPlan.RangeSummary.FromFrame != 30 {
		t.Fatal("按词全轨未复用核心", err)
	}
}

func TestRippleLargePreviewIsBoundedAndFullCommand(t *testing.T) {
	p := editor.NewProject("万片段全轨")
	for i := 0; i < 10000; i++ {
		c := editor.DefaultClip(fmt.Sprintf("title-%d", i), "track-text-1", "text")
		c.Text = "真实工程标题"
		c.StartFrame = int64(i * 3)
		c.DurationFrames = 2
		p.Clips = append(p.Clips, c)
	}
	plan, err := PreviewRippleDelete(p, RippleInput{ProjectID: p.ID, FromFrame: 10000, ToFrame: 10010})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Request.Commands) != 1 || len(plan.Effects) > 200 || len(plan.AffectedClipIDs) > 2000 || plan.RangeSummary.TotalAffectedClips <= 2000 {
		t.Fatal("大工程候选未有界或漏掉实际影响")
	}
	_, after := applyAlgorithm(t, p, plan)
	if editor.DurationFrames(after) != editor.DurationFrames(p)-10 {
		t.Fatal("只处理了候选分页片段")
	}
	t.Logf("10000片段实际命令完成：影响%d，返回%d标识/%d明细", plan.RangeSummary.TotalAffectedClips, len(plan.AffectedClipIDs), len(plan.Effects))
}
