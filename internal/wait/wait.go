// Package wait implements bounded pollers for the asynchronous GPU PaaS
// virtual-machine lifecycle (create/update provisioning, imperative actions,
// and destroy). All pollers are driven through a plain Getter func so they can
// be unit-tested without a clientset.
package wait

import (
	"context"
	"fmt"
	"strings"
	"time"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
)

// Getter fetches the current VM (typically clientset GetStatus).
type Getter func(ctx context.Context) (*apiv1.VirtualMachine, error)

const (
	// DefaultInterval is the poll cadence when Options.Interval is unset.
	DefaultInterval = 10 * time.Second
	// DefaultTimeout is the overall bound when Options.Timeout is unset.
	DefaultTimeout = 30 * time.Minute
	// maxTransientRetries bounds the exponential backoff (2^n seconds) applied
	// to consecutive transient getter errors before giving up.
	maxTransientRetries = 5
)

// Options tunes a wait loop.
type Options struct {
	// Interval between polls. Defaults to DefaultInterval.
	Interval time.Duration
	// Timeout bounds the whole wait. Defaults to DefaultTimeout.
	Timeout time.Duration
	// SubmittedAt is when the operation being awaited was submitted. When set,
	// it arms the true-status guard's freshness clause (see waitForTerminal).
	// When zero, a terminal state is accepted without a preceding pending
	// observation — use this for operations the backend may apply
	// synchronously without a visible status transition.
	SubmittedAt time.Time

	// Test seams. Nil values use the real clock.
	sleep func(ctx context.Context, d time.Duration) error
	now   func() time.Time
}

