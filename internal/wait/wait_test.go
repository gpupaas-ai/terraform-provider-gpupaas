package wait

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
)

// step is one scripted getter response: either a VM status or an error.
type step struct {
	status apiv1.VirtualMachineStatus
	err    error
}

func vmStep(status, reason, action, provisionedAt string) step {
	return step{status: apiv1.VirtualMachineStatus{
		Status:        status,
		Reason:        reason,
		Action:        action,
		ProvisionedAt: provisionedAt,
	}}
}

func errStep(err error) step { return step{err: err} }

// scriptedGetter returns each step in order; the last step repeats forever.
func scriptedGetter(t *testing.T, steps []step) (Getter, *int) {
	t.Helper()
	calls := 0
	i := 0
	return func(context.Context) (*apiv1.VirtualMachine, error) {
		calls++
		s := steps[i]
		if i < len(steps)-1 {
			i++
		}
		if s.err != nil {
			return nil, s.err
		}
		return &apiv1.VirtualMachine{Status: s.status}, nil
	}, &calls
}

// fakeClock advances a virtual clock on every sleep so wait loops run
// instantly while timeout arithmetic stays realistic.
type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) options(o Options) Options {
	o.sleep = func(_ context.Context, d time.Duration) error {
		c.t = c.t.Add(d)
		return nil
	}
	o.now = func() time.Time { return c.t }
	return o
}

func TestNormalizeStatus(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Status_SUCCESS":        "success",
		"Status_DESTROYPENDING": "destroypending",
		"Status_ACTIONCOMPLETE": "actioncomplete",
		"SUBMITTED":             "submitted",
		"failed":                "failed",
		"":                      "",
	}
	for in, want := range cases {
		if got := NormalizeStatus(in); got != want {
			t.Errorf("NormalizeStatus(%q) = %q want %q", in, got, want)
		}
	}
}

