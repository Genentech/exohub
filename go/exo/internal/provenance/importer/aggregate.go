package importer

import "time"

// ActivityPlan is one Activity the Writer builds: either the whole run
// (default aggregation) or one task (design D7 "--tasks").
type ActivityPlan struct {
	Name         string
	Started      time.Time
	Ended        time.Time
	CodeChecksum string
	Inputs       []File
	Outputs      []File
	Params       []Value
	Results      []Value
	Environments []Environment
}

// Aggregate builds the Activity plan(s) for run (design D7 "Run-level
// aggregation (default)" and "--tasks granularity"). Without perTask,
// aggregation collapses the whole run into one ActivityPlan: inputs are
// files used by any task and not produced by another task of the run;
// outputs are task outputs not consumed by another task of the run;
// params are the run's own parameters; environments are the distinct task
// environments. With perTask, one ActivityPlan per task is returned
// instead, unchanged from the task.
func Aggregate(run Run, perTask bool) []ActivityPlan {
	if perTask {
		plans := make([]ActivityPlan, len(run.Tasks))
		for i, t := range run.Tasks {
			plans[i] = ActivityPlan{
				Name:         t.Name,
				Started:      t.Started,
				Ended:        t.Ended,
				CodeChecksum: t.CodeChecksum,
				Inputs:       t.Inputs,
				Outputs:      t.Outputs,
				Params:       t.Params,
				Results:      t.Results,
				Environments: taskEnvironments(t),
			}
		}
		return plans
	}
	return []ActivityPlan{aggregateRun(run)}
}

func aggregateRun(run Run) ActivityPlan {
	produced := map[string]bool{}
	consumed := map[string]bool{}
	for _, t := range run.Tasks {
		for _, f := range t.Outputs {
			produced[fileKey(f)] = true
		}
	}
	for _, t := range run.Tasks {
		for _, f := range t.Inputs {
			consumed[fileKey(f)] = true
		}
	}

	var inputs, outputs []File
	seenIn, seenOut := map[string]bool{}, map[string]bool{}
	for _, t := range run.Tasks {
		for _, f := range t.Inputs {
			k := fileKey(f)
			if !produced[k] && !seenIn[k] {
				seenIn[k] = true
				inputs = append(inputs, f)
			}
		}
		for _, f := range t.Outputs {
			k := fileKey(f)
			if !consumed[k] && !seenOut[k] {
				seenOut[k] = true
				outputs = append(outputs, f)
			}
		}
	}

	var results []Value
	for _, t := range run.Tasks {
		results = append(results, t.Results...)
	}

	return ActivityPlan{
		Name:         run.Name,
		Started:      run.Started,
		Ended:        run.Ended,
		Inputs:       inputs,
		Outputs:      outputs,
		Params:       run.Params,
		Results:      results,
		Environments: runEnvironments(run),
	}
}

// runEnvironments returns the distinct environments used by run's tasks,
// in order of first appearance (design D7 "Environments = the distinct
// task environments").
func runEnvironments(run Run) []Environment {
	if len(run.Environments) > 0 {
		return run.Environments
	}
	var envs []Environment
	seen := map[string]bool{}
	for _, t := range run.Tasks {
		if t.Environment == nil || seen[t.Environment.ID] {
			continue
		}
		seen[t.Environment.ID] = true
		envs = append(envs, *t.Environment)
	}
	return envs
}

func taskEnvironments(t Task) []Environment {
	if t.Environment == nil {
		return nil
	}
	return []Environment{*t.Environment}
}

// fileKey identifies a File by the path or URL the engine recorded, for
// matching a task's output to another task's input of the same run
// (design D7's run-level aggregation), independent of its eventual content
// id.
func fileKey(f File) string {
	if f.Path != "" {
		return "path:" + f.Path
	}
	return "url:" + f.URL
}
