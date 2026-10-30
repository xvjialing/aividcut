// Package contentindex 提供接受版本的字面检索与源区间证据，不推断视觉或语义事实。
package contentindex

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"aividcut/internal/editor"
)

const Version = "accepted-content-v2"
const maxTextBytes = 32 << 20

type Query struct {
	Text   string `json:"text"`
	Origin string `json:"origin,omitempty"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}
type Hit struct {
	Kind               string                       `json:"kind,omitempty"`
	AnalysisID         string                       `json:"analysisId,omitempty"`
	NoteID             string                       `json:"noteId,omitempty"`
	NoteKind           string                       `json:"noteKind,omitempty"`
	Producer           string                       `json:"producer,omitempty"`
	Tags               []string                     `json:"tags,omitempty"`
	Frames             []editor.SourceFrameEvidence `json:"frames,omitempty"`
	ID                 string                       `json:"id"`
	AssetID            string                       `json:"assetId"`
	AssetName          string                       `json:"assetName"`
	TranscriptID       string                       `json:"transcriptId"`
	TranscriptRevision int64                        `json:"transcriptRevision"`
	SourceFingerprint  string                       `json:"sourceFingerprint"`
	Origin             string                       `json:"origin"`
	StartUs            int64                        `json:"startUs"`
	EndUs              int64                        `json:"endUs"`
	WordIDs            []string                     `json:"wordIds"`
	Text               string                       `json:"text"`
}
type Stats struct {
	Analyses       int `json:"analyses"`
	VisualNotes    int `json:"visualNotes"`
	Transcripts    int `json:"transcripts"`
	Words          int `json:"words"`
	SkippedStale   int `json:"skippedStale"`
	SkippedMissing int `json:"skippedMissing"`
}
type Result struct {
	Version string `json:"version"`
	Query   string `json:"query"`
	Origin  string `json:"origin"`
	Offset  int    `json:"offset"`
	Total   int    `json:"total"`
	Hits    []Hit  `json:"hits"`
	Stats   Stats  `json:"stats"`
}
type document struct {
	assetID, name, transcriptID, fingerprint, origin string
	revision                                         int64
	words                                            []editor.SourceWord
	text, original                                   string
	ends                                             []int // 小写文本中的UTF-8字节末端，避免每次命中扫描整个前缀。
}
type visualDocument struct {
	asset    editor.Asset
	analysis editor.VisualAnalysis
}
type Index struct {
	visuals   []visualDocument
	documents []document
	stats     Stats
}

func Validate(q Query) error {
	text := strings.TrimSpace(q.Text)
	if !utf8.ValidString(text) || text == "" || utf8.RuneCountInString(text) > 100 {
		return errors.New("请输入1–100个字符作为检索内容")
	}
	if q.Origin != "" && q.Origin != "asr" && q.Origin != "tts" && q.Origin != "visual" {
		return errors.New("检索来源仅支持原话转写、生成配音或画面分析")
	}
	if q.Offset < 0 || q.Limit < 0 || q.Limit > 100 {
		return errors.New("检索分页起点不能为负，单页最多100项")
	}
	return nil
}

func Build(ctx context.Context, p editor.Project, assetID string) (*Index, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	index := &Index{}
	assets := map[string]editor.Asset{}
	for _, a := range p.Assets {
		assets[a.ID] = a
	}
	if assetID != "" {
		if _, ok := assets[assetID]; !ok {
			return nil, errors.New("检索素材已不存在")
		}
	}
	transcripts := append([]editor.Transcript{}, p.Transcripts...)
	sort.Slice(transcripts, func(i, j int) bool {
		if transcripts[i].AssetID == transcripts[j].AssetID {
			return transcripts[i].ID < transcripts[j].ID
		}
		return transcripts[i].AssetID < transcripts[j].AssetID
	})
	bytes := 0
	for _, t := range transcripts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if assetID != "" && t.AssetID != assetID {
			continue
		}
		a, ok := assets[t.AssetID]
		if !ok || a.Missing {
			index.stats.SkippedMissing++
			continue
		}
		if t.Stale || t.SourceFingerprint == "" || t.SourceFingerprint != a.Fingerprint || t.SourceDurationUs != a.DurationUs {
			index.stats.SkippedStale++
			continue
		}
		for _, word := range t.AcceptedWords {
			bytes += len(word.Text)
		}
		if bytes > maxTextBytes {
			return nil, errors.New("接受文本超过当前32MiB索引上限，请选择单个素材缩小检索范围")
		}
		ordered, _, _ := editor.SourceTextParts(t.AcceptedWords)
		index.stats.Transcripts++
		index.stats.Words += len(ordered)
		start := 0
		for end := 1; end <= len(ordered); end++ {
			if end < len(ordered) && ordered[end].SpeakerID == ordered[end-1].SpeakerID && ordered[end].StartUs-ordered[end-1].EndUs <= 2_000_000 {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			words, text, runeEnds := editor.SourceTextParts(ordered[start:end])
			lower := strings.ToLower(text)
			byteEnds := make([]int, len(runeEnds))
			at, runes := 0, 0
			for i := range byteEnds {
				for runes < runeEnds[i] {
					_, size := utf8.DecodeRuneInString(lower[at:])
					at += size
					runes++
				}
				byteEnds[i] = at
			}
			origin := t.Origin
			index.documents = append(index.documents, document{assetID: a.ID, name: a.Name, transcriptID: t.ID, fingerprint: a.Fingerprint, origin: origin, revision: t.Revision, words: words, text: lower, original: text, ends: byteEnds})
			start = end
		}
	}
	for _, analysis := range p.VisualAnalyses {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if assetID != "" && analysis.AssetID != assetID {
			continue
		}
		asset, ok := assets[analysis.AssetID]
		if !ok || asset.Missing {
			index.stats.SkippedMissing++
			continue
		}
		if analysis.Stale || analysis.SourceFingerprint != asset.Fingerprint || analysis.SourceDurationUs != asset.DurationUs || analysis.SourceKind != asset.Kind {
			index.stats.SkippedStale++
			continue
		}
		for _, note := range analysis.Notes {
			bytes += len(note.Text)
			for _, tag := range note.Tags {
				bytes += len(tag)
			}
		}
		if bytes > maxTextBytes {
			return nil, errors.New("接受内容超过当前32MiB索引上限，请选择单个素材")
		}
		index.visuals = append(index.visuals, visualDocument{asset: asset, analysis: editor.CloneVisualAnalysis(analysis)})
		index.stats.Analyses++
		index.stats.VisualNotes += len(analysis.Notes)
	}
	sort.Slice(index.visuals, func(i, j int) bool { return index.visuals[i].analysis.ID < index.visuals[j].analysis.ID })
	return index, nil
}

func (index *Index) Search(ctx context.Context, q Query) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := Validate(q); err != nil {
		return Result{}, err
	}
	result := Result{Version: Version, Query: strings.TrimSpace(q.Text), Origin: q.Origin, Offset: q.Offset, Hits: []Hit{}, Stats: index.stats}
	limit := q.Limit
	if limit == 0 {
		limit = 40
	}
	needle := strings.ToLower(result.Query)
	for _, doc := range index.documents {
		if q.Origin != "" && doc.origin != q.Origin {
			continue
		}
		from, runeFrom, rawCursor, rawRunes := 0, 0, 0, 0
		for from < len(doc.text) {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			match := strings.Index(doc.text[from:], needle)
			if match < 0 {
				break
			}
			match += from
			runeMatch := runeFrom + utf8.RuneCountInString(doc.text[from:match])
			end := match + len(needle)
			from, runeFrom = end, runeMatch+utf8.RuneCountInString(needle)
			result.Total++
			if result.Total <= q.Offset || len(result.Hits) >= limit {
				continue
			}
			first := sort.Search(len(doc.ends), func(i int) bool { return doc.ends[i] > match })
			last := sort.Search(len(doc.ends), func(i int) bool { return doc.ends[i] >= end })
			if first >= len(doc.words) || last >= len(doc.words) {
				return Result{}, errors.New("源文字索引边界无效，请重新读取工程")
			}
			ids := make([]string, 0, last-first+1)
			endUs := int64(0)
			for _, word := range doc.words[first : last+1] {
				ids = append(ids, word.ID)
				endUs = max(endUs, word.EndUs)
			}
			// 只推进原文游标，不对每个命中重新扫描长文本，保留原文大小写。
			snippetStart := max(0, runeMatch-60)
			for rawRunes < snippetStart {
				_, size := utf8.DecodeRuneInString(doc.original[rawCursor:])
				rawCursor += size
				rawRunes++
			}
			snippetEnd, endRunes := rawCursor, rawRunes
			for snippetEnd < len(doc.original) && endRunes < runeFrom+80 {
				_, size := utf8.DecodeRuneInString(doc.original[snippetEnd:])
				snippetEnd += size
				endRunes++
			}
			text := doc.original[rawCursor:snippetEnd]
			if rawCursor > 0 {
				text = "…" + text
			}
			if snippetEnd < len(doc.original) {
				text += "…"
			}
			result.Hits = append(result.Hits, Hit{Kind: "transcript", ID: fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%d", doc.transcriptID, doc.revision, doc.words[0].ID, match)))), AssetID: doc.assetID, AssetName: doc.name, TranscriptID: doc.transcriptID, TranscriptRevision: doc.revision, SourceFingerprint: doc.fingerprint, Origin: doc.origin, StartUs: doc.words[first].StartUs, EndUs: endUs, WordIDs: ids, Text: text})
		}
	}
	if q.Origin == "" || q.Origin == "visual" {
		for _, doc := range index.visuals {
			for _, note := range doc.analysis.Notes {
				if err := ctx.Err(); err != nil {
					return Result{}, err
				}
				match := strings.Contains(strings.ToLower(note.Text), needle)
				for _, tag := range note.Tags {
					match = match || strings.Contains(strings.ToLower(tag), needle)
				}
				if !match {
					continue
				}
				result.Total++
				if result.Total <= q.Offset || len(result.Hits) >= limit {
					continue
				}
				used := map[string]bool{}
				for _, id := range note.EvidenceIDs {
					used[id] = true
				}
				frames := []editor.SourceFrameEvidence{}
				for _, frame := range doc.analysis.Frames {
					if used[frame.ID] {
						frames = append(frames, frame)
					}
				}
				sort.Slice(frames, func(i, j int) bool { return frames[i].PositionUs < frames[j].PositionUs })
				if len(frames) == 0 {
					return Result{}, errors.New("画面分析缺少证据")
				}
				text := note.Text
				runes := []rune(text)
				if len(runes) > 240 {
					at := strings.Index(strings.ToLower(text), needle)
					start := 0
					if at >= 0 {
						start = max(0, utf8.RuneCountInString(strings.ToLower(text)[:at])-60)
					}
					text = string(runes[start:min(len(runes), start+240)]) + "…"
					if start > 0 {
						text = "…" + text
					}
				}
				result.Hits = append(result.Hits, Hit{ID: doc.analysis.ID + ":" + note.ID, Kind: "visual", AnalysisID: doc.analysis.ID, NoteID: note.ID, NoteKind: note.Kind, Producer: doc.analysis.Producer, Tags: append([]string{}, note.Tags...), Frames: frames, AssetID: doc.asset.ID, AssetName: doc.asset.Name, SourceFingerprint: doc.analysis.SourceFingerprint, Origin: "visual", StartUs: frames[0].PositionUs, EndUs: frames[len(frames)-1].PositionUs, WordIDs: []string{}, Text: text})
			}
		}
	}
	return result, nil
}
