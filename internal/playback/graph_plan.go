package playback

import (
	"errors"
	"sort"

	"aividcut/internal/editor"
)

type clipActivity struct {
	index      int
	start, end int64
}
type activityNode struct {
	clip        clipActivity
	maxEnd      int64
	left, right *activityNode
}
type graphRange struct {
	from, to int64
	clips    []editor.Clip
}
type graphPlan struct {
	visualSources    map[string]VisualSource
	layoutScale      float64
	project          editor.Project
	duration, frames int64
	limit            int
	target           int
	activities       []clipActivity
	root             *activityNode
}

func prepareGraphPlan(project editor.Project, caps Capabilities, limit int, frames int64) (*graphPlan, error) {
	return prepareScaledGraphPlan(project, caps, limit, frames, 1)
}
func prepareScaledGraphPlan(project editor.Project, caps Capabilities, limit int, frames int64, layoutScale float64) (*graphPlan, error) {
	preferred := limit
	if !caps.WindowedGraph.Supported {
		return nil, errors.New("当前worker不支持保留源时钟的图窗口替换，请更新worker")
	}
	if limit == 0 {
		limit = 128
	}
	if limit < 1 || limit > 128 || frames < 0 || frames > 7200 {
		return nil, errors.New("图窗口需1–128片段、最多7200帧")
	}
	if len(project.Clips) > 100000 {
		return nil, errors.New("原生工程索引最多100000个片段，请拆分序列")
	}
	if err := editor.ValidateProject(project); err != nil {
		return nil, err
	}
	p := &graphPlan{project: editor.CloneProject(project), limit: limit, frames: frames, layoutScale: layoutScale}
	for _, clip := range p.project.Clips {
		p.duration = max(p.duration, clip.StartFrame+clip.DurationFrames)
	}
	if p.frames == 0 {
		p.frames = max(int64(1), (int64(project.Settings.FPSNum)*8+int64(project.Settings.FPSDen)-1)/int64(project.Settings.FPSDen))
	}
	if len(project.Clips) == 0 {
		if _, _, err := compileScaledSelection(p.project, nil, caps, 0, true, layoutScale); err != nil {
			return nil, err
		}
	}
	// 分批验证全部片段，丢弃临时XML；后段不支持的语义不能被窗口隐藏。
	for at := 0; at < len(p.project.Clips); at += 128 {
		if _, _, err := compileScaledSelection(p.project, p.project.Clips[at:min(at+128, len(p.project.Clips))], caps, p.duration, true, layoutScale); err != nil {
			return nil, err
		}
	}
	assets := map[string]editor.Asset{}
	tracks := map[string]editor.Track{}
	for _, a := range p.project.Assets {
		assets[a.ID] = a
	}
	for _, t := range p.project.Tracks {
		tracks[t.ID] = t
	}
	type event struct {
		at    int64
		delta int
	}
	events := make([]event, 0, len(p.project.Clips)*2)
	for index, clip := range p.project.Clips {
		track := tracks[clip.TrackID]
		asset := assets[clip.AssetID]
		start, end := p.duration, int64(0)
		if clip.Kind != "audio" && !track.Hidden {
			start, end = clip.StartFrame, clip.StartFrame+clip.DurationFrames
		}
		if (clip.Kind == "audio" || clip.Kind == "video") && asset.HasAudio && !clip.Muted && !track.Muted && clip.Volume > 0 {
			resolved, err := editor.ResolveAudioRange(clip, asset, p.project.Settings)
			if err != nil {
				return nil, err
			}
			if !resolved.Empty {
				a, err := sourceTimeFrame(resolved.TimelineStart, p.project.Settings, false)
				if err != nil {
					return nil, err
				}
				b, err := sourceTimeFrame(resolved.TimelineEnd, p.project.Settings, true)
				if err != nil {
					return nil, err
				}
				start, end = min(start, a), max(end, b)
			}
		}
		start, end = max(0, start), min(p.duration, end)
		if start < end {
			p.activities = append(p.activities, clipActivity{index, start, end})
			events = append(events, event{start, 1}, event{end, -1})
		}
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].at != events[j].at {
			return events[i].at < events[j].at
		}
		return events[i].delta < events[j].delta
	})
	live, maxLive := 0, 0
	for _, event := range events {
		live += event.delta
		maxLive = max(maxLive, live)
		if live > limit {
			return nil, errors.New("同一帧有效片段超过当前图窗口上限；请减少重叠图层或音轨")
		}
	}
	p.target = limit
	if preferred == 0 {
		p.target = min(limit, max(32, maxLive))
	}
	sort.Slice(p.activities, func(i, j int) bool {
		a, b := p.activities[i], p.activities[j]
		if a.start != b.start {
			return a.start < b.start
		}
		return a.index < b.index
	})
	var build func(int, int) *activityNode
	build = func(lo, hi int) *activityNode {
		if lo >= hi {
			return nil
		}
		mid := (lo + hi) / 2
		node := &activityNode{clip: p.activities[mid]}
		node.left, node.right = build(lo, mid), build(mid+1, hi)
		node.maxEnd = node.clip.end
		if node.left != nil {
			node.maxEnd = max(node.maxEnd, node.left.maxEnd)
		}
		if node.right != nil {
			node.maxEnd = max(node.maxEnd, node.right.maxEnd)
		}
		return node
	}
	p.root = build(0, len(p.activities))
	return p, nil
}

func (p *graphPlan) intersect(from, to int64) []int {
	result := make([]int, 0, min(p.limit+1, len(p.activities)))
	var visit func(*activityNode)
	visit = func(node *activityNode) {
		if node == nil || node.maxEnd <= from || len(result) > p.target {
			return
		}
		visit(node.left)
		if len(result) > p.target {
			return
		}
		if node.clip.start >= to {
			return
		}
		if node.clip.end > from {
			result = append(result, node.clip.index)
		}
		visit(node.right)
	}
	visit(p.root)
	return result
}
func (p *graphPlan) window(at int64) (graphRange, error) {
	if at < 0 || at >= p.duration {
		return graphRange{}, errors.New("播放位置超出工程")
	}
	from := at / p.frames * p.frames
	to := min(p.duration, from+p.frames)
	indices := p.intersect(from, to)
	if len(indices) > p.target {
		from = at
		low, high := at+1, to
		for low < high {
			mid := (low + high + 1) / 2
			if len(p.intersect(from, mid)) <= p.target {
				low = mid
			} else {
				high = mid - 1
			}
		}
		to = low
		indices = p.intersect(from, to)
		if len(indices) > p.target {
			return graphRange{}, errors.New("当前位置重叠片段超过原生窗口预算")
		}
	}
	if len(indices) == 0 {
		next := sort.Search(len(p.activities), func(i int) bool { return p.activities[i].start >= to })
		if next < len(p.activities) {
			to = p.activities[next].start
		} else {
			to = p.duration
		}
	}
	clips := make([]editor.Clip, 0, len(indices))
	for _, index := range indices {
		clips = append(clips, p.project.Clips[index])
	}
	return graphRange{from, to, clips}, nil
}
