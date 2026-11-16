package importer

import "testing"

func threeTaskRun() Run {
	return Run{
		ID:   "lid://run1",
		Name: "happy_curie",
		Tasks: []Task{
			{
				Name:    "align",
				Inputs:  []File{{Path: "data/reads.fq", Role: "reads"}},
				Outputs: []File{{Path: "work/aligned.bam"}},
			},
			{
				Name:    "sort",
				Inputs:  []File{{Path: "work/aligned.bam"}},
				Outputs: []File{{Path: "work/sorted.bam"}},
			},
			{
				Name:    "report",
				Inputs:  []File{{Path: "work/sorted.bam"}},
				Outputs: []File{{Path: "results/report.html", Role: "report"}},
			},
		},
	}
}

func TestAggregate_RunLevel(t *testing.T) {
	plans := Aggregate(threeTaskRun(), false)
	if len(plans) != 1 {
		t.Fatalf("got %d plans, want 1", len(plans))
	}
	plan := plans[0]
	if len(plan.Inputs) != 1 || plan.Inputs[0].Path != "data/reads.fq" {
		t.Errorf("Inputs = %+v, want only data/reads.fq", plan.Inputs)
	}
	if len(plan.Outputs) != 1 || plan.Outputs[0].Path != "results/report.html" {
		t.Errorf("Outputs = %+v, want only results/report.html", plan.Outputs)
	}
}

func TestAggregate_Tasks(t *testing.T) {
	plans := Aggregate(threeTaskRun(), true)
	if len(plans) != 3 {
		t.Fatalf("got %d plans, want 3", len(plans))
	}
	if plans[0].Name != "align" || plans[1].Name != "sort" || plans[2].Name != "report" {
		t.Errorf("plan names = %q, %q, %q", plans[0].Name, plans[1].Name, plans[2].Name)
	}
	// The intermediate file is a task input/output, not stripped.
	if plans[1].Inputs[0].Path != "work/aligned.bam" {
		t.Errorf("sort input = %q, want work/aligned.bam", plans[1].Inputs[0].Path)
	}
}

func TestAggregate_EnvironmentsDeduped(t *testing.T) {
	env := &Environment{Kind: "container", ID: "docker://img@sha256:abcd"}
	run := Run{
		Tasks: []Task{
			{Name: "a", Environment: env},
			{Name: "b", Environment: env},
		},
	}
	plans := Aggregate(run, false)
	if len(plans[0].Environments) != 1 {
		t.Errorf("Environments = %+v, want 1 deduped entry", plans[0].Environments)
	}
}
