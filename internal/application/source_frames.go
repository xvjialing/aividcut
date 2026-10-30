package application

import (
	"aividcut/internal/media"
	"context"
	"errors"
	"fmt"
	"os"

	"aividcut/internal/editor"
	"aividcut/internal/mcpserver"
)

type SourceFrameEvidence = editor.SourceFrameEvidence
type SourceFrameRequest struct {
	ThumbnailRequest
	Profile string `json:"profile"`
}
type SourceFrame struct {
	Evidence SourceFrameEvidence `json:"evidence"`
	URL      string              `json:"url"`
}
type sourceFrameRead struct {
	Evidence SourceFrameEvidence
	URL      string
	JPEG     []byte
}

func frameProfile(value string) (string, error) {
	if value == "" {
		return "detail", nil
	}
	if value != "detail" && value != "overview" {
		return "", errors.New("源画面仅支持detail或overview档位")
	}
	return value, nil
}
func (s *Service) GetSourceFrame(ctx context.Context, input SourceFrameRequest) (SourceFrame, error) {
	profile, err := frameProfile(input.Profile)
	if err != nil {
		return SourceFrame{}, err
	}
	value, err := s.readSourceFrame(ctx, input.ThumbnailRequest, profile, true, nil, true)
	if err != nil {
		return SourceFrame{}, err
	}
	return SourceFrame{Evidence: value.Evidence, URL: value.URL}, nil
}

// 仅接受当前已授权工程中的素材ID/源位置，不开放路径或远端URL。
func (s *Service) readMCPSourceFrames(ctx context.Context, scope *mcpProjectScope, assetID string, positions []int64, profile string) (mcpserver.ImageResult, error) {
	profile, err := frameProfile(profile)
	if err != nil {
		return mcpserver.ImageResult{}, err
	}
	if len(positions) < 1 || len(positions) > 3 {
		return mcpserver.ImageResult{}, errors.New("每次需要1–3个源位置")
	}
	s.mu.Lock()
	if err := scope.checkLocked(s); err != nil || !scope.allowSourceFrames {
		s.mu.Unlock()
		return mcpserver.ImageResult{}, errors.New("当前连接没有源画面读取权限")
	}
	project := s.engine.Snapshot()
	index := assetIndex(project, assetID)
	if index < 0 {
		s.mu.Unlock()
		return mcpserver.ImageResult{}, errors.New("源素材不存在")
	}
	asset := project.Assets[index]
	s.thumbMu.Lock()
	epoch := s.thumbEpoch
	s.thumbMu.Unlock()
	s.mu.Unlock()
	evidence := make([]SourceFrameEvidence, 0, len(positions))
	images := make([]mcpserver.Image, 0, len(positions))
	seen := map[int64]bool{}
	for _, position := range positions {
		if seen[position] {
			return mcpserver.ImageResult{}, errors.New("源位置重复，请减少帧数")
		}
		seen[position] = true
		value, err := s.readSourceFrame(ctx, ThumbnailRequest{SourceEpoch: epoch, RequestID: editor.NewID("source-frame"), ProjectID: scope.projectID, AssetID: assetID, Fingerprint: asset.Fingerprint, PositionUs: position}, profile, false, scope, true)
		if err != nil {
			return mcpserver.ImageResult{}, err
		}
		evidence = append(evidence, value.Evidence)
		images = append(images, mcpserver.Image{MIME: "image/jpeg", Data: value.JPEG})
	}
	if err := ctx.Err(); err != nil {
		return mcpserver.ImageResult{}, err
	}
	s.mu.Lock()
	err = scope.checkLocked(s)
	s.mu.Unlock()
	if err != nil {
		return mcpserver.ImageResult{}, err
	}
	if err = s.recordFrameEvidence(scope, evidence); err != nil {
		return mcpserver.ImageResult{}, err
	}
	if err = s.addScopeFrameEvidence(scope, evidence); err != nil {
		return mcpserver.ImageResult{}, err
	}
	return mcpserver.ImageResult{Metadata: map[string]any{"projectId": scope.projectID, "sourceEpoch": epoch, "frames": evidence, "note": "依次对应下方JPEG图像；近似源取样，不含时间线/LUT效果。观察需引用证据ID；抽样不能代表未查看的整段。"}, Images: images}, nil
}

