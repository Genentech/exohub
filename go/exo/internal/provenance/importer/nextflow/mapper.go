package nextflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Genentech/exohub/go/exo/internal/provenance/importer"
)

// workflowRun is the "WorkflowRun" record kind (design D8): the run
// itself, its engine, pipeline reference and parameters, plus the lids of
// its tasks.
type workflowRun struct {
	Type           string         `json:"type"`
	RunName        string         `json:"runName"`
	StartTime      time.Time      `json:"startTime"`
	EndTime        time.Time      `json:"endTime"`
	Engine         engineInfo     `json:"engine"`
	Repository     string         `json:"repository"`
	CommitID       string         `json:"commitId"`
	ScriptChecksum string         `json:"scriptChecksum"`
	Params         map[string]any `json:"params"`
	Tasks          []string       `json:"tasks"`
}

type engineInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// taskRun is the "TaskRun" record kind (design D8): one step of the run,
// with its code checksum, container/conda environment, and the lids of its
// input and output records.
type taskRun struct {
	Type         string         `json:"type"`
	Name         string         `json:"name"`
	StartTime    time.Time      `json:"startTime"`
	EndTime      time.Time      `json:"endTime"`
	CodeChecksum string         `json:"codeChecksum"`
	Container    string         `json:"container"`
	Conda        string         `json:"conda"`
	Params       map[string]any `json:"params"`
	Inputs       []string       `json:"inputs"`
	Outputs      []string       `json:"outputs"`
}

// fileOutput is the "FileOutput" record kind (design D8): a file entity,
// local (Path) or external (URL), with its size and the engine's checksum.
type fileOutput struct {
	Type     string   `json:"type"`
	Path     string   `json:"path"`
	URL      string   `json:"url"`
	Role     string   `json:"role"`
	Size     int64    `json:"size"`
	Checksum checksum `json:"checksum"`
}

type checksum struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

// valueOutput is the "WorkflowOutput"/"TaskOutput" record kind (design D8):
// a non-file output value.
type valueOutput struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// mapRun builds the format-neutral importer.Run for the WorkflowRun record
// at id, loading its tasks (and their inputs/outputs) from the store.
func (s *store) mapRun(id string, data json.RawMessage) (importer.Run, error) {
	var wf workflowRun
	if err := json.Unmarshal(data, &wf); err != nil {
		return importer.Run{}, fmt.Errorf("parse workflow run %s: %w", id, err)
	}

	run := importer.Run{
		ID:      id,
		Name:    wf.RunName,
		Started: wf.StartTime,
		Ended:   wf.EndTime,
		Engine:  importer.Engine{Name: wf.Engine.Name, Version: wf.Engine.Version},
		Pipeline: importer.Pipeline{
			Name:       wf.RunName,
			Checksum:   wf.ScriptChecksum,
			Repository: wf.Repository,
			Commit:     wf.CommitID,
			Local:      wf.Repository == "" && wf.CommitID == "",
		},
		Params: mapParams(wf.Params),
	}

	for _, lid := range wf.Tasks {
		task, err := s.loadTask(lid)
		if err != nil {
			return importer.Run{}, err
		}
		run.Tasks = append(run.Tasks, task)
	}
	return run, nil
}

// loadTask reads and maps the TaskRun record at lid, resolving its input
// and output record references.
func (s *store) loadTask(lid string) (importer.Task, error) {
	data, typ, err := s.loadRecord(lid)
	if err != nil {
		return importer.Task{}, err
	}
	if typ != "TaskRun" {
		return importer.Task{}, fmt.Errorf("%s: expected a TaskRun record, got %q", lid, typ)
	}
	var tr taskRun
	if err := json.Unmarshal(data, &tr); err != nil {
		return importer.Task{}, fmt.Errorf("parse task %s: %w", lid, err)
	}

	task := importer.Task{
		Name:         tr.Name,
		Started:      tr.StartTime,
		Ended:        tr.EndTime,
		CodeChecksum: tr.CodeChecksum,
		Params:       mapParams(tr.Params),
		Environment:  mapEnvironment(tr),
	}

	for _, inLid := range tr.Inputs {
		f, err := s.loadFile(inLid)
		if err != nil {
			return importer.Task{}, err
		}
		task.Inputs = append(task.Inputs, f)
	}

	for _, outLid := range tr.Outputs {
		data, typ, err := s.loadRecord(outLid)
		if err != nil {
			return importer.Task{}, err
		}
		switch typ {
		case "FileOutput":
			f, err := mapFileOutput(outLid, data)
			if err != nil {
				return importer.Task{}, err
			}
			f.ProducedByTask = tr.Name
			task.Outputs = append(task.Outputs, f)
		case "WorkflowOutput", "TaskOutput":
			v, err := mapValueOutput(outLid, data)
			if err != nil {
				return importer.Task{}, err
			}
			task.Results = append(task.Results, v)
		default:
			return importer.Task{}, fmt.Errorf("%s: unexpected output record type %q", outLid, typ)
		}
	}
	return task, nil
}

// loadFile reads and maps the FileOutput record at lid as an input.
func (s *store) loadFile(lid string) (importer.File, error) {
	data, typ, err := s.loadRecord(lid)
	if err != nil {
		return importer.File{}, err
	}
	if typ != "FileOutput" {
		return importer.File{}, fmt.Errorf("%s: expected a FileOutput record, got %q", lid, typ)
	}
	return mapFileOutput(lid, data)
}

func mapFileOutput(lid string, data json.RawMessage) (importer.File, error) {
	var fo fileOutput
	if err := json.Unmarshal(data, &fo); err != nil {
		return importer.File{}, fmt.Errorf("parse file %s: %w", lid, err)
	}
	return importer.File{
		Path:     fo.Path,
		URL:      fo.URL,
		Size:     fo.Size,
		Checksum: importer.Checksum{Algorithm: fo.Checksum.Algorithm, Value: fo.Checksum.Value},
		Role:     fo.Role,
	}, nil
}

func mapValueOutput(lid string, data json.RawMessage) (importer.Value, error) {
	var vo valueOutput
	if err := json.Unmarshal(data, &vo); err != nil {
		return importer.Value{}, fmt.Errorf("parse output %s: %w", lid, err)
	}
	return importer.Value{Name: vo.Name, Value: vo.Value}, nil
}

// mapParams turns a JSON params object into sorted-by-name Values, so
// mapping is deterministic regardless of Go's JSON object key order.
func mapParams(params map[string]any) []importer.Value {
	if len(params) == 0 {
		return nil
	}
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]importer.Value, len(names))
	for i, name := range names {
		out[i] = importer.Value{Name: name, Value: params[name]}
	}
	return out
}

// mapEnvironment builds the task's Environment from its container or conda
// reference (design D4 "Environment as an input"); a task declares at most
// one of the two.
func mapEnvironment(tr taskRun) *importer.Environment {
	switch {
	case tr.Container != "":
		return &importer.Environment{Kind: "container", ID: "docker://" + tr.Container, Label: tr.Container}
	case tr.Conda != "":
		return &importer.Environment{Kind: "conda", ID: "conda://" + tr.Conda, Label: tr.Conda}
	default:
		return nil
	}
}
