package mirror

import "testing"

func TestChunkPatternForKey(t *testing.T) {
	pattern := chunkPatternForKey("SHA256E-s123--abcdef.txt")
	if pattern != "SHA256E-s123-S*-C*--abcdef.txt" {
		t.Fatalf("chunkPatternForKey: %q", pattern)
	}
	if got := chunkPatternForKey("invalidkey"); got != "" {
		t.Fatalf("expected empty pattern: %q", got)
	}
}

func TestBuildPlanAndCpLine(t *testing.T) {
	opts := planOptions{s3Prefix: "s3://bucket", store: "/store", mode: "auto"}
	plan := buildPlan(opts, []string{"KEY--abc"})
	if len(plan) != 2 {
		t.Fatalf("buildPlan len: %d", len(plan))
	}
	if plan[0] != "cp s3://bucket/KEY--abc /store/" {
		t.Fatalf("plan[0]: %q", plan[0])
	}

	opts.noClobber = true
	line := cpLine(opts, "KEY--abc")
	if line != "cp -n s3://bucket/KEY--abc /store/" {
		t.Fatalf("cpLine: %q", line)
	}
}

func TestBoolHelpers(t *testing.T) {
	if got := flagTag(true, "tag"); got != "tag" {
		t.Fatalf("flagTag: %q", got)
	}
	if got := flagTag(false, "tag"); got != "" {
		t.Fatalf("flagTag false: %q", got)
	}
	if got := boolToInt(true); got != 1 {
		t.Fatalf("boolToInt true: %d", got)
	}
	if got := boolToInt(false); got != 0 {
		t.Fatalf("boolToInt false: %d", got)
	}
}