type frameSourceProof struct {
	key  string
	info os.FileInfo
}

// 流式核对完整SHA后按文件身份/大小/修改时间复用，避免每个取样重复扫长视频。
func (s *Service) verifyFrameSource(ctx context.Context, asset editor.Asset, epoch uint64) (os.FileInfo, error) {
	s.frameProofMu.Lock()
	defer s.frameProofMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := os.Stat(asset.Path)
	if err != nil {
		return nil, errors.New("源素材无法读取")
	}
	key := fmt.Sprintf("%d\x00%s\x00%s", epoch, asset.Path, asset.Fingerprint)
	for _, entry := range s.frameProofs {
		if entry.key == key && media.SameSourceFile(entry.info, before) {
			return before, nil
		}
	}
	if err = verifySourceFingerprint(ctx, asset.Path, asset.Fingerprint); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("源素材与已登记指纹不一致，不能作为原画面证据")
	}
	after, err := os.Stat(asset.Path)
	if err != nil || !media.SameSourceFile(before, after) {
		return nil, errors.New("核对源素材期间文件变化")
	}
	if len(s.frameProofs) >= 16 {
		s.frameProofs = s.frameProofs[1:]
	}
	s.frameProofs = append(s.frameProofs, frameSourceProof{key: key, info: after})
	return after, nil
}

// 记录的是本次工具准备的源帧；不等于传输成功或模型确已看懂图片。
func (s *Service) recordFrameEvidence(scope *mcpProjectScope, frames []SourceFrameEvidence) error {
	if scope.executionID == "" {
		return nil
	}
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	state, err := s.agentLocked()
	if err != nil {
		return err
	}
	record := state.executions[scope.executionID]
	if record == nil || record.engine != scope.engine || executionTerminal(record.Run.Status) {
		return errors.New("源画面所属执行已结束")
	}
	previous := append([]SourceFrameEvidence{}, record.Run.FrameEvidence...)
	known := map[string]bool{}
	for _, frame := range previous {
		known[frame.ID] = true
	}
	for _, frame := range frames {
		if !known[frame.ID] {
			record.Run.FrameEvidence = append(record.Run.FrameEvidence, frame)
			known[frame.ID] = true
		}
	}
	if len(record.Run.FrameEvidence) > 20 {
		record.Run.FrameEvidence = previous
		return errors.New("单次CLI执行最多准备20份不同画面证据，请缩小分析范围")
	}
	if err = state.saveExecutions(); err != nil {
		record.Run.FrameEvidence = previous
		return err
	}
	return nil
}

func frameEvidenceID(value SourceFrameEvidence) string { return editor.SourceFrameEvidenceID(value) }
func validFrameEvidence(value SourceFrameEvidence) bool {
	return editor.ValidateSourceFrameEvidence(value) == nil
}

func (s *Service) addScopeFrameEvidence(scope *mcpProjectScope, frames []SourceFrameEvidence) error {
	scope.frameMu.Lock()
	defer scope.frameMu.Unlock()
	if scope.frameEvidence == nil {
		scope.frameEvidence = map[string]SourceFrameEvidence{}
	}
	count := len(scope.frameEvidence)
	for _, frame := range frames {
		if _, ok := scope.frameEvidence[frame.ID]; !ok {
			count++
		}
	}
	if count > 128 {
		return errors.New("当前工具会话最多保留128份源帧证据，请处理候选后重新连接")
	}
	for _, frame := range frames {
		scope.frameEvidence[frame.ID] = frame
	}
	return nil
}
