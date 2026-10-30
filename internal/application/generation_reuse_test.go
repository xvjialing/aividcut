package application

import (
	"aividcut/internal/generation"
	"aividcut/internal/generationjobs"
	"aividcut/internal/media"
	"aividcut/internal/projectstore"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerationReuseReadOnlyCandidateImportUndoAndParent(t *testing.T) {
	directory := t.TempDir()
	store := &appSecretStore{values: map[string][]byte{}}
	control := &appProviderControl{}
	service := configuredGenerationService(t, directory, store, control)
	connection := appSaveConnection(t, service, "", "fixture-not-a-real-key")
	control.submit = func(_ context.Context, r generation.Request) (generation.Task, error) {
		task := appTask(r, generation.Succeeded, "")
		task.Recipe.ParentRecipeID = r.ParentRecipeID
		task.Recipe.Size = r.Size
		task.Recipe.Quality = r.Quality
		task.Recipe.OutputFormat = r.OutputFormat
		task.Recipe.Background = r.Background
		for range r.Count {
			task.Outputs = append(task.Outputs, appImageOutput())
		}
		return task, nil
	}
	input := appSubmit("reuse-origin", connection.ID, 2)
	input.Request.Size = "1536x864"
	input.Request.Quality = "high"
	input.Request.OutputFormat = "png"
	input.Request.Background = "transparent"
	if _, err := service.SubmitGeneration(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	job := appAwaitJob(t, service, input.Request.ID, generationjobs.Succeeded)
	before := service.GetBootstrap()
	reads, posts := store.readCount(), control.posts.Load()
	request := GenerationReuseInput{JobID: job.ID, ProjectID: before.Project.ID, SourceEpoch: before.SourceEpoch}
	plan, err := service.PrepareGenerationReuse(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Request.ID != "" || plan.Request.ParentRecipeID != job.ID || plan.Request.Count != 2 || plan.Request.Size != "1536x864" || plan.Request.Quality != "high" || plan.Request.Background != "transparent" {
		t.Fatal("未完整保留配方参数")
	}
	if service.GetBootstrap().Project.Revision != before.Project.Revision || store.readCount() != reads || control.posts.Load() != posts {
		t.Fatal("只读准备访问了凭据/模型或编辑工程")
	}
	request.CandidateID = job.Assets[0].ID
	edit, err := service.PrepareGenerationReuse(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !edit.NeedsImport || edit.Request.Mode != generation.ImageEdit || edit.Request.Count != 1 || edit.Candidate == nil || len(edit.Request.References) != 1 {
		t.Fatal("图片修改未准备单张参考")
	}
	bytes, err := os.ReadFile(job.Assets[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(job.Assets[0].Path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = service.PrepareGenerationReuse(context.Background(), request); err == nil {
		t.Fatal("篡改的候选通过来源核对")
	}
	if err = os.WriteFile(job.Assets[0].Path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	imported, err := service.ImportGenerationAssets(context.Background(), job.ID, []string{request.CandidateID})
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.PrepareGenerationReuse(context.Background(), request)
	if err != nil || again.NeedsImport {
		t.Fatal("已登记图片未复用", err)
	}
	if len(imported.Project.Clips) != len(before.Project.Clips) {
		t.Fatal("继续修改自动上轨")
	}
	if _, err = service.Undo(); err != nil {
		t.Fatal(err)
	}
	again, err = service.PrepareGenerationReuse(context.Background(), request)
	if err != nil || !again.NeedsImport {
		t.Fatal("撤销后素材状态错误", err)
	}
	child := input
	child.Request = plan.Request
	child.Request.ID = "reuse-child"
	child.Consent = true
	if _, err = service.SubmitGeneration(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	appAwaitJob(t, service, child.Request.ID, generationjobs.Succeeded)
	recipe, err := service.generation.jobs.CompletedRecipe(child.Request.ID)
	if err != nil || recipe.ParentRecipeID != job.ID {
		t.Fatal("子配方来源未保存", err)
	}
	saved := filepath.Join(t.TempDir(), "reuse.aividcut")
	if _, err = service.SaveProjectTo(saved); err != nil {
		t.Fatal(err)
	}
	service.Close()
	restored := configuredGenerationService(t, directory, store, control)
	if _, err = restored.OpenProjectPath(saved); err != nil {
		t.Fatal(err)
	}
	b := restored.GetBootstrap()
	if _, err = restored.PrepareGenerationReuse(context.Background(), GenerationReuseInput{JobID: child.Request.ID, ProjectID: b.Project.ID, SourceEpoch: b.SourceEpoch}); err != nil {
		t.Fatal("持久配方不能重开准备", err)
	}
}

func TestGenerationReviewContextRejectsBeforeCredentialsAndScopeReopen(t *testing.T) {
	s, store, control := captureApplication(t, media.Toolchain{})
	profile := appSaveConnection(t, s, "", "fixture-only")
	b := s.GetBootstrap()
	epoch := b.SourceEpoch
	input := appSubmit("context-request", profile.ID, 1)
	input.Context = &generationjobs.ProjectContext{ProjectID: b.Project.ID, Revision: b.Project.Revision + 1, SourceEpoch: &epoch}
	if _, err := s.SubmitGeneration(context.Background(), input); err == nil {
		t.Fatal("旧修订通过")
	}
	input.Context.Revision = b.Project.Revision
	badEpoch := epoch + 1
	input.Context.SourceEpoch = &badEpoch
	if _, err := s.SubmitGeneration(context.Background(), input); err == nil {
		t.Fatal("旧实例通过")
	}
	input.Context.SourceEpoch = &epoch
	input.Request.ParentRecipeID = "nonexistent-parent"
	if _, err := s.SubmitGeneration(context.Background(), input); err == nil {
		t.Fatal("伪造来源通过")
	}
	path := filepath.Join(t.TempDir(), "context.aividcut")
	if _, err := s.SaveProjectTo(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenProjectPath(path); err != nil {
		t.Fatal(err)
	}
	input.Request.ParentRecipeID = ""
	if _, err := s.SubmitGeneration(context.Background(), input); err == nil {
		t.Fatal("同ID重开复用旧确认")
	}
	if store.readCount() != 0 || control.posts.Load() != 0 {
		t.Fatal("拒绝前访问凭据/模型")
	}
}

func TestGenerationReuseReferenceProofSurvivesChangedProjectSource(t *testing.T) {
	store := &appSecretStore{values: map[string][]byte{}}
	control := &appProviderControl{}
	s := configuredGenerationService(t, t.TempDir(), store, control)
	profile := appSaveConnection(t, s, "", "fixture-only")
	source := sourceFrameFixture(t, s)
	control.submit = func(_ context.Context, r generation.Request) (generation.Task, error) {
		task := appTask(r, generation.Succeeded, "")
		task.Outputs = []generation.Output{appImageOutput()}
		task.Recipe.References = []generation.ReferenceRecipe{{AssetID: source.ID, Role: "reference_image", MIME: "image/png", SHA256: strings.TrimPrefix(source.Fingerprint, "sha256:")}}
		return task, nil
	}
	input := appSubmit("reference-origin", profile.ID, 1)
	input.Request.Mode = generation.ImageEdit
	input.Request.References = []generation.Reference{{AssetID: source.ID, Role: "reference_image"}}
	if _, err := s.SubmitGeneration(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	appAwaitJob(t, s, input.Request.ID, generationjobs.Succeeded)
	replacement := filepath.Join(t.TempDir(), "replacement.png")
	f, err := os.Create(replacement)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 32, 18))
	img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	err = png.Encode(f, img)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	changed, err := (media.Toolchain{}).Probe(context.Background(), replacement)
	if err != nil {
		t.Fatal(err)
	}
	current := s.GetBootstrap().Project
	changed.ID = source.ID
	current.Assets[assetIndex(current, source.ID)] = changed
	path := filepath.Join(t.TempDir(), "changed-source.aividcut")
	if err = projectstore.Save(path, current); err != nil {
		t.Fatal(err)
	}
	if _, err = s.OpenProjectPath(path); err != nil {
		t.Fatal(err)
	}
	b := s.GetBootstrap()
	reads, posts := store.readCount(), control.posts.Load()
	plan, err := s.PrepareGenerationReuse(context.Background(), GenerationReuseInput{JobID: input.Request.ID, ProjectID: b.Project.ID, SourceEpoch: b.SourceEpoch})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Warnings) == 0 || plan.ReferenceFingerprints[source.ID] != source.Fingerprint {
		t.Fatal("新文件覆盖了原配方参考证明")
	}
	if store.readCount() != reads || control.posts.Load() != posts {
		t.Fatal("参考核对触发模型")
	}
}
