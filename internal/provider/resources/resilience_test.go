package resources

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	typed "github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// stubInstances serves compute instances from a script: every Get returns the next result, and the
// last one repeats.
type stubInstances struct {
	typed.ComputeInstanceInterface
	results []stubResult
	calls   int
}

type stubResult struct {
	inst *apiv1.ComputeInstance
	err  error
}

func (s *stubInstances) Get(_ context.Context, _ string, _ gpupaas.GetOptions) (*apiv1.ComputeInstance, error) {
	i := s.calls
	if i >= len(s.results) {
		i = len(s.results) - 1
	}
	s.calls++
	return s.results[i].inst, s.results[i].err
}

var (
	errBoom  = errors.New("boom")
	errGone  = &gpupaas.APIError{StatusCode: 404}
	instDone = &apiv1.ComputeInstance{Status: apiv1.ComputeInstanceStatus{Status: "success"}}
)

func fastPolls(t *testing.T) {
	t.Helper()
	t.Cleanup(SetPollIntervalForTesting(time.Millisecond))
}

func TestWaitForInstanceGone(t *testing.T) {
	fastPolls(t)
	ctx := context.Background()

	t.Run("a missing instance is gone", func(t *testing.T) {
		if err := waitForInstanceGone(ctx, &stubInstances{results: []stubResult{{err: errGone}}}, "c"); err != nil {
			t.Fatalf("got %v, want nil", err)
		}
	})
	t.Run("transient read errors are retried", func(t *testing.T) {
		s := &stubInstances{results: []stubResult{{err: errBoom}, {err: errBoom}, {inst: instDone}, {err: errGone}}}
		if err := waitForInstanceGone(ctx, s, "c"); err != nil {
			t.Fatalf("got %v, want nil after the instance disappeared", err)
		}
		if s.calls != 4 {
			t.Fatalf("polled %d times, want 4", s.calls)
		}
	})
	t.Run("a run of read errors is not success", func(t *testing.T) {
		s := &stubInstances{results: []stubResult{{err: errBoom}}}
		err := waitForInstanceGone(ctx, s, "c")
		if err == nil || !errors.Is(err, errBoom) {
			t.Fatalf("got %v, want an error wrapping the read error", err)
		}
		if s.calls != maxHiddenReadErrors {
			t.Fatalf("gave up after %d reads, want %d", s.calls, maxHiddenReadErrors)
		}
	})
	t.Run("a deadline is not success", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		err := waitForInstanceGone(cctx, &stubInstances{results: []stubResult{{err: errBoom}}}, "c")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want the context error", err)
		}
	})
	t.Run("a failed instance is reported", func(t *testing.T) {
		failed := &apiv1.ComputeInstance{Status: apiv1.ComputeInstanceStatus{Status: "destroyfailed", Reason: "stuck"}}
		err := waitForInstanceGone(ctx, &stubInstances{results: []stubResult{{inst: failed}}}, "c")
		if err == nil {
			t.Fatal("got nil, want the failure")
		}
	})
}

func TestUpdateDoneDoesNotTreatUnreadableInstanceAsDone(t *testing.T) {
	ctx := context.Background()
	running := &apiv1.MKSCluster{Status: apiv1.MKSClusterStatus{Status: "MKS_CLUSTER_STATUS_RUNNING"}}
	want := apiv1.MKSClusterSpec{}

	t.Run("a failed read keeps waiting", func(t *testing.T) {
		done := updateDone(ctx, &stubInstances{results: []stubResult{{err: errBoom}}}, "c", want, false, "")
		if ok, err := done(running); ok || err != nil {
			t.Fatalf("got (%v, %v), want (false, nil)", ok, err)
		}
	})
	t.Run("a run of failed reads gives up", func(t *testing.T) {
		done := updateDone(ctx, &stubInstances{results: []stubResult{{err: errBoom}}}, "c", want, false, "")
		var ok bool
		var err error
		for i := 0; i < maxHiddenReadErrors; i++ {
			ok, err = done(running)
		}
		if ok || err == nil || !errors.Is(err, errBoom) {
			t.Fatalf("got (%v, %v), want an error after %d failed reads", ok, err, maxHiddenReadErrors)
		}
	})
	t.Run("a good read resets the run", func(t *testing.T) {
		s := &stubInstances{results: []stubResult{{err: errBoom}, {err: errBoom}, {inst: &apiv1.ComputeInstance{Status: apiv1.ComputeInstanceStatus{Status: "inprogress"}}}, {err: errBoom}, {inst: instDone}}}
		done := updateDone(ctx, s, "c", want, false, "")
		for i := 0; i < 4; i++ {
			if ok, err := done(running); ok || err != nil {
				t.Fatalf("poll %d: got (%v, %v), want (false, nil)", i, ok, err)
			}
		}
		if ok, err := done(running); !ok || err != nil {
			t.Fatalf("got (%v, %v), want done once the instance settled", ok, err)
		}
	})
	t.Run("no backing instance means the cluster status decides", func(t *testing.T) {
		done := updateDone(ctx, &stubInstances{results: []stubResult{{err: errGone}}}, "c", want, false, "")
		if ok, err := done(running); !ok || err != nil {
			t.Fatalf("got (%v, %v), want done", ok, err)
		}
	})
}