func TestWaitForVMReadySubmittedThenSuccess(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	get, calls := scriptedGetter(t, []step{
		vmStep("Status_SUBMITTED", "", "", ""),
		vmStep("Status_SUBMITTED", "", "", ""),
		vmStep("Status_SUCCESS", "", "", ""),
	})
	vm, err := WaitForVMReady(context.Background(), get, clk.options(Options{
		SubmittedAt: clk.t,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if NormalizeStatus(vm.Status.Status) != "success" {
		t.Fatalf("returned VM has status %q", vm.Status.Status)
	}
	if *calls != 3 {
		t.Fatalf("expected 3 polls, got %d", *calls)
	}
}

func TestWaitForVMReadyImmediateFailedWithReason(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	get, _ := scriptedGetter(t, []step{
		vmStep("Status_FAILED", "quota exceeded", "", ""),
	})
	// SubmittedAt zero: freshness clause disabled, terminal accepted at once.
	_, err := WaitForVMReady(context.Background(), get, clk.options(Options{}))
	if err == nil {
		t.Fatal("expected error for FAILED status")
	}
	if !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("error should carry backend reason, got: %v", err)
	}
}

func TestWaitForVMReadyPendingObservedBeforeFailed(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	get, _ := scriptedGetter(t, []step{
		vmStep("Status_NOT_DEPLOYED", "", "", ""),
		vmStep("Status_FAILED", "image not found", "", ""),
	})
	_, err := WaitForVMReady(context.Background(), get, clk.options(Options{
		SubmittedAt: clk.t,
	}))
	if err == nil || !strings.Contains(err.Error(), "image not found") {
		t.Fatalf("expected FAILED error with reason, got: %v", err)
	}
}

func TestWaitForVMActionStaleTerminalFirst(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	// Stale ACTIONCOMPLETE from a previous "start" op is reported first, then
	// the pending transition, then the real terminal for our "stop" verb.
	get, calls := scriptedGetter(t, []step{
		vmStep("Status_ACTIONCOMPLETE", "", "start", ""),
		vmStep("Status_ACTIONCOMPLETE", "", "start", ""),
		vmStep("Status_SUBMITTED", "", "stop", ""),
		vmStep("Status_ACTIONCOMPLETE", "", "stop", ""),
	})
	vm, err := WaitForVMAction(context.Background(), get, "stop", clk.options(Options{
		SubmittedAt: clk.t,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vm.Status.Action != "stop" {
		t.Fatalf("accepted terminal for wrong action %q", vm.Status.Action)
	}
	if *calls != 4 {
		t.Fatalf("expected 4 polls, got %d", *calls)
	}
}

func TestWaitForVMActionStaleSameVerbGuardedByFreshness(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	submitted := clk.t
	stale := submitted.Add(-1 * time.Hour).Format(time.RFC3339)
	fresh := submitted.Add(30 * time.Second).Format(time.RFC3339)
	// Same verb both times: only provisioned_at distinguishes the stale
	// terminal from the real one — no pending observation ever happens.
	get, _ := scriptedGetter(t, []step{
		vmStep("Status_ACTIONCOMPLETE", "", "stop", stale),
		vmStep("Status_ACTIONCOMPLETE", "", "stop", stale),
		vmStep("Status_ACTIONCOMPLETE", "", "stop", fresh),
	})
	vm, err := WaitForVMAction(context.Background(), get, "stop", clk.options(Options{
		SubmittedAt: submitted,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vm.Status.ProvisionedAt != fresh {
		t.Fatalf("accepted stale terminal (provisioned_at %q)", vm.Status.ProvisionedAt)
	}
}

func TestWaitForVMActionFailed(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	get, _ := scriptedGetter(t, []step{
		vmStep("Status_SUBMITTED", "", "reboot", ""),
		vmStep("Status_ACTIONFAILED", "hypervisor unreachable", "reboot", ""),
	})
	_, err := WaitForVMAction(context.Background(), get, "reboot", clk.options(Options{
		SubmittedAt: clk.t,
	}))
	if err == nil || !strings.Contains(err.Error(), "hypervisor unreachable") {
		t.Fatalf("expected ACTIONFAILED error with reason, got: %v", err)
	}
}

func TestWaitForVMReadyTimeout(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	get, _ := scriptedGetter(t, []step{
		vmStep("Status_SUBMITTED", "", "", ""),
	})
	_, err := WaitForVMReady(context.Background(), get, clk.options(Options{
		Interval:    10 * time.Second,
		Timeout:     time.Minute,
		SubmittedAt: clk.t,
	}))
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), `"submitted"`) {
		t.Fatalf("timeout error should mention last status, got: %v", err)
	}
}

func TestWaitForVMReadyTransientErrorsRecover(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	boom := errors.New("connection reset")
	get, _ := scriptedGetter(t, []step{
		errStep(boom),
		errStep(boom),
		vmStep("Status_SUBMITTED", "", "", ""),
		vmStep("Status_SUCCESS", "", "", ""),
	})
	vm, err := WaitForVMReady(context.Background(), get, clk.options(Options{
		SubmittedAt: clk.t,
	}))
	if err != nil {
		t.Fatalf("transient errors should be retried, got: %v", err)
	}
	if NormalizeStatus(vm.Status.Status) != "success" {
		t.Fatalf("unexpected final status %q", vm.Status.Status)
	}
}

func TestWaitForVMReadyTransientErrorsExhausted(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	boom := errors.New("connection reset")
	get, calls := scriptedGetter(t, []step{errStep(boom)})
	_, err := WaitForVMReady(context.Background(), get, clk.options(Options{}))
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("expected wrapped transient error after retries, got: %v", err)
	}
	if *calls != maxTransientRetries+1 {
		t.Fatalf("expected %d attempts, got %d", maxTransientRetries+1, *calls)
	}
}

func TestWaitForGone404IsSuccess(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	get, _ := scriptedGetter(t, []step{
		errStep(&gpupaas.APIError{StatusCode: 404, Message: "gone"}),
	})
	if err := WaitForGone(context.Background(), get, clk.options(Options{})); err != nil {
		t.Fatalf("404 during delete wait must be success, got: %v", err)
	}
}

func TestWaitForGoneDestroySequence(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	get, calls := scriptedGetter(t, []step{
		vmStep("Status_DESTROYPENDING", "", "", ""),
		vmStep("Status_DESTROYING", "", "", ""),
		errStep(&gpupaas.APIError{StatusCode: 404, Message: "gone"}),
	})
	if err := WaitForGone(context.Background(), get, clk.options(Options{})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *calls != 3 {
		t.Fatalf("expected 3 polls, got %d", *calls)
	}
}

func TestWaitForGoneDestroyed(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	get, _ := scriptedGetter(t, []step{
		vmStep("Status_DESTROYPENDING", "", "", ""),
		vmStep("Status_DESTROYED", "", "", ""),
	})
	if err := WaitForGone(context.Background(), get, clk.options(Options{})); err != nil {
		t.Fatalf("DESTROYED must be success, got: %v", err)
	}
}

func TestWaitForGoneDestroyFailed(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	get, _ := scriptedGetter(t, []step{
		vmStep("Status_DESTROYPENDING", "", "", ""),
		vmStep("Status_DESTROYFAILED", "volume detach failed", "", ""),
	})
	err := WaitForGone(context.Background(), get, clk.options(Options{}))
	if err == nil || !strings.Contains(err.Error(), "volume detach failed") {
		t.Fatalf("expected DESTROYFAILED error with reason, got: %v", err)
	}
}

func TestWaitForGoneTimeout(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	get, _ := scriptedGetter(t, []step{
		vmStep("Status_DESTROYPENDING", "", "", ""),
	})
	err := WaitForGone(context.Background(), get, clk.options(Options{
		Interval: 10 * time.Second,
		Timeout:  time.Minute,
	}))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}
