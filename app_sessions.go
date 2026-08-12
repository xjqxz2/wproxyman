package main

// 文件说明： 会话与导入导出：保存/打开会话、HAR 导入导出、cURL 导入。

import (
	"wproxyman/internal/codegen"
	"wproxyman/internal/models"
	"wproxyman/internal/storage"
)

// SaveSession writes the captured flows to path.
func (a *App) SaveSession(path string) error {
	a.mu.RLock()
	flows := make([]*models.Flow, len(a.flows))
	copy(flows, a.flows)
	a.mu.RUnlock()
	// Mark all as saved.
	for _, f := range flows {
		f.IsSaved = true
	}
	return storage.SaveSession(path, flows)
}

// OpenSession loads flows from a session file and replaces the current list.
func (a *App) OpenSession(path string) ([]*models.Flow, error) {
	flows, err := storage.OpenSession(path)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.flows = nil
	a.flowIdx = make(map[string]*models.Flow)
	for _, f := range flows {
		if f.ID == "" {
			f.ID = models.GenID()
		}
		f.IsSaved = true
		a.flowIdx[f.ID] = f
		a.flows = append(a.flows, f)
	}
	a.mu.Unlock()
	a.emit("flows:replaced", flows)
	return flows, nil
}

// ImportSession appends flows from a session file.
func (a *App) ImportSession(path string) ([]*models.Flow, error) {
	flows, err := storage.OpenSession(path)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	for _, f := range flows {
		if f.ID == "" {
			f.ID = models.GenID()
		}
		f.IsSaved = true
		a.flowIdx[f.ID] = f
		a.flows = append(a.flows, f)
	}
	a.mu.Unlock()
	a.emit("flows:imported", flows)
	return flows, nil
}

// ExportHAR writes selected flows (or all) to path in HAR format.
func (a *App) ExportHAR(path string, ids []string) error {
	a.mu.RLock()
	flows := make([]*models.Flow, 0)
	if len(ids) == 0 {
		flows = append(flows, a.flows...)
	} else {
		for _, id := range ids {
			if f, ok := a.flowIdx[id]; ok {
				flows = append(flows, f)
			}
		}
	}
	a.mu.RUnlock()
	return storage.ExportHAR(path, flows)
}

// ImportHAR imports a HAR file into the flow store.
func (a *App) ImportHAR(path string) ([]*models.Flow, error) {
	flows, err := storage.ImportHAR(path)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	for _, f := range flows {
		a.flowIdx[f.ID] = f
		a.flows = append(a.flows, f)
	}
	a.mu.Unlock()
	a.emit("flows:imported", flows)
	return flows, nil
}

// ImportCurlText parses a cURL command into a flow (for Compose).
func (a *App) ImportCurlText(text string) (*models.Flow, error) {
	return importCurl(text)
}

// ImportCurlFromFile reads and parses a file containing a cURL command.
func (a *App) ImportCurlFromFile(path string) (*models.Flow, error) {
	content, err := readFileString(path)
	if err != nil {
		return nil, err
	}
	return importCurl(content)
}

func importCurl(text string) (*models.Flow, error) {
	flow, err := codegen.ParseCurl(text)
	if err != nil {
		return nil, err
	}
	flow.IsSaved = false
	return flow, nil
}