// A failed hidden instance with a new trigger id is the failure of this update, even though the run
// was never seen in progress; with the old trigger id it is the previous run's failure and is waited out.
func TestUpdateDoneDetectsANewRunByItsTriggerID(t *testing.T) {
	ctx := context.Background()
	running := &apiv1.MKSCluster{Status: apiv1.MKSClusterStatus{Status: "MKS_CLUSTER_STATUS_RUNNING"}}
	failed := func(trigger string) *apiv1.ComputeInstance {
		return &apiv1.ComputeInstance{Status: apiv1.ComputeInstanceStatus{
			Status: "failed", Reason: "boom", ProvisionedResource: &apiv1.ComputeProvisionedResource{TriggerID: trigger},
		}}
	}
	want := apiv1.MKSClusterSpec{}

	old := updateDone(ctx, &stubInstances{results: []stubResult{{inst: failed("run-1")}}}, "c", want, true, "run-1")
	if ok, err := old(running); ok || err != nil {
		t.Fatalf("the previous run's failure must be waited out, got (%v, %v)", ok, err)
	}
	fresh := updateDone(ctx, &stubInstances{results: []stubResult{{inst: failed("run-2")}}}, "c", want, true, "run-1")
	if ok, err := fresh(running); ok || err == nil {
		t.Fatalf("a failure of a new run must be reported, got (%v, %v)", ok, err)
	}
}

