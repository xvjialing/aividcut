package main

import (
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// 原生选择器统一附着编辑窗口；取消仍返回空路径，由调用方保留当前工程。
type openDialogOptions struct {
	Title, DefaultFilename string
	Filters                []application.FileFilter
	AllowsOtherFileTypes   bool
	Directories            bool
}
type saveDialogOptions struct {
	Title, DefaultFilename string
	Filters                []application.FileFilter
	CanCreateDirectories   bool
}

func (a *App) openDialog(options openDialogOptions) *application.OpenFileDialogStruct {
	dialog := a.desktop.Dialog.OpenFile().SetTitle(options.Title).CanChooseFiles(!options.Directories).CanChooseDirectories(options.Directories).AllowsOtherFileTypes(options.AllowsOtherFileTypes).AttachToWindow(a.window)
	for _, filter := range options.Filters {
		dialog.AddFilter(filter.DisplayName, filter.Pattern)
	}
	if filepath.IsAbs(options.DefaultFilename) {
		dialog.SetDirectory(filepath.Dir(options.DefaultFilename))
	}
	return dialog
}
func (a *App) openFile(options openDialogOptions) (string, error) {
	return a.openDialog(options).PromptForSingleSelection()
}
func (a *App) openFiles(options openDialogOptions) ([]string, error) {
	return a.openDialog(options).PromptForMultipleSelection()
}
func (a *App) saveFile(options saveDialogOptions) (string, error) {
	dialog := a.desktop.Dialog.SaveFile().SetMessage(options.Title).SetFilename(options.DefaultFilename).CanCreateDirectories(options.CanCreateDirectories).AllowsOtherFileTypes(false).AttachToWindow(a.window)
	for _, filter := range options.Filters {
		dialog.AddFilter(filter.DisplayName, filter.Pattern)
	}
	return dialog.PromptForSingleSelection()
}