func (o Options) withDefaults() Options {
	if o.Interval <= 0 {
		o.Interval = DefaultInterval
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.sleep == nil {
		o.sleep = realSleep
	}
	if o.now == nil {
		o.now = time.Now
	}
	return o
}

func realSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// NormalizeStatus canonicalizes backend status enum values: the wire encodes
// them as "Status_SUCCESS", "Status_DESTROYPENDING", ... — strip the prefix
// and lowercase so state machines compare stable tokens.
func NormalizeStatus(s string) string {
	return strings.TrimPrefix(strings.ToLower(s), "status_")
}

var (
	pendingStates = map[string]bool{
		"not_deployed": true,
		"submitted":    true,
		"pending":      true,
	}
	createOKStates   = map[string]bool{"success": true}
	createFailStates = map[string]bool{"failed": true}
	actionOKStates   = map[string]bool{"actioncomplete": true}
	actionFailStates = map[string]bool{"actionfailed": true}
)

// WaitForVMReady polls until the VM reaches the create/update terminal state
// SUCCESS (nil error) or FAILED (error carrying the backend reason).
func WaitForVMReady(ctx context.Context, get Getter, opts Options) (*apiv1.VirtualMachine, error) {
	return waitForTerminal(ctx, get, opts, "", createOKStates, createFailStates)
}

// WaitForVMAction polls until the imperative action identified by verb
// (start/stop/reboot/...) reaches ACTIONCOMPLETE (nil error) or ACTIONFAILED
// (error carrying the backend reason).
func WaitForVMAction(ctx context.Context, get Getter, verb string, opts Options) (*apiv1.VirtualMachine, error) {
	return waitForTerminal(ctx, get, opts, verb, actionOKStates, actionFailStates)
}

// waitForTerminal is the shared poll loop.
//
// True-status guard: the backend syncs status back asynchronously, so right
// after submitting an operation the status endpoint may still report the
// PREVIOUS operation's terminal state (e.g. a stale ACTIONCOMPLETE). A
// terminal state is therefore only accepted when it belongs to our operation:
//  1. status.action matches the submitted verb (when a verb was given); AND
//  2. either a pending state was observed first, or status.provisioned_at is
//     newer than Options.SubmittedAt (freshness clause; vacuously true when
//     SubmittedAt is zero or provisioned_at is present and parseable).
//
// Anything that is neither pending nor an accepted terminal keeps polling
// until the timeout.
func waitForTerminal(ctx context.Context, get Getter, opts Options, verb string, okStates, failStates map[string]bool) (*apiv1.VirtualMachine, error) {
	opts = opts.withDefaults()
	deadline := opts.now().Add(opts.Timeout)
	sawPending := false
	lastStatus := "<none>"
	transientErrs := 0

	for {
		vm, err := get(ctx)
		if err != nil {
			transientErrs++
			if transientErrs > maxTransientRetries {
				return nil, fmt.Errorf("polling virtual machine status: %w", err)
			}
			if serr := opts.sleep(ctx, backoff(transientErrs)); serr != nil {
				return nil, serr
			}
			continue
		}
		transientErrs = 0

		st := NormalizeStatus(vm.Status.Status)
		lastStatus = st
		switch {
		case pendingStates[st]:
			sawPending = true
		case okStates[st] || failStates[st]:
			if verbMatches(verb, vm.Status.Action) && (sawPending || provisionedAfter(vm.Status.ProvisionedAt, opts.SubmittedAt)) {
				if failStates[st] {
					return vm, fmt.Errorf("virtual machine reached status %q: %s", st, reasonOr(vm.Status.Reason, "no reason reported"))
				}
				return vm, nil
			}
			// Stale terminal state from a previous operation — keep polling.
		}

		if !opts.now().Before(deadline) {
			return vm, fmt.Errorf("timed out after %s waiting for virtual machine (last status %q)", opts.Timeout, lastStatus)
		}
		if serr := opts.sleep(ctx, opts.Interval); serr != nil {
			return vm, serr
		}
	}
}

// WaitForGone polls until the VM is deleted: a 404 from the getter or a
// DESTROYED status is success; DESTROYFAILED is a terminal error. No
// true-status guard is needed — destroy states are unambiguous.
func WaitForGone(ctx context.Context, get Getter, opts Options) error {
	opts = opts.withDefaults()
	deadline := opts.now().Add(opts.Timeout)
	lastStatus := "<none>"
	transientErrs := 0

	for {
		vm, err := get(ctx)
		if err != nil {
			if gpupaas.IsNotFound(err) {
				return nil
			}
			transientErrs++
			if transientErrs > maxTransientRetries {
				return fmt.Errorf("polling virtual machine deletion: %w", err)
			}
			if serr := opts.sleep(ctx, backoff(transientErrs)); serr != nil {
				return serr
			}
			continue
		}
		transientErrs = 0

		switch st := NormalizeStatus(vm.Status.Status); st {
		case "destroyed":
			return nil
		case "destroyfailed":
			return fmt.Errorf("virtual machine destroy failed: %s", reasonOr(vm.Status.Reason, "no reason reported"))
		default:
			lastStatus = st
		}

		if !opts.now().Before(deadline) {
			return fmt.Errorf("timed out after %s waiting for virtual machine deletion (last status %q)", opts.Timeout, lastStatus)
		}
		if serr := opts.sleep(ctx, opts.Interval); serr != nil {
			return serr
		}
	}
}

// backoff returns the exponential (2^n seconds) delay for the nth consecutive
// transient error.
func backoff(n int) time.Duration {
	return time.Duration(1<<uint(n)) * time.Second
}

func verbMatches(verb, action string) bool {
	if verb == "" {
		return true
	}
	return strings.EqualFold(verb, strings.TrimPrefix(strings.ToLower(action), "action_"))
}

// provisionedAfter reports whether the freshness clause of the true-status
// guard passes. A zero submittedAt disables the clause entirely.
func provisionedAfter(provisionedAt string, submittedAt time.Time) bool {
	if submittedAt.IsZero() {
		return true
	}
	t, err := time.Parse(time.RFC3339, provisionedAt)
	if err != nil {
		return false
	}
	return t.After(submittedAt)
}

func reasonOr(reason, fallback string) string {
	if reason == "" {
		return fallback
	}
	return reason
}