func TestKeyMayExist(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"found", nil, true},
		{"not found", &gpupaas.APIError{StatusCode: 404}, false},
		{"server error", &gpupaas.APIError{StatusCode: 500}, true},
		{"forbidden", &gpupaas.APIError{StatusCode: 403}, true},
		{"network", errBoom, true},
	}
	for _, c := range cases {
		if got := keyMayExist(c.err); got != c.want {
			t.Errorf("%s: keyMayExist = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestErrorsAsWarnings(t *testing.T) {
	var in diag.Diagnostics
	in.AddError("Run action failed", "refused")
	in.AddWarning("Heads up", "careful")
	out := errorsAsWarnings(in, "not tainted")
	if out.HasError() {
		t.Fatalf("errors were kept: %v", out)
	}
	if len(out) != 2 {
		t.Fatalf("got %d diagnostics, want 2", len(out))
	}
	if out[0].Summary() != "Run action failed" || out[0].Detail() != "refused\n\nnot tainted" {
		t.Fatalf("converted diagnostic = %q / %q", out[0].Summary(), out[0].Detail())
	}
	if out[1].Detail() != "careful" {
		t.Fatalf("warning changed: %q", out[1].Detail())
	}
}

func TestMKSValidationOnlyBlocksNewOrChangedValues(t *testing.T) {
	profile := &apiv1.MKSClusterProfile{
		Metadata: apiv1.ObjectMeta{Name: "p"},
		Spec:     apiv1.MKSClusterProfileSpec{SupportedKubernetesVersions: []string{"1.35", "1.36"}},
	}
	model := func(version string) mksResourceModel {
		return mksResourceModel{Spec: mksSpecModel{ProfileName: types.StringValue("p"), KubernetesVersion: types.StringValue(version)}}
	}
	check := func(plan mksResourceModel, prior *mksResourceModel) diag.Diagnostics {
		var d diag.Diagnostics
		validateMKSAgainstProfileWithPrior(plan, prior, profile, &d)
		return d
	}

	old, fine := model("1.34"), model("1.36")

	if d := check(model("1.34"), nil); !d.HasError() {
		t.Error("create with an unsupported version must fail")
	}
	d := check(model("1.34"), &old)
	if d.HasError() || d.WarningsCount() != 1 {
		t.Errorf("an unchanged unsupported version must only warn, got %v", d)
	}
	if d := check(model("1.34"), &fine); !d.HasError() {
		t.Error("changing to an unsupported version must fail")
	}
	if d := check(model("1.36"), &old); d.HasError() || d.WarningsCount() != 0 {
		t.Errorf("moving to a supported version must be clean, got %v", d)
	}
}

// stubProfiles answers BmProfiles(project).Get from a map keyed by project; a project that has no
// entry answers 404, and one listed in broken answers 500.
type stubProfiles struct {
	typed.Interface
	byProject map[string]*apiv1.BmProfile
	broken    map[string]bool
	asked     []string
}

func (s *stubProfiles) BmProfiles(project string) typed.BmProfileInterface {
	return stubProfileGetter{s, project}
}

type stubProfileGetter struct {
	s       *stubProfiles
	project string
}

func (g stubProfileGetter) Get(_ context.Context, _ string, _ gpupaas.GetOptions) (*apiv1.BmProfile, error) {
	g.s.asked = append(g.s.asked, g.project)
	if g.s.broken[g.project] {
		return nil, &gpupaas.APIError{StatusCode: 500}
	}
	if p := g.s.byProject[g.project]; p != nil {
		return p, nil
	}
	return nil, &gpupaas.APIError{StatusCode: 404}
}

func (stubProfileGetter) List(context.Context, gpupaas.ListOptions) (*apiv1.BmProfileList, error) {
	return nil, errors.New("not used")
}

func TestGetBmProfileFallsBackToTheSystemCatalog(t *testing.T) {
	ctx := context.Background()
	catalog := &apiv1.BmProfile{Metadata: apiv1.ObjectMeta{Name: "sku"}}

	cs := &stubProfiles{byProject: map[string]*apiv1.BmProfile{systemCatalogProject: catalog}}
	got, err := getBmProfile(ctx, cs, "team-a", "sku")
	if err != nil || got != catalog {
		t.Fatalf("got (%v, %v), want the catalog SKU", got, err)
	}
	if len(cs.asked) != 2 || cs.asked[0] != "team-a" || cs.asked[1] != systemCatalogProject {
		t.Fatalf("asked %v, want the project then the catalog", cs.asked)
	}

	// A project's own SKU wins and the catalog is not asked.
	own := &apiv1.BmProfile{Metadata: apiv1.ObjectMeta{Name: "sku"}}
	cs = &stubProfiles{byProject: map[string]*apiv1.BmProfile{"team-a": own, systemCatalogProject: catalog}}
	if got, _ := getBmProfile(ctx, cs, "team-a", "sku"); got != own || len(cs.asked) != 1 {
		t.Fatalf("got %v after %v, want the project's own SKU only", got, cs.asked)
	}

	// Nothing anywhere: the not-found error of the first lookup comes back.
	cs = &stubProfiles{}
	if _, err := getBmProfile(ctx, cs, "team-a", "sku"); !gpupaas.IsNotFound(err) {
		t.Fatalf("got %v, want not found", err)
	}

	// Only a missing SKU falls back: a server error is reported as it is.
	cs = &stubProfiles{broken: map[string]bool{"team-a": true}, byProject: map[string]*apiv1.BmProfile{systemCatalogProject: catalog}}
	if _, err := getBmProfile(ctx, cs, "team-a", "sku"); err == nil || gpupaas.IsNotFound(err) {
		t.Fatalf("got %v, want the server error", err)
	}
	if len(cs.asked) != 1 {
		t.Fatalf("asked %v, want only the project", cs.asked)
	}

	// The system catalog itself is asked once.
	cs = &stubProfiles{}
	_, _ = getBmProfile(ctx, cs, systemCatalogProject, "sku")
	if len(cs.asked) != 1 {
		t.Fatalf("asked %v, want one lookup", cs.asked)
	}
}

func TestListPages(t *testing.T) {
	t.Run("follows the continue marker", func(t *testing.T) {
		var seen []gpupaas.ListOptions
		pages := map[string]struct {
			items []string
			next  string
		}{"": {[]string{"a", "b"}, "2"}, "2": {[]string{"c"}, ""}}
		got, err := listPages(func(o gpupaas.ListOptions) ([]string, string, error) {
			seen = append(seen, o)
			p := pages[o.Continue]
			return p.items, p.next, nil
		})
		if err != nil || len(got) != 3 || got[2] != "c" {
			t.Fatalf("got %v, %v, want a, b, c", got, err)
		}
		if len(seen) != 2 || seen[0].Limit != "100" || seen[1].Continue != "2" {
			t.Fatalf("requests = %+v", seen)
		}
	})
	t.Run("stops on an empty page", func(t *testing.T) {
		calls := 0
		got, _ := listPages(func(gpupaas.ListOptions) ([]string, string, error) {
			calls++
			if calls == 1 {
				return []string{"a"}, "1", nil
			}
			return nil, "2", nil
		})
		if calls != 2 || len(got) != 1 {
			t.Fatalf("calls %d, got %v", calls, got)
		}
	})
	t.Run("stops on a repeated marker", func(t *testing.T) {
		calls := 0
		_, _ = listPages(func(gpupaas.ListOptions) ([]string, string, error) {
			calls++
			return []string{"x"}, "same", nil
		})
		if calls != 2 {
			t.Fatalf("calls = %d, want 2", calls)
		}
	})
	t.Run("is bounded", func(t *testing.T) {
		calls := 0
		_, _ = listPages(func(gpupaas.ListOptions) ([]string, string, error) {
			calls++
			return []string{"x"}, string(rune('a'+calls%26)) + string(rune('a'+calls/26)), nil
		})
		if calls != maxListPages {
			t.Fatalf("calls = %d, want %d", calls, maxListPages)
		}
	})
	t.Run("a first page error is returned, a later one keeps what was read", func(t *testing.T) {
		if _, err := listPages(func(gpupaas.ListOptions) ([]string, string, error) { return nil, "", errBoom }); !errors.Is(err, errBoom) {
			t.Fatalf("got %v, want the error", err)
		}
		calls := 0
		got, err := listPages(func(gpupaas.ListOptions) ([]string, string, error) {
			if calls++; calls == 1 {
				return []string{"a"}, "1", nil
			}
			return nil, "", errBoom
		})
		if err != nil || len(got) != 1 {
			t.Fatalf("got %v, %v, want the first page and no error", got, err)
		}
	})
}

func TestClusterOwning(t *testing.T) {
	clusters := []string{"prod", "prod-east", "other"}
	cases := map[string]string{
		"prod":           "prod",
		"prod-w1":        "prod",
		"prod-east":      "prod-east",
		"prod-east-w1":   "prod-east",
		"prod-east-m0-x": "prod-east",
		"other-m0":       "other",
		"productx":       "",
		"staging-w0":     "",
	}
	for instance, want := range cases {
		if got := clusterOwning(instance, clusters); got != want {
			t.Errorf("clusterOwning(%q) = %q, want %q", instance, got, want)
		}
	}
	if got := clusterOwning("a-b-w0", []string{"", "a-b"}); got != "a-b" {
		t.Errorf("an empty cluster name must be ignored, got %q", got)
	}
}

func nodes(n int) *apiv1.MKSNodeList {
	return &apiv1.MKSNodeList{Items: make([]apiv1.MKSNode, n)}
}

func TestWaitForNodeList(t *testing.T) {
	fastPolls(t)
	script := func(results ...*apiv1.MKSNodeList) (func() *apiv1.MKSNodeList, *int) {
		calls := 0
		return func() *apiv1.MKSNodeList {
			i := calls
			if i >= len(results) {
				i = len(results) - 1
			}
			calls++
			return results[i]
		}, &calls
	}

	t.Run("returns once enough nodes registered", func(t *testing.T) {
		read, _ := script(nodes(1), nodes(1), nodes(2))
		got, err := waitForNodeList(context.Background(), read, "c", 2)
		if err != nil || len(got.Items) != 2 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("an unreadable list is reported, not hidden", func(t *testing.T) {
		read, calls := script(nil)
		_, err := waitForNodeList(context.Background(), read, "c", 2)
		if !errors.Is(err, errNodeListUnreadable) || *calls != 5 {
			t.Fatalf("got %v after %d reads, want errNodeListUnreadable after 5", err, *calls)
		}
	})
	t.Run("a good read resets the run of unreadable ones", func(t *testing.T) {
		read, _ := script(nil, nil, nil, nil, nodes(1), nil, nil, nil, nil, nodes(2))
		if _, err := waitForNodeList(context.Background(), read, "c", 2); err != nil {
			t.Fatalf("got %v, want success", err)
		}
	})
	t.Run("a deadline is its own error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		read, _ := script(nodes(1))
		_, err := waitForNodeList(ctx, read, "c", 2)
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errNodeListUnreadable) {
			t.Fatalf("got %v, want the deadline", err)
		}
	})
}

func TestMKSModelKeepsNodeStatusWhenTheListIsUnreadable(t *testing.T) {
	base := mksResourceModel{Status: mksStatusModel{
		NodeCount: types.Int64Value(2),
		Nodes:     []mksNodeModel{{Hostname: types.StringValue("n1")}, {Hostname: types.StringValue("n2")}},
	}}
	srv := &apiv1.MKSCluster{Metadata: apiv1.ObjectMeta{Name: "c"}, Status: apiv1.MKSClusterStatus{Status: "MKS_CLUSTER_STATUS_RUNNING"}}

	got := mksClusterModelFromServer(base, srv, nil, true)
	if got.Status.NodeCount.ValueInt64() != 2 || len(got.Status.Nodes) != 2 {
		t.Fatalf("a refresh with an unreadable node list lost the node status: %+v", got.Status)
	}
	fresh := mksClusterModelFromServer(base, srv, nodes(3), true)
	if fresh.Status.NodeCount.ValueInt64() != 3 {
		t.Fatalf("a readable list must replace the old status, got %+v", fresh.Status)
	}
	created := mksClusterModelFromServer(base, srv, nil, false)
	if !created.Status.NodeCount.IsNull() {
		t.Fatalf("outside a refresh the node count stays unknown (null), got %+v", created.Status.NodeCount)
	}
}

// The instance behind a deleted object may still read "failed" from its earlier provisioning for a poll or two before
// the teardown starts: that is not a failed teardown. A failure of the destroy action itself is.
func TestWaitForInstanceGoneToleratesAStaleFailedStatusButNotAFailedDestroy(t *testing.T) {
	fastPolls(t)
	ctx := context.Background()
	stale := &apiv1.ComputeInstance{Status: apiv1.ComputeInstanceStatus{Status: "failed", Action: "publish", Reason: "earlier"}}
	s := &stubInstances{results: []stubResult{{inst: stale}, {inst: stale}, {err: errGone}}}
	if err := waitForInstanceGone(ctx, s, "c"); err != nil {
		t.Fatalf("a stale failed status ended the wait: %v", err)
	}
	// stale for good: reported after the allowance
	if err := waitForInstanceGone(ctx, &stubInstances{results: []stubResult{{inst: stale}}}, "c"); err == nil {
		t.Fatal("a failed status that never changes must be reported")
	}
	failedDestroy := &apiv1.ComputeInstance{Status: apiv1.ComputeInstanceStatus{Status: "failed", Action: "destroy", Reason: "stuck"}}
	s = &stubInstances{results: []stubResult{{inst: failedDestroy}, {err: errGone}}}
	if err := waitForInstanceGone(ctx, s, "c"); err == nil || s.calls != 1 {
		t.Fatalf("a failed destroy must be reported at once: calls=%d err=%v", s.calls, err)
	}
}

func TestTruncateCutsByCharacter(t *testing.T) {
	got := truncate(strings.Repeat("é", 10), 4)
	if got != "éééé..." || !utf8.ValidString(got) {
		t.Fatalf("got %q", got)
	}
	if truncate("short", 10) != "short" {
		t.Fatal("a short string must be returned as it is")
	}
}
